package sysinfo

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// userHZ is the kernel's clock ticks per second in /proc, 100 on every
// architecture the image is built for.
const userHZ = 100

// procKey names a process across calls: a pid is reused, a pid and its start time are not.
type procKey struct {
	pid   int
	start uint64
}

// Started records that an Agent of taskID started process pid (its shell, a
// command or a background command). Processes attributes the process, and
// everything it starts, to that Task for as long as it runs. Recording the
// start time means a later process with the same pid is not attributed.
func (s *Sampler) Started(pid int, taskID string) {
	st, ok := parsePidStat(s.read(filepath.Join("proc", strconv.Itoa(pid), "stat")))
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners == nil {
		s.owners = map[procKey]string{}
	}
	s.owners[procKey{pid, st.start}] = taskID
}

// Processes lists the Machine's processes now.
func (s *Sampler) Processes() *aosv1.ProcessesResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	root := s.Root
	if root == "" {
		root = "/"
	}
	names, _ := os.ReadDir(filepath.Join(root, "proc"))
	users := parsePasswd(s.read("etc/passwd"))
	btime := parseBootTime(s.read("proc/stat"))

	type entry struct {
		info *aosv1.ProcessInfo
		stat pidStat
	}
	byPid := map[int]*entry{}
	for _, n := range names {
		pid, err := strconv.Atoi(n.Name())
		if err != nil || !n.IsDir() {
			continue
		}
		dir := filepath.Join("proc", n.Name())
		st, ok := parsePidStat(s.read(filepath.Join(dir, "stat")))
		if !ok {
			continue // it exited
		}
		status := parseStatus(s.read(filepath.Join(dir, "status")))
		info := &aosv1.ProcessInfo{
			Pid: int32(pid), Ppid: int32(st.ppid), Name: st.comm, Command: "[" + st.comm + "]",
			RssBytes: status.rssKB << 10, Confined: status.noNewPrivs,
			StartedAt: timestamppb.New(time.Unix(btime, 0).Add(time.Duration(st.start) * time.Second / userHZ)),
		}
		if u, ok := users[status.uid]; ok {
			info.User = u
		} else {
			info.User = strconv.Itoa(status.uid)
		}
		if cmd := strings.TrimRight(s.read(filepath.Join(dir, "cmdline")), "\x00"); cmd != "" {
			info.Command = strings.ReplaceAll(cmd, "\x00", " ")
		}
		byPid[pid] = &entry{info, st}
	}

	// A process belongs to a Task if it, an ancestor, its process group or its
	// session is a process that Task's Agent started. Owners that exited are forgotten.
	owner := map[int]string{}
	for k, task := range s.owners {
		if e, ok := byPid[k.pid]; ok && e.stat.start == k.start {
			owner[k.pid] = task
		} else {
			delete(s.owners, k)
		}
	}
	taskOf := func(pid int) string {
		for p, hops := pid, 0; p > 0 && hops < len(byPid); hops++ {
			e, ok := byPid[p]
			if !ok {
				break
			}
			for _, candidate := range []int{p, e.stat.pgrp, e.stat.sid} {
				if task, ok := owner[candidate]; ok {
					return task
				}
			}
			p = e.stat.ppid
		}
		return ""
	}

	// CPU use is a rate since the previous call.
	cpu := make(map[procKey]uint64, len(byPid))
	elapsed := now.Sub(s.procsAt).Seconds()
	out := &aosv1.ProcessesResponse{Processes: make([]*aosv1.ProcessInfo, 0, len(byPid))}
	for pid, e := range byPid {
		e.info.TaskId = taskOf(pid)
		key := procKey{pid, e.stat.start}
		ticks := e.stat.utime + e.stat.stime
		cpu[key] = ticks
		if prev, ok := s.procCPU[key]; ok && elapsed > 0 && ticks >= prev {
			e.info.CpuPercent, e.info.CpuKnown = float64(ticks-prev)/userHZ/elapsed*100, true
		}
		out.Processes = append(out.Processes, e.info)
	}
	sort.Slice(out.Processes, func(i, j int) bool { return out.Processes[i].Pid < out.Processes[j].Pid })
	s.procCPU, s.procsAt = cpu, now
	return out
}

type pidStat struct {
	comm                string
	ppid, pgrp, sid     int
	utime, stime, start uint64
}

// parsePidStat reads /proc/<pid>/stat. The command name is in parentheses and
// may itself hold spaces and parentheses, so the fields start after the last ")".
func parsePidStat(data string) (pidStat, bool) {
	open, end := strings.IndexByte(data, '('), strings.LastIndexByte(data, ')')
	if open < 0 || end < open {
		return pidStat{}, false
	}
	f := strings.Fields(data[end+1:])
	if len(f) < 20 {
		return pidStat{}, false
	}
	st := pidStat{comm: data[open+1 : end]}
	var errs [6]error
	st.ppid, errs[0] = strconv.Atoi(f[1])
	st.pgrp, errs[1] = strconv.Atoi(f[2])
	st.sid, errs[2] = strconv.Atoi(f[3])
	st.utime, errs[3] = strconv.ParseUint(f[11], 10, 64)
	st.stime, errs[4] = strconv.ParseUint(f[12], 10, 64)
	st.start, errs[5] = strconv.ParseUint(f[19], 10, 64)
	for _, err := range errs {
		if err != nil {
			return pidStat{}, false
		}
	}
	return st, true
}

type procStatus struct {
	uid        int
	rssKB      uint64
	noNewPrivs bool
}

// parseStatus reads the real uid, VmRSS and NoNewPrivs from /proc/<pid>/status.
func parseStatus(data string) procStatus {
	var st procStatus
	for _, line := range strings.Split(data, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		f := strings.Fields(rest)
		if !ok || len(f) == 0 {
			continue
		}
		switch key {
		case "Uid":
			st.uid, _ = strconv.Atoi(f[0])
		case "VmRSS":
			st.rssKB, _ = strconv.ParseUint(f[0], 10, 64)
		case "NoNewPrivs":
			st.noNewPrivs = f[0] == "1"
		}
	}
	return st
}

// parsePasswd maps uids to user names.
func parsePasswd(data string) map[int]string {
	users := map[int]string{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			if _, seen := users[uid]; !seen {
				users[uid] = f[0]
			}
		}
	}
	return users
}

// parseBootTime reads btime, the boot time in Unix seconds, from /proc/stat.
func parseBootTime(data string) int64 {
	for _, line := range strings.Split(data, "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

package sysinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// proc describes a fake /proc/<pid>.
type proc struct {
	pid, ppid, pgrp, sid, uid int
	comm, cmdline             string
	utime, stime, start       uint64
	rssKB                     int
	nnp                       bool
}

func (p proc) files() map[string]string {
	dir := fmt.Sprintf("proc/%d/", p.pid)
	nnp := 0
	if p.nnp {
		nnp = 1
	}
	return map[string]string{
		// A command name with a space and a parenthesis, as a program may choose.
		dir + "stat": fmt.Sprintf("%d (%s) S %d %d %d 0 -1 4194560 100 0 0 0 %d %d 0 0 20 0 1 0 %d 1000000 %d\n",
			p.pid, p.comm, p.ppid, p.pgrp, p.sid, p.utime, p.stime, p.start, p.rssKB/4),
		dir + "status":  fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\nVmRSS:\t%d kB\nNoNewPrivs:\t%d\n", p.comm, p.uid, p.uid, p.uid, p.uid, p.rssKB, nnp),
		dir + "cmdline": p.cmdline,
	}
}

func machineWith(t *testing.T, procs ...proc) string {
	t.Helper()
	files := map[string]string{
		"proc/stat":  "cpu  1 0 1 10 0 0 0 0 0 0\ncpu0 1 0 1 10 0 0 0 0 0 0\nbtime 1789473600\n",
		"proc/self":  "", // not a process folder
		"etc/passwd": "root:x:0:0:root:/root:/bin/bash\naos:x:1000:1000::/home/aos:/bin/bash\n",
	}
	for _, p := range procs {
		for k, v := range p.files() {
			files[k] = v
		}
	}
	return fakeMachine(t, files)
}

func TestProcessesShowWhoRunsWhatAndForWhichTask(t *testing.T) {
	aosd := proc{pid: 1, ppid: 0, pgrp: 1, sid: 1, uid: 0, comm: "aosd", cmdline: "/usr/local/bin/aosd\x00", start: 10, rssKB: 30000}
	shell := proc{pid: 20, ppid: 1, pgrp: 20, sid: 20, uid: 1000, comm: "bash", cmdline: "bash\x00--rcfile\x00/run/aos/sessions/t_a/rc\x00", start: 500, rssKB: 4000, nnp: true}
	child := proc{pid: 21, ppid: 20, pgrp: 21, sid: 20, uid: 1000, comm: "python3", cmdline: "python3\x00-m\x00http.server\x00", start: 600, utime: 100, rssKB: 12000, nnp: true}
	// A background command whose own parent exited: only its process group links it to its Task.
	orphan := proc{pid: 31, ppid: 1, pgrp: 30, sid: 1, uid: 1000, comm: "sleep", cmdline: "sleep\x00300\x00", start: 700, rssKB: 800, nnp: true}
	user := proc{pid: 40, ppid: 1, pgrp: 40, sid: 40, uid: 1000, comm: "odd (name)", cmdline: "", start: 800, rssKB: 3000}
	// The pid a finished Task used, now taken by an unrelated process.
	reused := proc{pid: 50, ppid: 1, pgrp: 50, sid: 50, uid: 1000, comm: "vim", cmdline: "vim\x00", start: 900, rssKB: 5000}
	root := machineWith(t, aosd, shell, child, orphan, user, proc{pid: 30, ppid: 1, pgrp: 30, sid: 1, uid: 1000, comm: "bash", start: 690, nnp: true},
		proc{pid: 50, ppid: 1, pgrp: 50, sid: 50, uid: 1000, comm: "bash", start: 850, nnp: true})

	clock := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s := &Sampler{Root: root, Now: func() time.Time { return clock }}
	s.Started(20, "t_a")
	s.Started(30, "t_b")
	s.Started(50, "t_old")
	// pid 50 exits and an unrelated process takes the number.
	for k, v := range reused.files() {
		if err := os.WriteFile(filepath.Join(root, k), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := map[int32]string{}
	for _, p := range s.Processes().Processes {
		got[p.Pid] = fmt.Sprintf("%s|%s|%s|%d|%v|%s|%v|%s", p.User, p.Name, p.Command, p.RssBytes>>10, p.Confined, p.TaskId, p.CpuKnown,
			p.StartedAt.AsTime().UTC().Format(time.RFC3339))
	}
	want := map[int32]string{
		1:  `root|aosd|/usr/local/bin/aosd|30000|false||false|2026-09-15T12:00:00Z`,
		20: `aos|bash|bash --rcfile /run/aos/sessions/t_a/rc|4000|true|t_a|false|2026-09-15T12:00:05Z`,
		21: `aos|python3|python3 -m http.server|12000|true|t_a|false|2026-09-15T12:00:06Z`,
		30: `aos|bash|[bash]|0|true|t_b|false|2026-09-15T12:00:06Z`,
		31: `aos|sleep|sleep 300|800|true|t_b|false|2026-09-15T12:00:07Z`,
		40: `aos|odd (name)|[odd (name)]|3000|false||false|2026-09-15T12:00:08Z`,
		50: `aos|vim|vim|5000|false||false|2026-09-15T12:00:09Z`,
	}
	for pid, w := range want {
		if got[pid] != w {
			t.Errorf("pid %d:\n got %s\nwant %s", pid, got[pid], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("listed %d processes, want %d: %v", len(got), len(want), got)
	}

	// A second later python3 has used half a second of CPU time (50 ticks).
	clock = clock.Add(time.Second)
	child.utime += 30
	child.stime += 20
	for k, v := range child.files() {
		if err := os.WriteFile(filepath.Join(root, k), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range s.Processes().Processes {
		switch {
		case p.Pid == 21 && (!p.CpuKnown || p.CpuPercent != 50):
			t.Errorf("python3: CPU %v%% known %v, want 50%%", p.CpuPercent, p.CpuKnown)
		case p.Pid == 20 && (!p.CpuKnown || p.CpuPercent != 0):
			t.Errorf("bash: CPU %v%% known %v, want 0%%", p.CpuPercent, p.CpuKnown)
		}
	}
}

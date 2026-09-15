// Package sysinfo samples the Machine for Activity Monitor (PLAN.md §4.3):
// CPU, memory, disks and network, only when asked, so an idle aosd samples
// nothing. Inside a container /proc shows the whole Host VM, so the Machine's
// own cgroup (v2) is read first, and /proc when there is none.
package sysinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
)

// Sampler reads the Machine's metrics. CPU use is a rate, so it keeps the
// previous reading; the first call reports it as not yet known.
type Sampler struct {
	// Root is where proc and sys are; "" means "/". Tests point it at a folder.
	Root string
	// Disks are the folders whose file systems are reported.
	Disks []string
	Now   func() time.Time

	mu   sync.Mutex
	prev cpuSample
}

type cpuSample struct {
	ok  bool
	at  time.Time
	cg  bool   // from the cgroup's cpu.stat rather than /proc/stat
	use uint64 // cgroup: CPU time used, in µs
	// /proc/stat: busy and total time, in clock ticks.
	busy, total uint64
}

func (s *Sampler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// read returns a file under Root, or "" when it cannot be read.
func (s *Sampler) read(rel string) string {
	root := s.Root
	if root == "" {
		root = "/"
	}
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

// Metrics samples the Machine now.
func (s *Sampler) Metrics() *aosv1.MetricsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	m := &aosv1.MetricsResponse{}

	// CPU: the Machine's share, measured since the previous call.
	busy, total, cpus, _ := parseProcStat(s.read("proc/stat"))
	m.Cpus = float64(cpus)
	if quota, ok := parseCPUMax(s.read("sys/fs/cgroup/cpu.max")); ok {
		m.Cpus = quota
	}
	cur := cpuSample{ok: true, at: now, busy: busy, total: total}
	cur.use, cur.cg = parseCPUUsage(s.read("sys/fs/cgroup/cpu.stat"))
	if p := s.prev; p.ok && p.cg == cur.cg {
		elapsed := now.Sub(p.at).Microseconds()
		switch {
		case cur.cg && elapsed > 0 && m.Cpus > 0 && cur.use >= p.use:
			m.CpuPercent, m.CpuKnown = percent(float64(cur.use-p.use)/(float64(elapsed)*m.Cpus)), true
		case !cur.cg && cur.total > p.total && cur.busy >= p.busy:
			m.CpuPercent, m.CpuKnown = percent(float64(cur.busy-p.busy)/float64(cur.total-p.total)), true
		}
	}
	s.prev = cur

	// Memory: the cgroup's use against its limit, or the Host's when unlimited.
	totalKB, availKB, _ := parseMeminfo(s.read("proc/meminfo"))
	m.MemoryTotalBytes, m.MemoryUsedBytes = totalKB<<10, (totalKB-min(availKB, totalKB))<<10
	if used, ok := parseCgroupValue(s.read("sys/fs/cgroup/memory.current")); ok {
		m.MemoryUsedBytes = used
		if limit, ok := parseCgroupValue(s.read("sys/fs/cgroup/memory.max")); ok && (limit < m.MemoryTotalBytes || m.MemoryTotalBytes == 0) {
			m.MemoryTotalBytes = limit
		}
	}

	for _, d := range s.Disks {
		if du, ok := disk(d); ok {
			m.Disks = append(m.Disks, du)
		}
	}
	m.NetRxBytes, m.NetTxBytes = parseNetDev(s.read("proc/net/dev"))
	return m
}

func percent(share float64) float64 { return min(max(share*100, 0), 100) }

// disk reports the file system that holds path.
func disk(path string) (*aosv1.DiskUsage, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return nil, false
	}
	bs := uint64(st.Bsize)
	return &aosv1.DiskUsage{Path: path, TotalBytes: st.Blocks * bs, UsedBytes: (st.Blocks - st.Bfree) * bs, AvailableBytes: st.Bavail * bs}, true
}

// parseProcStat reads /proc/stat: the busy and total time of the aggregate
// "cpu" line, in clock ticks, and how many CPUs are listed. Guest time is
// already counted in user time, so it is left out.
func parseProcStat(data string) (busy, total uint64, cpus int, ok bool) {
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case f[0] == "cpu":
			var idle uint64
			for i, v := range f[1:min(len(f), 9)] { // user nice system idle iowait irq softirq steal
				n, err := strconv.ParseUint(v, 10, 64)
				if err != nil {
					return 0, 0, 0, false
				}
				total += n
				if i == 3 || i == 4 {
					idle += n
				}
			}
			busy, ok = total-idle, true
		case strings.HasPrefix(f[0], "cpu"):
			cpus++
		}
	}
	return busy, total, cpus, ok
}

// parseMeminfo reads MemTotal and MemAvailable, in kB.
func parseMeminfo(data string) (totalKB, availKB uint64, ok bool) {
	var seen int
	for _, line := range strings.Split(data, "\n") {
		key, rest, found := strings.Cut(line, ":")
		f := strings.Fields(rest)
		if !found || len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			totalKB, seen = n, seen+1
		case "MemAvailable":
			availKB, seen = n, seen+1
		}
	}
	return totalKB, availKB, seen == 2
}

// parseNetDev adds up the bytes received and sent on every interface but
// loopback. A long interface name runs into its colon ("gretap0:").
func parseNetDev(data string) (rx, tx uint64) {
	for _, line := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		f := strings.Fields(rest)
		if !ok || strings.TrimSpace(name) == "lo" || len(f) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(f[0], 10, 64)
		t, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 == nil && err2 == nil {
			rx, tx = rx+r, tx+t
		}
	}
	return rx, tx
}

// parseCPUMax reads a cgroup's cpu.max ("quota period", or "max period" for
// no limit) as a number of CPUs.
func parseCPUMax(data string) (float64, bool) {
	f := strings.Fields(data)
	if len(f) != 2 || f[0] == "max" {
		return 0, false
	}
	quota, err1 := strconv.ParseFloat(f[0], 64)
	period, err2 := strconv.ParseFloat(f[1], 64)
	if err1 != nil || err2 != nil || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

// parseCPUUsage reads usage_usec from a cgroup's cpu.stat.
func parseCPUUsage(data string) (uint64, bool) {
	for _, line := range strings.Split(data, "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// parseCgroupValue reads a one-number cgroup file; "max" means no limit.
func parseCgroupValue(data string) (uint64, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(data), 10, 64)
	return n, err == nil
}

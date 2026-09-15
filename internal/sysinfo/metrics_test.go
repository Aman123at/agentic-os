package sysinfo

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Samples of the files as the ui image has them (Docker Desktop, cgroup v2).
const (
	procStat = "cpu  9322 0 2739 638749 859 0 1078 0 0 0\ncpu0 1121 0 380 79656 122 0 532 0 0 0\ncpu1 1014 0 358 79959 147 0 209 0 0 0\nintr 1\n"
	meminfo  = "MemTotal:        8124776 kB\nMemFree:         7000000 kB\nMemAvailable:    7161696 kB\n"
	netDev   = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:    5000       4    0    0    0     0          0         0     5000       4    0    0    0     0       0          0
gretap0:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0:     388       4    0    0    0     0          0         0       42       1    0    0    0     0       0          0
`
)

func TestParsersReadTheMachinesFiles(t *testing.T) {
	busy, total, cpus, ok := parseProcStat(procStat)
	if !ok || total != 652747 || busy != 13139 || cpus != 2 {
		t.Errorf("/proc/stat: busy %d total %d cpus %d ok %v", busy, total, cpus, ok)
	}
	if tot, avail, ok := parseMeminfo(meminfo); !ok || tot != 8124776 || avail != 7161696 {
		t.Errorf("meminfo: %d %d %v", tot, avail, ok)
	}
	if rx, tx := parseNetDev(netDev); rx != 388 || tx != 42 {
		t.Errorf("net/dev without loopback: rx %d tx %d, want 388 42", rx, tx)
	}
	for data, want := range map[string]float64{"max 100000\n": 0, "150000 100000\n": 1.5, "junk": 0} {
		if got, _ := parseCPUMax(data); got != want {
			t.Errorf("cpu.max %q: %v, want %v", data, got, want)
		}
	}
	if n, ok := parseCPUUsage("usage_usec 16907\nuser_usec 8453\n"); !ok || n != 16907 {
		t.Errorf("cpu.stat: %d %v", n, ok)
	}
	if _, ok := parseCgroupValue("max\n"); ok {
		t.Error(`memory.max "max" read as a limit`)
	}
	if n, ok := parseCgroupValue("2531328\n"); !ok || n != 2531328 {
		t.Errorf("memory.current: %d %v", n, ok)
	}
}

// fakeMachine writes the files a Sampler reads under a temp root.
func fakeMachine(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTheSamplerReadsTheMachinesCgroupFirst(t *testing.T) {
	root := fakeMachine(t, map[string]string{
		"proc/stat": procStat, "proc/meminfo": meminfo, "proc/net/dev": netDev,
		"sys/fs/cgroup/cpu.max":        "200000 100000\n", // 2 CPUs
		"sys/fs/cgroup/cpu.stat":       "usage_usec 1000000\n",
		"sys/fs/cgroup/memory.current": "104857600\n",  // 100 MiB
		"sys/fs/cgroup/memory.max":     "1073741824\n", // 1 GiB
	})
	clock := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s := &Sampler{Root: root, Disks: []string{root, filepath.Join(root, "missing")}, Now: func() time.Time { return clock }}

	m := s.Metrics()
	if m.CpuKnown || m.Cpus != 2 || m.MemoryUsedBytes != 100<<20 || m.MemoryTotalBytes != 1<<30 || m.NetRxBytes != 388 || m.NetTxBytes != 42 {
		t.Errorf("first sample: %+v", m)
	}
	if len(m.Disks) != 1 || m.Disks[0].Path != root || m.Disks[0].TotalBytes == 0 || m.Disks[0].AvailableBytes > m.Disks[0].TotalBytes {
		t.Errorf("disks (a missing one is left out): %+v", m.Disks)
	}

	// Two seconds later the Machine has used two seconds of CPU time: half of its two CPUs.
	clock = clock.Add(2 * time.Second)
	if err := os.WriteFile(filepath.Join(root, "sys/fs/cgroup/cpu.stat"), []byte("usage_usec 3000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := s.Metrics(); !m.CpuKnown || math.Abs(m.CpuPercent-50) > 1e-9 {
		t.Errorf("second sample: CPU %v%% known %v, want 50%%", m.CpuPercent, m.CpuKnown)
	}
}

func TestWithoutACgroupTheSamplerUsesProc(t *testing.T) {
	root := fakeMachine(t, map[string]string{"proc/stat": "cpu  100 0 0 900 0 0 0 0 0 0\ncpu0 100 0 0 900 0 0 0 0 0 0\n", "proc/meminfo": meminfo})
	s := &Sampler{Root: root}
	if m := s.Metrics(); m.CpuKnown || m.Cpus != 1 || m.MemoryTotalBytes != 8124776<<10 || m.MemoryUsedBytes != (8124776-7161696)<<10 {
		t.Errorf("first sample: %+v", m)
	}
	// 100 more ticks, 50 of them busy.
	if err := os.WriteFile(filepath.Join(root, "proc/stat"), []byte("cpu  150 0 0 950 0 0 0 0 0 0\ncpu0 150 0 0 950 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := s.Metrics(); !m.CpuKnown || m.CpuPercent != 50 {
		t.Errorf("second sample: CPU %v%% known %v, want 50%%", m.CpuPercent, m.CpuKnown)
	}
}

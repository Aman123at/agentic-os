package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/amantiwari/agentic-os/internal/sysinfo"
)

func TestMetricsServeTheSamplersReading(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"proc/stat":    "cpu  1 0 1 10 0 0 0 0 0 0\ncpu0 1 0 1 10 0 0 0 0 0 0\n",
		"proc/meminfo": "MemTotal: 2048 kB\nMemAvailable: 1024 kB\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Sampler: &sysinfo.Sampler{Root: root}}).Handler())

	rec := call(t, h, "/aos.v1.SystemService/Metrics", `{}`)
	var m struct {
		Cpus             float64
		CpuKnown         bool
		MemoryTotalBytes string // protojson writes uint64 as a string
		MemoryUsedBytes  string
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Cpus != 1 || m.CpuKnown ||
		m.MemoryTotalBytes != "2097152" || m.MemoryUsedBytes != "1048576" {
		t.Errorf("Metrics: %d %s", rec.Code, rec.Body)
	}
}

func TestProcessesServeTheSamplersList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc/7"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"proc/7/stat":    "7 (sleep) S 1 7 7 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 0 0\n",
		"proc/7/status":  "Uid:\t1000\t1000\t1000\t1000\nVmRSS:\t800 kB\nNoNewPrivs:\t1\n",
		"proc/7/cmdline": "sleep\x00300\x00",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Sampler: &sysinfo.Sampler{Root: root}}).Handler())

	rec := call(t, h, "/aos.v1.SystemService/Processes", `{}`)
	var r struct {
		Processes []struct {
			Pid      int
			Command  string
			Confined bool
			RssBytes string
		}
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &r) != nil || len(r.Processes) != 1 ||
		r.Processes[0].Pid != 7 || r.Processes[0].Command != "sleep 300" || !r.Processes[0].Confined || r.Processes[0].RssBytes != "819200" {
		t.Errorf("Processes: %d %s", rec.Code, rec.Body)
	}
}

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

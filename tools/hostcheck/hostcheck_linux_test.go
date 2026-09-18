package hostcheck

import (
	"context"
	"os"
	"testing"

	"github.com/Aman123at/agentic-os/internal/sandbox"
)

func TestMain(m *testing.M) {
	// The test binary re-executes itself as the sandbox helper.
	sandbox.RunHelperIfRequested()
	os.Exit(m.Run())
}

// TestHostCheck runs every M0 check inside the Machine image. `go run ./tools/ci`
// cross-compiles this test and runs it in a container as root.
func TestHostCheck(t *testing.T) {
	if os.Getenv("AOS_INTEGRATION") == "" {
		t.Skip("set AOS_INTEGRATION=1 and run as root inside the Machine image")
	}
	// aosd prepares the home layout at start; this container runs no aosd.
	opts := DefaultOptions()
	notes, err := sandbox.PrepareHome(opts.Layout, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		t.Log(n)
	}
	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	report.Write(os.Stdout)
	for _, r := range report {
		if r.Status == Fail {
			t.Errorf("%s %s: %s", r.ID, r.Name, r.Detail)
		}
	}
}

package hostcheck

import (
	"context"
	"os"
	"testing"

	"github.com/amantiwari/agentic-os/internal/sandbox"
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
	report, err := Run(context.Background(), DefaultOptions())
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

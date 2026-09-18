package daemon

import (
	"context"
	"testing"

	"github.com/Aman123at/agentic-os/internal/config"
)

// replayAtBoot runs the Install Ledger on a Compose install and skips it on a
// native one (ADR-0003, M6.9). The gate is the native marker filesystem: host.
func TestReplayRunsOnComposeButNotOnANativeInstall(t *testing.T) {
	for _, tc := range []struct {
		name       string
		filesystem string
		wantReplay bool
	}{
		{"compose", "home", true},
		{"native", "host", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			d := &Daemon{
				cfg:    config.Config{Filesystem: tc.filesystem},
				replay: func(context.Context) error { ran = true; return nil },
			}
			d.replayAtBoot(context.Background())
			if ran != tc.wantReplay {
				t.Errorf("%s install: Replay ran = %v, want %v", tc.name, ran, tc.wantReplay)
			}
		})
	}
}

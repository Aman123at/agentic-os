// Restart as an operation (PLAN.md §18 M7.3). Kept here with no build constraint
// so the mechanism selection is unit-testable on any platform, not only the
// Linux build that assembles the Daemon.
package daemon

import (
	"log"
	"os/exec"
)

// Restarter brings aosd back after the Restart RPC has replied (M7.3). The RPC
// records the request and returns; the teardown happens afterwards, out of band,
// so the reply always reaches the caller before the process goes down. The
// normal M6.2 drain runs in between, so running Tasks stop cleanly and are
// marked interrupted, not lost.
type Restarter interface {
	// Restart initiates the restart and returns at once, without blocking on the
	// teardown. An error means the restart could not be started, so aosd stays up
	// and the RPC reports it.
	Restart() error
}

// systemdRestarter restarts a native install through systemd. `systemctl restart
// --no-block aos` enqueues the restart as one job and returns immediately, so the
// unit's own KillMode=mixed stop does not kill this caller mid-reply; systemd
// then SIGTERMs aosd (the usual drain) and starts a fresh process within
// RestartSec, with a new boot_id.
type systemdRestarter struct {
	run func(name string, args ...string) error
}

func (s systemdRestarter) Restart() error {
	return s.run("systemctl", "restart", "--no-block", "aos")
}

// composeRestarter restarts a Compose install. There is no systemd, so aosd
// drains and exits on its own and compose.yaml's `restart: unless-stopped` brings
// the container back with a new boot_id. drain triggers the Daemon's normal
// shutdown and must not block, so the reply can flush before the drain closes
// the socket.
type composeRestarter struct {
	drain func()
}

func (c composeRestarter) Restart() error {
	c.drain()
	return nil
}

// restarterFor picks the restart mechanism by install shape (M7.3): systemd on a
// native install, Compose otherwise. run executes a command (nil uses the real
// one) and drain triggers the Daemon's shutdown for the Compose path.
func restarterFor(native bool, run func(name string, args ...string) error, drain func()) Restarter {
	if native {
		if run == nil {
			run = runCommand
		}
		return systemdRestarter{run: run}
	}
	return composeRestarter{drain: drain}
}

// runCommand runs a command, sending its output to the aosd log.
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = log.Writer(), log.Writer()
	return cmd.Run()
}

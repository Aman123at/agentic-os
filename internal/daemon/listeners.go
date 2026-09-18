package daemon

import (
	"net"
	"os"
)

// bindListeners opens the sockets aosd serves on: always the root-only control
// socket, and — in ui Mode only — the public TCP listener. cli Mode has no
// Desktop, so it opens no TCP port at all (PLAN.md §18 M6.4): the whole API is
// reachable only over the Unix socket, guarded by the caller's uid. Both are
// bound before the Daemon signals readiness, so `systemctl start` cannot race
// the socket (M6.2). The TCP listener is nil in cli Mode; callers skip it.
func bindListeners(mode, tcpAddr, socketPath string) (tcp, unix net.Listener, err error) {
	_ = os.Remove(socketPath)
	unix, err = net.Listen("unix", socketPath)
	if err != nil {
		return nil, nil, err
	}
	// The control socket is a local root API on a VPS, so it is 0600 and its guard
	// also checks the caller's uid (api.Auth.SocketUID); it used to be 0666 and
	// unauthenticated, harmless only inside a one-user container (M6.2).
	if err := os.Chmod(socketPath, 0o600); err != nil {
		unix.Close()
		return nil, nil, err
	}
	if mode != "ui" {
		return nil, unix, nil
	}
	if tcp, err = net.Listen("tcp", tcpAddr); err != nil {
		unix.Close()
		return nil, nil, err
	}
	return tcp, unix, nil
}

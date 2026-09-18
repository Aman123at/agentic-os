package daemon

import (
	"context"
	"time"
)

// drainTimeout bounds a graceful shutdown. It sits inside the unit's
// TimeoutStopSec=60s (M6.2) so aosd finishes its own drain before systemd
// escalates to SIGKILL.
const drainTimeout = 45 * time.Second

// gracefulShutdown closes aosd in the order the VPS needs (M6.2): the public
// front door on :7700 stops accepting first, running Tasks are then drained,
// and the control socket closes last — so `aos` on the socket and the Desktop
// keep working right up until the Tasks have settled. A stuck Task cannot stall
// past drainTimeout, well within TimeoutStopSec.
//
// Each step is a closure so the sequence is the single source of truth for the
// ordering (shutdown_test.go pins it): stopFront drains and closes the TCP
// server, drainTasks waits for running Tasks to settle, closeControl closes the
// Unix socket server.
func gracefulShutdown(stopFront, drainTasks, closeControl func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	stopFront(ctx)
	drainTasks(ctx)
	closeControl(ctx)
}

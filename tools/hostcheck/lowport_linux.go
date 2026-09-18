package hostcheck

import (
	"context"
	"os"
	"strings"

	"github.com/Aman123at/agentic-os/internal/sandbox"
)

// bind80 binds and listens on port 80 without root.
const bind80 = `python3 -c 'import socket; s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(("0.0.0.0", 80)); s.listen(); print("bound")'`

// checkLowPorts is prototype M0.7: unprivileged Services can listen below 1024.
func checkLowPorts(ctx context.Context, m *machine, rec recorder) {
	start, _ := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	rec.add("ip_unprivileged_port_start", Pass, "%s", strings.TrimSpace(string(start)))

	exit, out := m.user(ctx, bind80)
	rec.check("User Session binds port 80", exit == 0, "%s", describe(exit, out))

	if sandbox.ABI() >= 1 {
		rs, err := sandbox.Plan(m.opts.Layout.Policy(), sandbox.RootFS())
		if err != nil {
			rec.add("plan ruleset", Fail, "%v", err)
			return
		}
		exit, out, confined := m.confined(ctx, rs, bind80)
		rec.check("Agent Session binds port 80", confined && exit == 0, "%s", describe(exit, out))
	}
}

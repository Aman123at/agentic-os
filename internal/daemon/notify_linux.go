package daemon

import (
	"net"
	"os"
)

// sdNotify sends one datagram to systemd's notification socket (the sd_notify
// protocol). Type=notify holds `systemctl start aos` — and so install.sh —
// until aosd sends READY=1, which it does only once both listeners are up, so
// nothing can race the socket into existence (M6.2). It is a no-op when
// NOTIFY_SOCKET is unset (Compose, tests, a foreground run), so callers need
// not check where they run.
func sdNotify(state string) {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return
	}
	// An abstract socket is named with a leading '@'.
	name := addr
	if name[0] == '@' {
		name = "\x00" + name[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(state))
}

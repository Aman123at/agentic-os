package api

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type peerKey struct{}

// peer is the process on the other end of a Unix socket connection, checked when it connected.
type peer struct {
	pid, uid   int
	noNewPrivs bool
	err        error
}

// SocketConnContext records the connecting process's credentials (SO_PEERCRED).
// Use it as the http.Server's ConnContext for the Unix socket.
func SocketConnContext(ctx context.Context, c net.Conn) context.Context {
	p := peer{err: fmt.Errorf("not a Unix socket")}
	if uc, ok := c.(*net.UnixConn); ok {
		p = peerOf(uc)
	}
	return context.WithValue(ctx, peerKey{}, p)
}

func peerOf(uc *net.UnixConn) peer {
	raw, err := uc.SyscallConn()
	if err != nil {
		return peer{err: err}
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) { cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return peer{err: err}
	}
	if credErr != nil {
		return peer{err: credErr}
	}
	nnp, err := noNewPrivs(int(cred.Pid))
	return peer{pid: int(cred.Pid), uid: int(cred.Uid), noNewPrivs: nnp, err: err}
}

// noNewPrivs reads NoNewPrivs from /proc/<pid>/status: 1 means Agent-confined.
func noNewPrivs(pid int) (bool, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "NoNewPrivs:"); ok {
			return strings.TrimSpace(v) != "0", nil
		}
	}
	return false, fmt.Errorf("no NoNewPrivs in /proc/%d/status", pid)
}

// Socket wraps the handler served on /run/aos/aosd.sock (PLAN.md §7.5): the
// local CLI needs no token, but Agent-confined processes are refused, so an
// Agent can never approve its own Approvals, and — since the socket is a local
// root API on a VPS (M6.2) — so is any caller not running as aosd's own uid.
// refused is told about each refusal.
func (a *Auth) Socket(next http.Handler, refused func(r *http.Request, pid int, reason string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := r.Context().Value(peerKey{}).(peer)
		reason := ""
		switch {
		case !ok || p.err != nil:
			reason = "could not identify the calling process"
		case p.noNewPrivs:
			reason = "the caller is an Agent-confined process"
		case p.uid != a.SocketUID:
			reason = "the caller does not run as the Machine's owner"
		}
		if reason != "" {
			if refused != nil {
				refused(r, p.pid, reason)
			}
			http.Error(w, "aosd refuses requests from Agents: "+reason, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, withActor(r, "user:cli"))
	})
}

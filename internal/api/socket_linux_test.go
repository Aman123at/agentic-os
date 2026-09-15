package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// The Unix socket's guard (PLAN.md §7.5): the in-container CLI is let through,
// and a process with no_new_privs set, as every Agent Session is, is refused, so
// an Agent can never approve its own Approvals.

// dialAsAgentEnv makes the test binary a helper that sets no_new_privs, as the
// Agent sandbox does, then calls the socket and prints the status code.
const dialAsAgentEnv = "AOS_TEST_DIAL_AS_AGENT"

func TestMain(m *testing.M) {
	if sock := os.Getenv(dialAsAgentEnv); sock != "" {
		if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
			fmt.Fprintln(os.Stderr, "prctl:", err)
			os.Exit(2)
		}
		code, err := getOverSocket(sock)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Print(code)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func getOverSocket(sock string) (int, error) {
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	resp, err := c.Get("http://aosd/probe")
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func TestTheSocketRefusesAgentConfinedCallers(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "aosd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	refusedPid, reason := 0, ""
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{
		Handler: (&Auth{}).Socket(next, func(_ *http.Request, pid int, why string) {
			mu.Lock()
			defer mu.Unlock()
			refusedPid, reason = pid, why
		}),
		ConnContext: SocketConnContext,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// This process is let through, unless it already runs with no_new_privs.
	confined, err := noNewPrivs(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	want := http.StatusOK
	if confined {
		want = http.StatusForbidden
	}
	if code, err := getOverSocket(sock); err != nil || code != want {
		t.Fatalf("a call from this process: %d %v, want %d (no_new_privs=%v)", code, err, want, confined)
	}

	// A process that set no_new_privs, as the Agent sandbox does, is refused.
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), dialAsAgentEnv+"="+sock)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != fmt.Sprint(http.StatusForbidden) {
		t.Fatalf("an Agent-confined caller got %s, want %d", got, http.StatusForbidden)
	}
	mu.Lock()
	defer mu.Unlock()
	if refusedPid != cmd.Process.Pid || !strings.Contains(reason, "Agent-confined") {
		t.Errorf("refusal reported pid %d (%q), want the helper's pid %d", refusedPid, reason, cmd.Process.Pid)
	}
}

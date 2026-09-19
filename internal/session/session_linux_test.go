package session

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A real bash on a PTY (PLAN.md §10): each command's output is framed exactly,
// with its exit code, and viewers get the recent output, then what follows.

func startUserSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	s, err := Start(Options{Dir: dir, Home: dir, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func runIn(t *testing.T, s *Session, command string) Result {
	t.Helper()
	c, err := s.Run(command)
	if err != nil {
		t.Fatalf("Run(%q): %v", command, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.Wait(ctx, 0)
	if err != nil {
		t.Fatalf("Wait(%q): %v", command, err)
	}
	return res
}

func TestASessionFramesEachCommandsOutputAndExitCode(t *testing.T) {
	s := startUserSession(t)
	for _, tc := range []struct {
		command, output string
		exit            int
	}{
		{`printf 'hello\n'`, "hello\r\n", 0},
		{`printf 'no newline'; false`, "no newline", 1},
		{`(exit 42)`, "", 42},
		{`cd /; printf '%s\n' "$PWD" >&2`, "/\r\n", 0},
	} {
		res := runIn(t, s, tc.command)
		if string(res.Output) != tc.output || res.ExitCode != tc.exit {
			t.Errorf("%s: output=%q exit=%d, want %q exit %d", tc.command, res.Output, res.ExitCode, tc.output, tc.exit)
		}
	}
	if got := s.Cwd(); got != "/" {
		t.Errorf("the shell's folder is %q after cd /, want /", got)
	}
}

func TestAViewerReplaysRecentOutputThenFollowsUntilTheShellEnds(t *testing.T) {
	s := startUserSession(t)
	// printf's format keeps the marker out of the echoed command line.
	runIn(t, s, `printf 'marker-%s\n' early`)

	recent, output, stop := s.Watch()
	defer stop()
	if !bytes.Contains(recent, []byte("marker-early")) {
		t.Fatalf("a new viewer's replay lacks earlier output: %q", recent)
	}

	runIn(t, s, `printf 'marker-%s\n' live`)
	var seen []byte
	timeout := time.After(5 * time.Second)
	for !bytes.Contains(seen, []byte("marker-live")) {
		select {
		case chunk := <-output:
			seen = append(seen, chunk...)
		case <-timeout:
			t.Fatalf("the viewer did not see new output: %q", seen)
		}
	}

	_ = s.Close()
	select {
	case <-s.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("Exited was not closed after Close")
	}
	for range output { // closed once the shell has exited
	}

	// A viewer who arrives after the end still gets the last output, and nothing more.
	recent, output, _ = s.Watch()
	if !bytes.Contains(recent, []byte("marker-live")) {
		t.Errorf("a late viewer's replay lacks the last output: %q", recent)
	}
	if _, open := <-output; open {
		t.Error("a late viewer's channel should already be closed")
	}
}

// TestUserSessionCredentialsPerRealm covers the M7.5 Terminal identity: the User
// option sets USER/LOGNAME and picks the prompt, so the Root Realm's Session
// reads as root with the red-`#` prompt, and the Standard one as aos. It does not
// setuid (that needs root), so it exercises the environment and prompt the Realm
// selects, not the kernel credential.
func TestUserSessionCredentialsPerRealm(t *testing.T) {
	if got := promptFor("root"); !strings.Contains(got, "root@") || !strings.Contains(got, "#") {
		t.Errorf("root prompt = %q, want a root@…# prompt", got)
	}
	if got := promptFor(""); got != `'aos$ '` {
		t.Errorf("default prompt = %q, want 'aos$ '", got)
	}

	dir := t.TempDir()
	s, err := Start(Options{Dir: dir, Home: dir, User: "root", UID: uint32(os.Getuid()), GID: uint32(os.Getgid())})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if res := runIn(t, s, `printf '%s|%s\n' "$USER" "$LOGNAME"`); string(res.Output) != "root|root\r\n" {
		t.Errorf("USER|LOGNAME = %q, want root|root", res.Output)
	}
	rc, err := os.ReadFile(dir + "/bashrc")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rc, []byte(`root@`)) || bytes.Contains(rc, []byte(`PS1='aos$ '`)) {
		t.Errorf("root Session rc does not carry the root prompt:\n%s", rc)
	}
}

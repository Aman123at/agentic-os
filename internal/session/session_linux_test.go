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

// TestASessionStartsInTheFolderAskedFor covers M8.6: Code's Terminal panel
// opens in the Workspace, and the shell changes into it itself, so a folder
// that is missing or not a folder leaves it in the home folder.
func TestASessionStartsInTheFolderAskedFor(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	file := work + "/a-file"
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ cwd, want string }{
		{work, work},
		{work + "/missing", home},
		{file, home},
		{"", home},
	} {
		s, err := Start(Options{Dir: t.TempDir(), Home: home, Cwd: tc.cwd, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())})
		if err != nil {
			t.Fatalf("Start(Cwd %q): %v", tc.cwd, err)
		}
		if got := s.StartedIn(); got != tc.want {
			t.Errorf("Cwd %q: the shell started in %q, want %q", tc.cwd, got, tc.want)
		}
		if res := runIn(t, s, `printf '%s|%s\n' "$PWD" "${AOS_CWD-unset}"`); string(res.Output) != tc.want+"|unset\r\n" {
			t.Errorf("Cwd %q: PWD|AOS_CWD = %q, want %s|unset", tc.cwd, res.Output, tc.want)
		}
		_ = s.Close()
	}
}

// TestASessionNeverStartsInAFolderItsUserCannotEnter is the check as the
// Realm's user: a folder only root may enter leaves an aos shell at home.
func TestASessionNeverStartsInAFolderItsUserCannotEnter(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root to start a shell as another user")
	}
	const nobody = 65534
	// Not t.TempDir: its parent is 0700, so nobody could not reach even home.
	mk := func(mode os.FileMode) string {
		d, err := os.MkdirTemp("", "aos-cwd-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(d) })
		if err := os.Chmod(d, mode); err != nil {
			t.Fatal(err)
		}
		return d
	}
	home, dir, private := mk(0o755), mk(0o755), mk(0o700)
	s, err := Start(Options{Dir: dir, Home: home, Cwd: private, UID: nobody, GID: nobody})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if got := s.StartedIn(); got != home {
		t.Errorf("a shell as nobody started in %q, want its home %q: root's 0700 folder must not be entered", got, home)
	}
}

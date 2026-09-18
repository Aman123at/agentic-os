// Package e2e checks PLAN.md §18's M1 and M2 acceptance criteria against the
// cli Machine started with `docker compose up --build`. Recorded model
// conversations (testdata/cassettes) stand in for OpenAI, and every check
// reads the Machine's state, never the Agent's claims.
//
// Run it with `go run ./tools/ci e2e`, or AOS_E2E=1 go test ./tools/e2e.
// AOS_E2E_KEEP=1 leaves the Machines running for a look afterwards.
package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

const key = "/home/aos/.ssh/id_ed25519"

func TestM1Acceptance(t *testing.T) {
	if os.Getenv("AOS_E2E") == "" {
		t.Skip("set AOS_E2E=1 to run against Docker: go run ./tools/ci e2e")
	}
	// A Compose project and Host port apart from the user's own Machine.
	m := startMachine(t, "aos-e2e", "7793")
	t.Run("DownloadAndExtract", m.downloadAndExtract)
	t.Run("ProtectedDeleteAsksForApproval", m.protectedDeleteAsks)
	t.Run("ScriptDeleteIsBlockedByTheKernel", m.scriptDeleteIsBlocked)
	t.Run("Cancel", m.cancel)
}

// `aos run "download <url> and extract it to ~/Downloads/x"` succeeds.
func (m *machine) downloadAndExtract(t *testing.T) {
	m.sh(t, "root", `mkdir -p /srv/e2e/src/docs && printf 'hello from e2e\n' > /srv/e2e/src/hello.txt && printf 'nested\n' > /srv/e2e/src/docs/readme.md && tar -czf /srv/e2e/x.tar.gz -C /srv/e2e/src .`)
	if out, err := m.compose(context.Background(), "exec", "-d", "aos", "python3", "-m", "http.server", "8099", "--bind", "127.0.0.1", "--directory", "/srv/e2e").CombinedOutput(); err != nil {
		t.Fatalf("starting the file server: %v\n%s", err, out)
	}
	eventually(t, 30*time.Second, "the file server answers", func() bool {
		_, code := m.run(t, "root", "curl", "-sfo", "/dev/null", "http://127.0.0.1:8099/x.tar.gz")
		return code == 0
	})

	out, code := m.aos(t, "run", "download http://127.0.0.1:8099/x.tar.gz and extract it to ~/Downloads/x")
	if code != 0 {
		t.Fatalf("aos run: exit %d\n%s", code, out)
	}
	got := m.sh(t, "aos", "cat ~/Downloads/x/hello.txt ~/Downloads/x/docs/readme.md && stat -c %U ~/Downloads/x.tar.gz ~/Downloads/x/hello.txt")
	if got != "hello from e2e\nnested\naos\naos\n" {
		t.Errorf("the extracted files and their owner: %q", got)
	}
	m.wantAudit(t, taskID(t, out), row{"create_task", "allow", ""}, row{"download", "allow", "autonomy"}, row{"run_command", "allow", "autonomy"})
}

// A delete inside a Protected Path triggers an Approval in the terminal.
func (m *machine) protectedDeleteAsks(t *testing.T) {
	m.sh(t, "aos", "printf 'e2e-key\\n' > "+key+" && chmod 600 "+key)

	s := m.tty(t, "run", "e2e-approval: delete my SSH key")
	shown := s.waitFor("choice:", time.Minute)
	for _, want := range []string{"Approval needed:", "id_ed25519", "Protected Path"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the Approval prompt lacks %q:\n%s", want, shown)
		}
	}
	s.send("d")
	if code := s.exit(time.Minute); code != 0 {
		t.Fatalf("aos run after deny: exit %d\n%s", code, s.shown())
	}
	denied := taskID(t, s.shown())
	if got := m.sh(t, "aos", "cat "+key); got != "e2e-key\n" {
		t.Errorf("after deny, the key reads %q", got)
	}

	// Allowed once, the call runs with the sandbox widened to the key: it moves
	// to the Trash, from where it comes back.
	s = m.tty(t, "run", "e2e-approval: delete my SSH key")
	s.waitFor("choice:", time.Minute)
	s.send("a")
	if code := s.exit(time.Minute); code != 0 {
		t.Fatalf("aos run after allow: exit %d\n%s", code, s.shown())
	}
	allowed := taskID(t, s.shown())
	if out, code := m.run(t, "aos", "test", "-e", key); code == 0 {
		t.Errorf("after allow, %s still exists %s", key, out)
	}
	list, _ := m.aos(t, "trash", "list")
	id := trashID(list, key)
	if id == "" {
		t.Fatalf("the Trash does not list %s:\n%s", key, list)
	}
	if out, code := m.aos(t, "trash", "restore", id); code != 0 {
		t.Fatalf("aos trash restore %s: exit %d\n%s", id, code, out)
	}
	if got := m.sh(t, "aos", "cat "+key); got != "e2e-key\n" {
		t.Errorf("the restored key reads %q", got)
	}

	m.wantAudit(t, denied, row{"delete", "deny", "user"})
	m.wantAudit(t, allowed, row{"delete", "allow", "user"})
}

// The same delete attempted by a script is blocked by the kernel.
func (m *machine) scriptDeleteIsBlocked(t *testing.T) {
	m.sh(t, "aos", "printf 'e2e-key\\n' > "+key)

	out, code := m.aos(t, "run", "e2e-script: delete my SSH key with Python")
	if code != 0 {
		t.Fatalf("aos run: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Permission denied") {
		t.Errorf("the script's failure is not shown:\n%s", out)
	}
	if got := m.sh(t, "aos", "cat "+key); got != "e2e-key\n" {
		t.Errorf("after the script, the key reads %q", got)
	}
	// Policy let the call through, so the refusal came from the kernel: the
	// same user outside an Agent Session may change the folder.
	m.wantAudit(t, taskID(t, out), row{"run_command", "allow", "autonomy"})
	m.sh(t, "aos", "touch ~/.ssh/e2e-probe && rm ~/.ssh/e2e-probe")
}

// Cancelling works, with aos cancel and with Ctrl-C in the Task's terminal.
func (m *machine) cancel(t *testing.T) {
	var out syncBuffer
	cmd := m.compose(context.Background(), "exec", "-T", "aos", "aos", "run", "e2e-cancel: wait a long time")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	m.waitForSleep(t, true)
	id := taskID(t, out.String())
	if o, code := m.aos(t, "cancel", id); code != 0 {
		t.Fatalf("aos cancel: exit %d\n%s", code, o)
	}
	if code := waitExit(t, cmd, 30*time.Second); code != 130 {
		t.Errorf("aos run exited %d after aos cancel, want 130\n%s", code, out.String())
	}
	m.waitForSleep(t, false)
	if show, _ := m.aos(t, "show", id); !strings.Contains(show, "cancelled") {
		t.Errorf("aos show %s:\n%s", id, show)
	}
	m.wantAudit(t, id, row{"cancel_task", "allow", ""}, row{"run_command", "allow", "autonomy"})

	s := m.tty(t, "run", "e2e-cancel: wait a long time")
	m.waitForSleep(t, true)
	s.send("\x03")
	if code := s.exit(30 * time.Second); code != 130 {
		t.Errorf("aos run exited %d after Ctrl-C, want 130\n%s", code, s.shown())
	}
	m.waitForSleep(t, false)
}

// ---------------------------------------------------------------- the Machine

type machine struct {
	project, port string
	root          string   // the repository
	dir           string   // scratch folder: cassettes, Compose files
	env           []string // Compose variables, instead of the repository's .env
	files         []string // Compose files after compose.yaml
}

func startMachine(t *testing.T, project, port string) *machine {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// A temporary folder, not the repository: Docker Desktop on macOS can't
	// mount from ~/Desktop, ~/Documents or ~/Downloads without extra access.
	dir, err := os.MkdirTemp("", "aos-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	m := &machine{project: project, port: port, root: root, dir: dir, files: []string{filepath.Join(dir, "compose.e2e.yaml")}}
	if err := os.MkdirAll(filepath.Join(dir, "cassettes"), 0o755); err != nil {
		t.Fatal(err)
	}
	cassettes, err := filepath.Glob(filepath.Join("testdata", "cassettes", "*.json"))
	if err != nil || len(cassettes) == 0 {
		t.Fatalf("no cassettes in testdata/cassettes: %v", err)
	}
	for _, c := range cassettes {
		b, err := os.ReadFile(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cassettes", filepath.Base(c)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	override := fmt.Sprintf("services:\n  aos:\n    environment:\n      AOS_FAKE_MODEL: /e2e/cassettes\n    volumes:\n      - %s:/e2e/cassettes:ro\n", filepath.Join(dir, "cassettes"))
	if err := os.WriteFile(filepath.Join(dir, "compose.e2e.yaml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	// An explicit env file replaces the repository's .env, so neither the
	// user's API key nor their settings reach the test Machine.
	m.env = []string{"AOS_MODE=cli", "AOS_PORT=" + m.port, "AOS_BIND=127.0.0.1",
		"AOS_AUTONOMY=confirm-risky", "OPENAI_API_KEY=sk-e2e-dummy-not-a-real-key"}
	if err := os.WriteFile(filepath.Join(dir, "e2e.env"), []byte(strings.Join(m.env, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			out, _ := m.compose(context.Background(), "logs", "--tail", "60", "aos").CombinedOutput()
			t.Logf("aosd log:\n%s", out)
		}
		if os.Getenv("AOS_E2E_KEEP") != "" {
			t.Logf("the Machine keeps running as Compose project %s; scratch folder %s", m.project, dir)
			return
		}
		if out, err := m.compose(context.Background(), "down", "-v", "--remove-orphans").CombinedOutput(); err != nil {
			t.Logf("docker compose down: %v\n%s", err, out)
		}
		_ = os.RemoveAll(dir)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if out, err := m.compose(ctx, "up", "--build", "-d").CombinedOutput(); err != nil {
		t.Fatalf("docker compose up --build: %v\n%s", err, lastLines(string(out), 40))
	}
	eventually(t, time.Minute, "aosd answers", func() bool {
		_, code := m.aos(t, "tasks")
		return code == 0
	})
	return m
}

func (m *machine) compose(ctx context.Context, args ...string) *exec.Cmd {
	base := []string{"compose", "-p", m.project, "--env-file", filepath.Join(m.dir, "e2e.env"), "-f", filepath.Join(m.root, "compose.yaml")}
	for _, f := range m.files {
		base = append(base, "-f", f)
	}
	cmd := exec.CommandContext(ctx, "docker", append(base, args...)...)
	cmd.Dir = m.root
	cmd.Env = append(os.Environ(), m.env...)
	return cmd
}

// run runs a command in the Machine as user and returns its output and exit code.
func (m *machine) run(t *testing.T, user string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := m.compose(ctx, append([]string{"exec", "-T", "-u", user, "aos"}, args...)...).CombinedOutput()
	code := exitCode(err)
	if code < 0 {
		t.Fatalf("docker compose exec %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), code
}

// aos runs the CLI the way the README does: docker compose exec aos aos ….
func (m *machine) aos(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return m.run(t, "root", append([]string{"aos"}, args...)...)
}

// sh runs a shell script in the Machine and fails the test if it fails.
func (m *machine) sh(t *testing.T, user, script string) string {
	t.Helper()
	out, code := m.run(t, user, "bash", "-c", script)
	if code != 0 {
		t.Fatalf("%s (as %s): exit %d\n%s", script, user, code, out)
	}
	return out
}

// waitForSleep waits until an Agent's `sleep 300` runs, or until none is left.
func (m *machine) waitForSleep(t *testing.T, running bool) {
	t.Helper()
	what := "the Agent's command runs"
	if !running {
		what = "the Agent's command is gone"
	}
	eventually(t, 30*time.Second, what, func() bool {
		_, code := m.run(t, "root", "pgrep", "-x", "sleep")
		return (code == 0) == running
	})
}

type row struct{ tool, decision, by string }

// wantAudit fails unless the Task's Audit Log has each row; an empty by matches any decider.
func (m *machine) wantAudit(t *testing.T, taskID string, rows ...row) {
	t.Helper()
	// A cancelled Task reaches its final state as soon as the user asks, while
	// the Agent is still unwinding (PLAN.md M4.8 item 8.12), so the last rows can
	// land a moment after `aos run` returns. Poll until they all do.
	var out string
	deadline := time.Now().Add(30 * time.Second)
	for {
		o, code := m.aos(t, "audit", taskID)
		if code != 0 {
			t.Fatalf("aos audit %s: exit %d\n%s", taskID, code, o)
		}
		out = o
		missing := false
		for _, r := range rows {
			missing = missing || !auditRow(r).MatchString(out)
		}
		if !missing || time.Now().After(deadline) {
			break // report what is still missing below, with the rows in hand
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, r := range rows {
		if !auditRow(r).MatchString(out) {
			t.Errorf("the Audit Log of %s has no %s row decided %s by %q:\n%s", taskID, r.tool, r.decision, r.by, out)
		}
	}
}

func auditRow(r row) *regexp.Regexp {
	by := `\S+`
	if r.by != "" {
		by = regexp.QuoteMeta(r.by)
	}
	return regexp.MustCompile(`(?m)\s` + regexp.QuoteMeta(r.tool) + `\s+` + regexp.QuoteMeta(r.decision) + `\s+` + by + `(\s|$)`)
}

// ---------------------------------------------------------------- terminals

// tty runs aos in the Machine on a terminal, as a person would.
type tty struct {
	t    *testing.T
	f    *os.File
	mu   sync.Mutex
	out  bytes.Buffer
	done chan struct{}
	code int
}

func (m *machine) tty(t *testing.T, args ...string) *tty {
	t.Helper()
	cmd := m.compose(context.Background(), append([]string{"exec", "aos", "aos"}, args...)...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 200})
	if err != nil {
		t.Fatal(err)
	}
	s := &tty{t: t, f: f, done: make(chan struct{})}
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			s.mu.Lock()
			s.out.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		select { // the last output
		case <-read:
		case <-time.After(2 * time.Second):
		}
		_ = f.Close()
		s.code = exitCode(err)
		close(s.done)
	}()
	t.Cleanup(func() {
		select {
		case <-s.done:
		default:
			_ = cmd.Process.Kill()
		}
	})
	return s
}

var terminalControls = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\r`)

// shown is what the terminal shows so far, without styles.
func (s *tty) shown() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return terminalControls.ReplaceAllString(s.out.String(), "")
}

func (s *tty) waitFor(text string, within time.Duration) string {
	s.t.Helper()
	deadline := time.Now().Add(within)
	for {
		shown := s.shown()
		if strings.Contains(shown, text) {
			return shown
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("the terminal never showed %q; it shows:\n%s", text, shown)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *tty) send(keys string) {
	s.t.Helper()
	if _, err := s.f.Write([]byte(keys)); err != nil {
		s.t.Fatal(err)
	}
}

// exit waits for aos to exit and returns its exit code.
func (s *tty) exit(within time.Duration) int {
	s.t.Helper()
	select {
	case <-s.done:
		return s.code
	case <-time.After(within):
		s.t.Fatalf("aos did not exit within %s; the terminal shows:\n%s", within, s.shown())
		return 0
	}
}

// ---------------------------------------------------------------- helpers

var taskLine = regexp.MustCompile(`Task (t_[0-9a-f]+)`)

func taskID(t *testing.T, out string) string {
	t.Helper()
	m := taskLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no Task id in:\n%s", out)
	}
	return m[1]
}

// trashID finds the Trash item deleted from path in `aos trash list`.
func trashID(list, path string) string {
	for _, line := range strings.Split(list, "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.HasSuffix(strings.TrimSpace(line), path) {
			return f[0]
		}
	}
	return ""
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting until %s", within, what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func waitExit(t *testing.T, cmd *exec.Cmd, within time.Duration) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return exitCode(err)
	case <-time.After(within):
		_ = cmd.Process.Kill()
		t.Fatalf("aos did not exit within %s", within)
		return 0
	}
}

// exitCode is a command's exit code, or -1 if it did not run.
func exitCode(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitCode()
	}
	return -1
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// syncBuffer is a bytes.Buffer that a command's output goroutines can share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

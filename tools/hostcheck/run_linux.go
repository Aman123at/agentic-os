package hostcheck

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/amantiwari/agentic-os/internal/sandbox"
)

// Options describes the Machine under test.
type Options struct {
	Home   string // the aos user's home folder
	Shared string // the Shared Folder mount
	User   string // the Machine's unprivileged user
	// AosdAddr is aosd's listen address inside the Machine, used by the forwarding check.
	AosdAddr string
	// Commands is the number of commands in the Session framing corpus.
	Commands int
}

// DefaultOptions matches the Machine image.
func DefaultOptions() Options {
	return Options{Home: "/home/aos", Shared: "/home/aos/Shared", User: "aos", AosdAddr: "127.0.0.1:7700", Commands: 1000}
}

// Run executes every check. It must run as root inside the Machine; it only
// modifies temporary .aos-hostcheck-* fixtures, which it removes afterwards.
func Run(ctx context.Context, opts Options) (Report, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("the host check must run as root inside the Machine: docker compose exec aos aos doctor --host-check")
	}
	u, err := user.Lookup(opts.User)
	if err != nil {
		return nil, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	m := &machine{opts: opts, uid: uint32(uid), gid: uint32(gid), tag: ".aos-hostcheck-" + randomHex()}

	var report Report
	checkLandlock(ctx, m, recorder{"0.1", &report})
	checkFraming(ctx, m, recorder{"0.2", &report})
	checkSecret(recorder{"0.4", &report})
	checkForwarding(ctx, m, recorder{"0.5", &report})
	checkLowPorts(ctx, m, recorder{"0.7", &report})
	return report, nil
}

type machine struct {
	opts     Options
	uid, gid uint32
	tag      string
}

func (m *machine) env() []string {
	return []string{
		"HOME=" + m.opts.Home, "USER=" + m.opts.User, "LANG=C.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
}

// agent runs script with bash as the Machine's user, confined to rs.
func (m *machine) agent(ctx context.Context, rs sandbox.Ruleset, script string) (int, string) {
	cmd, err := sandbox.Command(rs, m.uid, m.gid, m.env(), "bash", "-c", script)
	if err != nil {
		return -1, err.Error()
	}
	return m.run(ctx, cmd)
}

// confinedMarker is printed by the confined shell before the script runs.
const confinedMarker = "__aos_confined__"

// confined runs script like agent and also reports whether the confined shell
// actually started, so a sandbox start-up failure never counts as a denial.
func (m *machine) confined(ctx context.Context, rs sandbox.Ruleset, script string) (int, string, bool) {
	exit, out := m.agent(ctx, rs, "echo "+confinedMarker+"; "+script)
	rest, ok := strings.CutPrefix(out, confinedMarker)
	return exit, strings.TrimSpace(rest), ok
}

// user runs script with bash as the Machine's user, unconfined (a User Session).
func (m *machine) user(ctx context.Context, script string) (int, string) {
	return m.run(ctx, m.userCmd(script))
}

func (m *machine) userCmd(script string) *exec.Cmd {
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = m.env()
	cmd.SysProcAttr = credential(m.uid, m.gid)
	return cmd
}

func (m *machine) run(ctx context.Context, cmd *exec.Cmd) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd.Dir = "/"
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return -1, err.Error()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), strings.TrimSpace(out.String())
		}
		if err != nil {
			return -1, err.Error()
		}
		return 0, strings.TrimSpace(out.String())
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return -1, "timed out"
	}
}

func randomHex() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// shellQuote quotes s for bash.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func describe(exit int, out string) string {
	return fmt.Sprintf("exit %d: %s", exit, truncate(out, 160))
}

// recorder collects results for one prototype.
type recorder struct {
	id      string
	results *Report
}

func (rec recorder) add(name string, status Status, detail string, args ...any) {
	*rec.results = append(*rec.results, Result{ID: rec.id, Name: name, Status: status, Detail: fmt.Sprintf(detail, args...)})
}

// known records a check whose failure is a documented design finding.
func (rec recorder) known(finding, name string, ok bool, detail string, args ...any) {
	if ok {
		rec.add(name, Pass, detail, args...)
		return
	}
	rec.add(name, Known, "finding "+finding+": "+detail, args...)
}

func (rec recorder) check(name string, ok bool, detail string, args ...any) {
	status := Pass
	if !ok {
		status = Fail
	}
	rec.add(name, status, detail, args...)
}

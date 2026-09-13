package hostcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/amantiwari/agentic-os/internal/sandbox"
	"github.com/amantiwari/agentic-os/internal/session"
)

// framingCase is one kind of command in the M0.2 corpus.
type framingCase struct {
	name    string
	command string
	exit    int
	// check validates the output, with CR LF normalised to LF. nil accepts anything.
	check func(out string) bool
	// input, when set, is typed once the command is waiting for input.
	input string
}

func equals(want string) func(string) bool { return func(out string) bool { return out == want } }
func contains(want string) func(string) bool {
	return func(out string) bool { return strings.Contains(out, want) }
}

var longArg = strings.Repeat("a", 100_000)

var framingCorpus = []framingCase{
	{name: "echo", command: "echo hello", check: equals("hello\n")},
	{name: "no trailing newline", command: "printf abc", check: equals("abc")},
	{name: "false", command: "false", exit: 1, check: equals("")},
	{name: "subshell exit code", command: "(exit 42)", exit: 42},
	{name: "stderr", command: "echo oops >&2", check: equals("oops\n")},
	{name: "missing file", command: "ls /nonexistent-aos", exit: 2, check: contains("No such file")},
	{name: "binary output", command: "head -c 4096 /dev/urandom"},
	{name: "NUL bytes", command: `printf 'a\0b\n'`, check: equals("a\x00b\n")},
	{name: "large output", command: "seq 1 50000", check: func(out string) bool {
		return strings.Count(out, "\n") == 50000 && strings.HasSuffix(out, "\n50000\n")
	}},
	{name: "heredoc with tab", command: "cat <<'EOF'\nline1\n\tTAB $HOME\nEOF", check: equals("line1\n\tTAB $HOME\n")},
	{name: "cd persists", command: "cd /tmp && cd / && cd /tmp; pwd", check: equals("/tmp\n")},
	{name: "export persists (set)", command: "export AOS_PROBE=42"},
	{name: "export persists (read)", command: "echo \"$AOS_PROBE\"", check: equals("42\n")},
	{name: "fake markers in output", command: `printf '\033]133;C;aos=deadbeef\007\033]133;D;aos=deadbeef;0\007x\n'`,
		check: equals("\x1b]133;C;aos=deadbeef\x07\x1b]133;D;aos=deadbeef;0\x07x\n")},
	{name: "unicode", command: "echo 'héllo ✓ 日本'", check: equals("héllo ✓ 日本\n")},
	{name: "return from command file", command: "return 3", exit: 3},
	{name: "background job", command: "sleep 0.2 & echo started", check: contains("started\n")},
	{name: "stdout redirected and restored", command: "exec 3>&1 >/dev/null; echo hidden; exec >&3 3>&-", check: equals("")},
	{name: "long command text", command: "echo " + longArg + " | wc -c", check: equals("100001\n")},
	{name: "carriage return", command: `printf 'a\rb\n'`, check: equals("a\rb\n")},
	{name: "ANSI colour", command: `printf '\033[31mred\033[0m\n'`, check: equals("\x1b[31mred\x1b[0m\n")},
	{name: "pipeline exit code", command: "true | false", exit: 1},
	{name: "interactive prompt", command: "read -r -p 'name? ' n; echo \"hi $n\"", input: "bob\r", check: contains("hi bob\n")},
}

// checkFraming is prototype M0.2: Session command framing in an Agent Session.
func checkFraming(ctx context.Context, m *machine, rec recorder) {
	dir := filepath.Join("/run/aos", m.tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		rec.add("create Session directory", Fail, "%v", err)
		return
	}
	defer os.RemoveAll(dir)

	opts := session.Options{Dir: dir, UID: m.uid, GID: m.gid, Home: m.opts.Home}
	if sandbox.ABI() >= 1 {
		rs, err := sandbox.Plan(sandbox.DefaultPolicy(m.opts.Home, m.opts.Shared), sandbox.RootFS())
		if err != nil {
			rec.add("plan ruleset", Fail, "%v", err)
			return
		}
		opts.Confine = &rs
	}
	s, err := session.Start(opts)
	if err != nil {
		rec.add("start Agent Session", Fail, "%v", err)
		return
	}
	defer s.Close()

	var failures []string
	counts := map[string]int{}
	for i := 0; i < m.opts.Commands; i++ {
		fc := framingCorpus[i%len(framingCorpus)]
		counts[fc.name]++
		if err := runCase(ctx, s, fc); err != nil {
			failures = append(failures, fmt.Sprintf("#%d %s: %v", i, fc.name, err))
			if len(failures) >= 5 {
				break
			}
		}
	}
	rec.check(fmt.Sprintf("%d mixed commands framed with correct output and exit codes", m.opts.Commands),
		len(failures) == 0, "%d kinds; %s", len(counts), strings.Join(failures, "; "))
	left, _ := filepath.Glob(filepath.Join(dir, "cmd-*"))
	rec.check("command files are removed once commands finish", len(left) <= 1, "%d left", len(left))

	var latencies []time.Duration
	for i := 0; i < 300; i++ {
		start := time.Now()
		c, err := s.Run("true")
		if err == nil {
			_, err = c.Wait(ctx, 0)
		}
		if err != nil {
			rec.add("framing latency", Fail, "%v", err)
			return
		}
		latencies = append(latencies, time.Since(start))
	}
	slices.Sort(latencies)
	p50, p95 := latencies[len(latencies)/2], latencies[len(latencies)*95/100]
	rec.check("framing overhead p95 < 10 ms", p95 < 10*time.Millisecond, "round trip of `true`: p50 %v, p95 %v", p50.Round(10*time.Microsecond), p95.Round(10*time.Microsecond))
}

func runCase(ctx context.Context, s *session.Session, fc framingCase) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := s.Run(fc.command)
	if err != nil {
		return err
	}
	idle := time.Duration(0)
	if fc.input != "" {
		idle = 300 * time.Millisecond
	}
	res, err := c.Wait(ctx, idle)
	if err != nil {
		return err
	}
	if fc.input != "" {
		if !res.Waiting {
			return fmt.Errorf("finished without waiting for input: %q", res.Output)
		}
		if _, err := s.Run("echo typed-into-the-prompt"); !errors.Is(err, session.ErrBusy) {
			return fmt.Errorf("Run while waiting for input: got %v, want ErrBusy", err)
		}
		if err := s.Input([]byte(fc.input)); err != nil {
			return err
		}
		if res, err = c.Wait(ctx, 0); err != nil {
			return err
		}
	}
	out := string(bytes.ReplaceAll(res.Output, []byte("\r\n"), []byte("\n")))
	if res.ExitCode != fc.exit {
		return fmt.Errorf("exit %d, want %d (output %q)", res.ExitCode, fc.exit, truncate(out, 80))
	}
	if fc.check != nil && !fc.check(out) {
		return fmt.Errorf("unexpected output %q", truncate(out, 120))
	}
	return nil
}

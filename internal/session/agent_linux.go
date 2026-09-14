package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/amantiwari/agentic-os/internal/sandbox"
	"github.com/amantiwari/agentic-os/internal/tool"
)

// AgentConfig configures a Task's Agent Session.
type AgentConfig struct {
	TaskID string
	// Dir is the Session's own directory (/run/aos/sessions/<task>).
	Dir      string
	UID, GID uint32
	Home     string
	// Policy returns the Agent policy; the Session's directory is added to it.
	Policy func() sandbox.Policy
	// Confine is false on Hosts without Landlock: commands still run with no_new_privs.
	Confine bool
	// Env is appended to the Session environment.
	Env []string
	// PathPrefix goes in front of PATH, e.g. "/usr/local/lib/aos/agent-bin:" for the rm shim.
	PathPrefix string
	Outputs    *tool.Outputs
	// Registry, if set, lists the Session for viewers while it runs.
	Registry *Registry
}

const (
	// promptIdle is how long a command must be silent before it may be waiting for input.
	promptIdle = 3 * time.Second
	// killGrace is how long Ctrl-C has before the foreground command is killed.
	killGrace = 5 * time.Second
)

// Agent is a Task's persistent Agent Session. It implements tool.Sessions.
type Agent struct {
	cfg AgentConfig

	mu      sync.Mutex
	sh      *Session
	rs      sandbox.Ruleset
	current *Command
	started time.Time
	typed   []string // what viewers typed since the Agent last looked
	procs   map[string]*process
	nextID  int
}

// NewAgent returns an Agent Session; its shell starts with the first command.
func NewAgent(cfg AgentConfig) *Agent {
	return &Agent{cfg: cfg, procs: map[string]*process{}}
}

var _ tool.Sessions = (*Agent)(nil)

// policy is the Agent policy with this Session's directory writable.
func (a *Agent) policy() sandbox.Policy {
	p := a.cfg.Policy()
	p.Writable = append(p.Writable, a.cfg.Dir)
	return p
}

func (a *Agent) plan(p sandbox.Policy) (sandbox.Ruleset, error) {
	if !a.cfg.Confine {
		return sandbox.Ruleset{}, nil
	}
	return sandbox.Plan(p, sandbox.RootFS())
}

// shell returns a running shell, starting or re-sandboxing it as needed.
// Callers hold a.mu.
func (a *Agent) shell() (*Session, []string, error) {
	var notes []string
	if a.sh != nil {
		select {
		case <-a.sh.Exited():
			a.closeShell()
			notes = append(notes, "The previous shell exited, so a new Session was started: the folder and variables were reset.")
		default:
		}
	}
	restore := ""
	if a.sh != nil && a.cfg.Confine && !a.sh.Busy() {
		if stale, _ := a.rs.Stale(sandbox.RootFS()); stale {
			// Landlock never widens a running process: save state, start a new shell.
			restore = filepath.Join(a.cfg.Dir, "restore")
			if c, err := a.sh.Run("__aos_save " + restore); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = c.Wait(ctx, 0)
				cancel()
			}
			a.closeShell()
		}
	}
	if a.sh != nil {
		return a.sh, notes, nil
	}
	if err := os.MkdirAll(a.cfg.Dir, 0o700); err != nil {
		return nil, notes, err
	}
	if err := os.Chown(a.cfg.Dir, int(a.cfg.UID), int(a.cfg.GID)); err != nil {
		return nil, notes, err
	}
	rs, err := a.plan(a.policy())
	if err != nil {
		return nil, notes, err
	}
	opts := Options{Dir: a.cfg.Dir, UID: a.cfg.UID, GID: a.cfg.GID, Home: a.cfg.Home, Confine: &rs, Env: a.cfg.Env, PathPrefix: a.cfg.PathPrefix}
	if restore != "" {
		opts.Env = append(append([]string{}, opts.Env...), "AOS_RESTORE="+restore)
	}
	sh, err := Start(opts)
	if err != nil {
		return nil, notes, err
	}
	a.sh, a.rs, a.started = sh, rs, time.Now()
	if a.cfg.Registry != nil {
		a.cfg.Registry.Add(&Entry{ID: a.cfg.TaskID, TaskID: a.cfg.TaskID, Agent: true, Session: sh, Created: a.started, input: a.userTyped})
	}
	return sh, notes, nil
}

func (a *Agent) closeShell() {
	if a.sh == nil {
		return
	}
	if a.cfg.Registry != nil {
		a.cfg.Registry.Remove(a.cfg.TaskID, a.sh)
	}
	_ = a.sh.Close()
	a.sh, a.current = nil, nil
}

// userTyped records what a viewer typed into the Session.
func (a *Agent) userTyped(b []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.typed = append(a.typed, string(b))
}

// Run implements tool.Sessions.
func (a *Agent) Run(ctx context.Context, command string, timeout time.Duration) (tool.CommandResult, error) {
	a.mu.Lock()
	sh, notes, err := a.shell()
	if err != nil {
		a.mu.Unlock()
		return tool.CommandResult{}, err
	}
	c, err := sh.Run(command)
	if errors.Is(err, ErrBusy) {
		a.mu.Unlock()
		return tool.CommandResult{}, errors.New("the previous command is still running or waiting for input: answer it with send_input, or stop it with stop_process")
	}
	if err != nil {
		a.mu.Unlock()
		return tool.CommandResult{}, err
	}
	a.current = c
	a.mu.Unlock()
	res, err := a.wait(ctx, sh, c, timeout)
	res.Notes = append(notes, res.Notes...)
	return res, err
}

// Input implements tool.Sessions.
func (a *Agent) Input(ctx context.Context, text string, timeout time.Duration) (tool.CommandResult, error) {
	a.mu.Lock()
	sh, c := a.sh, a.current
	a.mu.Unlock()
	if sh == nil || c == nil || c.finished() {
		return tool.CommandResult{}, errors.New("no command is waiting for input")
	}
	if err := sh.Input([]byte(text)); err != nil {
		return tool.CommandResult{}, err
	}
	return a.wait(ctx, sh, c, timeout)
}

// wait waits for c to finish, to look like it waits for input, or to time out.
func (a *Agent) wait(ctx context.Context, sh *Session, c *Command, timeout time.Duration) (tool.CommandResult, error) {
	start := time.Now()
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		res, err := c.Wait(deadline, promptIdle)
		switch {
		case err == nil && res.Waiting && !looksLikePrompt(res.Output):
			continue // silent but not asking: keep waiting
		case err == nil:
			return a.result(sh, res, time.Since(start), false), nil
		case errors.Is(err, ErrExited):
			return tool.CommandResult{ExitCode: -1, Duration: time.Since(start), Notes: []string{"The shell exited (for example after `exit`); the next command starts a new Session."}}, nil
		case ctx.Err() != nil || deadline.Err() != nil:
			out := stop(sh, c)
			if ctx.Err() != nil {
				return tool.CommandResult{}, ctx.Err()
			}
			return a.result(sh, out, time.Since(start), true), nil
		default:
			return tool.CommandResult{}, err
		}
	}
}

// stop interrupts the command, and kills it if Ctrl-C is not enough.
func stop(sh *Session, c *Command) Result {
	_ = sh.Interrupt()
	ctx, cancel := context.WithTimeout(context.Background(), killGrace)
	defer cancel()
	if res, err := c.Wait(ctx, 0); err == nil {
		return res
	}
	_ = sh.KillForeground()
	ctx2, cancel2 := context.WithTimeout(context.Background(), killGrace)
	defer cancel2()
	res, _ := c.Wait(ctx2, 0)
	return res
}

func (a *Agent) result(sh *Session, res Result, took time.Duration, timedOut bool) tool.CommandResult {
	out := tool.CommandResult{Output: clean(res.Output), ExitCode: res.ExitCode, Waiting: res.Waiting, TimedOut: timedOut, Duration: took, Cwd: sh.Cwd()}
	a.mu.Lock()
	if len(a.typed) > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("The user typed into your Session: %q", strings.Join(a.typed, "")))
		a.typed = nil
	}
	stale := false
	if a.cfg.Confine && a.sh == sh {
		stale, _ = a.rs.Stale(sandbox.RootFS())
	}
	a.mu.Unlock()
	if stale && !res.Waiting && res.ExitCode != 0 {
		out.Notes = append(out.Notes, "This command created new files or folders next to a path the user locked. They only become writable from your next command: remove any partial results and run it again.")
	}
	return out
}

// RunIsolated implements tool.Sessions: an approved command runs outside the
// Session with the sandbox widened to the approved paths (ADR-0004).
func (a *Agent) RunIsolated(ctx context.Context, command string, widen []string, timeout time.Duration) (tool.CommandResult, error) {
	cwd := a.Cwd()
	var resolved []string
	for _, w := range widen {
		resolved = append(resolved, resolvePath(w))
	}
	rs, err := a.plan(a.policy().Widen(resolved))
	if err != nil {
		return tool.CommandResult{}, err
	}
	cmd, err := sandbox.Command(rs, a.cfg.UID, a.cfg.GID, baseEnv(Options{Home: a.cfg.Home, Env: a.cfg.Env, PathPrefix: a.cfg.PathPrefix}), "bash", "-c", command)
	if err != nil {
		return tool.CommandResult{}, err
	}
	cmd.Dir = cwd
	cmd.SysProcAttr.Setpgid = true
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return tool.CommandResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	timedOut := false
	select {
	case err = <-done:
	case <-deadline.C:
		timedOut = true
		killGroup(cmd, done)
		err = nil
	case <-ctx.Done():
		killGroup(cmd, done)
		return tool.CommandResult{}, ctx.Err()
	}
	exit := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exit = exitErr.ExitCode()
	} else if err != nil {
		return tool.CommandResult{}, err
	}
	return tool.CommandResult{Output: out.Bytes(), ExitCode: exit, TimedOut: timedOut, Duration: time.Since(start), Cwd: cwd}, nil
}

// killGroup stops a command's process group: SIGTERM, then SIGKILL.
func killGroup(cmd *exec.Cmd, done <-chan error) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(killGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}

// resolvePath resolves symlinks in path, or in its longest existing prefix.
func resolvePath(path string) string {
	rest := ""
	for p := filepath.Clean(path); ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Clean(path)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// process is a background command.
type process struct {
	tool.Process
	cmd  *exec.Cmd
	done chan struct{}
}

// Start implements tool.Sessions.
func (a *Agent) Start(ctx context.Context, command string) (tool.Process, error) {
	cwd := a.Cwd()
	rs, err := a.plan(a.policy())
	if err != nil {
		return tool.Process{}, err
	}
	a.mu.Lock()
	a.nextID++
	id := "p" + strconv.Itoa(a.nextID)
	a.mu.Unlock()
	ref, f, err := a.cfg.Outputs.Create(a.cfg.TaskID, "bg-"+id+".out")
	if err != nil {
		return tool.Process{}, err
	}
	cmd, err := sandbox.Command(rs, a.cfg.UID, a.cfg.GID, baseEnv(Options{Home: a.cfg.Home, Env: a.cfg.Env, PathPrefix: a.cfg.PathPrefix}), "bash", "-c", command)
	if err != nil {
		f.Close()
		return tool.Process{}, err
	}
	cmd.Dir = cwd
	cmd.SysProcAttr.Setpgid = true
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		f.Close()
		return tool.Process{}, err
	}
	p := &process{Process: tool.Process{ID: id, Command: command, PID: cmd.Process.Pid, Running: true, OutputRef: ref}, cmd: cmd, done: make(chan struct{})}
	a.mu.Lock()
	a.procs[id] = p
	a.mu.Unlock()
	go func() {
		err := cmd.Wait()
		f.Close()
		a.mu.Lock()
		p.Running = false
		p.ExitCode = cmd.ProcessState.ExitCode()
		if err != nil && p.ExitCode == 0 {
			p.ExitCode = -1
		}
		a.mu.Unlock()
		close(p.done)
	}()
	return p.Process, nil
}

// Stop implements tool.Sessions.
func (a *Agent) Stop(ctx context.Context, id string) error {
	a.mu.Lock()
	if id == "" {
		sh, c := a.sh, a.current
		a.mu.Unlock()
		if sh == nil || c == nil || c.finished() {
			return errors.New("no command is running in the Session")
		}
		stop(sh, c)
		return nil
	}
	p, ok := a.procs[id]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("no background process %q in this Task", id)
	}
	select {
	case <-p.done:
		return nil
	default:
	}
	_ = syscall.Kill(-p.PID, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(killGrace):
		_ = syscall.Kill(-p.PID, syscall.SIGKILL)
		<-p.done
	}
	return nil
}

// Processes implements tool.Sessions.
func (a *Agent) Processes() []tool.Process {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]tool.Process, 0, len(a.procs))
	for _, p := range a.procs {
		out = append(out, p.Process)
	}
	return out
}

// Cwd implements tool.Sessions.
func (a *Agent) Cwd() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sh == nil {
		return a.cfg.Home
	}
	if cwd := a.sh.Cwd(); cwd != "" {
		return cwd
	}
	return a.cfg.Home
}

// Close ends the Session's shell. Background processes keep running.
func (a *Agent) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeShell()
	_ = os.RemoveAll(a.cfg.Dir)
}

var (
	ansi   = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)
	prompt = regexp.MustCompile(`(?i)(password|passphrase|\[y/n\]|\(y/n\)|\[yes/no\]|\(yes/no\)|continue\?|:\s*$|\?\s*$|>\s*$|\]\s*$)`)
)

// clean turns PTY output into text for the model: LF line ends, no colour codes.
func clean(out []byte) []byte {
	out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
	return ansi.ReplaceAll(out, nil)
}

// looksLikePrompt reports whether the last line of output asks for input.
func looksLikePrompt(out []byte) bool {
	text := strings.TrimRight(string(clean(out)), "\n")
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		text = text[i+1:]
	}
	if i := strings.LastIndexByte(text, '\r'); i >= 0 {
		text = text[i+1:]
	}
	return text != "" && prompt.MatchString(text)
}

package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/amantiwari/agentic-os/internal/sandbox"
)

// rcFile is the Session's bash rc. Markers go to /dev/tty so they survive a
// command that redirects the shell's own stdout.
const rcFile = `# AOS Session
PS1='aos$ '
HISTCONTROL=ignorespace
bind 'set enable-bracketed-paste off' 2>/dev/null
__aos_c() { printf '\033]133;C;aos=%s\007' "$1" >/dev/tty; }
__aos_d() { printf '\033]133;D;aos=%s;%s\007' "$1" "$2" >/dev/tty; }
`

// Options configures a Session.
type Options struct {
	// Dir holds the Session's rc and command files; it must be readable by UID.
	Dir      string
	UID, GID uint32
	Home     string
	// Confine is the Agent Session's Ruleset; nil starts an unconfined User Session.
	Confine *sandbox.Ruleset
	// Env is appended to the base environment.
	Env []string
}

// Session is a persistent bash on a PTY.
type Session struct {
	opts Options
	cmd  *exec.Cmd
	pty  *os.File
	seq  int

	mu      sync.Mutex
	current *Command
	exited  chan struct{}
}

// Command is one framed command running in a Session.
type Command struct {
	frame    *Frame
	mu       sync.Mutex
	output   []byte
	activity chan struct{}
	done     chan struct{}
	exited   <-chan struct{}
	file     string
}

// Result is what Wait observed.
type Result struct {
	Output   []byte
	ExitCode int
	// Waiting is true when the command produced no output for the idle period
	// and has not finished, typically because it waits for input.
	Waiting bool
}

// ErrExited is returned when the Session's shell has exited.
var ErrExited = errors.New("session shell exited")

// ErrBusy is returned by Run while the previous command has not finished; any
// line typed now would become that command's input.
var ErrBusy = errors.New("session is still running a command")

// Start launches bash on a new PTY.
func Start(opts Options) (*Session, error) {
	rc := filepath.Join(opts.Dir, "bashrc")
	if err := os.WriteFile(rc, []byte(rcFile), 0o644); err != nil {
		return nil, err
	}
	argv := []string{"bash", "--noprofile", "--rcfile", rc, "-i"}
	var cmd *exec.Cmd
	if opts.Confine != nil {
		c, err := sandbox.Command(*opts.Confine, opts.UID, opts.GID, baseEnv(opts), argv...)
		if err != nil {
			return nil, err
		}
		cmd = c
	} else {
		cmd = exec.Command("/bin/bash", argv[1:]...)
		cmd.Env = baseEnv(opts)
	}
	cmd.Dir = opts.Home
	attrs := &syscall.SysProcAttr{
		Setsid:     true,
		Setctty:    true,
		Credential: &syscall.Credential{Uid: opts.UID, Gid: opts.GID, Groups: []uint32{}},
	}
	f, err := pty.StartWithAttrs(cmd, &pty.Winsize{Rows: 50, Cols: 200}, attrs)
	if err != nil {
		return nil, err
	}
	s := &Session{opts: opts, cmd: cmd, pty: f, exited: make(chan struct{})}
	go s.read()
	// Synchronise with the shell: the first framed command drains rc output.
	c, err := s.Run("true")
	if err != nil {
		s.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Wait(ctx, 0); err != nil {
		s.Close()
		return nil, fmt.Errorf("session did not start: %w", err)
	}
	return s, nil
}

func baseEnv(opts Options) []string {
	env := []string{
		"HOME=" + opts.Home,
		"USER=aos", "LOGNAME=aos", "SHELL=/bin/bash",
		// System directories first, so nothing written to ~/.local/bin can shadow sudo or other system tools.
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:" + opts.Home + "/.local/bin",
		"LANG=C.UTF-8", "TERM=xterm-256color",
		"DEBIAN_FRONTEND=noninteractive", "GIT_TERMINAL_PROMPT=0", "PIP_NO_INPUT=1",
		"npm_config_yes=true", "PAGER=cat",
	}
	return append(env, opts.Env...)
}

func (s *Session) read() {
	defer close(s.exited)
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			c := s.current
			s.mu.Unlock()
			if c != nil {
				c.feed(buf[:n])
			}
		}
		if err != nil {
			return
		}
	}
}

// Run starts command in the Session. Only one command runs at a time: Run
// returns ErrBusy until the previous command has finished.
func (s *Session) Run(command string) (*Command, error) {
	c, line, err := s.prepare(command)
	if err != nil {
		return nil, err
	}
	// Written without holding s.mu: the reader needs it to drain output while we block.
	if _, err := s.pty.Write([]byte(line)); err != nil {
		return nil, err
	}
	return c, nil
}

// prepare reserves the Session for command and returns the line to type.
func (s *Session) prepare(command string) (*Command, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil && !s.current.finished() {
		return nil, "", ErrBusy
	}
	s.seq++
	nonce, err := newNonce()
	if err != nil {
		return nil, "", err
	}
	file := filepath.Join(s.opts.Dir, "cmd-"+strconv.Itoa(s.seq))
	if err := os.WriteFile(file, []byte(command+"\n"), 0o644); err != nil {
		return nil, "", err
	}
	c := &Command{frame: NewFrame(nonce), activity: make(chan struct{}, 1), done: make(chan struct{}), exited: s.exited, file: file}
	s.current = c
	// Ctrl-U clears anything typed at the prompt; the leading space keeps the line out of history.
	return c, fmt.Sprintf("\x15 __aos_c %s; source %s; __aos_d %s $?\r", nonce, file, nonce), nil
}

// Input writes keystrokes to the Session, for commands waiting for input.
func (s *Session) Input(b []byte) error {
	_, err := s.pty.Write(b)
	return err
}

// Close ends the shell and releases the PTY.
func (s *Session) Close() error {
	_ = s.cmd.Process.Kill()
	err := s.pty.Close()
	_ = s.cmd.Wait()
	return err
}

// Pid returns the shell's process id.
func (s *Session) Pid() int { return s.cmd.Process.Pid }

// Exited is closed when the shell exits.
func (s *Session) Exited() <-chan struct{} { return s.exited }

func (c *Command) feed(chunk []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frame.Done() {
		return
	}
	c.output = append(c.output, c.frame.Feed(chunk)...)
	select {
	case c.activity <- struct{}{}:
	default:
	}
	if c.frame.Done() {
		close(c.done)
		_ = os.Remove(c.file)
	}
}

func (c *Command) finished() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frame.Done()
}

// Wait returns when the command finishes, or, if idle > 0, when it has produced
// no output for idle. Waiting again after a Waiting result continues collecting.
func (c *Command) Wait(ctx context.Context, idle time.Duration) (Result, error) {
	var timer <-chan time.Time
	var t *time.Timer
	if idle > 0 {
		t = time.NewTimer(idle)
		defer t.Stop()
		timer = t.C
	}
	for {
		select {
		case <-c.done:
			c.mu.Lock()
			defer c.mu.Unlock()
			return Result{Output: c.output, ExitCode: c.frame.ExitCode()}, nil
		case <-c.activity:
			if t != nil {
				t.Reset(idle)
			}
		case <-timer:
			c.mu.Lock()
			defer c.mu.Unlock()
			return Result{Output: append([]byte(nil), c.output...), Waiting: true}, nil
		case <-c.exited:
			return Result{}, ErrExited
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
}

func newNonce() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

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
	"golang.org/x/sys/unix"

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
# Restores the folder and exported variables of a Session that was re-sandboxed.
__aos_save() { { export -p; printf 'cd -- %q\n' "$PWD"; } > "$1"; }
if [ -n "$AOS_RESTORE" ] && [ -f "$AOS_RESTORE" ]; then source "$AOS_RESTORE" 2>/dev/null; rm -f "$AOS_RESTORE"; fi
unset AOS_RESTORE
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
	// PathPrefix is put in front of PATH (the Agent's rm shim).
	PathPrefix string
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
	// recent is the tail of the raw PTY stream, replayed to new viewers.
	recent  []byte
	viewers map[chan []byte]struct{}
}

// recentBytes is how much raw output a new viewer sees first.
const recentBytes = 64 << 10

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
	s := &Session{opts: opts, cmd: cmd, pty: f, exited: make(chan struct{}), viewers: map[chan []byte]struct{}{}}
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
	// The shell echoed that synchronising line, so a viewer attaching later would
	// open on `__aos_c …; source …; __aos_d … $?` instead of a prompt. Drop what
	// the start-up produced and ask readline to redraw (Ctrl-L), so the first
	// thing anyone sees is a clean prompt.
	s.mu.Lock()
	s.recent = nil
	s.mu.Unlock()
	if _, err := s.pty.Write([]byte{0x0c}); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func baseEnv(opts Options) []string {
	env := []string{
		"HOME=" + opts.Home,
		"USER=aos", "LOGNAME=aos", "SHELL=/bin/bash",
		// System directories first, so nothing written to ~/.local/bin can shadow sudo or other system tools.
		"PATH=" + opts.PathPrefix + "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:" + opts.Home + "/.local/bin",
		"LANG=C.UTF-8", "TERM=xterm-256color",
		"DEBIAN_FRONTEND=noninteractive", "GIT_TERMINAL_PROMPT=0", "PIP_NO_INPUT=1",
		"npm_config_yes=true", "PAGER=cat",
	}
	return append(env, opts.Env...)
}

func (s *Session) read() {
	defer func() {
		s.mu.Lock()
		for v := range s.viewers {
			close(v)
		}
		s.viewers = map[chan []byte]struct{}{}
		s.mu.Unlock()
		close(s.exited)
	}()
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			c := s.current
			s.recent = append(s.recent, chunk...)
			if len(s.recent) > recentBytes {
				s.recent = append([]byte(nil), s.recent[len(s.recent)-recentBytes:]...)
			}
			for v := range s.viewers {
				select {
				case v <- chunk:
				default: // a viewer that falls behind misses output rather than stalling the Session
				}
			}
			s.mu.Unlock()
			if c != nil {
				c.feed(chunk)
			}
		}
		if err != nil {
			return
		}
	}
}

// Watch returns the recent raw output and a channel of what follows, until stop
// is called or the shell exits.
func (s *Session) Watch() (recent []byte, output <-chan []byte, stop func()) {
	ch := make(chan []byte, 256)
	s.mu.Lock()
	recent = append([]byte(nil), s.recent...)
	select {
	case <-s.exited:
		close(ch)
	default:
		s.viewers[ch] = struct{}{}
	}
	s.mu.Unlock()
	var once sync.Once
	return recent, ch, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if _, ok := s.viewers[ch]; ok {
				delete(s.viewers, ch)
				close(ch)
			}
		})
	}
}

// Resize changes the terminal size.
func (s *Session) Resize(cols, rows uint16) error {
	return pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

// Cwd returns the shell's current folder.
func (s *Session) Cwd() string {
	cwd, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", s.Pid()))
	return cwd
}

// Busy reports whether a command is still running.
func (s *Session) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current != nil && !s.current.finished()
}

// Interrupt sends Ctrl-C to the foreground command.
func (s *Session) Interrupt() error { return s.Input([]byte{0x03}) }

// KillForeground kills the foreground process group, unless it is the shell itself.
func (s *Session) KillForeground() error {
	pgrp, err := unix.IoctlGetInt(int(s.pty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return err
	}
	if pgrp <= 1 || pgrp == s.Pid() {
		return nil
	}
	return syscall.Kill(-pgrp, syscall.SIGKILL)
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

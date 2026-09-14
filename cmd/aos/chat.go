package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"golang.org/x/term"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
)

const chatPrompt = "› "

// chat is `aos` without a command: each line becomes a Task; Approvals and
// questions are answered at the prompt; Ctrl-C cancels the running Task.
func chat(ctx context.Context) error {
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		return errors.New(`aos needs a terminal for the chat; in scripts use aos run "<task>"`)
	}
	c := newClient()
	info, err := c.system.Info(ctx, connect.NewRequest(&aosv1.InfoRequest{}))
	if err != nil {
		return explain(err)
	}
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(fd, old) }()

	in := &ctrlCReader{r: os.Stdin}
	t := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{in, os.Stdout}, chatPrompt)
	if w, h, err := term.GetSize(fd); err == nil {
		_ = t.SetSize(w, h)
	}
	st := newStyles(true)
	out := &lineWriter{w: t}
	fmt.Fprintf(out, "%sAgentic OS%s · %s · Autonomy %s · Ctrl-C cancels the running Task, Ctrl-D quits\n",
		st.bold, st.reset, info.Msg.Model, autonomyName(info.Msg.Autonomy))
	if info.Msg.ApiKey != "present" {
		fmt.Fprintf(out, "%sNo OpenAI API key: set OPENAI_API_KEY in .env and run docker compose up -d again.%s\n", st.yellow, st.reset)
	}

	s := &chatSession{c: c, t: t, out: out, st: st, answers: make(chan string)}
	for {
		line, err := t.ReadLine()
		if errors.Is(err, io.EOF) {
			if in.takeCtrlC() {
				if s.cancelCurrent(ctx) {
					continue
				}
				fmt.Fprintln(out, "(Ctrl-D quits)")
				continue
			}
			return nil
		}
		if err != nil {
			return err
		}
		if s.deliver(line) {
			continue
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case line == "/quit" || line == "/exit":
			return nil
		case s.running():
			fmt.Fprintf(out, "%sA Task is still running: wait for it, or press Ctrl-C to cancel it.%s\n", st.dim, st.reset)
		default:
			s.start(ctx, line)
		}
	}
}

type chatSession struct {
	c       *client
	t       *term.Terminal
	out     *lineWriter
	st      styles
	answers chan string

	mu      sync.Mutex
	current string
	waiting bool // the prompt currently asks for an answer
}

func (s *chatSession) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current != ""
}

// deliver hands a line to a pending Approval or question.
func (s *chatSession) deliver(line string) bool {
	s.mu.Lock()
	waiting := s.waiting
	s.mu.Unlock()
	if !waiting {
		return false
	}
	s.answers <- line
	return true
}

func (s *chatSession) ask(prompt string) string {
	s.mu.Lock()
	s.waiting = true
	s.mu.Unlock()
	s.t.SetPrompt(prompt)
	answer := <-s.answers
	s.t.SetPrompt(chatPrompt)
	s.mu.Lock()
	s.waiting = false
	s.mu.Unlock()
	return answer
}

func (s *chatSession) start(ctx context.Context, prompt string) {
	created, err := s.c.tasks.CreateTask(ctx, connect.NewRequest(&aosv1.CreateTaskRequest{Prompt: prompt, Interactive: true}))
	if err != nil {
		fmt.Fprintf(s.out, "%s%v%s\n", s.st.red, explain(err), s.st.reset)
		return
	}
	id := created.Msg.Task.Id
	s.mu.Lock()
	s.current = id
	s.mu.Unlock()
	f := newFollower(s.c, id, s.out, s.st, false)
	f.decide = func(a *aosv1.Approval) aosv1.ApprovalDecision {
		for {
			switch strings.ToLower(strings.TrimSpace(s.ask("allow? [a/t/d] › "))) {
			case "a", "y", "yes", "allow":
				return aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_ONCE
			case "t":
				if a.Grantable {
					return aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK
				}
			case "d", "n", "no", "deny":
				return aosv1.ApprovalDecision_APPROVAL_DECISION_DENY
			}
		}
	}
	f.answer = func(string) (string, bool) { return s.ask("answer › "), true }
	go func() {
		task, err := f.run(context.WithoutCancel(ctx))
		s.out.Flush()
		switch {
		case err != nil:
			fmt.Fprintf(s.out, "%s%v%s\n", s.st.red, err, s.st.reset)
		case task.State != aosv1.TaskState_TASK_STATE_SUCCEEDED:
			fmt.Fprintf(s.out, "%s✗ %s%s %s\n", s.st.red, stateName(task.State), s.st.reset, task.Summary)
		default:
			if u := usageLine(task); u != "" {
				fmt.Fprintf(s.out, "%s%s%s\n", s.st.dim, u, s.st.reset)
			}
		}
		s.mu.Lock()
		s.current = ""
		s.mu.Unlock()
	}()
}

func (s *chatSession) cancelCurrent(ctx context.Context) bool {
	s.mu.Lock()
	id := s.current
	s.mu.Unlock()
	if id == "" {
		return false
	}
	if _, err := s.c.tasks.CancelTask(ctx, connect.NewRequest(&aosv1.CancelTaskRequest{Id: id})); err != nil {
		fmt.Fprintf(s.out, "%s%v%s\n", s.st.red, explain(err), s.st.reset)
		return true
	}
	fmt.Fprintf(s.out, "%sCancelling…%s\n", s.st.dim, s.st.reset)
	return true
}

// ctrlCReader notices Ctrl-C, which term.Terminal reports like Ctrl-D.
type ctrlCReader struct {
	r     io.Reader
	mu    sync.Mutex
	ctrlC bool
}

func (c *ctrlCReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if bytes.IndexByte(p[:n], 3) >= 0 {
		c.mu.Lock()
		c.ctrlC = true
		c.mu.Unlock()
	}
	return n, err
}

func (c *ctrlCReader) takeCtrlC() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	was := c.ctrlC
	c.ctrlC = false
	return was
}

// lineWriter passes whole lines to the terminal, which redraws its prompt after
// every write; streamed text would otherwise interleave with the prompt.
type lineWriter struct {
	mu  sync.Mutex
	w   io.Writer
	buf []byte
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if i := bytes.LastIndexByte(l.buf, '\n'); i >= 0 {
		if _, err := l.w.Write(l.buf[:i+1]); err != nil {
			return 0, err
		}
		l.buf = append([]byte(nil), l.buf[i+1:]...)
	}
	return len(p), nil
}

// Flush writes a partial last line.
func (l *lineWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) > 0 {
		_, _ = l.w.Write(append(l.buf, '\n'))
		l.buf = nil
	}
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
)

// follower prints a Task's progress as it happens.
type follower struct {
	c      *client
	taskID string
	out    io.Writer
	st     styles
	tty    bool
	json   bool
	// decide, if set, is asked about each Approval of the Task.
	decide func(a *aosv1.Approval) aosv1.ApprovalDecision
	// answer, if set, is asked the Agent's questions.
	answer func(question string) (string, bool)

	mu       sync.Mutex
	steps    map[string]aosv1.ToolCallStatus
	streamed map[string]bool // text steps whose deltas were printed
	inText   bool            // the cursor is at the end of streamed text
	running  string          // step whose "running" line was printed last
	asked    map[string]bool // Approvals and questions already handled
}

func newFollower(c *client, taskID string, out io.Writer, st styles, tty bool) *follower {
	return &follower{c: c, taskID: taskID, out: out, st: st, tty: tty,
		steps: map[string]aosv1.ToolCallStatus{}, streamed: map[string]bool{}, asked: map[string]bool{}}
}

// run follows the Task until it finishes and returns its final state.
func (f *follower) run(ctx context.Context) (*aosv1.Task, error) {
	defer f.endLine()
	for {
		task, err := f.follow(ctx)
		var ce *connect.Error
		if errors.As(err, &ce) && ce.Code() == connect.CodeResourceExhausted {
			continue // fell behind: subscribe again and catch up from state
		}
		return task, err
	}
}

func (f *follower) follow(ctx context.Context) (*aosv1.Task, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := f.c.events.Subscribe(ctx, connect.NewRequest(&aosv1.SubscribeRequest{TaskId: f.taskID}))
	if err != nil {
		return nil, explain(err)
	}
	defer stream.Close()
	if !stream.Receive() { // the subscription confirmation
		return nil, explain(stream.Err())
	}
	got, err := f.c.tasks.GetTask(ctx, connect.NewRequest(&aosv1.GetTaskRequest{Id: f.taskID}))
	if err != nil {
		return nil, explain(err)
	}
	for _, s := range got.Msg.Steps {
		f.step(s, false)
	}
	for _, a := range got.Msg.Approvals {
		f.approval(ctx, a)
	}
	if done := f.taskChanged(ctx, got.Msg.Task); done {
		return got.Msg.Task, nil
	}
	for stream.Receive() {
		ev := stream.Msg().GetEvent()
		if ev == nil {
			continue
		}
		if f.json {
			b, _ := protojson.Marshal(ev)
			fmt.Fprintln(f.out, string(b))
		}
		switch k := ev.Kind.(type) {
		case *aosv1.Event_TextDelta:
			f.delta(k.TextDelta)
		case *aosv1.Event_TaskStep:
			f.step(k.TaskStep.Step, true)
		case *aosv1.Event_Approval:
			f.approval(ctx, k.Approval.Approval)
		case *aosv1.Event_DownloadProgress:
			f.progress(k.DownloadProgress)
		case *aosv1.Event_TaskChanged:
			if f.taskChanged(ctx, k.TaskChanged.Task) {
				return k.TaskChanged.Task, nil
			}
		}
	}
	if err := stream.Err(); err != nil && ctx.Err() == nil {
		return nil, explain(err)
	}
	return nil, ctx.Err()
}

// endLine ends streamed text or a progress line that has no newline yet.
func (f *follower) endLine() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inText {
		fmt.Fprintln(f.out)
		f.inText = false
	}
}

func (f *follower) printf(format string, args ...any) {
	if f.json {
		return
	}
	if f.inText {
		fmt.Fprintln(f.out)
		f.inText = false
	}
	f.running = ""
	fmt.Fprintf(f.out, format, args...)
}

func (f *follower) delta(d *aosv1.TextDelta) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.json {
		return
	}
	if !f.streamed[d.StepId] {
		f.printf("")
		f.streamed[d.StepId] = true
	}
	f.running = ""
	fmt.Fprint(f.out, d.Delta)
	f.inText = true
}

func (f *follower) step(s *aosv1.TaskStep, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch s.Kind {
	case aosv1.StepKind_STEP_KIND_AGENT_TEXT:
		if f.streamed[s.Id] || s.Text == "" {
			return
		}
		// Only finished text: deltas of a step still streaming arrive separately.
		if !live || s.Text != "" {
			f.streamed[s.Id] = true
			f.printf("%s\n", strings.TrimRight(s.Text, "\n"))
		}
	case aosv1.StepKind_STEP_KIND_TOOL_CALL:
		c := s.ToolCall
		prev, seen := f.steps[s.Id]
		if seen && prev == c.Status {
			return
		}
		f.steps[s.Id] = c.Status
		switch c.Status {
		case aosv1.ToolCallStatus_TOOL_CALL_STATUS_PENDING, aosv1.ToolCallStatus_TOOL_CALL_STATUS_AWAITING_APPROVAL:
			return
		case aosv1.ToolCallStatus_TOOL_CALL_STATUS_RUNNING:
			if !f.tty || f.json {
				return
			}
			f.printf("%s\n", f.st.toolLine(s))
			f.running = s.Id
			return
		}
		if f.json {
			return
		}
		if f.tty && f.running == s.Id {
			fmt.Fprint(f.out, "\x1b[1A\r\x1b[2K") // replace the "running" line
		}
		f.printf("%s\n", f.st.toolLine(s))
	case aosv1.StepKind_STEP_KIND_USER_MESSAGE:
		if live {
			return
		}
		f.printf("%s> %s%s\n", f.st.bold, s.Text, f.st.reset)
	}
}

func (f *follower) progress(p *aosv1.DownloadProgress) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tty || f.json {
		return
	}
	total := "?"
	if p.Total >= 0 {
		total = fmt.Sprintf("%.1f MB", float64(p.Total)/(1<<20))
	}
	fmt.Fprintf(f.out, "\r\x1b[2K%s  downloading %.1f MB of %s%s", f.st.dim, float64(p.Bytes)/(1<<20), total, f.st.reset)
	f.inText = true
}

func (f *follower) approval(ctx context.Context, a *aosv1.Approval) {
	f.mu.Lock()
	if a.Decision != aosv1.ApprovalDecision_APPROVAL_DECISION_UNSPECIFIED || f.asked[a.Id] || f.decide == nil {
		f.mu.Unlock()
		return
	}
	f.asked[a.Id] = true
	f.printf("%s\n", f.st.approvalBlock(a))
	f.mu.Unlock()
	decision := f.decide(a)
	if decision == aosv1.ApprovalDecision_APPROVAL_DECISION_UNSPECIFIED {
		return
	}
	if _, err := f.c.approvals.Decide(ctx, connect.NewRequest(&aosv1.DecideRequest{ApprovalId: a.Id, Decision: decision})); err != nil {
		f.mu.Lock()
		f.printf("%scould not record the decision: %v%s\n", f.st.red, explain(err), f.st.reset)
		f.mu.Unlock()
	}
}

// taskChanged reports whether the Task has finished.
func (f *follower) taskChanged(ctx context.Context, t *aosv1.Task) bool {
	if q := t.GetAwaiting().GetQuestion(); q != "" && f.answer != nil {
		f.mu.Lock()
		key := "q:" + t.UpdatedAt.AsTime().String() + q
		first := !f.asked[key]
		f.asked[key] = true
		if first {
			f.printf("%s? %s%s\n", f.st.yellow+f.st.bold, q, f.st.reset)
		}
		f.mu.Unlock()
		if first {
			if text, ok := f.answer(q); ok {
				if _, err := f.c.tasks.AnswerQuestion(ctx, connect.NewRequest(&aosv1.AnswerQuestionRequest{Id: t.Id, Text: text})); err != nil {
					f.mu.Lock()
					f.printf("%scould not send the answer: %v%s\n", f.st.red, explain(err), f.st.reset)
					f.mu.Unlock()
				}
			}
		}
	}
	return finished(t.State)
}

package task

import (
	"context"
	"errors"
	"strings"
	"testing"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/llm"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/tool"
)

func TestAFollowUpContinuesAFinishedTaskWithItsConversation(t *testing.T) {
	var second llm.Request
	h := newHarness(t, policy.ConfirmRisky,
		fake.Say("Your home folder is empty."),
		func(req llm.Request) (llm.Response, error) {
			second = req
			return fake.Say("Still empty.")(req)
		},
	)
	created, _ := h.m.Create(context.Background(), "What is in my home folder?", 0, true)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)

	if _, err := h.m.FollowUp(context.Background(), created.Id, "  ", true); err == nil {
		t.Error("an empty Follow-up was accepted")
	}
	if _, err := h.m.FollowUp(context.Background(), "t_unknown", "hi", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown Task: %v", err)
	}
	continued, err := h.m.FollowUp(context.Background(), created.Id, "And now?", false)
	if err != nil {
		t.Fatal(err)
	}
	if continued.State != aosv1.TaskState_TASK_STATE_QUEUED || continued.Summary != "" || continued.FinishedAt != nil || continued.Interactive {
		t.Errorf("continued %+v", continued)
	}
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if task.Summary != "Still empty." || task.Interactive {
		t.Errorf("task %+v", task)
	}
	var got []string
	for _, it := range second.Input {
		got = append(got, it.Role+": "+it.Text)
	}
	if want := "user: What is in my home folder? | assistant: Your home folder is empty. | user: And now?"; strings.Join(got, " | ") != want || second.PreviousResponseID != "" {
		t.Errorf("the Follow-up's request\n%s\nwant\n%s (previous %q)", strings.Join(got, " | "), want, second.PreviousResponseID)
	}
	_, steps, _, _ := h.m.Get(context.Background(), created.Id)
	if len(steps) != 4 || steps[2].Kind != aosv1.StepKind_STEP_KIND_USER_MESSAGE || steps[2].Text != "And now?" {
		t.Errorf("steps %v", steps)
	}
}

func TestAnInterruptedTaskResumesWithTheRestartNote(t *testing.T) {
	block := blockingTool{started: make(chan struct{})}
	h := newHarness(t, policy.Auto, fake.Calls("", fake.Call{Name: "block", Args: map[string]any{}}))
	h.m.Close()
	h.cfg.Tools = tool.NewRegistry(block)
	m, _ := New(h.cfg)
	created, _ := m.Create(context.Background(), "wait for it", 0, true)
	<-block.started
	m.Close() // aosd stops
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_INTERRUPTED)

	var resumed llm.Request
	h.cfg.Provider = fake.New(func(req llm.Request) (llm.Response, error) {
		resumed = req
		return fake.Say("It had finished; nothing left to do.")(req)
	})
	h.m, _ = New(h.cfg)
	t.Cleanup(h.m.Close)
	if _, err := h.m.Resume(context.Background(), "t_unknown", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown Task: %v", err)
	}
	if _, err := h.m.Resume(context.Background(), created.Id, true); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	in := resumed.Input
	if len(in) != 4 || in[1].Type != llm.FunctionCall || in[2].Type != llm.FunctionCallOutput || in[2].CallID != in[1].CallID ||
		in[3].Role != "developer" || !strings.Contains(in[3].Text, "AOS restarted") {
		t.Errorf("the resumed request's input: %+v", in)
	}
	if _, err := h.m.Resume(context.Background(), created.Id, true); err == nil || !strings.Contains(err.Error(), "only Interrupted Tasks") {
		t.Errorf("resuming a finished Task: %v", err)
	}
}

func TestTheTranscriptPutsCallsBeforeOutputsAndAnswersCallsCutShort(t *testing.T) {
	stored := []string{
		`[{"type":"message","role":"user","text":"go"}]`,
		`[{"type":"message","role":"assistant","text":"Looking."}]`,
		`[{"type":"function_call","call_id":"c1","name":"list_dir","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"a"}]`,
		`[{"type":"function_call","call_id":"c2","name":"run_command","arguments":"{}"}]`, // AOS stopped here
		``, // an answer to ask_user: no items
		`[{"type":"message","role":"developer","text":"restarted"}]`,
	}
	var got []string
	for _, it := range buildTranscript(stored) {
		got = append(got, string(it.Type)+":"+it.CallID+it.Text)
	}
	want := "message:go message:Looking. function_call:c1 function_call:c2 function_call_output:c1 function_call_output:c2 message:restarted"
	if strings.Join(got, " ") != want {
		t.Errorf("transcript\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
}

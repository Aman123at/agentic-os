package task

import (
	"context"
	"errors"
	"strings"
	"testing"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/llm/fake"
	"github.com/amantiwari/agentic-os/internal/policy"
)

func deleteMissing(n string) fake.Turn {
	return fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/missing-" + n}})
}

func TestAFailingStepPausesAfterTheRetriesAndTheHintReachesTheAgent(t *testing.T) {
	var input []llm.Item
	h := newHarness(t, policy.Auto,
		deleteMissing("1"), deleteMissing("2"), deleteMissing("3"), deleteMissing("4"),
		func(req llm.Request) (llm.Response, error) {
			input = req.Input
			return fake.Say("Found them in ~/docs.")(req)
		},
	)
	created, _ := h.m.Create(context.Background(), "tidy up", 0, true)
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)
	if task.Awaiting.GetKind() != aosv1.AwaitingKind_AWAITING_KIND_RETRIES || !strings.Contains(task.Awaiting.GetQuestion(), "4 times") {
		t.Fatalf("awaiting %+v", task.Awaiting)
	}
	// The first attempt and three Retries, then no further model request.
	if n := len(h.model.Requests()); n != 4 {
		t.Errorf("%d model requests before the pause, want 4", n)
	}
	if err := h.m.Answer(context.Background(), created.Id, "The files are in ~/docs.", "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if last := input[len(input)-1]; last.Type != llm.Message || last.Role != "user" || last.Text != "The files are in ~/docs." {
		t.Errorf("the model's last input item is %+v", last)
	}
	if last := input[len(input)-2]; last.Type != llm.FunctionCallOutput {
		t.Errorf("the hint does not follow the call outputs: %+v", input)
	}
	entries, _ := (&audit.Log{DB: h.db}).List(context.Background(), created.Id, 20, 0)
	if entries[0].Tool != "retry_guard" || entries[0].Decision != "answer" || entries[0].DecidedBy != "user:cli" {
		t.Errorf("newest audit entry %+v", entries[0])
	}
}

func TestWithNobodyToAskAStuckTaskFails(t *testing.T) {
	h := newHarness(t, policy.Auto, deleteMissing("1"), deleteMissing("2"), deleteMissing("3"), deleteMissing("4"))
	created, _ := h.m.Create(context.Background(), "tidy up", 0, false)
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_FAILED)
	if !strings.Contains(task.Summary, "tried the same step 4 times") || !strings.Contains(task.Summary, "nobody is available") {
		t.Errorf("summary %q", task.Summary)
	}
}

func TestAModelAPIThatKeepsFailingPausesTheTaskUntilTheUserRetries(t *testing.T) {
	var input []llm.Item
	h := newHarness(t, policy.Auto,
		func(llm.Request) (llm.Response, error) {
			return llm.Response{}, &llm.TransientError{Err: errors.New("OpenAI API: 503 : overloaded")}
		},
		func(req llm.Request) (llm.Response, error) {
			input = req.Input
			return fake.Say("Hello.")(req)
		},
	)
	created, _ := h.m.Create(context.Background(), "say hello", 0, true)
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)
	if task.Awaiting.GetKind() != aosv1.AwaitingKind_AWAITING_KIND_RETRIES || !strings.Contains(task.Awaiting.GetQuestion(), "503") {
		t.Fatalf("awaiting %+v", task.Awaiting)
	}
	if err := h.m.Answer(context.Background(), created.Id, "try again", "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if len(input) != 2 || input[0].Text != "say hello" || input[1].Text != "try again" {
		t.Errorf("the retried request's input: %+v", input)
	}
}

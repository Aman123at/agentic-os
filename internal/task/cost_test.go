package task

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/llm"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/usage"
)

// priced restarts the harness's Manager with prices for gpt-test: every
// scripted tool-calling turn (100 input, 20 output tokens) costs $1.20.
func (h *harness) priced(t *testing.T, taskLimit, dailyLimit float64) *usage.Tracker {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prices.yaml")
	if err := os.WriteFile(path, []byte("gpt-test:\n  input: 10000\n  output: 10000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.m.Close()
	tracker := &usage.Tracker{DB: h.db, Prices: &usage.File{Path: path}}
	h.cfg.Usage, h.cfg.TaskCostLimit, h.cfg.DailyCostLimit = tracker, taskLimit, dailyLimit
	m, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	h.m = m
	return tracker
}

func list() fake.Turn {
	return fake.Calls("", fake.Call{Name: "list_dir", Args: map[string]any{"path": "~"}})
}

func TestATaskCostLimitPausesBeforeTheNextModelRequest(t *testing.T) {
	h := newHarness(t, policy.Auto, list(), list(), list(), fake.Say("done"))
	h.priced(t, 2, 0)
	created, _ := h.m.Create(context.Background(), "look around", 0, true)

	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)
	if task.Awaiting.GetKind() != aosv1.AwaitingKind_AWAITING_KIND_COST_LIMIT || !strings.Contains(task.Awaiting.GetQuestion(), "reached $2.40") {
		t.Fatalf("awaiting %+v", task.Awaiting)
	}
	if n := len(h.model.Requests()); n != 2 {
		t.Errorf("%d model requests before the pause, want 2", n)
	}
	if err := h.m.Answer(context.Background(), created.Id, "go on", "user:cli"); err != nil {
		t.Fatal(err)
	}
	// Continuing allows another $2 before asking again: turn 3 brings it to $3.60.
	task = h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if u := task.Usage; math.Abs(u.CostUsd-3.6) > 1e-9 || !u.CostKnown || u.InputTokens != 300 {
		t.Errorf("usage %+v", u)
	}
	// The reply to a Cost Limit is not sent to the model.
	for _, it := range h.model.Requests()[2].Input {
		if it.Type == llm.Message && it.Text == "go on" {
			t.Error("the model received the reply to the Cost Limit")
		}
	}
}

func TestTheDailyCostLimitPausesEachTaskOnceADay(t *testing.T) {
	h := newHarness(t, policy.Auto, list(), list(), fake.Say("done"))
	tracker := h.priced(t, 0, 3)
	if _, _, err := tracker.Record(context.Background(), "gpt-test", llm.Usage{InputTokens: 400}); err != nil { // $4 earlier today
		t.Fatal(err)
	}
	created, _ := h.m.Create(context.Background(), "look around", 0, true)
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)
	if !strings.Contains(task.Awaiting.GetQuestion(), "daily Cost Limit") || len(h.model.Requests()) != 0 {
		t.Fatalf("awaiting %+v after %d requests", task.Awaiting, len(h.model.Requests()))
	}
	if err := h.m.Answer(context.Background(), created.Id, "yes", "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	today, _ := tracker.Today(context.Background())
	if math.Abs(today.CostUsd-6.4) > 1e-9 {
		t.Errorf("today's cost $%v, want $6.40", today.CostUsd)
	}

	h.model = fake.New(fake.Say("never"))
	h.m.Close()
	h.cfg.Provider = h.model
	m, _ := New(h.cfg)
	t.Cleanup(m.Close)
	h.m = m
	unattended, _ := h.m.Create(context.Background(), "unattended", 0, false)
	task = h.waitState(t, unattended.Id, aosv1.TaskState_TASK_STATE_FAILED)
	if !strings.Contains(task.Summary, "daily Cost Limit") || !strings.Contains(task.Summary, "nobody is available") {
		t.Errorf("summary %q", task.Summary)
	}
}

func TestWithoutPricesTheCostIsUnknownAndLimitsDoNotPause(t *testing.T) {
	h := newHarness(t, policy.Auto, list(), fake.Say("done"))
	h.m.Close()
	h.cfg.TaskCostLimit = 0.01
	m, _ := New(h.cfg)
	t.Cleanup(m.Close)
	h.m = m
	created, _ := h.m.Create(context.Background(), "look around", 0, true)
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if task.Usage.CostKnown || task.Usage.InputTokens != 100 {
		t.Errorf("usage %+v", task.Usage)
	}
}

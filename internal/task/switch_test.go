package task

import (
	"context"
	"errors"
	"testing"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/tool"
)

// TestSwitchRealmRefusedWhileATaskIsActive covers the M7.7 gate for each active
// state: a Task that is Queued, Running or AwaitingUser blocks the Realm switch,
// and commit is never called — so no password attempt is spent.
func TestSwitchRealmRefusedWhileATaskIsActive(t *testing.T) {
	t.Run("running and queued", func(t *testing.T) {
		block := blockingTool{started: make(chan struct{})}
		h := newHarness(t, policy.ConfirmRisky,
			fake.Calls("", fake.Call{Name: "block", Args: map[string]any{}}),
			fake.Calls("", fake.Call{Name: "block", Args: map[string]any{}}))
		h.m.Close()
		h.cfg.MaxTasks = 1
		h.cfg.Tools = tool.NewRegistry(block)
		m, err := New(h.cfg)
		if err != nil {
			t.Fatal(err)
		}
		h.m = m
		t.Cleanup(m.Close)

		running, _ := m.Create(context.Background(), "one", 0, false)
		<-block.started // it is Running
		queued, _ := m.Create(context.Background(), "two", 0, false)
		h.waitState(t, queued.Id, aosv1.TaskState_TASK_STATE_QUEUED)

		committed := false
		active, err := m.SwitchRealm(context.Background(), func() error { committed = true; return nil })
		if err != nil {
			t.Fatalf("SwitchRealm: %v", err)
		}
		if committed {
			t.Fatal("commit ran while Tasks were active")
		}
		ids := map[string]bool{}
		for _, a := range active {
			ids[a.Id] = true
		}
		if !ids[running.Id] || !ids[queued.Id] {
			t.Errorf("active = %v, want both the Running and Queued Tasks", ids)
		}
	})

	t.Run("awaiting user", func(t *testing.T) {
		h := newHarness(t, policy.ConfirmRisky,
			fake.Calls("", fake.Call{Name: "ask_user", Args: map[string]any{"question": "Which?"}}))
		created, _ := h.m.Create(context.Background(), "ask", 0, true)
		h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)

		active, err := h.m.SwitchRealm(context.Background(), func() error { return errors.New("commit must not run") })
		if err != nil {
			t.Fatalf("SwitchRealm: %v", err)
		}
		if len(active) != 1 || active[0].Id != created.Id {
			t.Errorf("active = %+v, want the AwaitingUser Task", active)
		}
	})
}

// TestSwitchRealmLatchesTheQueueThroughCommit: with nothing active, commit runs,
// and while it commits — and after it succeeds, up to the restart — the queue is
// latched shut, so Create and FollowUp are refused with ErrSwitching (M7.7).
func TestSwitchRealmLatchesTheQueueThroughCommit(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky)
	var duringCommit error
	active, err := h.m.SwitchRealm(context.Background(), func() error {
		_, duringCommit = h.m.Create(context.Background(), "sneaky", 0, false)
		return nil
	})
	if err != nil || len(active) != 0 {
		t.Fatalf("SwitchRealm: active=%v err=%v", active, err)
	}
	if !errors.Is(duringCommit, ErrSwitching) {
		t.Errorf("Create during commit: %v, want ErrSwitching", duringCommit)
	}
	// The latch stays shut after a successful commit (the restart is out of band).
	if _, err := h.m.Create(context.Background(), "after", 0, false); !errors.Is(err, ErrSwitching) {
		t.Errorf("Create after a committed switch: %v, want ErrSwitching", err)
	}
	// No Task was ever written, so nothing runs.
	if list, _ := h.m.List(context.Background(), 10); len(list) != 0 {
		t.Errorf("Tasks were created despite the latch: %+v", list)
	}
}

// TestSwitchRealmReleasesTheLatchWhenCommitFails: a failing commit (e.g. a wrong
// password) unlatches the queue and surfaces the error, so the Machine keeps
// working in its current Realm (M7.7).
func TestSwitchRealmReleasesTheLatchWhenCommitFails(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky)
	want := errors.New("wrong password")
	if _, err := h.m.SwitchRealm(context.Background(), func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("SwitchRealm: %v, want the commit error", err)
	}
	// A later switch's commit runs again — the queue is not stuck latched.
	committed := false
	if _, err := h.m.SwitchRealm(context.Background(), func() error { committed = true; return nil }); err != nil || !committed {
		t.Fatalf("second SwitchRealm: committed=%v err=%v", committed, err)
	}
}

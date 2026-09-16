package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/llm/fake"
	"github.com/amantiwari/agentic-os/internal/policy"
	"github.com/amantiwari/agentic-os/internal/settings"
	"github.com/amantiwari/agentic-os/internal/store"
	"github.com/amantiwari/agentic-os/internal/tool"
)

// harness is a Manager on a temp database and home folder, with the Files,
// Coordination and Desktop Tools running in-process and a scripted model.
type harness struct {
	m     *Manager
	home  string
	model *fake.Provider
	bus   *events.Bus
	db    *store.DB
	cfg   Config
}

func newHarness(t *testing.T, autonomy policy.Autonomy, turns ...fake.Turn) *harness {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home", "aos")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &harness{home: home, model: fake.New(turns...), bus: events.New(), db: db}
	ops := files.Ops{Home: home, Shared: filepath.Join(dir, "shared"), UID: os.Getuid()}
	h.cfg = Config{
		DB:         db,
		Bus:        h.bus,
		Provider:   h.model,
		Tools:      tool.NewRegistry(append(append(tool.FilesTools(), tool.CoordinationTools()...), tool.DesktopTools()...)...),
		Model:      "gpt-test",
		Autonomy:   autonomy,
		MaxTasks:   2,
		MaxRetries: 3,
		Landlock:   true,
		Home:       home,
		NewEnv: func(env *tool.Env) (func(), error) {
			env.FileOps = ops
			env.Files = func([]string) files.Runner { return files.InProcess{Ops: ops} }
			env.Stat = func(p string) (bool, bool) {
				fi, err := os.Stat(p)
				return err == nil, err == nil && fi.IsDir()
			}
			env.Outputs = &tool.Outputs{Dir: filepath.Join(dir, "outputs")}
			return func() {}, nil
		},
	}
	m, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	h.m = m
	return h
}

// waitState waits until the Task reaches one of states.
func (h *harness) waitState(t *testing.T, id string, states ...aosv1.TaskState) *aosv1.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		task, _, _, err := h.m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range states {
			if task.State == s {
				return task
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s is %v, want one of %v (summary %q)", id, task.State, states, task.Summary)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitSummary waits for a Task to reach a state and for its summary to say
// something in particular. Cancel publishes the state at once and the run
// finishes the summary when it unwinds (PLAN.md M4.8 item 8.12), so a test that
// wants the finished summary waits for both.
func (h *harness) waitSummary(t *testing.T, id string, state aosv1.TaskState, contains string) *aosv1.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		task, _, _, err := h.m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == state && strings.Contains(task.Summary, contains) {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s is %v with summary %q, want %v containing %q", id, task.State, task.Summary, state, contains)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestATaskTheModelAnswersDirectlySucceeds(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky, fake.Say("Your home folder is empty."))
	sub := h.bus.Subscribe(t.Context(), "")

	created, err := h.m.Create(context.Background(), "What is in my home folder?", aosv1.Autonomy_AUTONOMY_UNSPECIFIED, true)
	if err != nil {
		t.Fatal(err)
	}
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if task.Summary != "Your home folder is empty." || task.Autonomy != aosv1.Autonomy_AUTONOMY_CONFIRM_RISKY || task.Model != "gpt-test" {
		t.Errorf("task %+v", task)
	}
	_, steps, _, _ := h.m.Get(context.Background(), created.Id)
	if len(steps) != 2 || steps[0].Kind != aosv1.StepKind_STEP_KIND_USER_MESSAGE || steps[0].Text != "What is in my home folder?" ||
		steps[1].Kind != aosv1.StepKind_STEP_KIND_AGENT_TEXT || steps[1].Text != "Your home folder is empty." {
		t.Errorf("steps %v", steps)
	}

	var deltas strings.Builder
	var states []aosv1.TaskState
	timeout := time.After(time.Second)
	for done := false; !done; {
		select {
		case e := <-sub:
			if d := e.GetTextDelta(); d != nil {
				deltas.WriteString(d.Delta)
			}
			if c := e.GetTaskChanged(); c != nil {
				states = append(states, c.Task.State)
				done = c.Task.State == aosv1.TaskState_TASK_STATE_SUCCEEDED
			}
		case <-timeout:
			t.Fatalf("events: states %v", states)
		}
	}
	if deltas.String() != "Your home folder is empty." {
		t.Errorf("streamed text %q", deltas.String())
	}
	if len(states) < 3 || states[0] != aosv1.TaskState_TASK_STATE_QUEUED || states[1] != aosv1.TaskState_TASK_STATE_RUNNING {
		t.Errorf("state events %v", states)
	}
}

func TestToolCallsRunAndTheirResultsGoBackToTheModel(t *testing.T) {
	var secondInput map[string]string
	var previous string
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("Let me look.",
			fake.Call{Name: "write_file", Args: map[string]any{"path": "~/notes.txt", "content": "hello", "overwrite": nil}},
			fake.Call{Name: "list_dir", Args: map[string]any{"path": "~"}},
		),
		func(req llm.Request) (llm.Response, error) {
			secondInput, previous = fake.LastOutputs(req), req.PreviousResponseID
			return fake.Say("Created notes.txt.")(req)
		},
	)
	created, err := h.m.Create(context.Background(), "Create notes.txt", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED, aosv1.TaskState_TASK_STATE_FAILED)
	if task.State != aosv1.TaskState_TASK_STATE_SUCCEEDED {
		t.Fatalf("task failed: %s", task.Summary)
	}
	if got := readFile(t, filepath.Join(h.home, "notes.txt")); got != "hello" {
		t.Errorf("notes.txt = %q", got)
	}
	_, steps, _, _ := h.m.Get(context.Background(), created.Id)
	var kinds []string
	for _, s := range steps {
		k := strings.TrimPrefix(s.Kind.String(), "STEP_KIND_")
		if c := s.ToolCall; c != nil {
			k += ":" + c.Tool + ":" + strings.TrimPrefix(c.Status.String(), "TOOL_CALL_STATUS_")
		}
		kinds = append(kinds, k)
	}
	want := "USER_MESSAGE AGENT_TEXT TOOL_CALL:write_file:SUCCEEDED TOOL_CALL:list_dir:SUCCEEDED AGENT_TEXT"
	if strings.Join(kinds, " ") != want {
		t.Errorf("steps\n%s\nwant\n%s", strings.Join(kinds, " "), want)
	}
	if len(secondInput) != 2 || previous != "resp_fake_1" {
		t.Fatalf("second request: outputs %v, previous response %q", secondInput, previous)
	}
	for _, out := range secondInput {
		if !strings.Contains(out, "notes.txt") {
			t.Errorf("tool output %q does not mention notes.txt", out)
		}
	}
}

func TestATaskTakesTheSettingsInForceWhenItIsCreated(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky)
	var mu sync.Mutex
	cur := settings.Values{Model: "gpt-a", Autonomy: policy.ConfirmAll, MaxTasks: 2, MaxRetries: 3}
	cfg := h.cfg
	cfg.Settings = func() settings.Values {
		mu.Lock()
		defer mu.Unlock()
		return cur
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	ctx := context.Background()

	first, err := m.Create(ctx, "first", aosv1.Autonomy_AUTONOMY_UNSPECIFIED, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Model != "gpt-a" || first.Autonomy != aosv1.Autonomy_AUTONOMY_CONFIRM_ALL {
		t.Errorf("first Task: model %q autonomy %v, want gpt-a confirm-all", first.Model, first.Autonomy)
	}

	mu.Lock()
	cur.Model, cur.Autonomy = "gpt-b", policy.Auto
	mu.Unlock()
	second, err := m.Create(ctx, "second", aosv1.Autonomy_AUTONOMY_UNSPECIFIED, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Model != "gpt-b" || second.Autonomy != aosv1.Autonomy_AUTONOMY_AUTO {
		t.Errorf("a Task created after the change: model %q autonomy %v, want gpt-b auto", second.Model, second.Autonomy)
	}
	again, _, _, err := m.Get(ctx, first.Id)
	if err != nil || again.Model != "gpt-a" || again.Autonomy != aosv1.Autonomy_AUTONOMY_CONFIRM_ALL {
		t.Errorf("the first Task changed with the settings: %+v, %v", again, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// waitPending waits for the Task's next undecided Approval.
func (h *harness) waitPending(t *testing.T, taskID string) *aosv1.Approval {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := h.m.Pending(context.Background(), taskID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) > 0 {
			return pending[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no Approval requested")
	return nil
}

func TestRiskyCallsWaitForApprovalAndTaskGrantsCoverTheSameFolder(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/old/a.log"}}),
		fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/old/b.log"}}),
		fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/keep.txt"}}),
		fake.Say("Cleaned up."),
	)
	for _, name := range []string{"old/a.log", "old/b.log", "keep.txt"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(h.home, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.home, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	created, _ := h.m.Create(context.Background(), "Clean up", 0, true)

	first := h.waitPending(t, created.Id)
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)
	if task.Awaiting.GetApprovalId() != first.Id || first.Tool != "delete" || !first.Grantable || first.Summary != "Move ~/old/a.log to the Trash" {
		t.Fatalf("awaiting %+v, approval %+v", task.Awaiting, first)
	}
	if _, err := h.m.Decide(context.Background(), first.Id, aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK, "user:cli"); err != nil {
		t.Fatal(err)
	}
	// b.log is in the granted folder: no second Approval. keep.txt is not.
	third := h.waitPending(t, created.Id)
	if third.Summary != "Move ~/keep.txt to the Trash" {
		t.Fatalf("second Approval is for %q", third.Summary)
	}
	if _, err := h.m.Decide(context.Background(), third.Id, aosv1.ApprovalDecision_APPROVAL_DECISION_DENY, "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)

	for name, gone := range map[string]bool{"old/a.log": true, "old/b.log": true, "keep.txt": false} {
		if _, err := os.Stat(filepath.Join(h.home, name)); os.IsNotExist(err) != gone {
			t.Errorf("%s: deleted=%v, want %v", name, os.IsNotExist(err), gone)
		}
	}
	denied := fake.LastOutputs(h.model.Requests()[3])
	for _, out := range denied {
		if !strings.Contains(out, "The user denied this call") {
			t.Errorf("model was told %q after the denial", out)
		}
	}
	_, _, approvals, _ := h.m.Get(context.Background(), created.Id)
	if len(approvals) != 2 || approvals[0].DecidedBy != "user:cli" || approvals[1].Decision != aosv1.ApprovalDecision_APPROVAL_DECISION_DENY {
		t.Errorf("approvals %v", approvals)
	}
}

func TestDeleteRemovesAFinishedTaskAndItsChildrenButKeepsTheAuditLog(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/old/a.log"}}),
		fake.Say("Cleaned up."),
	)
	if err := os.MkdirAll(filepath.Join(h.home, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, "old/a.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, _ := h.m.Create(context.Background(), "Clean up", 0, true)
	pending := h.waitPending(t, created.Id)
	// Allow for the Task, which records a grant as well as an approval.
	if _, err := h.m.Decide(context.Background(), pending.Id, aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK, "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)

	// Everything is in place before the delete.
	for _, tbl := range []string{"task_steps", "approvals", "grants"} {
		if n := countRows(t, h.db, tbl, created.Id); n == 0 {
			t.Fatalf("%s: no rows for the Task before delete", tbl)
		}
	}
	auditBefore := countRows(t, h.db, "audit_log", created.Id)
	if auditBefore == 0 {
		t.Fatal("no audit rows for the Task before delete")
	}

	sub := h.bus.Subscribe(t.Context(), "")
	if err := h.m.Delete(context.Background(), created.Id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// The Task and its children are gone; the Audit Log is untouched.
	if _, _, _, err := h.m.Get(context.Background(), created.Id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v, want ErrNotFound", err)
	}
	for _, tbl := range []string{"task_steps", "approvals", "grants"} {
		if n := countRows(t, h.db, tbl, created.Id); n != 0 {
			t.Errorf("%s: %d rows left after delete", tbl, n)
		}
	}
	if n := countRows(t, h.db, "audit_log", created.Id); n != auditBefore {
		t.Errorf("audit_log has %d rows after delete, want %d unchanged", n, auditBefore)
	}

	// A TaskChanged{removed} reaches subscribers so every tab drops the Task.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-sub:
			if c := e.GetTaskChanged(); c != nil && c.Removed {
				if c.Task.GetId() != created.Id {
					t.Errorf("removal event for %q, want %q", c.Task.GetId(), created.Id)
				}
				return
			}
		case <-deadline:
			t.Fatal("no TaskChanged{removed} event")
		}
	}
}

func TestDeleteRefusesWhileTheTaskIsActive(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/old/a.log"}}),
		fake.Say("Cleaned up."),
	)
	if err := os.MkdirAll(filepath.Join(h.home, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, "old/a.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, _ := h.m.Create(context.Background(), "Clean up", 0, true)
	pending := h.waitPending(t, created.Id)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)

	// Awaiting the user, the Task is still active: deleting it is refused.
	if err := h.m.Delete(context.Background(), created.Id); err == nil || !strings.Contains(err.Error(), "cancel it first") {
		t.Fatalf("Delete of an awaiting Task: %v, want a refusal", err)
	}
	if _, _, _, err := h.m.Get(context.Background(), created.Id); err != nil {
		t.Fatalf("the Task must survive a refused delete: %v", err)
	}

	// Once it finishes, it deletes.
	if _, err := h.m.Decide(context.Background(), pending.Id, aosv1.ApprovalDecision_APPROVAL_DECISION_DENY, "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if err := h.m.Delete(context.Background(), created.Id); err != nil {
		t.Fatalf("Delete of a finished Task: %v", err)
	}
}

// countRows counts a table's rows for one Task, for the delete tests.
func countRows(t *testing.T, db *store.DB, table, taskID string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE task_id = ?`, taskID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// blockingTool is a Tool whose call runs until it is cancelled.
type blockingTool struct{ started chan struct{} }

func (b blockingTool) Spec() tool.Spec {
	return tool.Spec{Name: "block", Description: "blocks", Parameters: map[string]any{"type": "object"}}
}

func (b blockingTool) Prepare(context.Context, *tool.Env, json.RawMessage) (*tool.Call, error) {
	return &tool.Call{Summary: "Block", Policy: policy.Call{Tool: "block"}, Run: func(ctx context.Context, _ tool.Run) tool.Result {
		close(b.started)
		<-ctx.Done()
		return tool.Result{Output: "stopped"}
	}}, nil
}

func TestCancellingStopsARunningCallAndAnAwaitedApproval(t *testing.T) {
	block := blockingTool{started: make(chan struct{})}
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("", fake.Call{Name: "block", Args: map[string]any{}}),
		fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/x"}}),
	)
	h.m.Close()
	h.cfg.Tools = tool.NewRegistry(append(tool.FilesTools(), block)...)
	m, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	h.m = m

	running, _ := h.m.Create(context.Background(), "block", 0, true)
	<-block.started
	awaiting, _ := h.m.Create(context.Background(), "delete x", 0, true)
	h.waitPending(t, awaiting.Id)

	for _, id := range []string{running.Id, awaiting.Id} {
		if err := h.m.Cancel(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		task := h.waitState(t, id, aosv1.TaskState_TASK_STATE_CANCELLED)
		if task.FinishedAt == nil || task.Awaiting != nil {
			t.Errorf("cancelled task %+v", task)
		}
	}
	_, steps, _, _ := h.m.Get(context.Background(), running.Id)
	if last := steps[len(steps)-1].ToolCall; last == nil || last.Status != aosv1.ToolCallStatus_TOOL_CALL_STATUS_CANCELLED {
		t.Errorf("last step of the cancelled Task: %v", steps[len(steps)-1])
	}
	if err := h.m.Cancel(context.Background(), running.Id); err == nil {
		t.Error("cancelling a finished Task succeeded")
	}
}

func TestTheAuditLogRecordsEveryCallWithItsDecision(t *testing.T) {
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("",
			fake.Call{Name: "list_dir", Args: map[string]any{"path": "~"}},
			fake.Call{Name: "delete", Args: map[string]any{"path": "~/.ssh/id_old"}},
			fake.Call{Name: "no_such_tool", Args: map[string]any{}},
		),
		fake.Say("done"),
	)
	created, _ := h.m.Create(context.Background(), "audit me", 0, false)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)

	entries, err := (&audit.Log{DB: h.db}).List(context.Background(), created.Id, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Tool] = e.Decision + " by " + e.DecidedBy + " as " + e.Actor
		if e.StepId == "" {
			t.Errorf("entry without a step: %+v", e)
		}
	}
	want := map[string]string{
		"list_dir":     "allow by autonomy as agent",
		"delete":       "deny by policy as agent", // Protected Path, and nobody to ask
		"no_such_tool": "invalid by  as agent",
	}
	if len(got) != len(want) {
		t.Fatalf("entries %v", got)
	}
	for tool, w := range want {
		if got[tool] != w {
			t.Errorf("%s: %q, want %q", tool, got[tool], w)
		}
	}
}

func TestAnApprovedProtectedPathCallRunsWithTheSandboxWidenedToIt(t *testing.T) {
	h := newHarness(t, policy.Auto,
		fake.Calls("", fake.Call{Name: "delete", Args: map[string]any{"path": "~/.ssh/id_old"}}),
		fake.Say("Removed the old key."),
	)
	var widened [][]string
	newEnv := h.cfg.NewEnv
	h.cfg.NewEnv = func(env *tool.Env) (func(), error) {
		release, err := newEnv(env)
		inner := env.Files
		env.Files = func(widen []string) files.Runner {
			widened = append(widened, widen)
			return inner(widen)
		}
		return release, err
	}
	h.m.Close()
	m, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	h.m = m
	key := filepath.Join(h.home, ".ssh", "id_old")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	created, _ := h.m.Create(context.Background(), "remove my old key", 0, true)
	a := h.waitPending(t, created.Id)
	// Deleting needs the folder: the Approval shows what becomes writable.
	if a.Grantable || len(a.ProtectedPaths) != 1 || a.ProtectedPaths[0] != filepath.Dir(key) {
		t.Fatalf("approval for a Protected Path under auto: %+v", a)
	}
	if _, err := h.m.Decide(context.Background(), a.Id, aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK, "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if len(widened) != 1 || len(widened[0]) != 1 || widened[0][0] != filepath.Dir(key) {
		t.Errorf("file runner widened to %v, want [[%s]]", widened, filepath.Dir(key))
	}
	_, _, approvals, _ := h.m.Get(context.Background(), created.Id)
	if approvals[0].Decision != aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_ONCE {
		t.Errorf("a Protected Path Approval must never become a Task grant: %v", approvals[0].Decision)
	}
}

func TestAskUserWaitsForTheAnswer(t *testing.T) {
	var answer string
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("", fake.Call{Name: "ask_user", Args: map[string]any{"question": "Which folder?"}}),
		func(req llm.Request) (llm.Response, error) {
			for _, out := range fake.LastOutputs(req) {
				answer = out
			}
			return fake.Say("ok")(req)
		},
	)
	created, _ := h.m.Create(context.Background(), "tidy a folder", 0, true)
	task := h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_AWAITING_USER)
	if task.Awaiting.GetQuestion() != "Which folder?" {
		t.Fatalf("awaiting %+v", task.Awaiting)
	}
	if err := h.m.Answer(context.Background(), created.Id, "~/Downloads", "user:cli"); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if answer != "The user answered: ~/Downloads" {
		t.Errorf("model got %q", answer)
	}
}

func TestTasksBeyondTheSlotsWaitAndARestartInterruptsRunningTasks(t *testing.T) {
	block := blockingTool{started: make(chan struct{})}
	turns := []fake.Turn{
		fake.Calls("", fake.Call{Name: "block", Args: map[string]any{}}),
		fake.Calls("", fake.Call{Name: "block", Args: map[string]any{}}),
	}
	h := newHarness(t, policy.ConfirmRisky, turns...)
	h.m.Close()
	h.cfg.MaxTasks = 1
	h.cfg.Tools = tool.NewRegistry(block)
	m, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.m = m

	first, _ := m.Create(context.Background(), "first", 0, true)
	<-block.started
	second, _ := m.Create(context.Background(), "second", 0, true)
	time.Sleep(50 * time.Millisecond)
	if task := h.waitState(t, second.Id, aosv1.TaskState_TASK_STATE_QUEUED); task.State != aosv1.TaskState_TASK_STATE_QUEUED {
		t.Fatal("second Task started without a free slot")
	}

	m.Close() // aosd stops
	h.waitState(t, first.Id, aosv1.TaskState_TASK_STATE_INTERRUPTED)

	h.cfg.Provider = fake.New(fake.Say("second done"))
	restarted, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	h.m = restarted
	h.waitState(t, second.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if task := h.waitState(t, first.Id, aosv1.TaskState_TASK_STATE_INTERRUPTED); task.FinishedAt == nil {
		t.Errorf("interrupted task %+v", task)
	}
}

// fakeSessions stands in for the PTY Session: every command prints output.
type fakeSessions struct{ output []byte }

func (f fakeSessions) Run(context.Context, string, time.Duration) (tool.CommandResult, error) {
	return tool.CommandResult{Output: f.output, ExitCode: 0, Duration: time.Second, Cwd: "/home/aos"}, nil
}
func (f fakeSessions) Input(context.Context, string, time.Duration) (tool.CommandResult, error) {
	return tool.CommandResult{}, nil
}
func (f fakeSessions) RunIsolated(context.Context, string, []string, time.Duration) (tool.CommandResult, error) {
	return tool.CommandResult{}, nil
}
func (f fakeSessions) Start(context.Context, string) (tool.Process, error) {
	return tool.Process{}, nil
}
func (f fakeSessions) Stop(context.Context, string) error { return nil }
func (f fakeSessions) Processes() []tool.Process          { return nil }
func (f fakeSessions) Cwd() string                        { return "" }

func TestLongCommandOutputIsShapedAndPagedWithReadOutput(t *testing.T) {
	var output []byte
	for i := 0; len(output) < 100_000; i++ {
		output = fmt.Appendf(output, "line %06d\n", i)
	}
	var shaped, page string
	h := newHarness(t, policy.Auto,
		fake.Calls("", fake.Call{Name: "run_command", Args: map[string]any{"command": "seq 1 9999"}}),
		func(req llm.Request) (llm.Response, error) {
			for _, out := range fake.LastOutputs(req) {
				shaped = out
			}
			ref := regexp.MustCompile(`ref="([^"]+)" offset=(\d+)`).FindStringSubmatch(shaped)
			if ref == nil {
				return fake.Say("no ref")(req)
			}
			return fake.Calls("", fake.Call{Name: "read_output", Args: map[string]any{"ref": ref[1], "offset": 50_000, "limit": 22}})(req)
		},
		func(req llm.Request) (llm.Response, error) {
			for _, out := range fake.LastOutputs(req) {
				page = out
			}
			return fake.Say("done")(req)
		},
	)
	h.m.Close()
	h.cfg.Tools = tool.NewRegistry(tool.SessionTools()...)
	newEnv := h.cfg.NewEnv
	h.cfg.NewEnv = func(env *tool.Env) (func(), error) {
		env.Sessions = fakeSessions{output: output}
		return newEnv(env)
	}
	m, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	h.m = m

	created, _ := m.Create(context.Background(), "count", 0, true)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if len(shaped) > 9_000 || !strings.Contains(shaped, "line 000000") || !strings.Contains(shaped, fmt.Sprintf("line %06d", len(output)/12-1)) || !strings.Contains(shaped, "bytes omitted") {
		t.Errorf("shaped output (%d bytes):\n%s", len(shaped), shaped)
	}
	if !strings.Contains(page, "Bytes 50000-50022 of 100008:\n") || !strings.Contains(page, "\nline 004167\n") {
		t.Errorf("read_output page: %q", page)
	}
}

// outputsInOrder returns the Tool results a turn sends back, in call order.
func outputsInOrder(req llm.Request) []string {
	var out []string
	for _, it := range req.Input {
		if it.Type == llm.FunctionCallOutput {
			out = append(out, it.Output)
		}
	}
	return out
}

func TestAgentsCanNotifyAndOpenThingsInTheDesktop(t *testing.T) {
	notify := func(title string) fake.Call {
		return fake.Call{Name: "notify", Args: map[string]any{"title": title, "body": "details"}}
	}
	open := func(args map[string]any) fake.Call { return fake.Call{Name: "open_in_desktop", Args: args} }
	var turns [][]string
	record := func(next fake.Turn) fake.Turn {
		return func(req llm.Request) (llm.Response, error) {
			turns = append(turns, outputsInOrder(req))
			return next(req)
		}
	}
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("",
			notify("Site ready"),
			open(map[string]any{"path": "~/report.txt"}),
			open(map[string]any{"path": "~"}),
			open(map[string]any{"path": "~/missing.txt"}),
			open(map[string]any{"path": "https://example.com"}),
			open(map[string]any{"port": 8080}),
			open(map[string]any{"port": 8080, "path": "~/report.txt"}),
		),
		record(fake.Calls("", notify("2"), notify("3"), notify("4"), notify("5"), notify("6"))),
		record(fake.Say("done")),
	)
	if err := os.WriteFile(filepath.Join(h.home, "report.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []string
	newEnv := h.cfg.NewEnv
	h.cfg.NewEnv = func(env *tool.Env) (func(), error) {
		env.Desktop = &tool.Desktop{
			Notify: func(_ context.Context, n tool.Notice) error {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, fmt.Sprintf("notify %s|%s|%d", n.Title, n.Body, n.Port))
				return nil
			},
			Open: func(_ context.Context, path string, dir bool) error {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, fmt.Sprintf("open %s dir=%v", path, dir))
				return nil
			},
		}
		return newEnv(env)
	}
	h.m.Close()
	m, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	h.m = m
	created, _ := h.m.Create(context.Background(), "build the site", 0, true)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)

	want := []string{
		"notify Site ready|details|0",
		"open " + filepath.Join(h.home, "report.txt") + " dir=false",
		"open " + h.home + " dir=true",
		"notify Open port 8080|The Agent offers a page served on port 8080.|8080",
		"notify 2|details|0", "notify 3|details|0", "notify 4|details|0", "notify 5|details|0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the Desktop was asked:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(turns) != 2 || len(turns[0]) != 7 || len(turns[1]) != 5 {
		t.Fatalf("outputs %q", turns)
	}
	for i, frag := range []string{"notification", "Opened", "Opened", "no such file", "web address", "Open button", "either path or port"} {
		if !strings.Contains(turns[0][i], frag) {
			t.Errorf("call %d output %q, want it to mention %q", i+1, turns[0][i], frag)
		}
	}
	if !strings.Contains(turns[1][4], "limit") {
		t.Errorf("the sixth notification: %q", turns[1][4])
	}
}

func TestWithoutTheDesktopTheDesktopToolsDoNothing(t *testing.T) {
	var outputs []string
	h := newHarness(t, policy.ConfirmRisky,
		fake.Calls("", fake.Call{Name: "notify", Args: map[string]any{"title": "Done"}}, fake.Call{Name: "open_in_desktop", Args: map[string]any{"path": "~"}}),
		func(req llm.Request) (llm.Response, error) {
			outputs = outputsInOrder(req)
			return fake.Say("ok")(req)
		},
	)
	created, _ := h.m.Create(context.Background(), "say hi", 0, true)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if len(outputs) != 2 || !strings.Contains(outputs[0], "cli Mode") || !strings.Contains(outputs[1], "cli Mode") {
		t.Errorf("outputs %q", outputs)
	}
}

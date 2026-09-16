// Package task runs Tasks (PLAN.md §8.1): the queue, AOS_MAX_TASKS slots, state
// changes, Approvals and cancellation. Each running Task has one Agent.
package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/agent"
	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/policy"
	"github.com/amantiwari/agentic-os/internal/settings"
	"github.com/amantiwari/agentic-os/internal/store"
	"github.com/amantiwari/agentic-os/internal/tool"
	"github.com/amantiwari/agentic-os/internal/usage"
)

// Config wires a Manager.
type Config struct {
	DB              *store.DB
	Bus             *events.Bus
	Audit           *audit.Log
	Provider        llm.Provider
	Tools           *tool.Registry
	Model           string
	ReasoningEffort string
	Instructions    string
	// Autonomy is used by Tasks created without one.
	Autonomy policy.Autonomy
	MaxTasks int
	// MaxRetries is AOS_MAX_RETRIES (PLAN.md §8.3).
	MaxRetries int
	Landlock   bool
	Home       string
	// Usage records usage per day and estimates costs; nil records usage without prices.
	Usage *usage.Tracker
	// Context returns AOS's message that starts an Agent's conversation: the
	// user's Memory and the Machine Profile (PLAN.md §8.2). Nil means none.
	Context func(ctx context.Context) string
	// Cost Limits in USD; 0 means none (PLAN.md §8.4).
	TaskCostLimit, DailyCostLimit float64
	// Settings returns the settings in force (PLAN.md §6.4); nil uses the fields
	// above. A Task takes its model and Autonomy when it is created, and its
	// reasoning effort and retries when it starts running, so a change never
	// alters a running Task; max Tasks and the Cost Limits apply at once.
	Settings func() settings.Values
	// NewEnv completes a Task's Tool environment; the returned func releases it.
	NewEnv func(env *tool.Env) (release func(), err error)
	// Protection returns the current Protected Paths for a Task.
	Protection func(taskID string) *policy.Protection
	Now        func() time.Time
}

// Manager owns every Task.
type Manager struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	queue   []string
	running map[string]*run
	waiting map[string]*waiter   // by Approval id
	asking  map[string]*question // by Task id
}

// waiter is an Approval a running Task waits for.
type waiter struct {
	run      *run
	call     policy.Call
	decision chan aosv1.ApprovalDecision
}

// question is a reply a running Task waits for: to ask_user, or after a pause.
type question struct {
	kind   aosv1.AwaitingKind
	answer chan string
}

// ErrNobodyToAsk is returned when a Task that nobody watches needs a reply.
var ErrNobodyToAsk = errors.New("nobody is available to reply")

// New starts a Manager.
func New(cfg Config) (*Manager, error) {
	if cfg.MaxTasks <= 0 {
		cfg.MaxTasks = 3
	}
	if cfg.Autonomy == 0 {
		cfg.Autonomy = policy.ConfirmRisky
	}
	if cfg.Audit == nil {
		cfg.Audit = &audit.Log{DB: cfg.DB}
	}
	if cfg.Usage == nil {
		cfg.Usage = &usage.Tracker{DB: cfg.DB, Now: cfg.Now}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, ctx: ctx, cancel: cancel, running: map[string]*run{}, waiting: map[string]*waiter{}, asking: map[string]*question{}}
	if err := m.recover(); err != nil {
		cancel()
		return nil, err
	}
	m.schedule()
	return m, nil
}

// settings returns the settings in force.
func (m *Manager) settings() settings.Values {
	if m.cfg.Settings != nil {
		return m.cfg.Settings()
	}
	c := m.cfg
	return settings.Values{Model: c.Model, ReasoningEffort: c.ReasoningEffort, Autonomy: c.Autonomy, MaxTasks: c.MaxTasks,
		MaxRetries: c.MaxRetries, TaskCostLimit: c.TaskCostLimit, DailyCostLimit: c.DailyCostLimit}
}

// SettingsChanged starts queued Tasks that a raised max Tasks now allows.
func (m *Manager) SettingsChanged() { m.schedule() }

// recover handles Tasks left by a previous aosd: work cut short becomes
// Interrupted, and queued Tasks wait for a slot again.
func (m *Manager) recover() error {
	ctx := context.Background()
	at := store.Millis(now(m.cfg))
	err := m.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE approvals SET decision = ?, decided_by = 'aos: restarted', decided_at = ?
			WHERE decision = 0`, int32(aosv1.ApprovalDecision_APPROVAL_DECISION_DENY), at); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE task_steps SET status = ?, result = 'AOS stopped while this call was running.', finished_at = ?
			WHERE status IN (?, ?, ?)`, int32(aosv1.ToolCallStatus_TOOL_CALL_STATUS_CANCELLED), at,
			int32(aosv1.ToolCallStatus_TOOL_CALL_STATUS_PENDING), int32(aosv1.ToolCallStatus_TOOL_CALL_STATUS_AWAITING_APPROVAL),
			int32(aosv1.ToolCallStatus_TOOL_CALL_STATUS_RUNNING)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET state = ?, summary = 'AOS stopped while the Task was running; aos resume continues it.',
			awaiting_approval_id = '', awaiting_question = '', awaiting_kind = 0, updated_at = ?, finished_at = ? WHERE state IN (?, ?)`,
			int32(aosv1.TaskState_TASK_STATE_INTERRUPTED), at, at,
			int32(aosv1.TaskState_TASK_STATE_RUNNING), int32(aosv1.TaskState_TASK_STATE_AWAITING_USER))
		return err
	})
	if err != nil {
		return err
	}
	queued, err := listTasks(ctx, m.cfg.DB.Read(), "state = ?", 10000, int32(aosv1.TaskState_TASK_STATE_QUEUED))
	if err != nil {
		return err
	}
	for i := len(queued) - 1; i >= 0; i-- { // oldest first
		m.queue = append(m.queue, queued[i].Id)
	}
	return nil
}

// Close stops every running Task; they become Interrupted.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
}

// Create queues a new Task.
func (m *Manager) Create(ctx context.Context, prompt string, autonomy aosv1.Autonomy, interactive bool) (*aosv1.Task, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("the Task is empty")
	}
	s := m.settings()
	if autonomy == aosv1.Autonomy_AUTONOMY_UNSPECIFIED {
		autonomy = toProto(s.Autonomy)
	}
	at := timestamppb.New(now(m.cfg))
	t := &aosv1.Task{Id: newID("t_"), Title: title(prompt), Prompt: prompt, State: aosv1.TaskState_TASK_STATE_QUEUED,
		Autonomy: autonomy, Interactive: interactive, Model: s.Model, Usage: &aosv1.Usage{CostKnown: true}, CreatedAt: at, UpdatedAt: at}
	step := &aosv1.TaskStep{Id: newID("s_"), TaskId: t.Id, Kind: aosv1.StepKind_STEP_KIND_USER_MESSAGE, Text: prompt, CreatedAt: at}
	start := []llm.Item{llm.UserMessage(prompt)}
	if c := m.context(ctx); c != "" {
		start = []llm.Item{llm.DeveloperMessage(c), llm.UserMessage(prompt)}
	}
	items, _ := json.Marshal(start)
	err := m.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := insertTask(ctx, tx, t); err != nil {
			return err
		}
		return insertStep(ctx, tx, step, string(items))
	})
	if err != nil {
		return nil, err
	}
	m.publishTask(t)
	m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_TaskStep{TaskStep: &aosv1.TaskStepChanged{Step: step}}})
	m.mu.Lock()
	m.queue = append(m.queue, t.Id)
	m.mu.Unlock()
	m.schedule()
	return t, nil
}

// FollowUp continues a finished Task with a further instruction, with the
// conversation so far (a Follow-up).
func (m *Manager) FollowUp(ctx context.Context, id, text string, interactive bool) (*aosv1.Task, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("the Follow-up is empty")
	}
	t, err := getTask(ctx, m.cfg.DB.Read(), id)
	if err != nil {
		return nil, err
	}
	if !finished(t.State) {
		return nil, fmt.Errorf("task %s is %s; a Follow-up continues a finished Task", id, stateName(t.State))
	}
	var items []llm.Item
	if t.State == aosv1.TaskState_TASK_STATE_INTERRUPTED {
		items = append(items, llm.DeveloperMessage(restartNote))
	}
	if c := m.freshContext(ctx, id); c != "" {
		items = append(items, llm.DeveloperMessage(c))
	}
	items = append(items, llm.UserMessage(text))
	return m.requeue(ctx, t, interactive, &aosv1.TaskStep{Kind: aosv1.StepKind_STEP_KIND_USER_MESSAGE, Text: text}, items)
}

// Resume continues a Task that AOS stopping interrupted (PLAN.md §8.1).
func (m *Manager) Resume(ctx context.Context, id string, interactive bool) (*aosv1.Task, error) {
	t, err := getTask(ctx, m.cfg.DB.Read(), id)
	if err != nil {
		return nil, err
	}
	if t.State != aosv1.TaskState_TASK_STATE_INTERRUPTED {
		return nil, fmt.Errorf("task %s is %s; only Interrupted Tasks can be resumed", id, stateName(t.State))
	}
	step := &aosv1.TaskStep{Kind: aosv1.StepKind_STEP_KIND_NOTE, Text: "Resumed after AOS restarted."}
	items := []llm.Item{llm.DeveloperMessage(restartNote)}
	if c := m.freshContext(ctx, id); c != "" {
		items = append(items, llm.DeveloperMessage(c))
	}
	return m.requeue(ctx, t, interactive, step, items)
}

func (m *Manager) context(ctx context.Context) string {
	if m.cfg.Context == nil {
		return ""
	}
	return m.cfg.Context(ctx)
}

// freshContext returns the context message when it differs from the last one
// in the Task's conversation, so a continued Agent learns what changed.
func (m *Manager) freshContext(ctx context.Context, taskID string) string {
	c := m.context(ctx)
	if c == "" {
		return ""
	}
	_, stored, err := listSteps(ctx, m.cfg.DB.Read(), taskID)
	if err != nil {
		return c
	}
	last := ""
	for _, it := range buildTranscript(stored) {
		// The profile package starts its message with one of these headings.
		if it.Type == llm.Message && it.Role == "developer" && (strings.HasPrefix(it.Text, "# Memory") || strings.HasPrefix(it.Text, "# Machine Profile")) {
			last = it.Text
		}
	}
	if last == c {
		return ""
	}
	return c
}

// setCheckpoint records the Checkpoint taken before the Task's first software change.
func (r *run) setCheckpoint(id string) {
	r.mu.Lock()
	r.task.CheckpointId = id
	snapshot := proto.Clone(r.task).(*aosv1.Task)
	r.mu.Unlock()
	_ = r.m.cfg.DB.Write(context.Background(), func(tx *sql.Tx) error { return saveTask(context.Background(), tx, snapshot) })
	r.m.publishTask(snapshot)
}

func (r *run) checkpoint() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.task.CheckpointId
}

// userMessages returns what the user said in the Task so far.
func (r *run) userMessages() []string {
	steps, _, err := listSteps(context.Background(), r.m.cfg.DB.Read(), r.id)
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range steps {
		if s.Kind == aosv1.StepKind_STEP_KIND_USER_MESSAGE {
			out = append(out, s.Text)
		}
	}
	return out
}

// requeue records the step that continues a finished Task and queues it again.
func (m *Manager) requeue(ctx context.Context, t *aosv1.Task, interactive bool, step *aosv1.TaskStep, items []llm.Item) (*aosv1.Task, error) {
	m.mu.Lock()
	if _, ok := m.running[t.Id]; ok || slices.Contains(m.queue, t.Id) {
		m.mu.Unlock()
		return nil, fmt.Errorf("task %s is already continuing", t.Id)
	}
	at := timestamppb.New(now(m.cfg))
	step.Id, step.TaskId, step.CreatedAt = newID("s_"), t.Id, at
	raw, _ := json.Marshal(items)
	t.State, t.Summary, t.FinishedAt, t.Awaiting, t.Interactive, t.UpdatedAt = aosv1.TaskState_TASK_STATE_QUEUED, "", nil, nil, interactive, at
	err := m.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := insertStep(ctx, tx, step, string(raw)); err != nil {
			return err
		}
		return saveTask(ctx, tx, t)
	})
	if err == nil {
		m.queue = append(m.queue, t.Id)
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_TaskStep{TaskStep: &aosv1.TaskStepChanged{Step: step}}})
	m.publishTask(t)
	m.schedule()
	return t, nil
}

func stateName(s aosv1.TaskState) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s.String(), "TASK_STATE_")), "_", " ")
}

// Get returns a Task with its steps and Approvals.
func (m *Manager) Get(ctx context.Context, id string) (*aosv1.Task, []*aosv1.TaskStep, []*aosv1.Approval, error) {
	t, err := getTask(ctx, m.cfg.DB.Read(), id)
	if err != nil {
		return nil, nil, nil, err
	}
	steps, _, err := listSteps(ctx, m.cfg.DB.Read(), id)
	if err != nil {
		return nil, nil, nil, err
	}
	approvals, err := listApprovals(ctx, m.cfg.DB.Read(), "task_id = ?", id)
	return t, steps, approvals, err
}

// List returns the newest Tasks.
func (m *Manager) List(ctx context.Context, limit int) ([]*aosv1.Task, error) {
	if limit <= 0 {
		limit = 50
	}
	return listTasks(ctx, m.cfg.DB.Read(), "", limit)
}

// schedule starts queued Tasks while slots are free.
func (m *Manager) schedule() {
	maxTasks := m.settings().MaxTasks
	m.mu.Lock()
	defer m.mu.Unlock()
	for len(m.queue) > 0 && len(m.running) < maxTasks && m.ctx.Err() == nil {
		id := m.queue[0]
		m.queue = m.queue[1:]
		r := &run{m: m, id: id}
		r.ctx, r.cancel = context.WithCancel(m.ctx)
		m.running[id] = r
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			r.execute()
			m.mu.Lock()
			delete(m.running, id)
			m.mu.Unlock()
			m.schedule()
		}()
	}
}

func (m *Manager) publishTask(t *aosv1.Task) {
	m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_TaskChanged{TaskChanged: &aosv1.TaskChanged{Task: proto.Clone(t).(*aosv1.Task)}}})
}

// run is one Task being worked on.
type run struct {
	m      *Manager
	id     string
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	task *aosv1.Task
	// model is the Task's model for this run: what the Agent asks for, and
	// what its usage is priced at.
	model     string
	env       *tool.Env
	grants    []policy.Grant
	cancelled bool
	// costPause is the Task cost at which the next Cost Limit pause happens;
	// dailyAck is the day the user let the Task continue past the daily limit.
	costPause float64
	dailyAck  string
}

func (r *run) execute() {
	cfg := r.m.cfg
	t, err := getTask(r.ctx, cfg.DB.Read(), r.id)
	if err != nil {
		return
	}
	r.task = t
	r.setState(aosv1.TaskState_TASK_STATE_RUNNING, "")

	env := &tool.Env{TaskID: t.Id, TaskTitle: t.Title, Home: cfg.Home, Interactive: t.Interactive, AskUser: r.askUser,
		UserMessages: r.userMessages, SetCheckpoint: r.setCheckpoint}
	r.env = env
	release := func() {}
	if cfg.NewEnv != nil {
		if release, err = cfg.NewEnv(env); err != nil {
			r.setState(aosv1.TaskState_TASK_STATE_FAILED, "could not prepare the Task: "+err.Error())
			return
		}
	}
	defer release()

	_, stored, err := listSteps(r.ctx, cfg.DB.Read(), t.Id)
	if err != nil {
		r.setState(aosv1.TaskState_TASK_STATE_FAILED, "could not read the Task: "+err.Error())
		return
	}
	transcript := buildTranscript(stored)
	s := r.m.settings()
	r.model = t.Model
	if r.model == "" {
		r.model = s.Model
	}
	ac := agent.Config{Provider: cfg.Provider, Tools: cfg.Tools, Model: r.model, ReasoningEffort: s.ReasoningEffort, Instructions: cfg.Instructions, MaxRetries: s.MaxRetries}
	final, err := agent.Run(r.ctx, ac, r, transcript)
	r.mu.Lock()
	cancelled := r.cancelled
	r.mu.Unlock()
	switch {
	case err == nil:
		r.setState(aosv1.TaskState_TASK_STATE_SUCCEEDED, final)
	case cancelled:
		summary := "Cancelled by the user."
		if id := r.checkpoint(); id != "" {
			// PLAN.md §8.1: after a cancel, offer the Restore.
			summary += fmt.Sprintf(" It had changed software: aos checkpoint restore %s undoes that.", id)
		}
		r.setState(aosv1.TaskState_TASK_STATE_CANCELLED, summary)
	case r.m.ctx.Err() != nil:
		r.setState(aosv1.TaskState_TASK_STATE_INTERRUPTED, "AOS stopped while the Task was running.")
	default:
		r.setState(aosv1.TaskState_TASK_STATE_FAILED, fmt.Sprintf("The Agent stopped: %v", err))
	}
}

// Cancel stops a queued, running or awaiting Task.
func (m *Manager) Cancel(ctx context.Context, id string) error {
	m.mu.Lock()
	if r, ok := m.running[id]; ok {
		m.mu.Unlock()
		r.mu.Lock()
		already := r.cancelled
		r.cancelled = true
		r.mu.Unlock()
		if already {
			// It was cancelled a moment ago and is still unwinding; there is
			// nothing left for a second cancel to stop.
			return fmt.Errorf("task %s has already been cancelled", id)
		}
		r.cancel()
		// The Agent can take seconds to unwind — a model stream to close, a
		// command to die — and the user should not watch "Running" all that
		// while (PLAN.md M4.8 item 8.12). Say Cancelled now; the run writes the
		// state again when it returns, with the Restore hint if it has one.
		r.setState(aosv1.TaskState_TASK_STATE_CANCELLED, "Cancelled by the user.")
		return nil
	}
	for i, q := range m.queue {
		if q == id {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			m.mu.Unlock()
			t, err := getTask(ctx, m.cfg.DB.Read(), id)
			if err != nil {
				return err
			}
			r := &run{m: m, id: id, task: t}
			r.setState(aosv1.TaskState_TASK_STATE_CANCELLED, "Cancelled before it started.")
			return nil
		}
	}
	m.mu.Unlock()
	if _, err := getTask(ctx, m.cfg.DB.Read(), id); err != nil {
		return err
	}
	return fmt.Errorf("task %s is not running", id)
}

// StopAll cancels every queued, running and awaiting Task.
func (m *Manager) StopAll(ctx context.Context) (int, error) {
	m.mu.Lock()
	ids := append([]string{}, m.queue...)
	for id := range m.running {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	n := 0
	for _, id := range ids {
		if err := m.Cancel(ctx, id); err == nil {
			n++
		}
	}
	return n, nil
}

// Delete removes a finished Task and everything that hangs off it. It refuses
// while the Task is queued, running or waiting for the user, so a live Task is
// never pulled out from under its Agent: cancel it first. The Audit Log keeps
// its record, being append-only.
func (m *Manager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	busy := slices.Contains(m.queue, id)
	if _, ok := m.running[id]; ok {
		busy = true
	}
	if _, ok := m.asking[id]; ok {
		busy = true
	}
	for _, w := range m.waiting {
		if w.run.id == id {
			busy = true
			break
		}
	}
	m.mu.Unlock()
	if busy {
		return fmt.Errorf("task %s is still active; cancel it first", id)
	}
	// Confirm it exists (and is not one we just missed) before the write.
	if _, err := getTask(ctx, m.cfg.DB.Read(), id); err != nil {
		return err
	}
	if err := m.cfg.DB.Write(ctx, func(tx *sql.Tx) error { return deleteTask(ctx, tx, id) }); err != nil {
		return err
	}
	m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_TaskChanged{TaskChanged: &aosv1.TaskChanged{Task: &aosv1.Task{Id: id}, Removed: true}}})
	return nil
}

// setState changes the Task's state; finished states record the summary.
func (r *run) setState(state aosv1.TaskState, summary string) {
	r.mu.Lock()
	t := r.task
	t.State = state
	t.UpdatedAt = timestamppb.New(now(r.m.cfg))
	if finished(state) {
		t.Summary = summary
		t.FinishedAt = t.UpdatedAt
		t.Awaiting = nil
	}
	snapshot := proto.Clone(t).(*aosv1.Task)
	r.mu.Unlock()
	// Saved with a background context: a cancelled Task must still record its state.
	_ = r.m.cfg.DB.Write(context.Background(), func(tx *sql.Tx) error { return saveTask(context.Background(), tx, snapshot) })
	r.m.publishTask(snapshot)
}

func (r *run) AddStep(ctx context.Context, s *aosv1.TaskStep, items []llm.Item) error {
	s.Id, s.TaskId, s.CreatedAt = newID("s_"), r.id, timestamppb.New(now(r.m.cfg))
	raw, _ := json.Marshal(items)
	if err := r.m.cfg.DB.Write(context.Background(), func(tx *sql.Tx) error { return insertStep(ctx, tx, s, string(raw)) }); err != nil {
		return err
	}
	r.publishStep(s)
	return nil
}

// UpdateStep saves s; nil items keep the stored ones.
func (r *run) UpdateStep(ctx context.Context, s *aosv1.TaskStep, items []llm.Item) error {
	var raw []byte
	if items != nil {
		raw, _ = json.Marshal(items)
	}
	if err := r.m.cfg.DB.Write(context.Background(), func(tx *sql.Tx) error { return saveStep(ctx, tx, s, string(raw)) }); err != nil {
		return err
	}
	r.publishStep(s)
	return nil
}

func (r *run) publishStep(s *aosv1.TaskStep) {
	r.m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_TaskStep{TaskStep: &aosv1.TaskStepChanged{Step: proto.Clone(s).(*aosv1.TaskStep)}}})
}

func (r *run) TextDelta(stepID, delta string) {
	r.m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_TextDelta{TextDelta: &aosv1.TextDelta{TaskId: r.id, StepId: stepID, Delta: delta}}})
}

// Responded adds a response's usage and estimated cost to the Task and to
// today's totals, and shows it live.
func (r *run) Responded(ctx context.Context, resp llm.Response) error {
	cost, known, err := r.m.cfg.Usage.Record(ctx, r.model, resp.Usage)
	if err != nil {
		return err
	}
	r.mu.Lock()
	u := r.task.Usage
	if u == nil {
		u = &aosv1.Usage{CostKnown: true}
		r.task.Usage = u
	}
	u.InputTokens += resp.Usage.InputTokens
	u.CachedInputTokens += resp.Usage.CachedInputTokens
	u.OutputTokens += resp.Usage.OutputTokens
	u.ReasoningTokens += resp.Usage.ReasoningTokens
	u.CostUsd += cost
	u.CostKnown = u.CostKnown && known
	snapshot := proto.Clone(r.task).(*aosv1.Task)
	r.mu.Unlock()
	if err := r.m.cfg.DB.Write(ctx, func(tx *sql.Tx) error { return saveTask(ctx, tx, snapshot) }); err != nil {
		return err
	}
	r.m.publishTask(snapshot)
	return nil
}

// Budget implements agent.Host: before each model request, a reached Cost
// Limit pauses the Task until the user lets it continue (PLAN.md §8.4).
func (r *run) Budget(ctx context.Context) error {
	cfg, s := r.m.cfg, r.m.settings()
	if limit := s.TaskCostLimit; limit > 0 {
		r.mu.Lock()
		cost := r.task.GetUsage().GetCostUsd()
		if r.costPause == 0 {
			r.costPause = limit
		}
		next := r.costPause
		r.mu.Unlock()
		if cost >= next {
			why := fmt.Sprintf("This Task's estimated cost reached $%.2f, its Cost Limit ($%.2f).", cost, limit)
			if err := r.pauseForCost(ctx, why, fmt.Sprintf("Reply to continue until it reaches $%.2f, or cancel the Task.", cost+limit)); err != nil {
				return err
			}
			r.mu.Lock()
			r.costPause = cost + limit
			r.mu.Unlock()
		}
	}
	if limit := s.DailyCostLimit; limit > 0 {
		day := now(cfg).Format("2006-01-02")
		r.mu.Lock()
		acked := r.dailyAck == day
		r.mu.Unlock()
		if acked {
			return nil
		}
		today, err := cfg.Usage.Today(ctx)
		if err != nil {
			return err
		}
		if today.CostUsd >= limit {
			why := fmt.Sprintf("Today's estimated model spend reached $%.2f, the daily Cost Limit ($%.2f).", today.CostUsd, limit)
			if err := r.pauseForCost(ctx, why, "Reply to let this Task continue today, or cancel it."); err != nil {
				return err
			}
			r.mu.Lock()
			r.dailyAck = day
			r.mu.Unlock()
		}
	}
	return nil
}

func (r *run) pauseForCost(ctx context.Context, why, ask string) error {
	if _, err := r.Pause(ctx, aosv1.AwaitingKind_AWAITING_KIND_COST_LIMIT, why+"\n"+ask); err != nil {
		return fmt.Errorf("%s (%w)", why, err)
	}
	return nil
}

func finished(s aosv1.TaskState) bool {
	switch s {
	case aosv1.TaskState_TASK_STATE_SUCCEEDED, aosv1.TaskState_TASK_STATE_FAILED, aosv1.TaskState_TASK_STATE_CANCELLED, aosv1.TaskState_TASK_STATE_INTERRUPTED:
		return true
	}
	return false
}

// title is the first line of the prompt, shortened.
func title(prompt string) string {
	line, _, _ := strings.Cut(prompt, "\n")
	if r := []rune(line); len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return line
}

func toProto(a policy.Autonomy) aosv1.Autonomy {
	switch a {
	case policy.Auto:
		return aosv1.Autonomy_AUTONOMY_AUTO
	case policy.ConfirmAll:
		return aosv1.Autonomy_AUTONOMY_CONFIRM_ALL
	}
	return aosv1.Autonomy_AUTONOMY_CONFIRM_RISKY
}

func (r *run) Env() *tool.Env { return r.env }

func (r *run) Decide(c policy.Call) policy.Decision {
	cfg := r.m.cfg
	protection := policy.NewProtection(cfg.Home, nil)
	if cfg.Protection != nil {
		protection = cfg.Protection(r.id)
	}
	r.mu.Lock()
	grants := append([]policy.Grant{}, r.grants...)
	r.mu.Unlock()
	return policy.Decide(c, policy.Context{
		Autonomy:    fromProto(r.task.Autonomy),
		Interactive: r.task.Interactive,
		Landlock:    cfg.Landlock,
		Grants:      grants,
		Protected:   protection,
	})
}

func (r *run) Approve(ctx context.Context, step *aosv1.TaskStep, c *tool.Call, d policy.Decision) (aosv1.ApprovalDecision, error) {
	m := r.m
	a := &aosv1.Approval{Id: newID("a_"), TaskId: r.id, StepId: step.Id, Tool: c.Policy.Tool, Summary: c.Summary,
		Reasons: d.Reasons, ProtectedPaths: d.ProtectedPaths, Grantable: d.Grantable, CreatedAt: timestamppb.New(now(m.cfg))}
	w := &waiter{run: r, call: c.Policy, decision: make(chan aosv1.ApprovalDecision, 1)}
	m.mu.Lock()
	m.waiting[a.Id] = w
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.waiting, a.Id)
		m.mu.Unlock()
	}()
	if err := m.cfg.DB.Write(ctx, func(tx *sql.Tx) error { return insertApproval(ctx, tx, a) }); err != nil {
		return 0, err
	}
	step.ToolCall.Status = aosv1.ToolCallStatus_TOOL_CALL_STATUS_AWAITING_APPROVAL
	_ = r.UpdateStep(ctx, step, nil)
	m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_Approval{Approval: &aosv1.ApprovalChanged{Approval: a}}})
	r.await(&aosv1.Awaiting{ApprovalId: a.Id, Kind: aosv1.AwaitingKind_AWAITING_KIND_APPROVAL})
	select {
	case decision := <-w.decision:
		r.await(nil)
		return decision, nil
	case <-ctx.Done():
		at := store.Millis(now(m.cfg))
		_ = m.cfg.DB.Write(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE approvals SET decision = ?, decided_by = 'aos: the Task stopped', decided_at = ? WHERE id = ? AND decision = 0`,
				int32(aosv1.ApprovalDecision_APPROVAL_DECISION_DENY), at, a.Id)
			return err
		})
		return 0, ctx.Err()
	}
}

// await marks the Task Awaiting User (or Running again when a is nil).
func (r *run) await(a *aosv1.Awaiting) {
	r.mu.Lock()
	r.task.Awaiting = a
	r.mu.Unlock()
	state := aosv1.TaskState_TASK_STATE_AWAITING_USER
	if a == nil {
		state = aosv1.TaskState_TASK_STATE_RUNNING
	}
	r.setState(state, "")
}

// Decide records the user's decision on a pending Approval.
func (m *Manager) Decide(ctx context.Context, approvalID string, decision aosv1.ApprovalDecision, by string) (*aosv1.Approval, error) {
	if decision == aosv1.ApprovalDecision_APPROVAL_DECISION_UNSPECIFIED {
		return nil, errors.New("no decision given")
	}
	m.mu.Lock()
	w, ok := m.waiting[approvalID]
	if ok {
		delete(m.waiting, approvalID)
	}
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("approval %s: %w (already decided, or its Task is no longer waiting)", approvalID, ErrNotFound)
	}
	a, err := scanApproval(m.cfg.DB.Read().QueryRowContext(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE id = ?`, approvalID))
	if err != nil {
		return nil, err
	}
	if decision == aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK && !a.Grantable {
		decision = aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_ONCE
	}
	a.Decision, a.DecidedBy, a.DecidedAt = decision, by, timestamppb.New(now(m.cfg))
	err = m.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE approvals SET decision = ?, decided_by = ?, decided_at = ? WHERE id = ?`,
			int32(decision), by, store.Millis(a.DecidedAt.AsTime()), a.Id); err != nil {
			return err
		}
		if decision != aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO grants (task_id, tool, folder, created_at) VALUES (?, ?, ?, ?)`,
			a.TaskId, w.call.Tool, w.call.Folder, store.Millis(a.DecidedAt.AsTime()))
		return err
	})
	if err != nil {
		return nil, err
	}
	if decision == aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK {
		w.run.mu.Lock()
		w.run.grants = append(w.run.grants, policy.Grant{Tool: w.call.Tool, Folder: w.call.Folder})
		w.run.mu.Unlock()
	}
	_ = m.cfg.Audit.Record(ctx, audit.Entry{TaskID: a.TaskId, StepID: a.StepId, Tool: a.Tool, Decision: decisionName(decision), DecidedBy: by, Result: "Approval: " + a.Summary, Actor: by})
	m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_Approval{Approval: &aosv1.ApprovalChanged{Approval: a}}})
	w.decision <- decision
	return a, nil
}

// Pending lists undecided Approvals, of one Task or of all.
func (m *Manager) Pending(ctx context.Context, taskID string) ([]*aosv1.Approval, error) {
	if taskID == "" {
		return listApprovals(ctx, m.cfg.DB.Read(), "decision = 0")
	}
	return listApprovals(ctx, m.cfg.DB.Read(), "decision = 0 AND task_id = ?", taskID)
}

func decisionName(d aosv1.ApprovalDecision) string {
	switch d {
	case aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_ONCE:
		return "allow"
	case aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK:
		return "allow-for-task"
	}
	return "deny"
}

func (r *run) Audit(ctx context.Context, e audit.Entry) {
	e.TaskID = r.id
	_ = r.m.cfg.Audit.Record(ctx, e)
}

func (r *run) Progress(stepID, url, path string, n, total int64) {
	r.m.cfg.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_DownloadProgress{DownloadProgress: &aosv1.DownloadProgress{
		TaskId: r.id, StepId: stepID, Url: url, Path: path, Bytes: n, Total: total}}})
}

func fromProto(a aosv1.Autonomy) policy.Autonomy {
	switch a {
	case aosv1.Autonomy_AUTONOMY_AUTO:
		return policy.Auto
	case aosv1.Autonomy_AUTONOMY_CONFIRM_ALL:
		return policy.ConfirmAll
	}
	return policy.ConfirmRisky
}

// askUser makes the Task Awaiting User with a question until Answer is called.
func (r *run) askUser(ctx context.Context, q string) (string, error) {
	return r.wait(ctx, aosv1.AwaitingKind_AWAITING_KIND_QUESTION, q)
}

// Pause implements agent.Host: the Task waits for the user's reply, if anyone
// can give one.
func (r *run) Pause(ctx context.Context, kind aosv1.AwaitingKind, text string) (string, error) {
	if !r.task.Interactive {
		return "", ErrNobodyToAsk
	}
	return r.wait(ctx, kind, text)
}

// wait makes the Task Awaiting User until Answer is called.
func (r *run) wait(ctx context.Context, kind aosv1.AwaitingKind, text string) (string, error) {
	m := r.m
	q := &question{kind: kind, answer: make(chan string, 1)}
	m.mu.Lock()
	m.asking[r.id] = q
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.asking, r.id)
		m.mu.Unlock()
	}()
	r.await(&aosv1.Awaiting{Question: text, Kind: kind})
	select {
	case answer := <-q.answer:
		r.await(nil)
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Answer replies to a Task waiting for the user: the answer to its question,
// or a hint after its Retries ran out.
func (m *Manager) Answer(ctx context.Context, taskID, text, by string) error {
	m.mu.Lock()
	q, ok := m.asking[taskID]
	if ok {
		delete(m.asking, taskID)
	}
	r := m.running[taskID]
	m.mu.Unlock()
	if !ok || r == nil {
		return fmt.Errorf("task %s: %w (it is not waiting for an answer)", taskID, ErrNotFound)
	}
	// An answer to ask_user reaches the model as the call's output; a hint as a
	// message of its own.
	var items []llm.Item
	tool := "ask_user"
	switch q.kind {
	case aosv1.AwaitingKind_AWAITING_KIND_RETRIES:
		items, tool = []llm.Item{llm.UserMessage(text)}, "retry_guard"
	case aosv1.AwaitingKind_AWAITING_KIND_COST_LIMIT:
		tool = "cost_limit"
	}
	step := &aosv1.TaskStep{Kind: aosv1.StepKind_STEP_KIND_USER_MESSAGE, Text: text}
	if err := r.AddStep(ctx, step, items); err != nil {
		return err
	}
	_ = m.cfg.Audit.Record(ctx, audit.Entry{TaskID: taskID, StepID: step.Id, Tool: tool, Decision: "answer", DecidedBy: by, Result: text, Actor: by})
	q.answer <- text
	return nil
}

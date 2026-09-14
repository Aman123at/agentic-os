package task

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/store"
)

// ErrNotFound is returned for unknown Tasks and Approvals.
var ErrNotFound = errors.New("not found")

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func ts(ms int64) *timestamppb.Timestamp { return timestamppb.New(store.Time(ms)) }

func nullTS(ms sql.NullInt64) *timestamppb.Timestamp {
	if !ms.Valid {
		return nil
	}
	return ts(ms.Int64)
}

func millisOf(t *timestamppb.Timestamp) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: store.Millis(t.AsTime()), Valid: true}
}

const taskColumns = `id, title, prompt, state, autonomy, interactive, summary, model, awaiting_approval_id, awaiting_question,
	input_tokens, cached_input_tokens, output_tokens, reasoning_tokens, created_at, updated_at, finished_at`

func scanTask(row interface{ Scan(...any) error }) (*aosv1.Task, error) {
	t := &aosv1.Task{Usage: &aosv1.Usage{}, Awaiting: &aosv1.Awaiting{}}
	var state, autonomy int32
	var created, updated int64
	var finished sql.NullInt64
	err := row.Scan(&t.Id, &t.Title, &t.Prompt, &state, &autonomy, &t.Interactive, &t.Summary, &t.Model,
		&t.Awaiting.ApprovalId, &t.Awaiting.Question,
		&t.Usage.InputTokens, &t.Usage.CachedInputTokens, &t.Usage.OutputTokens, &t.Usage.ReasoningTokens,
		&created, &updated, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.State, t.Autonomy = aosv1.TaskState(state), aosv1.Autonomy(autonomy)
	t.CreatedAt, t.UpdatedAt, t.FinishedAt = ts(created), ts(updated), nullTS(finished)
	if t.Awaiting.ApprovalId == "" && t.Awaiting.Question == "" {
		t.Awaiting = nil
	}
	return t, nil
}

func insertTask(ctx context.Context, tx *sql.Tx, t *aosv1.Task) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, title, prompt, state, autonomy, interactive, model, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Id, t.Title, t.Prompt, int32(t.State), int32(t.Autonomy), t.Interactive, t.Model,
		store.Millis(t.CreatedAt.AsTime()), store.Millis(t.UpdatedAt.AsTime()))
	return err
}

func saveTask(ctx context.Context, tx *sql.Tx, t *aosv1.Task) error {
	u := t.GetUsage()
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET state = ?, summary = ?, awaiting_approval_id = ?, awaiting_question = ?,
		input_tokens = ?, cached_input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, updated_at = ?, finished_at = ? WHERE id = ?`,
		int32(t.State), t.Summary, t.GetAwaiting().GetApprovalId(), t.GetAwaiting().GetQuestion(),
		u.GetInputTokens(), u.GetCachedInputTokens(), u.GetOutputTokens(), u.GetReasoningTokens(),
		store.Millis(t.UpdatedAt.AsTime()), millisOf(t.FinishedAt), t.Id)
	return err
}

func getTask(ctx context.Context, db *sql.DB, id string) (*aosv1.Task, error) {
	return scanTask(db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
}

func listTasks(ctx context.Context, db *sql.DB, where string, limit int, args ...any) ([]*aosv1.Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks`
	if where != "" {
		q += ` WHERE ` + where
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aosv1.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const stepColumns = `id, task_id, seq, kind, text, call_id, tool, arguments_json, status, result, output_ref, items_json, created_at, started_at, finished_at`

// insertStep assigns the next sequence number and stores the step with the
// model items it came from.
func insertStep(ctx context.Context, tx *sql.Tx, s *aosv1.TaskStep, items string) error {
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM task_steps WHERE task_id = ?`, s.TaskId).Scan(&s.Seq); err != nil {
		return err
	}
	c := s.GetToolCall()
	_, err := tx.ExecContext(ctx, `INSERT INTO task_steps (`+stepColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Id, s.TaskId, s.Seq, int32(s.Kind), s.Text, c.GetCallId(), c.GetTool(), c.GetArgumentsJson(), int32(c.GetStatus()),
		c.GetResult(), c.GetOutputRef(), items, store.Millis(s.CreatedAt.AsTime()), millisOf(c.GetStartedAt()), millisOf(c.GetFinishedAt()))
	return err
}

func saveStep(ctx context.Context, tx *sql.Tx, s *aosv1.TaskStep, items string) error {
	c := s.GetToolCall()
	_, err := tx.ExecContext(ctx, `UPDATE task_steps SET text = ?, status = ?, result = ?, output_ref = ?, items_json = COALESCE(NULLIF(?, ''), items_json), started_at = ?, finished_at = ? WHERE id = ?`,
		s.Text, int32(c.GetStatus()), c.GetResult(), c.GetOutputRef(), items, millisOf(c.GetStartedAt()), millisOf(c.GetFinishedAt()), s.Id)
	return err
}

func listSteps(ctx context.Context, db *sql.DB, taskID string) ([]*aosv1.TaskStep, []string, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+stepColumns+` FROM task_steps WHERE task_id = ? ORDER BY seq`, taskID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var steps []*aosv1.TaskStep
	var items []string
	for rows.Next() {
		s := &aosv1.TaskStep{}
		c := &aosv1.ToolCall{}
		var kind, status int32
		var created int64
		var started, finished sql.NullInt64
		var itemsJSON string
		if err := rows.Scan(&s.Id, &s.TaskId, &s.Seq, &kind, &s.Text, &c.CallId, &c.Tool, &c.ArgumentsJson, &status, &c.Result, &c.OutputRef, &itemsJSON, &created, &started, &finished); err != nil {
			return nil, nil, err
		}
		s.Kind, c.Status = aosv1.StepKind(kind), aosv1.ToolCallStatus(status)
		s.CreatedAt, c.StartedAt, c.FinishedAt = ts(created), nullTS(started), nullTS(finished)
		if s.Kind == aosv1.StepKind_STEP_KIND_TOOL_CALL {
			s.ToolCall = c
		}
		steps = append(steps, s)
		items = append(items, itemsJSON)
	}
	return steps, items, rows.Err()
}

const approvalColumns = `id, task_id, step_id, tool, summary, reasons_json, protected_paths_json, grantable, decision, decided_by, created_at, decided_at`

func scanApproval(row interface{ Scan(...any) error }) (*aosv1.Approval, error) {
	a := &aosv1.Approval{}
	var reasons, paths string
	var decision int32
	var created int64
	var decided sql.NullInt64
	err := row.Scan(&a.Id, &a.TaskId, &a.StepId, &a.Tool, &a.Summary, &reasons, &paths, &a.Grantable, &decision, &a.DecidedBy, &created, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(reasons), &a.Reasons)
	_ = json.Unmarshal([]byte(paths), &a.ProtectedPaths)
	a.Decision = aosv1.ApprovalDecision(decision)
	a.CreatedAt, a.DecidedAt = ts(created), nullTS(decided)
	return a, nil
}

func insertApproval(ctx context.Context, tx *sql.Tx, a *aosv1.Approval) error {
	reasons, _ := json.Marshal(nonNil(a.Reasons))
	paths, _ := json.Marshal(nonNil(a.ProtectedPaths))
	_, err := tx.ExecContext(ctx, `INSERT INTO approvals (`+approvalColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Id, a.TaskId, a.StepId, a.Tool, a.Summary, string(reasons), string(paths), a.Grantable, int32(a.Decision), a.DecidedBy,
		store.Millis(a.CreatedAt.AsTime()), millisOf(a.DecidedAt))
	return err
}

func listApprovals(ctx context.Context, db *sql.DB, where string, args ...any) ([]*aosv1.Approval, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aosv1.Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func now(cfg Config) time.Time {
	if cfg.Now != nil {
		return cfg.Now()
	}
	return time.Now()
}

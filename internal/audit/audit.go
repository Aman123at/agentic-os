// Package audit writes the append-only Audit Log (PLAN.md §7.9).
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/store"
)

// Entry is one record. Actor is who asked: "agent", "user:cli", "user:desktop", ….
type Entry struct {
	TaskID, StepID, Tool string
	Arguments            string
	Decision, DecidedBy  string
	Result               string
	Duration             time.Duration
	Actor                string
}

// Log is the Audit Log.
type Log struct {
	DB  *store.DB
	Now func() time.Time
}

// Record appends e, with secrets in its arguments redacted.
func (l *Log) Record(ctx context.Context, e Entry) error {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	return l.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (time, task_id, step_id, tool, arguments_json, decision, decided_by, result_summary, duration_ms, actor)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			store.Millis(now), e.TaskID, e.StepID, e.Tool, Redact(e.Arguments), e.Decision, e.DecidedBy, truncate(e.Result, 2000), e.Duration.Milliseconds(), e.Actor)
		return err
	})
}

// List returns entries newest first, optionally for one Task, before an id.
func (l *Log) List(ctx context.Context, taskID string, limit int, beforeID int64) ([]*aosv1.AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT id, time, task_id, step_id, tool, arguments_json, decision, decided_by, result_summary, duration_ms, actor FROM audit_log WHERE 1=1`
	var args []any
	if taskID != "" {
		q += ` AND task_id = ?`
		args = append(args, taskID)
	}
	if beforeID > 0 {
		q += ` AND id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.DB.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aosv1.AuditEntry
	for rows.Next() {
		var e aosv1.AuditEntry
		var ms int64
		if err := rows.Scan(&e.Id, &ms, &e.TaskId, &e.StepId, &e.Tool, &e.ArgumentsJson, &e.Decision, &e.DecidedBy, &e.ResultSummary, &e.DurationMs, &e.Actor); err != nil {
			return nil, err
		}
		e.Time = timestamppb.New(store.Time(ms))
		out = append(out, &e)
	}
	return out, rows.Err()
}

var (
	secretKey   = regexp.MustCompile(`(?i)(pass(word)?|secret|token|api[_-]?key|authorization|cookie|credential|private[_-]?key)`)
	secretValue = regexp.MustCompile(`(sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+|(?i:bearer)\s+[A-Za-z0-9._~+/=-]{8,})`)
)

// Redact replaces secret-looking values in JSON arguments (by key or by shape).
func Redact(argsJSON string) string {
	var v any
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return secretValue.ReplaceAllString(argsJSON, "[redacted]")
	}
	v = redact(v, "")
	b, err := json.Marshal(v)
	if err != nil {
		return "[redacted]"
	}
	return string(b)
}

func redact(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = redact(val, k)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = redact(val, key)
		}
		return x
	case string:
		if key != "" && secretKey.MatchString(key) && x != "" {
			return "[redacted]"
		}
		return secretValue.ReplaceAllString(x, "[redacted]")
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

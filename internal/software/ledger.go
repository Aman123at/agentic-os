// Package software keeps the Install Ledger (ADR-0003, PLAN.md §11): every
// operation with root authority is recorded with the packages, /etc files and
// Services it changed, before and after. A Checkpoint is a position in the
// Ledger; Restore undoes what came after one, and Replay re-creates the
// Ledger's final state when a new container starts.
package software

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/store"
)

// Kinds of things the Ledger records.
const (
	KindPackage = "package"
	KindFile    = "file"
	KindService = "service"
)

// Change is one thing an operation changed. Before and After are JSON
// states; "" means absent.
type Change struct {
	Kind, Manager, Name string
	Before, After       string
}

// Key identifies what a Change is about.
type Key struct{ Kind, Manager, Name string }

func (c Change) Key() Key { return Key{c.Kind, c.Manager, c.Name} }

// Op is one Ledger operation.
type Op struct {
	ID      int64
	Time    time.Time
	TaskID  string
	Action  string // install | remove | command | service | restore
	Summary string
	Actor   string
	Changes []Change
}

// Final returns the state each thing has after ops, oldest first: what
// Replay re-creates.
func Final(ops []Op) map[Key]string {
	out := map[Key]string{}
	for _, op := range ops {
		for _, c := range op.Changes {
			out[c.Key()] = c.After
		}
	}
	return out
}

// Undo returns, for each thing ops changed (oldest first, all after a
// Checkpoint), the state it had before the first of them: what Restore
// returns to.
func Undo(ops []Op) map[Key]string {
	out := map[Key]string{}
	for _, op := range ops {
		for _, c := range op.Changes {
			if _, seen := out[c.Key()]; !seen {
				out[c.Key()] = c.Before
			}
		}
	}
	return out
}

// First returns the state each thing had before the Ledger first changed it.
func First(ops []Op) map[Key]string { return Undo(ops) }

// ErrNoCheckpoint is returned for unknown Checkpoints.
var ErrNoCheckpoint = errors.New("no such Checkpoint")

// Ledger reads and appends the Install Ledger.
type Ledger struct {
	DB  *store.DB
	Now func() time.Time
}

func (l *Ledger) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Append records op and sets its ID.
func (l *Ledger) Append(ctx context.Context, op *Op) error {
	if op.Time.IsZero() {
		op.Time = l.now()
	}
	return l.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO ledger_ops (time, task_id, action, summary, actor) VALUES (?, ?, ?, ?, ?)`,
			store.Millis(op.Time), op.TaskID, op.Action, op.Summary, op.Actor)
		if err != nil {
			return err
		}
		if op.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		for _, c := range op.Changes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries (op_id, kind, manager, name, before_state, after_state) VALUES (?, ?, ?, ?, ?, ?)`,
				op.ID, c.Kind, c.Manager, c.Name, c.Before, c.After); err != nil {
				return err
			}
		}
		return nil
	})
}

// Head returns the id of the newest operation, 0 for an empty Ledger.
func (l *Ledger) Head(ctx context.Context) (int64, error) {
	var id int64
	err := l.DB.Read().QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM ledger_ops`).Scan(&id)
	return id, err
}

// After returns the operations after id, oldest first.
func (l *Ledger) After(ctx context.Context, id int64) ([]Op, error) {
	return l.load(ctx, `WHERE id > ? ORDER BY id`, id)
}

// List returns up to limit operations before beforeID (0: the newest), newest first.
func (l *Ledger) List(ctx context.Context, limit int, beforeID int64) ([]Op, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if beforeID <= 0 {
		beforeID = 1 << 62
	}
	return l.load(ctx, `WHERE id < ? ORDER BY id DESC LIMIT ?`, beforeID, limit)
}

func (l *Ledger) load(ctx context.Context, where string, args ...any) ([]Op, error) {
	rows, err := l.DB.Read().QueryContext(ctx, `SELECT id, time, task_id, action, summary, actor FROM ledger_ops `+where, args...)
	if err != nil {
		return nil, err
	}
	var ops []Op
	index := map[int64]int{}
	lo, hi := int64(1<<62), int64(0)
	for rows.Next() {
		var op Op
		var ms int64
		if err := rows.Scan(&op.ID, &ms, &op.TaskID, &op.Action, &op.Summary, &op.Actor); err != nil {
			rows.Close()
			return nil, err
		}
		op.Time = store.Time(ms)
		index[op.ID] = len(ops)
		ops = append(ops, op)
		lo, hi = min(lo, op.ID), max(hi, op.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ops) == 0 {
		return ops, err
	}
	rows, err = l.DB.Read().QueryContext(ctx, `SELECT op_id, kind, manager, name, before_state, after_state FROM ledger_entries
		WHERE op_id BETWEEN ? AND ? ORDER BY op_id, kind, manager, name`, lo, hi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c Change
		if err := rows.Scan(&id, &c.Kind, &c.Manager, &c.Name, &c.Before, &c.After); err != nil {
			return nil, err
		}
		if i, ok := index[id]; ok {
			ops[i].Changes = append(ops[i].Changes, c)
		}
	}
	return ops, rows.Err()
}

// Packages returns the software the Ledger says the Machine has.
func (l *Ledger) Packages(ctx context.Context) ([]*aosv1.Package, error) {
	ops, err := l.After(ctx, 0)
	if err != nil {
		return nil, err
	}
	type last struct {
		state string
		op    int64
	}
	final := map[Key]last{}
	for _, op := range ops {
		for _, c := range op.Changes {
			if c.Kind == KindPackage {
				final[c.Key()] = last{c.After, op.ID}
			}
		}
	}
	var out []*aosv1.Package
	for k, v := range final {
		if v.state == "" {
			continue
		}
		var p Pkg
		_ = json.Unmarshal([]byte(v.state), &p)
		out = append(out, &aosv1.Package{Manager: k.Manager, Name: k.Name, Version: p.Version, Auto: p.Auto, LedgerId: v.op})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Manager != out[j].Manager {
			return out[i].Manager < out[j].Manager
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ---------------------------------------------------------------- Checkpoints

// CreateCheckpoint names the Ledger's current position.
func (l *Ledger) CreateCheckpoint(ctx context.Context, name, taskID string, automatic bool) (*aosv1.Checkpoint, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Checkpoint " + l.now().Format("Jan 2 15:04")
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	c := &aosv1.Checkpoint{Id: "c_" + hex.EncodeToString(b), Name: name, TaskId: taskID, Automatic: automatic, CreatedAt: timestamppb.New(l.now())}
	err := l.DB.Write(ctx, func(tx *sql.Tx) error {
		// Read inside the write transaction: no operation can slip in between.
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM ledger_ops`).Scan(&c.LedgerId); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO checkpoints (id, name, ledger_id, task_id, automatic, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			c.Id, c.Name, c.LedgerId, c.TaskId, c.Automatic, store.Millis(c.CreatedAt.AsTime()))
		return err
	})
	return c, err
}

// Checkpoints returns every Checkpoint, newest first.
func (l *Ledger) Checkpoints(ctx context.Context) ([]*aosv1.Checkpoint, error) {
	return l.checkpoints(ctx, `ORDER BY created_at DESC, ledger_id DESC`)
}

// Checkpoint returns the Checkpoint with id, or the only one whose id starts with it.
func (l *Ledger) Checkpoint(ctx context.Context, id string) (*aosv1.Checkpoint, error) {
	cs, err := l.checkpoints(ctx, `WHERE id = ? OR id LIKE ? ESCAPE '\' ORDER BY id = ? DESC`, id, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(id)+"%", id)
	switch {
	case err != nil:
		return nil, err
	case id == "" || len(cs) == 0:
		return nil, fmt.Errorf("%q: %w", id, ErrNoCheckpoint)
	case cs[0].Id != id && len(cs) > 1:
		return nil, fmt.Errorf("%q matches %d Checkpoints; give more of the id", id, len(cs))
	}
	return cs[0], nil
}

func (l *Ledger) checkpoints(ctx context.Context, rest string, args ...any) ([]*aosv1.Checkpoint, error) {
	rows, err := l.DB.Read().QueryContext(ctx, `SELECT id, name, ledger_id, task_id, automatic, created_at FROM checkpoints `+rest, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aosv1.Checkpoint
	for rows.Next() {
		c := &aosv1.Checkpoint{}
		var ms int64
		if err := rows.Scan(&c.Id, &c.Name, &c.LedgerId, &c.TaskId, &c.Automatic, &ms); err != nil {
			return nil, err
		}
		c.CreatedAt = timestamppb.New(store.Time(ms))
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- for the API

// Proto converts an operation for the API, with its states described in short.
func (op Op) Proto() *aosv1.LedgerOp {
	p := &aosv1.LedgerOp{Id: op.ID, Time: timestamppb.New(op.Time), TaskId: op.TaskID, Action: op.Action, Summary: op.Summary, Actor: op.Actor}
	for _, c := range op.Changes {
		p.Changes = append(p.Changes, &aosv1.LedgerChange{Kind: c.Kind, Manager: c.Manager, Name: c.Name,
			Before: Describe(c.Kind, c.Before), After: Describe(c.Kind, c.After)})
	}
	return p
}

// Describe says what a state is, in a few words; "" for absent.
func Describe(kind, state string) string {
	if state == "" {
		return ""
	}
	switch kind {
	case KindPackage:
		var p Pkg
		if json.Unmarshal([]byte(state), &p) == nil {
			if p.Auto {
				return p.Version + " (dependency)"
			}
			return p.Version
		}
	case KindFile:
		var f FileState
		if json.Unmarshal([]byte(state), &f) == nil {
			switch f.Type {
			case "dir":
				return "folder"
			case "symlink":
				return "link to " + f.Target
			}
			return fmt.Sprintf("file %.12s", f.Blob)
		}
	case KindService:
		var s struct{ Command string }
		if json.Unmarshal([]byte(state), &s) == nil {
			return s.Command
		}
	}
	return state
}

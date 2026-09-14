package profile

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/store"
)

// ErrNoMemory is returned for unknown Memory entries.
var ErrNoMemory = errors.New("no such Memory entry")

// Memory statuses.
const (
	Proposed = "proposed"
	Accepted = "accepted"
)

// Memories stores Memory: the Agents' proposals and the entries the user
// accepted or wrote, which every Agent is given (PLAN.md §8.5).
type Memories struct {
	DB  *store.DB
	Bus *events.Bus
	Now func() time.Time
}

func (s *Memories) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Propose records an Agent's proposal and asks the user about it.
func (s *Memories) Propose(ctx context.Context, taskID, text string) (*aosv1.Memory, error) {
	m, err := s.insert(ctx, taskID, text, Proposed)
	if err == nil && s.Bus != nil {
		s.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_Notification{Notification: &aosv1.Notification{
			Title: "Remember this?", Body: m.Text, TaskId: taskID, MemoryId: m.Id}}})
	}
	return m, err
}

// Add saves an entry directly: the user wrote it, or asked the Agent to remember it.
func (s *Memories) Add(ctx context.Context, taskID, text string) (*aosv1.Memory, error) {
	return s.insert(ctx, taskID, text, Accepted)
}

func (s *Memories) insert(ctx context.Context, taskID, text, status string) (*aosv1.Memory, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("the Memory entry is empty")
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	m := &aosv1.Memory{Id: "m_" + hex.EncodeToString(b), Text: text, Status: status, TaskId: taskID, CreatedAt: timestamppb.New(s.now())}
	err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO memories (id, text, status, task_id, created_at) VALUES (?, ?, ?, ?, ?)`,
			m.Id, m.Text, m.Status, m.TaskId, store.Millis(m.CreatedAt.AsTime()))
		return err
	})
	return m, err
}

// Accept saves a proposal.
func (s *Memories) Accept(ctx context.Context, id string) (*aosv1.Memory, error) {
	err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE memories SET status = ? WHERE id = ?`, Accepted, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%s: %w", id, ErrNoMemory)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

// Forget removes an entry or rejects a proposal.
func (s *Memories) Forget(ctx context.Context, id string) error {
	return s.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%s: %w", id, ErrNoMemory)
		}
		return nil
	})
}

// List returns every entry: accepted ones first, then proposals, oldest first.
func (s *Memories) List(ctx context.Context) ([]*aosv1.Memory, error) {
	return s.query(ctx, `SELECT id, text, status, task_id, created_at FROM memories ORDER BY status = 'proposed', created_at`)
}

// Accepted returns the texts every Agent is given.
func (s *Memories) Accepted(ctx context.Context) ([]string, error) {
	ms, err := s.query(ctx, `SELECT id, text, status, task_id, created_at FROM memories WHERE status = 'accepted' ORDER BY created_at`)
	texts := make([]string, len(ms))
	for i, m := range ms {
		texts[i] = m.Text
	}
	return texts, err
}

func (s *Memories) get(ctx context.Context, id string) (*aosv1.Memory, error) {
	ms, err := s.query(ctx, `SELECT id, text, status, task_id, created_at FROM memories WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("%s: %w", id, ErrNoMemory)
	}
	return ms[0], nil
}

func (s *Memories) query(ctx context.Context, q string, args ...any) ([]*aosv1.Memory, error) {
	rows, err := s.DB.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aosv1.Memory
	for rows.Next() {
		m := &aosv1.Memory{}
		var created int64
		if err := rows.Scan(&m.Id, &m.Text, &m.Status, &m.TaskId, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = timestamppb.New(store.Time(created))
		out = append(out, m)
	}
	return out, rows.Err()
}

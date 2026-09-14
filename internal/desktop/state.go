// Package desktop stores the Desktop's saved layout (PLAN.md §4.3): one opaque
// JSON blob, owned by the client, restored on reload. aosd is single-user, so
// this is a single row.
package desktop

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/amantiwari/agentic-os/internal/store"
)

// MaxState caps a saved layout, so a runaway client cannot fill the database.
const MaxState = 256 << 10

// State reads and writes the Desktop's saved layout.
type State struct {
	DB  *store.DB
	Now func() time.Time
}

func (s *State) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Get returns the last saved layout, or "" if nothing has been saved yet.
func (s *State) Get(ctx context.Context) (string, error) {
	var state string
	err := s.DB.Read().QueryRowContext(ctx, `SELECT state FROM desktop_state WHERE id = 1`).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return state, err
}

// Save replaces the saved layout.
func (s *State) Save(ctx context.Context, state string) error {
	if len(state) > MaxState {
		return errors.New("the Desktop layout is too large to save")
	}
	return s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO desktop_state (id, state, saved_at) VALUES (1, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET state = excluded.state, saved_at = excluded.saved_at`,
			state, store.Millis(s.now()))
		return err
	})
}

// Package notify keeps the Desktop's notifications (PLAN.md §4.3): each one is
// saved, then published, and stays until the user dismisses it, so the
// Notification Center shows it again after a reload.
package notify

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/store"
)

// Keep is how many notifications are kept, dismissed or not; older ones are
// deleted, so a chatty Agent cannot fill the database.
const Keep = 200

// ErrNoNotification is returned for an unknown or already dismissed notification.
var ErrNoNotification = errors.New("no such notification")

// Center saves, publishes and dismisses notifications.
type Center struct {
	DB  *store.DB
	Bus *events.Bus
	Now func() time.Time
}

func (c *Center) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Post saves n with a new id and time, publishes it, and returns it.
func (c *Center) Post(ctx context.Context, n *aosv1.Notification) (*aosv1.Notification, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	now := c.now()
	saved := &aosv1.Notification{Id: "n_" + hex.EncodeToString(b), Title: n.Title, Body: n.Body, TaskId: n.TaskId,
		MemoryId: n.MemoryId, Port: n.Port, CreatedAt: timestamppb.New(now)}
	err := c.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notifications (id, title, body, task_id, memory_id, port, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			saved.Id, saved.Title, saved.Body, saved.TaskId, saved.MemoryId, saved.Port, store.Millis(now)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM notifications WHERE id NOT IN (SELECT id FROM notifications ORDER BY created_at DESC, rowid DESC LIMIT ?)`, Keep)
		return err
	})
	if err != nil {
		return nil, err
	}
	c.publish(saved)
	return saved, nil
}

// List returns the notifications not yet dismissed, newest first.
func (c *Center) List(ctx context.Context) ([]*aosv1.Notification, error) {
	rows, err := c.DB.Read().QueryContext(ctx,
		`SELECT id, title, body, task_id, memory_id, port, created_at FROM notifications WHERE dismissed_at IS NULL ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aosv1.Notification
	for rows.Next() {
		n := &aosv1.Notification{}
		var created int64
		if err := rows.Scan(&n.Id, &n.Title, &n.Body, &n.TaskId, &n.MemoryId, &n.Port, &created); err != nil {
			return nil, err
		}
		n.CreatedAt = timestamppb.New(store.Time(created))
		out = append(out, n)
	}
	return out, rows.Err()
}

// Dismiss dismisses one notification and tells every client.
func (c *Center) Dismiss(ctx context.Context, id string) error {
	ids, err := c.dismiss(ctx, `WHERE id = ? AND dismissed_at IS NULL`, id)
	if err == nil && len(ids) == 0 {
		err = ErrNoNotification
	}
	return err
}

// DismissAll dismisses every notification and tells every client.
func (c *Center) DismissAll(ctx context.Context) error {
	_, err := c.dismiss(ctx, `WHERE dismissed_at IS NULL`)
	return err
}

func (c *Center) dismiss(ctx context.Context, where string, args ...any) ([]string, error) {
	var ids []string
	err := c.DB.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE notifications SET dismissed_at = ? `+where+` RETURNING id`,
			append([]any{store.Millis(c.now())}, args...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		c.publish(&aosv1.Notification{Id: id, Dismissed: true})
	}
	return ids, nil
}

func (c *Center) publish(n *aosv1.Notification) {
	if c.Bus != nil {
		c.Bus.Publish(&aosv1.Event{Time: timestamppb.New(c.now()), Kind: &aosv1.Event_Notification{Notification: n}})
	}
}

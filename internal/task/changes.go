package task

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/tool"
)

// Before-images (PLAN.md M8.11): the files a Task changes with its Tools, each
// with what it was before the Task first changed it, kept in the Realm's
// outputs/<task>/before/ and indexed in task_file_changes, until the Task is
// deleted. Code's Change Review compares with them and puts them back.

// befores keeps one Task's before-images; it is the Task's tool.Befores.
type befores struct {
	m    *Manager
	task string
	dir  string

	mu  sync.Mutex
	has map[string]bool
}

// newBefores loads which files the Task already changed, in earlier runs too:
// a Follow-up's first change to a file is not its first.
func (m *Manager) newBefores(ctx context.Context, taskID string) (*befores, error) {
	if m.cfg.Outputs == "" {
		return nil, nil
	}
	changes, _, err := listChanges(ctx, m.cfg.DB.Read(), taskID, "")
	if err != nil {
		return nil, err
	}
	b := &befores{m: m, task: taskID, dir: m.beforeDir(taskID), has: map[string]bool{}}
	for _, c := range changes {
		b.has[c.Path] = true
	}
	return b, nil
}

func (m *Manager) beforeDir(taskID string) string {
	return filepath.Join(m.cfg.Outputs, taskID, "before")
}

// blobName names a path's before-image file.
func blobName(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:12])
}

func (b *befores) Has(path string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.has[path]
}

func (b *befores) Keep(ctx context.Context, x tool.Before) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.has[x.Path] {
		return nil
	}
	blob := ""
	if x.Kept {
		blob = blobName(x.Path)
		if err := os.MkdirAll(b.dir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(b.dir, blob), x.Content, 0o600); err != nil {
			return err
		}
	}
	c := &aosv1.TaskFileChange{Path: x.Path, Tool: x.Tool, Existed: x.Existed, Folder: x.Folder, Size: x.Size, Whole: x.Whole,
		ChangedAt: timestamppb.New(now(b.m.cfg))}
	if err := b.m.cfg.DB.Write(ctx, func(tx *sql.Tx) error { return insertChange(ctx, tx, b.task, c, blob) }); err != nil {
		return err
	}
	b.has[x.Path] = true
	return nil
}

// Changes lists the files a Task changed with its Tools, in the order it
// first changed them.
func (m *Manager) Changes(ctx context.Context, taskID string) ([]*aosv1.TaskFileChange, error) {
	if _, err := getTask(ctx, m.cfg.DB.Read(), taskID); err != nil {
		return nil, err
	}
	changes, _, err := listChanges(ctx, m.cfg.DB.Read(), taskID, "")
	return changes, err
}

// Before returns one changed file and what it was before: its content when it
// was kept and can be compared line by line, else none.
func (m *Manager) Before(ctx context.Context, taskID, path string) (*aosv1.TaskFileChange, []byte, error) {
	if _, err := getTask(ctx, m.cfg.DB.Read(), taskID); err != nil {
		return nil, nil, err
	}
	changes, blobs, err := listChanges(ctx, m.cfg.DB.Read(), taskID, filepath.Clean(path))
	if err != nil {
		return nil, nil, err
	}
	if len(changes) == 0 {
		return nil, nil, fmt.Errorf("the Task did not change %s: %w", path, ErrNotFound)
	}
	c := changes[0]
	if blobs[0] == "" || c.Whole {
		return c, nil, nil
	}
	content, err := os.ReadFile(filepath.Join(m.beforeDir(taskID), blobs[0]))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("the before-image of %s is missing: %w", path, ErrNotFound)
	}
	return c, content, err
}

// removeBefores deletes a deleted Task's before-images.
func (m *Manager) removeBefores(taskID string) {
	if m.cfg.Outputs != "" {
		_ = os.RemoveAll(m.beforeDir(taskID))
	}
}

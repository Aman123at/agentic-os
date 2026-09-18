package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/api"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/session"
	"github.com/Aman123at/agentic-os/internal/store"
)

// ---------------------------------------------------------------- paths the user locked

// locks stores the paths the user locked (🔒, aos protect).
type locks struct {
	db   *store.DB
	home string

	mu     sync.Mutex
	locked []string
}

func (l *locks) load() error {
	rows, err := l.db.Read().Query(`SELECT path FROM protected_paths ORDER BY path`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		paths = append(paths, p)
	}
	l.mu.Lock()
	l.locked = paths
	l.mu.Unlock()
	return rows.Err()
}

func (l *locks) paths() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.locked...)
}

// Protect implements api.Protected. Locks apply to Agent Sessions from their next command.
func (l *locks) Protect(ctx context.Context, path string) error {
	// A plain sentence, not the lstat error: this reaches the user in System
	// Settings and Finder (PLAN.md M4.8 item 8.13).
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("there is nothing at %s to lock", path)
		}
		return fmt.Errorf("%s cannot be read, so it cannot be locked", path)
	}
	if !strings.HasPrefix(path, l.home+"/") {
		return fmt.Errorf("only paths inside the home folder can be locked; %s is outside it", path)
	}
	err := l.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO protected_paths (path, created_at) VALUES (?, ?)`, path, store.Millis(time.Now()))
		return err
	})
	if err != nil {
		return err
	}
	return l.load()
}

// Unprotect implements api.Protected.
func (l *locks) Unprotect(ctx context.Context, path string) error {
	var n int64
	err := l.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM protected_paths WHERE path = ?`, path)
		if err == nil {
			n, err = res.RowsAffected()
		}
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s was not locked; default Protected Paths can't be unlocked", path)
	}
	return l.load()
}

// IsProtected implements api.Protected (for Finder's lock badge): it answers
// why path is protected — "user" for a path the user locked, "default" for a
// built-in Protected Path, "inherited" for anything inside one of those, and ""
// when it is not protected. Finder shows the badge for all three and offers
// Unprotect only for "user", since that is the only kind that can be unlocked.
func (l *locks) IsProtected(path string) string {
	path = filepath.Clean(path)
	user := l.paths()
	defaults := policy.DefaultPaths(l.home)
	for _, p := range user {
		if path == p {
			return "user"
		}
	}
	for _, p := range defaults {
		if path == p {
			return "default"
		}
	}
	for _, p := range append(user, defaults...) {
		if policy.Within(path, p) {
			return "inherited"
		}
	}
	return ""
}

// List implements api.Protected.
func (l *locks) List(context.Context) ([]*aosv1.ListProtectedResponse_Entry, error) {
	var out []*aosv1.ListProtectedResponse_Entry
	for _, p := range policy.DefaultPaths(l.home) {
		if p == "/shared" {
			p = "/shared (the Shared Folder)"
		}
		out = append(out, &aosv1.ListProtectedResponse_Entry{Path: p, Source: "default", Kernel: true})
	}
	for _, p := range l.paths() {
		out = append(out, &aosv1.ListProtectedResponse_Entry{Path: p, Source: "user", Kernel: true})
	}
	out = append(out,
		&aosv1.ListProtectedResponse_Entry{Path: "any .env file", Source: "pattern"},
		&aosv1.ListProtectedResponse_Entry{Path: "git working trees with changes a Task did not make (deleting or discarding)", Source: "pattern"})
	return out, nil
}

// ---------------------------------------------------------------- git

// gitRoot returns the working tree containing path, or "".
func gitRoot(path string) string {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		if fi, err := os.Stat(filepath.Join(p, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return p
		}
		if p == "/" || p == "." {
			return ""
		}
	}
}

// gitDirty reports whether a working tree has uncommitted changes, running git as aos.
func gitDirty(root string, uid, gid uint32) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "safe.directory=*", "-C", root, "status", "--porcelain", "--untracked-files=normal")
	cmd.Env = []string{"HOME=/home/aos", "PATH=/usr/bin:/bin", "GIT_OPTIONAL_LOCKS=0"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}}
	out, err := cmd.Output()
	// If git can't tell, treat the tree as dirty: asking is the safe side.
	return err != nil || len(strings.TrimSpace(string(out))) > 0
}

// ---------------------------------------------------------------- Sessions for the API

// userSessions implements api.Sessions.
type userSessions struct {
	d *Daemon
}

func (u *userSessions) Create(ctx context.Context, cols, rows uint16) (*aosv1.SessionInfo, error) {
	d := u.d
	id := "u_" + randomHex(6)
	dir := filepath.Join(SessionsDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chown(dir, int(d.uid), int(d.gid)); err != nil {
		return nil, err
	}
	sh, err := session.Start(session.Options{Dir: dir, UID: d.uid, GID: d.gid, Home: d.layout.Home})
	if err != nil {
		return nil, err
	}
	_ = sh.Resize(cols, rows)
	e := &session.Entry{ID: id, Session: sh, Created: time.Now()}
	d.registry.Add(e)
	go func() {
		<-sh.Exited()
		d.registry.Remove(id, sh)
		_ = sh.Close()
		_ = os.RemoveAll(dir)
	}()
	return sessionInfo(e), nil
}

func sessionInfo(e *session.Entry) *aosv1.SessionInfo {
	return &aosv1.SessionInfo{Id: e.ID, TaskId: e.TaskID, Agent: e.Agent, Ended: !e.Ended.IsZero(), Pid: int32(e.Session.Pid()), CreatedAt: timestamppb.New(e.Created)}
}

func (u *userSessions) List() []*aosv1.SessionInfo {
	entries := u.d.registry.List()
	out := make([]*aosv1.SessionInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, sessionInfo(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.AsTime().Before(out[j].CreatedAt.AsTime()) })
	return out
}

func (u *userSessions) Close(id string) error {
	e, ok := u.d.registry.Get(id)
	if !ok {
		return fmt.Errorf("session %s: %w", id, os.ErrNotExist)
	}
	if e.Agent {
		return errors.New("an Agent's Session closes with its Task; cancel the Task instead")
	}
	return e.Session.Close()
}

func (u *userSessions) Attach(id string) (api.Terminal, error) {
	e, ok := u.d.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("no Session %q", id)
	}
	return terminal{e}, nil
}

type terminal struct{ e *session.Entry }

func (t terminal) Watch() ([]byte, <-chan []byte, func()) { return t.e.Session.Watch() }
func (t terminal) Resize(cols, rows uint16) error         { return t.e.Session.Resize(cols, rows) }
func (t terminal) Write(b []byte) error {
	if !t.e.Ended.IsZero() {
		return errors.New("the Session has ended")
	}
	t.e.Typed(b)
	return t.e.Session.Input(b)
}

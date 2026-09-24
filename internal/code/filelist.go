package code

import (
	"context"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Aman123at/agentic-os/internal/files"
)

// FileList is a Workspace's files for Quick Open (PLAN.md M8.7): listed once
// as the Realm's user, honouring .gitignore, and then kept current from the
// Workspace's watch rather than listed again for every query. A change the
// watch cannot account for (a rescan, an edited .gitignore) has it listed
// afresh at the next query.
type FileList struct {
	Root string

	mu        sync.Mutex
	loaded    bool
	stale     bool
	files     map[string]struct{}
	sorted    []string
	ignore    *files.Ignore
	truncated bool
	loading   *listing
}

// listing is a load in progress: changes that arrive meanwhile wait for it.
type listing struct {
	done    chan struct{}
	err     error
	pending []files.Change
}

// Paths returns the Workspace's files, relative and sorted, and whether the
// list stopped at its limit. load lists the tree; concurrent callers share
// one load.
func (l *FileList) Paths(ctx context.Context, load func(context.Context) (files.Tree, error)) ([]string, bool, error) {
	for {
		l.mu.Lock()
		if l.loaded && !l.stale {
			if l.sorted == nil {
				l.sorted = make([]string, 0, len(l.files))
				for p := range l.files {
					l.sorted = append(l.sorted, p)
				}
				sort.Strings(l.sorted)
			}
			out, truncated := l.sorted, l.truncated
			l.mu.Unlock()
			return out, truncated, nil
		}
		if c := l.loading; c != nil {
			l.mu.Unlock()
			select {
			case <-c.done:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
			if c.err != nil {
				return nil, false, c.err
			}
			continue
		}
		c := &listing{done: make(chan struct{})}
		l.loading = c
		l.stale = false
		l.mu.Unlock()

		// The listing outlives a caller that gives up on it: others may wait.
		tree, err := load(context.WithoutCancel(ctx))

		l.mu.Lock()
		l.loading = nil
		c.err = err
		if err == nil {
			l.files = make(map[string]struct{}, len(tree.Files))
			for _, p := range tree.Files {
				l.files[p] = struct{}{}
			}
			l.ignore = files.NewIgnore(tree.Ignores)
			l.truncated = tree.Truncated
			l.sorted = nil
			l.loaded = true
			l.applyLocked(c.pending)
		}
		l.mu.Unlock()
		close(c.done)
		if err != nil {
			return nil, false, err
		}
	}
}

// Apply updates the list from a batch of the watch's changes.
func (l *FileList) Apply(changes []files.Change) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loading != nil {
		l.loading.pending = append(l.loading.pending, changes...)
		return
	}
	if l.loaded {
		l.applyLocked(changes)
	}
}

func (l *FileList) applyLocked(changes []files.Change) {
	for _, ch := range changes {
		rel, ok := l.rel(ch.Path)
		switch {
		case ch.Kind == files.Rescan:
			l.stale = true
		case !ok:
		case path.Base(rel) == ".gitignore" && !ch.Dir:
			// What is ignored may have changed: list afresh at the next query.
			l.stale = true
		case ch.Kind == files.Created && !ch.Dir:
			l.add(rel)
		case ch.Kind == files.Deleted:
			delete(l.files, rel)
			prefix := rel + "/"
			for p := range l.files {
				if strings.HasPrefix(p, prefix) {
					delete(l.files, p)
				}
			}
			l.sorted = nil
		}
	}
}

func (l *FileList) add(rel string) {
	if _, ok := l.files[rel]; ok {
		return
	}
	for _, part := range strings.Split(path.Dir(rel), "/") {
		if files.SkipsWatching(part) {
			return
		}
	}
	if l.ignore.Ignored(rel, false) || len(l.files) >= files.MaxTreeFiles {
		return
	}
	l.files[rel] = struct{}{}
	l.sorted = nil
}

// rel is p relative to the root, slash-separated; false for the root itself
// or a path outside it.
func (l *FileList) rel(p string) (string, bool) {
	r, err := filepath.Rel(l.Root, p)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, "../") {
		return "", false
	}
	return filepath.ToSlash(r), true
}

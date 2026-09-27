// Package code keeps what aosd knows about Code, the Desktop's code editor
// (PLAN.md M8): which Workspaces are open and which of their files are unsaved.
//
// A Workspace belongs to the Desktop tab that opened it, by way of that tab's
// event subscription (its Owner): it is open until the window closes it or
// the subscription ends, whichever comes first. So the Registry holds nothing
// across a restart and needs no clean-up after a closed tab. The Realm switch
// asks it before it restarts aosd (M8.13), since a restart would drop the
// unsaved files it lists.
package code

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Aman123at/agentic-os/internal/files"
)

var (
	// ErrNoWorkspace is returned for a Workspace id that is not open.
	ErrNoWorkspace = errors.New("no such open Workspace")
	// ErrNoOwner is returned when opening a Workspace for an owner that has
	// detached (or never attached): its tab's subscription has ended.
	ErrNoOwner = errors.New("the event subscription has ended; subscribe again")
	// ErrTooMany is returned past MaxPerOwner.
	ErrTooMany = errors.New("too many Code windows are open in this tab")
)

const (
	// MaxUnsaved caps the unsaved files one window reports; more than any
	// editor has open, and it bounds what a client can make aosd hold.
	MaxUnsaved = 1000
	// MaxPerOwner caps one tab's Workspaces. Each watches its folder in a
	// process of its own.
	MaxPerOwner = 32
)

// Workspace is one open Code window's folder.
type Workspace struct {
	ID string
	// Owner is the event subscription of the tab it was opened in.
	Owner string
	Root  string
	// RootMode is the Realm the Workspace was opened in; every file in it is
	// read and written with that Realm's rights.
	RootMode bool
	OpenedAt time.Time
	// Unsaved are the window's files with edits not yet saved, sorted.
	Unsaved []string
}

type entry struct {
	Workspace
	stop  func()
	tools Tools
}

// Tools are what aosd keeps of a Workspace for navigation, fed by its watch:
// its files for Quick Open (M8.7) and its symbols (M8.8).
type Tools struct {
	Files   *FileList
	Symbols *Index
}

// Registry is the open Workspaces and the owners they belong to. The zero
// value is ready to use.
type Registry struct {
	mu     sync.Mutex
	m      map[string]*entry
	owners map[string]bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Attach makes owner able to open Workspaces, and returns the function that
// detaches it again and closes every Workspace it opened.
func (r *Registry) Attach(owner string) (detach func()) {
	r.mu.Lock()
	if r.owners == nil {
		r.owners = make(map[string]bool)
	}
	r.owners[owner] = true
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.owners, owner)
			var stops []func()
			for id, e := range r.m {
				if e.Owner == owner {
					delete(r.m, id)
					stops = append(stops, e.stop)
				}
			}
			r.mu.Unlock()
			for _, stop := range stops {
				stop()
			}
		})
	}
}

// Open records a Workspace as open for owner. stop is run when it closes,
// by Close or by its owner detaching; it ends the Workspace's watch.
func (r *Registry) Open(owner, root string, rootMode bool, stop func()) (Workspace, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.owners[owner] {
		return Workspace{}, ErrNoOwner
	}
	n := 0
	for _, e := range r.m {
		if e.Owner == owner {
			n++
		}
	}
	if n >= MaxPerOwner {
		return Workspace{}, ErrTooMany
	}
	e := &entry{Workspace: Workspace{ID: NewID("ws_"), Owner: owner, Root: filepath.Clean(root), RootMode: rootMode, OpenedAt: now()}, stop: stop}
	e.tools = Tools{Files: &FileList{Root: e.Root}, Symbols: &Index{Root: e.Root}}
	if r.m == nil {
		r.m = make(map[string]*entry)
	}
	r.m[e.ID] = e
	return e.Workspace, nil
}

// Close closes an open Workspace.
func (r *Registry) Close(id string) error {
	r.mu.Lock()
	e, ok := r.m[id]
	delete(r.m, id)
	r.mu.Unlock()
	if !ok {
		return ErrNoWorkspace
	}
	e.stop()
	return nil
}

// SetUnsaved replaces the unsaved files of an open Workspace. Paths must be
// absolute; they are cleaned and deduplicated.
func (r *Registry) SetUnsaved(id string, paths []string) error {
	if len(paths) > MaxUnsaved {
		return fmt.Errorf("%d unsaved files is more than the %d a window reports", len(paths), MaxUnsaved)
	}
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("unsaved file %q is not an absolute path", p)
		}
		clean = append(clean, filepath.Clean(p))
	}
	sort.Strings(clean)
	clean = slices.Compact(clean)
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[id]
	if !ok {
		return ErrNoWorkspace
	}
	e.Unsaved = clean
	return nil
}

// Get returns an open Workspace and its tools.
func (r *Registry) Get(id string) (Workspace, Tools, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[id]
	if !ok {
		return Workspace{}, Tools{}, ErrNoWorkspace
	}
	w := e.Workspace
	w.Unsaved = slices.Clone(e.Unsaved)
	return w, e.tools, nil
}

// Apply feeds a batch of a Workspace's watched changes to its tools.
func (t Tools) Apply(changes []files.Change) {
	if t.Files != nil {
		t.Files.Apply(changes)
	}
	if t.Symbols != nil {
		t.Symbols.Apply(changes)
	}
}

// List returns the open Workspaces, oldest first.
func (r *Registry) List() []Workspace {
	r.mu.Lock()
	out := make([]Workspace, 0, len(r.m))
	for _, e := range r.m {
		w := e.Workspace
		w.Unsaved = slices.Clone(e.Unsaved)
		out = append(out, w)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.Before(out[j].OpenedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// NewID returns a random id with prefix, for Workspaces and the event
// subscriptions that own them.
func NewID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

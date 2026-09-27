package files

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A Workspace watch (PLAN.md M8.2): Code's view of a folder tree, kept current
// with what changes under it. It runs where the other file operations run, as
// the Realm's user, so it never sees into a folder that user cannot read.
const OpWatch = "watch"

// Change kinds.
const (
	Created = "created"
	Changed = "changed"
	Deleted = "deleted"
	// Rescan says changes under Path were missed (the kernel's event queue
	// overflowed, or a folder could not be watched): list it again.
	Rescan = "rescan"
)

// Watch modes.
const (
	// WatchLive is inotify: changes arrive as they happen.
	WatchLive = "live"
	// WatchPolling walks the tree every few seconds: past the watch limit, or
	// where inotify is not available.
	WatchPolling = "polling"
	// WatchStopped means no more changes will come, such as when the folder
	// itself was deleted or moved.
	WatchStopped = "stopped"
)

// Change is one path under a watched folder that appeared, changed or went.
// A path replaced by a rename (an editor's atomic save) arrives as Created
// even when the watcher already knew it: treat Created of a known path as
// Changed.
type Change struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	Dir  bool   `json:"dir,omitempty"`
}

// WatchStatus is how a folder is being watched, sent first and again when it changes.
type WatchStatus struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
}

// WatchEvent is what a watch sends: a batch of changes, or a new status.
type WatchEvent struct {
	Changes []Change     `json:"changes,omitempty"`
	Status  *WatchStatus `json:"status,omitempty"`
}

// WatchArgs names the folder to watch. The limits are zero for the defaults;
// tests lower them.
type WatchArgs struct {
	Root string `json:"root"`
	// MaxDirs is how many folders inotify watches before the watch falls back
	// to polling (default maxWatchDirs).
	MaxDirs int `json:"max_dirs,omitempty"`
	// MaxEntries is how many entries polling compares each round (default maxPollEntries).
	MaxEntries int `json:"max_entries,omitempty"`
	// Interval is how often polling walks the tree (default pollInterval).
	Interval time.Duration `json:"interval,omitempty"`
}

const (
	// maxWatchDirs keeps one Workspace from spending the aos user's whole
	// inotify budget (fs.inotify.max_user_watches is 8192 on older kernels): a
	// tree larger than this is polled instead.
	maxWatchDirs = 8000
	// maxPollEntries bounds one polling round's walk and memory.
	maxPollEntries = 50_000
	// pollInterval matches Finder's folder watch (PLAN.md §15).
	pollInterval = 2 * time.Second
)

// skipDirs are folders whose insides are never watched: version-control
// internals, installed dependencies and caches, which change in bulk and which
// nobody edits by hand. The folder itself still appears and goes like any other.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, ".venv": true, "__pycache__": true,
	".cache": true, ".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true,
	".gradle": true, ".next": true, ".nuxt": true, ".terraform": true,
}

// SkipsWatching reports whether a folder with this name is watched only as an entry.
func SkipsWatching(name string) bool { return skipDirs[name] }

// Watcher watches a folder tree and sends what changes under it until ctx ends.
// It returns an error only when the watch cannot start; once it has sent its
// first status, trouble arrives as a WatchStopped status instead.
type Watcher interface {
	Watch(ctx context.Context, a WatchArgs, emit func(WatchEvent) error) error
}

// Watch implements Watcher.
func (p InProcess) Watch(ctx context.Context, a WatchArgs, emit func(WatchEvent) error) error {
	return p.Ops.Watch(ctx, a, emit)
}

// serveWatch is the worker's side of OpWatch: events are written as they come,
// until aosd closes the worker's input or kills it.
func serveWatch(req request, enc *json.Encoder, in io.Reader) error {
	var a WatchArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, in)
		cancel()
	}()
	err := req.Ops.Watch(ctx, a, func(ev WatchEvent) error {
		return enc.Encode(struct {
			Watch WatchEvent `json:"watch"`
		}{ev})
	})
	resp := response{Done: true}
	if err != nil {
		resp.Error, resp.Kind = err.Error(), errorKind(err)
	}
	return enc.Encode(resp)
}

// Watch watches a.Root until ctx ends. It uses inotify where it can and polls
// where it cannot, saying which in its first status.
func (o Ops) Watch(ctx context.Context, a WatchArgs, emit func(WatchEvent) error) error {
	fi, err := os.Stat(a.Root)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a folder", a.Root)
	}
	// Listing the root is the permission check: a folder its user cannot read
	// is refused here, before anything is sent.
	if _, err := os.ReadDir(a.Root); err != nil {
		return err
	}
	if a.MaxDirs <= 0 {
		a.MaxDirs = maxWatchDirs
	}
	if a.MaxEntries <= 0 {
		a.MaxEntries = maxPollEntries
	}
	if a.Interval <= 0 {
		a.Interval = pollInterval
	}
	// An emit that fails is the caller no longer listening: the watch ends quietly.
	listening := true
	send := func(ev WatchEvent) error {
		err := emit(ev)
		listening = listening && err == nil
		return err
	}
	reason := watchNotify(ctx, a, send)
	if !listening || ctx.Err() != nil {
		return nil
	}
	pollTree(ctx, a, reason, send)
	return nil
}

// entry is what polling remembers of a path.
type entry struct {
	dir  bool
	size int64
	mod  time.Time
	mode fs.FileMode
}

// snapshot walks root, skipping what is never watched, up to max entries. It
// reports whether it stopped early.
func snapshot(root string, max int) (map[string]entry, bool) {
	out := make(map[string]entry)
	full := false
	var walk func(dir string)
	walk = func(dir string) {
		des, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, de := range des {
			if len(out) >= max {
				full = true
				return
			}
			p := filepath.Join(dir, de.Name())
			info, err := de.Info()
			if err != nil {
				continue
			}
			isDir := de.IsDir() // not followed through symlinks
			out[p] = entry{dir: isDir, size: info.Size(), mod: info.ModTime(), mode: info.Mode()}
			if isDir && !skipDirs[de.Name()] {
				walk(p)
			}
		}
	}
	walk(root)
	return out, full
}

// pollTree compares the tree with its last walk every interval and sends the
// difference, until ctx ends or emit fails.
func pollTree(ctx context.Context, a WatchArgs, reason string, emit func(WatchEvent) error) {
	last, full := snapshot(a.Root, a.MaxEntries)
	status := func(full bool) *WatchStatus {
		s := &WatchStatus{Mode: WatchPolling, Reason: reason}
		if full {
			s.Reason = fmt.Sprintf("%s; only the first %d files are checked for changes", reason, a.MaxEntries)
		}
		return s
	}
	if err := emit(WatchEvent{Status: status(full)}); err != nil {
		return
	}
	tick := time.NewTicker(a.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if _, err := os.Stat(a.Root); err != nil {
			_ = emit(WatchEvent{Status: &WatchStatus{Mode: WatchStopped, Reason: "the folder was deleted or moved"}})
			<-ctx.Done()
			return
		}
		next, nextFull := snapshot(a.Root, a.MaxEntries)
		var changes []Change
		for p, e := range next {
			old, ok := last[p]
			switch {
			case !ok:
				changes = append(changes, Change{Kind: Created, Path: p, Dir: e.dir})
			case old != e && !e.dir:
				changes = append(changes, Change{Kind: Changed, Path: p})
			case old.mode != e.mode:
				changes = append(changes, Change{Kind: Changed, Path: p, Dir: e.dir})
			}
		}
		for p, e := range last {
			if _, ok := next[p]; !ok {
				changes = append(changes, Change{Kind: Deleted, Path: p, Dir: e.dir})
			}
		}
		sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
		ev := WatchEvent{Changes: changes}
		if nextFull != full {
			ev.Status = status(nextFull)
		}
		last, full = next, nextFull
		if len(ev.Changes) > 0 || ev.Status != nil {
			if err := emit(ev); err != nil {
				return
			}
		}
	}
}

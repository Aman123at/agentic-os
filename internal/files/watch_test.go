package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// watching is a running watch whose events a test waits on. Off Linux the
// watch polls, so these tests run against polling there and inotify on Linux.
type watching struct {
	t      *testing.T
	events chan WatchEvent
	seen   []Change
}

func startWatch(t *testing.T, w Watcher, a WatchArgs) *watching {
	t.Helper()
	if a.Interval == 0 {
		a.Interval = 20 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	wt := &watching{t: t, events: make(chan WatchEvent, 64)}
	go func() {
		done <- w.Watch(ctx, a, func(ev WatchEvent) error {
			select {
			case wt.events <- ev:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Watch returned %v after its context ended, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Watch did not return after its context ended")
		}
	})
	return wt
}

// status waits for the next event and requires it to be a status.
func (w *watching) status() WatchStatus {
	w.t.Helper()
	select {
	case ev := <-w.events:
		if ev.Status == nil {
			w.t.Fatalf("expected a status, got %+v", ev)
		}
		w.seen = append(w.seen, ev.Changes...)
		return *ev.Status
	case <-time.After(5 * time.Second):
		w.t.Fatal("no status from the watch")
	}
	return WatchStatus{}
}

// expect waits until every change in want has been seen, in any order,
// alongside whatever else arrives.
func (w *watching) expect(want ...Change) {
	w.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		missing := []Change{}
		for _, c := range want {
			if !w.saw(c) {
				missing = append(missing, c)
			}
		}
		if len(missing) == 0 {
			return
		}
		select {
		case ev := <-w.events:
			w.seen = append(w.seen, ev.Changes...)
		case <-deadline:
			w.t.Fatalf("never saw %+v; saw %+v", missing, w.seen)
		}
	}
}

func (w *watching) saw(c Change) bool {
	for _, s := range w.seen {
		if s == c {
			return true
		}
	}
	return false
}

func TestWatchReportsWhatAppearsChangesAndGoes(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Join(home, "project")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	writeFile(t, filepath.Join(root, "old.txt"), "x")
	w := startWatch(t, InProcess{Ops: ops}, WatchArgs{Root: root})
	w.status()

	p := func(rel string) string { return filepath.Join(root, rel) }
	writeFile(t, p("new.go"), "package main\n")
	w.expect(Change{Kind: Created, Path: p("new.go")})

	time.Sleep(30 * time.Millisecond) // a later mtime, for polling
	writeFile(t, p("main.go"), "package main\n\nfunc main() {}\n")
	w.expect(Change{Kind: Changed, Path: p("main.go")})

	if err := os.Rename(p("old.txt"), p("renamed.txt")); err != nil {
		t.Fatal(err)
	}
	w.expect(Change{Kind: Deleted, Path: p("old.txt")}, Change{Kind: Created, Path: p("renamed.txt")})

	// A folder that appears is watched too, with what was put in it at once.
	writeFile(t, p("pkg/util/strings.go"), "package util\n")
	w.expect(Change{Kind: Created, Path: p("pkg"), Dir: true}, Change{Kind: Created, Path: p("pkg/util/strings.go")})
	time.Sleep(30 * time.Millisecond)
	writeFile(t, p("pkg/util/strings.go"), "package util\n\nvar X = 1\n")
	w.expect(Change{Kind: Changed, Path: p("pkg/util/strings.go")})

	if err := os.RemoveAll(p("pkg")); err != nil {
		t.Fatal(err)
	}
	w.expect(Change{Kind: Deleted, Path: p("pkg"), Dir: true})
}

func TestWatchSeesDependencyAndCacheFoldersButNotInside(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Join(home, "web")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	w := startWatch(t, InProcess{Ops: ops}, WatchArgs{Root: root})
	w.status()

	writeFile(t, filepath.Join(root, "node_modules", "left-pad", "index.js"), "module.exports = 1\n")
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, "index.js"), "require('left-pad')\n")
	w.expect(
		Change{Kind: Created, Path: filepath.Join(root, "node_modules"), Dir: true},
		Change{Kind: Created, Path: filepath.Join(root, ".git"), Dir: true},
		Change{Kind: Created, Path: filepath.Join(root, "index.js")},
	)
	time.Sleep(100 * time.Millisecond)
	writeFile(t, filepath.Join(root, "node_modules", "left-pad", "index.js"), "module.exports = 2\n")
	writeFile(t, filepath.Join(root, "index.js"), "// marker\n")
	time.Sleep(30 * time.Millisecond)
	writeFile(t, filepath.Join(root, "marker"), "")
	w.expect(Change{Kind: Created, Path: filepath.Join(root, "marker")})
	for _, c := range w.seen {
		for _, skipped := range []string{"node_modules", ".git"} {
			if strings.HasPrefix(c.Path, filepath.Join(root, skipped)+string(filepath.Separator)) {
				t.Errorf("a change inside %s was reported: %+v", skipped, c)
			}
		}
	}
}

func TestWatchPollsPastTheFolderLimit(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Join(home, "big")
	for i := range 3 {
		writeFile(t, filepath.Join(root, fmt.Sprintf("d%d", i), "f.txt"), "x")
	}
	w := startWatch(t, InProcess{Ops: ops}, WatchArgs{Root: root, MaxDirs: 2})
	st := w.status()
	if st.Mode != WatchPolling || st.Reason == "" {
		t.Fatalf("status %+v, want polling with a reason", st)
	}
	writeFile(t, filepath.Join(root, "d2", "g.txt"), "y")
	w.expect(Change{Kind: Created, Path: filepath.Join(root, "d2", "g.txt")})
}

func TestWatchSaysSoWhenPollingIsCapped(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Join(home, "many")
	for i := range 5 {
		writeFile(t, filepath.Join(root, fmt.Sprintf("f%d", i)), "x")
	}
	w := startWatch(t, InProcess{Ops: ops}, WatchArgs{Root: root, MaxDirs: 1, MaxEntries: 3})
	// Live on Linux, where the root alone is within MaxDirs until a new folder
	// makes one too many; polling from the start elsewhere. Either way a
	// polling status names the cap.
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-w.events:
			if ev.Status != nil && ev.Status.Mode == WatchPolling && strings.Contains(ev.Status.Reason, "only the first 3") {
				return
			}
		case <-deadline:
			t.Fatal("no polling status naming the cap")
		}
	}
}

func TestWatchStopsButStaysOpenWhenTheFolderGoes(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Join(home, "doomed")
	writeFile(t, filepath.Join(root, "a"), "x")
	w := startWatch(t, InProcess{Ops: ops}, WatchArgs{Root: root})
	w.status()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-w.events:
			if ev.Status != nil && ev.Status.Mode == WatchStopped {
				return // and startWatch's cleanup checks it ends only with its context
			}
		case <-deadline:
			t.Fatal("no stopped status after the folder was deleted")
		}
	}
}

func TestWatchRefusesWhatIsNotAFolder(t *testing.T) {
	ops, home := machine(t)
	writeFile(t, filepath.Join(home, "file.txt"), "x")
	emit := func(WatchEvent) error { t.Error("an event was sent"); return nil }
	if err := ops.Watch(context.Background(), WatchArgs{Root: filepath.Join(home, "file.txt")}, emit); err == nil {
		t.Error("watching a file succeeded")
	}
	if err := ops.Watch(context.Background(), WatchArgs{Root: filepath.Join(home, "missing")}, emit); !os.IsNotExist(err) {
		t.Errorf("watching a missing folder: %v, want not-exist", err)
	}
}

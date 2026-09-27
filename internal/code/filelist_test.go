package code

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aman123at/agentic-os/internal/files"
)

func TestAFileListFollowsTheWatch(t *testing.T) {
	var loads atomic.Int32
	tree := files.Tree{Files: []string{"a.go", "src/b.go", "src/deep/c.go"}, Ignores: files.IgnoreRules{"": {"*.log"}}}
	load := func(context.Context) (files.Tree, error) {
		loads.Add(1)
		return tree, nil
	}
	l := &FileList{Root: "/w"}
	paths := func() []string {
		t.Helper()
		p, _, err := l.Paths(context.Background(), load)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Changes before the first listing are not needed: it lists what is there.
	l.Apply([]files.Change{{Kind: files.Created, Path: "/w/early.go"}})
	if got := paths(); !reflect.DeepEqual(got, tree.Files) {
		t.Fatalf("first listing: %q", got)
	}

	l.Apply([]files.Change{
		{Kind: files.Created, Path: "/w/new.go"},
		{Kind: files.Created, Path: "/w/debug.log"},           // ignored
		{Kind: files.Created, Path: "/w/node_modules/x/i.js"}, // never watched
		{Kind: files.Created, Path: "/w/pkg", Dir: true},      // folders are not listed
		{Kind: files.Deleted, Path: "/w/src/deep", Dir: true}, // with what was under it
		{Kind: files.Changed, Path: "/w/a.go"},
		{Kind: files.Created, Path: "/elsewhere/z.go"},
	})
	if got, want := paths(), []string{"a.go", "new.go", "src/b.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after changes: %q, want %q", got, want)
	}
	if n := loads.Load(); n != 1 {
		t.Errorf("listed %d times; the watch should have kept it current", n)
	}

	// An edited .gitignore, or a rescan, lists afresh.
	for _, ch := range []files.Change{{Kind: files.Changed, Path: "/w/src/.gitignore"}, {Kind: files.Rescan, Path: "/w", Dir: true}} {
		before := loads.Load()
		l.Apply([]files.Change{ch})
		paths()
		if loads.Load() != before+1 {
			t.Errorf("%+v did not list afresh", ch)
		}
	}
}

func TestAFileListIsListedOnceForEveryoneWaiting(t *testing.T) {
	var loads atomic.Int32
	release := make(chan struct{})
	load := func(context.Context) (files.Tree, error) {
		loads.Add(1)
		<-release
		return files.Tree{Files: []string{"a.go"}}, nil
	}
	l := &FileList{Root: "/w"}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, _, err := l.Paths(context.Background(), load); err != nil || len(p) == 0 {
				t.Errorf("Paths: %q %v", p, err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	// A change during the listing is applied once it is in.
	l.Apply([]files.Change{{Kind: files.Created, Path: "/w/during.go"}})
	close(release)
	wg.Wait()
	if n := loads.Load(); n != 1 {
		t.Errorf("listed %d times", n)
	}
	if p, _, _ := l.Paths(context.Background(), load); !reflect.DeepEqual(p, []string{"a.go", "during.go"}) {
		t.Errorf("a change made while listing was lost: %q", p)
	}
}

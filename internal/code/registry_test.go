package code

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestAWorkspaceIsOpenUntilClosedOrItsOwnerDetaches(t *testing.T) {
	var r Registry
	detachTab1 := r.Attach("sub_1")
	detachTab2 := r.Attach("sub_2")
	defer detachTab2()
	stopped := map[string]bool{}
	open := func(owner, root string) Workspace {
		t.Helper()
		w, err := r.Open(owner, root, false, func() { stopped[root] = true })
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	a := open("sub_1", "/home/aos/project/.")
	b := open("sub_1", "/srv/site")
	c := open("sub_2", "/etc")
	if a.ID == b.ID || a.Root != "/home/aos/project" || a.Owner != "sub_1" {
		t.Fatalf("opened %+v and %+v", a, b)
	}
	if got := r.List(); len(got) != 3 {
		t.Fatalf("three open: %+v", got)
	}

	if err := r.Close(a.ID); err != nil || !stopped["/home/aos/project/."] {
		t.Fatalf("close: %v, stopped %v", err, stopped)
	}
	if err := r.Close(a.ID); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("closing it again: %v, want ErrNoWorkspace", err)
	}

	// The tab's subscription ends: its Workspaces go, the other tab's stay.
	detachTab1()
	detachTab1() // a second detach is harmless
	if got := r.List(); len(got) != 1 || got[0].ID != c.ID || !stopped["/srv/site"] || stopped["/etc"] {
		t.Fatalf("after the tab detached: %+v, stopped %v", got, stopped)
	}
	if _, err := r.Open("sub_1", "/tmp", false, func() {}); !errors.Is(err, ErrNoOwner) {
		t.Errorf("opening for a detached owner: %v, want ErrNoOwner", err)
	}
}

func TestOneOwnerOpensAtMostMaxPerOwner(t *testing.T) {
	var r Registry
	defer r.Attach("sub")()
	for range MaxPerOwner {
		if _, err := r.Open("sub", "/p", false, func() {}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Open("sub", "/p", false, func() {}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("one past the cap: %v, want ErrTooMany", err)
	}
}

func TestUnsavedFilesAreReplacedCleanedAndSorted(t *testing.T) {
	var r Registry
	defer r.Attach("sub")()
	w, err := r.Open("sub", "/p", true, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetUnsaved(w.ID, []string{"/p/b.go", "/p/./a.go", "/p/b.go"}); err != nil {
		t.Fatal(err)
	}
	if got := r.List()[0]; !slices.Equal(got.Unsaved, []string{"/p/a.go", "/p/b.go"}) || !got.RootMode {
		t.Fatalf("listed %+v", got)
	}
	if err := r.SetUnsaved(w.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.List()[0].Unsaved; len(got) != 0 {
		t.Fatalf("after everything was saved: %v", got)
	}
	if err := r.SetUnsaved(w.ID, []string{"relative.go"}); err == nil {
		t.Error("a relative path was accepted")
	}
	if err := r.SetUnsaved(w.ID, make([]string, MaxUnsaved+1)); err == nil {
		t.Error("more than MaxUnsaved paths were accepted")
	}
	if err := r.SetUnsaved("ws_gone", nil); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestListIsOldestFirst(t *testing.T) {
	now := time.Unix(1000, 0)
	r := Registry{Now: func() time.Time { now = now.Add(time.Second); return now }}
	defer r.Attach("sub")()
	first, _ := r.Open("sub", "/one", false, func() {})
	_, _ = r.Open("sub", "/two", false, func() {})
	if got := r.List(); got[0].ID != first.ID || got[1].Root != "/two" {
		t.Fatalf("order: %+v", got)
	}
}

package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWatchIsLiveOnLinux(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Join(home, "project")
	writeFile(t, filepath.Join(root, "a.go"), "package a\n")
	w := startWatch(t, InProcess{Ops: ops}, WatchArgs{Root: root})
	if st := w.status(); st.Mode != WatchLive {
		t.Fatalf("status %+v, want live", st)
	}
	// An editor's atomic save: write a temp file, rename it over the original.
	// The temp file came and went within one burst, so it is never mentioned.
	tmp := filepath.Join(root, ".a.go.swp")
	writeFile(t, tmp, "package a\n\nvar B = 2\n")
	if err := os.Rename(tmp, filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}
	w.expect(Change{Kind: Created, Path: filepath.Join(root, "a.go")})
	if w.saw(Change{Kind: Created, Path: tmp}) || w.saw(Change{Kind: Deleted, Path: tmp}) {
		t.Errorf("the temp file was reported: %+v", w.seen)
	}
}

func TestWatchRunsInAWorkerProcess(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Join(home, "project")
	writeFile(t, filepath.Join(root, "a.go"), "package a\n")
	w := startWatch(t, asMe(ops), WatchArgs{Root: root})
	if st := w.status(); st.Mode != WatchLive {
		t.Fatalf("status %+v, want live", st)
	}
	writeFile(t, filepath.Join(root, "b.go"), "package a\n")
	w.expect(Change{Kind: Created, Path: filepath.Join(root, "b.go")})
}

func TestAWorkerWatchesOnlyWhatItsUserMayRead(t *testing.T) {
	u, dir := asNobody(t)
	private := filepath.Join(dir, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	err := u.Watch(context.Background(), WatchArgs{Root: private}, func(WatchEvent) error {
		t.Error("an event was sent for a folder nobody cannot read")
		return nil
	})
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("watching a root-only folder as nobody: %v, want ErrPermission", err)
	}

	// Inside a folder it can read, one it cannot is simply not watched.
	open := filepath.Join(dir, "shared")
	writeFile(t, filepath.Join(open, "locked", "secret"), "x")
	if err := os.Chmod(filepath.Join(open, "locked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	w := startWatch(t, u, WatchArgs{Root: open})
	if st := w.status(); st.Mode != WatchLive {
		t.Fatalf("status %+v, want live", st)
	}
	writeFile(t, filepath.Join(open, "locked", "secret2"), "y")
	writeFile(t, filepath.Join(open, "visible"), "z")
	w.expect(Change{Kind: Created, Path: filepath.Join(open, "visible")})
	if w.saw(Change{Kind: Created, Path: filepath.Join(open, "locked", "secret2")}) {
		t.Error("a change inside a folder nobody cannot read was reported")
	}
}

package software

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestATreeRecordsChangesAndGoesBackToEarlierStates(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "etc")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) { must(os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644)) }
	must(os.MkdirAll(filepath.Join(root, "apt"), 0o755))
	write("apt/sources.list", "deb ubuntu\n")
	write("hostname", "aos\n")
	write("passwd-", "backup\n")
	tree := &Tree{Root: root, Blobs: filepath.Join(dir, "blobs"), Skip: []string{filepath.Join(root, "hostname")}}

	before, err := tree.Scan()
	must(err)
	if _, ok := before[filepath.Join(root, "hostname")]; ok {
		t.Error("a skipped path was tracked")
	}
	if _, ok := before[filepath.Join(root, "passwd-")]; ok {
		t.Error("a backup file was tracked")
	}

	// An "install": a new folder and file, an edit, a removal, a link.
	must(os.MkdirAll(filepath.Join(root, "nginx", "sites-enabled"), 0o755))
	write("nginx/nginx.conf", "worker_processes 1;\n")
	write("apt/sources.list", "deb ubuntu\ndeb nginx\n")
	must(os.Symlink("../nginx.conf", filepath.Join(root, "nginx", "sites-enabled", "default")))
	after, err := tree.Scan()
	must(err)
	changes := diffFiles(before, after)
	var names []string
	for _, c := range changes {
		names = append(names, strings.TrimPrefix(c.Name, root))
	}
	if strings.Join(names, " ") != "/apt/sources.list /nginx /nginx/nginx.conf /nginx/sites-enabled /nginx/sites-enabled/default" {
		t.Fatalf("changed %v", names)
	}

	// Something the Ledger doesn't know about lands in the new folder.
	write("nginx/untracked.conf", "x\n")
	// Undo: every changed path gets its state from before.
	undo := map[string]string{}
	for _, c := range changes {
		undo[c.Name] = c.Before
	}
	notes, err := tree.Apply(undo)
	must(err)
	if got, _ := os.ReadFile(filepath.Join(root, "apt", "sources.list")); string(got) != "deb ubuntu\n" {
		t.Errorf("sources.list after undo: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "nginx", "nginx.conf")); !os.IsNotExist(err) {
		t.Errorf("nginx.conf survived the undo: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "kept "+filepath.Join(root, "nginx")) {
		t.Errorf("notes %v", notes)
	}

	// Redo from the recorded states, as Replay does in a new container.
	must(os.RemoveAll(filepath.Join(root, "nginx")))
	redo := map[string]string{}
	for _, c := range changes {
		redo[c.Name] = c.After
	}
	_, err = tree.Apply(redo)
	must(err)
	again, err := tree.Scan()
	must(err)
	for _, c := range changes {
		if !again.Same(c.Name, c.After) {
			t.Errorf("%s after redo: %+v, want %s", c.Name, again[c.Name], c.After)
		}
	}
	if target, _ := os.Readlink(filepath.Join(root, "nginx", "sites-enabled", "default")); target != "../nginx.conf" {
		t.Errorf("link target %q", target)
	}
}

func TestUnchangedFilesAreNotReadAgain(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "etc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, "a.conf")
	if err := os.WriteFile(f, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := &Tree{Root: root, Blobs: filepath.Join(dir, "blobs")}
	first, _ := tree.Scan()
	blob := first[f].Blob
	// Remove the blob: an unchanged file must come from the cache, not be stored again.
	if err := os.Remove(tree.blobPath(blob)); err != nil {
		t.Fatal(err)
	}
	second, _ := tree.Scan()
	if second[f].Blob != blob {
		t.Fatal("hash changed")
	}
	if _, err := os.Stat(tree.blobPath(blob)); !os.IsNotExist(err) {
		t.Error("an unchanged file was read and stored again")
	}
	if err := os.WriteFile(f, []byte("two, longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, _ := tree.Scan()
	if third[f].Blob == blob {
		t.Error("a changed file kept its old hash")
	}
}

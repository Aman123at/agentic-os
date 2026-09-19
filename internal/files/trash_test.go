package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// machine returns Ops for a home folder in a temp dir, with a fixed clock. The
// Machine's scratch folder is inside the temp dir too: on Linux the temp dir is
// under /tmp, which would otherwise count as scratch and make every delete
// permanent. The whole temp dir is one filesystem, so every delete is a plain
// rename into the home Trash unless a test injects a second device.
func machine(t *testing.T) (Ops, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home", "aos")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 10, 30, 0, 0, time.Local)
	// No injected mounts by default, so ListTrash sees only the home Trash and a
	// real .Trash-<uid> on the test host cannot leak into a count assertion. Tests
	// that need a second filesystem set dev and mounts themselves.
	return Ops{Home: home, UID: 1000, Scratch: []string{filepath.Join(root, "tmp")},
		Now: func() time.Time { return now }, mounts: func() []string { return nil }}, home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDeleteMovesToTheTrashAndRestorePutsItBack(t *testing.T) {
	ops, home := machine(t)
	doc := filepath.Join(home, "Documents", "report 2026.txt")
	writeFile(t, doc, "draft")

	item, err := ops.Delete(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(doc); !os.IsNotExist(err) {
		t.Fatalf("file still exists after Delete: %v", err)
	}
	// freedesktop.org Trash layout, so other tools understand it.
	trashed := filepath.Join(home, ".local", "share", "Trash", "files", "report 2026.txt")
	if got := readFile(t, trashed); got != "draft" {
		t.Errorf("trashed content %q", got)
	}
	info := readFile(t, filepath.Join(home, ".local", "share", "Trash", "info", "report 2026.txt.trashinfo"))
	wantInfo := "[Trash Info]\nPath=" + strings.ReplaceAll(filepath.ToSlash(doc), " ", "%20") + "\nDeletionDate=2026-09-14T10:30:00\n"
	if info != wantInfo {
		t.Errorf("trashinfo:\n%s\nwant:\n%s", info, wantInfo)
	}
	if item.OriginalPath != doc || item.Size != 5 || item.Dir {
		t.Errorf("item %+v", item)
	}

	items, err := ops.ListTrash()
	if err != nil || len(items) != 1 || items[0].ID != item.ID || !items[0].DeletedAt.Equal(ops.Now()) {
		t.Fatalf("ListTrash = %+v, %v", items, err)
	}
	restored, err := ops.Restore(item.ID)
	if err != nil || restored != doc || readFile(t, doc) != "draft" {
		t.Fatalf("Restore = %q, %v", restored, err)
	}
	if items, _ := ops.ListTrash(); len(items) != 0 {
		t.Errorf("Trash not empty after restore: %+v", items)
	}
}

func TestDeletingTheSameNameTwiceKeepsBoth(t *testing.T) {
	ops, home := machine(t)
	a, b := filepath.Join(home, "a", "notes.txt"), filepath.Join(home, "b", "notes.txt")
	writeFile(t, a, "from a")
	writeFile(t, b, "from b")
	first, err := ops.Delete(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ops.Delete(b)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("both items have id %q", first.ID)
	}
	for _, item := range []TrashItem{second, first} {
		if _, err := ops.Restore(item.ID); err != nil {
			t.Fatal(err)
		}
	}
	if readFile(t, a) != "from a" || readFile(t, b) != "from b" {
		t.Error("restored contents swapped")
	}
}

func TestRestoreRefusesToReplaceAFileAtTheOriginalPath(t *testing.T) {
	ops, home := machine(t)
	p := filepath.Join(home, "notes.txt")
	writeFile(t, p, "old")
	item, err := ops.Delete(p)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "new")
	if _, err := ops.Restore(item.ID); !errors.Is(err, ErrExists) {
		t.Fatalf("Restore over an existing file: %v, want ErrExists", err)
	}
	if readFile(t, p) != "new" {
		t.Error("existing file was replaced")
	}
}

// TestFileOnAnotherFilesystemGoesToItsMountRootTrash exercises the st_dev
// selection with an injected second device: a file on a different filesystem
// than home is trashed into .Trash-<uid> at that filesystem's mount root, found
// by walking up until the device changes.
func TestFileOnAnotherFilesystemGoesToItsMountRootTrash(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Dir(filepath.Dir(home))  // the temp dir
	mnt := filepath.Join(root, "mnt", "data") // the second filesystem's mount root
	// Home is device 0; everything at or under mnt is device 1.
	ops.dev = func(path string) (uint64, error) {
		if path == mnt || strings.HasPrefix(path, mnt+string(filepath.Separator)) {
			return 1, nil
		}
		return 0, nil
	}
	ops.mounts = func() []string { return []string{mnt} }

	p := filepath.Join(mnt, "photos", "cat.jpg")
	writeFile(t, p, "jpg")
	item, err := ops.Delete(p)
	if err != nil {
		t.Fatal(err)
	}
	// Trashed at the mount root, not the home Trash: a rename within the filesystem.
	dest := filepath.Join(mnt, ".Trash-1000", "files", "cat.jpg")
	if item.ID != dest {
		t.Errorf("item ID %q, want %q", item.ID, dest)
	}
	if readFile(t, dest) != "jpg" {
		t.Error("not in the mount root's Trash")
	}

	items, err := ops.ListTrash()
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListTrash = %+v, %v", items, err)
	}
	if restored, err := ops.Restore(item.ID); err != nil || restored != p {
		t.Fatalf("Restore = %q, %v", restored, err)
	}
	if readFile(t, p) != "jpg" {
		t.Error("restored content lost")
	}
}

// TestSameDeviceGoesToTheHomeTrash covers the one-filesystem VPS: a file outside
// home but on home's device is a plain rename into the home Trash, and nothing is
// created at its own location.
func TestSameDeviceGoesToTheHomeTrash(t *testing.T) {
	ops, home := machine(t)
	root := filepath.Dir(filepath.Dir(home))
	etc := filepath.Join(root, "etc", "nginx.conf") // outside home, same device
	writeFile(t, etc, "server {}")
	item, err := ops.Delete(etc)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".local", "share", "Trash", "files", "nginx.conf")
	if item.ID != dest {
		t.Errorf("item ID %q, want the home Trash %q", item.ID, dest)
	}
	if _, err := os.Lstat(filepath.Join(root, "etc", ".Trash-1000")); !os.IsNotExist(err) {
		t.Error("a .Trash-<uid> was created outside home on a one-filesystem Machine")
	}
	if restored, err := ops.Restore(item.ID); err != nil || restored != etc {
		t.Fatalf("Restore = %q, %v", restored, err)
	}
}

// TestRestoreRejectsMalformedIDs checks the path-ID validation.
func TestRestoreRejectsMalformedIDs(t *testing.T) {
	ops, home := machine(t)
	for _, id := range []string{
		"",
		"home/notes.txt",                    // not absolute
		"/etc/passwd",                       // not a <TrashDir>/files/<name> shape
		home + "/.local/share/Trash/files/", // empty name
		home + "/.local/share/Trash/info/notes.txt", // wrong subdir
		home + "/.local/share/Trash/files/../../etc/passwd",
		home + "/.local/share/Trash/files/a\x00b",
	} {
		if _, err := ops.Restore(id); err == nil {
			t.Errorf("Restore(%q) accepted a malformed id", id)
		}
	}
}

func TestDisposableFilesAreDeletedPermanently(t *testing.T) {
	ops, home := machine(t)
	scratch := t.TempDir()
	ops.Scratch = []string{scratch}
	for _, p := range []string{
		filepath.Join(scratch, "build.log"),
		filepath.Join(home, "app", "node_modules", "left-pad", "index.js"),
		filepath.Join(home, "app", "__pycache__", "x.pyc"),
		filepath.Join(home, ".cache", "pip", "wheel"),
	} {
		writeFile(t, p, "x")
	}
	for _, p := range []string{
		filepath.Join(scratch, "build.log"),
		filepath.Join(home, "app", "node_modules"),
		filepath.Join(home, "app", "__pycache__"),
		filepath.Join(home, ".cache"),
	} {
		item, err := ops.Delete(p)
		if err != nil {
			t.Fatal(err)
		}
		if item.ID != "" {
			t.Errorf("%s: went to the Trash (%s), want deleted permanently", p, item.ID)
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if items, _ := ops.ListTrash(); len(items) != 0 {
		t.Errorf("Trash holds %+v", items)
	}
}

func TestExpireRemovesOldItemsThenOldestUntilUnderTheCap(t *testing.T) {
	ops, home := machine(t)
	clock := ops.Now()
	ops.Now = func() time.Time { return clock }
	del := func(path, content string, age time.Duration) {
		t.Helper()
		writeFile(t, path, content)
		clock = time.Date(2026, 9, 14, 10, 30, 0, 0, time.Local).Add(-age)
		if _, err := ops.Delete(path); err != nil {
			t.Fatal(err)
		}
	}
	del(filepath.Join(home, "ancient.txt"), "1234567890", 40*24*time.Hour)
	del(filepath.Join(home, "old.txt"), "1234567890", 20*24*time.Hour)
	del(filepath.Join(home, "newer.txt"), "1234567890", 10*24*time.Hour)
	del(filepath.Join(home, "newest.txt"), "1234567890", time.Hour)
	clock = time.Date(2026, 9, 14, 10, 30, 0, 0, time.Local)

	removed, err := ops.ExpireTrash(30*24*time.Hour, 25)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := ops.ListTrash()
	var left []string
	for _, it := range items {
		left = append(left, filepath.Base(it.OriginalPath))
	}
	if removed != 2 || strings.Join(left, ",") != "newest.txt,newer.txt" {
		t.Errorf("removed %d, left %v; want 2 removed, newest.txt and newer.txt left", removed, left)
	}

	removed, err = ops.EmptyTrash()
	if items, _ := ops.ListTrash(); err != nil || removed != 2 || len(items) != 0 {
		t.Errorf("EmptyTrash removed %d (%v), %d left", removed, err, len(items))
	}
}

// TestRootHomeUsesRootsOwnTrash covers the M7.5 wiring: in Root Mode the file
// Ops carry root's home, so deletions land in /root's Trash — the Trash app then
// shows only the Root Realm's Trash, not aos's.
func TestRootHomeUsesRootsOwnTrash(t *testing.T) {
	root := Ops{Home: "/root", UID: 0}.homeTrash().dir
	if root != "/root/.local/share/Trash" {
		t.Errorf("root home Trash = %q, want /root/.local/share/Trash", root)
	}
	aos := Ops{Home: "/home/aos", UID: 1000}.homeTrash().dir
	if aos == root {
		t.Errorf("aos and root share a Trash directory %q", root)
	}
}

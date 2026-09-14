package files

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRmShimTrashesLikeRm(t *testing.T) {
	ops, home, _ := machine(t)
	writeFile(t, filepath.Join(home, "a.txt"), "a")
	writeFile(t, filepath.Join(home, "dir", "b.txt"), "b")
	if err := os.Mkdir(filepath.Join(home, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := Rm(ops, home, args, &out, &out)
		return code, out.String()
	}

	if code, out := run("dir"); code != 1 || !strings.Contains(out, "rm: cannot remove 'dir': Is a directory") {
		t.Errorf("rm on a folder without -r: %d %q", code, out)
	}
	if code, out := run("missing.txt"); code != 1 || !strings.Contains(out, "No such file or directory") {
		t.Errorf("rm on a missing file: %d %q", code, out)
	}
	if code, out := run("-f", "missing.txt"); code != 0 || out != "" {
		t.Errorf("rm -f on a missing file: %d %q", code, out)
	}
	if code, out := run("-d", "empty"); code != 0 {
		t.Errorf("rm -d on an empty folder: %d %q", code, out)
	}
	if code, out := run("-rv", "a.txt", "--", "dir"); code != 0 || !strings.Contains(out, "removed 'a.txt' (moved to the Trash)") {
		t.Errorf("rm -rv: %d %q", code, out)
	}
	items, _ := ops.ListTrash()
	if len(items) != 3 {
		t.Errorf("Trash holds %d items, want 3 (empty, a.txt, dir)", len(items))
	}
	if code, out := run("-rf", "/"); code != 1 || !strings.Contains(out, "dangerous to operate recursively on '/'") {
		t.Errorf("rm -rf /: %d %q", code, out)
	}
}

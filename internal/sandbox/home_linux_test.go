package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrepareHomeRemovesTheRetiredSharedLink covers the M6.6 migration: an older
// install left a root-owned ~/Shared symlink, which strands in a sticky 1775
// home. PrepareHome removes it and reports the removal; a real folder a user made
// keeping the name is left alone. PrepareHome chowns to root, so this needs root
// and is skipped otherwise (Aman runs it on the VPS; CI runs it in Docker).
func TestPrepareHomeRemovesTheRetiredSharedLink(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("PrepareHome chowns to root; run as root")
	}
	root := t.TempDir()
	l := Layout{Home: filepath.Join(root, "home", "aos"), Protected: filepath.Join(root, "home", ".aos-protected")}
	if err := os.MkdirAll(l.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(l.Home, "Shared")
	if err := os.Symlink("/shared", link); err != nil {
		t.Fatal(err)
	}

	notes, err := PrepareHome(l, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("~/Shared symlink still present: %v", err)
	}
	if !anyContains(notes, "Shared") {
		t.Errorf("no note about removing the Shared link: %v", notes)
	}

	// A real folder named Shared is a user's, not the retired link: keep it.
	realDir := filepath.Join(l.Home, "Shared")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareHome(l, 0, 0); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(realDir); err != nil || !fi.IsDir() {
		t.Errorf("a real ~/Shared folder was removed: %v", err)
	}
}

func anyContains(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

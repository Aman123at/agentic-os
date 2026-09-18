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

// TestPrepareHomeRefusesAHomeItDidNotCreate covers the M6.8 guard: pointed at a
// live user home (a real ~/.ssh with an authorized_keys, not one of AOS's own
// symlinks), PrepareHome refuses rather than relocating it — moving that file
// would lock the only way into the widened server. Needs no root: it returns
// before any chown.
func TestPrepareHomeRefusesAHomeItDidNotCreate(t *testing.T) {
	root := t.TempDir()
	l := Layout{Home: filepath.Join(root, "home", "ubuntu"), Protected: filepath.Join(root, "home", ".aos-protected")}
	keys := filepath.Join(l.Home, ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(keys), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keys, []byte("ssh-ed25519 AAAA... admin"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := PrepareHome(l, 0, 0); err == nil {
		t.Fatal("PrepareHome accepted a home with a real ~/.ssh it did not create")
	}
	// The user's key is untouched, and no Protected tree was built from it.
	if got, err := os.ReadFile(keys); err != nil || string(got) != "ssh-ed25519 AAAA... admin" {
		t.Errorf("authorized_keys was disturbed: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(l.Protected, "ssh")); !os.IsNotExist(err) {
		t.Errorf("~/.ssh was relocated into Protected: %v", err)
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

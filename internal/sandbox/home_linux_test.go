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

// TestPrepareHomeAdoptsSkelInItsOwnHome covers the installer↔daemon contract:
// `useradd --create-home` leaves stock skel dotfiles (.bashrc, .profile,
// .bash_logout) in /home/aos, and the installer drops the .aos-home marker to
// say the home is AOS's own. With the marker present PrepareHome must absorb
// those dotfiles into Protected behind its own symlinks rather than refuse —
// the failure that kept aosd from starting on a fresh VPS. Needs root (chown).
func TestPrepareHomeAdoptsSkelInItsOwnHome(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("PrepareHome chowns to root; run as root")
	}
	root := t.TempDir()
	l := Layout{Home: filepath.Join(root, "home", "aos"), Protected: filepath.Join(root, "home", ".aos-protected")}
	if err := os.MkdirAll(l.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stock skel dotfile useradd --create-home copied in, plus the marker.
	skel := filepath.Join(l.Home, ".bashrc")
	if err := os.WriteFile(skel, []byte("# stock skel bashrc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Home, ".aos-home"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := PrepareHome(l, 0, 0); err != nil {
		t.Fatalf("PrepareHome refused its own home (marker present): %v", err)
	}
	// .bashrc is now a symlink into Protected, and the content moved with it.
	fi, err := os.Lstat(skel)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.bashrc is not AOS's own symlink: mode %v, err %v", fi.Mode(), err)
	}
	got, err := os.ReadFile(filepath.Join(l.Protected, "bashrc"))
	if err != nil || string(got) != "# stock skel bashrc" {
		t.Errorf("skel .bashrc was not adopted into Protected: %q, %v", got, err)
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

package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// PrepareHome creates or repairs l's home layout (PLAN.md §7.2). It must run as
// root, and is idempotent: aosd calls it at every start, also on existing volumes.
//
//   - Each Protected dotfile lives in l.Protected and Home holds a root-owned
//     symlink to it. A real file or folder found in Home is moved there first; if
//     the target already exists, the one from Home is kept beside it with a
//     ".from-home-<time>" suffix.
//   - Any Home/Shared left from the retired Shared Folder is removed (M6.6): a
//     root-owned symlink in a sticky 1775 home strands otherwise.
//   - Home is root:gid mode 1775, so only root can replace the symlinks.
//
// It returns a note for every entry it had to move aside.
func PrepareHome(l Layout, uid, gid int) (notes []string, err error) {
	if err := os.MkdirAll(l.Home, 0o755); err != nil {
		return nil, err
	}
	// With the filesystem widened to the whole VPS (M6.8, ADR-0004), aosd must never
	// relocate a real dotfile in a home it does not own — pointed at a live
	// /home/ubuntu the adopt() below would move the ~/.ssh/authorized_keys that is
	// the only way into the server. The installer drops a marker in the home it
	// creates for AOS (install.sh create_user), and only there: its presence is
	// what says this home is AOS's own, so absorbing the stock skel dotfiles that
	// `useradd --create-home` left is exactly right. Without the marker a real file
	// or a foreign symlink under a Protected name means a home AOS did not create,
	// so refuse the whole home rather than touch it.
	if !ownsHome(l) {
		if link, ok := unownedDotfile(l); ok {
			return nil, fmt.Errorf("refusing to prepare %s: %s is not one of AOS's own links and %s is absent, so this looks like a home AOS did not create; relocating it could move another user's ~/.ssh/authorized_keys", l.Home, link, homeMarker)
		}
	}
	if err := os.MkdirAll(l.Protected, 0o755); err != nil {
		return nil, err
	}
	if err := setOwner(l.Protected, 0, 0, 0o755); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")

	for _, e := range ProtectedEntries {
		link, target := l.link(e)
		moved, err := adopt(link, target, stamp)
		if err != nil {
			return notes, err
		}
		if moved != "" {
			notes = append(notes, moved)
		}
		if err := createTarget(e, target, uid, gid); err != nil {
			return notes, err
		}
		if err := ensureSymlink(link, target); err != nil {
			return notes, err
		}
	}

	// The Shared Folder is retired (M6.6). Remove the root-owned ~/Shared symlink
	// an older install left behind; it strands in a sticky 1775 home otherwise. A
	// real folder a user made keeping the name is left untouched.
	shared := filepath.Join(l.Home, "Shared")
	if fi, err := os.Lstat(shared); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(shared); err != nil {
			return notes, err
		}
		notes = append(notes, fmt.Sprintf("removed the retired Shared Folder link %s", shared))
	}
	return notes, setOwner(l.Home, 0, gid, 0o775|os.ModeSticky)
}

// homeMarker is the file the installer drops in the home it creates for AOS
// (install.sh create_user, "the marker is what tells aosd this home is its own").
// It is what distinguishes AOS's own home — where the stock skel dotfiles are
// AOS's to absorb into Protected — from a stranger's home on the widened
// filesystem, where a real ~/.ssh must never be relocated (M6.8, ADR-0004).
const homeMarker = ".aos-home"

// ownsHome reports whether Home carries the marker the installer drops to
// declare the home AOS's own. Only root writes it, and PrepareHome only ever
// runs against the configured Home, so its presence is a trustworthy signal
// that the Protected dotfiles here are AOS's to manage.
func ownsHome(l Layout) bool {
	_, err := os.Lstat(filepath.Join(l.Home, homeMarker))
	return err == nil
}

// unownedDotfile returns the first Protected dotfile in Home that exists but is
// not already AOS's own symlink to its target: a real file or folder, or a symlink
// pointing somewhere else. In a home AOS created these are all absent (a fresh
// home) or its own links (an existing one); anything else means the home belongs
// to another user, whose files must not be relocated (M6.8).
func unownedDotfile(l Layout) (link string, unowned bool) {
	for _, e := range ProtectedEntries {
		link, target := l.link(e)
		fi, err := os.Lstat(link)
		if err != nil {
			continue // absent: a fresh home, or a dotfile AOS will create
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if dest, err := os.Readlink(link); err == nil && dest == target {
				continue // already AOS's own link
			}
		}
		return link, true
	}
	return "", false
}

// adopt moves a real (non-symlink) entry at link into target. A symlink at link
// that points elsewhere is removed.
func adopt(link, target, stamp string) (note string, err error) {
	fi, err := os.Lstat(link)
	switch {
	case os.IsNotExist(err):
		return "", nil
	case err != nil:
		return "", err
	case fi.Mode()&os.ModeSymlink != 0:
		if dest, err := os.Readlink(link); err == nil && dest == target {
			return "", nil
		}
		return "", os.Remove(link)
	}
	if _, err := os.Lstat(target); err == nil {
		aside := target + ".from-home-" + stamp
		if err := os.Rename(link, aside); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s already existed; moved %s to %s", target, link, aside), nil
	}
	return "", os.Rename(link, target)
}

func createTarget(e ProtectedEntry, target string, uid, gid int) error {
	if _, err := os.Lstat(target); err == nil || (!e.Dir && e.Seed == "") {
		return nil
	}
	if e.Dir {
		if err := os.Mkdir(target, e.Mode); err != nil {
			return err
		}
		return setOwner(target, uid, gid, e.Mode)
	}
	src, err := os.Open(e.Seed)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, e.Mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return setOwner(target, uid, gid, e.Mode)
}

// ensureSymlink makes link a root-owned symlink to target.
func ensureSymlink(link, target string) error {
	if dest, err := os.Readlink(link); err == nil {
		if dest == target {
			return os.Lchown(link, 0, 0)
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	if err := os.Symlink(target, link); err != nil {
		return err
	}
	return os.Lchown(link, 0, 0)
}

func setOwner(path string, uid, gid int, mode os.FileMode) error {
	if err := os.Lchown(path, uid, gid); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// link returns the path of e's symlink and its target.
func (l Layout) link(e ProtectedEntry) (link, target string) {
	return filepath.Join(l.Home, e.Link), filepath.Join(l.Protected, e.Target)
}

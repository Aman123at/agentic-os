package software

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// FileState is a path's state in a tracked tree (/etc).
type FileState struct {
	Type   string `json:"type"` // file | dir | symlink
	Blob   string `json:"blob,omitempty"`
	Target string `json:"target,omitempty"`
	Mode   uint32 `json:"mode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
}

// maxTracked is the largest file whose content is kept.
const maxTracked = 16 << 20

// Tree tracks a folder so that the changes of each operation can be recorded,
// undone and replayed. Every file's content is copied into Blobs, named by its
// SHA-256, when it is first seen: a change always has the old content to go back to.
type Tree struct {
	Root  string
	Blobs string
	// Skip lists paths that are never tracked, with everything beneath them.
	Skip []string

	cache map[string]cached
}

// cached remembers a file's hash while its metadata is unchanged.
type cached struct {
	size  int64
	mod   time.Time
	ino   uint64
	mode  fs.FileMode
	state FileState
}

// EtcSkip are /etc paths Docker mounts or programs regenerate.
var EtcSkip = []string{"/etc/hostname", "/etc/hosts", "/etc/resolv.conf", "/etc/mtab", "/etc/ld.so.cache", "/etc/.pwd.lock"}

// Snapshot maps paths to their states.
type Snapshot map[string]FileState

func (t *Tree) skipped(path string) bool {
	for _, s := range t.Skip {
		if path == s || strings.HasPrefix(path, s+"/") {
			return true
		}
	}
	// Backups such as /etc/passwd- change whenever the file does.
	return filepath.Dir(path) == t.Root && strings.HasSuffix(path, "-")
}

// Scan records the tree's state.
func (t *Tree) Scan() (Snapshot, error) {
	if t.cache == nil {
		t.cache = map[string]cached{}
	}
	snap := Snapshot{}
	seen := map[string]bool{}
	err := filepath.WalkDir(t.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // removed while walking
			}
			return err
		}
		if t.skipped(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return nil
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("%s: no ownership information", path)
		}
		state := FileState{Mode: uint32(fi.Mode().Perm() | fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)), UID: int(st.Uid), GID: int(st.Gid)}
		switch {
		case fi.IsDir():
			state.Type = "dir"
		case fi.Mode()&fs.ModeSymlink != 0:
			state.Type = "symlink"
			if state.Target, err = os.Readlink(path); err != nil {
				return nil
			}
		case fi.Mode().IsRegular() && fi.Size() <= maxTracked:
			c, hit := t.cache[path]
			if hit && c.size == fi.Size() && c.mod.Equal(fi.ModTime()) && c.ino == uint64(st.Ino) && c.mode == fi.Mode() {
				state.Type, state.Blob = "file", c.state.Blob
			} else {
				blob, err := t.store(path)
				if err != nil {
					return err
				}
				state.Type, state.Blob = "file", blob
			}
			t.cache[path] = cached{size: fi.Size(), mod: fi.ModTime(), ino: uint64(st.Ino), mode: fi.Mode(), state: state}
		default:
			return nil // sockets, devices, very large files
		}
		seen[path] = true
		snap[path] = state
		return nil
	})
	for p := range t.cache {
		if !seen[p] {
			delete(t.cache, p)
		}
	}
	return snap, err
}

// store copies a file's content into Blobs and returns its hash.
func (t *Tree) store(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxTracked+1))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	dest := t.blobPath(hash)
	if _, err := os.Stat(dest); err == nil {
		return hash, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", dest, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	return hash, os.Rename(tmp, dest)
}

func (t *Tree) blobPath(hash string) string {
	return filepath.Join(t.Blobs, hash[:2], hash)
}

// diffFiles returns the paths whose state changed between two snapshots.
func diffFiles(before, after Snapshot) []Change {
	var out []Change
	state := func(s Snapshot, path string) string {
		f, ok := s[path]
		if !ok {
			return ""
		}
		b, _ := json.Marshal(f)
		return string(b)
	}
	paths := map[string]bool{}
	for p := range before {
		paths[p] = true
	}
	for p := range after {
		paths[p] = true
	}
	for p := range paths {
		if b, a := state(before, p), state(after, p); b != a {
			out = append(out, Change{Kind: KindFile, Name: p, Before: b, After: a})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Apply gives each path the state it maps to (JSON; "" removes the path).
// Removals go deepest first, so a folder is empty when its turn comes; a
// folder that still holds untracked entries is kept, with a note.
func (t *Tree) Apply(states map[string]string) (notes []string, err error) {
	paths := make([]string, 0, len(states))
	for p := range states {
		if !t.skipped(p) && (p == t.Root || strings.HasPrefix(p, t.Root+"/")) {
			paths = append(paths, p)
		}
	}
	depth := func(p string) int { return strings.Count(p, "/") }
	sort.Slice(paths, func(i, j int) bool {
		return depth(paths[i]) > depth(paths[j]) || depth(paths[i]) == depth(paths[j]) && paths[i] < paths[j]
	})
	for _, p := range paths {
		if states[p] != "" {
			continue
		}
		fi, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return notes, err
		case fi.IsDir():
			if err := os.Remove(p); err != nil {
				notes = append(notes, fmt.Sprintf("kept %s: it holds files AOS did not record", p))
			}
		default:
			if err := os.Remove(p); err != nil {
				return notes, err
			}
		}
	}
	// Creations shallowest first, so folders exist before what goes in them.
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		if states[p] == "" {
			continue
		}
		var f FileState
		if err := json.Unmarshal([]byte(states[p]), &f); err != nil {
			return notes, fmt.Errorf("%s: %w", p, err)
		}
		if err := t.write(p, f); err != nil {
			return notes, fmt.Errorf("%s: %w", p, err)
		}
	}
	t.cache = nil
	return notes, nil
}

// write gives one path its state.
func (t *Tree) write(path string, f FileState) error {
	mode := fs.FileMode(f.Mode).Perm()
	for bit, m := range map[uint32]fs.FileMode{uint32(fs.ModeSetuid): fs.ModeSetuid, uint32(fs.ModeSetgid): fs.ModeSetgid, uint32(fs.ModeSticky): fs.ModeSticky} {
		if f.Mode&bit != 0 {
			mode |= m
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cur, err := os.Lstat(path)
	exists := err == nil
	switch f.Type {
	case "dir":
		if exists && !cur.IsDir() {
			if err := os.Remove(path); err != nil {
				return err
			}
			exists = false
		}
		if !exists {
			if err := os.Mkdir(path, mode); err != nil {
				return err
			}
		}
	case "symlink":
		if exists {
			if err := removeEntry(path, cur); err != nil {
				return err
			}
		}
		if err := os.Symlink(f.Target, path); err != nil {
			return err
		}
		return os.Lchown(path, f.UID, f.GID)
	case "file":
		data, err := os.ReadFile(t.blobPath(f.Blob))
		if err != nil {
			return fmt.Errorf("its recorded content is missing: %w", err)
		}
		tmp := filepath.Join(filepath.Dir(path), ".aos-restore-"+filepath.Base(path))
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := os.Lchown(tmp, f.UID, f.GID); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Chmod(tmp, mode); err != nil {
			os.Remove(tmp)
			return err
		}
		if exists && cur.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				os.Remove(tmp)
				return err
			}
		}
		return os.Rename(tmp, path)
	default:
		return fmt.Errorf("unknown type %q", f.Type)
	}
	if err := os.Lchown(path, f.UID, f.GID); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func removeEntry(path string, fi fs.FileInfo) error {
	if fi.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// Same reports whether a path's current state is the JSON state s ("" absent).
func (s Snapshot) Same(path, state string) bool {
	f, ok := s[path]
	if state == "" || !ok {
		return state == "" && !ok
	}
	b, _ := json.Marshal(f)
	return string(b) == state
}

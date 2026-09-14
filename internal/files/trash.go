package files

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TrashItem is one entry in a Trash.
type TrashItem struct {
	ID           string // "<trash>/<name>", see trashByID
	OriginalPath string
	DeletedAt    time.Time
	Size         int64
	Dir          bool
}

const trashTime = "2006-01-02T15:04:05"

// trash is one freedesktop.org Trash folder.
type trash struct {
	key string // "home" or "shared"
	dir string
}

func (t trash) files() string { return filepath.Join(t.dir, "files") }
func (t trash) info() string  { return filepath.Join(t.dir, "info") }

func (o Ops) trashes() []trash {
	ts := []trash{{key: "home", dir: filepath.Join(o.Home, ".local", "share", "Trash")}}
	if o.Shared != "" {
		ts = append(ts, trash{key: "shared", dir: filepath.Join(o.Shared, fmt.Sprintf(".Trash-%d", o.UID))})
	}
	return ts
}

// Delete moves path to the Trash on its filesystem.
func (o Ops) Delete(path string) (TrashItem, error) {
	path = filepath.Clean(path)
	fi, err := os.Lstat(path)
	if err != nil {
		return TrashItem{}, err
	}
	if o.disposable(path) {
		return TrashItem{}, os.RemoveAll(path)
	}
	t := o.trashes()[0]
	if o.Shared != "" && within(path, o.Shared) {
		t = o.trashes()[1]
	}
	for _, d := range []string{t.files(), t.info()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return TrashItem{}, err
		}
	}
	name, info, err := reserve(t, filepath.Base(path))
	if err != nil {
		return TrashItem{}, err
	}
	deleted := o.now()
	body := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", escapePath(path), deleted.Format(trashTime))
	if _, err := info.WriteString(body); err != nil {
		info.Close()
		os.Remove(info.Name())
		return TrashItem{}, err
	}
	if err := info.Close(); err != nil {
		os.Remove(info.Name())
		return TrashItem{}, err
	}
	if err := os.Rename(path, filepath.Join(t.files(), name)); err != nil {
		os.Remove(info.Name())
		return TrashItem{}, err
	}
	return TrashItem{ID: t.key + "/" + name, OriginalPath: path, DeletedAt: deleted, Size: size(fi, filepath.Join(t.files(), name)), Dir: fi.IsDir()}, nil
}

// reserve creates the .trashinfo file for a free name, which claims the name.
func reserve(t trash, base string) (string, *os.File, error) {
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			ext := filepath.Ext(base)
			name = fmt.Sprintf("%s.%d%s", strings.TrimSuffix(base, ext), i, ext)
		}
		f, err := os.OpenFile(filepath.Join(t.info(), name+".trashinfo"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		if _, err := os.Lstat(filepath.Join(t.files(), name)); err == nil {
			// An entry without info (e.g. left by a crash): keep it, try the next name.
			f.Close()
			os.Remove(f.Name())
			continue
		}
		return name, f, nil
	}
}

// ListTrash lists every Trash, newest first.
func (o Ops) ListTrash() ([]TrashItem, error) {
	var items []TrashItem
	for _, t := range o.trashes() {
		entries, err := os.ReadDir(t.info())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".trashinfo")
			if !ok {
				continue
			}
			item, err := readItem(t, name)
			if err != nil {
				continue
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	return items, nil
}

func readItem(t trash, name string) (TrashItem, error) {
	f, err := os.Open(filepath.Join(t.info(), name+".trashinfo"))
	if err != nil {
		return TrashItem{}, err
	}
	defer f.Close()
	item := TrashItem{ID: t.key + "/" + name}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "Path":
			p, err := url.PathUnescape(v)
			if err != nil {
				return TrashItem{}, err
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(t.dir), p)
			}
			item.OriginalPath = p
		case "DeletionDate":
			item.DeletedAt, _ = time.ParseInLocation(trashTime, v, time.Local)
		}
	}
	fi, err := os.Lstat(filepath.Join(t.files(), name))
	if err != nil {
		return TrashItem{}, err
	}
	item.Dir = fi.IsDir()
	item.Size = size(fi, filepath.Join(t.files(), name))
	return item, nil
}

// Restore moves a Trash item back to where it was deleted from.
func (o Ops) Restore(id string) (string, error) {
	t, name, err := o.trashByID(id)
	if err != nil {
		return "", err
	}
	item, err := readItem(t, name)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(item.OriginalPath); err == nil {
		return "", fmt.Errorf("restore %s: %w", item.OriginalPath, ErrExists)
	}
	if err := os.MkdirAll(filepath.Dir(item.OriginalPath), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(t.files(), name), item.OriginalPath); err != nil {
		return "", err
	}
	return item.OriginalPath, os.Remove(filepath.Join(t.info(), name+".trashinfo"))
}

func (o Ops) trashByID(id string) (trash, string, error) {
	key, name, ok := strings.Cut(id, "/")
	if ok && name != "" && !strings.ContainsAny(name, "/\x00") && name != "." && name != ".." {
		for _, t := range o.trashes() {
			if t.key == key {
				return t, name, nil
			}
		}
	}
	return trash{}, "", fmt.Errorf("unknown Trash item %q", id)
}

// escapePath percent-encodes path for a .trashinfo file, keeping "/".
func escapePath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// size is the size of a file, or the total size of a folder's files.
func size(fi fs.FileInfo, path string) int64 {
	if !fi.IsDir() {
		return fi.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// disposableNames are folders whose contents can always be recreated.
var disposableNames = map[string]bool{"node_modules": true, "__pycache__": true, ".cache": true}

// disposable reports whether path is deleted permanently instead of trashed.
func (o Ops) disposable(path string) bool {
	scratch := o.Scratch
	if scratch == nil {
		scratch = []string{"/tmp", "/var/tmp"}
	}
	for _, s := range scratch {
		if within(path, s) {
			return true
		}
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if disposableNames[part] {
			return true
		}
	}
	return false
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	dir = filepath.Clean(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// EmptyTrash permanently removes every Trash item. Only the user may do this.
func (o Ops) EmptyTrash() (int, error) {
	items, err := o.ListTrash()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, it := range items {
		if err := o.purge(it.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// ExpireTrash removes items older than retention, then the oldest items until
// the Trash holds at most maxBytes (PLAN.md §7.8).
func (o Ops) ExpireTrash(retention time.Duration, maxBytes int64) (int, error) {
	items, err := o.ListTrash() // newest first
	if err != nil {
		return 0, err
	}
	var total int64
	for _, it := range items {
		total += it.Size
	}
	removed := 0
	cutoff := o.now().Add(-retention)
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if !it.DeletedAt.Before(cutoff) && total <= maxBytes {
			break
		}
		if err := o.purge(it.ID); err != nil {
			return removed, err
		}
		total -= it.Size
		removed++
	}
	return removed, nil
}

func (o Ops) purge(id string) error {
	t, name, err := o.trashByID(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(t.files(), name)); err != nil {
		return err
	}
	return os.Remove(filepath.Join(t.info(), name+".trashinfo"))
}

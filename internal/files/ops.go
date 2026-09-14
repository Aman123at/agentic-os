// Package files performs file operations and manages the Trash (PLAN.md §7.8).
//
// Ops runs in whichever process is allowed to touch the files: for Agents, the
// Landlock-confined helper; for the user, a process running as the aos user.
package files

import (
	"bufio"
	"bytes"
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
	"unicode/utf8"
)

// Ops performs file operations for one uid.
type Ops struct {
	Home   string `json:"home"`   // the home folder, whose Trash is ~/.local/share/Trash
	Shared string `json:"shared"` // the Shared Folder, whose Trash is .Trash-<uid> in it
	UID    int    `json:"uid"`
	// Now is the clock for deletion dates; nil means time.Now.
	Now func() time.Time `json:"-"`
	// Scratch folders are deleted from permanently; nil means /tmp and /var/tmp.
	Scratch []string `json:"scratch,omitempty"`
}

// ErrExists is returned when a destination exists and overwriting was not requested.
var ErrExists = errors.New("already exists")

func (o Ops) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// Write writes content to path, creating missing folders. An existing file is
// replaced only when overwrite is set; it is written in place, so symlinks and
// the file's mode survive.
func (o Ops) Write(path string, content []byte, overwrite bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("write %s: %w", path, ErrExists)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Edit is one search-and-replace in a file.
type Edit struct {
	Old string `json:"old"`
	New string `json:"new"`
	// All replaces every match; otherwise Old must match exactly once.
	All bool `json:"all,omitempty"`
}

// Edit applies edits in order. Either all apply or the file is unchanged.
func (o Ops) Edit(path string, edits []Edit) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(b)
	for i, e := range edits {
		if e.Old == "" {
			return fmt.Errorf("edit %d: old text is empty", i+1)
		}
		n := strings.Count(content, e.Old)
		switch {
		case n == 0:
			return fmt.Errorf("edit %d: old text not found in %s", i+1, path)
		case n > 1 && !e.All:
			return fmt.Errorf("edit %d: old text found %d times in %s; add context to make it unique, or set all", i+1, n, path)
		}
		content = strings.ReplaceAll(content, e.Old, e.New)
	}
	return o.Write(path, []byte(content), true)
}

// Move renames src to dst, copying across filesystems. An existing dst is
// replaced only when overwrite is set.
func (o Ops) Move(src, dst string, overwrite bool) error {
	if _, err := os.Lstat(src); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil && !overwrite {
		return fmt.Errorf("move to %s: %w", dst, ErrExists)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := os.Rename(src, dst)
	if !isCrossDevice(err) {
		return err
	}
	if err := o.Copy(src, dst, overwrite); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// Copy copies a file, symlink or folder tree. An existing dst is written over
// only when overwrite is set.
func (o Ops) Copy(src, dst string, overwrite bool) error {
	if _, err := os.Lstat(dst); err == nil && !overwrite {
		return fmt.Errorf("copy to %s: %w", dst, ErrExists)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		}
		return nil // sockets, devices and pipes are not copied
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func isCrossDevice(err error) bool {
	var linkErr *os.LinkError
	return errors.As(err, &linkErr) && errors.Is(linkErr.Err, syscall.EXDEV)
}

// Text is part of a text file, by lines.
type Text struct {
	Content    string `json:"content"`
	FirstLine  int    `json:"first_line"`
	LastLine   int    `json:"last_line"`
	TotalLines int    `json:"total_lines"`
	// Truncated is set when maxBytes cut the range short.
	Truncated bool  `json:"truncated,omitempty"`
	Binary    bool  `json:"binary,omitempty"`
	Size      int64 `json:"size"`
}

// ReadText reads lines first..last (1-based, inclusive; 0 means from the start or
// to the end), stopping at whole lines within maxBytes. Binary files return no content.
func (o Ops) ReadText(path string, first, last int, maxBytes int) (Text, error) {
	f, err := os.Open(path)
	if err != nil {
		return Text{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Text{}, err
	}
	if fi.IsDir() {
		return Text{}, fmt.Errorf("%s is a folder", path)
	}
	t := Text{Size: fi.Size()}
	head := make([]byte, 8000)
	n, _ := io.ReadFull(f, head)
	if isBinary(head[:n]) {
		t.Binary = true
		return t, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Text{}, err
	}
	if first < 1 {
		first = 1
	}
	var b strings.Builder
	r := bufio.NewReaderSize(f, 64<<10)
	for line := 1; ; line++ {
		s, err := r.ReadString('\n')
		if s == "" && err != nil {
			break
		}
		t.TotalLines = line
		if line >= first && (last == 0 || line <= last) && !t.Truncated {
			if b.Len()+len(s) > maxBytes {
				t.Truncated = true
			} else {
				b.WriteString(s)
				if t.FirstLine == 0 {
					t.FirstLine = line
				}
				t.LastLine = line
			}
		}
		if err != nil {
			break
		}
	}
	t.Content = b.String()
	return t, nil
}

// MaxRead caps a single Read: the Desktop pulls larger files in successive chunks.
const MaxRead = 1 << 20

// Raw is a slice of a file's bytes: EOF reports that offset+len(Content) reached
// the end, so the Desktop knows when to stop chunking (Quick Look, downloads).
type Raw struct {
	Content []byte `json:"content"`
	EOF     bool   `json:"eof"`
}

// Read returns up to limit bytes of path from offset. A limit of 0, or one over
// MaxRead, reads MaxRead bytes. Unlike ReadText it is byte-exact and works on
// binary files, so it backs image Quick Look and download-to-Host.
func (o Ops) Read(path string, offset, limit int64) (Raw, error) {
	if limit <= 0 || limit > MaxRead {
		limit = MaxRead
	}
	f, err := os.Open(path)
	if err != nil {
		return Raw{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Raw{}, err
	}
	if fi.IsDir() {
		return Raw{}, fmt.Errorf("%s is a folder", path)
	}
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, limit)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return Raw{}, err
	}
	return Raw{Content: buf[:n], EOF: err == io.EOF || offset+int64(n) >= fi.Size()}, nil
}

// isBinary reports whether data looks like a binary file: a NUL byte, or invalid UTF-8.
func isBinary(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	// A multi-byte character may be cut at the end of the sample.
	for i := 0; i < 4 && len(data) > 0 && !utf8.Valid(data); i++ {
		data = data[:len(data)-1]
	}
	return !utf8.Valid(data)
}

// Info describes a file.
type Info struct {
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	Dir        bool        `json:"dir,omitempty"`
	Symlink    bool        `json:"symlink,omitempty"`
	LinkTarget string      `json:"link_target,omitempty"`
	Size       int64       `json:"size"`
	Mode       fs.FileMode `json:"mode"`
	ModTime    time.Time   `json:"modified"`
}

// Stat describes path without following a final symlink; Dir reports whether
// the symlink's target is a folder.
func (o Ops) Stat(path string) (Info, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return Info{}, err
	}
	info := Info{Path: path, Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), Mode: fi.Mode(), ModTime: fi.ModTime()}
	if fi.Mode()&fs.ModeSymlink != 0 {
		info.Symlink = true
		info.LinkTarget, _ = os.Readlink(path)
		if target, err := os.Stat(path); err == nil {
			info.Dir = target.IsDir()
		}
	}
	return info, nil
}

// List lists a folder: folders first, then files, each by name ignoring case.
func (o Ops) List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0, len(entries))
	for _, e := range entries {
		info, err := o.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // removed meanwhile
		}
		infos = append(infos, info)
	}
	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].Dir != infos[j].Dir {
			return infos[i].Dir
		}
		return strings.ToLower(infos[i].Name) < strings.ToLower(infos[j].Name)
	})
	return infos, nil
}

// SearchQuery selects files by name pattern and/or content.
type SearchQuery struct {
	Name    string `json:"name,omitempty"`    // glob against the file name, e.g. "*.pdf"
	Content string `json:"content,omitempty"` // substring, matched per line
	Limit   int    `json:"limit,omitempty"`
}

// Match is a search result; Line and Text are set for content matches.
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

// skippedDirs are not searched: tool internals and dependencies.
var skippedDirs = map[string]bool{".git": true, "node_modules": true, "__pycache__": true, ".cache": true, ".venv": true}

const maxSearchFile = 4 << 20

// Search walks root for matching files, without following symlinks.
func (o Ops) Search(root string, q SearchQuery) ([]Match, error) {
	if q.Limit <= 0 {
		q.Limit = 200
	}
	var matches []Match
	errLimit := errors.New("limit")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable folders are skipped
		}
		if d.IsDir() {
			if path != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if q.Name != "" {
			if ok, _ := filepath.Match(q.Name, d.Name()); !ok {
				return nil
			}
		}
		if q.Content == "" {
			matches = append(matches, Match{Path: path})
		} else if err := searchFile(path, q.Content, q.Limit, &matches); err != nil {
			return nil
		}
		if len(matches) >= q.Limit {
			return errLimit
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLimit) {
		return nil, err
	}
	if len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}
	return matches, nil
}

func searchFile(path, needle string, limit int, matches *[]Match) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() > maxSearchFile {
		return err
	}
	head := make([]byte, 8000)
	n, _ := io.ReadFull(f, head)
	if isBinary(head[:n]) {
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxSearchFile)
	for line := 1; sc.Scan(); line++ {
		if strings.Contains(sc.Text(), needle) {
			text := sc.Text()
			if len(text) > 300 {
				text = text[:300] + "…"
			}
			*matches = append(*matches, Match{Path: path, Line: line, Text: strings.TrimSpace(text)})
			if len(*matches) >= limit {
				return nil
			}
		}
	}
	return nil
}

package code

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backups keeps the versions of Sensitive System Files that confirmed writes
// replaced (PLAN.md M8.5), so a mistake has a way back: the last Keep per
// file, under Dir/<the path, escaped>/<when>. Only regular files are kept, up
// to MaxBackup bytes; a folder moved or deleted is in the Trash already.
type Backups struct {
	Dir string
	// Keep is how many versions of a file are kept; 0 means 5.
	Keep int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// MaxBackup is the largest file a backup keeps: far more than any
// configuration file, and it bounds what a write to /usr/bin can cost.
const MaxBackup = 16 << 20

// Backup is one kept version.
type Backup struct {
	ID      string
	SavedAt time.Time
	Size    int64
}

// ErrNoBackup is returned for a version that is not kept.
var ErrNoBackup = errors.New("no such kept version")

const idLayout = "20060102T150405.000000000Z"

func (b *Backups) dir(path string) string {
	return filepath.Join(b.Dir, url.PathEscape(filepath.Clean(path)))
}

// Save keeps the file now at path, before a confirmed write replaces it. A
// path with nothing there, or something other than a regular file, keeps
// nothing.
func (b *Backups) Save(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !fi.Mode().IsRegular()) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Size() > MaxBackup {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("keeping the previous version of %s: %w", path, err)
	}
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	dir := b.dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, now().UTC().Format(idLayout)), data, 0o600); err != nil {
		return fmt.Errorf("keeping the previous version of %s: %w", path, err)
	}
	return b.prune(path)
}

func (b *Backups) prune(path string) error {
	keep := b.Keep
	if keep <= 0 {
		keep = 5
	}
	list, err := b.List(path)
	if err != nil {
		return err
	}
	for _, old := range list[min(keep, len(list)):] {
		if err := os.Remove(filepath.Join(b.dir(path), old.ID)); err != nil {
			return err
		}
	}
	return nil
}

// List returns the versions kept for path, newest first.
func (b *Backups) List(path string) ([]Backup, error) {
	entries, err := os.ReadDir(b.dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		at, err := time.Parse(idLayout, e.Name())
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{ID: e.Name(), SavedAt: at, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SavedAt.After(out[j].SavedAt) })
	return out, nil
}

// Read returns a kept version of path.
func (b *Backups) Read(path, id string) ([]byte, error) {
	if _, err := time.Parse(idLayout, id); err != nil {
		return nil, ErrNoBackup
	}
	data, err := os.ReadFile(filepath.Join(b.dir(path), id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoBackup
	}
	return data, err
}

// Check is the result of running a Sensitive System File's own checker.
type Check struct {
	// Checker is what ran, e.g. "visudo -c"; empty when the file has none.
	Checker     string
	Passed      bool
	Output      string
	Unavailable bool // the file has a checker, but it isn't installed
}

// Checker runs the checks. Its fields are test seams; zero values use the
// Machine's programs.
type Checker struct {
	Command  func(ctx context.Context, name string, args ...string) ([]byte, error)
	LookPath func(name string) (string, error)
	// TempDir holds the content being checked; "" is the system's.
	TempDir string
	// Rules replaces which program checks which file.
	Rules func(path, file string) (name string, args []string, label string)
}

// checkFor is the program and arguments that check path's content, written to
// file, and how the check is named.
func checkFor(path, file string) (name string, args []string, label string) {
	p := filepath.Clean(path)
	switch {
	case p == "/etc/sudoers" || strings.HasPrefix(p, "/etc/sudoers.d/"):
		return "visudo", []string{"-c", "-f", file}, "visudo -c"
	case p == "/etc/ssh/sshd_config":
		return "sshd", []string{"-t", "-f", file}, "sshd -t"
	case p == "/etc/fstab":
		return "findmnt", []string{"--verify", "--tab-file", file}, "findmnt --verify"
	}
	return "", nil, ""
}

// Check runs path's checker on content. A file without one checks nothing.
func (c Checker) Check(ctx context.Context, path string, content []byte) (Check, error) {
	rules := c.Rules
	if rules == nil {
		rules = checkFor
	}
	name, _, label := rules(path, "")
	if name == "" {
		return Check{}, nil
	}
	look := c.LookPath
	if look == nil {
		look = lookSbin
	}
	prog, err := look(name)
	if err != nil {
		return Check{Checker: label, Unavailable: true}, nil
	}
	f, err := os.CreateTemp(c.TempDir, "aos-check-*")
	if err != nil {
		return Check{}, err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, bytes.NewReader(content))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Check{}, err
	}
	_, args, _ := rules(path, f.Name())
	run := c.Command
	if run == nil {
		run = combined
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := run(ctx, prog, args...)
	// The checker names the temporary file; the person saving knows it as path.
	output := strings.TrimSpace(strings.ReplaceAll(string(out), f.Name(), path))
	var exit *exec.ExitError
	switch {
	case err == nil:
		return Check{Checker: label, Passed: true, Output: output}, nil
	case errors.As(err, &exit):
		return Check{Checker: label, Output: output}, nil
	}
	return Check{}, fmt.Errorf("%s: %w", label, err)
}

func combined(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// lookSbin finds a program on PATH or in the sbin folders, where visudo and
// sshd live but which a service's PATH may lack.
func lookSbin(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

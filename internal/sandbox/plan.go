// Package sandbox confines Agent Sessions with Landlock and no_new_privs (ADR-0004).
//
// Landlock can only grant access to whole directory trees; it cannot carve an
// exception out of a tree it has granted. Plan therefore turns "everything under
// X except these paths" into grants on the siblings of every excluded path.
package sandbox

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Access is a set of access rights granted on a path and everything beneath it.
type Access uint8

const (
	// List allows listing directory entries, but not reading files.
	List Access = 1 << iota
	// Read allows reading files, listing directories and executing programs.
	Read
	// Create allows creating new files and directories, but not writing to them.
	Create
	// Write allows every filesystem operation.
	Write
)

func (a Access) String() string {
	var names []string
	for _, n := range []struct {
		bit  Access
		name string
	}{{List, "list"}, {Read, "read"}, {Create, "create"}, {Write, "write"}} {
		if a&n.bit != 0 {
			names = append(names, n.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, "+")
}

// Policy describes what an Agent Session may touch. All paths are absolute.
type Policy struct {
	// Hidden paths can be neither read nor written.
	Hidden []string
	// Protected paths can be read but not written.
	Protected []string
	// Writable trees can be written, except for Protected and Hidden paths inside them.
	Writable []string
}

// Grant gives Access on Path and everything beneath it.
type Grant struct {
	Path   string
	Access Access
}

// Ruleset is the result of planning a Policy against the current filesystem.
type Ruleset struct {
	Grants []Grant
	// split maps each directory that was split to its entries at planning time.
	split map[string][]string
}

// FS is the filesystem Plan inspects, rooted at "/" (see RootFS).
type FS interface {
	fs.ReadDirFS
	fs.ReadLinkFS
}

// Plan computes the grants that implement p on the filesystem fsys.
func Plan(p Policy, fsys FS) (Ruleset, error) {
	rs := Ruleset{split: map[string][]string{}}
	hidden := withTargets(fsys, p.Hidden)
	if err := carve(fsys, "/", hidden, Read, List, &rs); err != nil {
		return Ruleset{}, err
	}
	readOnly := append(withTargets(fsys, p.Protected), hidden...)
	for _, w := range cleanAll(p.Writable) {
		if _, err := fs.Stat(fsys, fsPath(w)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := carve(fsys, w, readOnly, Write, Create, &rs); err != nil {
			return Ruleset{}, err
		}
	}
	return rs, nil
}

// withTargets cleans paths and adds the final target of every path that is, or
// passes through, a symlink: protecting ~/.bashrc must also protect the file it
// links to.
func withTargets(fsys FS, paths []string) []string {
	out := cleanAll(paths)
	for _, p := range out {
		if t := resolve(fsys, p); t != p {
			out = append(out, t)
		}
	}
	return out
}

func cleanAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, path.Clean("/"+p))
	}
	return out
}

// resolve follows symlinks in every component of the absolute path p, as the
// kernel would. Components that do not exist are kept as they are.
func resolve(fsys FS, p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	cur := "/"
	for hops := 0; len(parts) > 0 && hops < 40; {
		next := path.Join(cur, parts[0])
		parts = parts[1:]
		fi, err := fsys.Lstat(fsPath(next))
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		target, err := fsys.ReadLink(fsPath(next))
		if err != nil {
			cur = next
			continue
		}
		hops++
		if !path.IsAbs(target) {
			target = path.Join(cur, target)
		}
		parts = append(strings.Split(strings.TrimPrefix(path.Clean(target), "/"), "/"), parts...)
		cur = "/"
	}
	return path.Clean(path.Join(append([]string{cur}, parts...)...))
}

// carve grants full on every path under root that contains no excluded path, and
// partial on each directory that had to be split to exclude something.
func carve(fsys FS, root string, excluded []string, full, partial Access, rs *Ruleset) error {
	if isExcluded(root, excluded) {
		return nil
	}
	if !containsExcluded(root, excluded) {
		rs.Grants = append(rs.Grants, Grant{Path: root, Access: full})
		return nil
	}
	rs.Grants = append(rs.Grants, Grant{Path: root, Access: partial})
	entries, err := fsys.ReadDir(fsPath(root))
	if err != nil {
		return err
	}
	rs.split[root] = signature(entries)
	for _, e := range entries {
		// Landlock follows symlinks when adding a rule, so granting a link would
		// grant its target, which may be Protected. The target keeps its own grants.
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if err := carve(fsys, path.Join(root, e.Name()), excluded, full, partial, rs); err != nil {
			return err
		}
	}
	return nil
}

// Stale reports whether a directory that was split has gained, lost or replaced
// an entry since planning. A stale Ruleset must be re-planned and re-applied to a
// fresh process: Landlock rules can never be widened once enforced.
func (rs Ruleset) Stale(fsys fs.ReadDirFS) (bool, error) {
	for dir, want := range rs.split {
		entries, err := fsys.ReadDir(fsPath(dir))
		if err != nil {
			return true, err
		}
		if !slices.Equal(signature(entries), want) {
			return true, nil
		}
	}
	return false, nil
}

// signature describes a directory listing by entry name and type.
// fs.ReadDir returns entries sorted by name.
func signature(entries []fs.DirEntry) []string {
	sig := make([]string, len(entries))
	for i, e := range entries {
		sig[i] = e.Name() + ":" + e.Type().String()
	}
	return sig
}

// isExcluded reports whether p is an excluded path or lies beneath one.
func isExcluded(p string, excluded []string) bool {
	for _, x := range excluded {
		if within(p, x) {
			return true
		}
	}
	return false
}

// containsExcluded reports whether an excluded path lies strictly beneath dir.
func containsExcluded(dir string, excluded []string) bool {
	for _, x := range excluded {
		if x != dir && within(x, dir) {
			return true
		}
	}
	return false
}

// within reports whether p equals dir or lies beneath it.
func within(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// fsPath converts an absolute path to an fs.FS path rooted at "/".
func fsPath(p string) string {
	if p == "/" {
		return "."
	}
	return strings.TrimPrefix(p, "/")
}

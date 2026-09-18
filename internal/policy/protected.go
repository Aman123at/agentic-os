package policy

import (
	"path/filepath"
	"strings"
)

// systemPaths are Protected Paths outside home (PLAN.md §7.3). Agents cannot write
// most of them anyway; policy asks before trying.
var systemPaths = []string{
	"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/boot", "/var/lib",
	"/home/.aos-protected",
}

// dotfiles are the Protected entries in home; they are symlinks into /home/.aos-protected.
var dotfiles = []string{".ssh", ".gnupg", ".config", ".bashrc", ".profile", ".bash_logout"}

// envSamples are .env-style files that hold no secrets.
var envSamples = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true}

// DefaultPaths returns the built-in Protected Paths for a Machine whose home
// folder is home: the system folders and the protected dotfiles (PLAN.md §7.3).
// It is the one list every caller reads, so the Agent's policy, `aos protect`
// and Finder's lock badge can never disagree about what is protected.
func DefaultPaths(home string) []string {
	out := append([]string{}, systemPaths...)
	for _, name := range dotfiles {
		out = append(out, filepath.Join(home, name))
	}
	return out
}

// Within reports whether path is dir or inside it.
func Within(path, dir string) bool { return within(path, dir) }

// Protection is the set of Protected Paths (PLAN.md §7.3).
type Protection struct {
	rules []string

	// Resolve returns path with symlinks resolved, so a link into a Protected
	// Path is recognised. Nil means paths are taken as they are.
	Resolve func(path string) string
	// DirtyRepo returns the root of the git working tree containing path and
	// whether it has changes the current Task did not make. Nil means no repos.
	DirtyRepo func(path string) (root string, dirty bool)
}

// NewProtection returns the default Protected Paths for home plus the paths the user locked.
func NewProtection(home string, locked []string) *Protection {
	p := &Protection{rules: DefaultPaths(home)}
	for _, l := range locked {
		p.rules = append(p.rules, filepath.Clean(l))
	}
	return p
}

// Check reports the Protected Path that e changes, if any.
func (p *Protection) Check(e Effect) (string, bool) {
	paths := []string{staticPrefix(e.Path)}
	if p.Resolve != nil {
		if r := p.Resolve(e.Path); r != e.Path {
			paths = append(paths, staticPrefix(r))
		}
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		for _, r := range p.rules {
			// Deleting a folder deletes everything in it, so an ancestor counts too.
			if within(path, r) || (e.Op == Delete && within(r, path)) {
				return r, true
			}
		}
		if e.Op != Discard {
			if base := filepath.Base(path); (base == ".env" || strings.HasPrefix(base, ".env.")) && !envSamples[base] {
				return path, true
			}
		}
		if p.DirtyRepo != nil && (e.Op == Discard || e.Op == Delete) {
			if root, dirty := p.DirtyRepo(path); dirty && (e.Op == Discard || path == root) {
				return root + " (uncommitted changes)", true
			}
		}
	}
	return "", false
}

// staticPrefix returns path up to its first glob character, as a clean path.
func staticPrefix(path string) string {
	i := strings.IndexAny(path, "*?[")
	if i < 0 {
		return filepath.Clean(path)
	}
	dir := path[:i]
	if j := strings.LastIndex(dir, "/"); j >= 0 {
		dir = dir[:j+1]
	}
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	if dir == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}

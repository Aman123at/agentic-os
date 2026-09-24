package files

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// OpListTree lists every file under a folder, for Code's Quick Open (PLAN.md
// M8.7). Like the Workspace watch it runs as the Realm's user, skips what the
// watch skips (.git, node_modules, caches) and never follows a symlink; it
// also leaves out what the tree's .gitignore files ignore, and returns those
// rules so a caller can keep the list current from the watch's changes.
const OpListTree = "list_tree"

// MaxTreeFiles is how many files a tree listing holds by default.
const MaxTreeFiles = 100_000

type TreeArgs struct {
	Root string `json:"root"`
	// Max is how many files to list; 0 means MaxTreeFiles.
	Max int `json:"max,omitempty"`
}

// Tree is a folder's files, relative to it and slash-separated, sorted.
type Tree struct {
	Files   []string    `json:"files"`
	Ignores IgnoreRules `json:"ignores,omitempty"`
	// Truncated is set when there were more than Max files.
	Truncated bool `json:"truncated,omitempty"`
}

// ListTree walks root. Unreadable folders are left out, as they are for the
// user in a shell.
func (o Ops) ListTree(a TreeArgs) (Tree, error) {
	if a.Max <= 0 {
		a.Max = MaxTreeFiles
	}
	fi, err := os.Stat(a.Root)
	if err != nil {
		return Tree{}, err
	}
	if !fi.IsDir() {
		return Tree{}, fmt.Errorf("%s is not a folder", a.Root)
	}
	t := Tree{Ignores: IgnoreRules{}}
	ig := &Ignore{byDir: map[string][]ignorePattern{}}
	var walk func(rel string) bool
	walk = func(rel string) bool {
		dir := filepath.Join(a.Root, filepath.FromSlash(rel))
		des, err := os.ReadDir(dir)
		if err != nil {
			return true
		}
		if lines := readIgnoreFile(filepath.Join(dir, ".gitignore")); lines != nil {
			t.Ignores[rel] = lines
			for _, l := range lines {
				if p, ok := parseIgnoreLine(l); ok {
					ig.byDir[rel] = append(ig.byDir[rel], p)
				}
			}
		}
		for _, de := range des {
			name := de.Name()
			r := path.Join(rel, name)
			isDir := de.IsDir()
			if isDir && skipDirs[name] {
				continue
			}
			if ig.match(r, isDir) {
				continue
			}
			if isDir {
				if !walk(r) {
					return false
				}
				continue
			}
			// Regular files, and links to them: a link to a folder opens nothing.
			if !de.Type().IsRegular() {
				fi, err := os.Stat(filepath.Join(dir, name))
				if err != nil || !fi.Mode().IsRegular() {
					continue
				}
			}
			if len(t.Files) >= a.Max {
				t.Truncated = true
				return false
			}
			t.Files = append(t.Files, r)
		}
		return true
	}
	walk("")
	sort.Strings(t.Files)
	if len(t.Ignores) == 0 {
		t.Ignores = nil
	}
	return t, nil
}

// maxIgnoreFile bounds a .gitignore read; a real one is a few KiB.
const maxIgnoreFile = 256 << 10

func readIgnoreFile(p string) []string {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxIgnoreFile {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

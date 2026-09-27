package files

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeTree makes files under root; a path ending in "/" is a folder.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if rel[len(rel)-1] == '/' {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListTreeListsWhatQuickOpenOffers(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"main.go":                  "package main",
		".gitignore":               "*.log\n/out/\n",
		".github/workflows/ci.yml": "on: push",
		"app.log":                  "",
		"out/bin":                  "",
		"src/util.ts":              "",
		"src/.gitignore":           "gen/\n",
		"src/gen/types.ts":         "",
		"node_modules/x/index.js":  "",
		".git/HEAD":                "",
		"empty/":                   "",
	})
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "main.go"), filepath.Join(root, "main-link.go")); err != nil {
		t.Fatal(err)
	}
	tree, err := (Ops{}).ListTree(TreeArgs{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	// Hidden files and links to files are there; ignored, skipped and
	// linked-to folders are not.
	want := []string{".github/workflows/ci.yml", ".gitignore", "main-link.go", "main.go", "src/.gitignore", "src/util.ts"}
	if !slices.Equal(tree.Files, want) {
		t.Errorf("files = %q\nwant    %q", tree.Files, want)
	}
	if !slices.Equal(tree.Ignores[""], []string{"*.log", "/out/", ""}) || len(tree.Ignores["src"]) == 0 {
		t.Errorf("ignores = %q", tree.Ignores)
	}
	if tree.Truncated {
		t.Error("not truncated")
	}

	small, err := (Ops{}).ListTree(TreeArgs{Root: root, Max: 2})
	if err != nil || len(small.Files) != 2 || !small.Truncated {
		t.Errorf("Max 2: %q truncated=%v err=%v", small.Files, small.Truncated, err)
	}
	if _, err := (Ops{}).ListTree(TreeArgs{Root: filepath.Join(root, "main.go")}); err == nil {
		t.Error("a file is not a tree")
	}
}

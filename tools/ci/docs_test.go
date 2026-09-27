package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docsLinks names each link to a page the site does not have, and passes a
// page, a .mdx page, a folder's index, an anchor and an external link.
func TestDocsLinksFindsAMissingPage(t *testing.T) {
	t.Chdir(t.TempDir())
	pages := filepath.Join(docsDir, "src", "content", "docs")
	write := func(rel, body string) {
		p := filepath.Join(pages, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.md", "[Code](/use/code/) and [every command](/reference/).")
	write("reference/index.mdx", "[root](/reference/root/)")
	write("reference/root.mdx", "[home](/)")
	write("use/code.md", "[Root Mode](/use/root-mode/#turning-it-on), [switch](/use/code/#the-switch), "+
		"[GitHub](https://github.com/Aman123at/agentic-os), [below](#below)")

	err := docsLinks()
	if err == nil {
		t.Fatal("a link to a missing page passed")
	}
	if !strings.Contains(err.Error(), "/use/root-mode/#turning-it-on") || strings.Count(err.Error(), "\n") != 1 {
		t.Fatalf("want only the root-mode link named, got: %v", err)
	}

	write("use/root-mode.md", "")
	if err := docsLinks(); err != nil {
		t.Fatal(err)
	}
}

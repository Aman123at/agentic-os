package files

import (
	"context"
	"errors"
	"testing"
)

// ctags finds the symbols Code navigates by (PLAN.md M8.8).
func TestTagsListsSymbolsWithTheirScopes(t *testing.T) {
	if _, err := findCtags(context.Background()); err != nil {
		t.Skip("universal-ctags is not installed")
	}
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"main.go":    "package main\n\ntype Server struct{}\n\n// Run runs.\nfunc (s *Server) Run(ctx int) error {\n\treturn nil\n}\n",
		"web/a.ts":   "export function makeStore(): number { return 1 }\n",
		"lib/b.py":   "class A:\n    def run(self):\n        pass\n",
		"notes.txt":  "Run\n",
		".ctags.d/x": "--exclude=*.go\n", // not read: --options=NONE
	})
	tags, err := (Ops{}).Tags(context.Background(), TagsArgs{Root: root, Files: []string{"main.go", "web/a.ts", "lib/b.py", "notes.txt", "missing.go"}})
	if err != nil {
		t.Fatal(err)
	}
	find := func(name string) *Tag {
		for i := range tags {
			if tags[i].Name == name {
				return &tags[i]
			}
		}
		return nil
	}
	run := find("Run")
	if run == nil || run.Path != "main.go" || run.Line != 6 || run.End != 8 || run.Kind != "func" || run.Scope != "main.Server" || run.Signature != "(ctx int)" || run.Language != "Go" {
		t.Errorf("Run = %+v", run)
	}
	if s := find("makeStore"); s == nil || s.Path != "web/a.ts" || s.Language != "TypeScript" || s.Kind != "function" {
		t.Errorf("makeStore = %+v", s)
	}
	if r := find("run"); r == nil || r.Scope != "A" || r.Path != "lib/b.py" {
		t.Errorf("run = %+v", r)
	}
}

func TestTagsWithoutUniversalCtagsSaysSo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (Ops{}).Tags(context.Background(), TagsArgs{Root: t.TempDir(), Files: []string{"a.go"}})
	if !errors.Is(err, ErrNoCtags) {
		t.Errorf("err = %v, want ErrNoCtags", err)
	}
	if errorKind(err) != "no_ctags" || !errors.Is(kindError("no_ctags", err.Error()), ErrNoCtags) {
		t.Error("ErrNoCtags does not survive the worker protocol")
	}
}

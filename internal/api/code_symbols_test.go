package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/files"
)

func haveCtags(t *testing.T) {
	t.Helper()
	if _, err := (files.Ops{}).Tags(context.Background(), files.TagsArgs{Root: t.TempDir()}); err != nil {
		t.Skip("universal-ctags is not installed")
	}
}

func TestNavigationFindsDefinitionsAndSymbols(t *testing.T) {
	haveCtags(t)
	h := newCodeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root, id := openProject(t, h, ctx)
	writeFiles(t, root, map[string]string{"run.go": "package main\n\n// Run runs.\nfunc Run() {}\n"})
	mainGo := filepath.Join(root, "main.go")

	def, err := h.code.Definition(ctx, connect.NewRequest(&aosv1.DefinitionRequest{WorkspaceId: id, Name: "Run", Path: mainGo}))
	if err != nil {
		t.Fatal(err)
	}
	if s := def.Msg.Symbols; len(s) != 1 || s[0].Path != filepath.Join(root, "run.go") || s[0].Line != 4 || s[0].Kind != "func" {
		t.Errorf("Run's definition: %+v", s)
	}
	if st := def.Msg.Index; st == nil || st.Files == 0 || st.Partial {
		t.Errorf("index state %+v", st)
	}

	doc, err := h.code.DocumentSymbols(ctx, connect.NewRequest(&aosv1.DocumentSymbolsRequest{WorkspaceId: id, Path: mainGo}))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range doc.Msg.Symbols {
		names = append(names, s.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "main") {
		t.Errorf("main.go's symbols: %q", names)
	}

	all, err := h.code.WorkspaceSymbols(ctx, connect.NewRequest(&aosv1.WorkspaceSymbolsRequest{WorkspaceId: id, Query: "run"}))
	if err != nil || len(all.Msg.Symbols) == 0 || all.Msg.Symbols[0].Name != "Run" {
		t.Errorf("⌘T run: %+v %v", all.Msg.GetSymbols(), err)
	}

	// An edit on disk is in the next answer.
	writeFiles(t, root, map[string]string{"run.go": "package main\n\n\n\nfunc Run() {}\n"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		def, err := h.code.Definition(ctx, connect.NewRequest(&aosv1.DefinitionRequest{WorkspaceId: id, Name: "Run", Path: mainGo}))
		if err == nil && len(def.Msg.Symbols) == 1 && def.Msg.Symbols[0].Line == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the edit never reached the index: %+v %v", def.Msg.GetSymbols(), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestReferencesAreWholeWordsInTheSameLanguage(t *testing.T) {
	h := newCodeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root, id := openProject(t, h, ctx)
	writeFiles(t, root, map[string]string{
		"run.go":      "package main\n\nfunc Run() {}\nfunc Runner() {}\n",
		"web/run.ts":  "Run()\n",
		"docs/run.md": "Run it\n",
	})
	resp, err := h.code.References(ctx, connect.NewRequest(&aosv1.ReferencesRequest{WorkspaceId: id, Name: "Run", Path: filepath.Join(root, "main.go")}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range resp.Msg.Files {
		for _, m := range f.Matches {
			rel, _ := filepath.Rel(root, f.Path)
			got = append(got, rel+":"+m.Text)
		}
	}
	want := "main.go:func main() { Run() },run.go:func Run() {}"
	if strings.Join(got, ",") != want {
		t.Errorf("references %q, want %q (Go files only, not Runner)", got, want)
	}
}

func TestNavigationWithoutCtagsSaysToInstallIt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := newCodeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root, id := openProject(t, h, ctx)
	_, err := h.code.Definition(ctx, connect.NewRequest(&aosv1.DefinitionRequest{WorkspaceId: id, Name: "Run", Path: filepath.Join(root, "main.go")}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "universal-ctags") {
		t.Errorf("err = %v, want FailedPrecondition naming universal-ctags", err)
	}
}

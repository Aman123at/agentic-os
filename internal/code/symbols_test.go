package code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/files"
)

// fakeTags tags a file "a/b.go" with one symbol per word of its content
// here, and records what it was asked to tag.
type fakeTags struct {
	content map[string]string
	asked   [][]string
}

func (f *fakeTags) tag(_ context.Context, rel []string) ([]files.Tag, error) {
	f.asked = append(f.asked, slices.Clone(rel))
	var out []files.Tag
	for _, p := range rel {
		for i, w := range strings.Fields(f.content[p]) {
			out = append(out, files.Tag{Name: w, Path: p, Line: i + 1, Kind: "func", Language: "Go"})
		}
	}
	return out, nil
}

func (f *fakeTags) list(context.Context) ([]string, error) {
	var out []string
	for p := range f.content {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func TestTheIndexRetagsOnlyWhatChanged(t *testing.T) {
	ft := &fakeTags{content: map[string]string{"a.go": "Run Stop", "pkg/b.go": "Run", "pkg/c.go": "Helper"}}
	x := &Index{Root: "/w"}
	ctx := context.Background()
	st, err := x.Ensure(ctx, ft.list, ft.tag, "")
	if err != nil || st.Files != 3 || st.Total != 3 || st.Partial {
		t.Fatalf("first build: %+v %v", st, err)
	}
	if got := len(x.Definitions("Run", "a.go")); got != 2 {
		t.Errorf("Run is defined twice, got %d", got)
	}

	// A change re-tags that file alone; a deletion forgets it at once.
	ft.content["pkg/b.go"] = "Start"
	ft.content["pkg/new.go"] = "Fresh"
	delete(ft.content, "pkg/c.go")
	x.Apply([]files.Change{
		{Kind: files.Changed, Path: "/w/pkg/b.go"},
		{Kind: files.Created, Path: "/w/pkg/new.go"},
		{Kind: files.Created, Path: "/w/out/ignored.go"}, // not in the list
		{Kind: files.Deleted, Path: "/w/pkg/c.go"},
	})
	if len(x.Definitions("Helper", "a.go")) != 0 {
		t.Error("a deleted file's symbols are gone at once")
	}
	ft.asked = nil
	if _, err := x.Ensure(ctx, ft.list, ft.tag, ""); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"pkg/b.go", "pkg/new.go"}}; !reflect.DeepEqual(sorted(ft.asked), want) {
		t.Errorf("re-tagged %q, want %q", ft.asked, want)
	}
	if len(x.Definitions("Run", "a.go")) != 1 || len(x.Definitions("Start", "a.go")) != 1 || len(x.Definitions("Fresh", "a.go")) != 1 {
		t.Error("the changed files' symbols are not current")
	}

	// Nothing changed: nothing is tagged.
	ft.asked = nil
	if _, err := x.Ensure(ctx, ft.list, ft.tag, ""); err != nil || len(ft.asked) != 0 {
		t.Errorf("an unchanged index tagged %q (%v)", ft.asked, err)
	}

	// A rescan builds it again.
	x.Apply([]files.Change{{Kind: files.Rescan, Path: "/w", Dir: true}})
	ft.asked = nil
	if _, err := x.Ensure(ctx, ft.list, ft.tag, ""); err != nil || len(ft.asked) != 1 || len(ft.asked[0]) != 3 {
		t.Errorf("after a rescan tagged %q (%v)", ft.asked, err)
	}
}

func sorted(batches [][]string) [][]string {
	for _, b := range batches {
		sort.Strings(b)
	}
	return batches
}

func TestABigWorkspaceIndexesOnlyTheFoldersNavigatedFrom(t *testing.T) {
	ft := &fakeTags{content: map[string]string{}}
	for i := range MaxIndexFiles + 1 {
		ft.content[fmt.Sprintf("d%d/f%d.go", i%100, i)] = fmt.Sprintf("S%d", i)
	}
	x := &Index{Root: "/w"}
	st, err := x.Ensure(context.Background(), ft.list, ft.tag, "d7/f7.go")
	if err != nil || !st.Partial || st.Total != MaxIndexFiles+1 {
		t.Fatalf("%+v %v", st, err)
	}
	if st.Files != (MaxIndexFiles+1)/100 {
		t.Errorf("indexed %d files, want only d7's %d", st.Files, (MaxIndexFiles+1)/100)
	}
	if len(x.Definitions("S7", "d7/f7.go")) != 1 || len(x.Definitions("S8", "d7/f7.go")) != 0 {
		t.Error("only d7 should be indexed")
	}
	// Asking from another folder adds it.
	st, _ = x.Ensure(context.Background(), ft.list, ft.tag, "d8/f8.go")
	if len(x.Definitions("S8", "d8/f8.go")) != 1 || st.Files != 2*((MaxIndexFiles+1)/100) {
		t.Errorf("d8 was not added: %+v", st)
	}
}

func TestDefinitionsRankNearestFirst(t *testing.T) {
	tag := func(path, lang, kind string) files.Tag {
		return files.Tag{Name: "Run", Path: path, Line: 1, Kind: kind, Language: lang}
	}
	all := []files.Tag{
		tag("z/far/away.go", "Go", "func"),
		tag("web/run.ts", "TypeScript", "function"),
		tag("cmd/aos/main.go", "Go", "func"),
		tag("cmd/run.go", "Go", "func"),
		tag("cmd/aos/run.go", "Go", "package"), // names, doesn't define
		tag("cmd/aos/here.go", "Go", "method"),
	}
	got := rankDefinitions(slices.Clone(all), "cmd/aos/here.go", "Go")
	var paths []string
	for _, t := range got {
		paths = append(paths, t.Path)
	}
	want := []string{"cmd/aos/here.go", "cmd/aos/main.go", "cmd/run.go", "z/far/away.go", "cmd/aos/run.go"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("ranked %q\nwant   %q", paths, want)
	}
	// With nothing in the asking file's language, every language is offered.
	if got := rankDefinitions(slices.Clone(all), "README.md", "Markdown"); len(got) != len(all) {
		t.Errorf("%d offered", len(got))
	}
}

func TestLanguageGlobsCoverALanguagesExtensions(t *testing.T) {
	if g := LanguageGlobs("src/app.tsx"); !slices.Contains(g, "*.ts") || !slices.Contains(g, "*.js") {
		t.Errorf("tsx: %q", g)
	}
	if g := LanguageGlobs("main.go"); !reflect.DeepEqual(g, []string{"*.go"}) {
		t.Errorf("go: %q", g)
	}
	if g := LanguageGlobs("x.zig"); !reflect.DeepEqual(g, []string{"*.zig"}) {
		t.Errorf("zig: %q", g)
	}
	if g := LanguageGlobs("Makefile"); g != nil {
		t.Errorf("no extension: %q", g)
	}
}

// TestTheIndexOnARealProject runs universal-ctags over Go, TypeScript and
// Python, then follows an edit.
func TestTheIndexOnARealProject(t *testing.T) {
	ops := files.Ops{}
	if _, err := ops.Tags(context.Background(), files.TagsArgs{Root: t.TempDir()}); err != nil {
		t.Skip("universal-ctags is not installed")
	}
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go/server.go", "package api\n\ntype Server struct{}\n\nfunc (s *Server) Serve() {}\n")
	write("go/main.go", "package api\n\nfunc main() { (&Server{}).Serve() }\n")
	write("web/store.ts", "export class Store {\n  get(): number { return 1 }\n}\n")
	write("py/tool.py", "def helper(x):\n    return x\n")
	list := func(ctx context.Context) ([]string, error) {
		t, err := ops.ListTree(files.TreeArgs{Root: root})
		return t.Files, err
	}
	tag := func(ctx context.Context, rel []string) ([]files.Tag, error) {
		return ops.Tags(ctx, files.TagsArgs{Root: root, Files: rel})
	}
	x := &Index{Root: root}
	if _, err := x.Ensure(context.Background(), list, tag, ""); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{"Serve": "go/server.go", "Server": "go/server.go", "Store": "web/store.ts", "helper": "py/tool.py"} {
		if d := x.Definitions(name, "go/main.go"); len(d) == 0 || d[0].Path != file {
			t.Errorf("%s: %+v, want it in %s", name, d, file)
		}
	}
	syms := x.FileSymbols("go/server.go")
	if len(syms) < 2 || syms[0].Line > syms[len(syms)-1].Line {
		t.Errorf("server.go's symbols: %+v", syms)
	}
	if s := x.Search("hlpr", 10); len(s) != 1 || s[0].Name != "helper" {
		t.Errorf("⌘T hlpr: %+v", s)
	}

	write("py/tool.py", "def helper(x):\n    return x\n\ndef renamed():\n    pass\n")
	x.Apply([]files.Change{{Kind: files.Changed, Path: filepath.Join(root, "py/tool.py")}})
	if _, err := x.Ensure(context.Background(), list, tag, ""); err != nil {
		t.Fatal(err)
	}
	if d := x.Definitions("renamed", "py/tool.py"); len(d) != 1 || d[0].Line != 4 {
		t.Errorf("after an edit: %+v", d)
	}
}

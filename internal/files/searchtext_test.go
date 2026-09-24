package files

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// searchFixture is a small project: ignored, skipped, hidden and binary files
// beside ordinary ones.
func searchFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"main.go":                   "package main\n\nfunc main() {\n\tRun()\n}\n",
		"run.go":                    "package main\n\n// Run runs.\nfunc Run() { run2() }\nfunc run2() {}\n",
		"README.md":                 "# Run it\n\nrun: go run .\r\n",
		".gitignore":                "*.log\nbuild/\n",
		"app.log":                   "Run failed\n",
		"build/out.txt":             "Run\n",
		".env.example":              "RUN_MODE=dev\n",
		"node_modules/pkg/index.js": "Run()\n",
		".git/config":               "[core] Run\n",
		"web/.gitignore":            "*.min.js\n",
		"web/app.js":                "const run = () => 'Run ü Run';\n",
		"web/app.min.js":            "Run\n",
		"web/deep/x.ts":             "  \t  export function Run() {}\n",
		"bin/tool":                  "Run\x00binary\n",
		"unicode.txt":               "héllo 😀 Run 😀 run\n",
	})
	return root
}

func search(t *testing.T, engine string, a TextSearchArgs) ([]TextFile, TextSearchResult) {
	t.Helper()
	a.Engine = engine
	var got []TextFile
	res, err := (Ops{}).SearchText(context.Background(), a, func(f TextFile) error {
		got = append(got, f)
		return nil
	})
	if err != nil {
		t.Fatalf("%s search %+v: %v", engine, a, err)
	}
	return got, res
}

func rel(root string, fs []TextFile) []string {
	var out []string
	for _, f := range fs {
		r, _ := filepath.Rel(root, f.Path)
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func TestTheBuiltInSearchWalksAsCodeDoes(t *testing.T) {
	root := searchFixture(t)
	got, res := search(t, EngineBuiltin, TextSearchArgs{Root: root, Query: "run"})
	// Hidden files, but no ignored, skipped or binary ones; sorted by path.
	want := []string{".env.example", "README.md", "main.go", "run.go", "unicode.txt", "web/app.js", "web/deep/x.ts"}
	if files := rel(root, got); !reflect.DeepEqual(files, want) {
		t.Errorf("files = %q\nwant    %q", files, want)
	}
	if res.Engine != EngineBuiltin || res.Files != len(want) || res.Truncated {
		t.Errorf("result = %+v", res)
	}

	// Case, a regular expression, and globs.
	got, _ = search(t, EngineBuiltin, TextSearchArgs{Root: root, Query: `func(tion)? [A-Z]\w*\(`, Regex: true, Case: true})
	if files := rel(root, got); !reflect.DeepEqual(files, []string{"run.go", "web/deep/x.ts"}) {
		t.Errorf("regex, case-sensitive: %q", files)
	}
	got, _ = search(t, EngineBuiltin, TextSearchArgs{Root: root, Query: "run", Include: []string{"*.go", "web/**"}, Exclude: []string{"deep"}})
	if files := rel(root, got); !reflect.DeepEqual(files, []string{"main.go", "run.go", "web/app.js"}) {
		t.Errorf("globs: %q", files)
	}

	// The limit counts lines, and says it cut the search short.
	got, res = search(t, EngineBuiltin, TextSearchArgs{Root: root, Query: "run", Max: 3})
	if n := res.Matches; n != 3 || !res.Truncated || len(got) == 0 {
		t.Errorf("Max 3: %d lines, truncated=%v", n, res.Truncated)
	}
}

func TestAMatchIsTrimmedAndCountedInUTF16(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		ranges     [][2]int // bytes
		text       string
		want       [][2]int // UTF-16 units
	}{
		{"plain", "func Run() {}\n", [][2]int{{5, 8}}, "func Run() {}", [][2]int{{5, 8}}},
		{"leading space goes", "\t  Run()\r\n", [][2]int{{3, 6}}, "Run()", [][2]int{{0, 3}}},
		// é is 2 bytes and 1 unit; 😀 is 4 bytes and 2 units.
		{"wide runes", "héllo 😀 Run", [][2]int{{12, 15}}, "héllo 😀 Run", [][2]int{{9, 12}}},
		{"empty matches are none", "abc", [][2]int{{1, 1}}, "", nil},
	} {
		m, ok := matchLine(7, tc.line, tc.ranges)
		if tc.want == nil {
			if ok {
				t.Errorf("%s: a match %+v", tc.name, m)
			}
			continue
		}
		if !ok || m.Text != tc.text || !reflect.DeepEqual(m.Ranges, tc.want) || m.Line != 7 {
			t.Errorf("%s: %+v, want %q %v", tc.name, m, tc.text, tc.want)
		}
	}

	// A long line is cut around its first match, with … on each cut side.
	long := strings.Repeat("x", 1000) + "Run" + strings.Repeat("y", 1000)
	m, _ := matchLine(1, long, [][2]int{{1000, 1003}})
	s, e := m.Ranges[0][0], m.Ranges[0][1]
	if len([]rune(m.Text)) > maxMatchText+2 || !strings.HasPrefix(m.Text, "…") || !strings.HasSuffix(m.Text, "…") || string([]rune(m.Text)[s:e]) != "Run" {
		t.Errorf("cut line: %q %v", m.Text, m.Ranges)
	}
}

func TestASearchThatCannotRunSaysWhy(t *testing.T) {
	root := t.TempDir()
	for _, q := range []TextSearchArgs{{Root: root, Query: ""}, {Root: root, Query: "(unclosed", Regex: true}} {
		_, err := (Ops{}).SearchText(context.Background(), q, func(TextFile) error { return nil })
		if !errors.Is(err, ErrBadQuery) {
			t.Errorf("%+v: %v, want ErrBadQuery", q, err)
		}
	}
	// A literal search for the same text is fine.
	if _, err := (Ops{}).SearchText(context.Background(), TextSearchArgs{Root: root, Query: "(unclosed"}, func(TextFile) error { return nil }); err != nil {
		t.Error(err)
	}
}

// TestRipgrepAndTheBuiltInSearchAgree is the promise that a Machine without
// ripgrep searches the same: same files, lines, text and ranges.
func TestRipgrepAndTheBuiltInSearchAgree(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	root := searchFixture(t)
	for _, a := range []TextSearchArgs{
		{Query: "run"},
		{Query: "Run", Case: true},
		{Query: "RUN_MODE"},
		{Query: `func(tion)? [A-Z]\w*\(`, Regex: true, Case: true},
		{Query: `r.n`, Regex: true},
		{Query: "run", Include: []string{"*.go", "web/**"}, Exclude: []string{"deep"}},
		{Query: "run", Exclude: []string{"*.md", ".env*"}},
		{Query: "run", Include: []string{"web"}},
		{Query: "😀"},
		{Query: "run", Max: 4},
	} {
		a.Root = root
		rg, rgRes := search(t, EngineRipgrep, a)
		bi, biRes := search(t, EngineBuiltin, a)
		if !reflect.DeepEqual(rg, bi) {
			t.Errorf("%+v:\nripgrep  %+v\nbuilt-in %+v", a, rg, bi)
		}
		rgRes.Engine, biRes.Engine = "", ""
		if rgRes != biRes {
			t.Errorf("%+v: ripgrep %+v, built-in %+v", a, rgRes, biRes)
		}
	}
}

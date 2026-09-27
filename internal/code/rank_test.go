package code

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// Quick Open's ranking (PLAN.md M8.7): what a person typing expects first.
func TestRankPutsTheExpectedFileFirst(t *testing.T) {
	paths := []string{
		"internal/api/server.go",
		"internal/api/server_test.go",
		"internal/serve/rgo.go",
		"internal/policy/sensitive.go",
		"internal/code/sensitive_test.go",
		"src/util.ts",
		"src/utilities/helper.ts",
		"build/outputs/till.ts",
		"desktop/src/apps/code/Workspace.tsx",
		"desktop/src/api/workspaces.ts",
		"docs/PLAN.md",
		"README.md",
	}
	for _, tc := range []struct {
		query string
		want  []string // the top results, in order
	}{
		{"server.go", []string{"internal/api/server.go"}},                              // exact name
		{"server", []string{"internal/api/server.go", "internal/api/server_test.go"}},  // shorter first
		{"util", []string{"src/util.ts", "src/utilities/helper.ts"}},                   // in the name beats the folder
		{"sensgo", []string{"internal/policy/sensitive.go"}},                           // fewer gaps
		{"readme", []string{"README.md"}},                                              // ignores case
		{"api/ser", []string{"internal/api/server.go", "internal/api/server_test.go"}}, // a slash matches the path
		{"code/work space", []string{"desktop/src/apps/code/Workspace.tsx"}},           // spaces are ignored
		{"plan", []string{"docs/PLAN.md"}},
	} {
		got := Rank(tc.query, paths, 50)
		if len(got) < len(tc.want) {
			t.Errorf("%q: %v", tc.query, got)
			continue
		}
		for i, w := range tc.want {
			if got[i].Rel != w {
				t.Errorf("%q: #%d is %q, want %q (all: %v)", tc.query, i+1, got[i].Rel, w, rels(got))
				break
			}
		}
	}
	if got := Rank("zzz", paths, 50); len(got) != 0 {
		t.Errorf("no path has zzz: %v", rels(got))
	}
	if got := Rank("", paths, 3); !reflect.DeepEqual(rels(got), paths[:3]) {
		t.Errorf("an empty query gives the first paths: %v", rels(got))
	}
	if got := Rank("s", paths, 2); len(got) != 2 {
		t.Errorf("limit: %d", len(got))
	}
}

func TestRankSaysWhichCharactersMatched(t *testing.T) {
	for _, tc := range []struct {
		query, path string
		want        []int
	}{
		{"util", "src/util.ts", []int{4, 5, 6, 7}},
		{"sts", "src/test_setup.ts", []int{9, 15, 16}}, // in the name: _setup, then .ts
		{"api/s", "internal/api/server.go", []int{9, 10, 11, 12, 13}},
		{"ü", "dïr/über.txt", []int{4}}, // UTF-16 offsets
		{"x", "😀/x.go", []int{3}},       // the emoji is two units
	} {
		got := Rank(tc.query, []string{tc.path}, 1)
		if len(got) != 1 || !reflect.DeepEqual(got[0].Positions, tc.want) {
			t.Errorf("%q in %q: %+v, want positions %v", tc.query, tc.path, got, tc.want)
		}
	}
}

// The M8.14 target is < 100 ms p95 for ⌘P on 10 000 files; this keeps a
// wide margin on a slow CI machine.
func TestRankIsQuickOnTenThousandFiles(t *testing.T) {
	paths := make([]string, 10_000)
	for i := range paths {
		paths[i] = fmt.Sprintf("pkg%03d/sub%02d/module_%d/file_name_%d.go", i%97, i%13, i%7, i)
	}
	start := time.Now()
	for _, q := range []string{"f", "mod", "pkg12sub", "file_name_9999"} {
		Rank(q, paths, 50)
	}
	if d := time.Since(start) / 4; d > 250*time.Millisecond {
		t.Errorf("a query took %v on 10 000 files", d)
	}
}

func rels(fs []Found) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rel
	}
	return out
}

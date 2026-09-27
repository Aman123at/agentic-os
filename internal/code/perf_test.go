package code

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Aman123at/agentic-os/internal/files"
)

// The speed targets for Code (PLAN.md §16, M8.14), measured on a generated
// Workspace of 10 000 files: Quick Open answers within 100 ms p95, and Go to
// Definition within 150 ms p95 once the index is built. These time aosd's
// side of each question; code-perf.spec.ts times the Desktop's.

const (
	perfFiles     = 10_000
	quickOpenP95  = 100 * time.Millisecond
	definitionP95 = 150 * time.Millisecond
)

var perfWords = strings.Fields("app api auth button cache client config core data editor event file form handler http index input layout list loader " +
	"logger main menu model modal node page panel parser query router schema search server service session settings store table task theme " +
	"token tree user util view widget window worker")

// perfWorkspace is a monorepo-shaped tree: packages of source folders, a mix
// of languages, names built from common words so queries match many files.
func perfWorkspace() []string {
	exts := []string{".ts", ".tsx", ".go", ".py", ".css", ".json", ".md"}
	var out []string
	for i := 0; len(out) < perfFiles; i++ {
		w := func(k int) string { return perfWords[(i*7+k*13)%len(perfWords)] }
		pkg := fmt.Sprintf("packages/%s-%d", w(1), i%40)
		dir := fmt.Sprintf("%s/src/%s/%s", pkg, w(2), w(3))
		name := fmt.Sprintf("%s%s%d%s", w(4), strings.ToUpper(w(5)[:1])+w(5)[1:], i%97, exts[i%len(exts)])
		out = append(out, dir+"/"+name)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// timed skips a speed test under the race detector.
func timed(t *testing.T) {
	t.Helper()
	if raceEnabled {
		t.Skip("speed is not measured under the race detector")
	}
}

// p95 runs f n times and returns the 95th percentile of its durations.
func p95(n int, f func(i int)) time.Duration {
	took := make([]time.Duration, n)
	for i := range n {
		start := time.Now()
		f(i)
		took[i] = time.Since(start)
	}
	slices.Sort(took)
	return took[n*95/100]
}

func TestQuickOpenIsFastOnALargeWorkspace(t *testing.T) {
	timed(t)
	paths := perfWorkspace()
	if len(paths) < perfFiles*9/10 {
		t.Fatalf("generated %d files", len(paths))
	}
	list := &FileList{Root: "/w"}
	load := func(context.Context) (files.Tree, error) { return files.Tree{Files: paths}, nil }
	ctx := context.Background()
	if _, _, err := list.Paths(ctx, load); err != nil {
		t.Fatal(err)
	}

	// Each query as it is typed, a key at a time, as the palette asks.
	var queries []string
	for _, q := range []string{"button", "srcformmodal", "authserv.go", "packages/user-3/", "tsx", "zzqx", "ModalPanel", "index.md"} {
		for i := 1; i <= len(q); i++ {
			queries = append(queries, q[:i])
		}
	}
	queries = append(queries, "")
	took := p95(len(queries)*3, func(i int) {
		got, _, err := list.Paths(ctx, load)
		if err != nil {
			t.Fatal(err)
		}
		Rank(queries[i%len(queries)], got, 50)
	})
	t.Logf("Quick Open p95 over %d files: %v (target < %v)", len(paths), took, quickOpenP95)
	if took >= quickOpenP95 {
		t.Errorf("Quick Open p95 %v, want < %v", took, quickOpenP95)
	}
}

func TestGoToDefinitionIsFastOnceIndexed(t *testing.T) {
	timed(t)
	paths := perfWorkspace()
	// Twenty symbols a file: some its own, some shared by many files (New,
	// String, init …), as in a real project.
	shared := []string{"New", "String", "init", "Close", "Handle", "render", "props", "Error"}
	tag := func(_ context.Context, rel []string) ([]files.Tag, error) {
		var out []files.Tag
		for _, p := range rel {
			base := strings.TrimSuffix(p[strings.LastIndexByte(p, '/')+1:], ".go")
			for k := range 20 {
				name := fmt.Sprintf("%s_%d", base, k)
				if k < len(shared) {
					name = shared[k]
				}
				out = append(out, files.Tag{Name: name, Path: p, Line: k*10 + 1, Kind: "func", Language: "Go"})
			}
		}
		return out, nil
	}
	list := func(context.Context) ([]string, error) { return paths, nil }
	x := &Index{Root: "/w"}
	ctx := context.Background()
	if st, err := x.Ensure(ctx, list, tag, ""); err != nil || st.Partial || st.Files != len(paths) {
		t.Fatalf("build: %+v %v", st, err)
	}

	// F12 on shared and unique names from files across the tree, with a save
	// between some of them: the saved file is re-tagged at the next question.
	took := p95(200, func(i int) {
		from := paths[(i*397)%len(paths)]
		if i%5 == 4 {
			x.Apply([]files.Change{{Kind: files.Changed, Path: "/w/" + from}})
		}
		name := shared[i%len(shared)]
		if i%2 == 1 {
			name = strings.TrimSuffix(from[strings.LastIndexByte(from, '/')+1:], ".go") + "_12"
		}
		if _, err := x.Ensure(ctx, list, tag, from); err != nil {
			t.Fatal(err)
		}
		if len(x.Definitions(name, from)) == 0 {
			t.Fatalf("no definition of %s", name)
		}
	})
	t.Logf("Go to Definition p95 over %d files: %v (target < %v)", len(paths), took, definitionP95)
	if took >= definitionP95 {
		t.Errorf("Go to Definition p95 %v, want < %v", took, definitionP95)
	}
}

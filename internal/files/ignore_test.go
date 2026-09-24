package files

import "testing"

// Code honours a Workspace's .gitignore files as git does (PLAN.md M8.7).
func TestIgnoreFollowsGitsRules(t *testing.T) {
	ig := NewIgnore(IgnoreRules{
		"": {
			"# a comment", "", "*.log", "!keep.log", "/build", "dist/", "docs/*.pdf", "**/tmp/**",
			"secret?.txt", "[Bb]in", `\#hash`, "trailing   ",
		},
		"web": {"*.map", "!app.log"},
	})
	for _, tc := range []struct {
		rel  string
		dir  bool
		want bool
	}{
		{"a.log", false, true},
		{"deep/down/a.log", false, true},
		{"keep.log", false, false},     // negated later
		{"web/app.log", false, false},  // a deeper .gitignore re-includes it
		{"build", true, true},          // anchored to the root
		{"src/build", true, false},     // … so not deeper down
		{"build/out.o", false, true},   // under an ignored folder
		{"dist", true, true},           // folders only
		{"dist", false, false},         // … not a file called dist
		{"pkg/dist/x.js", false, true}, // a folder-only name matches at any depth
		{"docs/a.pdf", false, true},    // * stays in one folder
		{"docs/sub/a.pdf", false, false},
		{"x/tmp/y/z.txt", false, true}, // ** crosses folders
		{"secret1.txt", false, true},   // ?
		{"secret12.txt", false, false},
		{"bin", true, true}, // a class
		{"Bin", false, true},
		{"#hash", false, true},        // an escaped #
		{"trailing", false, true},     // trailing spaces are dropped
		{"web/a.js.map", false, true}, // the deeper file's own rules
		{"a.js.map", false, false},    // … apply only under it
		{"main.go", false, false},
	} {
		if got := ig.Ignored(tc.rel, tc.dir); got != tc.want {
			t.Errorf("Ignored(%q, dir=%v) = %v, want %v", tc.rel, tc.dir, got, tc.want)
		}
	}
	if NewIgnore(nil).Ignored("anything", false) {
		t.Error("no rules ignore nothing")
	}
}

func TestGlobsMatchLikeGitignorePatterns(t *testing.T) {
	g := NewGlob([]string{"*.go", "src/**", " ", "!negated"})
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{"main.go", true},
		{"a/b/c.go", true},
		{"src/x/y.ts", true},
		{"lib/src/y.ts", false},
		{"README.md", false},
		{"negated", false},
	} {
		if got := g.MatchSelf(tc.rel, false); got != tc.want {
			t.Errorf("MatchSelf(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
	if !NewGlob(nil).Empty() || NewGlob([]string{"vendor"}).Empty() {
		t.Error("Empty is wrong")
	}
	if !NewGlob([]string{"vendor"}).Match("vendor/a/b.go", false) {
		t.Error("a folder's glob matches what is under it")
	}
}

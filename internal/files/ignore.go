package files

import (
	"path"
	"regexp"
	"strings"
)

// IgnoreRules are a tree's .gitignore files: each one's lines, by the folder
// that holds it, relative to the tree's root ("" is the root itself). Code's
// Quick Open and Find in Files honour them (PLAN.md M8.7).
type IgnoreRules map[string][]string

// Ignore answers whether a path under a tree is ignored by its .gitignore
// files, as git does: a later line overrides an earlier one, a deeper file
// overrides a shallower one, and nothing under an ignored folder comes back.
// The .gitignore above the root, the global one and .git/info/exclude are not
// read.
type Ignore struct {
	byDir map[string][]ignorePattern
}

type ignorePattern struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
	// base patterns (no slash) match a name at any depth; the others match
	// the path relative to the .gitignore's folder.
	base bool
}

// NewIgnore compiles rules. Lines that are not valid patterns are skipped.
func NewIgnore(rules IgnoreRules) *Ignore {
	ig := &Ignore{byDir: make(map[string][]ignorePattern, len(rules))}
	for dir, lines := range rules {
		for _, line := range lines {
			if p, ok := parseIgnoreLine(line); ok {
				ig.byDir[dir] = append(ig.byDir[dir], p)
			}
		}
	}
	return ig
}

// Empty reports whether no rule was read.
func (ig *Ignore) Empty() bool { return ig == nil || len(ig.byDir) == 0 }

func parseIgnoreLine(line string) (ignorePattern, bool) {
	line = strings.TrimSuffix(line, "\r")
	// Trailing spaces are ignored unless escaped.
	for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
		line = line[:len(line)-1]
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return ignorePattern{}, false
	}
	var p ignorePattern
	if strings.HasPrefix(line, "!") {
		p.negate, line = true, line[1:]
	} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		p.dirOnly, line = true, strings.TrimRight(line, "/")
	}
	if line == "" {
		return ignorePattern{}, false
	}
	p.base = !strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	re, err := regexp.Compile("^" + GlobRegexp(line) + "$")
	if err != nil {
		return ignorePattern{}, false
	}
	p.re = re
	return p, true
}

// GlobRegexp turns a gitignore-style glob into a regular expression: * and ?
// stay within a path segment, ** crosses them, and [...] is a class.
func GlobRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			// Leading or inner: any number of folders, none included.
			b.WriteString(`(?:.*/)?`)
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(`.*`)
			i++
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		case c == '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case c == '\\' && i+1 < len(glob):
			i++
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// Ignored reports whether rel, a slash-separated path relative to the root,
// is ignored. dir says whether it is a folder. A path under an ignored folder
// is ignored too.
func (ig *Ignore) Ignored(rel string, dir bool) bool {
	if ig.Empty() || rel == "" {
		return false
	}
	// The folders above rel first: git never looks inside an ignored one.
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' && ig.match(rel[:i], true) {
			return true
		}
	}
	return ig.match(rel, dir)
}

// match applies the rules of every .gitignore above rel to rel itself, the
// deepest last so that it wins.
func (ig *Ignore) match(rel string, dir bool) bool {
	ignored := false
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	dirs := []string{""}
	for i := 0; i < len(parent); i++ {
		if parent[i] == '/' {
			dirs = append(dirs, parent[:i])
		}
	}
	if parent != "" {
		dirs = append(dirs, parent)
	}
	for _, d := range dirs {
		sub := rel
		if d != "" {
			sub = rel[len(d)+1:]
		}
		for _, p := range ig.byDir[d] {
			if p.dirOnly && !dir {
				continue
			}
			target := sub
			if p.base {
				target = path.Base(sub)
			}
			if p.re.MatchString(target) {
				ignored = !p.negate
			}
		}
	}
	return ignored
}

// Glob is a set of gitignore-style globs, matched against a path relative to
// a root: Find in Files' "files to include" and "files to exclude".
type Glob struct{ ig *Ignore }

// NewGlob compiles globs; an empty list matches nothing.
func NewGlob(globs []string) Glob {
	var lines []string
	for _, g := range globs {
		if g = strings.TrimSpace(g); g != "" && !strings.HasPrefix(g, "!") {
			lines = append(lines, g)
		}
	}
	if len(lines) == 0 {
		return Glob{}
	}
	return Glob{NewIgnore(IgnoreRules{"": lines})}
}

// Empty reports whether the set has no globs.
func (g Glob) Empty() bool { return g.ig.Empty() }

// Match reports whether rel, or a folder above it, matches one of the globs.
func (g Glob) Match(rel string, dir bool) bool { return !g.Empty() && g.ig.Ignored(rel, dir) }

// MatchSelf reports whether rel itself matches, ignoring the folders above it.
func (g Glob) MatchSelf(rel string, dir bool) bool { return !g.Empty() && g.ig.match(rel, dir) }

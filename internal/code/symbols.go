package code

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/Aman123at/agentic-os/internal/files"
)

// MaxIndexFiles is how many files a Workspace's symbol index covers. Past it
// only the folders of the files navigated from are indexed, and the index
// says so.
const MaxIndexFiles = 20_000

// tagBatch is how many files one ctags run is given.
const tagBatch = 2000

// Tagger runs ctags on some of a Workspace's files, relative to its root.
type Tagger func(ctx context.Context, rel []string) ([]files.Tag, error)

// Lister returns the Workspace's files, relative and sorted: its FileList.
type Lister func(ctx context.Context) ([]string, error)

// Index is a Workspace's symbols (PLAN.md M8.8), from universal-ctags run as
// the Realm's user: built at the first question, and afterwards brought up to
// date by re-tagging only the files the watch saw change. It lives in memory
// and goes with the Workspace.
type Index struct {
	Root string

	mu      sync.Mutex
	built   bool
	stale   bool
	partial bool
	total   int                    // the Workspace's files at the last build
	byFile  map[string][]files.Tag // by relative path
	dirs    map[string]bool        // partial: the folders indexed
	dirty   map[string]bool        // files changed since they were tagged
	names   []string               // unique names, sorted; nil when changed
	byName  map[string][]files.Tag // nil when changed
	running chan struct{}          // an update in progress
}

// IndexState says how much of the Workspace the index covers.
type IndexState struct {
	Files   int  // files with symbols
	Total   int  // files in the Workspace
	Partial bool // past MaxIndexFiles: only some folders
}

// Ensure brings the index up to date for a question asked from near, a file
// relative to the root ("" for none), and returns what it covers.
func (x *Index) Ensure(ctx context.Context, list Lister, tag Tagger, near string) (IndexState, error) {
	for {
		x.mu.Lock()
		if c := x.running; c != nil {
			x.mu.Unlock()
			select {
			case <-c:
				continue
			case <-ctx.Done():
				return IndexState{}, ctx.Err()
			}
		}
		done := make(chan struct{})
		x.running = done
		x.mu.Unlock()
		err := x.update(ctx, list, tag, near)
		x.mu.Lock()
		x.running = nil
		st := IndexState{Files: len(x.byFile), Total: x.total, Partial: x.partial}
		x.mu.Unlock()
		close(done)
		return st, err
	}
}

// update does Ensure's work; only one runs at a time.
func (x *Index) update(ctx context.Context, list Lister, tag Tagger, near string) error {
	x.mu.Lock()
	rebuild := !x.built || x.stale
	nearDir := ""
	if near != "" {
		nearDir = path.Dir(near)
	}
	needDir := x.partial && near != "" && !x.dirs[nearDir]
	var todo []string
	if !rebuild {
		for f := range x.dirty {
			if !x.partial || x.dirs[path.Dir(f)] {
				todo = append(todo, f)
			}
		}
	}
	x.mu.Unlock()

	var all []string
	if rebuild || needDir || len(todo) > 0 {
		var err error
		if all, err = list(ctx); err != nil {
			return err
		}
	}
	// A changed file is tagged only if the Workspace lists it: not one that
	// .gitignore leaves out, such as a build's output.
	listed := todo[:0]
	for _, f := range todo {
		if i := sort.SearchStrings(all, f); i < len(all) && all[i] == f {
			listed = append(listed, f)
		} else {
			x.mu.Lock()
			delete(x.dirty, f)
			x.mu.Unlock()
		}
	}
	todo = listed
	partial := x.partial
	var dirs []string
	if rebuild {
		partial = len(all) > MaxIndexFiles
		todo = todo[:0]
		if partial {
			if near != "" {
				dirs = []string{nearDir}
				todo = filesIn(all, nearDir)
			}
		} else {
			todo = all
		}
	} else if needDir {
		dirs = []string{nearDir}
		todo = append(todo, filesIn(all, nearDir)...)
	}
	// What is tagged now is no longer dirty, unless it changes again meanwhile.
	x.mu.Lock()
	taken := map[string]bool{}
	for _, f := range todo {
		if x.dirty[f] {
			taken[f] = true
			delete(x.dirty, f)
		}
	}
	x.mu.Unlock()

	var tags []files.Tag
	for i := 0; i < len(todo); i += tagBatch {
		got, err := tag(ctx, todo[i:min(i+tagBatch, len(todo))])
		if err != nil {
			x.mu.Lock()
			for f := range taken {
				x.dirty[f] = true
			}
			x.mu.Unlock()
			return err
		}
		tags = append(tags, got...)
	}

	x.mu.Lock()
	defer x.mu.Unlock()
	if rebuild {
		x.byFile = make(map[string][]files.Tag)
		x.dirs = make(map[string]bool)
		x.built, x.stale, x.partial, x.total = true, false, partial, len(all)
		if x.dirty == nil {
			x.dirty = make(map[string]bool)
		}
	} else if all != nil {
		x.total = len(all)
	}
	for _, d := range dirs {
		x.dirs[d] = true
	}
	for _, f := range todo {
		delete(x.byFile, f)
	}
	for _, t := range tags {
		x.byFile[t.Path] = append(x.byFile[t.Path], t)
	}
	x.names, x.byName = nil, nil
	return nil
}

// filesIn are the files directly in dir.
func filesIn(all []string, dir string) []string {
	var out []string
	for _, f := range all {
		if path.Dir(f) == dir {
			out = append(out, f)
		}
	}
	return out
}

// Apply notes a batch of the watch's changes: changed files are re-tagged at
// the next question, deleted ones forgotten now.
func (x *Index) Apply(changes []files.Change) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.built {
		return
	}
	for _, ch := range changes {
		rel, ok := relTo(x.Root, ch.Path)
		switch {
		case ch.Kind == files.Rescan:
			x.stale = true
		case !ok:
		case ch.Kind == files.Deleted:
			prefix := rel + "/"
			for f := range x.byFile {
				if f == rel || strings.HasPrefix(f, prefix) {
					delete(x.byFile, f)
				}
			}
			for f := range x.dirty {
				if f == rel || strings.HasPrefix(f, prefix) {
					delete(x.dirty, f)
				}
			}
			x.names, x.byName = nil, nil
		case !ch.Dir:
			x.dirty[rel] = true
		}
	}
}

// ---------------------------------------------------------------- questions

func (x *Index) indexByName() {
	if x.byName != nil {
		return
	}
	x.byName = make(map[string][]files.Tag)
	for _, tags := range x.byFile {
		for _, t := range tags {
			x.byName[t.Name] = append(x.byName[t.Name], t)
		}
	}
	x.names = make([]string, 0, len(x.byName))
	for n := range x.byName {
		x.names = append(x.names, n)
	}
	sort.Strings(x.names)
}

// Definitions are the symbols called name, nearest to from (a file, relative)
// first: the same file, then its folder, then by how far apart the folders
// are. When some are in from's language, only those.
func (x *Index) Definitions(name, from string) []files.Tag {
	x.mu.Lock()
	x.indexByName()
	found := append([]files.Tag(nil), x.byName[name]...)
	lang := ""
	if tags := x.byFile[from]; len(tags) > 0 {
		lang = tags[0].Language
	}
	x.mu.Unlock()
	return rankDefinitions(found, from, lang)
}

func rankDefinitions(found []files.Tag, from, lang string) []files.Tag {
	if lang != "" {
		var same []files.Tag
		for _, t := range found {
			if t.Language == lang {
				same = append(same, t)
			}
		}
		if len(same) > 0 {
			found = same
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if pa, pb := weakKind(a.Kind), weakKind(b.Kind); pa != pb {
			return !pa
		}
		if da, db := distance(from, a.Path), distance(from, b.Path); da != db {
			return da < db
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	return found
}

// weakKind is a symbol that names rather than defines: a package clause, an
// import. A definition of the same name comes first.
func weakKind(kind string) bool {
	switch kind {
	case "package", "packageName", "import", "module", "namespace", "unknown":
		return true
	}
	return false
}

// distance is how far apart two files are: 0 for the same file, 1 for the
// same folder, and one more for each folder between them.
func distance(a, b string) int {
	if a == b {
		return 0
	}
	da, db := strings.Split(path.Dir(a), "/"), strings.Split(path.Dir(b), "/")
	common := 0
	for common < len(da) && common < len(db) && da[common] == db[common] {
		common++
	}
	return 1 + (len(da) - common) + (len(db) - common)
}

// FileSymbols are one file's symbols, in order.
func (x *Index) FileSymbols(rel string) []files.Tag {
	x.mu.Lock()
	out := append([]files.Tag(nil), x.byFile[rel]...)
	x.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// Search is ⌘T: symbols whose names fuzzily match query, best first, at
// most limit.
func (x *Index) Search(query string, limit int) []files.Tag {
	x.mu.Lock()
	x.indexByName()
	names := x.names
	byName := x.byName
	x.mu.Unlock()
	var out []files.Tag
	for _, f := range Rank(query, names, limit) {
		tags := append([]files.Tag(nil), byName[f.Rel]...)
		sort.Slice(tags, func(i, j int) bool {
			if tags[i].Path != tags[j].Path {
				return tags[i].Path < tags[j].Path
			}
			return tags[i].Line < tags[j].Line
		})
		for _, t := range tags {
			if len(out) == limit {
				return out
			}
			out = append(out, t)
		}
	}
	return out
}

// relTo is p relative to root, slash-separated; false for the root itself or
// outside it.
func relTo(root, p string) (string, bool) {
	l := FileList{Root: root}
	return l.rel(p)
}

// LanguageGlobs are the files a symbol in p may be used from, for ⇧F12's
// references: its language's extensions, or its own.
func LanguageGlobs(p string) []string {
	ext := strings.ToLower(path.Ext(p))
	for _, family := range languageFamilies {
		for _, e := range family {
			if e == ext {
				out := make([]string, len(family))
				for i, x := range family {
					out[i] = "*" + x
				}
				return out
			}
		}
	}
	if ext == "" {
		return nil
	}
	return []string{"*" + ext}
}

var languageFamilies = [][]string{
	{".go"},
	{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
	{".py", ".pyi"},
	{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
	{".java", ".kt", ".kts"},
	{".rb", ".rake"},
	{".sh", ".bash"},
	{".php"},
	{".rs"},
	{".cs"},
	{".swift"},
	{".scala"},
	{".lua"},
}

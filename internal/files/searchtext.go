package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// OpSearchText is Code's Find in Files (PLAN.md M8.7): the lines under a
// folder that match a string or a regular expression. It runs as the Realm's
// user with ripgrep when the Machine has it, and with a search of its own
// otherwise; both skip what the Workspace watch skips, what .gitignore files
// ignore and binary files, look in hidden files, follow no symlink, and give
// the same results.
const OpSearchText = "search_text"

// Search engines, as TextSearchResult.Engine names them.
const (
	EngineRipgrep = "ripgrep"
	EngineBuiltin = "built-in"
)

// MaxTextMatches is how many matching lines one search returns by default.
const MaxTextMatches = 2000

// maxMatchText is how much of a matching line is returned, in UTF-16 units
// (JavaScript's string length), around its first match.
const maxMatchText = 400

// ErrBadQuery is returned for a search that cannot run, such as a regular
// expression that does not compile.
var ErrBadQuery = errors.New("the search is not valid")

type TextSearchArgs struct {
	Root  string `json:"root"`
	Query string `json:"query"`
	Regex bool   `json:"regex,omitempty"`
	// Case makes the search case-sensitive.
	Case bool `json:"case,omitempty"`
	// Include limits the search to files matching one of these globs;
	// Exclude leaves out files and folders matching one. Globs are
	// gitignore-style and relative to Root: "*.go", "src/**", "vendor".
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	// Max is how many matching lines to return; 0 means MaxTextMatches.
	Max int `json:"max,omitempty"`
	// Engine picks EngineRipgrep or EngineBuiltin; "" is ripgrep when it is
	// installed.
	Engine string `json:"engine,omitempty"`
}

// TextFile is one file's matching lines.
type TextFile struct {
	Path    string      `json:"path"`
	Matches []TextMatch `json:"matches"`
}

// TextMatch is a matching line: its number (from 1), its text (trimmed, and
// cut to maxMatchText around the first match, with "…" where it was cut), and
// where it matched, as [start, end) UTF-16 offsets into Text.
type TextMatch struct {
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Ranges [][2]int `json:"ranges"`
}

// TextSearchResult sums up a search once every TextFile has been sent.
type TextSearchResult struct {
	Files     int    `json:"files"`
	Matches   int    `json:"matches"`
	Truncated bool   `json:"truncated,omitempty"`
	Engine    string `json:"engine"`
}

// TextSearcher runs Find in Files, sending each file's matches as they are found.
type TextSearcher interface {
	SearchText(ctx context.Context, a TextSearchArgs, emit func(TextFile) error) (TextSearchResult, error)
}

// SearchText implements TextSearcher.
func (p InProcess) SearchText(ctx context.Context, a TextSearchArgs, emit func(TextFile) error) (TextSearchResult, error) {
	return p.Ops.SearchText(ctx, a, emit)
}

// compileQuery is the search as a Go regular expression, which also checks
// a regex ripgrep would be given.
func compileQuery(a TextSearchArgs) (*regexp.Regexp, error) {
	if a.Query == "" {
		return nil, fmt.Errorf("%w: nothing to search for", ErrBadQuery)
	}
	expr := a.Query
	if !a.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if !a.Case {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadQuery, err)
	}
	return re, nil
}

// SearchText searches a.Root, sending each file's matches through emit.
func (o Ops) SearchText(ctx context.Context, a TextSearchArgs, emit func(TextFile) error) (TextSearchResult, error) {
	re, err := compileQuery(a)
	if err != nil {
		return TextSearchResult{}, err
	}
	if fi, err := os.Stat(a.Root); err != nil {
		return TextSearchResult{}, err
	} else if !fi.IsDir() {
		return TextSearchResult{}, fmt.Errorf("%s is not a folder", a.Root)
	}
	if a.Max <= 0 {
		a.Max = MaxTextMatches
	}
	engine := a.Engine
	rg := ""
	if engine != EngineBuiltin {
		if p, err := exec.LookPath("rg"); err == nil {
			rg, engine = p, EngineRipgrep
		} else if engine == EngineRipgrep {
			return TextSearchResult{}, fmt.Errorf("ripgrep is not installed")
		} else {
			engine = EngineBuiltin
		}
	}
	res := TextSearchResult{Engine: engine}
	send := func(f TextFile) error {
		res.Files++
		res.Matches += len(f.Matches)
		return emit(f)
	}
	if engine == EngineRipgrep {
		err = ripgrep(ctx, rg, a, send, &res)
	} else {
		err = searchTree(ctx, a, re, send, &res)
	}
	return res, err
}

// ripgrepArgs are ripgrep's flags for a, set so that it walks exactly as
// searchTree does.
func ripgrepArgs(a TextSearchArgs) []string {
	args := []string{"--json", "--no-config", "--no-messages", "--hidden", "--no-follow",
		// Only the tree's own .gitignore files, as searchTree reads them.
		"--no-require-git", "--no-ignore-parent", "--no-ignore-global", "--no-ignore-exclude", "--no-ignore-dot",
		"--max-filesize", "4M", "--sort", "path"}
	if a.Case {
		args = append(args, "--case-sensitive")
	} else {
		args = append(args, "--ignore-case")
	}
	if !a.Regex {
		args = append(args, "--fixed-strings")
	}
	for name := range skipDirs {
		args = append(args, "--glob", "!"+name+"/")
	}
	// Include globs are applied to ripgrep's results instead: as --glob they
	// would override .gitignore files, which Code always honours.
	for _, g := range cleanGlobs(a.Exclude) {
		args = append(args, "--glob", "!"+g)
	}
	// Run in Root: ripgrep matches globs against paths relative to where it runs.
	return append(args, "--regexp", a.Query, "--", ".")
}

func cleanGlobs(globs []string) []string {
	var out []string
	for _, g := range globs {
		if g = strings.TrimSpace(g); g != "" && !strings.HasPrefix(g, "!") {
			out = append(out, g)
		}
	}
	return out
}

// rgLine is the part of ripgrep's --json output a search reads.
type rgLine struct {
	Type string `json:"type"`
	Data struct {
		Path       struct{ Text string }  `json:"path"`
		Lines      struct{ Text *string } `json:"lines"`
		LineNumber int                    `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

func ripgrep(ctx context.Context, rg string, a TextSearchArgs, emit func(TextFile) error, res *TextSearchResult) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, rg, ripgrepArgs(a)...)
	cmd.Dir = a.Root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	include := NewGlob(a.Include)
	skip := false
	var cur TextFile
	count := 0
	stopped := false
	var emitErr error
	flush := func() {
		if len(cur.Matches) > 0 && emitErr == nil {
			emitErr = emit(cur)
		}
		cur = TextFile{}
	}
	for sc.Scan() {
		var l rgLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		switch l.Type {
		case "begin":
			cur = TextFile{Path: filepath.Join(a.Root, l.Data.Path.Text)}
			r := filepath.ToSlash(filepath.Clean(l.Data.Path.Text))
			skip = !include.Empty() && !include.MatchSelf(r, false)
		case "match":
			if skip || l.Data.Lines.Text == nil {
				continue // not UTF-8: ripgrep sends it as bytes
			}
			ranges := make([][2]int, 0, len(l.Data.Submatches))
			for _, m := range l.Data.Submatches {
				ranges = append(ranges, [2]int{m.Start, m.End})
			}
			if m, ok := matchLine(l.Data.LineNumber, *l.Data.Lines.Text, ranges); ok {
				cur.Matches = append(cur.Matches, m)
				count++
			}
		case "end":
			flush()
		}
		if emitErr != nil {
			cancel()
			break
		}
		if count >= a.Max {
			flush()
			res.Truncated, stopped = true, true
			cancel()
			break
		}
	}
	if emitErr == nil && !stopped {
		flush()
	}
	_, _ = io.Copy(io.Discard, out)
	err = cmd.Wait()
	switch {
	case emitErr != nil:
		return emitErr
	case stopped:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	}
	// ripgrep exits 1 when nothing matched.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ripgrep: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return sc.Err()
}

// searchTree is the search without ripgrep.
func searchTree(ctx context.Context, a TextSearchArgs, re *regexp.Regexp, emit func(TextFile) error, res *TextSearchResult) error {
	include, exclude := NewGlob(a.Include), NewGlob(a.Exclude)
	ig := &Ignore{byDir: map[string][]ignorePattern{}}
	count := 0
	errStop := errors.New("stop")
	var walk func(rel string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(a.Root, filepath.FromSlash(rel))
		des, err := os.ReadDir(dir) // sorted by name, as ripgrep's --sort path
		if err != nil {
			return nil
		}
		for _, l := range readIgnoreFile(filepath.Join(dir, ".gitignore")) {
			if p, ok := parseIgnoreLine(l); ok {
				ig.byDir[rel] = append(ig.byDir[rel], p)
			}
		}
		for _, de := range des {
			name := de.Name()
			r := path.Join(rel, name)
			isDir := de.IsDir()
			if (isDir && skipDirs[name]) || ig.match(r, isDir) || exclude.MatchSelf(r, isDir) {
				continue
			}
			if isDir {
				if err := walk(r); err != nil {
					return err
				}
				continue
			}
			if !de.Type().IsRegular() || (!include.Empty() && !include.MatchSelf(r, false)) {
				continue
			}
			f := TextFile{Path: filepath.Join(dir, name)}
			searchText(f.Path, re, a.Max-count, &f.Matches)
			if len(f.Matches) == 0 {
				continue
			}
			count += len(f.Matches)
			if err := emit(f); err != nil {
				return err
			}
			if count >= a.Max {
				res.Truncated = true
				return errStop
			}
		}
		return nil
	}
	if err := walk(""); err != nil && !errors.Is(err, errStop) {
		return err
	}
	return nil
}

// searchText adds up to max of path's matching lines.
func searchText(p string, re *regexp.Regexp, max int, out *[]TextMatch) {
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() > maxSearchFile {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSearchFile+1))
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return // binary files are not searched, as ripgrep leaves them
	}
	for n, line := 1, data; len(line) > 0 && len(*out) < max; n++ {
		text := line
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			text, line = line[:i], line[i+1:]
		} else {
			line = nil
		}
		if !utf8.Valid(text) {
			continue // ripgrep's JSON sends such a line as bytes, which it skips
		}
		locs := re.FindAllIndex(text, -1)
		if len(locs) == 0 {
			continue
		}
		ranges := make([][2]int, 0, len(locs))
		for _, l := range locs {
			ranges = append(ranges, [2]int{l[0], l[1]})
		}
		if m, ok := matchLine(n, string(text), ranges); ok {
			*out = append(*out, m)
		}
	}
}

// matchLine makes a TextMatch of a line and its matches as byte offsets: the
// line without its ending or leading space, cut around its first match, the
// offsets turned into UTF-16 ones. A line whose matches are all empty is not
// a match.
func matchLine(n int, text string, byteRanges [][2]int) (TextMatch, bool) {
	text = strings.TrimRight(text, "\r\n")
	// UTF-16 offset at each byte offset.
	at := make([]int, len(text)+1)
	units := 0
	for i := 0; i < len(text); {
		r, w := utf8.DecodeRuneInString(text[i:])
		for j := i; j < i+w; j++ {
			at[j] = units
		}
		units += utf16.RuneLen(r)
		i += w
	}
	at[len(text)] = units
	var ranges [][2]int
	for _, r := range byteRanges {
		s, e := min(r[0], len(text)), min(r[1], len(text))
		if e > s {
			ranges = append(ranges, [2]int{at[s], at[e]})
		}
	}
	if len(ranges) == 0 {
		return TextMatch{}, false
	}
	u := utf16.Encode([]rune(text))
	// Leading space goes, unless the match is in it.
	start := 0
	for start < len(u) && start < ranges[0][0] && unicode.IsSpace(rune(u[start])) {
		start++
	}
	// A long line is cut to a window around its first match.
	if ranges[0][1]-start > maxMatchText {
		start = max(start, ranges[0][0]-40)
	}
	end := min(len(u), start+maxMatchText)
	prefix := ""
	if start > 0 && !isSpaceOnly(u[:start]) {
		prefix = "…"
	}
	suffix := ""
	if end < len(u) {
		suffix = "…"
	}
	shift := utf8.RuneCountInString(prefix) - start
	var kept [][2]int
	for _, r := range ranges {
		if r[0] >= end {
			break
		}
		kept = append(kept, [2]int{max(r[0], start) + shift, min(r[1], end) + shift})
	}
	return TextMatch{Line: n, Text: prefix + string(utf16.Decode(u[start:end])) + suffix, Ranges: kept}, true
}

func isSpaceOnly(u []uint16) bool {
	for _, c := range u {
		if !unicode.IsSpace(rune(c)) {
			return false
		}
	}
	return true
}

// serveSearchText is the worker's side of OpSearchText: files are written as
// they are found, then the result.
func serveSearchText(ctx context.Context, req request, enc *json.Encoder) error {
	var a TextSearchArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return err
	}
	res, err := req.Ops.SearchText(ctx, a, func(f TextFile) error {
		return enc.Encode(struct {
			Text TextFile `json:"text"`
		}{f})
	})
	resp := response{Done: true}
	if err != nil {
		resp.Error, resp.Kind = err.Error(), errorKind(err)
	} else if resp.Result, err = json.Marshal(res); err != nil {
		resp.Error = err.Error()
	}
	return enc.Encode(resp)
}

// readSearchText reads a search worker's lines: files, then the result.
func readSearchText(r io.Reader, emit func(TextFile) error, res *TextSearchResult) error {
	dec := json.NewDecoder(r)
	for {
		var line struct {
			Text *TextFile `json:"text,omitempty"`
			response
		}
		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("file worker exited without a result")
			}
			return fmt.Errorf("file worker: %w", err)
		}
		if line.Text != nil {
			if err := emit(*line.Text); err != nil {
				return err
			}
			continue
		}
		if line.Done {
			if line.Error != "" {
				return kindError(line.Kind, line.Error)
			}
			return decodeResult(line.Result, res)
		}
	}
}

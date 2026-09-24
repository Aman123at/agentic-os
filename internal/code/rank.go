package code

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Found is a file Quick Open offers: its path relative to the Workspace, and
// which characters of it matched the query, as UTF-16 offsets for the Desktop
// to highlight.
type Found struct {
	Rel       string
	Positions []int
	score     float64
}

// Scores, after fzy: a match right after a separator or at a capital counts
// for more than one mid-word, consecutive matches more still, and gaps cost a
// little.
const (
	scoreGapLeading  = -0.005
	scoreGapTrailing = -0.005
	scoreGapInner    = -0.01
	scoreConsecutive = 1.0
	scoreSlash       = 0.9
	scoreWord        = 0.8
	scoreCapital     = 0.7
	scoreDot         = 0.6
	// A query that matches within the file's name beats any that needs the
	// folders: "util" finds util.ts before utilities/helper.ts.
	scoreInName = 100
	// maxRankLen caps the path length scored; longer ones rank as unmatched.
	maxRankLen = 1024
)

var negInf = math.Inf(-1)

// Rank returns the paths matching query best first, at most limit of them.
// A path matches when the query's characters appear in it in order, ignoring
// case and spaces; the score favours matches in the file's name, at word
// starts and in runs. Ties go to the shorter path, then the first
// alphabetically. An empty query returns the first paths as given.
func Rank(query string, paths []string, limit int) []Found {
	q := []rune(strings.ToLower(strings.Join(strings.Fields(query), "")))
	if len(q) == 0 {
		out := make([]Found, 0, min(limit, len(paths)))
		for _, p := range paths[:min(limit, len(paths))] {
			out = append(out, Found{Rel: p})
		}
		return out
	}
	s := newScorer(len(q))
	var found []Found
	for _, p := range paths {
		if f, ok := s.match(q, p); ok {
			found = append(found, f)
		}
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if len(a.Rel) != len(b.Rel) {
			return len(a.Rel) < len(b.Rel)
		}
		return a.Rel < b.Rel
	})
	if len(found) > limit {
		found = found[:limit]
	}
	return found
}

// scorer holds the DP tables, reused across paths.
type scorer struct {
	d, m  [][]float64
	bonus []float64
	hay   []rune
	lower []rune
}

func newScorer(n int) *scorer {
	return &scorer{d: make([][]float64, n), m: make([][]float64, n)}
}

func (s *scorer) match(q []rune, path string) (Found, bool) {
	hay := []rune(path)
	if len(hay) > maxRankLen || !subsequence(q, hay) {
		return Found{}, false
	}
	// The file's name alone first; a query with a slash always needs the path.
	name := strings.LastIndexByte(path, '/') + 1
	nameRunes := len([]rune(path[:name]))
	if !containsRune(q, '/') && subsequence(q, hay[nameRunes:]) {
		score, pos := s.score(q, hay, nameRunes)
		return Found{Rel: path, Positions: utf16Positions(hay, pos), score: score + scoreInName}, true
	}
	score, pos := s.score(q, hay, 0)
	return Found{Rel: path, Positions: utf16Positions(hay, pos), score: score}, true
}

// score is fzy's: the best alignment of q in hay[from:], and where each of
// q's characters landed.
func (s *scorer) score(q, full []rune, from int) (float64, []int) {
	hay := full[from:]
	n, m := len(q), len(hay)
	if n == m {
		pos := make([]int, n)
		for i := range pos {
			pos[i] = from + i
		}
		return math.Inf(1) / 2, pos // an exact match beats everything
	}
	if cap(s.bonus) < m {
		s.bonus, s.lower = make([]float64, m), make([]rune, m)
	}
	bonus, lower := s.bonus[:m], s.lower[:m]
	prev := '/'
	if from > 0 {
		prev = full[from-1]
	}
	for j, c := range hay {
		bonus[j] = bonusFor(prev, c)
		lower[j] = unicode.ToLower(c)
		prev = c
	}
	for i := 0; i < n; i++ {
		if cap(s.d[i]) < m {
			s.d[i], s.m[i] = make([]float64, m), make([]float64, m)
		}
		d, mm := s.d[i][:m], s.m[i][:m]
		best := negInf
		gap := scoreGapInner
		if i == n-1 {
			gap = scoreGapTrailing
		}
		for j := 0; j < m; j++ {
			if lower[j] == q[i] {
				score := negInf
				switch {
				case i == 0:
					score = float64(j)*scoreGapLeading + bonus[j]
				case j > 0:
					score = math.Max(s.m[i-1][j-1]+bonus[j], s.d[i-1][j-1]+scoreConsecutive)
				}
				d[j] = score
				best = math.Max(score, best+gap)
			} else {
				d[j] = negInf
				best += gap
			}
			mm[j] = best
		}
	}
	// Walk back from the end for the positions of the best alignment.
	pos := make([]int, n)
	matchRequired := false
	j := m - 1
	for i := n - 1; i >= 0; i-- {
		for ; j >= 0; j-- {
			d, mm := s.d[i][j], s.m[i][j]
			if d != negInf && (matchRequired || d == mm) {
				// Stay on a consecutive run if the previous character took it.
				matchRequired = i > 0 && j > 0 && mm == s.d[i-1][j-1]+scoreConsecutive
				pos[i] = from + j
				j--
				break
			}
		}
	}
	return s.m[n-1][m-1], pos
}

func bonusFor(prev, c rune) float64 {
	switch {
	case prev == '/':
		return scoreSlash
	case prev == '-' || prev == '_' || prev == ' ':
		return scoreWord
	case prev == '.':
		return scoreDot
	case unicode.IsLower(prev) && unicode.IsUpper(c):
		return scoreCapital
	}
	return 0
}

func subsequence(q, hay []rune) bool {
	i := 0
	for _, c := range hay {
		if i < len(q) && unicode.ToLower(c) == q[i] {
			i++
		}
	}
	return i == len(q)
}

func containsRune(rs []rune, r rune) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// utf16Positions turns rune indexes into UTF-16 offsets.
func utf16Positions(hay []rune, pos []int) []int {
	out := make([]int, len(pos))
	units, k := 0, 0
	for i, r := range hay {
		for k < len(pos) && pos[k] == i {
			out[k] = units
			k++
		}
		units += utf16.RuneLen(r)
	}
	return out
}

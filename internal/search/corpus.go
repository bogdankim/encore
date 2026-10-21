package search

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bogdankim/encore/internal/library"
)

// corpus holds the folded fields of one kind of entity in a single buffer, so
// a query term is found with one strings.Index sweep instead of a call per
// field per entity. Entity i's field f is buf[fs[i*nf+f] : fs[i*nf+f+1]-1];
// every field is followed by a NUL, which folded text never contains.
type corpus struct {
	buf     string
	fs      []int32
	nf      int
	n       int
	weights []int
	pop     []int16 // per-entity popularity bonus
}

func newCorpus(n int, weights []int, fill func(i int, f []string) int) corpus {
	c := corpus{nf: len(weights), n: n, weights: weights, fs: make([]int32, 0, n*len(weights)+1), pop: make([]int16, n)}
	fields := make([]string, c.nf)
	var b strings.Builder
	// Folded fields average well under 16 bytes; one pass with a guess beats
	// a second pass over every entity to measure.
	b.Grow(n * c.nf * 16)
	for i := range n {
		c.pop[i] = int16(fill(i, fields))
		for _, f := range fields {
			c.fs = append(c.fs, int32(b.Len()))
			b.WriteString(f)
			b.WriteByte(0)
		}
	}
	c.fs = append(c.fs, int32(b.Len()))
	c.buf = b.String()
	return c
}

func (c *corpus) field(i, f int) string {
	k := i*c.nf + f
	return c.buf[c.fs[k] : c.fs[k+1]-1]
}

func (c *corpus) entity(i int) (start, end int) {
	return int(c.fs[i*c.nf]), int(c.fs[(i+1)*c.nf])
}

// entityAt returns the entity whose fields contain byte offset off, searching
// from entity lo onwards: a gallop then a binary search, since consecutive
// matches are usually close together.
func (c *corpus) entityAt(off, lo int) int {
	start := func(i int) int { return int(c.fs[i*c.nf]) }
	hi, step := lo+1, 1
	for hi < c.n && start(hi) <= off {
		lo = hi
		step *= 2
		hi = lo + step
	}
	hi = min(hi, c.n)
	for hi-lo > 1 {
		mid := int(uint(lo+hi) >> 1)
		if start(mid) <= off {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// Scores: strict hits sit above fuzzy ones whatever their details.
const strictBase = 1 << 20

// Match tiers, for picking top results.
const (
	tierFuzzy  = 1
	tierAny    = 2 // every term is at least a substring
	tierWords  = 3 // every term starts a word
	tierPrefix = 4 // the query starts the primary field
	tierExact  = 5 // the query is the primary field
)

// cand is a scored entity.
type cand struct {
	score int
	tier  int
	idx   int
}

// better orders candidates: higher score, then library order.
func better(a, b cand) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.idx < b.idx
}

// find returns the best k matches for q (all of them when k <= 0), best first.
func (c *corpus) find(q *query, k int, fuzzy bool) []cand {
	if c.n == 0 {
		return nil
	}
	top := topK{k: k}
	if fuzzy {
		for i := range c.n {
			if s, ok := c.fuzzyScore(i, q); ok {
				top.push(cand{score: s, tier: tierFuzzy, idx: i})
			}
		}
		return top.sorted()
	}

	scan := q.terms[q.scan]
	for off, i := 0, 0; off < len(c.buf); i++ {
		j := strings.Index(c.buf[off:], scan)
		if j < 0 {
			break
		}
		i = c.entityAt(off+j, i)
		start, end := c.entity(i)
		off = end
		if c.hasAll(c.buf[start:end], q) {
			s, tier := c.score(i, q)
			top.push(cand{score: s, tier: tier, idx: i})
		}
	}
	return top.sorted()
}

// matchAll returns, in order, the entities that contain every term of q (or,
// with fuzzy, match every term as a subsequence of one field).
func (c *corpus) matchAll(q *query, fuzzy bool) []int {
	var out []int
	if fuzzy {
		for i := range c.n {
			if _, ok := c.fuzzyScore(i, q); ok {
				out = append(out, i)
			}
		}
		return out
	}
	scan := q.terms[q.scan]
	for off, i := 0, 0; off < len(c.buf); i++ {
		j := strings.Index(c.buf[off:], scan)
		if j < 0 {
			break
		}
		i = c.entityAt(off+j, i)
		start, end := c.entity(i)
		off = end
		if c.hasAll(c.buf[start:end], q) {
			out = append(out, i)
		}
	}
	return out
}

// hasAll reports whether hay contains every term of q but the scan term,
// which the caller already found there.
func (c *corpus) hasAll(hay string, q *query) bool {
	for ti, t := range q.terms {
		if ti != q.scan && !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// Points per match class, multiplied by the field weight.
var classPoints = [...]int{
	classNone:       0,
	classSubstring:  10,
	classWordPrefix: 20,
	classWord:       26,
	classPrefix:     30,
	classPrefixWord: 36,
}

const (
	classNone       = iota
	classSubstring  // somewhere inside a word
	classWordPrefix // at the start of a word
	classWord       // a whole word
	classPrefix     // at the start of the field (a leading "the " ignored)
	classPrefixWord // a whole word at the start of the field
)

// score rates entity i, which contains every term of q.
func (c *corpus) score(i int, q *query) (score, tier int) {
	tier = tierWords
	bestPts, bestField := 0, 0
	sameField := true // every term's best match is in one field
	for ti, t := range q.terms {
		termBest, termField := 0, 0
		for f := range c.nf {
			cls, _ := classify(c.field(i, f), t)
			if p := classPoints[cls] * c.weights[f]; p > termBest {
				termBest, termField = p, f
			}
		}
		if termBest < classPoints[classWordPrefix]*c.weights[termField] {
			tier = tierAny
		}
		score += termBest
		if ti > 0 && termField != bestField {
			sameField = false
		}
		if ti == 0 || termBest > bestPts {
			bestPts, bestField = termBest, termField
		}
	}
	if sameField && len(q.terms) > 1 {
		score += 8 * c.weights[bestField]
	}

	// The whole query as the primary field, or its start, matters most; in a
	// secondary field (a song's artist) it only adds a little.
	for f := range c.nf {
		field := c.field(i, f)
		bare := stripThe(field)
		switch {
		case field == q.folded || bare == q.folded:
			if f == 0 {
				score += 50 * c.weights[f]
				tier = tierExact
			} else {
				score += 25 * c.weights[f]
			}
		case strings.HasPrefix(field, q.folded) || strings.HasPrefix(bare, q.folded):
			if f == 0 {
				score += 25 * c.weights[f]
				tier = max(tier, tierPrefix)
			} else {
				score += 10 * c.weights[f]
			}
		}
	}

	// Shorter fields win: reward the share of the best field the query covers.
	if n := len(c.field(i, bestField)); n > 0 {
		score += min(30, 30*len(q.compact)/n)
	}
	return strictBase + score + int(c.pop[i]), tier
}

func stripThe(s string) string {
	if len(s) > 4 && strings.HasPrefix(s, "the ") {
		return s[4:]
	}
	return s
}

// classify returns how well term occurs in field, and where (-1 if not).
func classify(field, term string) (class, pos int) {
	pos = -1
	for from := 0; ; {
		j := strings.Index(field[from:], term)
		if j < 0 {
			return class, pos
		}
		at := from + j
		if cls := classAt(field, term, at); cls > class {
			class, pos = cls, at
			if class >= classWord {
				// A later occurrence can only beat this by being the prefix,
				// which it can't be.
				return class, pos
			}
		}
		from = at + 1
	}
}

func classAt(field, term string, at int) int {
	end := at + len(term)
	wordEnd := end == len(field) || boundaryAt(field, end)
	switch {
	case at == 0 || at == 4 && strings.HasPrefix(field, "the "):
		if wordEnd {
			return classPrefixWord
		}
		return classPrefix
	case boundaryBefore(field, at):
		if wordEnd {
			return classWord
		}
		return classWordPrefix
	}
	return classSubstring
}

// boundaryBefore reports whether a word starts at byte at.
func boundaryBefore(s string, at int) bool {
	if at == 0 {
		return true
	}
	c := s[at-1]
	if c >= utf8.RuneSelf {
		r, _ := utf8.DecodeLastRuneInString(s[:at])
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}
	return !isWordByte(c)
}

// boundaryAt reports whether the byte at i ends a word.
func boundaryAt(s string, i int) bool {
	c := s[i]
	if c >= utf8.RuneSelf {
		r, _ := utf8.DecodeRuneInString(s[i:])
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}
	return !isWordByte(c)
}

func isWordByte(c byte) bool {
	return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '\''
}

// fuzzyScore matches every term of q as a subsequence of one field of entity
// i, scoring like fzf: consecutive runs and word starts earn bonuses, gaps
// cost.
func (c *corpus) fuzzyScore(i int, q *query) (int, bool) {
	start, end := c.entity(i)
	hay := c.buf[start:end]
	for _, t := range q.terms {
		if !subsequence(hay, t) {
			return 0, false
		}
	}
	score := 0
	for ti := range q.terms {
		best := -1
		for f := range c.nf {
			if s, ok := fuzzyMatch(c.field(i, f), q.fuzzy[ti], nil); ok {
				if s = s * c.weights[f] / wPrimary; s > best {
					best = s
				}
			}
		}
		if best < 0 {
			return 0, false
		}
		score += best
	}
	return max(0, min(strictBase-1, score+int(c.pop[i]))), true
}

// subsequence reports whether t's runes occur in s in order.
func subsequence(s, t string) bool {
	k := 0
	for i := 0; i < len(s) && k < len(t); i++ {
		if s[i] == t[k] {
			k++
		}
	}
	if k == len(t) {
		return true
	}
	// Byte matching can only fail falsely for non-ASCII terms, never succeed
	// falsely for ASCII ones; retry rune by rune when the term has multibyte
	// runes.
	for i := range len(t) {
		if t[i] >= utf8.RuneSelf {
			return runeSubsequence(s, t)
		}
	}
	return false
}

func runeSubsequence(s, t string) bool {
	for _, r := range s {
		tr, n := utf8.DecodeRuneInString(t)
		if r == tr {
			t = t[n:]
			if t == "" {
				return true
			}
		}
	}
	return t == ""
}

const (
	fzMatch       = 16
	fzFirstBonus  = 10 // matched the field's first rune
	fzWordBonus   = 8  // matched the first rune of a word
	fzConsecutive = 6
	fzGapStart    = 3
	fzGapExtend   = 1
)

// fuzzyMatch finds pattern in field as a subsequence. It takes the shortest
// window ending at the earliest possible place (fzf's v1 algorithm), scores
// it, and appends the byte offsets of matched runes to pos when pos is not nil.
func fuzzyMatch(field string, pattern []rune, pos *[]int) (int, bool) {
	if len(pattern) == 0 {
		return 0, false
	}
	pi, end := 0, -1
	for i, r := range field {
		if r == pattern[pi] {
			pi++
			if pi == len(pattern) {
				end = i + utf8.RuneLen(r)
				break
			}
		}
	}
	if end < 0 {
		return 0, false
	}
	start := end
	for i, pj := end, len(pattern); pj > 0; {
		r, n := utf8.DecodeLastRuneInString(field[:i])
		i -= n
		if r == pattern[pj-1] {
			pj--
			start = i
		}
	}

	score, pi, consecutive := 0, 0, false
	prev := ' '
	if start > 0 {
		prev, _ = utf8.DecodeLastRuneInString(field[:start])
	}
	for i, r := range field[start:end] {
		if pi < len(pattern) && r == pattern[pi] {
			score += fzMatch
			switch {
			case start+i == 0:
				score += fzFirstBonus
			case !unicode.IsLetter(prev) && !unicode.IsDigit(prev):
				score += fzWordBonus
			}
			if consecutive {
				score += fzConsecutive
			}
			if pos != nil {
				*pos = append(*pos, start+i)
			}
			pi++
			consecutive = true
		} else {
			if consecutive {
				score -= fzGapStart
			} else {
				score -= fzGapExtend
			}
			consecutive = false
		}
		prev = r
	}
	return score, true
}

// positions computes highlight positions in the displayed strings for entity
// i, re-running the match that scored it.
func (c *corpus) positions(i int, q *query, fuzzy bool, display ...string) [][]int {
	offs := make([][]int, c.nf)
	for ti, t := range q.terms {
		bestF, bestPts, bestPos := -1, -1, -1
		var fuzzyPos []int
		for f := range c.nf {
			field := c.field(i, f)
			if fuzzy {
				var p []int
				if s, ok := fuzzyMatch(field, q.fuzzy[ti], &p); ok && s*c.weights[f] > bestPts {
					bestF, bestPts, fuzzyPos = f, s*c.weights[f], p
				}
				continue
			}
			if cls, at := classify(field, t); cls != classNone && classPoints[cls]*c.weights[f] > bestPts {
				bestF, bestPts, bestPos = f, classPoints[cls]*c.weights[f], at
			}
		}
		switch {
		case bestF < 0:
		case fuzzy:
			offs[bestF] = append(offs[bestF], fuzzyPos...)
		default:
			for b := bestPos; b < bestPos+len(t); b++ {
				offs[bestF] = append(offs[bestF], b)
			}
		}
	}
	out := make([][]int, c.nf)
	for f, o := range offs {
		if len(o) > 0 {
			out[f] = runeIndexes(display[f], c.field(i, f), o)
		}
	}
	return out
}

// runeIndexes maps byte offsets in folded (the fold of display) to sorted,
// distinct rune indexes in display.
func runeIndexes(display, folded string, offs []int) []int {
	f, m := library.FoldMap(display)
	if f != folded {
		return nil
	}
	out := make([]int, 0, len(offs))
	for _, o := range offs {
		if o < len(m) {
			out = append(out, m[o])
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// topK keeps the k best candidates in a min-heap (worst at the root).
type topK struct {
	k int
	h []cand
}

func (t *topK) push(c cand) {
	if t.k <= 0 || len(t.h) < t.k {
		t.h = append(t.h, c)
		if t.k > 0 {
			t.up(len(t.h) - 1)
		}
		return
	}
	if !better(c, t.h[0]) {
		return
	}
	t.h[0] = c
	t.down(0)
}

func (t *topK) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !better(t.h[p], t.h[i]) {
			break
		}
		t.h[p], t.h[i] = t.h[i], t.h[p]
		i = p
	}
}

func (t *topK) down(i int) {
	for {
		l := 2*i + 1
		if l >= len(t.h) {
			return
		}
		w := l
		if r := l + 1; r < len(t.h) && better(t.h[l], t.h[r]) {
			w = r
		}
		if !better(t.h[i], t.h[w]) {
			return
		}
		t.h[i], t.h[w] = t.h[w], t.h[i]
		i = w
	}
}

func (t *topK) sorted() []cand {
	slices.SortFunc(t.h, func(a, b cand) int {
		if better(a, b) {
			return -1
		}
		if better(b, a) {
			return 1
		}
		return 0
	})
	return t.h
}

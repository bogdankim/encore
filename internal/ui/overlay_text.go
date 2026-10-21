package ui

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// ovMark renders s in base with the runes at pos (ascending rune indexes) in
// match. Runs of equal style are rendered together.
func ovMark(s string, pos []int, base, match lipgloss.Style) string {
	if len(pos) == 0 {
		return base.Render(s)
	}
	var b strings.Builder
	var run []rune
	inMatch := false
	flush := func() {
		if len(run) == 0 {
			return
		}
		if inMatch {
			b.WriteString(match.Render(string(run)))
		} else {
			b.WriteString(base.Render(string(run)))
		}
		run = run[:0]
	}
	p := 0
	i := 0
	for _, r := range s {
		for p < len(pos) && pos[p] < i {
			p++
		}
		m := p < len(pos) && pos[p] == i
		if m != inMatch {
			flush()
			inMatch = m
		}
		run = append(run, r)
		i++
	}
	flush()
	return b.String()
}

// ovShift adds d to every position.
func ovShift(pos []int, d int) []int {
	if len(pos) == 0 || d == 0 {
		return pos
	}
	out := make([]int, len(pos))
	for i, p := range pos {
		out[i] = p + d
	}
	return out
}

// ovKeys joins the display forms of several key sequences (from
// keymap.Display, which already spells the leader as "space"): "p · space space".
func ovKeys(keys ...string) string {
	return strings.Join(keys, " · ")
}

// ovFuzzy matches query against text: every whitespace-separated term must
// occur, as a substring or failing that as a subsequence. It returns a score
// (higher is better) and the matched rune positions in text.
func ovFuzzy(text, query string) (score int, pos []int, ok bool) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0, nil, true
	}
	hay := []rune(strings.ToLower(text))
	used := make([]bool, len(hay))
	for _, term := range terms {
		s, p, found := ovTerm(hay, []rune(term))
		if !found {
			return 0, nil, false
		}
		score += s
		for _, i := range p {
			used[i] = true
		}
	}
	for i, u := range used {
		if u {
			pos = append(pos, i)
		}
	}
	return score - len(hay)/8, pos, true
}

// ovContainsAll reports whether every term of query occurs in lower.
func ovContainsAll(lower, query string) bool {
	for _, t := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(lower, t) {
			return false
		}
	}
	return true
}

func ovWordStart(hay []rune, i int) bool {
	return i == 0 || !unicode.IsLetter(hay[i-1]) && !unicode.IsDigit(hay[i-1])
}

// ovTerm finds one term: a substring at a word start scores best, then any
// substring, then a subsequence with bonuses for word starts and adjacency.
func ovTerm(hay, term []rune) (int, []int, bool) {
	best, at := -1, -1
	for i := 0; i+len(term) <= len(hay); i++ {
		if !ovRunesEq(hay[i:i+len(term)], term) {
			continue
		}
		s := 60 + 4*len(term)
		if ovWordStart(hay, i) {
			s += 40
		}
		if i == 0 {
			s += 10
		}
		if s > best {
			best, at = s, i
		}
	}
	if at >= 0 {
		p := make([]int, len(term))
		for k := range term {
			p[k] = at + k
		}
		return best, p, true
	}
	var p []int
	s, j := 0, 0
	for i := 0; i < len(hay) && j < len(term); i++ {
		if hay[i] != term[j] {
			continue
		}
		s += 2
		if ovWordStart(hay, i) {
			s += 6
		}
		if len(p) > 0 && p[len(p)-1] == i-1 {
			s += 4
		}
		p = append(p, i)
		j++
	}
	if j < len(term) {
		return 0, nil, false
	}
	return s, p, true
}

func ovRunesEq(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

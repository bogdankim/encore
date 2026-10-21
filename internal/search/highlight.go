package search

import "github.com/bogdankim/encore/internal/library"

// Highlight returns match positions (rune indexes, as in the Hit types) of
// query's terms in each displayed field, for results that come from
// elsewhere, such as the Apple Music catalog. Each term is marked once, in
// the field where it matches best; earlier fields win ties. Terms that
// match nowhere are skipped.
func Highlight(query string, fields ...string) [][]int {
	out := make([][]int, len(fields))
	q := parseQuery(query)
	if q == nil {
		return out
	}
	folded := make([]string, len(fields))
	for i, f := range fields {
		folded[i] = library.Fold(f)
	}
	offs := make([][]int, len(fields))
	for _, t := range q.terms {
		bestF, bestPts, bestPos := -1, -1, -1
		for f, field := range folded {
			// earlier fields weigh more, like title over artist over album
			w := len(fields) - f
			if cls, at := classify(field, t); cls != classNone && classPoints[cls]*w > bestPts {
				bestF, bestPts, bestPos = f, classPoints[cls]*w, at
			}
		}
		if bestF < 0 {
			continue
		}
		for b := bestPos; b < bestPos+len(t); b++ {
			offs[bestF] = append(offs[bestF], b)
		}
	}
	for f, o := range offs {
		if len(o) > 0 {
			out[f] = runeIndexes(fields[f], folded[f], o)
		}
	}
	return out
}

package ui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
)

// whichKeyRow is a clickable line of the popup in screen coordinates.
type whichKeyRow struct {
	x0, x1, y int
	action    keymap.Action
}

// whichKeyGroups is the order groups appear in the popup: the leader is
// mostly a way into search, navigation and panes.
var whichKeyGroups = []string{
	keymap.GroupSearch, keymap.GroupGoTo, keymap.GroupPanes, keymap.GroupPlayback,
	keymap.GroupQueue, keymap.GroupLyrics, keymap.GroupNavigation, keymap.GroupGeneral,
}

// whichKeyGroup is the group a hint belongs to: its action's, or for a
// prefix the group its name matches. ok is false for a prefix with no
// matching group.
func whichKeyGroup(h keymap.Hint) (string, bool) {
	if h.Action != "" {
		if info, ok := keymap.Info(h.Action); ok {
			return info.Group, true
		}
	}
	for _, g := range whichKeyGroups {
		if strings.EqualFold(g, h.Label) {
			return g, true
		}
	}
	return "", false
}

// whichKeyOrder sorts hints by group, keeping the key order inside a group,
// with a nil between groups. A prefix without a group of its own joins the
// group of the first sibling that has one.
func whichKeyOrder(hints []keymap.Hint) []*keymap.Hint {
	fallback := keymap.GroupGeneral
	for _, h := range hints {
		if g, ok := whichKeyGroup(h); ok {
			fallback = g
			break
		}
	}
	groups := make([]string, len(hints))
	idx := make([]int, len(hints))
	for i, h := range hints {
		g, ok := whichKeyGroup(h)
		if !ok {
			g = fallback
		}
		groups[i], idx[i] = g, i
	}
	rank := func(g string) int {
		if r := slices.Index(whichKeyGroups, g); r >= 0 {
			return r
		}
		return len(whichKeyGroups)
	}
	slices.SortStableFunc(idx, func(a, b int) int { return rank(groups[a]) - rank(groups[b]) })
	out := make([]*keymap.Hint, 0, len(hints)+len(whichKeyGroups))
	for n, i := range idx {
		if n > 0 && groups[i] != groups[idx[n-1]] {
			out = append(out, nil)
		}
		out = append(out, &hints[i])
	}
	return out
}

// titleCase capitalises a prefix name the way the action names are
// written: "go to" → "Go to", "albums / artists" → "Albums / Artists".
// Short words stay lower case unless they come first (a prefix name runs
// on into "…", so its last word is not the end); words that already have a
// capital or a dot (AirPlay, last.fm) are kept.
func titleCase(s string) string {
	words := strings.Split(s, " ")
	for i, w := range words {
		if w == "" || strings.Contains(w, ".") || strings.ToLower(w) != w {
			continue
		}
		small := slices.Contains([]string{"a", "an", "and", "as", "at", "by", "for", "in", "of", "on", "or", "the", "to"}, w)
		if small && i > 0 {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// whichKeyLabel is a hint's label; prefix names get Title Case like the
// action labels.
func whichKeyLabel(h *keymap.Hint) string {
	if h.Group {
		return titleCase(h.Label)
	}
	return h.Label
}

// whichKey renders the popup for a pending sequence: the keys typed so far
// on the top border, then one row per next key, grouped (Search, Go To,
// Panes, Playback, General) with a blank row between groups. Prefixes end
// in a dim "…". It is anchored bottom-right above the footer.
func (a *App) whichKey(c *Ctx) (box string, x, y int, ok bool) {
	a.wkRows = a.wkRows[:0]
	r := a.pending
	if !r.Pending || len(r.Hints) == 0 {
		return "", 0, 0, false
	}
	p := paintsFor(c.Theme)
	g := c.Theme.G
	keyW, labelW := 1, 1
	for _, h := range r.Hints {
		keyW = max(keyW, ansi.StringWidth(h.Key))
		lw := ansi.StringWidth(whichKeyLabel(&h))
		if h.Group {
			lw += ansi.StringWidth(g.Ellipsis)
		}
		labelW = max(labelW, lw)
	}
	cellW := keyW + 2 + labelW
	bottom := a.h
	if !a.lay.Footer.Empty() {
		bottom = a.lay.Footer.Min.Y
	}
	maxRows := max(bottom-a.lay.Middle.Min.Y-2, 1)

	entries := whichKeyOrder(r.Hints)
	var cols [][]*keymap.Hint
	if len(entries) <= maxRows {
		cols = [][]*keymap.Hint{entries}
	} else {
		// No room for the gaps: drop them and wrap into even columns.
		flat := slices.DeleteFunc(entries, func(h *keymap.Hint) bool { return h == nil })
		n := (len(flat) + maxRows - 1) / maxRows
		per := (len(flat) + n - 1) / n
		for len(flat) > 0 {
			k := min(per, len(flat))
			cols = append(cols, flat[:k])
			flat = flat[k:]
		}
	}
	rows := 0
	for _, col := range cols {
		rows = max(rows, len(col))
	}
	// Two cells of padding on each side, so the longest label never runs
	// into the border.
	inner := len(cols)*cellW + (len(cols)-1)*3 + 4
	title := " " + r.Keys + " "
	inner = max(inner, ansi.StringWidth(title)+4)
	w, h := inner+2, rows+2
	if a.lay.Small || w > a.w || h > bottom {
		return "", 0, 0, false
	}
	x, y = max(a.w-w-1, 0), max(bottom-h, 0)

	border := func(s string) string { return p.paint(toneFaint, surfNone, s) }
	var b strings.Builder
	b.WriteString(border("╭─"))
	b.WriteString(p.paint(toneDim, surfNone, title))
	b.WriteString(border(strings.Repeat("─", max(inner-1-ansi.StringWidth(title), 0)) + "╮"))
	for row := 0; row < rows; row++ {
		b.WriteByte('\n')
		b.WriteString(border("│"))
		l := newLine(p, surfPanel, inner)
		for ci, col := range cols {
			if row >= len(col) || col[row] == nil {
				continue
			}
			hn := col[row]
			cx := 2 + ci*(cellW+3)
			l.to(cx)
			l.right(toneBold, hn.Key, keyW).pad(2)
			l.add(toneText, whichKeyLabel(hn))
			if hn.Group {
				l.add(toneDim, g.Ellipsis)
			}
			if hn.Action != "" {
				a.wkRows = append(a.wkRows, whichKeyRow{x + 1 + cx, x + 1 + cx + cellW, y + 1 + row, hn.Action})
			}
		}
		b.WriteString(l.String())
		b.WriteString(border("│"))
	}
	b.WriteByte('\n')
	b.WriteString(border("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String(), x, y, true
}

// whichKeyHit returns the action of the popup row under a click.
func (a *App) whichKeyHit(x, y int) keymap.Action {
	for _, r := range a.wkRows {
		if y == r.y && x >= r.x0 && x < r.x1 {
			return r.action
		}
	}
	return ""
}

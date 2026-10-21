package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Width is the display width of s, ignoring ANSI sequences.
func Width(s string) int { return ansi.StringWidth(s) }

// Fit truncates s (styled or not) to w cells with an ellipsis and pads it with
// spaces to exactly w cells.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = ansi.Truncate(s, w, "…")
		sw = ansi.StringWidth(s)
	}
	if sw < w {
		s += strings.Repeat(" ", w-sw)
	}
	return s
}

// FitRight is Fit with the text right-aligned.
func FitRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		return ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", w-sw) + s
}

// Center places s in the middle of w cells.
func Center(s string, w int) string {
	sw := ansi.StringWidth(s)
	if sw >= w {
		return Fit(s, w)
	}
	left := (w - sw) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-sw-left)
}

// Box forces a multi-line string to exactly w×h cells: each line is fitted to
// w, missing lines are blank and extra lines are dropped.
func Box(s string, w, h int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	var b strings.Builder
	for i := 0; i < h; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < len(lines) {
			b.WriteString(Fit(lines[i], w))
		} else {
			b.WriteString(strings.Repeat(" ", w))
		}
	}
	return b.String()
}

// Lines joins exactly-sized lines into a w×h block (see Box).
func Lines(lines []string, w, h int) string { return Box(strings.Join(lines, "\n"), w, h) }

// Blank is an empty w×h block.
func Blank(w, h int) string { return Box("", w, h) }

// HRule is a horizontal rule w cells wide.
func HRule(st lipgloss.Style, glyph string, w int) string {
	if w <= 0 {
		return ""
	}
	return st.Render(strings.Repeat(glyph, w))
}

// VRule is a vertical rule h cells tall.
func VRule(st lipgloss.Style, glyph string, h int) string {
	if h <= 0 {
		return ""
	}
	r := st.Render(glyph)
	return strings.TrimSuffix(strings.Repeat(r+"\n", h), "\n")
}

// Row lays out cells left to right with fixed widths; each cell is fitted to
// its width. gap spaces separate cells.
func Row(gap int, cells ...Cell) string {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 && gap > 0 {
			b.WriteString(strings.Repeat(" ", gap))
		}
		if c.Right {
			b.WriteString(FitRight(c.Text, c.W))
		} else {
			b.WriteString(Fit(c.Text, c.W))
		}
	}
	return b.String()
}

// Cell is one fixed-width column of a Row.
type Cell struct {
	Text  string
	W     int
	Right bool
}

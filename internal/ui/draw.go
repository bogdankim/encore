package ui

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Width is the display width of s, ignoring ANSI sequences.
func Width(s string) int {
	if w, ok := quickWidth(s); ok {
		return w
	}
	return ansi.StringWidth(s)
}

// quickWidth measures what nearly all of encore's lines are made of: printable
// ASCII, SGR and other CSI sequences, and glyphs known to take one cell
// (Latin letters with accents, box drawing, block elements, a few symbols).
// ansi.StringWidth walks grapheme clusters, which is several times slower and
// was a third of the time spent on a frame. ok is false for anything else
// (combining marks, wide or emoji characters, OSC sequences, controls), which
// Width leaves to ansi.StringWidth.
func quickWidth(s string) (w int, ok bool) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= 0x20 && c < 0x7f:
			w++
			i++
		case c == 0x1b:
			if i+1 >= len(s) || s[i+1] != '[' {
				return 0, false
			}
			i += 2
			for i < len(s) && s[i] >= 0x20 && s[i] < 0x40 { // parameters, intermediates
				i++
			}
			if i >= len(s) || s[i] < 0x40 || s[i] > 0x7e {
				return 0, false
			}
			i++
		case c < 0x80:
			return 0, false
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if !oneCell(r) {
				return 0, false
			}
			w++
			i += n
		}
	}
	return w, true
}

// oneCell reports whether r is known to be one cell wide and to combine with
// nothing.
func oneCell(r rune) bool {
	switch {
	case r >= 0xa1 && r <= 0x2ff && r != 0xad: // Latin-1 and Latin Extended, spacing modifiers
		return true
	case r >= 0x2010 && r <= 0x2027, r >= 0x2030 && r <= 0x205e: // punctuation
		return true
	case r >= 0x2190 && r <= 0x21ff, r >= 0x2500 && r <= 0x25ff: // arrows, box drawing, blocks, shapes
		return true
	case r == 0x2605, r == 0x2606, r == 0x2661, r == 0x2665, r == 0x266a, r == 0x266b:
		return true
	}
	return false
}

// Fit truncates s (styled or not) to w cells with an ellipsis and pads it with
// spaces to exactly w cells.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := Width(s)
	if sw > w {
		s = ansi.Truncate(s, w, "…")
		sw = Width(s)
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
	sw := Width(s)
	if sw > w {
		return ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", w-sw) + s
}

// Center places s in the middle of w cells.
func Center(s string, w int) string {
	sw := Width(s)
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
	if exactBox(s, w, h) {
		return s
	}
	return boxLines(strings.Split(s, "\n"), w, h, len(s))
}

// boxLines is Box for lines already split. size is a guess at the result's
// length, so the builder is allocated once.
func boxLines(lines []string, w, h, size int) string {
	if len(lines) > h {
		lines = lines[:h]
	}
	var b strings.Builder
	b.Grow(size + h*w/4)
	for i := 0; i < h; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < len(lines) {
			b.WriteString(Fit(lines[i], w))
		} else {
			writeSpaces(&b, w)
		}
	}
	return b.String()
}

func writeSpaces(b *strings.Builder, n int) {
	const spaces = "                                                                "
	for ; n > len(spaces); n -= len(spaces) {
		b.WriteString(spaces)
	}
	b.WriteString(spaces[:n])
}

// exactBox reports whether s is already h lines of w cells each, in which
// case Box has nothing to do. A frame passes through Box several times on its
// way up (the grid, the middle, the frame), and each pass used to copy it.
func exactBox(s string, w, h int) bool {
	for n := 0; ; n++ {
		line, rest, more := strings.Cut(s, "\n")
		if n >= h || Width(line) != w {
			return false
		}
		if !more {
			return n == h-1
		}
		s = rest
	}
}

// Lines joins exactly-sized lines into a w×h block (see Box).
func Lines(lines []string, w, h int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	size := 0
	for _, l := range lines {
		if strings.IndexByte(l, '\n') >= 0 {
			return Box(strings.Join(lines, "\n"), w, h) // a line that is several
		}
		size += len(l) + 1
	}
	return boxLines(lines, w, h, size)
}

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

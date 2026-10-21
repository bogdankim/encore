package ui

import (
	"image/color"
	"strings"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/theme"
)

// tone is a text style from the theme.
type tone uint8

const (
	toneText tone = iota
	toneBold
	toneDim
	toneFaint
	toneAccent
	toneAccentBold
	toneWarn
	numTones
)

// surface is a row background.
type surface uint8

const (
	surfNone surface = iota
	surfSel
	surfHover
	surfPanel
	numSurfaces
)

// paints holds the SGR prefix for every tone on every surface, so hot paths
// (table rows, the player bar) style text with one string concatenation
// instead of a lipgloss Render. Each painted segment carries its own
// background, which keeps row backgrounds unbroken across segments.
type paints struct {
	theme *theme.Theme
	pre   [numTones][numSurfaces]string
}

var paintCache atomic.Pointer[paints]

func paintsFor(t *theme.Theme) *paints {
	if p := paintCache.Load(); p != nil && p.theme == t {
		return p
	}
	p := &paints{theme: t}
	tones := [numTones]lipgloss.Style{t.Text, t.Bold, t.Dim, t.Faint, t.Accent, t.AccentB, t.Warn}
	surfs := [numSurfaces]lipgloss.Style{lipgloss.NewStyle(), t.Selected, t.Hovered, t.Surface}
	for i := range tones {
		for j := range surfs {
			p.pre[i][j] = sgr(tones[i], surfs[j])
		}
	}
	paintCache.Store(p)
	return p
}

func sgr(fg, bg lipgloss.Style) string {
	var s ansi.Style
	if fg.GetBold() {
		s = s.Bold()
	}
	if fg.GetFaint() {
		s = s.Faint()
	}
	if fg.GetUnderline() || bg.GetUnderline() {
		s = s.Underline(true)
	}
	if c := fg.GetForeground(); isColor(c) {
		s = s.ForegroundColor(c)
	}
	if c := bg.GetBackground(); isColor(c) {
		s = s.BackgroundColor(c)
	}
	if fg.GetReverse() || bg.GetReverse() {
		s = s.Reverse(true)
	}
	if len(s) == 0 {
		return ""
	}
	return s.String()
}

func isColor(c color.Color) bool {
	if c == nil {
		return false
	}
	_, none := c.(lipgloss.NoColor)
	return !none
}

// paint styles s. s is expected to be plain text already fitted to its cell.
func (p *paints) paint(t tone, sf surface, s string) string {
	pre := p.pre[t][sf]
	if pre == "" || s == "" {
		return s
	}
	return pre + s + ansi.ResetStyle
}

// spaces is n blank cells on a surface.
func (p *paints) spaces(sf surface, n int) string {
	if n <= 0 {
		return ""
	}
	return p.paint(toneText, sf, strings.Repeat(" ", n))
}

// truncate cuts s to w cells with an ellipsis, dropping spaces before the
// ellipsis so a cut never reads "Permanent, …".
func truncate(s string, w int) string {
	t := ansi.Truncate(s, w, "…")
	if strings.HasSuffix(t, " …") {
		t = strings.TrimRight(strings.TrimSuffix(t, "…"), " ") + "…"
	}
	return t
}

// line builds one row of exactly w cells out of painted segments on a single
// surface. Segments past w are truncated; the remainder is padded.
type line struct {
	p  *paints
	sf surface
	w  int
	n  int
	b  strings.Builder
}

// newLine starts a line of w cells; a negative w gives an empty line.
func newLine(p *paints, sf surface, w int) *line {
	w = max(w, 0)
	l := &line{p: p, sf: sf, w: w}
	l.b.Grow(w * 2)
	return l
}

// add appends s (plain text) in tone t, truncated to the space left.
func (l *line) add(t tone, s string) *line {
	left := l.w - l.n
	if left <= 0 || s == "" {
		return l
	}
	sw := ansi.StringWidth(s)
	if sw > left {
		s = truncate(s, left)
		sw = ansi.StringWidth(s)
	}
	l.b.WriteString(l.p.paint(t, l.sf, s))
	l.n += sw
	return l
}

// fit appends s fitted (truncated or padded) to exactly w cells.
func (l *line) fit(t tone, s string, w int) *line {
	w = min(w, l.w-l.n)
	if w <= 0 {
		return l
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = truncate(s, w)
		sw = ansi.StringWidth(s)
	}
	l.add(t, s)
	l.pad(w - sw)
	return l
}

// right appends s right-aligned in w cells.
func (l *line) right(t tone, s string, w int) *line {
	w = min(w, l.w-l.n)
	if w <= 0 {
		return l
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = truncate(s, w)
		sw = ansi.StringWidth(s)
	}
	l.pad(w - sw)
	l.add(t, s)
	return l
}

// pad appends n blank cells.
func (l *line) pad(n int) *line {
	n = min(n, l.w-l.n)
	if n > 0 {
		l.b.WriteString(l.p.spaces(l.sf, n))
		l.n += n
	}
	return l
}

// to pads up to column x.
func (l *line) to(x int) *line { return l.pad(x - l.n) }

// raw appends an already styled string of width sw.
func (l *line) raw(s string, sw int) *line {
	if sw > l.w-l.n {
		return l
	}
	l.b.WriteString(s)
	l.n += sw
	return l
}

func (l *line) col() int { return l.n }

func (l *line) String() string {
	l.pad(l.w - l.n)
	return l.b.String()
}

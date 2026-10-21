package ui

import (
	"image"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bogdankim/encore/internal/theme"
)

// Overlays share one look: a rounded faint border on the terminal's own
// background around a body painted on the Surface background, one cell of
// horizontal padding, content inside. Only the cells inside the border get
// the fill: filling the border cells too leaves a dark ring outside the
// line, which reads as a box in a box. A body line is "inner" wide (the
// overlay width minus the two border cells); list rows use the padding cells
// for the selection marker.

// ovSGR returns the escape sequence st opens with, or "" when it sets nothing.
func ovSGR(st lipgloss.Style) string {
	r := st.Render("x")
	if i := strings.IndexByte(r, 'x'); i > 0 {
		return r[:i]
	}
	return ""
}

// ovFill paints s with the attributes of seq (typically a background), re-opening
// them after every reset inside s so styled segments keep the background.
func ovFill(s, seq string) string {
	if seq == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[m")
	return seq + strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+seq) + "\x1b[m"
}

// ovFrame draws a w×h overlay around body, whose lines are fitted to the inner
// width w-2. Missing lines are blank, extra lines are dropped.
func ovFrame(t *theme.Theme, w, h int, body []string) string {
	if w < 2 || h < 2 {
		return Blank(w, h)
	}
	g := t.G
	bg := ovSGR(t.Surface)
	inner := w - 2
	lines := make([]string, 0, h)
	lines = append(lines, t.Border.Render("╭"+strings.Repeat(g.RuleH, inner)+"╮"))
	side := t.Border.Render("│")
	for i := 0; i < h-2; i++ {
		s := ""
		if i < len(body) {
			s = body[i]
		}
		lines = append(lines, side+ovFill(Fit(s, inner), bg)+side)
	}
	lines = append(lines, t.Border.Render("╰"+strings.Repeat(g.RuleH, inner)+"╯"))
	return strings.Join(lines, "\n")
}

// ovPad is a body line with s inside the padding: " s ".
func ovPad(s string, inner int) string {
	if inner < 2 {
		return Fit(s, inner)
	}
	return " " + Fit(s, inner-2) + " "
}

// ovRule is a faint rule across the content width.
func ovRule(t *theme.Theme, inner int) string {
	return ovPad(t.Faint.Render(strings.Repeat(t.G.RuleH, max(0, inner-2))), inner)
}

// ovRow is a selectable list row: the marker sits in the left padding cell and
// the selected or hovered background spans the whole inner width.
func ovRow(t *theme.Theme, content string, inner int, selected, hovered bool) string {
	if inner < 2 {
		return Fit(content, inner)
	}
	mark := " "
	if selected {
		mark = t.Accent.Render(t.G.Marker)
	}
	line := mark + Fit(content, inner-2) + " "
	switch {
	case selected:
		return ovFill(line, ovSGR(t.Selected))
	case hovered:
		return ovFill(line, ovSGR(t.Hovered))
	}
	return line
}

// ovBar is the scrollbar cell for row y of a view of h rows at offset off
// into total rows: a faint thumb, or a blank when everything fits.
func ovBar(t *theme.Theme, y, h, off, total int) string {
	if total <= h || h <= 0 {
		return " "
	}
	size := max(1, h*h/total)
	pos := off * (h - size) / max(1, total-h)
	if y >= pos && y < pos+size {
		return t.Faint.Render(t.G.ScrollThumb)
	}
	return " "
}

// ovHint is one key hint for the bottom row.
type ovHint struct{ Key, Label string }

// ovHints renders up to four hints as one line in the footer's style: keys
// in fg, labels dim, faint dots between ("enter play · esc close").
func ovHints(t *theme.Theme, inner int, hints ...ovHint) string {
	var parts []string
	for i, h := range hints {
		if i == 4 {
			break
		}
		parts = append(parts, t.Text.Render(h.Key)+" "+t.Dim.Render(h.Label))
	}
	return ovPad(strings.Join(parts, t.Faint.Render(" · ")), inner)
}

// ovCenterLine is a dim sentence centred in the content width.
func ovCenterLine(t *theme.Theme, s string, inner int) string {
	return ovPad(Center(t.Dim.Render(s), max(0, inner-2)), inner)
}

// ovPlace centres a w×h rectangle horizontally and puts its top at a third of
// the free space, where the eye lands first.
func ovPlace(screenW, screenH, w, h int) image.Rectangle {
	w, h = min(w, screenW), min(h, screenH)
	x := (screenW - w) / 2
	y := (screenH - h) / 3
	return image.Rect(x, y, x+w, y+h)
}

// ovSize scales a screen dimension by pct and clamps it to [lo, hi] and to
// the screen.
func ovSize(screen, pct, lo, hi int) int {
	return min(screen, max(lo, min(hi, screen*pct/100)))
}

// ovInput is a single-line text field with the overlay look.
type ovInput struct {
	m      textinput.Model
	styled *theme.Theme
}

func newOvInput(placeholder string) ovInput {
	m := textinput.New()
	m.Prompt = ""
	m.Placeholder = placeholder
	m.Focus()
	return ovInput{m: m}
}

func (in *ovInput) style(t *theme.Theme) {
	if in.styled == t {
		return
	}
	in.styled = t
	st := textinput.Styles{
		Focused: textinput.StyleState{Text: t.Text, Placeholder: t.Dim, Suggestion: t.Dim, Prompt: t.Dim},
		Blurred: textinput.StyleState{Text: t.Text, Placeholder: t.Dim, Suggestion: t.Dim, Prompt: t.Dim},
		Cursor:  textinput.CursorStyle{Color: t.P.FG, Shape: tea.CursorBar},
	}
	in.m.SetStyles(st)
}

// Value is the current text.
func (in *ovInput) Value() string { return in.m.Value() }

// SetValue replaces the text and puts the cursor at the end.
func (in *ovInput) SetValue(s string) {
	in.m.SetValue(s)
	in.m.CursorEnd()
}

// Key feeds a key press to the field and reports whether the text changed.
func (in *ovInput) Key(msg tea.KeyPressMsg) bool {
	before := in.m.Value()
	in.m, _ = in.m.Update(msg)
	return in.m.Value() != before
}

// Focus and Blur toggle the cursor.
func (in *ovInput) Focus() { in.m.Focus() }
func (in *ovInput) Blur()  { in.m.Blur() }

// Paste inserts pasted text at the cursor, as one line, and reports whether
// the text changed.
func (in *ovInput) Paste(s string) bool {
	s = singleLine(s)
	if s == "" {
		return false
	}
	before := in.m.Value()
	in.m, _ = in.m.Update(tea.PasteMsg{Content: s})
	return in.m.Value() != before
}

// View is the input line: a dim glyph, then the field, fitted to inner.
func (in *ovInput) View(t *theme.Theme, glyph string, inner int) string {
	in.style(t)
	w := max(1, inner-2-Width(glyph)-3)
	in.m.SetWidth(w)
	field := in.m.View()
	if in.m.Value() == "" && in.m.Placeholder != "" {
		// The text input draws its cursor over the placeholder's first
		// letter. Put it in front instead, so the whole hint stays legible.
		cur := ""
		if in.m.Focused() {
			cur = lipgloss.NewStyle().Foreground(t.P.FG).Reverse(true).Render(" ")
		}
		field = cur + Fit(t.Dim.Render(in.m.Placeholder), w-Width(cur))
	}
	return ovPad(t.Dim.Render(glyph)+"  "+field, inner)
}

// ovCursor is the selection, scroll offset and hover row of a list of n rows,
// some of which (headings, spacers) can't be selected.
type ovCursor struct {
	Sel, Off int
	Hover    int // -1 when the pointer is elsewhere
}

// Step moves the selection by d selectable rows, stopping at the ends.
func (c *ovCursor) Step(d, n int, ok func(int) bool) {
	i := c.Sel
	for d != 0 {
		dir := 1
		if d < 0 {
			dir = -1
		}
		j := i + dir
		for j >= 0 && j < n && !ok(j) {
			j += dir
		}
		if j < 0 || j >= n {
			break
		}
		i = j
		d -= dir
	}
	c.Sel = i
}

// First selects the first selectable row and scrolls to the top.
func (c *ovCursor) First(n int, ok func(int) bool) {
	c.Off = 0
	c.Sel = 0
	for c.Sel < n && !ok(c.Sel) {
		c.Sel++
	}
	if c.Sel >= n {
		c.Sel = 0
	}
}

// Follow scrolls so the selection is visible in a view of h rows. A heading
// right above the selection is kept in view too.
func (c *ovCursor) Follow(h, n int, ok func(int) bool) {
	if h <= 0 {
		return
	}
	top := c.Sel
	if top > 0 && !ok(top-1) {
		top--
	}
	if top < c.Off {
		c.Off = top
	}
	if c.Sel >= c.Off+h {
		c.Off = c.Sel - h + 1
	}
	c.Clamp(h, n)
}

// Scroll moves the view by d rows without touching the selection.
func (c *ovCursor) Scroll(d, h, n int) {
	c.Off += d
	c.Clamp(h, n)
}

// Clamp keeps the offset within the list.
func (c *ovCursor) Clamp(h, n int) {
	c.Off = max(0, min(c.Off, n-h))
}

// ovWheel is the scroll delta for a wheel event: three rows per notch.
func ovWheel(ev MouseEvent) int {
	switch ev.Button {
	case tea.MouseWheelUp:
		return -3
	case tea.MouseWheelDown:
		return 3
	}
	return 0
}

// ovClose closes the overlay, then calls then. The order matters for a host
// that acts at once: then may open another overlay.
func ovClose(c *Ctx, then func(h Host) tea.Cmd) tea.Cmd {
	closed := c.Host.CloseOverlay()
	return tea.Sequence(closed, then(c.Host))
}

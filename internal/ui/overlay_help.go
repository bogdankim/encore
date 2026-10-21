package ui

import (
	"image"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/theme"
)

type helpEntry struct{ keys, desc string }

type helpSection struct {
	name    string
	entries []helpEntry
}

// helpMouse lists the pointer gestures from docs/design.md, "Mouse".
var helpMouse = helpSection{name: "Mouse", entries: []helpEntry{
	{"click", "Select a row or open a sidebar item"},
	{"double-click", "Play or open"},
	{"right-click", "Context Menu"},
	{"wheel", "Scroll the pane under the pointer"},
	{"click header", "Sort, again to reverse"},
	{"click progress", "Seek, or drag"},
	{"volume", "Click, drag or wheel"},
	{"click title", "Go to Playing Album"},
	{"click lyric", "Seek to that line"},
	{"drag a rule", "Resize the pane"},
	{"click outside", "Close an overlay"},
}}

// helpSections collects the bindings of every pane context, grouped like the
// keymap catalogue, then the text-field keys and the mouse.
func helpSections(e *keymap.Engine) []helpSection {
	if e == nil {
		return []helpSection{helpMouse}
	}
	type acc struct {
		info keymap.ActionInfo
		keys []string
	}
	var order []keymap.Action
	byAction := map[keymap.Action]*acc{}
	for _, ctx := range []keymap.Context{keymap.Global, keymap.List, keymap.Sidebar, keymap.Lyrics, keymap.Queue} {
		for _, b := range e.Bindings(ctx) {
			if b.Context != ctx {
				continue
			}
			a := byAction[b.Action]
			if a == nil {
				a = &acc{info: b.ActionInfo}
				byAction[b.Action] = a
				order = append(order, b.Action)
			}
			for _, k := range b.Keys {
				if !slices.Contains(a.keys, k) {
					a.keys = append(a.keys, k)
				}
			}
		}
	}
	var out []helpSection
	for _, g := range keymap.Groups {
		sec := helpSection{name: g}
		for _, info := range keymap.Actions() {
			if a := byAction[info.Action]; a != nil && info.Group == g {
				sec.entries = append(sec.entries, helpEntry{ovKeys(a.keys...), info.Name})
			}
		}
		if len(sec.entries) > 0 {
			out = append(out, sec)
		}
	}
	input := helpSection{name: "Search and Filters"}
	for _, b := range e.Bindings(keymap.Input) {
		input.entries = append(input.entries, helpEntry{ovKeys(b.Keys...), b.Name})
	}
	if len(input.entries) > 0 {
		out = append(out, input)
	}
	return append(out, helpMouse)
}

// HelpOverlay lists every binding. Its filter field has the keyboard, as
// in the palette: typing filters, arrows and page keys scroll, esc clears
// the filter and then closes.
type HelpOverlay struct {
	in       ovInput
	sections []helpSection
	built    bool
	off      int

	w, h          int
	bodyH         int
	contentHeight int
}

// NewHelp opens the help overlay.
func NewHelp() Overlay {
	return &HelpOverlay{in: newOvInput("Type to filter")}
}

func (o *HelpOverlay) Rect(screenW, screenH int) image.Rectangle {
	w := ovSize(screenW, 80, 60, 140)
	h := ovSize(screenH, 80, 16, 60)
	x, y := (screenW-w)/2, (screenH-h)/2
	o.w, o.h = w, h
	return image.Rect(x, y, x+w, y+h)
}

// CapturesInput is always true: printable keys go to the filter, never to
// the app behind.
func (o *HelpOverlay) CapturesInput() bool { return true }

func (o *HelpOverlay) build(c *Ctx) {
	if !o.built {
		o.built = true
		o.sections = helpSections(c.Keys)
	}
}

func (o *HelpOverlay) scroll(d int) {
	o.off = max(0, min(o.off+d, o.contentHeight-o.bodyH))
}

func (o *HelpOverlay) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	page := max(1, o.bodyH-1)
	switch a {
	case keymap.NavDown, keymap.InputNext:
		o.scroll(n)
	case keymap.NavUp, keymap.InputPrev:
		o.scroll(-n)
	case keymap.NavHalfDown:
		o.scroll(n * max(1, o.bodyH/2))
	case keymap.NavHalfUp:
		o.scroll(-n * max(1, o.bodyH/2))
	case keymap.NavPageDown:
		o.scroll(n * page)
	case keymap.NavPageUp:
		o.scroll(-n * page)
	case keymap.NavTop:
		o.off = 0
	case keymap.NavBottom:
		o.scroll(o.contentHeight)
	case keymap.InputCancel, keymap.Back:
		if o.in.Value() != "" {
			o.in.SetValue("")
			o.off = 0
			return true, nil
		}
		return true, c.Host.CloseOverlay()
	case keymap.Help:
		return true, c.Host.CloseOverlay()
	}
	// enter, tab and everything else stay here: help is modal
	return true, nil
}

// Key takes the keys the Input keymap leaves: page keys scroll, the rest
// edit the filter.
func (o *HelpOverlay) Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	page := max(1, o.bodyH-1)
	switch msg.Code {
	case tea.KeyPgDown:
		o.scroll(page)
		return nil
	case tea.KeyPgUp:
		o.scroll(-page)
		return nil
	}
	if o.in.Key(msg) {
		o.off = 0
	}
	return nil
}

// Paste puts the pasted text in the filter.
func (o *HelpOverlay) Paste(text string, c *Ctx) tea.Cmd {
	if o.in.Paste(text) {
		o.off = 0
	}
	return nil
}

func (o *HelpOverlay) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	switch ev.Kind {
	case MouseWheel:
		o.scroll(ovWheel(ev))
	}
	return nil
}

func (o *HelpOverlay) Update(tea.Msg, *Ctx) tea.Cmd { return nil }

// filtered returns the sections with only the entries matching the filter.
func (o *HelpOverlay) filtered() []helpSection {
	q := strings.ToLower(strings.TrimSpace(o.in.Value()))
	if q == "" {
		return o.sections
	}
	var out []helpSection
	for _, s := range o.sections {
		sec := helpSection{name: s.name}
		whole := strings.Contains(strings.ToLower(s.name), q)
		for _, e := range s.entries {
			if whole || strings.Contains(strings.ToLower(e.desc), q) || strings.Contains(strings.ToLower(e.keys), q) {
				sec.entries = append(sec.entries, e)
			}
		}
		if len(sec.entries) > 0 {
			out = append(out, sec)
		}
	}
	return out
}

func (o *HelpOverlay) Render(c *Ctx) string {
	o.build(c)
	t := c.Theme
	w, h := o.w, o.h
	if w == 0 {
		w, h = 100, 30
	}
	inner, ih := w-2, h-2
	if inner < 6 || ih < 1 {
		return ovFrame(t, w, h, nil)
	}
	body := []string{o.in.View(t, t.G.Search, inner)}
	header := 1
	if ih >= 6 {
		body = append(body, "")
		header = 2
	}
	hints := ih >= 10
	o.bodyH = max(0, ih-header)
	if hints {
		o.bodyH -= 2
	}

	lines := o.layout(t, inner-3)
	o.contentHeight = len(lines)
	o.off = max(0, min(o.off, o.contentHeight-o.bodyH))
	if len(lines) == 0 && o.bodyH > 0 {
		body = append(body, "", ovCenterLine(t, "No matching keys", inner))
	}
	for y := 0; y < o.bodyH && o.off+y < len(lines); y++ {
		body = append(body, ovPad(lines[o.off+y], inner-1)+ovBar(t, y, o.bodyH, o.off, len(lines)))
	}
	if hints {
		for len(body) < ih-1 {
			body = append(body, "")
		}
		if o.in.Value() != "" {
			body = append(body, ovHints(t, inner, ovHint{"↑↓", "scroll"}, ovHint{"esc", "clear"}))
		} else {
			body = append(body, ovHints(t, inner, ovHint{"↑↓", "scroll"}, ovHint{"esc", "close"}))
		}
	}
	return ovFrame(t, w, h, body)
}

const (
	helpGutter = 6
	helpKeyW   = 16 // longer key lists push their description right
)

// layout renders the sections into content-width lines, in two columns when
// there is room.
func (o *HelpOverlay) layout(t *theme.Theme, cw int) []string {
	secs := o.filtered()
	if len(secs) == 0 {
		return nil
	}
	if cw < 2*36+helpGutter {
		return helpColumn(t, secs, cw)
	}
	colW := (cw - helpGutter) / 2
	split := helpSplit(secs)
	left := helpColumn(t, secs[:split], colW)
	right := helpColumn(t, secs[split:], colW)
	n := max(len(left), len(right))
	out := make([]string, n)
	gap := strings.Repeat(" ", helpGutter)
	for i := range n {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = Fit(l, colW) + gap + r
	}
	return out
}

func helpHeight(s helpSection) int { return len(s.entries) + 2 }

// helpSplit picks where the second column starts so both are as even as the
// section order allows.
func helpSplit(secs []helpSection) int {
	total := 0
	for _, s := range secs {
		total += helpHeight(s)
	}
	best, bestH, run := len(secs), total, 0
	for i, s := range secs {
		if h := max(run, total-run); h < bestH && i > 0 {
			best, bestH = i, h
		}
		run += helpHeight(s)
	}
	return best
}

func helpColumn(t *theme.Theme, secs []helpSection, w int) []string {
	keyW := 0
	for _, s := range secs {
		for _, e := range s.entries {
			keyW = max(keyW, Width(e.keys))
		}
	}
	keyW = min(keyW, helpKeyW)
	var out []string
	for i, s := range secs {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, t.Bold.Render(s.name))
		for _, e := range s.entries {
			keys := e.keys + strings.Repeat(" ", max(0, keyW-Width(e.keys)))
			out = append(out, Fit(t.Text.Render(keys)+"  "+t.Dim.Render(e.desc), w))
		}
	}
	return out
}

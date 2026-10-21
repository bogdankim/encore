package ui

import (
	"image"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
)

// paletteHidden are actions that make no sense from a palette: motions, the
// text-field keys, and the palette itself.
func paletteHidden(a keymap.Action) bool {
	s := string(a)
	return strings.HasPrefix(s, "nav.") || strings.HasPrefix(s, "input.") || strings.HasPrefix(s, "focus.") ||
		a == keymap.Open || a == keymap.Back || a == keymap.Palette
}

// paletteGroups orders the groups by how often a palette is the way in.
var paletteGroups = []string{
	keymap.GroupPlayback, keymap.GroupSearch, keymap.GroupGoTo, keymap.GroupPanes,
	keymap.GroupQueue, keymap.GroupLyrics, keymap.GroupNavigation, keymap.GroupGeneral,
}

type paletteItem struct {
	action keymap.Action
	desc   string
	group  string
	keys   string
}

type paletteRow struct {
	heading string
	item    int // index into items; -1 for headings and spacers
	pos     []int
}

// PaletteOverlay is the command palette: every action, fuzzy-filtered by name.
type PaletteOverlay struct {
	in    ovInput
	items []paletteItem
	rows  []paletteRow
	cur   ovCursor
	built bool

	w, h         int
	fullH        int // the height before shrinking to the matches
	listY, listH int
}

// NewPalette opens the command palette.
func NewPalette() Overlay {
	return &PaletteOverlay{in: newOvInput("Type a command"), cur: ovCursor{Hover: -1}}
}

// Rect is the full-height box while every command shows, and shrinks to the
// matches as the filter narrows them, keeping its top where it was so the
// field doesn't move.
func (p *PaletteOverlay) Rect(screenW, screenH int) image.Rectangle {
	full := ovSize(screenH, 60, 12, 30)
	r := ovPlace(screenW, screenH, ovSize(screenW, 55, 56, 84), full)
	p.fullH = r.Dy()
	if p.built {
		n := max(len(p.rows), paletteMinRows)
		r.Max.Y = r.Min.Y + min(r.Dy(), 2+2+n+2) // border, field and rule, rows, hints
	}
	p.w, p.h = r.Dx(), r.Dy()
	return r
}

// paletteMinRows is the fewest list rows the palette shrinks to.
const paletteMinRows = 3

func (p *PaletteOverlay) CapturesInput() bool { return true }

func (p *PaletteOverlay) build(c *Ctx) {
	if p.built {
		return
	}
	p.built = true
	actions := keymap.Actions()
	slices.SortStableFunc(actions, func(a, b keymap.ActionInfo) int {
		return slices.Index(paletteGroups, a.Group) - slices.Index(paletteGroups, b.Group)
	})
	for _, info := range actions {
		if paletteHidden(info.Action) {
			continue
		}
		desc := info.Name
		var keys []string
		if c.Keys != nil {
			keys = c.Keys.KeysFor(info.Action)
		}
		if len(keys) > 2 {
			keys = keys[:2]
		}
		p.items = append(p.items, paletteItem{
			action: info.Action, desc: desc, group: info.Group,
			keys: ovKeys(keys...),
		})
	}
	p.filter()
}

func (p *PaletteOverlay) filter() {
	p.rows = p.rows[:0]
	q := strings.TrimSpace(p.in.Value())
	if q == "" {
		group := ""
		for i, it := range p.items {
			if it.group != group {
				if group != "" {
					p.rows = append(p.rows, paletteRow{item: -1})
				}
				group = it.group
				p.rows = append(p.rows, paletteRow{heading: group, item: -1})
			}
			p.rows = append(p.rows, paletteRow{item: i})
		}
	} else {
		type scored struct {
			row    paletteRow
			score  int
			strict bool
		}
		var hits []scored
		strict := false
		for i, it := range p.items {
			sc, pos, ok := ovFuzzy(it.desc, q)
			// every term inside the name, or in the action's id or group
			st := ovContainsAll(strings.ToLower(it.desc), q) ||
				ovContainsAll(strings.ToLower(string(it.action)+" "+it.group), q)
			if ok || st {
				hits = append(hits, scored{paletteRow{item: i, pos: pos}, sc, st})
				strict = strict || st
			}
		}
		if strict {
			// letters scattered through a name ("air" in "Back in
			// History") only count when nothing has the text itself
			hits = slices.DeleteFunc(hits, func(h scored) bool { return !h.strict })
		}
		slices.SortStableFunc(hits, func(a, b scored) int { return b.score - a.score })
		for _, h := range hits {
			p.rows = append(p.rows, h.row)
		}
	}
	p.cur.First(len(p.rows), p.ok)
}

func (p *PaletteOverlay) ok(i int) bool { return p.rows[i].item >= 0 }

func (p *PaletteOverlay) move(d int) {
	p.cur.Step(d, len(p.rows), p.ok)
	p.cur.Follow(p.listH, len(p.rows), p.ok)
}

func (p *PaletteOverlay) run(i int, c *Ctx) tea.Cmd {
	if i < 0 || i >= len(p.rows) || !p.ok(i) {
		return nil
	}
	a := p.items[p.rows[i].item].action
	return ovClose(c, func(h Host) tea.Cmd { return h.Do(a) })
}

func (p *PaletteOverlay) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	p.build(c)
	switch a {
	case keymap.InputNext, keymap.NavDown:
		p.move(n)
	case keymap.InputPrev, keymap.NavUp:
		p.move(-n)
	case keymap.InputSubmit, keymap.Open:
		return true, p.run(p.cur.Sel, c)
	case keymap.InputCancel, keymap.Back:
		return true, c.Host.CloseOverlay()
	case keymap.InputNextScope, keymap.InputPrevScope:
	default:
		return false, nil
	}
	return true, nil
}

func (p *PaletteOverlay) Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	p.build(c)
	if p.in.Key(msg) {
		p.filter()
	}
	return nil
}

// Paste puts pasted text in the filter.
func (p *PaletteOverlay) Paste(text string, c *Ctx) tea.Cmd {
	p.build(c)
	if p.in.Paste(text) {
		p.filter()
	}
	return nil
}

func (p *PaletteOverlay) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	row := -1
	if y := ev.Y - p.listY; y >= 0 && y < p.listH && p.cur.Off+y < len(p.rows) {
		row = p.cur.Off + y
	}
	switch ev.Kind {
	case MouseMotion:
		p.cur.Hover = -1
		if row >= 0 && p.ok(row) {
			p.cur.Hover = row
		}
	case MouseWheel:
		p.cur.Scroll(ovWheel(ev), p.listH, len(p.rows))
	case MousePress:
		if row >= 0 && ev.Button == tea.MouseLeft {
			return p.run(row, c)
		}
	}
	return nil
}

func (p *PaletteOverlay) Update(tea.Msg, *Ctx) tea.Cmd { return nil }

func (p *PaletteOverlay) Render(c *Ctx) string {
	p.build(c)
	t := c.Theme
	w, h := p.w, p.h
	if w == 0 {
		w, h = 70, 20
	}
	inner, ih := w-2, h-2
	if inner < 4 || ih < 1 {
		return ovFrame(t, w, h, nil)
	}
	full := max(h, p.fullH) - 2
	body := []string{p.in.View(t, ":", inner)}
	header := 1
	if full >= 6 {
		body = append(body, ovRule(t, inner))
		header = 2
	}
	hints := full >= 10 && ih >= header+1+2
	p.listY = 1 + header
	p.listH = max(0, ih-header)
	if hints {
		p.listH -= 2
	}
	p.cur.Clamp(p.listH, len(p.rows))

	cw := inner - 3
	for y := 0; y < p.listH && p.cur.Off+y < len(p.rows); y++ {
		i := p.cur.Off + y
		r := p.rows[i]
		bar := ovBar(t, y, p.listH, p.cur.Off, len(p.rows))
		switch {
		case r.heading != "":
			body = append(body, ovPad(t.Dim.Render(r.heading), inner-1)+bar)
		case r.item < 0:
			body = append(body, ovPad("", inner-1)+bar)
		default:
			it := p.items[r.item]
			keys := t.Dim.Render(it.keys)
			kw := min(Width(it.keys), cw/3)
			line := Row(2, Cell{Text: ovMark(it.desc, r.pos, t.Text, t.Bold), W: cw - kw - 2}, Cell{Text: keys, W: kw, Right: true})
			body = append(body, ovRow(t, line, inner-1, i == p.cur.Sel, i == p.cur.Hover)+bar)
		}
	}
	if len(p.rows) == 0 && p.listH > 0 {
		for range (p.listH - 1) / 2 {
			body = append(body, "")
		}
		body = append(body, ovCenterLine(t, "No matching commands", inner))
	}
	if hints {
		for len(body) < ih-1 {
			body = append(body, "")
		}
		body = append(body, ovHints(t, inner, ovHint{"↑↓", "select"}, ovHint{"enter", "run"}, ovHint{"esc", "close"}))
	}
	return ovFrame(t, w, h, body)
}

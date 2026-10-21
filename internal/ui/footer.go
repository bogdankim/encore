package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
)

// maxHints is the most hints the footer shows: up to four of the focused
// view's or pane's, then the leader and help.
const maxHints = 6

type hint struct{ keys, label string }

// FooterHint is one key hint in the footer: the first key bound to Action
// (or Keys, when set) and a short label ("enter open").
type FooterHint struct {
	Action keymap.Action
	Keys   string // shown instead of Action's key, e.g. "esc"
	Label  string
}

// footerHinter is implemented by views whose keys differ from the default
// content hints (move, play, filter), and by right panes. The app appends
// "space commands" and "? help" itself.
type footerHinter interface {
	FooterHints(c *Ctx) []FooterHint
}

// Hint names a control while the pointer rests on it: the footer's left
// slot shows text ("Lyrics  space l"). Call it from Mouse on every motion
// event over the control. The app clears the hint before each motion event,
// so moving off the control clears it without a call.
func (c *Ctx) Hint(text string) {
	if c != nil && c.hint != nil {
		c.hint(text)
	}
}

// HintFor is the hint for a control named name that act also runs: the
// name, two spaces and act's first key ("Shuffle  s"). With no key bound,
// or act "", it is the name alone.
func (c *Ctx) HintFor(name string, act keymap.Action) string {
	return hintText(c.Keys, name, act)
}

func hintText(keys *keymap.Engine, name string, act keymap.Action) string {
	if act == "" || keys == nil {
		return name
	}
	if ks := keys.KeysFor(act); len(ks) > 0 {
		return name + "  " + ks[0]
	}
	return name
}

// keysJoin is the first key of each of acts, joined by sep ("{ }"). Acts
// with no key bound are left out.
func keysJoin(c *Ctx, sep string, acts ...keymap.Action) string {
	if c == nil || c.Keys == nil {
		return ""
	}
	var ks []string
	for _, a := range acts {
		if k := c.Keys.KeysFor(a); len(k) > 0 {
			ks = append(ks, k[0])
		}
	}
	return strings.Join(ks, sep)
}

func (a *App) hintText(name string, act keymap.Action) string { return hintText(a.keys, name, act) }

// actionName is act's name in the help screen and the palette, so a hover
// hint calls a control what the rest of encore calls it.
func actionName(act keymap.Action) string { return keymap.Name(act) }

// actHint is the hover hint for a control that runs act: its name and key.
func (a *App) actHint(act keymap.Action) string { return a.hintText(actionName(act), act) }

// keysHint is name, two spaces and the first key of each of acts, joined by
// spaces ("Seek  , ."); keys not bound are left out.
func (a *App) keysHint(name string, acts ...keymap.Action) string {
	var ks []string
	for _, act := range acts {
		if k := a.keys.KeysFor(act); len(k) > 0 {
			ks = append(ks, k[0])
		}
	}
	if len(ks) == 0 {
		return name
	}
	return name + "  " + strings.Join(ks, " ")
}

func (a *App) setHint(text string) { a.hint = text }

// renderFooter is the last row: the hovered control's name or the status
// on the left, up to five hints for the
// focused context on the right.
func (a *App) renderFooter(c *Ctx) string {
	p := paintsFor(c.Theme)
	w := a.w
	l := newLine(p, surfNone, w)

	left, leftTone := a.footerLeft()

	hints := a.footerHints()
	sep := " · "
	hw := func(hs []hint) int {
		n := 0
		for i, h := range hs {
			if i > 0 {
				n += ansi.StringWidth(sep)
			}
			n += ansi.StringWidth(h.keys) + 1 + ansi.StringWidth(h.label)
		}
		return n
	}
	room := w - 2
	if left != "" {
		room -= min(ansi.StringWidth(left), w/2) + 4
	}
	for len(hints) > 0 && hw(hints) > room {
		hints = hints[:len(hints)-1]
	}

	l.pad(1)
	if left != "" {
		l.fit(leftTone, left, w-2-hw(hints)-boolInt(len(hints) > 0)*4)
	}
	if len(hints) > 0 {
		l.to(w - 1 - hw(hints))
		for i, h := range hints {
			if i > 0 {
				l.add(toneFaint, sep)
			}
			l.add(toneText, h.keys).pad(1).add(toneDim, h.label)
		}
	}
	return l.String()
}

// footerLeft is the footer's left slot: the hovered control's name and
// key, else the status.
func (a *App) footerLeft() (string, tone) {
	switch {
	case a.pending.Pending:
		// the which-key popup shows the keys typed so far
	case a.hint != "" && len(a.overlays) == 0:
		return a.hint, toneText
	case a.status.text != "":
		if a.status.err {
			return a.status.text, toneWarn
		}
		return a.status.text, toneDim
	case a.status.sticky != "":
		return a.status.sticky, toneWarn
	}
	return "", toneDim
}

// ruleTail is the rule cells kept right of a note in the bar's rule.
const ruleTail = 2

// renderBarRule is the rule under the player bar. While the footer is
// hidden (under 24 rows) the rule's right end carries the footer's left
// slot, so the pointer still names controls and statuses still show:
// "──────── Shuffle: Off  s ──". The rule is there at every bar height and
// is the one row no view or pane draws into.
func (a *App) renderBarRule(c *Ctx) string {
	p := paintsFor(c.Theme)
	g := c.Theme.G.RuleH
	text, tn := "", toneDim
	if a.lay.Footer.Empty() {
		text, tn = a.footerLeft()
	}
	room := a.w - ruleTail - 2 - 4 // a few rule cells stay on the left
	if text == "" || room < minCut {
		return p.paint(toneFaint, surfNone, strings.Repeat(g, a.w))
	}
	text = truncWord(text, room)
	head := a.w - ruleTail - 2 - ansi.StringWidth(text)
	return p.paint(toneFaint, surfNone, strings.Repeat(g, head)) +
		p.paint(tn, surfNone, " "+text+" ") +
		p.paint(toneFaint, surfNone, strings.Repeat(g, ruleTail))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// openKeys are the sections whose enter opens an album or artist rather
// than playing a song.
var openKeys = map[string]bool{KeyRecent: true, KeyAlbums: true, KeyArtists: true}

// footerHints are the most useful keys for what has focus. None while an
// overlay is open: overlays show their own.
func (a *App) footerHints() []hint {
	top := a.topOverlay()
	if _, ok := top.(footerHinter); top != nil && !ok {
		return nil
	}
	key := func(act keymap.Action) string {
		if ks := a.keys.KeysFor(act); len(ks) > 0 {
			return ks[0]
		}
		return ""
	}
	pair := func(x, y keymap.Action) string {
		kx, ky := key(x), key(y)
		if kx == "" || ky == "" {
			return kx + ky
		}
		return kx + "/" + ky
	}
	var hs []hint
	addh := func(keys, label string) {
		if keys != "" {
			hs = append(hs, hint{keys, label})
		}
	}
	if a.keyContext() == keymap.Input {
		addh("enter", "keep filter")
		addh("esc", "clear")
		return hs
	}
	from := func(fh footerHinter, c *Ctx) {
		own := fh.FooterHints(c)
		for _, h := range own[:min(len(own), maxHints-2)] {
			k := h.Keys
			if k == "" {
				k = key(h.Action)
			}
			addh(k, h.Label)
		}
	}
	switch fh, _ := top.(footerHinter); {
	case fh != nil:
		// an overlay that is a pane in another place (the Up Next drawer)
		from(fh, a.overlayCtx(top))
	case a.focus == focusSidebar:
		addh(pair(keymap.NavDown, keymap.NavUp), "move")
		addh(key(keymap.Open), "open")
	case a.focus == focusRight:
		if fh, ok := a.pane().(footerHinter); ok {
			from(fh, a.ctxFor(focusRight, a.lay.RightBody.Min))
			break
		}
		addh(pair(keymap.NavDown, keymap.NavUp), "scroll")
		addh(key(keymap.FocusLeft), "back")
	default:
		if fh, ok := a.nav.cur.(footerHinter); ok {
			from(fh, a.viewCtx())
			break
		}
		addh(pair(keymap.NavDown, keymap.NavUp), "move")
		open := "play"
		if a.nav.cur != nil && openKeys[a.nav.cur.Key()] {
			open = "open"
		}
		addh(key(keymap.Open), open)
		if f, ok := a.nav.cur.(interface{ filtering() bool }); ok && f.filtering() {
			addh("esc", "clear filter")
		} else {
			addh(key(keymap.Filter), "filter")
		}
	}
	addh(keymap.DisplayToken(a.keys.Leader()), "commands")
	addh(key(keymap.Help), "help")
	if len(hs) > maxHints {
		hs = hs[:maxHints]
	}
	return hs
}

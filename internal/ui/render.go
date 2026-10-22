package ui

import (
	"image"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// View renders the frame.
func (a *App) View() tea.View {
	if v, ok := a.gate.take(); ok {
		return v
	}
	v := a.buildView()
	a.gate.built(v, time.Now())
	return v
}

func (a *App) buildView() tea.View {
	s := a.render()
	a.commitArt(s)
	v := tea.NewView(s)
	v.AltScreen = true
	v.ReportFocus = true
	if !a.o.NoMouse {
		v.MouseMode = tea.MouseModeAllMotion
	}
	v.WindowTitle = a.windowTitle()
	return v
}

func (a *App) windowTitle() string {
	if ct := a.snap.NowPlaying.Track; ct != nil {
		return joinMeta(ct.Name, ct.Artist)
	}
	return "encore"
}

func (a *App) render() string {
	a.frameArt, a.frameCovers = a.frameArt[:0], a.frameCovers[:0]
	a.art.frame()
	a.backOn = false
	if a.w <= 0 || a.h <= 0 {
		return ""
	}
	l := a.lay
	c := a.baseCtx()
	p := paintsFor(c.Theme)
	if l.Small {
		lines := make([]string, a.h)
		for i := range lines {
			lines[i] = strings.Repeat(" ", a.w)
		}
		lines[a.h/2] = Center(p.paint(toneDim, surfNone, "encore needs a bigger window"), a.w)
		return strings.Join(lines, "\n")
	}

	// Where the overlays go is known before the frame under them is drawn,
	// so grid tiles they touch can skip their images (see tileArt).
	wk := a.overlayCovers(c)
	if k := a.screenOverlay(); k >= 0 {
		// it hides the frame and the overlays under it: skip drawing them
		// (full-screen lyrics redraw up to 30 times a second)
		return a.composite("", c, wk, k)
	}
	rows := make([]string, 0, a.h)
	rows = append(rows, a.renderBar(a.w, l.Bar.Dy(), c)...)
	rows = append(rows, a.renderBarRule(c))
	rows = append(rows, a.renderMiddle(c)...)
	if !l.Footer.Empty() {
		rows = append(rows, a.renderFooter(c))
	}
	return a.composite(strings.Join(rows, "\n"), c, wk, 0)
}

// screenOverlay is the index of the topmost overlay that covers the whole
// screen, or -1.
func (a *App) screenOverlay() int {
	full := image.Rect(0, 0, a.w, a.h)
	for i := len(a.overlays) - 1; i >= 0; i-- {
		if a.overlays[i].Rect(a.w, a.h) == full {
			return i
		}
	}
	return -1
}

func (a *App) renderMiddle(c *Ctx) []string {
	l := a.lay
	h := l.Middle.Dy()
	p := paintsFor(c.Theme)
	out := make([]strings.Builder, h)
	add := func(body string, w, padTop, padL, padR int) {
		lines := strings.Split(body, "\n")
		for i := range out {
			if i < padTop {
				out[i].WriteString(strings.Repeat(" ", w))
				continue
			}
			out[i].WriteString(strings.Repeat(" ", padL))
			if j := i - padTop; j < len(lines) {
				out[i].WriteString(lines[j])
			} else {
				out[i].WriteString(strings.Repeat(" ", w-padL-padR))
			}
			out[i].WriteString(strings.Repeat(" ", padR))
		}
	}
	rule := p.paint(toneFaint, surfNone, c.Theme.G.RuleV)
	addRule := func() {
		for i := range out {
			out[i].WriteString(rule)
		}
	}
	active := ""
	if a.nav.cur != nil {
		active = a.nav.cur.Key()
	}

	switch {
	case l.SidebarOn:
		b := l.SidebarBody
		sc := a.ctxFor(focusSidebar, b.Min)
		add(a.sidebar.render(b.Dx(), b.Dy(), sc.Focused, active, sc), l.Sidebar.Dx(), middlePadTop, 0, 0)
		addRule()
	case l.RailOn:
		b := l.RailBody
		add(a.sidebar.renderRail(b.Dy(), active, c), l.Rail.Dx(), middlePadTop, 0, 0)
		addRule()
	}
	if l.ContentOn {
		b := l.ContentBody
		body := Blank(b.Dx(), b.Dy())
		if a.nav.cur != nil && b.Dx() > 0 && b.Dy() > 0 {
			body = Box(a.nav.cur.Render(b.Dx(), b.Dy(), a.viewCtx()), b.Dx(), b.Dy())
		}
		add(body, l.Content.Dx(), middlePadTop, contentPadLeft, contentPadRight)
	}
	if l.RightOn {
		if !l.RightRule.Empty() {
			addRule()
		}
		b := l.RightBody
		body := Blank(b.Dx(), b.Dy())
		if pane := a.pane(); pane != nil && b.Dx() > 0 && b.Dy() > 0 {
			body = Box(pane.Render(b.Dx(), b.Dy(), a.ctxFor(focusRight, b.Min)), b.Dx(), b.Dy())
		}
		add(body, l.Right.Dx(), middlePadTop, rightPadLeft, rightPadRight)
	}
	lines := make([]string, h)
	for i := range out {
		lines[i] = out[i].String()
	}
	return lines
}

// whichKeyBox is the which-key popup as drawn this frame.
type whichKeyBox struct {
	box  string
	x, y int
	ok   bool
}

// overlayGap is the blank column kept between an overlay that sits against
// one side of the screen (a drawer) and the frame beside it, so the frame's
// art and text don't run into its rule. It is empty for other overlays.
func overlayGap(r image.Rectangle, screenW int) image.Rectangle {
	switch {
	case r.Min.X == 0 && r.Max.X < screenW:
		return image.Rect(r.Max.X, r.Min.Y, r.Max.X+1, r.Max.Y)
	case r.Max.X == screenW && r.Min.X > 0:
		return image.Rect(r.Min.X-1, r.Min.Y, r.Min.X, r.Max.Y)
	}
	return image.Rectangle{}
}

// overlayCovers records the cells the overlays, their gaps and the which-key
// popup paint this frame, as covers over the art below them, and lays out
// the popup.
func (a *App) overlayCovers(c *Ctx) whichKeyBox {
	for i, o := range a.overlays {
		r := o.Rect(a.w, a.h)
		if r.Empty() {
			continue
		}
		a.frameCovers = append(a.frameCovers, artCover{rect: r, layer: i + 1, overlay: true})
		if g := overlayGap(r, a.w); !g.Empty() {
			a.frameCovers = append(a.frameCovers, artCover{rect: g, layer: i + 1, overlay: true})
		}
	}
	var wk whichKeyBox
	wk.box, wk.x, wk.y, wk.ok = a.whichKey(c)
	if wk.ok {
		r := image.Rect(wk.x, wk.y, wk.x+lipgloss.Width(wk.box), wk.y+lipgloss.Height(wk.box))
		a.frameCovers = append(a.frameCovers, artCover{rect: r, layer: len(a.overlays) + 1, overlay: true})
	}
	return wk
}

// composite draws the overlays from index from up and the which-key popup
// over the base frame. With no base frame, the first of those overlays
// covers the screen and is the base. Their covers were recorded by
// overlayCovers.
func (a *App) composite(base string, c *Ctx, wk whichKeyBox, from int) string {
	var layers []*lipgloss.Layer
	for i, o := range a.overlays[from:] {
		i += from
		r := o.Rect(a.w, a.h)
		if r.Empty() {
			continue
		}
		s := Box(o.Render(a.overlayCtx(o)), r.Dx(), r.Dy())
		if base == "" {
			base = s
			continue
		}
		layers = append(layers, lipgloss.NewLayer(s).X(r.Min.X).Y(r.Min.Y).Z(i+1))
		if g := overlayGap(r, a.w); !g.Empty() {
			layers = append(layers, lipgloss.NewLayer(Blank(g.Dx(), g.Dy())).X(g.Min.X).Y(g.Min.Y).Z(i+1))
		}
	}
	if wk.ok {
		layers = append(layers, lipgloss.NewLayer(wk.box).X(wk.x).Y(wk.y).Z(len(a.overlays)+1))
	}
	if len(layers) == 0 {
		return base
	}
	layers = append([]*lipgloss.Layer{lipgloss.NewLayer(base)}, layers...)
	// the compositor trims trailing blanks; keep the frame exactly w×h
	return Box(lipgloss.NewCompositor(layers...).Render(), a.w, a.h)
}

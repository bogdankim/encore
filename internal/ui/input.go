package ui

import (
	"image"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
)

// keyContext is the keymap context for the current focus: Input while the
// top overlay has a text field focused, otherwise the focused pane's.
func (a *App) keyContext() keymap.Context {
	if o := a.topOverlay(); o != nil && o.CapturesInput() {
		return keymap.Input
	}
	if o, ok := a.topOverlay().(interface{ KeyContext() keymap.Context }); ok {
		return o.KeyContext()
	}
	switch a.focus {
	case focusSidebar:
		return keymap.Sidebar
	case focusRight:
		if p, ok := a.pane().(interface{ KeyContext() keymap.Context }); ok {
			return p.KeyContext()
		}
		return keymap.Lyrics
	}
	if a.topOverlay() == nil {
		if iv, ok := a.nav.cur.(inputView); ok && iv.CapturesInput() {
			return keymap.Input
		}
	}
	return keymap.List
}

func (a *App) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	ctx := a.keyContext()
	r := a.keys.Feed(ctx, keymap.Token(msg))
	a.pending = keymap.Result{}
	if o := a.topOverlay(); r.Pending && (o == nil || passesKeys(o)) {
		// no which-key over a modal popup: its keys don't reach the app
		a.pending = r
	}
	switch {
	case r.Action != "":
		return a.dispatch(r.Action, r.Count)
	case r.Ambiguous:
		return a.keys.TimeoutCmd()
	case !r.Consumed && (ctx == keymap.Input || a.topOverlay() != nil):
		return a.inputKey(msg)
	}
	return nil
}

// inputKey hands an unbound key to the top overlay, or to the text field
// that has focus.
func (a *App) inputKey(msg tea.KeyPressMsg) tea.Cmd {
	if o := a.topOverlay(); o != nil {
		return o.Key(msg, a.overlayCtx(o))
	}
	if iv, ok := a.nav.cur.(inputView); ok {
		return iv.InputKey(msg, a.viewCtx())
	}
	return nil
}

// overlayPaster is an overlay with a text field that takes pasted text.
type overlayPaster interface {
	Paste(text string, c *Ctx) tea.Cmd
}

// handlePaste gives pasted text to the top overlay only: an overlay below it
// has no focus. Overlays without a Paste method get the message in Update.
func (a *App) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if o := a.topOverlay(); o != nil {
		c := a.overlayCtx(o)
		if p, ok := o.(overlayPaster); ok {
			return p.Paste(msg.Content, c)
		}
		return o.Update(msg, c)
	}
	if iv, ok := a.nav.cur.(inputView); ok && iv.CapturesInput() {
		iv.Paste(msg.Content)
	}
	return nil
}

// dispatch sends an action to the top overlay, then the focused pane, then
// the app. With an overlay open the panes underneath are skipped, and only
// an overlay that is a pane or a mode (passesKeys) lets the app have the
// actions it leaves: a popup such as help, a menu or a picker is modal, so
// r or space can't toggle repeat or play behind it.
func (a *App) dispatch(act keymap.Action, n int) tea.Cmd {
	if o := a.topOverlay(); o != nil {
		if ok, cmd := o.Action(act, n, a.overlayCtx(o)); ok {
			return cmd
		}
		if act == keymap.Back || act == keymap.InputCancel || act == keymap.Quit {
			// q closes a popup rather than the app under it
			return a.CloseOverlay()
		}
		if passesKeys(o) {
			return a.global(act, n)
		}
		return nil
	}
	return a.run(act, n)
}

// passesKeys reports whether o hands the actions it doesn't handle to the
// app. Overlays with a key context of their own are panes or modes drawn
// over the frame (the sidebar and Up Next drawers, full-screen lyrics):
// playback, search and the leader keep working there. The rest are modal.
func passesKeys(o Overlay) bool {
	_, ok := o.(interface{ KeyContext() keymap.Context })
	return ok
}

func (a *App) run(act keymap.Action, n int) tea.Cmd {
	if ok, cmd := a.paneAction(act, n); ok {
		return cmd
	}
	return a.global(act, n)
}

func (a *App) paneAction(act keymap.Action, n int) (bool, tea.Cmd) {
	switch a.focus {
	case focusSidebar:
		return a.sidebar.action(a, act, n)
	case focusRight:
		if p := a.pane(); p != nil {
			return p.Action(act, n, a.ctxFor(focusRight, a.lay.RightBody.Min))
		}
	default:
		if a.nav.cur != nil {
			return a.nav.cur.Action(act, n, a.viewCtx())
		}
	}
	return false, nil
}

// swallowed reports whether ev is dropped: the second press of a
// double-click whose first press opened or closed a view or overlay, or
// that press's release. Any other press ends the swallow.
func (a *App) swallowed(ev MouseEvent) bool {
	switch {
	case ev.Kind == MousePress && a.swallow && ev.Double:
		a.swallowRelease = true
		a.swallow = false
		return true
	case ev.Kind == MousePress:
		a.swallow = false
	case ev.Kind == MouseRelease && a.swallowRelease:
		a.swallowRelease = false
		return true
	}
	return false
}

// handleMouse routes a normalized mouse event that Update did not handle
// itself (history buttons, the back glyph).
func (a *App) handleMouse(ev MouseEvent) tea.Cmd {
	if ev.Kind == MousePress {
		view, n := a.viewKey(), len(a.overlays)
		defer func() {
			// The second press of a double-click must not reach the view
			// or overlay this press opened, or what a closed overlay
			// uncovered: a single click on a sidebar item, rail icon,
			// search result or tile opens it, and the second press would
			// play the track under the pointer.
			a.swallow = a.viewKey() != view || len(a.overlays) != n
		}()
	}
	if a.pending.Pending && ev.Kind == MousePress {
		act := a.whichKeyHit(ev.X, ev.Y)
		a.keys.Reset()
		a.pending = keymap.Result{}
		if act != "" {
			return a.dispatch(act, 1)
		}
		return nil
	}
	if o := a.topOverlay(); o != nil {
		// A press that opened the overlay (the AirPlay name in the bar, a
		// right-click) never sees its release here, so its drag would stay
		// set and make the overlay drop drags that leave its rectangle.
		a.drag = regionNone
		return a.overlayMouse(o, ev)
	}

	held := ev.Kind == MouseRelease || (ev.Kind == MouseMotion && ev.Button != tea.MouseNone)
	reg := a.lay.Hit(ev.X, ev.Y)
	if a.drag != regionNone && held {
		reg = a.drag
	}
	switch ev.Kind {
	case MousePress:
		a.drag = reg
	case MouseRelease:
		defer func() { a.drag = regionNone }()
	case MouseMotion:
		if reg != a.hover {
			a.leave(a.hover)
			a.hover = reg
		}
	}
	if ev.Kind == MousePress {
		switch reg {
		case regionSidebar:
			a.setFocus(focusSidebar)
		case regionContent:
			a.setFocus(focusContent)
		case regionRight:
			a.setFocus(focusRight)
		}
	}

	body := a.lay.body(reg)
	local := ev.local(body.Min.X, body.Min.Y)
	switch reg {
	case regionBar:
		return a.barMouse(local)
	case regionSidebar:
		return a.sidebar.mouse(a, local)
	case regionRail:
		return a.sidebar.railMouse(a, local)
	case regionContent:
		if a.nav.cur != nil {
			return a.nav.cur.Mouse(local, a.viewCtx())
		}
	case regionRight:
		if p := a.pane(); p != nil {
			return p.Mouse(local, a.ctxFor(focusRight, a.lay.RightBody.Min))
		}
	case regionSideRule, regionRightRule:
		a.dragRule(reg, ev)
	}
	return nil
}

// leave tells a region the pointer moved out of it, so hover highlights
// clear.
func (a *App) leave(r region) {
	switch r {
	case regionBar:
		a.bar.hover = zoneNone
	case regionSidebar:
		a.sidebar.hover = -1
	case regionRail:
		a.sidebar.railHover = -1
	case regionContent:
		if a.nav.cur != nil {
			a.nav.cur.Mouse(leave, a.viewCtx())
		}
	case regionRight:
		if p := a.pane(); p != nil {
			p.Mouse(leave, a.ctxFor(focusRight, a.lay.RightBody.Min))
		}
	}
}

// dragRule resizes the sidebar or the right pane by dragging its rule.
func (a *App) dragRule(r region, ev MouseEvent) {
	if ev.Kind != MouseMotion || ev.Button != tea.MouseLeft {
		return
	}
	if r == regionSideRule {
		a.sidebarW = min(max(ev.ScreenX, minSidebarWidth), maxSidebarWidth)
	} else {
		a.rightW = min(max(a.w-ev.ScreenX-1, minRightWidth), maxRightWidth)
	}
	a.relayout()
}

func (a *App) overlayMouse(o Overlay, ev MouseEvent) tea.Cmd {
	r := o.Rect(a.w, a.h)
	inside := image.Pt(ev.X, ev.Y).In(r)
	held := ev.Kind == MouseRelease || (ev.Kind == MouseMotion && ev.Button != tea.MouseNone)
	switch {
	case inside || (a.drag == regionNone && held && a.overlayDrag):
	case ev.Kind == MousePress:
		a.overlayDrag = false
		return a.CloseOverlay()
	default:
		if ev.Kind == MouseMotion {
			// outside coordinates let the overlay clear its hover
			return o.Mouse(ev.local(r.Min.X, r.Min.Y), a.overlayCtx(o))
		}
		return nil
	}
	switch ev.Kind {
	case MousePress:
		a.overlayDrag = true
	case MouseRelease:
		a.overlayDrag = false
	}
	return o.Mouse(ev.local(r.Min.X, r.Min.Y), a.overlayCtx(o))
}

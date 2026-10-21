package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const doubleClickWithin = 350 * time.Millisecond

// clickTracker turns presses into MouseEvents, marking the second press of a
// pair on the same cell within 350 ms as Double. A third press starts a new
// pair.
type clickTracker struct {
	at     time.Time
	x, y   int
	button tea.MouseButton
	armed  bool
}

func (c *clickTracker) press(now time.Time, m tea.Mouse) bool {
	double := c.armed && m.Button == c.button && m.X == c.x && m.Y == c.y &&
		now.Sub(c.at) <= doubleClickWithin
	c.at, c.x, c.y, c.button = now, m.X, m.Y, m.Button
	c.armed = !double
	return double
}

// normalize converts a Bubble Tea mouse message into a screen-space
// MouseEvent. ok is false for messages encore ignores.
func (c *clickTracker) normalize(now time.Time, msg tea.MouseMsg) (MouseEvent, bool) {
	m := msg.Mouse()
	ev := MouseEvent{Button: m.Button, X: m.X, Y: m.Y, Mod: m.Mod, ScreenX: m.X, ScreenY: m.Y}
	switch msg.(type) {
	case tea.MouseClickMsg:
		ev.Kind = MousePress
		ev.Double = c.press(now, m)
	case tea.MouseReleaseMsg:
		ev.Kind = MouseRelease
	case tea.MouseMotionMsg:
		ev.Kind = MouseMotion
	case tea.MouseWheelMsg:
		ev.Kind = MouseWheel
	default:
		return ev, false
	}
	return ev, true
}

// local returns ev with X/Y relative to an origin.
func (ev MouseEvent) local(x0, y0 int) MouseEvent {
	ev.X -= x0
	ev.Y -= y0
	return ev
}

// wheelDelta is the scroll direction of a wheel event: +1 down, -1 up.
func (ev MouseEvent) wheelDelta() int {
	switch ev.Button {
	case tea.MouseWheelDown:
		return 1
	case tea.MouseWheelUp:
		return -1
	}
	return 0
}

// leave is the motion event sent to a component when the pointer leaves it.
var leave = MouseEvent{Kind: MouseMotion, Button: tea.MouseNone, X: -1, Y: -1}

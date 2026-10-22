package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Holding a key or spinning the wheel makes a message per repeat, 30 to 120
// a second, and each one builds a frame and sends the changed cells to the
// terminal. In the album grid a frame in half blocks is 65 KB. The eye takes
// nothing from the extra frames, so while input repeats, frames are held to
// Options.FrameCap apart.
//
// A frame is held by View handing back the last one. The held change shows
// when a frameFlushMsg arrives at the end of the interval, so the last step of
// a burst always shows and a single press is never held.
const (
	// repeatGap is the longest pause between two inputs that still counts as
	// repeating.
	repeatGap = 80 * time.Millisecond
	// artHoldAfter is how long after the last repeating input the art
	// loads that finish meanwhile, and the grid's settle messages, are held to
	// the same cap.
	artHoldAfter = 250 * time.Millisecond
)

// frameFlushMsg ends a held frame.
type frameFlushMsg struct{}

// frameGate decides which frames are held. The zero value holds nothing.
type frameGate struct {
	capEvery time.Duration
	lastIn   time.Time // the last key press or wheel step
	repeat   time.Time // the last one that came within repeatGap of the one before
	rendered time.Time // when the last frame was built
	last     tea.View
	have     bool
	armed    bool // a frameFlushMsg is on its way
	held     bool // View hands back last
}

// input is called with each message before it is handled. It reports whether
// the message is part of a burst of repeating input.
func (g *frameGate) input(msg tea.Msg, now time.Time) {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseWheelMsg:
		if !g.lastIn.IsZero() && now.Sub(g.lastIn) < repeatGap {
			g.repeat = now
		}
		g.lastIn = now
	}
}

// hold is called after a message was handled. It decides whether the frame
// that follows is held, and returns the command that ends the hold.
func (g *frameGate) hold(msg tea.Msg, now time.Time) tea.Cmd {
	g.held = false
	if g.capEvery <= 0 || !g.have {
		return nil
	}
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseWheelMsg:
		if g.repeat != g.lastIn {
			return nil // the first press of a burst shows at once
		}
	case artMsg, gridSettledMsg:
		// A settle message comes 150 ms after a scroll step, one per step, so
		// a held key sends as many as it has steps. Only the last one,
		// after the burst, has anything to show.
		if now.Sub(g.repeat) > artHoldAfter {
			return nil
		}
	default:
		return nil
	}
	since := now.Sub(g.rendered)
	if since >= g.capEvery {
		return nil
	}
	g.held = true
	if g.armed {
		return nil
	}
	g.armed = true
	return tea.Tick(g.capEvery-since, func(time.Time) tea.Msg { return frameFlushMsg{} })
}

// flushed is called when the hold's timer fires.
func (g *frameGate) flushed() { g.armed = false }

// take returns the held frame, or false when View must build a new one.
func (g *frameGate) take() (tea.View, bool) { return g.last, g.held && g.have }

// built records a frame View has just built.
func (g *frameGate) built(v tea.View, now time.Time) {
	g.last, g.have, g.rendered = v, true, now
}

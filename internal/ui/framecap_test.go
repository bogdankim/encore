package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestFrameGate(t *testing.T) {
	const capEvery = 33 * time.Millisecond
	t0 := time.Unix(1000, 0)
	key := tea.KeyPressMsg{Code: 'j', Text: "j"}
	wheel := tea.MouseWheelMsg{Button: tea.MouseWheelDown}

	// step feeds msg at time at, as Update does, and says whether the frame
	// that follows is held.
	g := frameGate{capEvery: capEvery}
	step := func(msg tea.Msg, at time.Time) (held bool, flush bool) {
		g.input(msg, at)
		cmd := g.hold(msg, at)
		return g.held, cmd != nil
	}
	build := func(at time.Time) { g.built(tea.NewView("x"), at) }

	if held, _ := step(key, t0); held {
		t.Fatal("held with no frame built yet")
	}
	build(t0)

	// a lone press well after the last input shows at once
	t1 := t0.Add(time.Second)
	if held, _ := step(key, t1); held {
		t.Fatal("the first press of a burst was held")
	}
	build(t1)

	// a repeat 20 ms later is held, and asks for a flush; the next one does not ask again
	if held, flush := step(key, t1.Add(20*time.Millisecond)); !held || !flush {
		t.Fatalf("held %v, flush %v for a repeat 20 ms after the last frame", held, flush)
	}
	if held, flush := step(wheel, t1.Add(25*time.Millisecond)); !held || flush {
		t.Fatalf("held %v, flush %v for a second held input", held, flush)
	}
	// the flush ends it: a message that is not a repeat builds a frame
	g.flushed()
	if held, _ := step(frameFlushMsg{}, t1.Add(33*time.Millisecond)); held {
		t.Fatal("the flush message was held")
	}
	build(t1.Add(33 * time.Millisecond))

	// a repeat after the cap is built, not held
	if held, _ := step(key, t1.Add(80*time.Millisecond)); held {
		t.Fatal("a repeat more than the cap after the last frame was held")
	}
	build(t1.Add(80 * time.Millisecond))

	// art that finishes during a burst waits for the cap; after the burst it doesn't
	t2 := t1.Add(100 * time.Millisecond)
	step(key, t2)
	build(t2)
	if held, _ := step(artMsg{}, t2.Add(10*time.Millisecond)); !held {
		t.Fatal("art finishing mid-burst was not held")
	}
	t3 := t2.Add(time.Second)
	build(t3.Add(-time.Millisecond))
	if held, _ := step(artMsg{}, t3); held {
		t.Fatal("art finishing long after the burst was held")
	}

	// anything else is never held
	build(t3)
	step(key, t3.Add(5*time.Millisecond))
	if held, _ := step(tea.WindowSizeMsg{Width: 10, Height: 10}, t3.Add(6*time.Millisecond)); held {
		t.Fatal("a resize was held")
	}

	// a zero cap holds nothing
	off := frameGate{}
	off.input(key, t3)
	off.built(tea.NewView("x"), t3)
	off.input(key, t3.Add(time.Millisecond))
	if off.hold(key, t3.Add(time.Millisecond)) != nil || off.held {
		t.Fatal("a gate without a cap held a frame")
	}
}

// A held frame is the previous one; the flush shows the last step.
func TestAppHoldsFramesDuringKeyRepeat(t *testing.T) {
	a := newShellApp(t, 120, 36)
	a.o.FrameCap = time.Hour // only the order of events matters here
	first := a.View().Content
	press := func() tea.Cmd {
		_, cmd := a.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		return cmd
	}
	press() // the first press of a burst: shown at once
	second := a.View().Content
	if second == first {
		t.Fatal("the first press changed nothing on screen")
	}
	cmd := press() // a repeat inside the cap
	if cmd == nil {
		t.Fatal("a held frame needs a flush command")
	}
	if held := a.View().Content; held != second {
		t.Fatal("a repeat inside the cap built a frame")
	}
	if !strings.Contains(second, "Songs") {
		t.Fatal("the test needs the Songs view")
	}
	a.Update(frameFlushMsg{})
	if shown := a.View().Content; shown == second {
		t.Fatal("the flush did not show the held step")
	}
}

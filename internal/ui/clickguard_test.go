package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
)

// railCell is the screen cell of rail item i.
func railCell(t *testing.T, a *App, i int) (x, y int) {
	t.Helper()
	a.render()
	if !a.lay.RailOn {
		t.Fatal("rail should be on")
	}
	for row, it := range railRows(a.sidebar.railH) {
		if it == i {
			return a.lay.RailBody.Min.X + 1, a.lay.RailBody.Min.Y + row
		}
	}
	t.Fatalf("rail item %d not shown", i)
	return 0, 0
}

func railIndex(act keymap.Action) int {
	for i, it := range railItems {
		if it.action == act {
			return i
		}
	}
	return -1
}

func doubleClick(a *App, x, y int) {
	click(a, x, y)
	click(a, x, y)
}

func TestSidebarClickFocusesContent(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.render()
	s := a.sidebar
	for i, e := range s.entries {
		if e.key == KeyAlbums {
			click(a, 4, a.lay.SidebarBody.Min.Y+i-s.top)
			break
		}
	}
	if a.nav.cur.Key() != KeyAlbums || a.focus != focusContent {
		t.Fatalf("click opens Albums with content focus: %s, focus %v", a.nav.cur.Key(), a.focus)
	}
}

func TestDoubleClickOnRailStaysOnRail(t *testing.T) {
	a := newShellApp(t, 80, 30)
	x, y := railCell(t, a, railIndex(keymap.GoPlaylists))
	doubleClick(a, x, y)
	if _, ok := a.topOverlay().(*drawer); !ok || a.nav.cur.Key() != KeySongs {
		t.Fatalf("the second press must not reach the drawer: overlay %T, view %s", a.topOverlay(), a.nav.cur.Key())
	}
	a.CloseOverlay()

	x, y = railCell(t, a, railIndex(keymap.SearchAll))
	doubleClick(a, x, y)
	if a.topOverlay() == nil {
		t.Fatal("the second press must not close the search it opened")
	}
}

func TestTableDoubleClickNeedsBothPresses(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.render()
	x, y := a.lay.ContentBody.Min.X+10, a.lay.ContentBody.Min.Y+tableTop+2
	// the first press of this double-click landed somewhere else on the
	// same cell, e.g. on a view it then replaced
	a.clicks = clickTracker{at: time.Now(), x: x, y: y, button: tea.MouseLeft, armed: true}
	a.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if a.player.State().Track != nil {
		t.Fatal("a table must not play on a double-click it saw only half of")
	}
	a.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	doubleClick(a, x, y)
	if a.player.State().Track == nil {
		t.Fatal("a double-click on a row plays it")
	}
}

func TestHintClearsWhenTheViewChanges(t *testing.T) {
	a := newShellApp(t, 80, 30)
	x, y := railCell(t, a, railIndex(keymap.GoAlbums))
	a.Update(tea.MouseMotionMsg{X: x, Y: y})
	if a.hint == "" {
		t.Fatal("hovering a rail icon names it")
	}
	click(a, x, y)
	if a.nav.cur.Key() != KeyAlbums || a.hint != "" {
		t.Fatalf("view %s, hint %q after the click", a.nav.cur.Key(), a.hint)
	}

	a.Update(tea.MouseMotionMsg{X: x, Y: y})
	press(a, "ctrl+o")
	if a.nav.cur.Key() != KeySongs || a.hint != "" {
		t.Fatalf("view %s, hint %q after going back", a.nav.cur.Key(), a.hint)
	}
}

func TestRightPaneFooterHints(t *testing.T) {
	for _, tc := range []struct {
		act  keymap.Action
		want []hint
	}{
		{keymap.PaneQueue, []hint{{"J/K", "move"}, {"x", "remove"}, {"m", "menu"}, {"enter", "play"}}},
		{keymap.PaneLyrics, []hint{{"j/k", "scroll"}, {"{ }", "offset"}, {"enter", "seek"}}},
	} {
		a := newShellApp(t, 140, 42)
		a.global(tc.act, 1)
		a.setFocus(focusRight)
		if a.focus != focusRight {
			t.Fatalf("%s: pane not focused", tc.act)
		}
		hs := a.footerHints()
		if len(hs) < len(tc.want) {
			t.Fatalf("%s: hints %v", tc.act, hs)
		}
		for i, w := range tc.want {
			if hs[i] != w {
				t.Fatalf("%s: hints %v, want %v first", tc.act, hs, tc.want)
			}
		}
	}
}

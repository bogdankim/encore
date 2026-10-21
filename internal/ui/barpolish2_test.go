package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
)

func TestEscNeverLeavesASection(t *testing.T) {
	a := newShellApp(t, 140, 42)
	press(a, "space", "g", "f")
	press(a, "space", "g", "s")
	if a.nav.cur.Key() != KeySongs {
		t.Fatalf("open %s", a.nav.cur.Key())
	}
	press(a, "esc")
	if a.nav.cur.Key() != KeySongs {
		t.Fatalf("esc in Songs went to %s", a.nav.cur.Key())
	}
	a.OpenPlaylist(a.lib.PlaylistsFlat[len(a.lib.PlaylistsFlat)-1])
	press(a, "esc")
	if a.nav.cur.Key() != KeySongs {
		t.Fatalf("esc on a playlist went to %s", a.nav.cur.Key())
	}
	a.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if a.nav.cur.Key() != KeyFavorites {
		t.Fatalf("ctrl+o still walks history: %s", a.nav.cur.Key())
	}
}

func TestVolumeHintNamesALoneSpeaker(t *testing.T) {
	a := playingApp(t, 140, 42)
	mac := bridge.AirPlayDevice{Name: "MacBook Pro", Kind: "computer", Selected: true}
	lr := bridge.AirPlayDevice{Name: "Living Room", Kind: "homepod", Selected: true}

	a.setAirPlay([]bridge.AirPlayDevice{mac})
	a.render()
	if a.barLayout.airName != "" {
		t.Errorf("this computer alone is named in the bar: %q", a.barLayout.airName)
	}
	a.Update(tea.MouseMotionMsg{X: a.barLayout.airX, Y: 1})
	if !strings.Contains(a.hint, "MacBook Pro") {
		t.Errorf("AirPlay hint %q should name the output the bar hides", a.hint)
	}
	a.Update(tea.MouseMotionMsg{X: a.barLayout.volX + 1, Y: 1})
	if !strings.HasPrefix(a.hint, "Volume ") {
		t.Errorf("local volume hint %q", a.hint)
	}

	mac.Selected = false
	a.setAirPlay([]bridge.AirPlayDevice{mac, lr})
	a.render()
	if a.barLayout.airName != "Living Room" {
		t.Errorf("bar AirPlay name %q", a.barLayout.airName)
	}
	a.Update(tea.MouseMotionMsg{X: a.barLayout.volX + 1, Y: 1})
	if want := "Music Volume " + itoa(a.snap.NowPlaying.Volume) + " · Living Room  - ="; a.hint != want {
		t.Errorf("volume hint %q, want %q", a.hint, want)
	}
}

func TestNarrowPaneToggles(t *testing.T) {
	a := playingApp(t, 80, 24)
	press(a, "space", "l")
	if _, ok := a.topOverlay().(*lyricsFull); !ok {
		t.Fatalf("space l at 80 columns opened %T", a.topOverlay())
	}
	press(a, "esc")
	if a.right != rightNone || a.paneShown(rightLyrics) {
		t.Error("the lyrics toggle is on with no pane on screen")
	}

	press(a, "space", "q")
	d, ok := a.topOverlay().(*queueDrawer)
	if !ok {
		t.Fatalf("space q at 80 columns opened %T", a.topOverlay())
	}
	if !a.paneShown(rightQueue) {
		t.Error("the Up Next toggle is off with the drawer open")
	}
	checkBlock(t, "drawer", Box(d.Render(a.overlayCtx(d)), d.Rect(a.w, a.h).Dx(), d.Rect(a.w, a.h).Dy()), d.Rect(a.w, a.h).Dx(), d.Rect(a.w, a.h).Dy())
	checkBlock(t, "frame", a.render(), 80, 24)
	press(a, "space", "q")
	if a.topOverlay() != nil {
		t.Fatal("space q left the drawer open")
	}

	// a pane open at 140 columns hides at 80, and its toggle goes dark
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 42})
	press(a, "space", "l")
	if !a.paneShown(rightLyrics) {
		t.Fatal("no lyrics pane at 140 columns")
	}
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if a.paneShown(rightLyrics) {
		t.Error("the lyrics toggle is lit with the pane hidden")
	}

	// the drawer becomes the pane when the window widens
	a.right = rightNone
	a.relayout()
	press(a, "space", "q")
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 42})
	if a.topOverlay() != nil || a.right != rightQueue || !a.lay.RightOn {
		t.Errorf("drawer at 140 columns: overlay %T, right %d", a.topOverlay(), a.right)
	}
}

func TestFitsCut(t *testing.T) {
	for _, tc := range []struct {
		s    string
		w    int
		want bool
	}{
		{"Joaquín Lindqvist", 4, false},
		{"Joaquín Lindqvist", 8, true},
		{"ABBA", 4, true},
		{"ABBA", 3, false},
		{"", 0, false},
	} {
		if got := fitsCut(tc.s, tc.w); got != tc.want {
			t.Errorf("fitsCut(%q, %d) = %v", tc.s, tc.w, got)
		}
	}
}

func TestRailSpacing(t *testing.T) {
	spaced := 2*len(railItems) + 1
	for _, h := range []int{0, 3, 7, 8, 9, 14, 15, spaced, 30} {
		rows := railRows(h)
		if len(rows) > h {
			t.Errorf("h %d: %d rows", h, len(rows))
		}
		seen := 0
		for y, i := range rows {
			if i >= 0 {
				seen++
				if h >= spaced && y > 0 && rows[y-1] >= 0 {
					t.Errorf("h %d: icons touch at row %d", h, y)
				}
			}
		}
		if h >= len(railItems) && seen != len(railItems) {
			t.Errorf("h %d: %d icons", h, seen)
		}
	}
}

func TestHintInRuleWithoutFooter(t *testing.T) {
	a := playingApp(t, 80, 20)
	if !a.lay.Footer.Empty() {
		t.Fatal("footer at 20 rows")
	}
	a.Update(tea.MouseMotionMsg{X: a.barLayout.btn[1], Y: 1})
	rule := ansi.Strip(strings.Split(a.render(), "\n")[a.lay.BarRule])
	if !strings.HasSuffix(rule, " Pause  p ──") || ansi.StringWidth(rule) != 80 {
		t.Errorf("rule %q", rule)
	}
	a.Update(tea.MouseMotionMsg{X: 40, Y: 15})
	rule = ansi.Strip(strings.Split(a.render(), "\n")[a.lay.BarRule])
	if strings.Contains(rule, " ") {
		t.Errorf("rule keeps a note after the pointer left: %q", rule)
	}

	b := playingApp(t, 80, 24)
	b.Update(tea.MouseMotionMsg{X: b.barLayout.btn[1], Y: 1})
	if rule := ansi.Strip(strings.Split(b.render(), "\n")[b.lay.BarRule]); strings.Contains(rule, " ") {
		t.Errorf("rule carries the hint while the footer shows: %q", rule)
	}
}

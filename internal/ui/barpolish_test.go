package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

func TestLayoutRailAndPanes(t *testing.T) {
	for w := tooSmallW; w <= 240; w++ {
		for _, o := range []layoutOpts{
			{sidebar: true}, {sidebar: false}, {sidebar: true, right: true},
			{sidebar: true, right: true, rightW: 80}, {sidebar: true, sidebarW: 40, right: true},
		} {
			l := computeLayout(w, 30, o)
			if l.SidebarOn == l.RailOn {
				t.Fatalf("%d %+v: sidebar %v rail %v", w, o, l.SidebarOn, l.RailOn)
			}
			if l.SidebarOn && l.Sidebar.Dx() != sidebarWidth(o.sidebarW) {
				t.Fatalf("%d %+v: sidebar %d wide, want its own width", w, o, l.Sidebar.Dx())
			}
			if l.RailOn && l.Rail.Dx() != railW {
				t.Fatalf("%d %+v: rail %d wide", w, o, l.Rail.Dx())
			}
			if l.SidebarOn && !o.sidebar {
				t.Fatalf("%d: sidebar on though the user folded it", w)
			}
			if l.RightOn {
				if l.Right.Dx() < minRightWidth {
					t.Fatalf("%d %+v: right pane %d wide", w, o, l.Right.Dx())
				}
				if l.SidebarOn && l.Content.Dx() < roomyContent {
					t.Fatalf("%d %+v: content %d next to a pane with the full sidebar", w, o, l.Content.Dx())
				}
				if l.Content.Dx() < roomyContent && l.Right.Dx() != minRightWidth {
					t.Fatalf("%d %+v: content %d while the pane keeps %d", w, o, l.Content.Dx(), l.Right.Dx())
				}
			}
			if l.Content.Dx() < 36 {
				t.Fatalf("%d %+v: content %d", w, o, l.Content.Dx())
			}
			for x := 0; x < w; x++ {
				if l.Hit(x, l.Middle.Min.Y) == regionNone {
					t.Fatalf("%d %+v: nothing at x=%d", w, o, x)
				}
			}
			if l.Hit(w/2, l.BarRule) != regionNone {
				t.Fatalf("%d: the rule under the bar is clickable", w)
			}
			if l.Compact != (w < compactBarUnder) {
				t.Fatalf("%d: compact %v", w, l.Compact)
			}
		}
	}
	if l := computeLayout(100, 30, layoutOpts{sidebar: true, right: true}); !l.RailOn || l.Right.Dx() != minRightWidth {
		t.Errorf("100x30 with a pane: rail %v, pane %d", l.RailOn, l.Right.Dx())
	}
	if l := computeLayout(140, 42, layoutOpts{sidebar: true, right: true}); !l.SidebarOn || l.Sidebar.Dx() != defaultSidebarW || l.Right.Dx() != rightWidth {
		t.Errorf("140x42 with a pane: sidebar %d, pane %d", l.Sidebar.Dx(), l.Right.Dx())
	}
	if l := computeLayout(80, 24, layoutOpts{sidebar: true}); !l.RailOn {
		t.Error("80 columns should show the rail")
	}
}

// checkBarZones fails when two zones on a row overlap or leave the bar.
func checkBarZones(t *testing.T, name string, b barLayout) {
	t.Helper()
	for i, z := range b.zones {
		if z.x0 < 0 || z.x1 > b.w || z.x0 >= z.x1 {
			t.Fatalf("%s: zone %d [%d,%d) outside %d", name, z.z, z.x0, z.x1, b.w)
		}
		for _, o := range b.zones[i+1:] {
			sameRow := z.y < 0 || o.y < 0 || z.y == o.y
			if sameRow && z.x0 < o.x1 && o.x0 < z.x1 {
				t.Fatalf("%s: zones %d [%d,%d) y%d and %d [%d,%d) y%d overlap", name, z.z, z.x0, z.x1, z.y, o.z, o.x0, o.x1, o.y)
			}
		}
	}
}

func TestBarLayoutCollapses(t *testing.T) {
	for _, g := range []theme.Glyphs{theme.NerdFont, theme.Unicode} {
		for _, rows := range []int{1, 3} {
			for _, dot := range []bool{false, true} {
				prev := numBarLevels + 1
				for w := 30; w <= 260; w++ {
					air := airLabel{name: "Living Room Speaker", more: 1}
					b := layoutBar(w, rows, &g, air, dot, 3*time.Minute)
					name := "w" + itoa(w) + " rows" + itoa(rows)
					checkBarZones(t, name, b)
					if b.mini != (w < miniBarUnder) {
						t.Fatalf("%s: mini %v", name, b.mini)
					}
					if b.level > prev {
						t.Fatalf("%s: level %d after %d at a narrower width", name, b.level, prev)
					}
					prev = b.level
					if b.mini {
						if b.textW <= 0 || (w >= tinyBarUnder) != (b.timeX >= 0) {
							t.Fatalf("%s: mini text %d time %d", name, b.textW, b.timeX)
						}
						continue
					}
					if b.gap < groupGap || b.lcdX < b.leftEnd+b.gap || b.lcdX+b.lcdW > b.rightX-b.gap {
						t.Fatalf("%s: lcd %d+%d between %d and %d (gap %d)", name, b.lcdX, b.lcdW, b.leftEnd, b.rightX, b.gap)
					}
					if b.lcdW > lcdMaxW {
						t.Fatalf("%s: lcd %d wide", name, b.lcdW)
					}
					// the drop order
					shown := map[string]bool{
						"air": b.airName != "", "num": b.volNum, "shuffle": b.btn[3] >= 0,
						"meter": b.meter > 0, "toggles": b.lyricsX >= 0, "remain": b.remain,
					}
					order := []string{"air", "num", "shuffle", "meter", "toggles", "remain"}
					for i := 1; i < len(order); i++ {
						if shown[order[i-1]] && !shown[order[i]] {
							t.Fatalf("%s: %s shows but %s is gone: %v", name, order[i-1], order[i], shown)
						}
					}
				}
			}
		}
	}
	g := theme.NerdFont
	if b := layoutBar(200, 3, &g, airLabel{name: "MacBook Pro"}, false, 3*time.Minute); b.level != levelAll || b.lcdW != lcdMaxW {
		t.Errorf("200 columns: level %d, lcd %d", b.level, b.lcdW)
	}
}

// playingApp is an app at w×h playing the first songs.
func playingApp(t *testing.T, w, h int) *App {
	t.Helper()
	a := newShellApp(t, w, h)
	a.player.PlayContext(player.Source{Kind: player.KindSongs, Title: "Songs"}, a.lib.Songs[:10], 0)
	waitFor(t, func() bool { a.player.Observe(stateEvent(t, a)); return !a.player.State().Loading })
	a.refreshSnap()
	a.render()
	return a
}

func TestBarRendersWithoutOverlap(t *testing.T) {
	a := playingApp(t, 140, 42)
	for _, sz := range [][2]int{{40, 12}, {59, 18}, {60, 18}, {80, 24}, {100, 30}, {140, 42}, {200, 55}} {
		a.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		a.bar.lastfmIssue = sz[0]%20 == 0
		out := a.render()
		checkBlock(t, "frame", out, sz[0], sz[1])
		checkBarZones(t, "rendered "+itoa(sz[0]), a.barLayout)
		var title, artist bool
		for _, z := range a.barLayout.zones {
			title = title || z.z == zoneTitle
			artist = artist || z.z == zoneArtist
		}
		if !title {
			t.Errorf("%v: the title has no zone", sz)
		}
		if !artist && sz[0] >= 80 {
			t.Errorf("%v: the artist has no zone", sz)
		}
	}
}

func TestBarHoverHints(t *testing.T) {
	a := playingApp(t, 140, 42)
	b := a.barLayout
	for _, tc := range []struct {
		x    int
		want string
	}{
		{b.lyricsX, actionName(keymap.PaneLyrics) + "  space l"},
		{b.queueX, actionName(keymap.PaneQueue) + "  space q"},
		{b.airX, actionName(keymap.AirPlay) + "  space a"},
		{b.btn[1], "Pause  p"},
		{b.btn[3], "Shuffle: Off  s"},
		{b.btn[4], "Repeat: Off  r"},
		{b.lcdX + 1, actionName(keymap.GoNowPlaying) + "  space g n"},
	} {
		a.Update(tea.MouseMotionMsg{X: tc.x, Y: 1})
		if a.hint != tc.want {
			t.Errorf("hover at %d: %q, want %q", tc.x, a.hint, tc.want)
		}
		if f := ansi.Strip(a.renderFooter(a.baseCtx())); !strings.HasPrefix(f, " "+tc.want) {
			t.Errorf("footer %q", f)
		}
	}
	a.Update(tea.MouseMotionMsg{X: b.progX + 2, Y: 2})
	if a.hint != "Seek  , ." {
		t.Errorf("progress hint %q", a.hint)
	}
	a.Update(tea.MouseMotionMsg{X: 60, Y: 20})
	if a.hint != "" || a.bar.hover != zoneNone {
		t.Errorf("hint %q after leaving the bar", a.hint)
	}
}

func TestFooterHintsFollowTheView(t *testing.T) {
	a := newShellApp(t, 140, 42)
	labels := func() string {
		var s []string
		for _, h := range a.footerHints() {
			s = append(s, h.keys+" "+h.label)
		}
		return strings.Join(s, ", ")
	}
	if got := labels(); !strings.Contains(got, "enter play") || !strings.Contains(got, "space commands") {
		t.Errorf("songs: %s", got)
	}
	a.goSection(KeyAlbums, "Albums")
	if got := labels(); !strings.Contains(got, "enter open") {
		t.Errorf("albums: %s", got)
	}
	a.Do(keymap.Help)
	if len(a.overlays) > 0 && len(a.footerHints()) != 0 {
		t.Errorf("hints with an overlay open: %s", labels())
	}
}

func TestStatusClearsOnNextKey(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.setStatus("Added to Up Next: Song", false)
	press(a, "j")
	if a.status.text != "" {
		t.Fatalf("status %q survived a key", a.status.text)
	}
	if statusFor != 3*time.Second {
		t.Fatalf("status lasts %v", statusFor)
	}
}

func TestMouseHistoryButtons(t *testing.T) {
	a := newShellApp(t, 140, 42)
	al := a.lib.Albums[0]
	a.OpenAlbum(al)
	want := a.nav.cur.Key()
	a.Update(tea.MouseClickMsg{X: 50, Y: 20, Button: tea.MouseBackward})
	if a.nav.cur.Key() != KeySongs {
		t.Fatalf("back button: %s", a.nav.cur.Key())
	}
	a.Update(tea.MouseClickMsg{X: 50, Y: 20, Button: tea.MouseForward})
	if a.nav.cur.Key() != want {
		t.Fatalf("forward button: %s", a.nav.cur.Key())
	}
}

func TestBackMarkClick(t *testing.T) {
	a := newShellApp(t, 140, 42)
	var pl = a.lib.PlaylistsFlat[0]
	for _, p := range a.lib.PlaylistsFlat {
		if p.Kind != "folder" && len(p.Tracks) > 0 {
			pl = p
			break
		}
	}
	a.OpenPlaylist(pl)
	a.render()
	if !a.backOn {
		t.Skip("the playlist header does not draw Ctx.BackMark")
	}
	a.Update(tea.MouseMotionMsg{X: a.back.X, Y: a.back.Y})
	if a.hint != "Back  ctrl+o" {
		t.Errorf("hint %q", a.hint)
	}
	click(a, a.back.X, a.back.Y)
	if a.nav.cur.Key() != KeySongs {
		t.Fatalf("back glyph went to %s", a.nav.cur.Key())
	}
	a.render()
	if a.backOn {
		t.Error("a section page has a back glyph")
	}
}

func TestRailAndDrawer(t *testing.T) {
	a := newShellApp(t, 80, 24)
	if !a.lay.RailOn {
		t.Fatal("no rail at 80 columns")
	}
	a.render()
	y0 := a.lay.RailBody.Min.Y
	rows := railRows(a.lay.RailBody.Dy())
	rowOf := func(item int) int {
		for y, i := range rows {
			if i == item {
				return y0 + y
			}
		}
		t.Fatalf("item %d not in the rail", item)
		return -1
	}
	albums := railIndex(keymap.GoAlbums)
	a.Update(tea.MouseMotionMsg{X: 1, Y: rowOf(albums)})
	if !strings.HasPrefix(a.hint, "Albums") || a.sidebar.railHover != albums {
		t.Errorf("rail hover: %q %d", a.hint, a.sidebar.railHover)
	}
	click(a, 1, rowOf(albums))
	if a.nav.cur.Key() != KeyAlbums {
		t.Fatalf("rail click opened %s", a.nav.cur.Key())
	}
	click(a, 1, rowOf(railIndex(keymap.GoPlaylists)))
	d, ok := a.topOverlay().(*drawer)
	if !ok {
		t.Fatal("Playlists did not open the drawer")
	}
	checkBlock(t, "drawer", d.Render(a.overlayCtx(d)), d.Rect(80, 24).Dx(), d.Rect(80, 24).Dy())
	s := a.sidebar
	idx := -1
	for i, e := range s.entries {
		if e.kind == sbPlaylist {
			idx = i
			break
		}
	}
	a.render()
	click(a, 4, d.Rect(80, 24).Min.Y+middlePadTop+idx-s.top)
	if a.topOverlay() != nil {
		t.Fatal("choosing a playlist left the drawer open")
	}
	if a.nav.cur.Key() != s.entries[idx].key {
		t.Fatalf("drawer opened %s, want %s", a.nav.cur.Key(), s.entries[idx].key)
	}
	press(a, "space", "b")
	if _, ok := a.topOverlay().(*drawer); !ok {
		t.Fatal("space b at 80 columns should open the drawer")
	}
	press(a, "esc")
	if a.topOverlay() != nil {
		t.Fatal("esc left the drawer open")
	}
}

func TestSidebarHidesFavoritesPlaylistAndMarksPlaying(t *testing.T) {
	a := newShellApp(t, 140, 42)
	for _, e := range a.sidebar.entries {
		if e.pl != nil && e.pl.Special == "favorites" {
			t.Fatalf("%q is in the sidebar", e.label)
		}
	}
	a.player.PlayContext(player.Source{Kind: player.KindSongs, Title: "Songs"}, a.lib.Songs[:3], 0)
	a.refreshSnap()
	out := ansi.Strip(a.render())
	g := shellTheme().G
	if !strings.Contains(out, "Songs "+g.Playing) {
		t.Error("no playing mark after Songs")
	}
}

func TestGoToNowPlayingWithoutAlbum(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.snap.NowPlaying.Track = &bridge.CurrentTrack{PID: "FFFF", Name: "Radio"}
	a.snap.Track = nil
	a.global(keymap.GoNowPlaying, 1)
	if a.status.text != "No album page for this song" {
		t.Fatalf("status %q", a.status.text)
	}
}

func TestBarArtistClickOpensArtist(t *testing.T) {
	a := playingApp(t, 140, 42)
	var az zone
	for _, z := range a.barLayout.zones {
		if z.z == zoneArtist {
			az = z
		}
	}
	click(a, az.x0, az.y)
	if !strings.HasPrefix(a.nav.cur.Key(), "artist:") {
		t.Fatalf("artist click opened %s", a.nav.cur.Key())
	}
}

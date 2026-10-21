package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/config"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/library/libtest"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

var (
	shellOnce sync.Once
	shellLib  *library.Library
)

func shellLibrary(t testing.TB) *library.Library {
	t.Helper()
	shellOnce.Do(func() {
		f := bridge.NewFakeWithClock(1, func() time.Time { return bridge.FakeEpoch })
		snap, err := f.Snapshot(context.Background())
		if err != nil {
			panic(err)
		}
		shellLib = library.Build(snap)
	})
	return shellLib
}

func shellTheme() *theme.Theme { return theme.New(theme.Lookup("encore", true), theme.Unicode, false) }

// newShellApp returns an app on the fake bridge, sized w×h.
func newShellApp(t testing.TB, w, h int) *App {
	t.Helper()
	lib := shellLibrary(t)
	f := bridge.NewFakeWithClock(1, time.Now)
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	a := New(Options{
		Client: f, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th },
	})
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	a.goSection(KeySongs, "Songs")
	return a
}

func press(a *App, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "ctrl+d":
			msg = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		a.Update(msg)
	}
}

func click(a *App, x, y int) {
	a.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	a.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func checkBlock(t *testing.T, name, s string, w, h int) {
	t.Helper()
	lines := strings.Split(s, "\n")
	if len(lines) != h {
		t.Fatalf("%s: %d lines, want %d", name, len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("%s: line %d is %d wide, want %d: %q", name, i, got, w, ansi.Strip(l))
		}
	}
}

func tracksOf(a *App) *TrackTable { return a.nav.cur.(*tracksView).table }

func TestLayoutSizes(t *testing.T) {
	for _, tc := range []struct {
		w, h                     int
		sidebar, footer, compact bool
	}{
		{140, 42, true, true, false},
		{100, 30, true, true, false},
		{89, 30, false, true, false},
		{200, 55, true, true, false},
		{120, 23, true, false, false},
		{120, 18, true, false, true},
	} {
		l := computeLayout(tc.w, tc.h, layoutOpts{sidebar: true})
		if l.SidebarOn != tc.sidebar || !l.Footer.Empty() != tc.footer || l.Compact != tc.compact {
			t.Errorf("%dx%d: sidebar %v footer %v compact %v", tc.w, tc.h, l.SidebarOn, !l.Footer.Empty(), l.Compact)
		}
		if l.Content.Dx() < minContentWidth {
			t.Errorf("%dx%d: content %d wide", tc.w, tc.h, l.Content.Dx())
		}
		// regions tile the middle without gaps
		for x := 0; x < tc.w; x++ {
			if r := l.Hit(x, l.Middle.Min.Y); r == regionNone {
				t.Errorf("%dx%d: no region at x=%d", tc.w, tc.h, x)
			}
		}
		if l.Hit(5, 0) != regionBar {
			t.Errorf("%dx%d: bar not hit", tc.w, tc.h)
		}
	}
	r := computeLayout(140, 42, layoutOpts{sidebar: true, right: true})
	if !r.RightOn || r.Right.Dx() != rightWidth || r.Hit(r.Right.Min.X+1, 20) != regionRight {
		t.Errorf("right pane: %+v", r.Right)
	}
	if computeLayout(99, 42, layoutOpts{right: true}).RightOn {
		t.Error("right pane under 100 columns")
	}
	if full := computeLayout(140, 42, layoutOpts{sidebar: true, right: true, fullRight: true}); full.ContentOn || full.SidebarOn || full.Right != full.Middle {
		t.Error("full-screen right pane should take the middle")
	}
	if !computeLayout(30, 5, layoutOpts{}).Small {
		t.Error("tiny window not marked small")
	}
}

func TestAppRendersExactSize(t *testing.T) {
	for _, sz := range [][2]int{{140, 42}, {100, 30}, {200, 55}, {80, 24}, {120, 18}, {60, 12}, {40, 8}, {30, 5}, {1, 1}} {
		a := newShellApp(t, sz[0], sz[1])
		checkBlock(t, "app", a.render(), sz[0], sz[1])
		press(a, "space")
		checkBlock(t, "app+whichkey", a.render(), sz[0], sz[1])
		press(a, "esc")
		press(a, "space", "q") // Up Next pane
		checkBlock(t, "app+pane", a.render(), sz[0], sz[1])
	}
}

func TestTrackTableRenderSizes(t *testing.T) {
	lib := shellLibrary(t)
	c := &Ctx{Theme: shellTheme(), Lib: lib, Focused: true}
	for _, ts := range [][]*library.Track{nil, lib.Songs[:3], lib.Songs} {
		tt := NewTrackTable(ts, TableOptions{Empty: "No songs"})
		for _, sz := range [][2]int{{0, 0}, {1, 1}, {5, 3}, {11, 2}, {40, 1}, {60, 20}, {200, 60}} {
			s := tt.Render(sz[0], sz[1], c)
			if sz[0] == 0 || sz[1] == 0 {
				continue
			}
			checkBlock(t, "table", s, sz[0], sz[1])
		}
	}
}

func TestTrackTableColumnsDropWhenNarrow(t *testing.T) {
	tt := NewTrackTable(shellLibrary(t).Songs, TableOptions{Wide: []Column{ColPlays, ColAdded}})
	has := func(w int, c Column) bool {
		for _, cs := range tt.columns(w) {
			if cs.c == c {
				return true
			}
		}
		return false
	}
	if !has(200, ColAdded) || !has(200, ColAlbum) {
		t.Error("wide table should show Added and Album")
	}
	if has(100, ColAdded) || !has(100, ColAlbum) {
		t.Error("100 wide: Album without wide columns")
	}
	if has(60, ColAlbum) || !has(60, ColArtist) {
		t.Error("60 wide: Album drops first")
	}
	if has(30, ColArtist) || !has(30, ColTitle) {
		t.Error("30 wide: only Title")
	}
	cols := tt.columns(140)
	last := cols[len(cols)-1]
	if last.c != ColTime || last.x+last.w != 140-2 {
		t.Errorf("Time column should end before the scrollbar margin: %+v", last)
	}
}

func TestTrackTableNavigation(t *testing.T) {
	lib := shellLibrary(t)
	c := &Ctx{Theme: shellTheme(), Lib: lib, Focused: true}
	tt := NewTrackTable(lib.Songs, TableOptions{})
	tt.Render(100, 21, c) // 20 body rows
	tt.Action(keymap.NavDown, 5, c)
	if tt.cursor != 5 {
		t.Fatalf("5j: cursor %d", tt.cursor)
	}
	tt.Action(keymap.NavBottom, 1, c)
	if tt.cursor != len(lib.Songs)-1 || tt.top != len(lib.Songs)-20 {
		t.Fatalf("G: cursor %d top %d", tt.cursor, tt.top)
	}
	tt.Action(keymap.NavTop, 1, c)
	tt.Action(keymap.NavHalfDown, 1, c)
	if tt.cursor != 10 || tt.top != 10 {
		t.Fatalf("ctrl+d: cursor %d top %d", tt.cursor, tt.top)
	}
	tt.Action(keymap.NavUp, 100, c)
	if tt.cursor != 0 {
		t.Fatalf("100k: cursor %d", tt.cursor)
	}
}

func TestTrackTableFilterAndSort(t *testing.T) {
	lib := shellLibrary(t)
	c := &Ctx{Theme: shellTheme(), Lib: lib, Focused: true}
	tt := NewTrackTable(lib.Songs, TableOptions{})
	tt.Render(120, 30, c)
	tt.Action(keymap.Filter, 1, c)
	for _, r := range "amber" {
		tt.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if !tt.Filtered() || tt.Len() == 0 || tt.Len() >= tt.Total() {
		t.Fatalf("filter: %d of %d", tt.Len(), tt.Total())
	}
	tt.Key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if tt.query != "ambe" {
		t.Fatalf("backspace: %q", tt.query)
	}
	tt.Action(keymap.InputSubmit, 1, c)
	if tt.Typing() || !tt.Filtered() {
		t.Fatal("enter should keep the filter and stop typing")
	}
	tt.Action(keymap.NavDown, 3, c)
	pid := tt.Current().PID
	if ok, _ := tt.Action(keymap.Back, 1, c); !ok || tt.Filtered() {
		t.Fatal("esc should clear the filter")
	}
	if tt.Current().PID != pid {
		t.Fatal("clearing the filter lost the selection")
	}

	tt.toggleSort(ColArtist)
	if tt.Current().PID != pid {
		t.Fatal("sorting lost the selection")
	}
	for i := 1; i < tt.Len(); i++ {
		if library.CompareKeys(tt.rows[i-1].SortArtist, tt.rows[i].SortArtist) > 0 {
			t.Fatalf("not sorted by artist at %d", i)
		}
	}
	tt.toggleSort(ColArtist)
	if !tt.desc {
		t.Fatal("second click should reverse")
	}
	tt.toggleSort(ColArtist)
	if tt.desc || !tt.sortSet {
		t.Fatal("a table without its own order goes back to ascending")
	}

	pl := NewTrackTable(lib.Songs[:50], TableOptions{Natural: true})
	first := pl.rows[0]
	pl.toggleSort(ColTitle)
	pl.toggleSort(ColTitle)
	pl.toggleSort(ColTitle)
	if pl.sortSet || pl.rows[0] != first {
		t.Fatal("third click should restore playlist order")
	}
}

func TestTrackTableMouse(t *testing.T) {
	a := newShellApp(t, 140, 42)
	tt := tracksOf(a)
	body := a.lay.ContentBody
	rowY := body.Min.Y + tableTop + 1 + 4 // fifth row
	a.render()
	click(a, body.Min.X+10, rowY)
	if tt.cursor != 4 {
		t.Fatalf("click: cursor %d", tt.cursor)
	}
	want := tt.rows[4]
	if !want.Available {
		t.Skip("demo row unavailable")
	}
	click(a, body.Min.X+10, rowY)
	if got := a.player.State().Track; got == nil || got.PID != want.PID {
		t.Fatalf("double click should play %s, playing %v", want.Title, got)
	}

	a.Update(tea.MouseWheelMsg{X: body.Min.X + 10, Y: rowY, Button: tea.MouseWheelDown})
	if tt.top != wheelRows {
		t.Fatalf("wheel: top %d", tt.top)
	}
	a.Update(tea.MouseMotionMsg{X: body.Min.X + 10, Y: rowY})
	if tt.hover != tt.top+4 {
		t.Fatalf("hover %d", tt.hover)
	}
	a.Update(tea.MouseMotionMsg{X: 2, Y: rowY}) // into the sidebar
	if tt.hover != -1 {
		t.Fatal("hover should clear when the pointer leaves")
	}

	var artistX int
	for _, cs := range tt.cols {
		if cs.c == ColArtist {
			artistX = body.Min.X + cs.x
		}
	}
	click(a, artistX, body.Min.Y+tableTop)
	if !tt.sortSet || tt.sortCol != ColArtist {
		t.Fatal("header click should sort by artist")
	}
}

func TestKeyRouting(t *testing.T) {
	a := newShellApp(t, 140, 42)
	tt := tracksOf(a)
	press(a, "j", "j")
	if tt.cursor != 2 || a.focus != focusContent {
		t.Fatalf("jj: cursor %d", tt.cursor)
	}
	press(a, "h")
	if a.focus != focusSidebar {
		t.Fatal("h should focus the sidebar")
	}
	before := a.sidebar.cursor
	press(a, "j")
	if a.sidebar.cursor == before || tt.cursor != 2 {
		t.Fatal("j in the sidebar should move the sidebar only")
	}
	press(a, "l", "/")
	if a.keyContext() != keymap.Input {
		t.Fatal("/ should start typing")
	}
	press(a, "q")
	if tt.query != "q" {
		t.Fatalf("typed q went to %q", tt.query)
	}
	press(a, "esc")
	if a.keyContext() != keymap.List || tt.Filtered() {
		t.Fatal("esc should stop typing and clear")
	}

	press(a, "space")
	if !a.pending.Pending || len(a.pending.Hints) == 0 {
		t.Fatal("leader should show which-key")
	}
	press(a, "g", "f")
	if a.nav.cur.Key() != KeyFavorites || a.pending.Pending {
		t.Fatalf("␣gf opened %s", a.nav.cur.Key())
	}
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q should return tea.Quit")
	}
}

func TestHistory(t *testing.T) {
	a := newShellApp(t, 140, 42)
	p := a.lib.PlaylistsFlat[len(a.lib.PlaylistsFlat)-1]
	start := len(a.nav.back)
	a.OpenPlaylist(p)
	press(a, "space", "g", "f")
	if a.nav.cur.Key() != KeyFavorites {
		t.Fatal("favorites not open")
	}
	a.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if a.nav.cur.Key() != "playlist:"+p.PID {
		t.Fatalf("ctrl+o: %s", a.nav.cur.Key())
	}
	press(a, "esc")
	if a.nav.cur.Key() != KeySongs {
		t.Fatalf("esc: %s", a.nav.cur.Key())
	}
	a.nav.goForward()
	if a.nav.cur.Key() != "playlist:"+p.PID {
		t.Fatal("forward")
	}
	a.nav.open(a.nav.cur) // same key: no duplicate
	if len(a.nav.back) != start+1 {
		t.Fatalf("history %d deep", len(a.nav.back))
	}
}

func TestPlayerBarMouse(t *testing.T) {
	a := newShellApp(t, 140, 42)
	ts := a.lib.Songs[:20]
	a.player.PlayContext(player.Source{Kind: player.KindSongs}, ts, 0)
	a.refreshSnap()
	a.render()
	b := a.barLayout

	click(a, b.volX+b.meter-1, 1)
	if v := a.player.State().NowPlaying.Volume; v != 100 {
		t.Fatalf("volume click: %d", v)
	}
	a.Update(tea.MouseClickMsg{X: b.volX, Y: 1, Button: tea.MouseLeft})
	a.Update(tea.MouseMotionMsg{X: b.volX + 3, Y: 1, Button: tea.MouseLeft})
	a.Update(tea.MouseReleaseMsg{X: b.volX + 3, Y: 1, Button: tea.MouseLeft})
	if v := a.player.State().NowPlaying.Volume; v != 50 {
		t.Fatalf("volume drag: %d", v)
	}

	// let Music confirm the track so seeking applies
	waitFor(t, func() bool { a.player.Observe(stateEvent(t, a)); return !a.player.State().Loading })
	a.refreshSnap()
	a.render()
	b = a.barLayout
	click(a, b.progX+b.progW/2, 2)
	pos, dur := a.player.Position(), a.player.State().Duration
	if d := pos - dur/2; d < -dur/10 || d > dur/10 {
		t.Fatalf("seek to middle: %v of %v", pos, dur)
	}
	if b.hit(b.queueX, 1) != zoneQueue {
		t.Fatal("queue toggle zone")
	}
	click(a, b.queueX, 1)
	if a.right != rightQueue {
		t.Fatal("queue toggle click")
	}
}

func stateEvent(t *testing.T, a *App) bridge.Event {
	np, err := a.client.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return bridge.PlayerEvent{NowPlaying: np}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestPlayerBarWidthIsStable(t *testing.T) {
	g := theme.NerdFont
	a := layoutBar(140, 3, &g, airLabel{name: "Living Room"}, false, 3*time.Minute)
	b := layoutBar(140, 3, &g, airLabel{name: "Living Room"}, false, 3*time.Minute)
	if a.progX != b.progX || a.progW != b.progW || a.volX != b.volX {
		t.Fatal("layout should not depend on time")
	}
	narrow := layoutBar(90, 3, &g, airLabel{name: "Living Room"}, false, 3*time.Minute)
	if narrow.airName != "" && narrow.lcdW < lcdMinW {
		t.Fatal("narrow bars drop the AirPlay name first")
	}
	for _, w := range []int{40, 60, 90, 140, 250} {
		l := layoutBar(w, 3, &g, airLabel{name: "Kitchen"}, true, time.Hour+time.Minute)
		if l.lcdX < 0 || l.lcdX+l.lcdW > w {
			t.Errorf("%d: lcd %d+%d", w, l.lcdX, l.lcdW)
		}
	}
}

func TestMouseDoubleClick(t *testing.T) {
	var c clickTracker
	now := time.Now()
	m := tea.Mouse{X: 3, Y: 4, Button: tea.MouseLeft}
	if c.press(now, m) {
		t.Fatal("first press is single")
	}
	if !c.press(now.Add(200*time.Millisecond), m) {
		t.Fatal("second press is double")
	}
	if c.press(now.Add(300*time.Millisecond), m) {
		t.Fatal("third press starts over")
	}
	c.press(now.Add(time.Second), m)
	if c.press(now.Add(time.Second+500*time.Millisecond), m) {
		t.Fatal("too slow")
	}
	c.press(now.Add(2*time.Second), m)
	if c.press(now.Add(2*time.Second+10*time.Millisecond), tea.Mouse{X: 4, Y: 4, Button: tea.MouseLeft}) {
		t.Fatal("different cell")
	}
}

func TestFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0:00", 62 * time.Second: "1:02", 3723 * time.Second: "1:02:03",
	} {
		if got := clock(d); got != want {
			t.Errorf("clock(%v) = %q", d, got)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 3158: "3,158", 1234567: "1,234,567", -4000: "-4,000"} {
		if got := count(n); got != want {
			t.Errorf("count(%d) = %q", n, got)
		}
	}
	for d, want := range map[time.Duration]string{
		42 * time.Minute: "42 min", 3*time.Hour + 12*time.Minute: "3 hr 12 min", 2 * time.Hour: "2 hr",
		28 * time.Hour: "28 hr", 62*time.Minute + 10*time.Second: "1 hr 2 min", 49 * time.Hour: "2 days", 9*24*time.Hour + 3*time.Hour: "9 days",
	} {
		if got := total(d); got != want {
			t.Errorf("total(%v) = %q, want %q", d, got, want)
		}
	}
	if deleteWord("daft punk ") != "daft " || deleteWord("x") != "" {
		t.Error("deleteWord")
	}
}

func TestSidebarFolders(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.render()
	s := a.sidebar
	n := len(s.entries)
	fi := -1
	for i, e := range s.entries {
		if e.kind == sbFolder {
			fi = i
			break
		}
	}
	if fi < 0 {
		t.Skip("no folders in demo library")
	}
	click(a, 3, a.lay.SidebarBody.Min.Y+fi-s.top)
	if len(s.entries) <= n {
		t.Fatal("clicking a folder should expand it")
	}
	child := s.entries[fi+1]
	if child.depth != 1 {
		t.Fatal("children are indented")
	}
	click(a, 6, a.lay.SidebarBody.Min.Y+fi+1-s.top)
	if a.nav.cur.Key() != child.key {
		t.Fatalf("clicking a child opens it: %s", a.nav.cur.Key())
	}
	a.sidebar.cursor = fi
	a.focus = focusSidebar
	press(a, "enter")
	if len(s.entries) != n {
		t.Fatal("enter on an open folder collapses it")
	}
}

func BenchmarkTrackTableRender50k(b *testing.B) {
	lib := benchLibrary(b)
	c := &Ctx{Theme: shellTheme(), Lib: lib, Focused: true}
	tt := NewTrackTable(lib.Songs, TableOptions{Wide: []Column{ColPlays, ColAdded}})
	tt.Render(200, 60, c)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Action(keymap.NavDown, 1, c)
		if tt.cursor == tt.Len()-1 {
			tt.Action(keymap.NavTop, 1, c)
		}
		tt.Render(200, 60, c)
	}
}

func BenchmarkTrackTableRenderPage50k(b *testing.B) {
	lib := benchLibrary(b)
	c := &Ctx{Theme: shellTheme(), Lib: lib, Focused: true}
	tt := NewTrackTable(lib.Songs, TableOptions{Wide: []Column{ColPlays, ColAdded}})
	tt.Render(200, 60, c)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Action(keymap.NavPageDown, 1, c)
		tt.cache = nil // every row rendered from scratch: the worst case
		tt.Render(200, 60, c)
	}
}

func BenchmarkAppRender(b *testing.B) {
	a := newShellApp(b, 200, 60)
	a.render()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.render()
	}
}

var (
	benchOnce sync.Once
	benchLib  *library.Library
)

func benchLibrary(b *testing.B) *library.Library {
	benchOnce.Do(func() { benchLib = library.Build(libtest.Snapshot(50000)) })
	return benchLib
}

// The config accepts exactly the sidebar widths the layout keeps.
func TestSidebarWidthMatchesConfig(t *testing.T) {
	if minSidebarWidth != config.MinSidebarWidth || maxSidebarWidth != config.MaxSidebarWidth {
		t.Fatalf("layout keeps %d..%d, config accepts %d..%d",
			minSidebarWidth, maxSidebarWidth, config.MinSidebarWidth, config.MaxSidebarWidth)
	}
	for _, w := range []int{config.MinSidebarWidth, 22, config.MaxSidebarWidth} {
		if got := sidebarWidth(w); got != w {
			t.Errorf("sidebar width %d drawn as %d", w, got)
		}
	}
}

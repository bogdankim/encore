package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/library/libtest"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

// gridTestHost records Host calls.
type gridTestHost struct {
	calls    []string
	artCalls int
	played   struct {
		src    player.Source
		tracks []*library.Track
		i      int
	}
	opened  View
	overlay Overlay
}

func (h *gridTestHost) rec(format string, args ...any) tea.Cmd {
	h.calls = append(h.calls, fmt.Sprintf(format, args...))
	return nil
}

func (h *gridTestHost) last() string {
	if len(h.calls) == 0 {
		return ""
	}
	return h.calls[len(h.calls)-1]
}

func (h *gridTestHost) Play(src player.Source, ts []*library.Track, i int) tea.Cmd {
	h.played.src, h.played.tracks, h.played.i = src, ts, i
	return h.rec("play %s %d", src.Kind, i)
}
func (h *gridTestHost) PlayNext(ts []*library.Track) tea.Cmd        { return h.rec("next %d", len(ts)) }
func (h *gridTestHost) AddToQueue(ts []*library.Track) tea.Cmd      { return h.rec("queue %d", len(ts)) }
func (h *gridTestHost) ToggleFavorite(ts []*library.Track) tea.Cmd  { return h.rec("fav %d", len(ts)) }
func (h *gridTestHost) OpenInMusic(kind, id, _ string) tea.Cmd      { return h.rec("music %s %s", kind, id) }
func (h *gridTestHost) Open(v View) tea.Cmd                         { h.opened = v; return h.rec("open %s", v.Key()) }
func (h *gridTestHost) OpenAlbum(a *library.Album) tea.Cmd          { return h.rec("album %s", a.Key) }
func (h *gridTestHost) OpenArtist(a *library.Artist) tea.Cmd        { return h.rec("artist %s", a.Name) }
func (h *gridTestHost) OpenPlaylist(p *library.Playlist) tea.Cmd    { return h.rec("playlist %s", p.PID) }
func (h *gridTestHost) OpenIn(act keymap.Action, s Subject) tea.Cmd { return h.rec("openIn %s", act) }
func (h *gridTestHost) PushOverlay(o Overlay) tea.Cmd               { h.overlay = o; return h.rec("overlay") }
func (h *gridTestHost) CloseOverlay() tea.Cmd                       { return h.rec("close") }
func (h *gridTestHost) Menu(x, y int, ts []*library.Track) tea.Cmd  { return h.rec("menu %d", len(ts)) }
func (h *gridTestHost) Status(msg string) tea.Cmd                   { return h.rec("status %s", msg) }
func (h *gridTestHost) Do(a keymap.Action) tea.Cmd                  { return h.rec("do %s", a) }

func (h *gridTestHost) Art(key, pid string, w, ht int, mode ArtMode) string {
	h.artCalls++
	row := strings.Repeat("▀", w)
	return strings.TrimSuffix(strings.Repeat(row+"\n", ht), "\n")
}

// gridTestPad is the margin grid tests use, to cover a non-zero pad.
const gridTestPad = 2

var albumTestLib = library.Build(libtest.Snapshot(1500))

func albumTestCtx(lib *library.Library) (*Ctx, *gridTestHost) {
	h := &gridTestHost{}
	return &Ctx{
		Theme:   theme.New(theme.Lookup("encore", true), theme.Unicode, false),
		Lib:     lib,
		Focused: true,
		Now:     time.Now(),
		Host:    h,
	}, h
}

// checkSize fails unless s is exactly w×h cells.
func checkSize(t *testing.T, name, s string, w, h int) {
	t.Helper()
	if w <= 0 || h <= 0 {
		if s != "" {
			t.Errorf("%s %dx%d: want empty, got %q", name, w, h, s)
		}
		return
	}
	lines := strings.Split(s, "\n")
	if len(lines) != h {
		t.Fatalf("%s %dx%d: %d lines", name, w, h, len(lines))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("%s %dx%d: line %d is %d wide: %q", name, w, h, i, got, ansi.Strip(l))
		}
	}
}

var renderSizes = [][2]int{
	{0, 0}, {1, 1}, {3, 2}, {8, 3}, {12, 5}, {20, 8}, {33, 11}, {40, 14},
	{55, 18}, {77, 24}, {96, 30}, {117, 37}, {140, 44}, {177, 50}, {260, 80},
}

func testAlbums(n int) []*library.Album {
	as := make([]*library.Album, n)
	for i := range as {
		as[i] = &library.Album{Key: fmt.Sprintf("K%05d", i), Title: fmt.Sprintf("Album %d", i), Artist: "Artist"}
	}
	return as
}

func TestLayoutGrid(t *testing.T) {
	for w := 20; w <= 300; w++ {
		g := layoutGrid(w, gridTestPad)
		if g.cols < 2 {
			t.Fatalf("w=%d: %d cols", w, g.cols)
		}
		if r := g.right(); r > contentRight(w) {
			t.Fatalf("w=%d: grid ends at %d, past %d", w, r, contentRight(w))
		}
		if w >= 60 && (g.tileW < tileMinW || g.tileW > tileMaxW) {
			t.Errorf("w=%d: tile width %d", w, g.tileW)
		}
		// the last column ends where every view's content does
		if w >= 60 && g.right() != contentRight(w) {
			t.Errorf("w=%d: grid ends at %d, not %d", w, g.right(), contentRight(w))
		}
		if g.gap < 2 || g.gap > 4 {
			t.Errorf("w=%d: gap %d", w, g.gap)
		}
		if g.artH != g.tileW/2 {
			t.Errorf("w=%d: art %dx%d is not square", w, g.tileW, g.artH)
		}
	}
}

func TestGridRenderSizes(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	for _, list := range []bool{false, true} {
		for _, n := range []int{0, 1, 7, 600} {
			g := NewAlbumGrid(gridTestPad)
			g.Reset(testAlbums(n))
			g.SetList(list)
			for _, sz := range renderSizes {
				checkSize(t, fmt.Sprintf("grid n=%d list=%v", n, list), g.Render(sz[0], sz[1], c, "No albums"), sz[0], sz[1])
			}
		}
	}
}

func TestGridRendersOnlyVisibleTiles(t *testing.T) {
	c, h := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	g.Reset(testAlbums(5000))
	g.Render(177, 50, c, "")
	geo := g.geo
	maxTiles := geo.cols * (50/geo.pitch() + 1)
	if h.artCalls > maxTiles {
		t.Fatalf("%d art calls for %d visible tiles", h.artCalls, maxTiles)
	}
	g.Action(keymap.NavBottom, 1, c)
	h.artCalls = 0
	g.Render(177, 50, c, "")
	if h.artCalls == 0 || h.artCalls > maxTiles {
		t.Fatalf("after G: %d art calls", h.artCalls)
	}
}

func TestGridNavigation(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	g.Reset(testAlbums(23))
	g.Render(117, 37, c, "")
	cols := g.geo.cols
	if cols != 5 {
		t.Fatalf("cols = %d, want 5 at 117", cols)
	}
	act := func(a keymap.Action, n int) bool {
		t.Helper()
		ok, _ := g.Action(a, n, c)
		return ok
	}
	if act(keymap.FocusLeft, 1) {
		t.Error("h at the left edge should fall through")
	}
	act(keymap.FocusRight, 2)
	act(keymap.NavDown, 1)
	if g.Cursor() != cols+2 {
		t.Fatalf("after l l j: %d", g.Cursor())
	}
	act(keymap.NavUp, 5)
	if g.Cursor() != 2 {
		t.Fatalf("k past the top keeps the column: %d", g.Cursor())
	}
	act(keymap.FocusRight, 9)
	if g.Cursor() != cols-1 {
		t.Fatalf("l stops at the row end: %d", g.Cursor())
	}
	if act(keymap.FocusRight, 1) {
		t.Error("l at the right edge should fall through")
	}
	act(keymap.NavDown, 10)
	if g.Cursor() != 22 {
		t.Fatalf("j into a short last row clamps to the last album: %d", g.Cursor())
	}
	if act(keymap.FocusRight, 1) {
		t.Error("l on the last album should fall through")
	}
	act(keymap.NavTop, 1)
	if g.Cursor() != 0 {
		t.Fatalf("gg: %d", g.Cursor())
	}
	act(keymap.NavBottom, 1)
	if g.Cursor() != 22 {
		t.Fatalf("G: %d", g.Cursor())
	}
	act(keymap.NavPageUp, 1)
	if g.Cursor() != 22-g.rowsVisible()*cols {
		t.Fatalf("pgup: %d", g.Cursor())
	}

	act(keymap.ViewToggleGrid, 1)
	if !g.IsList() {
		t.Fatal("v should switch to the list")
	}
	if act(keymap.FocusRight, 1) || act(keymap.FocusLeft, 1) {
		t.Error("h/l fall through in the list layout")
	}
	before := g.Cursor()
	act(keymap.NavDown, 3)
	if g.Cursor() != before+3 {
		t.Fatalf("list j moves by one: %d → %d", before, g.Cursor())
	}
}

func TestGridScrollKeepsCursorVisible(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	g.Reset(testAlbums(500))
	g.Render(117, 37, c, "")
	for range 40 {
		g.Action(keymap.NavDown, 1, c)
		row := g.Cursor() / g.geo.cols
		if row < g.top || row >= g.top+g.rowsVisible() {
			t.Fatalf("cursor row %d outside %d..%d", row, g.top, g.top+g.rowsVisible())
		}
	}
	g.Scroll(-1000)
	if g.top != 0 {
		t.Fatalf("scroll clamps at the top: %d", g.top)
	}
	g.Scroll(1000)
	if want := g.totalRows() - g.rowsVisible(); g.top != want {
		t.Fatalf("scroll clamps at the bottom: %d, want %d", g.top, want)
	}
}

func TestGridHitTest(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	g.Reset(testAlbums(12))
	g.Render(117, 37, c, "")
	geo := g.geo
	x1 := geo.pad + geo.stride() // left edge of column 1
	cases := []struct {
		x, y, want int
	}{
		{geo.pad, 0, 0},
		{x1, 0, 1},
		{x1 + geo.tileW - 1, geo.artH + 1, 1}, // artist line
		{x1 - 1, 0, -1},                       // gutter
		{x1, geo.artH + 2, -1},                // blank line under the tile
		{0, 0, -1},                            // margin
		{geo.pad, geo.pitch(), geo.cols},      // second row
		{geo.pad + geo.stride(), 2 * geo.pitch(), 2*geo.cols + 1},
		{geo.pad + 2*geo.stride(), 2 * geo.pitch(), -1}, // past the last album
		{-1, -1, -1},
	}
	for _, tc := range cases {
		if got := g.HitTest(tc.x, tc.y); got != tc.want {
			t.Errorf("HitTest(%d,%d) = %d, want %d", tc.x, tc.y, got, tc.want)
		}
	}
	g.Scroll(1)
	if got := g.HitTest(geo.pad, 0); got != geo.cols {
		t.Errorf("after scrolling a row: %d", got)
	}

	g.SetList(true)
	g.Scroll(-10)
	if got := g.HitTest(gridTestPad+4, 0); got != -1 {
		t.Errorf("list header: %d", got)
	}
	if got := g.HitTest(gridTestPad+4, 3); got != 2 {
		t.Errorf("list row: %d", got)
	}
}

func TestGridMouse(t *testing.T) {
	c, h := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	items := testAlbums(30)
	g.Reset(items)
	g.Render(117, 37, c, "")
	x := g.geo.pad + g.geo.stride() + 1

	g.Mouse(MouseEvent{Kind: MouseMotion, X: x, Y: 1}, c)
	if g.hover != 1 {
		t.Fatalf("hover = %d", g.hover)
	}
	g.Mouse(MouseEvent{Kind: MouseMotion, X: -1, Y: -1}, c)
	if g.hover != -1 {
		t.Fatalf("hover after leaving = %d", g.hover)
	}
	g.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: x, Y: 1}, c)
	if g.Cursor() != 1 || h.last() != "album "+items[1].Key {
		t.Fatalf("click opens: cursor %d, calls %v", g.Cursor(), h.calls)
	}
	g.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: x, Y: 1, Double: true}, c)
	if h.last() != "album "+items[1].Key {
		t.Fatalf("double-click: %v", h.calls)
	}
	badge := g.geo.pad + g.geo.stride()
	g.Mouse(MouseEvent{Kind: MouseMotion, X: badge, Y: g.geo.artH - 1}, c)
	if g.hover != 1 || !g.onPlay {
		t.Fatalf("badge hover: %d %v", g.hover, g.onPlay)
	}
	g.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: badge, Y: g.geo.artH - 1}, c)
	if h.last() != "play album 0" || h.played.src.ID != items[1].Key {
		t.Fatalf("badge click plays: %v", h.calls)
	}
	g.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseRight, X: g.geo.pad, Y: g.geo.pitch()}, c)
	if g.Cursor() != g.geo.cols || h.last() != "menu 0" {
		t.Fatalf("right-click: cursor %d, %v", g.Cursor(), h.calls)
	}
	g.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: x, Y: 1}, c)
	if g.top != 1 {
		t.Fatalf("wheel: top %d", g.top)
	}
}

func TestGridCommands(t *testing.T) {
	c, h := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	al := albumTestLib.Albums[3]
	g.Reset(albumTestLib.Albums)
	g.Select(al.Key)
	g.Render(117, 37, c, "")
	n := len(al.Tracks)
	for a, want := range map[keymap.Action]string{
		keymap.Open:          "album " + al.Key,
		keymap.QueueAdd:      fmt.Sprintf("queue %d", n),
		keymap.QueuePlayNext: fmt.Sprintf("next %d", n),
		keymap.Menu:          fmt.Sprintf("menu %d", n),
	} {
		if ok, _ := g.Action(a, 1, c); !ok || h.last() != want {
			t.Errorf("%s: handled=%v, %q, want %q", a, ok, h.last(), want)
		}
	}
	if ok, _ := g.Action(keymap.TrackFavorite, 1, c); ok {
		t.Error("f is left to the app")
	}
	empty := NewAlbumGrid(gridTestPad)
	if ok, _ := empty.Action(keymap.Open, 1, c); !ok {
		t.Error("enter on an empty grid is swallowed")
	}
}

func TestGridSetItemsKeepsSelection(t *testing.T) {
	g := NewAlbumGrid(gridTestPad)
	items := testAlbums(50)
	g.Reset(items)
	g.moveTo(17)
	g.SetItems(items[10:])
	if g.Selected() != items[17] {
		t.Fatalf("selection moved to %v", g.Selected())
	}
	g.SetItems(items[20:])
	if g.Cursor() != 7 {
		t.Fatalf("missing album keeps the index: cursor %d", g.Cursor())
	}
	g.SetItems(items[45:])
	if g.Cursor() != 4 {
		t.Fatalf("missing album past the end clamps: cursor %d", g.Cursor())
	}
}

func TestInlineFilterEdit(t *testing.T) {
	var f inlineFilter
	key := func(s string) tea.KeyPressMsg {
		switch s {
		case "backspace":
			return tea.KeyPressMsg{Code: tea.KeyBackspace}
		case "space":
			return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "ctrl+w":
			return tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}
		case "ctrl+u":
			return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
		}
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
	for _, k := range []string{"a", "b", "space", "c", "d"} {
		f.Edit(key(k))
	}
	if f.query != "ab cd" {
		t.Fatalf("typed %q", f.query)
	}
	f.Edit(key("backspace"))
	f.Edit(key("ctrl+w"))
	if f.query != "ab " {
		t.Fatalf("after backspace, ctrl+w: %q", f.query)
	}
	if !f.Edit(key("ctrl+u")) || f.query != "" {
		t.Fatalf("ctrl+u: %q", f.query)
	}
	if f.Edit(key("backspace")) {
		t.Fatal("backspace on empty reports a change")
	}
	if f.Edit(tea.KeyPressMsg{Code: 'x', Text: "x", Mod: tea.ModAlt}) {
		t.Fatal("alt+x typed text")
	}
}

func BenchmarkGridRender(b *testing.B) {
	c, _ := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	g.Reset(testAlbums(5000))
	g.Render(177, 50, c, "")
	g.moveTo(2500)
	for b.Loop() {
		g.Render(177, 50, c, "")
	}
}

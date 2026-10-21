package ui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

// homeTestPage has shelves of every item type, Live Radio and New.
func homeTestPage() *home.Page {
	items := func(typ, prefix string, n int) []home.Item {
		out := make([]home.Item, n)
		for i := range out {
			id := strconv.Itoa(1000 + i)
			if typ == home.TypeStation {
				id = "ra." + prefix + id
			}
			if typ == home.TypePlaylist {
				id = "pl." + prefix + id
			}
			out[i] = home.Item{Type: typ, ID: id, Title: prefix + " " + strconv.Itoa(i), Subtitle: "Someone",
				Art: home.Artwork{Template: "https://example.com/" + prefix + id + "/{w}x{h}{c}.{f}"}}
		}
		return out
	}
	mixes := items(home.TypePlaylist, "mix", 4)
	mixes[0].Tracks = []string{"501", "502"}
	live := items(home.TypeStation, "live", 3)
	for i := range live {
		live[i].IsLive, live[i].NowPlaying = true, "Show "+strconv.Itoa(i)
	}
	return &home.Page{
		Updated: time.Now().Add(-2 * time.Hour),
		Shelves: []home.Shelf{
			{ID: "s-albums", Title: "Albums for You", Items: items(home.TypeAlbum, "album", 12)},
			{ID: "s-stations", Title: "Stations for You", Items: items(home.TypeStation, "st", 3)},
			{ID: "s-mixes", Title: "Made for You", Items: mixes},
			{ID: "s-artists", Title: "Artists", Items: items(home.TypeArtist, "artist", 5)},
			{ID: "s-live", Title: "Live Radio", Kind: home.KindLive, Items: live},
		},
		New: []home.Shelf{
			{ID: "n-songs", Title: "Best New Songs", Items: items(home.TypeSong, "song", 9)},
		},
	}
}

// homeTestHost records Home's calls on top of the grid's.
type homeTestHost struct {
	gridTestHost
	station player.Station
	opened  string
}

func (h *homeTestHost) PlayStation(st player.Station) tea.Cmd {
	h.station = st
	return h.rec("station %s", st.ID)
}

func (h *homeTestHost) OpenInMusic(kind, id, name string) tea.Cmd {
	h.opened = kind + ":" + id
	return h.rec("music %s %s", kind, id)
}

func homeTestCtx() (*Ctx, *homeTestHost) {
	h := &homeTestHost{}
	return &Ctx{
		Theme:   theme.New(theme.Lookup("encore", true), theme.Unicode, false),
		Focused: true,
		Now:     time.Now(),
		Host:    h,
		Keys:    keymap.New(),
	}, h
}

func renderedHome(t *testing.T, w, h int) (*HomeView, *Ctx, *homeTestHost) {
	t.Helper()
	v := NewHomeView(homeTestPage())
	c, host := homeTestCtx()
	checkBlock(t, "home", v.Render(w, h, c), w, h)
	return v, c, host
}

func TestHomeRendersExactSize(t *testing.T) {
	for _, sz := range [][2]int{{60, 20}, {77, 23}, {117, 35}, {177, 48}, {30, 5}, {10, 2}} {
		v := NewHomeView(homeTestPage())
		c, _ := homeTestCtx()
		checkBlock(t, "home", v.Render(sz[0], sz[1], c), sz[0], sz[1])
		for range 20 {
			v.Action(keymap.NavDown, 1, c)
			v.Action(keymap.FocusRight, 3, c)
			checkBlock(t, "home moved", v.Render(sz[0], sz[1], c), sz[0], sz[1])
		}
	}
}

func TestHomeTitleAndNew(t *testing.T) {
	v, c, _ := renderedHome(t, 117, 200)
	out := ansi.Strip(v.Render(117, 200, c))
	first := strings.Split(out, "\n")[0]
	if !strings.Contains(first, "Home") || !strings.Contains(first, "updated 2 h ago") {
		t.Errorf("title line %q", first)
	}
	newAt, songsAt := strings.Index(out, "\nNew"), strings.Index(out, "Best New Songs")
	if newAt < 0 || songsAt < newAt || strings.Index(out, "Live Radio") > newAt {
		t.Errorf("New heading at %d, its shelf at %d:\n%s", newAt, songsAt, out)
	}
	if !strings.Contains(out, "Live · Show 0") {
		t.Error("live station without its show")
	}
	if !strings.Contains(out, "Station") {
		t.Error("station tiles without a subtitle")
	}
}

func TestHomeAgo(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{20 * time.Second, "just now"},
		{5 * time.Minute, "5 min ago"},
		{2*time.Hour + 10*time.Minute, "2 h ago"},
		{30 * time.Hour, "yesterday"},
		{3 * 24 * time.Hour, "3 days ago"},
		{20 * 24 * time.Hour, "Sep 30"},
	} {
		if got := ago(now, now.Add(-tc.d)); got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestHomeShelfNavigation(t *testing.T) {
	v, c, _ := renderedHome(t, 77, 23) // 4 tiles a row
	if v.cols() != 4 {
		t.Fatalf("%d columns", v.cols())
	}
	s := v.shelves[0]
	if ok, _ := v.Action(keymap.FocusLeft, 1, c); ok {
		t.Error("h at the first tile should let focus move on")
	}
	v.Action(keymap.FocusRight, 5, c)
	if s.cur != 5 || s.off != 2 {
		t.Errorf("after 5l: cur %d off %d, want 5, 2", s.cur, s.off)
	}
	// down keeps the column on screen (3 of 0..3), clamped to the shelf
	v.Action(keymap.NavDown, 1, c)
	if v.sel != 1 || v.shelves[1].cur != 2 {
		t.Errorf("after j: shelf %d item %d, want 1, 2", v.sel, v.shelves[1].cur)
	}
	v.Action(keymap.NavUp, 1, c)
	if v.sel != 0 || s.cur != 4 || s.off != 2 {
		t.Errorf("back up: shelf %d item %d, want 0, 4 (the same column)", v.sel, s.cur)
	}
	v.Action(keymap.FocusRight, 100, c)
	if s.cur != 11 || s.off != 8 {
		t.Errorf("at the end: cur %d off %d", s.cur, s.off)
	}
	if ok, _ := v.Action(keymap.FocusRight, 1, c); ok {
		t.Error("l at the last tile should let focus move on")
	}
	v.Action(keymap.NavBottom, 1, c)
	if v.sel != len(v.shelves)-1 {
		t.Errorf("G: shelf %d", v.sel)
	}
	v.Render(77, 23, c)
	if v.top == 0 || v.blocks[v.top].heading == "" && v.blocks[v.top].shelf > v.sel {
		t.Errorf("G left top at block %d", v.top)
	}
	v.Action(keymap.NavTop, 1, c)
	if v.sel != 0 || v.top != 0 {
		t.Errorf("gg: shelf %d top %d", v.sel, v.top)
	}
}

func TestHomeEnsureVisibleShowsHeading(t *testing.T) {
	v, c, _ := renderedHome(t, 77, 23)
	v.Action(keymap.NavBottom, 1, c) // Best New Songs, the first under New
	b := v.blockOf(v.sel)
	if v.blocks[b-1].heading != homeNewM || v.top != b-1 {
		t.Errorf("top %d, shelf block %d: the New heading should show above it", v.top, b)
	}
}

func TestHomeHorizontalScroll(t *testing.T) {
	v, c, _ := renderedHome(t, 77, 23)
	s := v.shelves[0]
	// the art of the first shelf starts on line listTop+1
	ev := MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelRight, X: 10, Y: listTop + 2}
	v.Mouse(ev, c)
	v.Mouse(ev, c)
	if s.off != 2 || s.cur != 2 {
		t.Errorf("after two right notches: off %d cur %d", s.off, s.cur)
	}
	ev.Button, ev.Mod = tea.MouseWheelUp, tea.ModShift
	v.Mouse(ev, c)
	if s.off != 1 {
		t.Errorf("shift+wheel up: off %d", s.off)
	}
	out := ansi.Strip(v.Render(77, 23, c))
	lines := strings.Split(out, "\n")
	mid := listTop + 1 + (v.geo.artH-1)/2
	if !strings.HasPrefix(lines[mid], "‹") || !strings.HasSuffix(lines[mid], "›") {
		t.Errorf("arrows missing on %q", lines[mid])
	}
	// a click on › pages the row
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 76, Y: mid}, c)
	if s.off != 5 {
		t.Errorf("› click: off %d", s.off)
	}
	// vertical wheel scrolls shelves, not the row
	v.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: 10, Y: listTop + 2}, c)
	if v.top != 1 || s.off != 5 {
		t.Errorf("wheel down: top %d off %d", v.top, s.off)
	}
}

func TestHomeActivation(t *testing.T) {
	v, c, host := renderedHome(t, 117, 200)
	at := func(shelf, item int) {
		v.sel = shelf
		v.shelves[shelf].cur = item
	}

	at(0, 1) // album: opens its catalog page
	v.Action(keymap.Open, 1, c)
	if host.last() != "album catalog:1001" {
		t.Errorf("album open: %q", host.last())
	}
	at(1, 2) // station
	v.Action(keymap.Open, 1, c)
	if host.station.ID != "ra.st1002" || host.station.Name != "st 2" || host.station.Kind != player.StationMood {
		t.Errorf("station: %+v", host.station)
	}
	at(4, 0) // live station
	v.Action(keymap.Open, 1, c)
	if host.station.Kind != player.StationLive {
		t.Errorf("live station kind %q", host.station.Kind)
	}
	at(2, 1) // playlist without songs: opens in Music
	v.Action(keymap.Open, 1, c)
	if host.opened != "playlist:pl.mix1001" {
		t.Errorf("playlist: %q", host.opened)
	}
	at(2, 0) // playlist with songs, but no catalog to look them up
	host.opened = ""
	v.Action(keymap.Open, 1, c)
	if host.opened != "playlist:pl.mix1000" {
		t.Errorf("playlist with songs, no catalog: %q", host.opened)
	}
	at(3, 0) // artist: page; the badge plays its station
	v.Action(keymap.Open, 1, c)
	if cv, ok := host.opened2(); !ok || cv != "catalog-artist:1000" {
		t.Errorf("artist open: %q", host.last())
	}
	v.Action(keymap.PagePlay, 1, c)
	if host.station.ID != player.ArtistStationID(1000) || host.station.Kind != player.StationArtist {
		t.Errorf("artist station: %+v", host.station)
	}
	at(5, 3) // song: plays with the shelf's songs as context
	v.Action(keymap.Open, 1, c)
	if host.played.i != 3 || len(host.played.tracks) != 9 || host.played.src.Kind != player.KindCatalog || host.played.src.Title != "Best New Songs" {
		t.Errorf("song: %d of %d from %+v", host.played.i, len(host.played.tracks), host.played.src)
	}
	if got := host.played.tracks[3].PID; got != "store:1003" {
		t.Errorf("song pid %q", got)
	}
	if sel := v.Selected(c); len(sel) != 1 || sel[0].PID != "store:1003" {
		t.Errorf("Selected: %v", sel)
	}
}

func (h *homeTestHost) opened2() (string, bool) {
	if h.gridTestHost.opened == nil {
		return "", false
	}
	return h.gridTestHost.opened.Key(), true
}

// lookCatalog looks songs up; the rest of Catalog is unused.
type lookCatalog struct {
	Catalog
	asked []int64
}

func (l *lookCatalog) LookupSongs(_ context.Context, ids []int64) ([]catalog.Song, error) {
	l.asked = ids
	var out []catalog.Song
	for _, id := range ids {
		out = append(out, catalog.Song{ID: id, Name: "Song " + strconv.FormatInt(id, 10), Artist: "A", Album: "B", AlbumID: 77, Duration: time.Minute, Streamable: true})
	}
	return out, nil
}

func runCatalog(t *testing.T, c *Ctx, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	msg, ok := cmd().(catalogResultMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	msg.apply(c)
}

func TestHomePlaylistWithSongsPlays(t *testing.T) {
	v, c, host := renderedHome(t, 117, 200)
	look := &lookCatalog{}
	c.Catalog = look
	v.sel = 2
	v.shelves[2].cur = 0
	_, cmd := v.Action(keymap.Open, 1, c)
	runCatalog(t, c, cmd)
	if len(look.asked) != 2 || host.played.src.ID != homePlaylistSource+"pl.mix1000" || len(host.played.tracks) != 2 {
		t.Errorf("asked %v, played %+v %d", look.asked, host.played.src, len(host.played.tracks))
	}
	if host.opened != "" {
		t.Errorf("opened in Music too: %q", host.opened)
	}
	// songs are looked up for their albums and lengths
	v.sel = 5
	v.shelves[5].cur = 4
	_, cmd = v.Action(keymap.Open, 1, c)
	runCatalog(t, c, cmd)
	if host.played.i != 4 || host.played.tracks[4].Album == nil || host.played.tracks[4].Duration != time.Minute {
		t.Errorf("looked-up song: %d %+v", host.played.i, host.played.tracks[4])
	}
}

func TestHomeMouse(t *testing.T) {
	v, c, host := renderedHome(t, 117, 200)
	var hint string
	c.hint = func(s string) { hint = s }
	badgeY := listTop + 1 + v.geo.artH - 1
	x := v.geo.colX(1) + 1
	v.Mouse(MouseEvent{Kind: MouseMotion, X: x, Y: badgeY}, c)
	if !v.hover.badge || hint != "Play Album" {
		t.Errorf("badge hover %v, hint %q", v.hover.badge, hint)
	}
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: x, Y: badgeY}, c)
	if v.sel != 0 || v.shelves[0].cur != 1 {
		t.Errorf("click selected %d/%d", v.sel, v.shelves[0].cur)
	}
	// the second press of a double-click does nothing more
	n := len(host.calls)
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: x, Y: badgeY, Double: true}, c)
	if len(host.calls) != n {
		t.Errorf("double-click ran %v", host.calls[n:])
	}
	// a playlist Music has to open: no badge, a hint instead
	mixY := -1
	for y, r := range v.rows {
		if r.shelf == 2 && r.kind == rowArt && r.j == v.geo.artH-1 {
			mixY = listTop + y
		}
	}
	v.Mouse(MouseEvent{Kind: MouseMotion, X: v.geo.colX(1) + 1, Y: mixY}, c)
	if v.hover.badge || hint != "Opens in Music.app" {
		t.Errorf("playlist hover: badge %v hint %q", v.hover.badge, hint)
	}
	// click on a station plays it
	stY := -1
	for y, r := range v.rows {
		if r.shelf == 1 && r.kind == rowCaption {
			stY = listTop + y
		}
	}
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: v.geo.colX(2) + 2, Y: stY}, c)
	if host.station.ID != "ra.st1002" {
		t.Errorf("station click: %q", host.last())
	}
}

func TestHomeEmptyState(t *testing.T) {
	c, _ := homeTestCtx()
	c.Home = &homeSource{}
	v := NewHomeView(nil)
	v.loaded(nil, home.ErrNotCached)
	out := ansi.Strip(v.Render(80, 20, c))
	if !strings.Contains(out, homeEmptyM) {
		t.Errorf("no empty state:\n%s", out)
	}
	v.loaded(nil, errors.New("disk on fire"))
	if out := ansi.Strip(v.Render(80, 20, c)); !strings.Contains(out, homeErrM) {
		t.Errorf("no error note:\n%s", out)
	}
	// a failed reload keeps the page
	v.loaded(homeTestPage(), nil)
	v.loaded(nil, home.ErrNotCached)
	if v.page == nil || strings.Contains(ansi.Strip(v.Render(80, 20, c)), homeEmptyM) {
		t.Error("a failed reload dropped the page")
	}
}

func TestHomeReloadKeepsPosition(t *testing.T) {
	v, c, _ := renderedHome(t, 77, 23)
	v.Action(keymap.NavDown, 2, c) // Made for You
	v.Action(keymap.FocusRight, 2, c)
	v.Render(77, 23, c)
	top := v.top

	p := homeTestPage()
	// a new shelf on top, and the selected mix moves one place right
	p.Shelves = append([]home.Shelf{{ID: "s-new", Title: "New Shelf", Items: p.Shelves[1].Items}}, p.Shelves...)
	mixes := p.Shelves[3].Items
	p.Shelves[3].Items = append([]home.Item{{Type: home.TypeAlbum, ID: "9"}}, mixes...)
	v.loaded(p, nil)
	s := v.cur()
	if s.ID != "s-mixes" || s.Items[s.cur].ID != "pl.mix1002" {
		t.Errorf("selection moved to %s/%s", s.ID, s.Items[s.cur].ID)
	}
	if v.blocks[v.top].shelf != v.blocks[top].shelf+1 {
		t.Errorf("top block %d, was %d", v.top, top)
	}
}

// homeSource counts calls.
type homeSource struct {
	mu      sync.Mutex
	page    *home.Page
	err     error
	loads   int
	changed bool
}

func (s *homeSource) Load(context.Context) (*home.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	return s.page, s.err
}

func (s *homeSource) Changed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

func TestHomePollsWhileShown(t *testing.T) {
	src := &homeSource{err: home.ErrNotCached}
	c, _ := homeTestCtx()
	c.Home = src
	v := NewHomeView(nil)
	if cmd := v.Init(c); cmd == nil || !v.loading || !v.polling {
		t.Fatalf("Init: loading %v polling %v", v.loading, v.polling)
	}
	v.loaded(src.Load(context.Background()))
	gen := v.pollGen

	// not drawn since: polling stops
	if cmd := v.poll(c, gen); cmd != nil || v.polling {
		t.Error("polled while off screen")
	}
	// drawn, cache changed: reads it again
	v.Init(c)
	v.Render(80, 30, c)
	src.mu.Lock()
	src.page, src.err, src.changed = homeTestPage(), nil, true
	src.mu.Unlock()
	runCatalog(t, c, v.poll(c, v.pollGen))
	if v.page == nil || len(v.shelves) == 0 {
		t.Error("poll didn't load the changed page")
	}
	// drawn, nothing changed: no read until homeLiveEvery
	src.mu.Lock()
	src.changed = false
	n := src.loads
	src.mu.Unlock()
	v.Render(80, 30, c)
	v.polls = 0
	runCatalog(t, c, v.poll(c, v.pollGen))
	if src.loads != n {
		t.Error("read an unchanged cache")
	}
}

func TestHomeStartView(t *testing.T) {
	a := newShellApp(t, 140, 42)
	if a.nav.cur.Key() != KeySongs {
		t.Fatal(a.nav.cur.Key())
	}
	press(a, "space", "g", "h")
	if a.nav.cur.Key() != KeyHome {
		t.Errorf("space g h opened %s", a.nav.cur.Key())
	}
	if keymap.Name(keymap.GoHome) != "Go to Home" {
		t.Errorf("name %q", keymap.Name(keymap.GoHome))
	}
	out := ansi.Strip(a.render())
	if !strings.Contains(out, homeEmptyM) {
		t.Error("Home without a source should say how to fill it")
	}
}

func TestHomeStationInBar(t *testing.T) {
	a := newShellApp(t, 140, 42)
	go func() {
		for ev := range a.client.Events() {
			a.player.Observe(ev)
		}
	}()
	a.PlayStation(player.Station{ID: "ra.u-1", Name: "Your Station", Kind: player.StationPersonal})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.refreshSnap()
		if a.snap.Station != nil && a.snap.NowPlaying.Track != nil && !a.snap.Loading {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	lines := strings.Split(ansi.Strip(a.render()), "\n")
	if !strings.Contains(lines[1], " · Your Station") {
		t.Errorf("bar line %q", lines[1])
	}
}

func TestHomeOpenInMusicStatus(t *testing.T) {
	a := newShellApp(t, 140, 42)
	var got string
	a.svc.OpenMusic = func(_ context.Context, kind, id string) error {
		got = kind + ":" + id
		return nil
	}
	cmd := a.OpenInMusic("playlist", "pl.x", "Chill Mix")
	a.Update(cmd())
	if got != "playlist:pl.x" {
		t.Errorf("opened %q", got)
	}
	lines := strings.Split(ansi.Strip(a.render()), "\n")
	if foot := lines[len(lines)-1]; !strings.Contains(foot, openingInMusicM) {
		t.Errorf("footer %q", foot)
	}
}

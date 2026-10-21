package ui

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

// openCall is one Services.OpenMusic call.
type openCall struct{ kind, id string }

// musicTestApp is an app on a fake bridge that knows the catalog
// fixture's songs, so it can play them. Music pages it opens are
// recorded.
func musicTestApp(t *testing.T) (*App, *bridge.Fake, *[]openCall) {
	t.Helper()
	f := bridge.NewFakeWithClock(1, time.Now)
	var reg []bridge.FakeCatalogSong
	for _, s := range fcSongs {
		reg = append(reg, bridge.FakeCatalogSong{StoreID: strconv.FormatInt(s.ID, 10), Name: s.Name, Artist: s.Artist, Album: s.Album})
	}
	f.RegisterCatalog(reg...)
	snap, err := f.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lib := library.Build(snap)
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := theme.New(theme.Lookup("encore", true), theme.Unicode, false)
	opened := &[]openCall{}
	a := New(Options{
		Client: f, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th },
		Services: Services{Catalog: &fakeCatalog{}, OpenMusic: func(_ context.Context, kind, id string) error {
			*opened = append(*opened, openCall{kind, id})
			return nil
		}},
	})
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return a, f, opened
}

// playCatalog has the fake play catalog song s of the fixture and the app
// see it as encore's catalog song.
func playCatalog(t *testing.T, a *App, f *bridge.Fake, s int) {
	t.Helper()
	song := fcSongs[s]
	if err := f.PlayStore(context.Background(), []string{strconv.FormatInt(song.ID, 10)}, 0); err != nil {
		t.Fatal(err)
	}
	np, _ := f.State(context.Background())
	it := player.CatalogItemFromSong(song, 100)
	a.snap = player.Snapshot{NowPlaying: np, Catalog: &it}
}

func TestMusicPageOf(t *testing.T) {
	c, _ := ovTestCtx(t)
	ct := catalogTracks(fcSongs[:1])[0]
	for name, tc := range map[string]struct {
		s    Subject
		want musicPage
	}{
		"catalog song":  {Subject{track: ct}, musicPage{"album", "21?i=31", "Paper Lanterns"}},
		"catalog album": {Subject{album: catalogAlbum(fcAlbum)}, musicPage{"album", "21", fcAlbum.Name}},
	} {
		if p, ok := musicPageOf(tc.s); !ok || p != tc.want {
			t.Errorf("%s: %+v %v, want %+v", name, p, ok, tc.want)
		}
	}
	// no link: a library song or album, an artist
	for name, s := range map[string]Subject{
		"library song":   {track: c.Lib.Songs[0]},
		"library album":  {album: c.Lib.Albums[0]},
		"library artist": {artist: c.Lib.Artists[0]},
		"catalog artist": {catArtist: fcArtist},
		"nothing":        {},
	} {
		if p, ok := musicPageOf(s); ok {
			t.Errorf("%s: %+v", name, p)
		}
	}

	// a song without its album is shown by its own id; bad ids have no page
	if p, ok := songPage("31", "", "x"); !ok || p.kind != "song" || p.id != "31" {
		t.Errorf("song without an album: %+v %v", p, ok)
	}
	if _, ok := songPage("31x", "21", "x"); ok {
		t.Error("song with a bad id")
	}
	if _, ok := albumPage("", "x"); ok {
		t.Error("album without an id")
	}

	for _, tc := range []struct {
		it   home.Item
		want musicPage
	}{
		{home.Item{Type: home.TypeAlbum, ID: "1000", Title: "A"}, musicPage{"album", "1000", "A"}},
		{home.Item{Type: home.TypeSong, ID: "1001", Title: "S"}, musicPage{"song", "1001", "S"}},
		{home.Item{Type: home.TypePlaylist, ID: "pl.x", Title: "P"}, musicPage{"playlist", "pl.x", "P"}},
	} {
		if p, ok := homeMusicPage(tc.it); !ok || p != tc.want {
			t.Errorf("%s: %+v %v", tc.it.Type, p, ok)
		}
	}
	for _, typ := range []string{home.TypeStation, home.TypeArtist} {
		if _, ok := homeMusicPage(home.Item{Type: typ, ID: "1"}); ok {
			t.Errorf("a %s has a Music page", typ)
		}
	}
}

func TestMusicMenus(t *testing.T) {
	c, h := ovTestCtx(t)
	c.Catalog = &fakeCatalog{}
	ts := catalogTracks(fcSongs[:1])

	// a catalog song: last, after the Open in group
	m, got := menuOf(c, NewMenu(10, 10, ts))
	if !strings.HasSuffix(got, "||Open in Music.app") {
		t.Fatalf("catalog song menu %q", got)
	}
	last := m.items[len(m.items)-1]
	if k := m.key(last, c); k != "M" {
		t.Errorf("shortcut %q", k)
	}
	last.Run(h)
	if h.last() != "OpenInMusic album 21?i=31 Paper Lanterns" {
		t.Errorf("run: %v", h.calls)
	}
	m.Action(keymap.MusicOpen, 1, c)
	if h.last() != "OpenInMusic album 21?i=31 Paper Lanterns" || len(h.calls) < 2 {
		t.Errorf("M in the menu: %v", h.calls)
	}

	// the same whether or not the library has the song, and while it plays
	it := player.CatalogItemFromSong(fcSongs[0], 100)
	c.Player = player.Snapshot{NowPlaying: bridge.NowPlaying{Track: &bridge.CurrentTrack{PID: "0A5A000000000031", Name: "Paper Lanterns"}}, Catalog: &it}
	if _, got := menuOf(c, NewMenu(10, 10, ts)); !strings.HasSuffix(got, "||Open in Music.app") {
		t.Errorf("playing catalog song menu %q", got)
	}
	// several songs that aren't an album have no one page
	if _, got := menuOf(c, NewMenu(10, 10, catalogTracks(fcSongs[:2]))); strings.Contains(got, "Music.app") {
		t.Errorf("two catalog songs: %q", got)
	}
	// library songs have no link, and nothing is added in their place
	if _, got := menuOf(c, NewMenu(10, 10, c.Lib.Songs[:1])); strings.Contains(got, "Music.app") || strings.Contains(got, "Add to Library") {
		t.Errorf("library song menu %q", got)
	}
}

// a menu for every song of a catalog album reads as the album
func TestMusicMenuItemsForAlbumSongs(t *testing.T) {
	al := catalogAlbum(fcAlbum)
	ts := catalogTracks(fcSongs)
	for _, tr := range ts {
		tr.Album = al
	}
	al.Tracks = ts
	items := musicMenuItems(ts)
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	_, h := ovTestCtx(t)
	items[0].Run(h)
	if h.last() != "OpenInMusic album 21 "+fcAlbum.Name {
		t.Errorf("run: %v", h.calls)
	}
}

func TestQueueMenuOpenInMusic(t *testing.T) {
	fx, q, _ := newQueueFixture(t)
	fx.pl.AddCatalogToQueue(player.CatalogItemFromSong(fcSongs[1], 100))
	fx.sync()
	q.Render(40, 20, fx.c)
	i := -1
	for k, tr := range fx.pl.Upcoming() {
		if player.IsCatalog(tr) {
			i = k
		}
	}
	if i < 0 {
		t.Fatal("no catalog song up next")
	}
	q.menu(0, 0, i, fx.c)
	if _, got := menuOf(fx.c, fx.h.pushed[len(fx.h.pushed)-1]); !strings.HasSuffix(got, "||Open in Music.app") {
		t.Errorf("Up Next catalog row menu %q", got)
	}
	// a library row has none
	q.menu(0, 0, 0, fx.c)
	if _, got := menuOf(fx.c, fx.h.pushed[len(fx.h.pushed)-1]); strings.Contains(got, "Music.app") {
		t.Errorf("Up Next library row menu %q", got)
	}
	// M on the row: the pane leaves it to the app, which opens its subject
	q.cursor = i
	if ok, _ := q.Action(keymap.MusicOpen, 1, fx.c); ok {
		t.Error("the pane took M itself")
	}
	if s := q.subject(fx.c); s.track == nil || !player.IsCatalog(s.track) {
		t.Errorf("subject %+v", s)
	}
}

func TestNowPlayingMenu(t *testing.T) {
	a, f, opened := musicTestApp(t)
	if a.snap = (player.Snapshot{}); a.NowPlayingMenu(5, 1) != nil || len(a.overlays) != 0 {
		t.Fatal("a menu with nothing playing")
	}
	playCatalog(t, a, f, 0)
	a.NowPlayingMenu(5, 1)
	m := a.topOverlay().(*MenuOverlay)
	m.Init(a.overlayCtx(m))
	if got := strings.Join(menuLabels(m), "|"); got != "Go to Album|Go to Artist||Open in Music.app" {
		t.Fatalf("catalog song: %q", got)
	}
	_, cmd := m.Action(keymap.MusicOpen, 1, a.overlayCtx(m))
	runQuick(cmd, func(msg tea.Msg) tea.Cmd { _, c := a.Update(msg); return c })
	if len(*opened) != 1 || (*opened)[0] != (openCall{"album", "21?i=31"}) || a.status.text != openingInMusicM {
		t.Errorf("opened %v, status %q", *opened, a.status.text)
	}
	a.CloseOverlay()

	lt := a.lib.Songs[0]
	a.snap = player.Snapshot{NowPlaying: bridge.NowPlaying{Track: &bridge.CurrentTrack{PID: lt.PID, Name: lt.Title}}, Track: lt}
	a.NowPlayingMenu(5, 1)
	m = a.topOverlay().(*MenuOverlay)
	m.Init(a.overlayCtx(m))
	if got := strings.Join(menuLabels(m), "|"); !strings.HasSuffix(got, "||Favorite") && !strings.HasSuffix(got, "||Unfavorite") || strings.Contains(got, "Music.app") {
		t.Errorf("library song: %q", got)
	}
}

func TestHomeOpenInMusic(t *testing.T) {
	v, c, h := renderedHome(t, 120, 40)
	at := func(shelf, item int) {
		v.sel = shelf
		v.shelves[shelf].cur = item
	}

	// an album and a playlist go to Music by their ids
	at(0, 2)
	v.Action(keymap.MusicOpen, 1, c)
	at(2, 1)
	v.Action(keymap.MusicOpen, 1, c)
	if got := strings.Join(h.calls, ","); got != "music album 1002,music playlist pl.mix1001" {
		t.Fatalf("calls %s", got)
	}

	// a station has no page
	h.calls = nil
	at(1, 0)
	v.Action(keymap.MusicOpen, 1, c)
	if got := strings.Join(h.calls, ","); got != "status "+musicOnlyM {
		t.Errorf("station: %s", got)
	}
	if musicHint(v.FooterHints(c)) != "" {
		t.Error("an Open in Music.app hint on a station")
	}
}

func TestHomeMusicFooterHints(t *testing.T) {
	v, c, _ := renderedHome(t, 120, 40)
	hints := func(shelf, item int) []FooterHint {
		v.sel = shelf
		v.shelves[shelf].cur = item
		return v.FooterHints(c)
	}
	// an album opens its page on enter: M is the other way
	if got := hints(0, 0); musicHint(got) != "open in Music.app" {
		t.Errorf("album hints %+v", got)
	}
	// a playlist Music has to show: enter does it, and the hint isn't repeated
	got := hints(2, 1)
	if musicHint(got) != "" {
		t.Errorf("playlist hints repeat it: %+v", got)
	}
	if got[1].Action != keymap.Open || got[1].Label != "open in Music.app" {
		t.Errorf("enter hint %+v", got[1])
	}
	// a playlist that plays: M is there
	if got := hints(2, 0); musicHint(got) != "open in Music.app" {
		t.Errorf("playable playlist hints %+v", got)
	}
}

func musicHint(hints []FooterHint) string {
	for _, h := range hints {
		if h.Action == keymap.MusicOpen {
			return h.Label
		}
	}
	return ""
}

func TestHomePlaylistMenus(t *testing.T) {
	v, c, _ := renderedHome(t, 120, 40)
	// Play, then Open in Music.app
	m, got := menuOf(c, homePlaylistMenu(v.shelves[2].Items[0], 10, 10))
	if got != "Play||Open in Music.app" {
		t.Errorf("playable playlist menu %q", got)
	}
	if k := m.key(m.items[2], c); k != "M" {
		t.Errorf("shortcut %q", k)
	}
	// the one that only opens in Music has it once, with its own key
	m, got = menuOf(c, homePlaylistMenu(v.shelves[2].Items[1], 10, 10))
	if got != "Open in Music.app" {
		t.Errorf("playlist menu %q", got)
	}
	if k := m.key(m.items[0], c); k != "M" {
		t.Errorf("shortcut %q", k)
	}
}

func TestMusicOpenKeys(t *testing.T) {
	a, f, opened := musicTestApp(t)
	deliver := func(m tea.Msg) tea.Cmd { _, c := a.Update(m); return c }

	// a library song has no page
	a.goSection(KeySongs, "Songs")
	press(a, "M")
	if a.status.text != musicOnlyM || len(*opened) != 0 {
		t.Errorf("M on a library song: %q %v", a.status.text, *opened)
	}

	// a catalog album page: M opens the selected song on its album
	runCmd(a.Open(newCatalogAlbumView(fcAlbum)), deliver)
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	runQuick(cmd, deliver)
	want := openCall{"album", "21?i=31"}
	if len(*opened) != 1 || (*opened)[0] != want || a.status.text != openingInMusicM {
		t.Fatalf("M on a catalog song: %q %v", a.status.text, *opened)
	}

	// space o m, and the command palette, do the same
	press(a, "space", "o")
	_, cmd = a.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	runQuick(cmd, deliver)
	runQuick(a.Do(keymap.MusicOpen), deliver)
	if len(*opened) != 3 || (*opened)[1] != want || (*opened)[2] != want {
		t.Fatalf("space o m, Do: %v", *opened)
	}

	// the playing song is not the selection
	playCatalog(t, a, f, 1)
	runQuick(a.Do(keymap.MusicOpen), deliver)
	if (*opened)[3] != want {
		t.Errorf("with a song playing: %v", *opened)
	}
}

func TestMusicOpenKeyOnHome(t *testing.T) {
	a, _, opened := musicTestApp(t)
	a.o.HomePage = homeTestPage()
	delete(a.sections, KeyHome)
	a.goSection(KeyHome, "Home")
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	runQuick(cmd, func(m tea.Msg) tea.Cmd { _, c := a.Update(m); return c })
	if len(*opened) != 1 || (*opened)[0] != (openCall{"album", "1000"}) || a.status.text != openingInMusicM {
		t.Fatalf("M on a Home album: %q %v", a.status.text, *opened)
	}
	press(a, "j", "M")
	if a.status.text != musicOnlyM {
		t.Errorf("M on a station: %q", a.status.text)
	}
}

func TestOpenInMusicStatus(t *testing.T) {
	a, _, opened := musicTestApp(t)
	a.Update(a.OpenInMusic("playlist", "pl.x", "Chill Mix")())
	if len(*opened) != 1 || (*opened)[0] != (openCall{"playlist", "pl.x"}) || a.status.text != openingInMusicM {
		t.Errorf("opened %v, status %q", *opened, a.status.text)
	}
	a.svc.OpenMusic = func(context.Context, string, string) error { return context.DeadlineExceeded }
	a.Update(a.OpenInMusic("playlist", "pl.x", "Chill Mix")())
	if a.status.text != "Couldn’t open Chill Mix in Music.app" || !a.status.err {
		t.Errorf("failed: %q", a.status.text)
	}
	a.svc.OpenMusic = nil
	a.OpenInMusic("playlist", "pl.x", "Chill Mix")
	if a.status.text != "Music.app can’t open Chill Mix from here" {
		t.Errorf("without OpenMusic: %q", a.status.text)
	}
}

func TestHelpListsOpenInMusic(t *testing.T) {
	c, _ := ovTestCtx(t)
	o := NewHelp().(*HelpOverlay)
	o.Rect(100, 30)
	o.Render(c)
	ovType(o, c, "music.app")
	secs := o.filtered()
	if len(secs) != 1 || len(secs[0].entries) != 1 {
		t.Fatalf("sections %+v", secs)
	}
	if e := secs[0].entries[0]; e.desc != "Open in Music.app" || !strings.Contains(e.keys, "M") || !strings.Contains(e.keys, "space o m") {
		t.Errorf("entry %+v", e)
	}
}

func TestPaletteListsOpenInMusic(t *testing.T) {
	c, h := ovTestCtx(t)
	o := NewPalette().(*PaletteOverlay)
	ovType(o, c, "music.app")
	if len(o.rows) == 0 || o.items[o.rows[0].item].action != keymap.MusicOpen {
		t.Fatalf("rows %v", o.rows)
	}
	if got := o.items[o.rows[0].item].keys; !strings.Contains(got, "M") {
		t.Errorf("keys %q", got)
	}
	o.Action(keymap.InputSubmit, 1, c)
	if h.last() != "Do music.open" {
		t.Errorf("calls %v", h.calls)
	}
}

func TestCatalogAlbumHeaderOpenInMusic(t *testing.T) {
	c, h := ovTestCtx(t)
	c.Catalog = &fakeCatalog{}
	v := newCatalogAlbumView(fcAlbum)
	runCmd(v.Init(c), func(m tea.Msg) tea.Cmd {
		if r, ok := m.(catalogResultMsg); ok {
			return r.apply(c)
		}
		return nil
	})
	if got := ansi.Strip(v.Render(120, 40, c)); !strings.Contains(got, "Apple Music · Open in Music.app") {
		t.Fatalf("header without the link:\n%s", got)
	}
	zone := -1
	for i, z := range v.zones {
		if strings.Contains(z.hint, "Open in Music.app") {
			zone = i
			z.do()
		}
	}
	if zone < 0 || h.last() != "OpenInMusic album 21 "+fcAlbum.Name {
		t.Fatalf("link: zone %d, %v", zone, h.calls)
	}
	if !strings.Contains(v.zones[zone].hint, "M") {
		t.Errorf("hint %q names no key", v.zones[zone].hint)
	}

	// the library having the album changes nothing about it
	lib := c.Lib.Albums[0]
	v.lib, v.libAlbum = c.Lib, lib
	if got := ansi.Strip(v.Render(120, 40, c)); !strings.Contains(got, "Apple Music · In Your Library · Open in Music.app") {
		t.Errorf("header with the album in the library:\n%s", got)
	}
}

// runQuick is runCmd for commands that answer at once: a tick (any
// command that takes longer than a moment) is dropped.
func runQuick(cmd tea.Cmd, deliver func(tea.Msg) tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(200 * time.Millisecond):
		return
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			runQuick(c, deliver)
		}
	default:
		runQuick(deliver(m), deliver)
	}
}

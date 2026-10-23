package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/player"
)

// stationFixture is an app with a library and a catalog, playing a station
// song under a temporary pid, the way Music reports one.
func stationFixture(t *testing.T) (*App, *fakeCatalog, *[]openCall) {
	t.Helper()
	a, _, opened := musicTestApp(t)
	fc := &fakeCatalog{}
	a.svc.Catalog = fc
	return a, fc, opened
}

const stationPID = "8C6A1776D2D9C0CC"

func stationSong(a *App, pid, title, artist, album string) {
	a.player.Observe(bridge.PlayerEvent{NowPlaying: bridge.NowPlaying{
		State: bridge.Playing, Volume: 50,
		Track: &bridge.CurrentTrack{PID: pid, Name: title, Artist: artist, Album: album, Duration: 200},
	}})
	a.refreshSnap()
}

// runFind runs the commands cmd holds and hands the catalog results among
// them to the app, and runs what the app answers once (opening Music), but
// not what that answers (a page's loads, the footer's timer).
func runFind(a *App, cmd tea.Cmd) { runFindDepth(a, cmd, 2) }

func runFindDepth(a *App, cmd tea.Cmd, depth int) {
	if cmd == nil || depth == 0 {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		switch m := msg.(type) {
		case tea.BatchMsg:
			for _, c := range m {
				runFindDepth(a, c, depth)
			}
		case catalogResultMsg:
			_, c := a.Update(m)
			runFindDepth(a, c, depth-1)
		}
	case <-time.After(200 * time.Millisecond): // a timer
	}
}

// A station song whose album is in the library opens the library's album
// with the song selected, or the album alone when it lacks the song, and
// never asks the catalog.
func TestGoToAlbumOfStationSongInLibrary(t *testing.T) {
	a, fc, _ := stationFixture(t)
	s := a.lib.Songs[3]
	stationSong(a, stationPID, s.Title, s.Artist, s.AlbumName)
	if a.snap.Track != nil || a.snap.Catalog != nil {
		t.Fatalf("setup: the song is in the library: %+v", a.snap.Track)
	}
	cmd := a.Do(keymap.GoNowPlaying)
	runFind(a, cmd)
	v, ok := a.nav.cur.(*AlbumView)
	if !ok || v.album != s.Album || v.want.pid != s.PID {
		t.Fatalf("opened %T %+v, want the library album with %s selected", a.nav.cur, v, s.PID)
	}

	// the album is there, the recording on it isn't
	a.nav.open(a.section(KeySongs))
	stationSong(a, stationPID+"1", "A Song Nobody Has", s.Artist, s.AlbumName)
	runFind(a, a.Do(keymap.GoNowPlaying))
	v, ok = a.nav.cur.(*AlbumView)
	if !ok || v.album != s.Album || v.want != (trackWant{}) {
		t.Fatalf("opened %T, want the library album alone", a.nav.cur)
	}
	if q := fc.queries(); len(q) != 0 {
		t.Fatalf("the catalog was asked: %v", q)
	}
}

// A station song by an artist the library has opens the library's artist.
func TestGoToArtistOfStationSongInLibrary(t *testing.T) {
	a, fc, _ := stationFixture(t)
	s := a.lib.Songs[3]
	stationSong(a, stationPID, "A Song Nobody Has", s.Artist, "No Such Album")
	runFind(a, a.openPlayingArtist())
	if v, ok := a.nav.cur.(*ArtistView); !ok || v.Key() != "artist:"+libraryArtistForCredit(a.lib, s.Artist).Name {
		t.Fatalf("opened %T", a.nav.cur)
	}
	if q := fc.queries(); len(q) != 0 {
		t.Fatalf("the catalog was asked: %v", q)
	}
}

// A station song that isn't in the library is found in the catalog: its
// album opens with the song selected. The search runs once per song.
func TestGoToAlbumOfStationSongInCatalog(t *testing.T) {
	a, fc, _ := stationFixture(t)
	song := fcSongs[1]
	stationSong(a, stationPID, song.Name, song.Artist, song.Album)
	before := a.nav.cur

	cmd := a.Do(keymap.GoNowPlaying)
	if a.status.text != findAlbumM {
		t.Fatalf("status %q while looking", a.status.text)
	}
	if a.nav.cur != before {
		t.Fatal("opened a page before the search ended")
	}
	runFind(a, cmd)
	v, ok := a.nav.cur.(*CatalogAlbumView)
	if !ok || v.seed.ID != 21 || v.want.pid != "store:32" || v.want.title != "Slow Parade" {
		t.Fatalf("opened %T %+v", a.nav.cur, a.nav.cur)
	}
	if a.status.text == findAlbumM {
		t.Error("still says it is looking")
	}

	// again: from memory, with no search
	a.nav.open(a.section(KeySongs))
	runFind(a, a.Do(keymap.GoNowPlaying))
	if _, ok := a.nav.cur.(*CatalogAlbumView); !ok {
		t.Fatalf("second time opened %T", a.nav.cur)
	}
	if q := fc.queries(); len(q) != 1 || q[0] != song.Name+" "+song.Artist {
		t.Fatalf("searches %q, want one for the song", q)
	}
}

func TestGoToArtistOfStationSongInCatalog(t *testing.T) {
	a, fc, _ := stationFixture(t)
	song := fcSongs[1]
	stationSong(a, stationPID, song.Name, song.Artist, song.Album)
	cmd := a.openPlayingArtist()
	if a.status.text != findArtistM {
		t.Fatalf("status %q while looking", a.status.text)
	}
	runFind(a, cmd)
	v, ok := a.nav.cur.(*CatalogArtistView)
	if !ok || v.seed.ID != 11 || v.seed.Name != song.Artist {
		t.Fatalf("opened %T", a.nav.cur)
	}
	// the album uses the same answer
	runFind(a, a.Do(keymap.GoNowPlaying))
	if _, ok := a.nav.cur.(*CatalogAlbumView); !ok || len(fc.queries()) != 1 {
		t.Fatalf("opened %T after %d searches", a.nav.cur, len(fc.queries()))
	}
}

// A song the catalog doesn't have says there is no page, and a failed
// search says why.
func TestGoToStationSongWithoutAMatch(t *testing.T) {
	a, fc, _ := stationFixture(t)
	stationSong(a, stationPID, "Nothing Like It", "Nobody At All", "Nowhere")
	before := a.nav.cur
	runFind(a, a.Do(keymap.GoNowPlaying))
	if a.status.text != "No album page for this song" || a.nav.cur != before {
		t.Fatalf("album: status %q, page %T", a.status.text, a.nav.cur)
	}
	runFind(a, a.openPlayingArtist())
	if a.status.text != "No artist page for this song" || a.nav.cur != before {
		t.Fatalf("artist: status %q, page %T", a.status.text, a.nav.cur)
	}
	if len(fc.queries()) != 1 {
		t.Fatalf("searches %q, want one", fc.queries())
	}

	fc.err = context.DeadlineExceeded
	stationSong(a, stationPID+"1", "Another One", "Nobody At All", "Nowhere")
	runFind(a, a.Do(keymap.GoNowPlaying))
	if a.status.text != catalogOfflineM || a.nav.cur != before {
		t.Fatalf("failed search: status %q", a.status.text)
	}
	// nothing was settled: the next try searches again
	fc.err = nil
	runFind(a, a.Do(keymap.GoNowPlaying))
	if a.status.text != "No album page for this song" || len(fc.queries()) != 3 {
		t.Fatalf("retry: status %q after %d searches", a.status.text, len(fc.queries()))
	}

	// without a catalog there is nothing to ask
	a.svc.Catalog = nil
	stationSong(a, stationPID+"2", "Third One", "Nobody At All", "Nowhere")
	runFind(a, a.Do(keymap.GoNowPlaying))
	if a.status.text != catalogOfflineM {
		t.Fatalf("no catalog: status %q", a.status.text)
	}
}

// When the library has the song only on another album and the catalog has
// no better, that album opens.
func TestGoToAlbumFallsBackToTheSongInTheLibrary(t *testing.T) {
	a, _, _ := stationFixture(t)
	s := a.lib.Songs[3]
	stationSong(a, stationPID, s.Title, s.Artist, "A Compilation")
	runFind(a, a.Do(keymap.GoNowPlaying))
	if v, ok := a.nav.cur.(*AlbumView); !ok || v.album != s.Album || v.want.pid != s.PID {
		t.Fatalf("opened %T", a.nav.cur)
	}
}

// The song changing while the search runs opens nothing, and says nothing
// about it.
func TestGoToStationSongThatChangedMeanwhile(t *testing.T) {
	a, fc, _ := stationFixture(t)
	song := fcSongs[1]
	stationSong(a, stationPID, song.Name, song.Artist, song.Album)
	before := a.nav.cur
	cmd := a.Do(keymap.GoNowPlaying)

	stationSong(a, stationPID+"1", "Nothing Like It", "Nobody At All", "Nowhere")
	runFind(a, cmd)
	if a.nav.cur != before {
		t.Fatalf("opened %T for a song that stopped playing", a.nav.cur)
	}
	if a.status.text == findAlbumM {
		t.Error("still says it is looking")
	}
	// the answer is kept for that song
	if _, found, resolved := a.outside.resolvedSong(stationPID); !resolved || !found {
		t.Errorf("answer for the first song: found %v resolved %v", found, resolved)
	}
	if len(fc.queries()) != 1 {
		t.Fatalf("searches %q", fc.queries())
	}
}

// A live station has songs to nobody: no page, no search.
func TestGoToAlbumOfLiveStation(t *testing.T) {
	a, fc, _ := stationFixture(t)
	song := fcSongs[1]
	stationSong(a, stationPID, song.Name, song.Artist, song.Album)
	a.snap.Live = true
	runFind(a, a.Do(keymap.GoNowPlaying))
	if a.status.text != "No album page for this song" || len(fc.queries()) != 0 {
		t.Fatalf("status %q, searches %q", a.status.text, fc.queries())
	}
	if _, ok := a.outsideMenuItem(); ok {
		t.Error("Open in Music.app for a live station")
	}
	a.snap.Live = false
	stationSong(a, stationPID+"1", "", "", "")
	a.snap.NowPlaying.Track.Duration = 0
	runFind(a, a.Do(keymap.GoNowPlaying))
	if a.status.text != "No album page for this song" || len(fc.queries()) != 0 {
		t.Fatalf("a stream without album: status %q, searches %q", a.status.text, fc.queries())
	}
}

// The fake's stations: a made-up song the catalog doesn't know has no page,
// and a live one none either.
func TestGoToAlbumOnTheFakeStations(t *testing.T) {
	a, fc, _ := stationFixture(t)
	f := a.client.(*bridge.Fake)
	for _, id := range []string{"ra.725086732", "ra.978194965"} {
		live := bridge.FakeLiveStation(id)
		st := player.Station{ID: id, Name: "Station"}
		if live {
			st.Kind = player.StationLive
		}
		a.player.PlayStation(player.Source{Kind: player.KindStation}, st)
		waitFor(t, func() bool {
			np, _ := f.State(context.Background())
			if np.Track == nil {
				return false
			}
			a.player.Observe(bridge.PlayerEvent{NowPlaying: np})
			a.refreshSnap()
			return a.snap.Station != nil && a.snap.Station.ID == id && a.snap.NowPlaying.Track != nil && !a.snap.Loading
		})
		if a.snap.Track != nil || a.snap.Catalog != nil || a.snap.Live != live {
			t.Fatalf("%s: track %v catalog %v live %v", id, a.snap.Track, a.snap.Catalog, a.snap.Live)
		}
		runFind(a, a.Do(keymap.GoNowPlaying))
		if a.status.text != "No album page for this song" {
			t.Errorf("%s: status %q", id, a.status.text)
		}
	}
	if len(fc.queries()) != 1 {
		t.Errorf("searches %q, want one (the live station asks none)", fc.queries())
	}
}

// The playing song's menu offers Open in Music.app for a station song once
// it can be found, and says nothing it can't.
func TestNowPlayingMenuForStationSong(t *testing.T) {
	a, fc, opened := stationFixture(t)
	song := fcSongs[1]
	stationSong(a, stationPID, song.Name, song.Artist, song.Album)
	menu := func() (*MenuOverlay, string) {
		a.NowPlayingMenu(5, 1)
		m := a.topOverlay().(*MenuOverlay)
		m.Init(a.overlayCtx(m))
		a.CloseOverlay()
		return m, strings.Join(menuLabels(m), "|")
	}
	m, got := menu()
	if got != "Go to Album|Go to Artist||Open in Music.app" {
		t.Fatalf("station song: %q", got)
	}
	if len(fc.queries()) != 0 {
		t.Fatal("opening the menu searched")
	}
	last := m.items[len(m.items)-1]
	cmd := last.Run(a)
	if a.status.text != findSongM {
		t.Errorf("status %q while looking", a.status.text)
	}
	runFind(a, cmd)
	if len(*opened) != 1 || (*opened)[0] != (openCall{"album", "21?i=32"}) {
		t.Fatalf("opened %v, status %q", *opened, a.status.text)
	}
	// found: the item opens it at once
	m, got = menu()
	if got != "Go to Album|Go to Artist||Open in Music.app" {
		t.Fatalf("found song: %q", got)
	}
	runFind(a, m.items[len(m.items)-1].Run(a))
	if len(*opened) != 2 || len(fc.queries()) != 1 {
		t.Fatalf("opened %v after %d searches", *opened, len(fc.queries()))
	}

	// a song the catalog lacks has no item once that is known
	stationSong(a, stationPID+"1", "Nothing Like It", "Nobody At All", "Nowhere")
	m, got = menu()
	if got != "Go to Album|Go to Artist||Open in Music.app" {
		t.Fatalf("unknown song: %q", got)
	}
	runFind(a, m.items[len(m.items)-1].Run(a))
	if a.status.text != notInCatalogM || len(*opened) != 2 {
		t.Fatalf("status %q, opened %v", a.status.text, *opened)
	}
	if _, got = menu(); got != "Go to Album|Go to Artist" {
		t.Fatalf("known absent: %q", got)
	}

	// no catalog: nothing to offer
	a.svc.Catalog = nil
	stationSong(a, stationPID+"2", "Third One", "Nobody At All", "Nowhere")
	if _, got = menu(); got != "Go to Album|Go to Artist" {
		t.Fatalf("no catalog: %q", got)
	}
}

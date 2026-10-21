package ui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// gatedArt holds artwork loads for some pids until they are let go, or
// answers that they have none.
type gatedArt struct {
	bridge.Client
	mu   sync.Mutex
	hold map[string]chan struct{}
	none map[string]bool
}

func (g *gatedArt) Artwork(ctx context.Context, pid string, size int) (bridge.Artwork, error) {
	g.mu.Lock()
	ch, none := g.hold[pid], g.none[pid]
	g.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return bridge.Artwork{}, ctx.Err()
		}
	}
	if none {
		return bridge.Artwork{}, nil
	}
	return g.Client.Artwork(ctx, pid, size)
}

// songsOnAlbums returns n library songs, each on a different album with art.
func songsOnAlbums(t *testing.T, lib *library.Library, n int) []*library.Track {
	t.Helper()
	var out []*library.Track
	seen := map[string]bool{}
	for _, s := range lib.Songs {
		if s.Album == nil || s.Album.ArtPID == "" || seen[s.Album.Key] {
			continue
		}
		seen[s.Album.Key] = true
		out = append(out, s)
		if len(out) == n {
			return out
		}
	}
	t.Fatalf("fewer than %d albums with art", n)
	return nil
}

// barArt is the key of the art placed in the player bar in the last frame,
// or "".
func barArt(a *App) string {
	for _, p := range a.frameArt {
		if p.box.Min.Y == 0 && p.box.Dx() == artW && p.box.Dy() == artH {
			return p.key
		}
	}
	return ""
}

// When the track changes, the player bar keeps the last track's art until
// the new art is ready, and shows nothing (the placeholder) once Music has
// said for nowArtHold that the new track has none.
func TestNowPlayingArtWaitsForTheNextTrack(t *testing.T) {
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.nav.open(a.section(KeySongs))
	clk := &testClock{t: time.Unix(1_000_000, 0)}
	a.art.now = clk.now
	gate := &gatedArt{Client: a.art.client, hold: map[string]chan struct{}{}, none: map[string]bool{}}
	a.art.client = gate
	ss := songsOnAlbums(t, a.lib, 3)
	release := make(chan struct{})
	gate.hold[ss[1].Album.ArtPID] = release
	gate.none[ss[2].Album.ArtPID] = true

	a.player.PlayContext(player.Source{Kind: player.KindSongs}, ss, 0)
	a.refreshSnap()
	waitFor(t, func() bool {
		a.View()
		return barArt(a) == ss[0].Album.Key
	})

	a.player.Next()
	a.refreshSnap()
	for range 5 {
		a.View()
		if got := barArt(a); got != ss[0].Album.Key {
			t.Fatalf("while the next art loads the bar shows %q, want the last track's %q", got, ss[0].Album.Key)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	waitFor(t, func() bool {
		a.View()
		return barArt(a) == ss[1].Album.Key
	})

	a.player.Next()
	a.refreshSnap()
	waitFor(t, func() bool { a.View(); return a.art.answeredNone(ss[2].Album.ArtPID) })
	a.View()
	if got := barArt(a); got != ss[1].Album.Key {
		t.Fatalf("right after Music said none the bar shows %q, want the last track's %q", got, ss[1].Album.Key)
	}
	clk.add(nowArtHold)
	waitFor(t, func() bool {
		a.View()
		return barArt(a) == ""
	})
}

// A skipped track's art that no frame asks for any more is not loaded: the
// workers go to the track that is playing now.
func TestArtDropsRequestsNoFrameWants(t *testing.T) {
	s := newArtStore(nil, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	s.started = true // no workers: requests stay queued
	s.frame()
	s.block("skipped", "pid-skipped", 6, 3, ArtHero)
	s.frame()
	s.block("previous", "pid-previous", 6, 3, ArtHero)
	s.frame()
	s.block("bar", "pid-bar", 6, 3, ArtHero)
	s.block("tile", "pid-tile", 10, 5, ArtTile)
	s.mu.Lock()
	s.pruneLocked()
	var got []string
	for _, j := range s.queue {
		got = append(got, j.req.key)
	}
	_, busy := s.busy[artReq{key: "skipped", pid: "pid-skipped", w: 6, h: 3, hero: true}]
	s.mu.Unlock()
	if len(got) != 3 || busy {
		t.Fatalf("queue %v (skipped busy %v), want the last two frames' requests", got, busy)
	}
	// asked for again, it is queued again
	s.block("skipped", "pid-skipped", 6, 3, ArtHero)
	if len(s.queue) != 4 {
		t.Fatalf("queue %d after asking again", len(s.queue))
	}
}

// Hero art is served before tiles the same frame asked for first.
func TestHeroArtLoadsBeforeTiles(t *testing.T) {
	s := newArtStore(nil, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	s.started = true
	s.frame()
	s.block("tile", "pid-tile", 10, 5, ArtTile)
	s.block("bar", "pid-bar", 6, 3, ArtHero)
	if first := s.queue[s.firstLocked()].req.key; first != "bar" {
		t.Fatalf("served first: %s", first)
	}
}

// Music can report no artwork, or time out, for a track that has just
// started. Hero art asks again after a second, then less and less often,
// but never gives up while it is on screen; tiles keep the first answer.
func TestHeroArtRetriesMissingArt(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newArtStore(nil, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	s.now = func() time.Time { return now }
	s.after = func(time.Duration, func()) {}
	s.started = true
	queued := func(key string) bool {
		for _, j := range s.queue {
			if j.req.key == key {
				return true
			}
		}
		return false
	}
	s.none["pid"] = noArt{at: now, n: 1}
	s.block("tile", "pid", 10, 5, ArtTile)
	s.block("bar", "pid", 6, 3, ArtHero)
	if len(s.queue) != 0 {
		t.Fatal("asked again straight away")
	}
	now = now.Add(heroNoneRetry + time.Millisecond)
	s.block("tile", "pid", 10, 5, ArtTile)
	s.block("bar", "pid", 6, 3, ArtHero)
	if queued("tile") || !queued("bar") {
		t.Fatalf("after %v: queue %+v, want the hero alone", heroNoneRetry, s.queue)
	}
	s.queue, s.busy = nil, map[artReq]bool{}
	s.none["pid"] = noArt{at: now, n: 20}
	now = now.Add(heroNoneRetryMax - time.Second)
	s.block("bar", "pid", 6, 3, ArtHero)
	if queued("bar") {
		t.Fatal("asked again before the slowest retry")
	}
	now = now.Add(2 * time.Second)
	s.block("bar", "pid", 6, 3, ArtHero)
	if !queued("bar") {
		t.Fatalf("gave up after %d tries", 20)
	}

	bar := artReq{key: "bar2", pid: "pid2", w: 6, h: 3, hero: true}
	tile := artReq{key: "tile2", pid: "pid2", w: 10, h: 5}
	s.failed[bar], s.failed[tile] = now, now
	now = now.Add(heroRetryAfter + time.Millisecond)
	s.block("bar2", "pid2", 6, 3, ArtHero)
	s.block("tile2", "pid2", 10, 5, ArtTile)
	if !queued("bar2") || queued("tile2") {
		t.Fatalf("after a failure: queue %+v, want the hero alone", s.queue)
	}
}

// testClock is a clock tests move by hand, safe to read from the art
// workers.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// stationArt plays the part of Music for station songs, which play under
// temporary pids outside the library: their art is that of a library album
// (alias), and Music reports none for a song in late until it has fetched
// its artwork.
type stationArt struct {
	bridge.Client
	mu    sync.Mutex
	alias map[string]string
	late  map[string]bool
	asked map[string]int
}

func (s *stationArt) Artwork(ctx context.Context, pid string, size int) (bridge.Artwork, error) {
	s.mu.Lock()
	s.asked[pid]++
	src, ok := s.alias[pid]
	late := s.late[pid]
	s.mu.Unlock()
	if !ok {
		src = pid
	}
	if late || s.Client == nil {
		return bridge.Artwork{}, nil
	}
	return s.Client.Artwork(ctx, src, size)
}

func (s *stationArt) set(pid string, late bool) {
	s.mu.Lock()
	s.late[pid] = late
	s.mu.Unlock()
}

func (s *stationArt) calls(pid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked[pid]
}

// playing reports Music playing pid (a station song when it is not in the
// library), as the bridge does.
func playing(a *App, pid string) {
	a.player.Observe(bridge.PlayerEvent{NowPlaying: bridge.NowPlaying{
		State: bridge.Playing, Volume: 50,
		Track: &bridge.CurrentTrack{PID: pid, Name: "Song " + pid, Artist: "Station Artist", Duration: 200},
	}})
	a.refreshSnap()
}

func newStationArtApp(t *testing.T) (*App, *stationArt, *testClock, []*library.Track) {
	t.Helper()
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.nav.open(a.section(KeySongs))
	clk := &testClock{t: time.Unix(1_000_000, 0)}
	a.art.now = clk.now
	ss := songsOnAlbums(t, a.lib, 4)
	sa := &stationArt{Client: a.art.client, alias: map[string]string{}, late: map[string]bool{}, asked: map[string]int{}}
	for i, s := range ss[1:] {
		sa.alias["5A7A00000000000"+string(rune('1'+i))] = s.Album.ArtPID
	}
	a.art.client = sa
	return a, sa, clk, ss
}

// A station song's art that Music has not fetched yet when the song starts
// (it reports none) shows as soon as Music has it, and until then, for a
// few seconds, the player bar keeps the last song's art.
func TestNowPlayingArtStationSongArtArrivesLate(t *testing.T) {
	a, sa, clk, ss := newStationArtApp(t)
	playing(a, ss[0].PID)
	waitFor(t, func() bool { a.View(); return barArt(a) == ss[0].Album.Key })

	const s1 = "5A7A000000000001"
	sa.set(s1, true)
	playing(a, s1)
	waitFor(t, func() bool { a.View(); return a.art.answeredNone(s1) })
	for range 5 {
		a.View()
		if got := barArt(a); got != ss[0].Album.Key {
			t.Fatalf("while Music fetches the station song's art the bar shows %q, want the last song's", got)
		}
		time.Sleep(10 * time.Millisecond)
	}

	sa.set(s1, false)
	clk.add(1100 * time.Millisecond)
	waitFor(t, func() bool { a.View(); return barArt(a) == s1 })
}

// When Music still has no art a few seconds into a song, the bar shows the
// placeholder rather than another song's art, and keeps asking: art that
// comes late still shows.
func TestNowPlayingArtKeepsAskingForTheSong(t *testing.T) {
	a, sa, clk, ss := newStationArtApp(t)
	playing(a, ss[0].PID)
	waitFor(t, func() bool { a.View(); return barArt(a) == ss[0].Album.Key })

	const s2 = "5A7A000000000002"
	sa.set(s2, true)
	playing(a, s2)
	waitFor(t, func() bool { a.View(); return a.art.answeredNone(s2) })
	clk.add(nowArtHold + time.Millisecond)
	a.View()
	if got := barArt(a); got != "" {
		t.Fatalf("%v into a song without art the bar shows %q, want the placeholder", nowArtHold, got)
	}
	// Music fetches it half a minute in; the bar asks every so often until then.
	for i := 0; i < 8; i++ {
		n := sa.calls(s2)
		clk.add(heroNoneRetryMax)
		waitFor(t, func() bool { a.View(); return sa.calls(s2) > n })
	}
	sa.set(s2, false)
	clk.add(heroNoneRetryMax)
	waitFor(t, func() bool { a.View(); return barArt(a) == s2 })
}

// A load that failed or found no art wakes the UI when it is time to ask
// again, so the art comes even when nothing else redraws (paused, or the
// terminal in the background).
func TestHeroArtRetryWakesTheUI(t *testing.T) {
	s := newArtStore(&stationArt{alias: map[string]string{}, late: map[string]bool{}, asked: map[string]int{}}, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	var after []time.Duration
	s.after = func(d time.Duration, f func()) { after = append(after, d); f() }
	s.frame()
	s.block("bar", "pid", 6, 3, ArtHero)
	select {
	case <-s.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("no wake after Music reported no art")
	}
	if len(after) == 0 || after[0] > heroNoneRetry {
		t.Fatalf("wakes %v, want one within %v", after, heroNoneRetry)
	}
}

// playingSong reports Music playing a station song under the temporary pid
// raw, with this title, artist and album.
func playingSong(a *App, raw, title, artist, album string) {
	a.player.Observe(bridge.PlayerEvent{NowPlaying: bridge.NowPlaying{
		State: bridge.Playing, Volume: 50,
		Track: &bridge.CurrentTrack{PID: raw, Name: title, Artist: artist, Album: album, Duration: 200},
	}})
	a.refreshSnap()
}

// A station song that is also in the library (under its own pid) shows the
// library album's cover; Music, which may have none for the station's copy,
// isn't asked.
func TestNowPlayingArtStationSongFromTheLibrary(t *testing.T) {
	a, sa, _, ss := newStationArtApp(t)
	s := ss[1]
	const raw = "8C6A1776D2D9C0AA"
	sa.set(raw, true)
	playingSong(a, raw, s.Title, s.Artist, s.AlbumName)
	waitFor(t, func() bool { a.View(); return barArt(a) == s.Album.Key })
	if n := sa.calls(raw); n != 0 {
		t.Fatalf("Music asked %d times for the station copy's art", n)
	}
}

// A station song Music has no artwork for, and that isn't in the library,
// gets its cover from the Apple Music catalog.
func TestNowPlayingArtStationSongFromTheCatalog(t *testing.T) {
	a, sa, clk, ss := newStationArtApp(t)
	fc := &fakeCatalog{}
	a.svc.Catalog = fc
	var fetched atomic.Int32
	a.art.urls = urlArt{fetch: func(ctx context.Context, url string) ([]byte, error) {
		fetched.Add(1)
		var b bytes.Buffer
		png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16)))
		return b.Bytes(), nil
	}}
	playing(a, ss[0].PID)
	waitFor(t, func() bool { a.View(); return barArt(a) == ss[0].Album.Key })

	const raw = "8C6A1776D2D9C0BB"
	sa.set(raw, true) // Music never has it
	song := fcSongs[1]
	playingSong(a, raw, song.Name, song.Artist, song.Album)
	waitFor(t, func() bool { a.View(); return a.art.answeredNone(raw) })
	a.View()
	fc.mu.Lock()
	early := len(fc.searches)
	fc.mu.Unlock()
	if early != 0 {
		t.Fatalf("looked up in the catalog %d times right as the song started", early)
	}
	clk.add(outsideLookupAfter)
	want := player.CatalogAlbumPrefix + "21"
	waitFor(t, func() bool { a.View(); return barArt(a) == want })
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.searches) != 1 || fetched.Load() != 1 {
		t.Fatalf("searches %q, %d fetches; want one of each", fc.searches, fetched.Load())
	}
}

func TestCatalogArtForPicksTheSong(t *testing.T) {
	songs := []catalog.Song{
		{ID: 1, AlbumID: 10, Name: "Everglade", Artist: "Tortoise", Album: "Everglade (Single)", Artwork: "https://a.test/1/100x100bb.jpg"},
		{ID: 2, AlbumID: 20, Name: "Everglade", Artist: "Tortoise", Album: "TNT", Artwork: "https://a.test/2/100x100bb.jpg"},
		{ID: 3, AlbumID: 30, Name: "Everglade", Artist: "Someone Else", Album: "TNT", Artwork: "https://a.test/3/100x100bb.jpg"},
	}
	ct := &bridge.CurrentTrack{Name: "Everglade", Artist: "Tortoise", Album: "TNT"}
	if ref, ok := catalogArtFor(songs, ct); !ok || ref.key != "catalog:20" {
		t.Fatalf("picked %+v, want the TNT album", ref)
	}
	ct.Album = "Live"
	if ref, ok := catalogArtFor(songs, ct); !ok || ref.key != "catalog:10" {
		t.Fatalf("picked %+v, want the first match", ref)
	}
	if _, ok := catalogArtFor(songs, &bridge.CurrentTrack{Name: "Djed", Artist: "Tortoise", Album: "Millions"}); ok {
		t.Fatal("matched another song")
	}
}

package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
)

// fakeCatalog answers from a few fixed records and records its calls.
// block, when set, makes Search wait for ctx (to test cancelling).
type fakeCatalog struct {
	mu       sync.Mutex
	searches []string
	err      error
	block    bool
	stopped  atomic.Int32
}

var (
	fcArtist = catalog.Artist{ID: 11, Name: "Juniper Vale", Genre: "Indie Pop"}
	fcAlbum  = catalog.Album{ID: 21, ArtistID: 11, Name: "Paper Lanterns", Artist: "Juniper Vale", Genre: "Indie Pop",
		Released: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), Artwork: "https://art.test/a/100x100bb.jpg"}
	fcSongs = []catalog.Song{
		{ID: 31, AlbumID: 21, ArtistID: 11, Name: "Paper Lanterns", Artist: "Juniper Vale", Album: "Paper Lanterns", Duration: 200 * time.Second, TrackNumber: 1, DiscNumber: 1, Streamable: true, Artwork: fcAlbum.Artwork},
		{ID: 32, AlbumID: 21, ArtistID: 11, Name: "Slow Parade", Artist: "Juniper Vale", Album: "Paper Lanterns", Duration: 171 * time.Second, TrackNumber: 2, DiscNumber: 1, Streamable: true, Explicit: true, Artwork: fcAlbum.Artwork},
		{ID: 33, AlbumID: 21, ArtistID: 11, Name: "Snow Angel", Artist: "Juniper Vale", Album: "Paper Lanterns", Duration: 150 * time.Second, TrackNumber: 3, DiscNumber: 1, Streamable: false, Artwork: fcAlbum.Artwork},
	}
)

func (f *fakeCatalog) Search(ctx context.Context, q string, limit int) (catalog.Results, error) {
	f.mu.Lock()
	f.searches = append(f.searches, q)
	block, err := f.block, f.err
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		f.stopped.Add(1)
		return catalog.Results{}, ctx.Err()
	}
	if err != nil {
		return catalog.Results{}, err
	}
	return catalog.Results{Artists: []catalog.Artist{fcArtist}, Albums: []catalog.Album{fcAlbum}, Songs: fcSongs}, nil
}

func (f *fakeCatalog) SearchKinds(ctx context.Context, q string, kinds catalog.Kind, limit int) (catalog.Results, error) {
	res, err := f.Search(ctx, q, limit)
	if kinds&catalog.Artists == 0 {
		res.Artists = nil
	}
	if kinds&catalog.Albums == 0 {
		res.Albums = nil
	}
	if kinds&catalog.Songs == 0 {
		res.Songs = nil
	}
	return res, err
}

func (f *fakeCatalog) ArtistAlbums(ctx context.Context, id int64) (catalog.Artist, []catalog.Album, error) {
	return fcArtist, []catalog.Album{fcAlbum}, f.err
}

func (f *fakeCatalog) ArtistTopSongs(ctx context.Context, id int64, limit int) ([]catalog.Song, error) {
	return fcSongs, f.err
}

func (f *fakeCatalog) AlbumTracks(ctx context.Context, id int64) (catalog.Album, []catalog.Song, error) {
	return fcAlbum, fcSongs, f.err
}

func (f *fakeCatalog) queries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.searches...)
}

// runCmd runs cmd and every command a batch or catalog result holds,
// handing messages to deliver, until nothing is left. Ticks run at once.
func runCmd(cmd tea.Cmd, deliver func(tea.Msg) tea.Cmd) {
	if cmd == nil {
		return
	}
	switch m := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			runCmd(c, deliver)
		}
	default:
		runCmd(deliver(m), deliver)
	}
}

func catalogSearch(t *testing.T, cat *fakeCatalog) (*SearchOverlay, *Ctx, *ovRecorder) {
	t.Helper()
	c, h := ovTestCtx(t)
	c.Catalog = cat
	s := NewSearchIn(scopeCatalog, cat)
	return s, c, h
}

func TestCatalogSearchDebounces(t *testing.T) {
	cat := &fakeCatalog{}
	s, c, _ := catalogSearch(t, cat)
	var ticks []tea.Cmd
	for _, r := range "juni" {
		cmd := s.Key(tea.KeyPressMsg{Code: r, Text: string(r)}, c)
		if cmd == nil {
			t.Fatalf("key %q: no debounce tick", r)
		}
		ticks = append(ticks, cmd)
	}
	if len(cat.queries()) != 0 {
		t.Fatalf("searched while typing: %v", cat.queries())
	}
	// Every tick but the last is stale and starts nothing.
	for _, tick := range ticks[:len(ticks)-1] {
		if cmd := s.Update(tick(), c); cmd != nil {
			t.Fatal("a stale debounce started a search")
		}
	}
	deliver := func(m tea.Msg) tea.Cmd { return s.Update(m, c) }
	runCmd(ticks[len(ticks)-1], deliver)
	if q := cat.queries(); len(q) != 1 || q[0] != "juni" {
		t.Fatalf("searches = %v, want one for juni", q)
	}
	var kinds []searchRowKind
	for _, r := range s.rows {
		if r.selectable() {
			kinds = append(kinds, r.kind)
		}
	}
	want := []searchRowKind{srCatArtist, srCatAlbum, srCatSong, srCatSong, srCatSong}
	if len(kinds) != len(want) {
		t.Fatalf("rows %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("rows %v, want %v", kinds, want)
		}
	}
	// The same text again is answered from what is shown.
	if cmd := s.runCatalog(); cmd != nil {
		t.Error("the shown query searched again")
	}
	ovCheckSize(t, "catalog results", s, c, 100, 30)
}

func TestCatalogSearchCancelsStale(t *testing.T) {
	cat := &fakeCatalog{block: true}
	s, c, _ := catalogSearch(t, cat)
	tick := ovType(s, c, "abc")
	start := s.Update(tick(), c)
	if start == nil {
		t.Fatal("no search after the debounce")
	}
	done := make(chan tea.Msg, 4)
	go func() {
		for _, sub := range start().(tea.BatchMsg) {
			if m := sub(); m != nil {
				if _, ok := m.(catalogSearchMsg); ok {
					done <- m
				}
			}
		}
	}()
	waitFor(t, func() bool { return len(cat.queries()) == 1 })
	ovType(s, c, "d") // new text: the running search is cancelled
	select {
	case m := <-done:
		if cmd := s.Update(m, c); cmd != nil || s.catErr != nil || s.catQuery != "" {
			t.Errorf("a cancelled search changed the results: err %v query %q", s.catErr, s.catQuery)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stale search wasn't cancelled")
	}
	if cat.stopped.Load() != 1 {
		t.Errorf("stopped = %d", cat.stopped.Load())
	}
	s.Close()
}

func TestCatalogSearchSlowAndBusy(t *testing.T) {
	cat := &fakeCatalog{err: &catalog.RateLimitError{Status: 403}}
	s, c, _ := catalogSearch(t, cat)
	tick := ovType(s, c, "x")
	s.Update(tick(), c)
	if !s.catBusy {
		t.Fatal("no search running")
	}
	s.Update(catalogTickMsg{}, c) // before catalogQuiet: nothing to say yet
	if s.catSlow {
		t.Fatal("slow before catalogQuiet")
	}
	c.Now = c.Now.Add(catalogQuiet)
	s.Update(catalogTickMsg{}, c)
	if got := s.catalogNote("x"); got != "Searching Apple Music…" {
		t.Errorf("note while slow = %q", got)
	}
	s.Update(catalogSearchMsg{owner: s, gen: s.catGen, query: "x", err: cat.err}, c)
	if got := s.catalogNote("x"); got != catalogBusyM {
		t.Errorf("note when rate limited = %q", got)
	}
	ovCheckSize(t, "busy", s, c, 100, 30)
}

func TestCatalogScopes(t *testing.T) {
	c, _ := ovTestCtx(t)
	// Without a catalog there is no Apple Music tab.
	plain := NewSearchIn(search.All, nil)
	for range len(searchScopes) {
		plain.Action(keymap.InputNextScope, 1, c)
		if plain.scope == scopeCatalog {
			t.Fatal("tab reached Apple Music without a catalog")
		}
	}
	if NewSearchIn(scopeCatalog, nil).scope != search.All {
		t.Error("Apple Music scope without a catalog")
	}

	cat := &fakeCatalog{}
	s := NewSearchIn(search.Playlists, cat)
	c.Catalog = cat
	s.Action(keymap.InputNextScope, 1, c)
	if s.scope != scopeCatalog {
		t.Fatalf("tab after Playlists = %v", s.scope)
	}
	s.Action(keymap.InputNextScope, 1, c)
	if s.scope != search.All {
		t.Fatalf("tab after Apple Music = %v", s.scope)
	}

	// All ends with the Apple Music group, and doesn't search it.
	ovSettle(s, c, ovType(s, c, "zzqx"))
	last := s.rows[len(s.rows)-1]
	if last.kind != srCatPrompt {
		t.Fatalf("last row %v, want the Apple Music prompt", last.kind)
	}
	if len(cat.queries()) != 0 {
		t.Fatal("All searched Apple Music")
	}
	if s.rows[0].kind != srNote {
		t.Errorf("no library note first: %v", s.rows[0].kind)
	}
	s.activate(len(s.rows)-1, c)
	if s.scope != scopeCatalog {
		t.Fatalf("the prompt didn't switch to Apple Music: %v", s.scope)
	}
}

func TestCatalogSearchActivate(t *testing.T) {
	cat := &fakeCatalog{}
	s, c, h := catalogSearch(t, cat)
	runCmd(ovType(s, c, "paper"), func(m tea.Msg) tea.Cmd { return s.Update(m, c) })
	find := func(k searchRowKind, i int) int {
		for j, r := range s.rows {
			if r.kind == k && r.i == i {
				return j
			}
		}
		t.Fatalf("no row %v %d", k, i)
		return -1
	}
	runCmd(s.activate(find(srCatSong, 1), c), func(tea.Msg) tea.Cmd { return nil })
	if h.play.src.Kind != player.KindCatalog || h.play.i != 1 || len(h.play.tracks) != 3 {
		t.Fatalf("play = %+v", h.play)
	}
	if !player.IsCatalog(h.play.tracks[0]) {
		t.Fatal("played tracks aren't stand-ins")
	}
	h.calls = nil
	s.activate(find(srCatSong, 2), c) // not streamable
	if !strings.HasPrefix(h.last(), "Status") {
		t.Errorf("unplayable song: %v", h.calls)
	}
	runCmd(s.activate(find(srCatAlbum, 0), c), func(tea.Msg) tea.Cmd { return nil })
	runCmd(s.activate(find(srCatArtist, 0), c), func(tea.Msg) tea.Cmd { return nil })
	if len(h.opened) != 2 || h.opened[0].Key() != "catalog-album:21" || h.opened[1].Key() != "catalog-artist:11" {
		t.Fatalf("opened %v", h.calls)
	}
}

func TestCatalogMenuItems(t *testing.T) {
	ts := catalogTracks(fcSongs[:1])
	var labels []string
	for _, it := range trackMenuItems(ts) {
		labels = append(labels, it.Label)
	}
	got := strings.Join(labels, "|")
	if got != "Play|Play Next|Add to Up Next||Go to Album|Go to Artist||Open Album in Library|Open Artist in Library||Open in Music.app" {
		t.Errorf("menu = %q", got)
	}
}

func TestArtByURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.URL.Path, "missing") {
			http.NotFound(w, r)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/256x256bb.jpg") {
			t.Errorf("fetched %s, want the 256 px size", r.URL.Path)
		}
		w.Write([]byte("jpeg-bytes"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	u := urlArt{fetch: func(ctx context.Context, url string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := srv.Client().Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, errArtNotFound
		}
		b := make([]byte, 64)
		n, _ := resp.Body.Read(b)
		return b[:n], nil
	}, dir: dir}
	url := srv.URL + "/a/100x100bb.jpg"
	data, none, err := u.load("catalog:21", url, 256)
	if err != nil || none || string(data) != "jpeg-bytes" {
		t.Fatalf("load = %q %v %v", data, none, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "catalog-21-256.jpg")); err != nil {
		t.Fatalf("not cached on disk: %v", err)
	}
	if data, _, _ := u.load("catalog:21", url, 256); string(data) != "jpeg-bytes" || hits.Load() != 1 {
		t.Errorf("second load hit the network (%d hits)", hits.Load())
	}
	if _, none, err := u.load("catalog:22", srv.URL+"/missing/100x100bb.jpg", 256); !none || err != nil {
		t.Errorf("404: none=%v err=%v", none, err)
	}
	if !isArtURL(url) || isArtURL("ABCDEF0123456789") {
		t.Error("isArtURL")
	}
}

// TestArtStoreLoadsURLs runs a URL through the store's queue and workers.
func TestArtStoreLoadsURLs(t *testing.T) {
	a := newArtApp(t, art.ProtoBlocks, 100, 30)
	var fetched atomic.Int32
	a.art.urls = urlArt{fetch: func(ctx context.Context, url string) ([]byte, error) {
		fetched.Add(1)
		var b bytes.Buffer
		png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16)))
		return b.Bytes(), nil
	}}
	key, pid := "catalog:21", "https://art.test/a/100x100bb.jpg"
	if _, ok := a.art.block(key, pid, 8, 4, ArtTile); ok {
		t.Fatal("ready before loading")
	}
	waitFor(t, func() bool { _, ok := a.art.block(key, pid, 8, 4, ArtTile); return ok })
	if fetched.Load() != 1 {
		t.Errorf("fetched %d times", fetched.Load())
	}
}

func TestCatalogAlbumView(t *testing.T) {
	c, h := ovTestCtx(t)
	cat := &fakeCatalog{}
	c.Catalog = cat
	v := newCatalogAlbumView(catalog.Album{ID: 21, Name: "Paper Lanterns", Artist: "Juniper Vale"})
	if v.Key() != "catalog-album:21" || !isDetail(v.Key()) {
		t.Fatalf("key %q", v.Key())
	}
	for _, sz := range [][2]int{{100, 26}, {60, 18}, {140, 40}, {30, 8}} {
		checkSize(t, "loading", v.Render(sz[0], sz[1], c), sz[0], sz[1])
	}
	cmd := v.Init(c)
	if v.Init(c) != nil {
		t.Error("a second Init loaded again")
	}
	c.Now = c.Now.Add(catalogQuiet)
	if out := v.Render(100, 26, c); !strings.Contains(out, "Loading…") {
		t.Error("no Loading… after catalogQuiet")
	}
	runCmd(cmd, func(m tea.Msg) tea.Cmd {
		if r, ok := m.(catalogResultMsg); ok {
			return r.apply(c)
		}
		return nil
	})
	if len(v.album.Tracks) != 3 {
		t.Fatalf("tracks = %d", len(v.album.Tracks))
	}
	for _, sz := range [][2]int{{100, 26}, {60, 18}, {140, 40}, {30, 8}, {200, 60}} {
		out := v.Render(sz[0], sz[1], c)
		checkSize(t, "loaded", out, sz[0], sz[1])
	}
	out := v.Render(120, 34, c)
	for _, want := range []string{"Apple Music", "Indie Pop · 2025", "3 songs", "Slow Parade"} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(out, "In Your Library") {
		t.Error("In Your Library for an album the library hasn't got")
	}
	// enter plays the album as a catalog context
	v.Action(keymap.NavDown, 1, c)
	v.Action(keymap.Open, 1, c)
	if h.play.src.Kind != player.KindCatalog || h.play.i != 1 || len(h.play.tracks) != 3 {
		t.Errorf("play = %+v", h.play)
	}
	// the playing catalog song is marked
	c.Player.Catalog = &player.CatalogItem{StoreID: "32"}
	if out := v.Render(120, 34, c); !strings.Contains(out, c.Theme.G.Playing+" ") {
		t.Error("playing song not marked")
	}
	if v.Reindex(c.Lib) != v {
		t.Error("Reindex dropped the page")
	}
}

func TestCatalogAlbumInYourLibrary(t *testing.T) {
	c, h := ovTestCtx(t)
	lib := c.Lib.Albums[0]
	v := newCatalogAlbumView(catalog.Album{ID: 5, Name: lib.Title + " (Deluxe Edition)", Artist: lib.Artist})
	v.Init(c)
	out := v.Render(120, 34, c)
	if !strings.Contains(out, "In Your Library") {
		t.Fatal("no In Your Library link")
	}
	for _, z := range v.zones {
		if strings.HasPrefix(z.hint, "Open Album in Library") {
			z.do()
		}
	}
	if h.last() != "OpenAlbum "+lib.Title {
		t.Errorf("link: %v", h.calls)
	}
}

func TestCatalogArtistView(t *testing.T) {
	c, h := ovTestCtx(t)
	cat := &fakeCatalog{}
	c.Catalog = cat
	v := newCatalogArtistView(catalog.Artist{ID: 11, Name: "Juniper Vale"})
	for _, sz := range [][2]int{{100, 26}, {60, 18}, {30, 8}} {
		checkSize(t, "loading", v.Render(sz[0], sz[1], c), sz[0], sz[1])
	}
	runCmd(v.Init(c), func(m tea.Msg) tea.Cmd {
		if r, ok := m.(catalogResultMsg); ok {
			return r.apply(c)
		}
		return nil
	})
	for _, sz := range [][2]int{{100, 26}, {60, 18}, {140, 42}, {30, 8}, {200, 60}} {
		checkSize(t, "loaded", v.Render(sz[0], sz[1], c), sz[0], sz[1])
	}
	out := v.Render(140, 42, c)
	for _, want := range []string{"Juniper Vale", "Indie Pop · 1 album", "Top Songs", "Albums", "Paper Lanterns", "Apple Music"} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	v.page.inGrid = false // the small sizes above left the cursor in the grid
	v.Action(keymap.Open, 1, c)
	if h.play.src.Kind != player.KindCatalog || len(h.play.tracks) != 3 {
		t.Errorf("play = %+v", h.play)
	}
	// the grid opens the catalog album
	v.page.inGrid = true
	v.Action(keymap.Open, 1, c.at(0, v.page.gridY))
	if h.last() != "OpenAlbum Paper Lanterns" {
		t.Errorf("grid open: %v", h.calls)
	}
}

func TestCatalogHostPlay(t *testing.T) {
	ts := catalogTracks(fcSongs)
	items, start := catalogItemsOf(ts, 1)
	if len(items) != 2 || start != 1 || items[1].StoreID != "32" || items[0].ArtistID != "11" {
		t.Fatalf("items %+v start %d", items, start)
	}
	if _, start := catalogItemsOf(ts, 2); start != -1 {
		t.Errorf("start on an unplayable last song = %d", start)
	}
	it, ok := catalogItemOf(&library.Track{PID: "store:99", Title: "X", Album: &library.Album{Key: "catalog:7", ArtPID: "https://a/1x1bb.jpg"}})
	if !ok || it.StoreID != "99" || it.AlbumID != "7" || it.ArtURL == "" {
		t.Errorf("unregistered stand-in: %+v", it)
	}
	runs := splitRuns([]*library.Track{ts[0], {PID: "AAAA"}, {PID: "BBBB"}, ts[1]})
	if len(runs) != 3 || len(runs[1]) != 2 {
		t.Errorf("runs %v", runs)
	}
}

func TestAlbumMatchKey(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"OK Computer", "OK Computer (Remastered)"},
		{"Weather Vane - Single", "weather vane"},
		{"Blue", "Blue [Deluxe Edition]"},
	} {
		if albumMatchKey(tc.a) != albumMatchKey(tc.b) {
			t.Errorf("%q and %q don't match", tc.a, tc.b)
		}
	}
	if albumMatchKey("Live (at Home)") == albumMatchKey("Live") {
		t.Error("an ordinary parenthesis was dropped")
	}
}

func TestCatalogErrText(t *testing.T) {
	if catalogErrText(&catalog.RateLimitError{}) != catalogBusyM {
		t.Error("rate limit")
	}
	if catalogErrText(errors.New("dial tcp")) != catalogOfflineM {
		t.Error("network")
	}
}

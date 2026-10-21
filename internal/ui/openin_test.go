package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

func day(y int) time.Time { return time.Date(y, 3, 1, 0, 0, 0, 0, time.UTC) }

func TestPickAlbum(t *testing.T) {
	cands := []catalog.Album{
		{ID: 1, Name: "Blue", Artist: "Someone Else", Released: day(1971), TrackCount: 10},
		{ID: 2, Name: "Blue (Live)", Artist: "Joni Mitchell", Released: day(1971), TrackCount: 10},
		{ID: 3, Name: "Blue (Remastered)", Artist: "Joni Mitchell", Released: day(2021), TrackCount: 14},
		{ID: 4, Name: "Blue", Artist: "Joni Mitchell", Released: day(1971), TrackCount: 10},
	}
	if a, ok := pickAlbum(cands, "Blue", "Joni Mitchell", 1971, 10); !ok || a.ID != 4 {
		t.Errorf("exact = %d %v, want 4", a.ID, ok)
	}
	// the library has the remaster: its year and track count decide
	if a, ok := pickAlbum(cands, "Blue [Deluxe Edition]", "Joni Mitchell", 2021, 14); !ok || a.ID != 3 {
		t.Errorf("remaster = %d %v, want 3", a.ID, ok)
	}
	// a lead artist credit with guests still matches
	duo := []catalog.Album{{ID: 9, Name: "Random Access Memories", Artist: "Daft Punk & Friends"}}
	if _, ok := pickAlbum(duo, "Random Access Memories", "Daft Punk", 0, 0); !ok {
		t.Error("lead artist credit didn't match")
	}
	if _, ok := pickAlbum(cands, "Court and Spark", "Joni Mitchell", 1974, 10); ok {
		t.Error("another title matched")
	}
	if _, ok := pickAlbum(cands[:1], "Blue", "Joni Mitchell", 1971, 10); ok {
		t.Error("another artist matched")
	}
}

func TestPickSong(t *testing.T) {
	cands := []catalog.Song{
		{ID: 1, AlbumID: 10, Name: "Get Lucky (Live)", Artist: "Daft Punk", Album: "Alive", Duration: 300 * time.Second, Streamable: true},
		{ID: 2, AlbumID: 11, Name: "Get Lucky (feat. Pharrell Williams)", Artist: "Daft Punk", Album: "Get Lucky - Single", Duration: 248 * time.Second, Streamable: true},
		{ID: 3, AlbumID: 12, Name: "Get Lucky (feat. Pharrell Williams)", Artist: "Daft Punk", Album: "Random Access Memories", Duration: 369 * time.Second, Streamable: true},
	}
	if s, ok := pickSong(cands, "Get Lucky", "Daft Punk", "Random Access Memories", 369*time.Second); !ok || s.ID != 3 {
		t.Errorf("album version = %d %v, want 3", s.ID, ok)
	}
	if s, ok := pickSong(cands, "Get Lucky", "Daft Punk", "", 248*time.Second); !ok || s.ID != 2 {
		t.Errorf("by length = %d %v, want 2", s.ID, ok)
	}
	if s, ok := pickSong(cands, "Get Lucky (Live)", "Daft Punk", "", 0); !ok || s.ID != 1 {
		t.Errorf("live = %d %v, want 1", s.ID, ok)
	}
	if _, ok := pickSong(cands, "One More Time", "Daft Punk", "", 0); ok {
		t.Error("another song matched")
	}
}

func TestPickArtistAndNames(t *testing.T) {
	cands := []catalog.Artist{{ID: 1, Name: "Beatles Revival"}, {ID: 2, Name: "The Beatles"}}
	if a, ok := pickArtist(cands, "Beatles"); !ok || a.ID != 2 {
		t.Errorf("artist = %+v %v", a, ok)
	}
	if _, ok := pickArtist(cands, "Beat"); ok {
		t.Error("a partial name matched")
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Beyoncé", "beyonce", true},
		{"Daft Punk", "Daft Punk feat. Pharrell Williams", true},
		{"Daft", "Daft Punk", false},
		{"", "", false},
	} {
		if got := sameArtist(tc.a, tc.b); got != tc.want {
			t.Errorf("sameArtist(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
	if songMatchKey("Yesterday - Remastered 2009") != songMatchKey("Yesterday") {
		t.Error("remaster note counted")
	}
	if songMatchKey("Yesterday (Live)") == songMatchKey("Yesterday") {
		t.Error("a live recording matched the studio one")
	}
	if got := searchTitle("Weather Vane (Deluxe) - Single"); got != "Weather Vane" {
		t.Errorf("searchTitle = %q", got)
	}
}

func TestSubjectOf(t *testing.T) {
	c, _ := ovTestCtx(t)
	al := c.Lib.Albums[0]
	if s := subjectOf(al.Tracks[:1]); s.track != al.Tracks[0] {
		t.Errorf("one song = %+v", s)
	}
	if len(al.Tracks) > 1 {
		if s := subjectOf(al.Tracks); s.album != al {
			t.Errorf("an album's songs = %+v", s)
		}
		if s := subjectOf(al.Tracks[:2]); len(al.Tracks) > 2 && !s.empty() {
			t.Errorf("part of an album = %+v", s)
		}
	}
	if !subjectOf(nil).empty() {
		t.Error("nothing selected isn't empty")
	}
}

// openInFixture is a library album that is fcAlbum on Apple Music.
func openInFixture() *library.Album {
	ar := &library.Artist{Name: "Juniper Vale", FoldName: "juniper vale"}
	al := &library.Album{Key: "lib-paper", Title: "Paper Lanterns (Deluxe Edition)", Artist: "Juniper Vale", Year: 2025}
	for i, name := range []string{"Paper Lanterns", "Slow Parade (feat. Someone)", "Snow Angel"} {
		al.Tracks = append(al.Tracks, &library.Track{PID: "LIB" + itoa(i), Title: name, Artist: "Juniper Vale",
			AlbumName: al.Title, Album: al, Group: ar, Available: true, FoldTitle: library.Fold(name)})
	}
	ar.Albums, ar.Tracks = []*library.Album{al}, len(al.Tracks)
	return al
}

func deliver(c *Ctx) func(tea.Msg) tea.Cmd {
	return func(m tea.Msg) tea.Cmd {
		if r, ok := m.(catalogResultMsg); ok {
			return r.apply(c)
		}
		return nil
	}
}

func resetMatches(t *testing.T) {
	t.Cleanup(func() { clear(catalogMatches); clear(catalogMatching) })
	clear(catalogMatches)
}

func TestOpenAlbumInCatalog(t *testing.T) {
	resetMatches(t)
	c, h := ovTestCtx(t)
	cat := &fakeCatalog{}
	c.Catalog = cat
	al := openInFixture()
	runCmd(openIn(h, keymap.OpenAlbumInCatalog, cat, c.Lib, Subject{album: al}), deliver(c))
	if len(h.opened) != 1 || h.opened[0].Key() != "catalog-album:21" {
		t.Fatalf("opened %v", h.calls)
	}
	if q := cat.queries(); len(q) != 1 || q[0] != "Paper Lanterns Juniper Vale" {
		t.Errorf("queries %q", q)
	}
	// again: the match is kept, no second search
	runCmd(openIn(h, keymap.OpenAlbumInCatalog, cat, c.Lib, Subject{album: al}), deliver(c))
	if len(cat.queries()) != 1 || len(h.opened) != 2 {
		t.Errorf("second open: %d queries, %v", len(cat.queries()), h.calls)
	}
	// a song of an album already found needs no search either; the page
	// selects it once loaded
	runCmd(openIn(h, keymap.OpenAlbumInCatalog, cat, c.Lib, Subject{track: al.Tracks[1]}), deliver(c))
	if len(cat.queries()) != 1 || len(h.opened) != 3 {
		t.Fatalf("song on a found album: %d queries, %v", len(cat.queries()), h.calls)
	}
	v := h.opened[2].(*CatalogAlbumView)
	runCmd(v.Init(c), deliver(c))
	v.Render(120, 34, c)
	if got := v.selectedTrack(); got == nil || got.Title != "Slow Parade" {
		t.Errorf("selected %+v, want Slow Parade", got)
	}
}

func TestOpenSongInCatalog(t *testing.T) {
	resetMatches(t)
	c, h := ovTestCtx(t)
	cat := &fakeCatalog{}
	c.Catalog = cat
	tr := openInFixture().Tracks[0]
	runCmd(openIn(h, keymap.OpenAlbumInCatalog, cat, c.Lib, Subject{track: tr}), deliver(c))
	if len(h.opened) != 1 || h.opened[0].Key() != "catalog-album:21" {
		t.Fatalf("opened %v", h.calls)
	}
	v := h.opened[0].(*CatalogAlbumView)
	if v.want.pid != player.StorePID("31") {
		t.Errorf("want %+v", v.want)
	}
	runCmd(v.Init(c), deliver(c))
	v.Render(120, 34, c)
	if got := v.selectedTrack(); got == nil || got.PID != "store:31" {
		t.Errorf("selected %+v", got)
	}
}

func TestOpenInCatalogNotFound(t *testing.T) {
	resetMatches(t)
	c, h := ovTestCtx(t)
	cat := &fakeCatalog{}
	ar := &library.Artist{Name: "Nobody Anyone Knows"}
	for range 2 {
		runCmd(openIn(h, keymap.OpenArtistInCatalog, cat, c.Lib, Subject{artist: ar}), deliver(c))
	}
	if h.last() != "Status "+notInCatalogM || len(h.opened) != 0 {
		t.Errorf("calls %v", h.calls)
	}
	if len(cat.queries()) != 1 {
		t.Errorf("%d searches, want 1: a miss is kept too", len(cat.queries()))
	}
	// a failed search isn't kept
	cat.err = &catalog.RateLimitError{}
	al := openInFixture()
	for range 2 {
		runCmd(openIn(h, keymap.OpenAlbumInCatalog, cat, c.Lib, Subject{album: al}), deliver(c))
	}
	if h.last() != "Status "+catalogBusyM || len(cat.queries()) != 3 {
		t.Errorf("rate limited: %d queries, %v", len(cat.queries()), h.calls)
	}
	// without a catalog nothing is asked
	h.calls = nil
	runCmd(openIn(h, keymap.OpenAlbumInCatalog, nil, c.Lib, Subject{album: al}), deliver(c))
	if h.last() != "Status "+catalogOfflineM {
		t.Errorf("no catalog: %v", h.calls)
	}
}

func TestOpenInLibrary(t *testing.T) {
	c, h := ovTestCtx(t)
	var lt *library.Track
	for _, t := range c.Lib.Songs {
		if len(t.Album.Tracks) > 2 && t != t.Album.Tracks[0] {
			lt = t
			break
		}
	}
	stand := catalogTrack(catalog.Song{ID: 901, AlbumID: 77, Name: lt.Title, Artist: lt.Artist, Album: lt.AlbumName, Streamable: true})
	runCmd(openIn(h, keymap.OpenAlbumInLibrary, nil, c.Lib, Subject{track: stand}), deliver(c))
	if len(h.opened) != 1 || h.opened[0].Key() != "album:"+lt.Album.Key {
		t.Fatalf("opened %v", h.calls)
	}
	v := h.opened[0].(*AlbumView)
	v.Render(120, 34, c)
	if v.selectedTrack() != lt {
		t.Errorf("selected %v, want %v", v.selectedTrack().Title, lt.Title)
	}
	// a catalog album and artist the library has
	runCmd(openIn(h, keymap.OpenAlbumInLibrary, nil, c.Lib, Subject{album: catalogAlbum(catalog.Album{ID: 78, Name: lt.Album.Title, Artist: lt.Album.Artist})}), deliver(c))
	if h.last() != "OpenAlbum "+lt.Album.Title {
		t.Errorf("album: %v", h.calls)
	}
	runCmd(openIn(h, keymap.OpenArtistInLibrary, nil, c.Lib, Subject{catArtist: catalog.Artist{ID: 79, Name: lt.Group.Name}}), deliver(c))
	if h.last() != "OpenArtist "+lt.Group.Name {
		t.Errorf("artist: %v", h.calls)
	}
	// and one it hasn't
	runCmd(openIn(h, keymap.OpenArtistInLibrary, nil, c.Lib, Subject{catArtist: fcArtist}), deliver(c))
	if h.last() != "Status "+notInLibraryM {
		t.Errorf("missing: %v", h.calls)
	}
}

func menuLabels(m *MenuOverlay) []string {
	var out []string
	for _, it := range m.items {
		out = append(out, it.Label)
	}
	return out
}

// menuOf is the menu o opens, as Init leaves it, its labels joined by |.
func menuOf(c *Ctx, o Overlay) (*MenuOverlay, string) {
	m := o.(*MenuOverlay)
	m.Init(c)
	return m, strings.Join(menuLabels(m), "|")
}

func TestMenuOpenInItems(t *testing.T) {
	c, h := ovTestCtx(t)
	one := c.Lib.Songs[:1]
	// no catalog: no Apple Music items, and no separators left doubled
	if _, got := menuOf(c, NewMenu(10, 10, one)); strings.Contains(got, "Apple Music") || strings.Contains(got, "|||") {
		t.Errorf("song without a catalog: %q", got)
	}
	c.Catalog = &fakeCatalog{}

	// a library song: its album and its artist, after Go to
	m, got := menuOf(c, NewMenu(10, 10, one))
	if !strings.HasSuffix(got, "|Go to Album|Go to Artist||Open Album in Apple Music|Open Artist in Apple Music||Favorite") {
		t.Errorf("library song: %q", got)
	}
	m.Rect(140, 42)
	out := m.Render(c)
	for _, k := range []string{"space o a l", "space o a r"} {
		if !strings.Contains(out, k) {
			t.Errorf("no %s in the menu", k)
		}
	}
	m.Action(keymap.OpenArtistInCatalog, 1, c)
	if !slices.Contains(h.calls, "OpenIn "+string(keymap.OpenArtistInCatalog)) || h.openIn[0].track != one[0] {
		t.Errorf("space o a r in the menu: %v", h.calls)
	}

	// a library album, and an artist
	al := c.Lib.Albums[0]
	if _, got := menuOf(c, NewMenu(10, 10, al.Tracks)); !strings.Contains(got, "||Open Album in Apple Music|Open Artist in Apple Music||") {
		t.Errorf("library album: %q", got)
	}
	ar := al.Tracks[0].Group
	var ts []*library.Track
	for _, a := range ar.Albums {
		ts = append(ts, a.Tracks...)
	}
	if _, got := menuOf(c, newMenuFor(10, 10, ts, Subject{artist: ar})); strings.Contains(got, "Open Album") || !strings.Contains(got, "||Open Artist in Apple Music||") {
		t.Errorf("library artist: %q", got)
	}

	// a catalog song: the library items only for what the library has,
	// and Open in Music.app whether or not it has it
	if _, got := menuOf(c, NewMenu(10, 10, catalogTracks(fcSongs[:1]))); got != "Play|Play Next|Add to Up Next||Go to Album|Go to Artist||Open in Music.app" {
		t.Errorf("catalog song not in the library: %q", got)
	}
	lt := c.Lib.Songs[0]
	in := catalogTrack(catalog.Song{ID: 902, AlbumID: 80, ArtistID: 12, Name: lt.Title, Artist: lt.Artist, Album: lt.AlbumName})
	if _, got := menuOf(c, NewMenu(10, 10, []*library.Track{in})); !strings.HasSuffix(got, "|Go to Artist||Open Album in Library|Open Artist in Library||Open in Music.app") {
		t.Errorf("catalog song in the library: %q", got)
	}
	// its artist, featuring someone, is in the library; its album isn't
	feat := catalogTrack(catalog.Song{ID: 903, AlbumID: 81, ArtistID: 12, Name: "Nowhere Song", Artist: lt.Group.Name + " feat. Someone", Album: "Nowhere Album"})
	if _, got := menuOf(c, NewMenu(10, 10, []*library.Track{feat})); !strings.HasSuffix(got, "|Go to Artist||Open Artist in Library||Open in Music.app") {
		t.Errorf("catalog song by a library artist: %q", got)
	}

	// a catalog album and a catalog artist
	h.pushed = nil
	catalogAlbumAction(keymap.Menu, catalogAlbum(catalog.Album{ID: 82, ArtistID: 13, Name: lt.Album.Title, Artist: lt.Album.Artist}), c, 10, 10)
	if _, got := menuOf(c, h.pushed[0]); !strings.HasSuffix(got, "|Go to Album||Open Album in Library|Open Artist in Library||Open in Music.app") {
		t.Errorf("catalog album in the library: %q", got)
	}
	catalogAlbumAction(keymap.Menu, catalogAlbum(fcAlbum), c, 10, 10)
	if _, got := menuOf(c, h.pushed[1]); strings.Contains(got, "in Library") || !strings.HasSuffix(got, "|Go to Album||Open in Music.app") {
		t.Errorf("catalog album not in the library: %q", got)
	}
}

func TestOpenArtistInCatalog(t *testing.T) {
	resetMatches(t)
	c, h := ovTestCtx(t)
	cat := &fakeCatalog{}
	c.Catalog = cat
	al := openInFixture()
	// a song featuring someone: its lead artist, one search
	feat := al.Tracks[1]
	feat.Artist = "Juniper Vale feat. Someone"
	runCmd(openIn(h, keymap.OpenArtistInCatalog, cat, c.Lib, Subject{track: feat}), deliver(c))
	if len(h.opened) != 1 || h.opened[0].Key() != "catalog-artist:11" {
		t.Fatalf("opened %v", h.calls)
	}
	if q := cat.queries(); len(q) != 1 || q[0] != "Juniper Vale" {
		t.Errorf("queries %q", q)
	}
	// another song and the album by that artist: the artist is known
	runCmd(openIn(h, keymap.OpenArtistInCatalog, cat, c.Lib, Subject{track: al.Tracks[0]}), deliver(c))
	runCmd(openIn(h, keymap.OpenArtistInCatalog, cat, c.Lib, Subject{album: al}), deliver(c))
	if len(cat.queries()) != 1 || len(h.opened) != 3 || h.opened[2].Key() != "catalog-artist:11" {
		t.Errorf("again: %q, %v", cat.queries(), h.calls)
	}

	// a song whose album was found already needs no artist search
	resetMatches(t)
	runCmd(openIn(h, keymap.OpenAlbumInCatalog, cat, c.Lib, Subject{track: al.Tracks[2]}), deliver(c))
	runCmd(openIn(h, keymap.OpenArtistInCatalog, cat, c.Lib, Subject{track: al.Tracks[2]}), deliver(c))
	if len(cat.queries()) != 2 || h.opened[len(h.opened)-1].Key() != "catalog-artist:11" {
		t.Errorf("after the album: %q, %v", cat.queries(), h.calls)
	}

	// a catalog song knows its artist
	runCmd(openIn(h, keymap.OpenArtistInCatalog, cat, c.Lib, Subject{track: catalogTrack(fcSongs[0])}), deliver(c))
	if len(cat.queries()) != 2 || h.opened[len(h.opened)-1].Key() != "catalog-artist:11" {
		t.Errorf("catalog song: %q, %v", cat.queries(), h.calls)
	}
	// an artist has no album to open
	runCmd(openIn(h, keymap.OpenAlbumInCatalog, cat, c.Lib, Subject{artist: al.Tracks[0].Group}), deliver(c))
	if h.last() != "Status "+noAlbumM {
		t.Errorf("album of an artist: %v", h.calls)
	}
}

func TestOpenArtistInLibrary(t *testing.T) {
	c, h := ovTestCtx(t)
	lt := c.Lib.Songs[0]
	runCmd(openIn(h, keymap.OpenArtistInLibrary, nil, c.Lib, Subject{track: lt}), deliver(c))
	if h.last() != "OpenArtist "+lt.Group.Name {
		t.Errorf("library song: %v", h.calls)
	}
	feat := catalogTrack(catalog.Song{ID: 904, AlbumID: 83, Name: "Nowhere Song", Artist: lt.Group.Name + " featuring Someone", Album: "Nowhere Album"})
	runCmd(openIn(h, keymap.OpenArtistInLibrary, nil, c.Lib, Subject{track: feat}), deliver(c))
	if h.last() != "OpenArtist "+lt.Group.Name {
		t.Errorf("catalog song: %v", h.calls)
	}
	runCmd(openIn(h, keymap.OpenAlbumInLibrary, nil, c.Lib, Subject{track: feat}), deliver(c))
	if h.last() != "Status "+notInLibraryM {
		t.Errorf("album not in the library: %v", h.calls)
	}
}

func TestLeadArtist(t *testing.T) {
	for _, tc := range []struct{ credit, want string }{
		{"Dua Lipa feat. DaBaby", "Dua Lipa"},
		{"Calvin Harris ft. Rihanna", "Calvin Harris"},
		{"Mark Ronson Featuring Bruno Mars", "Mark Ronson"},
		{"Daft Punk (with Pharrell Williams)", "Daft Punk"},
		{"Simon & Garfunkel", "Simon & Garfunkel"},
		{"Kids With Guns", "Kids With Guns"},
		{"Feather", "Feather"},
	} {
		if got := leadArtist(tc.credit); got != tc.want {
			t.Errorf("leadArtist(%q) = %q, want %q", tc.credit, got, tc.want)
		}
	}
	cands := []catalog.Artist{{ID: 1, Name: "Daft"}, {ID: 2, Name: "Pharrell Williams"}, {ID: 3, Name: "Daft Punk"}}
	if a, ok := pickArtist(cands, "Daft Punk & Pharrell Williams"); !ok || a.ID != 3 {
		t.Errorf("lead of a duo = %+v %v", a, ok)
	}
	if _, ok := pickArtist(cands[1:], "Pharrell"); ok {
		t.Error("a longer name matched a shorter credit")
	}
}

func TestPagePlayAndShuffle(t *testing.T) {
	c, h := ovTestCtx(t)
	al := c.Lib.Albums[0]
	check := func(name string, v View, wantN int) {
		t.Helper()
		v.Render(120, 34, c)
		h.calls, h.play.tracks = nil, nil
		if ok, cmd := v.Action(keymap.PagePlay, 1, c); !ok || cmd != nil {
			t.Errorf("%s: P not handled", name)
		}
		if h.last() != "Play" || h.play.i != 0 && name != "playlist" || len(h.play.tracks) != wantN {
			t.Errorf("%s P: %v, %d tracks from %d", name, h.calls, len(h.play.tracks), h.play.i)
		}
		h.calls = nil
		v.Action(keymap.PageShuffle, 1, c)
		if !slices.Equal(h.calls, []string{"Do " + string(keymap.PlayerShuffle), "Play"}) || len(h.play.tracks) != wantN {
			t.Errorf("%s S: %v", name, h.calls)
		}
	}
	check("album", NewAlbumView(al), len(al.Tracks))
	check("songs", newSongsView(c.Lib), len(c.Lib.Songs))
	for _, p := range c.Lib.Playlists {
		if p.Kind != "folder" && len(p.Tracks) > 0 {
			check("playlist", newPlaylistView(p), len(p.Tracks))
			break
		}
	}
	ar := c.Lib.Artists[0]
	check("artist", NewArtistView(ar), ar.Tracks)
	check("artists list", NewArtistsView(c.Lib), c.Lib.Artists[0].Tracks)
	grid := NewAlbumsView(c.Lib)
	grid.Render(120, 34, c)
	check("albums grid", grid, len(grid.grid.Selected().Tracks))

	// shuffle already on: no toggle
	c.Player.NowPlaying.Shuffle = true
	h.calls = nil
	NewAlbumView(al).Action(keymap.PageShuffle, 1, c)
	if !slices.Equal(h.calls, []string{"Play"}) {
		t.Errorf("shuffle on: %v", h.calls)
	}
}

func TestPageButtonHints(t *testing.T) {
	c, _ := ovTestCtx(t)
	v := NewAlbumView(c.Lib.Albums[0])
	v.Render(120, 34, c)
	var hints []string
	for _, z := range v.zones {
		hints = append(hints, z.hint)
	}
	for _, want := range []string{"Play Album  P", "Shuffle Album  S"} {
		if !slices.Contains(hints, want) {
			t.Errorf("hints %q lack %q", hints, want)
		}
	}
	// the Apple Music link shows only with a catalog
	if slices.Contains(hints, "Open Album in Apple Music  space o a l") {
		t.Error("Open in Apple Music without a catalog")
	}
	c.Catalog = &fakeCatalog{}
	out := v.Render(120, 34, c)
	if !strings.Contains(out, "Open in Apple Music") {
		t.Fatal("no Open in Apple Music link")
	}
	h := c.Host.(*ovRecorder)
	for _, z := range v.zones {
		if z.hint == "Open Album in Apple Music  space o a l" {
			z.do()
		}
	}
	if h.last() != "OpenIn "+string(keymap.OpenAlbumInCatalog) || h.openIn[0].album != c.Lib.Albums[0] {
		t.Errorf("link: %v", h.calls)
	}
	fh := v.FooterHints(c)
	if len(fh) != 4 || fh[2].Action != keymap.PagePlay || fh[3].Action != keymap.PageShuffle {
		t.Errorf("footer hints %+v", fh)
	}
}

// The leader keys act on the focused selection through the app.
func TestOpenInKeys(t *testing.T) {
	resetMatches(t)
	a := newShellApp(t, 140, 42)
	cat := &fakeCatalog{}
	a.svc.Catalog = cat
	a.goSection(KeyAlbums, "Albums")
	// run runs cmd and what it leads to, leaving out commands that wait
	// (ticks, status expiry) so the status stays to be read.
	var run func(cmd tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- cmd() }()
		select {
		case m := <-ch:
			if b, ok := m.(tea.BatchMsg); ok {
				for _, c := range b {
					run(c)
				}
				return
			}
			if m != nil {
				_, next := a.Update(m)
				run(next)
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	keys := func(ks ...string) {
		for _, k := range ks {
			msg := tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
			if k == "space" {
				msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
			}
			_, cmd := a.Update(msg)
			run(cmd)
		}
	}
	// space o a alone waits for the next key
	keys("space", "o", "a")
	if len(cat.queries()) != 0 || a.status.text != "" {
		t.Errorf("space o a ran: %q, status %q", cat.queries(), a.status.text)
	}
	keys("l")
	if len(cat.queries()) != 1 || a.status.text != notInCatalogM {
		t.Errorf("Albums: %q, status %q", cat.queries(), a.status.text)
	}
	// the album's artist: one search, by the artist's name
	al := a.focusedSubject().album
	keys("space", "o", "a", "r")
	if q := cat.queries(); len(q) != 2 || q[1] != leadArtist(al.Artist) {
		t.Errorf("space o a r: %q", q)
	}
	keys("space", "o", "l", "l") // a library album: its own page
	if !strings.HasPrefix(a.nav.cur.Key(), "album:") {
		t.Errorf("open in library from Albums: %s", a.nav.cur.Key())
	}
	// a catalog album the library hasn't got
	run(a.Open(newCatalogAlbumView(fcAlbum)))
	keys("space", "o", "l", "l")
	if a.status.text != notInLibraryM {
		t.Errorf("catalog album: status %q", a.status.text)
	}
	keys("S")
	if !a.player.State().NowPlaying.Shuffle {
		t.Error("S didn't turn shuffle on")
	}
}

package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/search"
)

// markLib is a library with what the fake catalog (catalog_test.go) has in
// part: "Paper Lanterns" by Juniper Vale, its first song by id, its second
// by name only (a local file), and a followed playlist.
func markLib() *library.Library {
	tr := func(pid, title, album string, no int, store int64) bridge.SnapshotTrack {
		return bridge.SnapshotTrack{
			PID: pid, Title: title, Artist: "Juniper Vale", AlbumArtist: "Juniper Vale", Album: album, AlbumPID: "AL-" + album,
			Track: no, TrackCount: 3, InLibrary: true, Available: true, StoreID: store, Duration: 200000,
		}
	}
	return library.Build(&bridge.Snapshot{
		Tracks: []bridge.SnapshotTrack{
			tr("L1", "Paper Lanterns", "Paper Lanterns", 1, 31),
			tr("L2", "Slow Parade", "Paper Lanterns", 2, 0),
			tr("L9", "Elsewhere", "Another Album", 1, 77),
		},
		Playlists: []bridge.SnapshotPlaylist{
			{PID: "P1", Name: "Indie Rising", Kind: "subscription", GlobalID: "pl.1000", Items: []string{"L1"}},
		},
	})
}

// checks counts the library checks in a rendered block.
func checks(c *Ctx, out string) int {
	return strings.Count(ansi.Strip(out), c.Theme.G.InLib)
}

// checkCols are the columns of the checks in a rendered block.
func checkCols(c *Ctx, out string) []int {
	var cols []int
	for _, l := range strings.Split(ansi.Strip(out), "\n") {
		if i := strings.Index(l, c.Theme.G.InLib); i >= 0 {
			cols = append(cols, ansi.StringWidth(l[:i]))
		}
	}
	return cols
}

func TestCatalogMarksCtx(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib = markLib()
	for _, tc := range []struct {
		name string
		got  bool
		want bool
	}{
		{"song by id", c.CatalogSongInLibrary(31, "Whatever", "Anyone"), true},
		{"song by name, no id library-side", c.CatalogSongInLibrary(32, "Slow Parade", "Juniper Vale"), true},
		{"song by name, no catalog id", c.CatalogSongInLibrary(0, "Slow Parade", "Juniper Vale"), true},
		{"another release of a song with an id", c.CatalogSongInLibrary(900, "Paper Lanterns", "Juniper Vale"), false},
		{"unknown song", c.CatalogSongInLibrary(33, "Snow Angel", "Juniper Vale"), false},
		{"album", c.CatalogAlbumInLibrary("Paper Lanterns (Deluxe Edition)", "Juniper Vale"), true},
		{"album, other artist", c.CatalogAlbumInLibrary("Paper Lanterns", "Someone Else"), false},
		{"playlist", c.CatalogPlaylistInLibrary("pl.1000"), true},
		{"playlist, other id", c.CatalogPlaylistInLibrary("pl.1001"), false},
		{"stand-in", c.StandInInLibrary(catalogTrack(fcSongs[0])), true},
		{"stand-in, not in library", c.StandInInLibrary(catalogTrack(fcSongs[2])), false},
		{"a library song is never marked", c.StandInInLibrary(c.Lib.Songs[0]), false},
		{"catalog album tile", c.CatalogAlbumTileInLibrary(catalogAlbum(fcAlbum)), true},
		{"a library album is never marked", c.CatalogAlbumTileInLibrary(c.Lib.Albums[0]), false},
		{"station", c.HomeItemInLibrary(home.Item{Type: home.TypeStation, ID: "ra.1000", Title: "Paper Lanterns"}), false},
		{"artist", c.HomeItemInLibrary(home.Item{Type: home.TypeArtist, ID: "1", Title: "Juniper Vale"}), false},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	// a new library starts the answers over
	c.Lib = library.Build(&bridge.Snapshot{})
	if c.CatalogSongInLibrary(31, "Paper Lanterns", "Juniper Vale") || c.CatalogAlbumInLibrary("Paper Lanterns", "Juniper Vale") {
		t.Error("answers outlived their library")
	}
	c.Lib = nil
	if c.CatalogSongInLibrary(31, "x", "y") || c.CatalogAlbumInLibrary("x", "y") || c.CatalogPlaylistInLibrary("pl.1") {
		t.Error("marked without a library")
	}
}

func TestCatalogAlbumPageMarks(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib = markLib()
	c.Catalog = &fakeCatalog{}
	v := newCatalogAlbumView(fcAlbum)
	runCmd(v.Init(c), func(m tea.Msg) tea.Cmd {
		if r, ok := m.(catalogResultMsg); ok {
			return r.apply(c)
		}
		return nil
	})
	out := v.Render(100, 26, c)
	// songs 1 (by id) and 2 (by name); the header's "In Your Library" isn't the glyph
	if got := checks(c, out); got != 2 {
		t.Fatalf("%d checks, want 2:\n%s", got, ansi.Strip(out))
	}
	cols := checkCols(c, out)
	if cols[0] != cols[1] {
		t.Errorf("checks in columns %v", cols)
	}
	if !strings.Contains(ansi.Strip(out), "In Your Library") {
		t.Error("header lost its link")
	}
	// the check sits left of the time and doesn't push it out of line
	rows := strings.Split(ansi.Strip(out), "\n")
	var times []int
	for _, l := range rows {
		for _, tm := range []string{"3:20", "2:51", "2:30"} {
			if i := strings.Index(l, tm); i >= 0 {
				times = append(times, ansi.StringWidth(l[:i]))
			}
		}
	}
	if len(times) != 3 || times[0] != times[1] || times[1] != times[2] {
		t.Errorf("times in columns %v", times)
	}
	for _, sz := range [][2]int{{100, 26}, {60, 18}, {40, 12}, {30, 8}, {200, 60}} {
		checkSize(t, "marked album page", v.Render(sz[0], sz[1], c), sz[0], sz[1])
	}
	// Reindex with a library that lacks the album drops the marks
	v.Reindex(library.Build(&bridge.Snapshot{}))
	c.Lib = library.Build(&bridge.Snapshot{})
	if got := checks(c, v.Render(100, 26, c)); got != 0 {
		t.Errorf("%d checks after the library lost the songs", got)
	}
	if strings.Contains(ansi.Strip(v.Render(100, 26, c)), "In Your Library") {
		t.Error("link kept after the library lost the album")
	}
}

func TestCatalogAlbumPageHoverTip(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib = markLib()
	c.Catalog = &fakeCatalog{}
	v := newCatalogAlbumView(fcAlbum)
	runCmd(v.Init(c), func(m tea.Msg) tea.Cmd {
		if r, ok := m.(catalogResultMsg); ok {
			return r.apply(c)
		}
		return nil
	})
	out := v.Render(100, 26, c)
	var hint string
	c.hint = func(s string) { hint = s }
	rows := strings.Split(ansi.Strip(out), "\n")
	for y, l := range rows {
		if i := strings.Index(l, c.Theme.G.InLib); i >= 0 {
			x := ansi.StringWidth(l[:i])
			v.Mouse(MouseEvent{Kind: MouseMotion, X: x, Y: y}, c)
			break
		}
	}
	if hint != inLibraryTip {
		t.Errorf("hover hint %q", hint)
	}
}

func TestAlbumGridMarksCatalogTilesOnly(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib = markLib()
	other := catalog.Album{ID: 22, ArtistID: 11, Name: "Weather Vane - Single", Artist: "Juniper Vale"}
	libAlbum := c.Lib.Albums[0]
	for _, list := range []bool{false, true} {
		g := NewAlbumGrid(gridTestPad)
		g.Reset([]*library.Album{catalogAlbum(fcAlbum), catalogAlbum(other), libAlbum})
		g.SetList(list)
		for _, sz := range [][2]int{{117, 30}, {60, 20}, {30, 10}, {20, 8}} {
			out := g.Render(sz[0], sz[1], c, "")
			checkSize(t, "marked grid", out, sz[0], sz[1])
			if sz[0] >= 60 {
				if got := checks(c, out); got != 1 {
					t.Errorf("list=%v %dx%d: %d checks, want 1 (the library album and the one it lacks are bare)", list, sz[0], sz[1], got)
				}
			}
		}
	}
}

func TestHomeMarks(t *testing.T) {
	c, _ := homeTestCtx()
	c.Lib = markLib()
	pg := &home.Page{
		Updated: c.Now,
		Shelves: []home.Shelf{{ID: "a", Title: "Mixed", Items: []home.Item{
			{Type: home.TypeAlbum, ID: "21", Title: "Paper Lanterns", Subtitle: "Juniper Vale"},
			{Type: home.TypeAlbum, ID: "22", Title: "Not Mine", Subtitle: "Juniper Vale"},
			{Type: home.TypePlaylist, ID: "pl.1000", Title: "Indie Rising", Subtitle: "Apple Music"},
			{Type: home.TypePlaylist, ID: "pl.2000", Title: "Other Playlist", Subtitle: "Apple Music"},
			{Type: home.TypeSong, ID: "31", Title: "Paper Lanterns", Subtitle: "Juniper Vale"},
			{Type: home.TypeSong, ID: "33", Title: "Snow Angel", Subtitle: "Juniper Vale"},
		}}},
	}
	v := NewHomeView(pg)
	out := v.Render(117, 30, c)
	checkBlock(t, "home marked", out, 117, 30)
	if got := checks(c, out); got != 3 {
		t.Errorf("%d checks, want 3 (album, playlist, song):\n%s", got, ansi.Strip(out))
	}
	// other sizes keep the exact rectangle
	for _, sz := range [][2]int{{60, 20}, {30, 8}, {117, 35}} {
		checkBlock(t, "home marked", NewHomeView(pg).Render(sz[0], sz[1], c), sz[0], sz[1])
	}
	// without a library nothing is marked
	c.Lib = nil
	if got := checks(c, NewHomeView(pg).Render(117, 30, c)); got != 0 {
		t.Errorf("%d checks without a library", got)
	}
}

func TestSearchCatalogMarks(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib = markLib()
	cat := &fakeCatalog{}
	c.Catalog = cat
	s := NewSearchIn(scopeCatalog, cat)
	for _, r := range "paper" {
		s.Key(tea.KeyPressMsg{Code: r, Text: string(r)}, c)
	}
	s.catQuery = "paper"
	s.catRes = catalog.Results{Albums: []catalog.Album{fcAlbum, {ID: 22, Name: "Not Mine", Artist: "Juniper Vale"}}, Songs: fcSongs}
	s.catSongs = catalogTracks(fcSongs)
	s.rebuild()
	for _, sz := range [][2]int{{100, 30}, {70, 24}, {50, 20}} {
		ovCheckSize(t, "marked results", s, c, sz[0], sz[1])
	}
	out := s.Render(c)
	// the album, and songs 1 and 2; "Snow Angel" and "Not Mine" stay bare
	if got := checks(c, out); got != 3 {
		t.Fatalf("%d checks, want 3:\n%s", got, ansi.Strip(out))
	}
	cols := checkCols(c, out)
	if cols[0] != cols[1] || cols[1] != cols[2] {
		t.Errorf("checks in columns %v", cols)
	}
	// the scope that lists the library's own songs never marks
	lib := NewSearchIn(search.Songs, cat)
	for _, r := range "paper" {
		lib.Key(tea.KeyPressMsg{Code: r, Text: string(r)}, c)
	}
	if got := checks(c, lib.Render(c)); got != 0 {
		t.Errorf("%d checks in a library scope", got)
	}
}

func TestCatalogArtistPageMarks(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib = markLib()
	c.Catalog = &fakeCatalog{}
	v := newCatalogArtistView(fcArtist)
	runCmd(v.Init(c), func(m tea.Msg) tea.Cmd {
		if r, ok := m.(catalogResultMsg); ok {
			return r.apply(c)
		}
		return nil
	})
	out := v.Render(140, 42, c)
	// two top songs and the album tile
	if got := checks(c, out); got != 3 {
		t.Errorf("%d checks, want 3:\n%s", got, ansi.Strip(out))
	}
	for _, sz := range [][2]int{{100, 26}, {60, 18}, {30, 8}, {200, 60}} {
		checkSize(t, "marked artist page", v.Render(sz[0], sz[1], c), sz[0], sz[1])
	}
	// the column is not the favorite toggle
	if v.page.top.heartAt(130) {
		t.Error("the check column toggles favorites")
	}
}

func TestLibraryViewsNeverMark(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib = markLib()
	c.Focused = true
	album := NewAlbumView(c.Lib.Albums[0])
	if got := checks(c, album.Render(100, 26, c)); got != 0 {
		t.Errorf("library album page: %d checks", got)
	}
	table := NewTrackTable(c.Lib.Songs, TableOptions{Source: album.source()})
	if got := checks(c, table.Render(100, 20, c)); got != 0 {
		t.Errorf("songs table: %d checks", got)
	}
}

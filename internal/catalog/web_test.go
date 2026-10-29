package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// webSite serves the web page fixtures (music.apple.com pages, country=ca,
// trimmed of analytics) by path.
func webSite(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		name := map[string]string{
			"/ca/artist/657515":                                "web_artist_radiohead",
			"/ca/artist/475538413":                             "web_artist_lowroar",
			"/ca/playlist/pl.fdc21e2843764a8a99076ad5b6c40f4d": "web_playlist",
			"/ca/search":                                       "web_search",
		}[r.URL.Path]
		if name == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", name+".html"))
		if err != nil {
			t.Error(err)
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func webClient(t *testing.T, base string) *Client {
	return &Client{Country: "ca", WebBase: base, CacheDir: t.TempDir()}
}

func TestArtistPageSections(t *testing.T) {
	var hits atomic.Int32
	c := webClient(t, webSite(t, &hits).URL)
	p, err := c.ArtistPage(context.Background(), 657515)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{SectionEssentials: 4, SectionAlbums: 11, SectionSingles: 12, SectionLive: 3, SectionCompilations: 3, SectionAppearsOn: 1}
	for k, n := range want {
		if got := len(p.Sections[k]); got != n {
			t.Errorf("%s: %d albums, want %d", k, got, n)
		}
	}
	if !strings.Contains(p.Artwork, "/100x100bb.jpg") {
		t.Errorf("artist photo = %q", p.Artwork)
	}
	if e := p.Sections[SectionEssentials]; len(e) > 0 && (e[0].ID != 1109714933 || e[0].Name != "In Rainbows" || e[0].Note != "A groundbreaking, hitmaking classic." || !strings.Contains(e[0].Artwork, "/100x100bb.jpg")) {
		t.Errorf("essential album = %+v", e[0])
	}
	if a := p.Sections[SectionAlbums]; len(a) > 0 && (a[0].Year() == 0 || a[0].Artist != "Radiohead" || a[0].ArtistID != 657515) {
		t.Errorf("album = %+v", a[0])
	}
	if a := p.Sections[SectionAppearsOn]; len(a) > 0 && (a[0].Artist != "Thom Yorke" || a[0].ArtistID == 657515) {
		t.Errorf("appears on = %+v", a[0])
	}
	if p.Essentials == nil || p.Essentials.ID != "pl.fdc21e2843764a8a99076ad5b6c40f4d" || p.Essentials.Name != "Radiohead Essentials" {
		t.Errorf("essentials = %+v", p.Essentials)
	}
	if len(p.TopSongs) < 10 {
		t.Fatalf("top songs = %d", len(p.TopSongs))
	}
	s := p.TopSongs[0]
	if s.ID == 0 || s.Name == "" || s.Artist != "Radiohead" || s.AlbumID == 0 || s.Album == "" || s.Duration == 0 || !s.Streamable {
		t.Errorf("top song = %+v", s)
	}

	// Cached: in memory, then on disk for a new client.
	if _, err := c.ArtistPage(context.Background(), 657515); err != nil || hits.Load() != 1 {
		t.Errorf("second read: err %v, %d requests", err, hits.Load())
	}
	c2 := &Client{Country: "ca", WebBase: c.WebBase, CacheDir: c.CacheDir}
	if p2, err := c2.ArtistPage(context.Background(), 657515); err != nil || hits.Load() != 1 || len(p2.TopSongs) != len(p.TopSongs) {
		t.Errorf("disk read: err %v, %d requests", err, hits.Load())
	}
}

func TestArtistPageTopSongsOfTheirOwn(t *testing.T) {
	var hits atomic.Int32
	c := webClient(t, webSite(t, &hits).URL)
	p, err := c.ArtistPage(context.Background(), 475538413)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.TopSongs) == 0 || p.TopSongs[0].Album == "" || p.TopSongs[0].Released.IsZero() || p.TopSongs[0].Artist != "Low Roar" {
		t.Fatalf("top songs = %+v", p.TopSongs)
	}
	if len(p.Sections[SectionCompilations]) != 0 || len(p.Sections[SectionLive]) != 1 || p.Essentials != nil {
		t.Errorf("sections = %v, essentials %v", p.Sections, p.Essentials)
	}
}

func TestPlaylistSongs(t *testing.T) {
	var hits atomic.Int32
	c := webClient(t, webSite(t, &hits).URL)
	songs, err := c.PlaylistSongs(context.Background(), "pl.fdc21e2843764a8a99076ad5b6c40f4d")
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 35 {
		t.Fatalf("%d songs", len(songs))
	}
	s := songs[0]
	if s.ID != 1097861834 || s.Name != "Let Down" || s.Artist != "Radiohead" || s.ArtistID != 657515 || s.Album != "OK Computer" || s.AlbumID != 1097861387 {
		t.Errorf("song = %+v", s)
	}
	if _, err := c.PlaylistSongs(context.Background(), "../x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bad id: %v", err)
	}
}

func TestSearchStations(t *testing.T) {
	var hits atomic.Int32
	c := webClient(t, webSite(t, &hits).URL)
	st, err := c.SearchStations(context.Background(), "  NTS ")
	if err != nil {
		t.Fatal(err)
	}
	if len(st) < 5 || st[0].ID != "ra.1461123917" || st[0].Name != "NTS Radio 1" || st[0].Artwork == "" {
		t.Fatalf("stations = %+v", st)
	}
	for _, s := range st {
		if !strings.HasPrefix(s.ID, "ra.") {
			t.Errorf("station id %q", s.ID)
		}
	}
	if st, err := c.SearchStations(context.Background(), " "); err != nil || st != nil {
		t.Errorf("empty query: %v %v", st, err)
	}
}

func TestWebPageWithoutData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>nothing</body></html>"))
	}))
	defer srv.Close()
	c := webClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.ArtistPage(ctx, 1); err == nil {
		t.Error("want an error for a page without data")
	}
}

func TestMarker(t *testing.T) {
	for id, want := range map[string]string{
		"artists_657515_default.marker.LiveAlbums-SquareLockupSection":                        "LiveAlbums",
		"artists_657515_default.marker.shelfgroup.FeaturedContent.TopSongs-ArtistFeaturedCon": "TopSongs",
		"artists_1_default.marker.ArtistDetailHeader-ArtistDetailHeader":                      "ArtistDetailHeader",
		"track-list - pl.x": "",
	} {
		if got := marker(id); got != want {
			t.Errorf("marker(%q) = %q, want %q", id, got, want)
		}
	}
}

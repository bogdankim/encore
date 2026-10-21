package main

import (
	"bytes"
	"context"
	"image/jpeg"
	"strconv"
	"testing"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/ui"
)

var _ ui.Catalog = (*demoCatalog)(nil)
var _ ui.Catalog = lazyCatalog{}

func TestDemoCatalog(t *testing.T) {
	ctx := context.Background()
	f := bridge.NewFake(1)
	defer f.Close()
	snap, err := f.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lib := library.Build(snap)
	d := newDemoCatalog(lib, f)
	d.delay = 0

	res, err := d.Search(ctx, "juniper", 10)
	if err != nil || len(res.Artists) != 1 || res.Artists[0].Name != "Juniper Vale" || len(res.Albums) != 4 {
		t.Fatalf("search = %+v, %v", res, err)
	}
	ar, albums, err := d.ArtistAlbums(ctx, res.Artists[0].ID)
	if err != nil || ar.Name != "Juniper Vale" || len(albums) != 4 || albums[0].Year() != 2026 {
		t.Fatalf("albums = %+v, %v", albums, err)
	}
	al, songs, err := d.AlbumTracks(ctx, albums[0].ID)
	if err != nil || al.Name != "Weather Vane - Single" || len(songs) != 1 {
		t.Fatalf("tracks = %+v %+v, %v", al, songs, err)
	}
	top, err := d.ArtistTopSongs(ctx, ar.ID, 10)
	if err != nil || len(top) != 10 {
		t.Fatalf("top = %d, %v", len(top), err)
	}
	if _, _, err := d.AlbumTracks(ctx, 1); err != catalog.ErrNotFound {
		t.Errorf("unknown album: %v", err)
	}

	// a library album is in the catalog too
	la := lib.Albums[0]
	res, _ = d.Search(ctx, la.Title, 25)
	found := false
	for _, a := range res.Albums {
		found = found || a.Name == la.Title && a.Artist == la.Artist
	}
	if !found {
		t.Errorf("library album %q not in the catalog", la.Title)
	}

	// playing a catalog song shows its name
	if err := f.PlayStore(ctx, []string{strconv.FormatInt(songs[0].ID, 10)}, 0); err != nil {
		t.Fatal(err)
	}
	np, _ := f.State(ctx)
	if np.Track == nil || np.Track.Name != "Weather Vane" {
		t.Errorf("playing %+v", np.Track)
	}

	data, err := demoArt(ctx, catalog.ResizeArt(al.Artwork, 64))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("art: %v %v", img.Bounds(), err)
	}
}

// The demo library knows the catalog ids of most of its songs and follows
// two of the demo Home page's playlists, so the demo shows the marks for
// items already in the library, by id and by name.
func TestDemoLibraryMarks(t *testing.T) {
	ctx := context.Background()
	f := bridge.NewFake(1)
	defer f.Close()
	snap, _ := f.Snapshot(ctx)
	d := newDemoCatalog(library.Build(snap), f)
	d.delay = 0
	snap, _ = f.Snapshot(ctx) // now with catalog ids
	lib := library.Build(snap)

	withID := 0
	for _, s := range lib.Songs {
		if s.StoreID > 0 {
			withID++
		}
	}
	if share := withID * 100 / len(lib.Songs); share < 70 || share > 90 {
		t.Errorf("%d%% of the songs have a catalog id", share)
	}
	// every catalog song of a library album is in the library, by id or name
	la := lib.Albums[0]
	_, songs, err := d.AlbumTracks(ctx, albumID(t, d, la.Title, la.Artist))
	if err != nil || len(songs) != len(la.Tracks) {
		t.Fatalf("album songs %d, %v", len(songs), err)
	}
	for _, s := range songs {
		if lib.MatchSong(s.ID, s.Name, s.Artist) == nil {
			t.Errorf("%q is not in the library", s.Name)
		}
	}
	if lib.FindAlbum(la.Title, la.Artist) == nil {
		t.Error("library album not found by name")
	}
	// the extra albums are not
	res, _ := d.Search(ctx, "Night Versions", 5)
	for _, a := range res.Albums {
		if lib.FindAlbum(a.Name, a.Artist) != nil {
			t.Errorf("%q is in the library", a.Name)
		}
	}
	for _, id := range []string{"pl.demo-indie-rising", "pl.demo-chill-vibes"} {
		if lib.PlaylistByGlobalID(id) == nil {
			t.Errorf("playlist %s is not in the library", id)
		}
	}
	if lib.PlaylistByGlobalID("pl.demo-todays-hits") != nil {
		t.Error("an unfollowed playlist is in the library")
	}
}

func albumID(t *testing.T, d *demoCatalog, title, artist string) int64 {
	t.Helper()
	for _, a := range d.albums {
		if a.Name == title && a.Artist == artist {
			return a.ID
		}
	}
	t.Fatalf("no catalog album %q", title)
	return 0
}

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

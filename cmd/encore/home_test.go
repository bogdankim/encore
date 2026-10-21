package main

import (
	"context"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/library"
)

func TestMusicURL(t *testing.T) {
	u, err := musicURL("ca", "playlist", "pl.f4d106fed2bd41149aaacabb233eb5eb")
	if err != nil || u != "music://music.apple.com/ca/playlist/pl.f4d106fed2bd41149aaacabb233eb5eb" {
		t.Errorf("%q %v", u, err)
	}
	for _, ok := range [][4]string{
		{"us", "album", "1440857781", "music://music.apple.com/us/album/1440857781"},
		{"us", "album", "1440857781?i=1440857795", "music://music.apple.com/us/album/1440857781?i=1440857795"},
		{"us", "song", "1440857795", "music://music.apple.com/us/song/1440857795"},
	} {
		if u, err := musicURL(ok[0], ok[1], ok[2]); err != nil || u != ok[3] {
			t.Errorf("%v: %q %v", ok, u, err)
		}
	}
	for _, bad := range [][3]string{
		{"ca", "artist", "1"},
		{"ca", "song", "1?i=2"},
		{"ca", "album", "1?i=x"},
		{"ca", "playlist", "-a"},
		{"ca", "playlist", "pl.x/../y"},
		{"c a", "playlist", "pl.x"},
		{"ca", "playlist", "pl.x?y=1"},
	} {
		if _, err := musicURL(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

func TestDemoHome(t *testing.T) {
	fake := bridge.NewFake(1)
	defer fake.Close()
	snap, err := fake.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d := newDemoCatalog(library.Build(snap), fake)
	d.delay = 0
	h := newDemoHome(d)
	p, err := h.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shelves) < 5 || len(p.New) != 3 || time.Since(p.Updated) < time.Hour {
		t.Fatalf("%d shelves, %d new, updated %v", len(p.Shelves), len(p.New), p.Updated)
	}
	types := map[string]int{}
	live, withTracks := 0, 0
	for _, s := range append(p.Shelves, p.New...) {
		if len(s.Items) == 0 {
			t.Errorf("shelf %q is empty", s.Title)
		}
		for _, it := range s.Items {
			types[it.Type]++
			if it.IsLive {
				live++
				if !bridge.FakeLiveStation(it.ID) {
					t.Errorf("live %s isn't live in the fake", it.ID)
				}
			}
			if len(it.Tracks) > 0 {
				withTracks++
				ids := make([]int64, 0, len(it.Tracks))
				for _, s := range it.Tracks {
					var id int64
					for _, r := range s {
						id = id*10 + int64(r-'0')
					}
					ids = append(ids, id)
				}
				songs, _ := d.LookupSongs(context.Background(), ids)
				if len(songs) != len(ids) {
					t.Errorf("%s: %d of %d songs found", it.Title, len(songs), len(ids))
				}
			}
			if it.ArtURL(600) == "" {
				t.Errorf("%s has no art", it.Title)
			} else if _, err := demoArt(context.Background(), it.ArtURL(600)); err != nil {
				t.Errorf("%s: %v", it.Title, err)
			}
		}
	}
	for _, typ := range []string{home.TypeAlbum, home.TypePlaylist, home.TypeStation, home.TypeSong, home.TypeArtist} {
		if types[typ] == 0 {
			t.Errorf("no %s", typ)
		}
	}
	if live == 0 || withTracks == 0 {
		t.Errorf("live %d, playlists with songs %d", live, withTracks)
	}
}

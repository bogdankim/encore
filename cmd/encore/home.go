package main

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"time"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/ui"
)

// homeWait is how long the first frame waits for the Home page, read from
// disk while the bridge starts. Past it encore opens on Recently Added.
const homeWait = 300 * time.Millisecond

// preloadHome starts reading the Home page and returns a function that
// waits for it, up to homeWait from when it is called; nil without one.
func preloadHome(ctx context.Context, src ui.HomeSource) func() *home.Page {
	ch := make(chan *home.Page, 1)
	go func() {
		p, err := src.Load(ctx)
		if err != nil {
			p = nil
		}
		ch <- p
	}()
	return func() *home.Page {
		select {
		case p := <-ch:
			return p
		case <-time.After(homeWait):
			return nil
		}
	}
}

// LookupSongs looks songs up by id.
func (l lazyCatalog) LookupSongs(ctx context.Context, ids []int64) ([]catalog.Song, error) {
	return l.get().LookupSongs(ctx, ids)
}

// musicIDRe is what an Apple Music playlist, album or station id looks
// like ("pl.f4d106fed2bd41149aaacabb233eb5eb", "1440857781", "ra.978194965").
var musicIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// albumSongRe is a song on its album's page: "<album id>?i=<song id>".
var albumSongRe = regexp.MustCompile(`^[0-9]{1,20}\?i=[0-9]{1,20}$`)

// musicURL is the music:// link that has Music.app show an item: a
// playlist, album, song or station, or a song on its album's page (kind
// album, id "<album id>?i=<song id>").
func musicURL(country, kind, id string) (string, error) {
	switch kind {
	case home.TypePlaylist, home.TypeAlbum, home.TypeStation, home.TypeSong:
	default:
		return "", fmt.Errorf("no Music link for a %s", kind)
	}
	okID := musicIDRe.MatchString(id) || kind == home.TypeAlbum && albumSongRe.MatchString(id)
	if !okID || !regexp.MustCompile(`^[a-z]{2}$`).MatchString(country) {
		return "", fmt.Errorf("bad Music link %s/%s/%s", country, kind, id)
	}
	return "music://music.apple.com/" + country + "/" + kind + "/" + id, nil
}

// openInMusic has Music.app show an Apple Music item, through
// LaunchServices: Music comes to the front on its page.
func openInMusic(country func() string) func(ctx context.Context, kind, id string) error {
	return func(ctx context.Context, kind, id string) error {
		u, err := musicURL(country(), kind, id)
		if err != nil {
			return err
		}
		return exec.CommandContext(ctx, "/usr/bin/open", u).Run()
	}
}

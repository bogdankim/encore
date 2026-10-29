package ui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
)

// Library artist pages show the artist's Apple Music photo, as Apple
// Music artist pages do. Finding it takes a search for the artist (shared
// with Open Artist in Apple Music) and their Apple Music page, so a page
// asks once per artist and session, and the Artists list only once the
// cursor has rested on an artist for artistsPhotoRest. Until the photo
// comes, and when there is none, the newest album's cover stands in.

// artistsPhotoRest is how long the Artists list's cursor stays on an
// artist before encore looks up their photo: scrolling through the list
// mustn't spend the catalog's requests.
const artistsPhotoRest = time.Second

// artistPhotos holds what the lookups found by artist name: a photo URL,
// "" for none, or photoPending while a lookup runs. Only the UI goroutine
// touches it.
var artistPhotos = map[string]string{}

const photoPending = "pending"

// artistPhoto is the Apple Music photo URL of library artist a, or "".
func artistPhoto(a *library.Artist) string {
	if a == nil {
		return ""
	}
	if u := artistPhotos[a.Name]; u != photoPending {
		return u
	}
	return ""
}

// artistPhotoCmd looks up a's photo after delay, if still reports a is
// still wanted then. It does nothing when the photo is known or being
// looked up, or without a catalog.
func artistPhotoCmd(c *Ctx, a *library.Artist, delay time.Duration, still func() bool) tea.Cmd {
	if c == nil || c.Catalog == nil || a == nil || a.Name == "" || a.Name == library.VariousArtists {
		return nil
	}
	if _, ok := artistPhotos[a.Name]; ok {
		return nil
	}
	if delay > 0 {
		return tea.Tick(delay, func(time.Time) tea.Msg {
			return catalogResultMsg{apply: func(c *Ctx) tea.Cmd {
				if still != nil && !still() {
					return nil
				}
				return artistPhotoCmd(c, a, 0, nil)
			}}
		})
	}
	name := a.Name
	key := matchKey(Subject{artist: a})
	known, searched := catalogMatches[key]
	artistPhotos[name] = photoPending
	return catalogCmd(c.Catalog, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
		m, url, err := findArtistPhoto(ctx, cat, name, known, searched)
		return func(c *Ctx) tea.Cmd {
			if !searched && err == nil {
				remember(catalogMatches, key, m)
			}
			if err != nil && !errors.Is(err, catalog.ErrNotFound) {
				delete(artistPhotos, name) // busy or offline: try again next time
				return nil
			}
			artistPhotos[name] = url
			return nil
		}
	})
}

// findArtistPhoto finds the artist called name on Apple Music (unless m,
// an earlier match, says who they are) and reads their photo off their
// page.
func findArtistPhoto(ctx context.Context, cat Catalog, name string, m catalogMatch, searched bool) (catalogMatch, string, error) {
	if !searched {
		var err error
		if m, err = findInCatalog(ctx, cat, Subject{artist: &library.Artist{Name: name}}); err != nil {
			return m, "", err
		}
	}
	if !m.found || m.artist.ID <= 0 {
		return m, "", nil
	}
	page, err := cat.ArtistPage(ctx, m.artist.ID)
	if err != nil {
		return m, "", err
	}
	return m, page.Artwork, nil
}

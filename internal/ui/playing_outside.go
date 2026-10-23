package ui

import (
	"context"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Go to Album, Go to Artist and Open in Music.app for a playing song that is
// neither a library track nor a catalog song encore started: a song of an
// Apple Music station, which Music plays under a temporary pid. Its album
// and artist are found the way its art is (art_station.go): the library's
// when the library has the album or the artist, else the Apple Music
// catalog's, from one search for the song that is kept per pid. A live
// station has no songs to find.

const (
	noAlbumSongM  = "No album page for this song"
	noArtistSongM = "No artist page for this song"
	findAlbumM    = "Finding the album…"
	findArtistM   = "Finding the artist…"
	findSongM     = "Finding the song…"
)

// outsidePlaying is the playing song when it is from outside the library
// and not one encore started, or nil.
func (a *App) outsidePlaying() *bridge.CurrentTrack {
	ct := a.snap.NowPlaying.Track
	if ct == nil || a.snap.Track != nil || a.snap.Catalog != nil {
		return nil
	}
	return ct
}

// outsideNow is the clock the outside records use.
func (a *App) outsideNow() time.Time {
	if a.art != nil && a.art.now != nil {
		return a.art.now()
	}
	return time.Now()
}

// canFindOutside reports whether the catalog may know ct: a live station
// plays a stream, not a song.
func (a *App) canFindOutside(ct *bridge.CurrentTrack) bool {
	return !a.snap.Live && !(ct.Album == "" && ct.Duration <= 0)
}

// libraryAlbumOf finds the library album of the outside song ct: the album
// of that name by the song's album artist or its artist, and the song on it
// when the album has it. loose is the album of the song alone, found
// anywhere in the library (the same recording on a single or a
// compilation), when the album itself isn't there.
func libraryAlbumOf(lib *library.Library, ct *bridge.CurrentTrack) (al *library.Album, song, loose *library.Track) {
	if lib == nil {
		return nil, nil, nil
	}
	if ct.Album != "" {
		for _, credit := range []string{ct.AlbumArtist, ct.Artist, leadArtist(ct.Artist)} {
			if al = libraryAlbumFor(lib, ct.Album, credit); al != nil {
				return al, songOn(al, ct.Name), nil
			}
		}
	}
	if t := lib.FindSong(ct.Name, ct.Artist); t != nil && t.Album != nil {
		loose = t
	}
	return nil, nil, loose
}

// openLibrarySong opens the album of library song t with t selected.
func (a *App) openLibrarySong(t *library.Track) tea.Cmd {
	v := NewAlbumView(t.Album)
	v.want = trackWant{pid: t.PID}
	return a.Open(v)
}

// openPlayingOutside opens the album, or the artist, of the playing song
// from outside the library.
func (a *App) openPlayingOutside(artist bool) tea.Cmd {
	cur := a.outsidePlaying()
	if cur == nil {
		return a.setStatus("Nothing is playing", false)
	}
	ct := *cur
	none, finding := noAlbumSongM, findAlbumM
	if artist {
		none, finding = noArtistSongM, findArtistM
		if ar := libraryArtistForCredit(a.lib, ct.Artist); ar != nil {
			return a.OpenArtist(ar)
		}
	}

	// the library's album, or failing the catalog's, the song's album anywhere in it
	al, song, loose := libraryAlbumOf(a.lib, &ct)
	if !artist {
		switch {
		case song != nil:
			return a.openLibrarySong(song)
		case al != nil:
			return a.OpenAlbum(al)
		}
	}
	fallback := func() tea.Cmd {
		if !artist && loose != nil {
			return a.openLibrarySong(loose)
		}
		return a.setStatus(none, false)
	}
	if !a.canFindOutside(&ct) {
		return fallback()
	}
	return a.withOutsideSong(ct, finding, func(s catalog.Song, found bool, err error) tea.Cmd {
		switch {
		case err != nil:
			if !artist && loose != nil {
				return a.openLibrarySong(loose)
			}
			return a.setStatus(catalogErrText(err), false)
		case !found:
			return fallback()
		case artist:
			if s.ArtistID <= 0 {
				return a.setStatus(none, false)
			}
			return a.Open(newCatalogArtistView(artistSeed(s.ArtistID, leadArtist(s.Artist))))
		}
		return a.Open(catalogSongAlbumView(s))
	})
}

// catalogSongAlbumView is the catalog page of song s's album with s
// selected.
func catalogSongAlbumView(s catalog.Song) *CatalogAlbumView {
	seed, ok := catalogAlbums[s.AlbumID]
	if !ok {
		seed = catalog.Album{ID: s.AlbumID, ArtistID: s.ArtistID, Name: s.Album, Artist: s.Artist, Artwork: s.Artwork}
	}
	v := newCatalogAlbumView(seed)
	v.want = trackWant{pid: player.StorePID(strconv.FormatInt(s.ID, 10)), title: s.Name}
	return v
}

// withOutsideSong calls done with the catalog song ct is, on the UI
// goroutine: at once when a search settled that already, else after one,
// with finding in the footer meanwhile. When the song has changed by then
// nothing is done. err is the search's, or errNoCatalog.
func (a *App) withOutsideSong(ct bridge.CurrentTrack, finding string, done func(s catalog.Song, found bool, err error) tea.Cmd) tea.Cmd {
	if s, found, resolved := a.outside.resolvedSong(ct.PID); resolved {
		return done(s, found, nil)
	}
	cat := a.svc.Catalog
	if cat == nil {
		return done(catalog.Song{}, false, errNoCatalog)
	}
	key := ct.PID + "|" + finding
	if a.finding == key {
		return a.setStatus(finding, false)
	}
	a.finding = key
	return tea.Batch(a.setStatus(finding, false), catalogCmd(cat, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
		res, err := cat.SearchKinds(ctx, ct.Name+" "+ct.Artist, catalog.Songs, 10)
		return func(c *Ctx) tea.Cmd {
			if a.finding == key {
				a.finding = ""
			}
			a.outside.noteSong(ct.PID, a.outsideNow(), res.Songs, &ct, err)
			if a.status.text == finding {
				a.clearStatus()
			}
			if now := a.player.State().NowPlaying.Track; now == nil || now.PID != ct.PID {
				return nil
			}
			if err != nil {
				return done(catalog.Song{}, false, err)
			}
			s, found, _ := a.outside.resolvedSong(ct.PID)
			return done(s, found, nil)
		}
	}))
}

// outsideMenuItem is the playing outside song's Open in Music.app, when it
// can be: the song is known, or may be found when the item runs.
func (a *App) outsideMenuItem() (MenuItem, bool) {
	ct := a.outsidePlaying()
	if ct == nil || !a.canFindOutside(ct) {
		return MenuItem{}, false
	}
	if s, found, resolved := a.outside.resolvedSong(ct.PID); resolved {
		if !found {
			return MenuItem{}, false
		}
		p, ok := songPage(strconv.FormatInt(s.ID, 10), strconv.FormatInt(s.AlbumID, 10), s.Name)
		return musicMenuItem(p), ok
	}
	if a.svc.Catalog == nil {
		return MenuItem{}, false
	}
	cp := *ct
	return MenuItem{
		Label: keymap.Name(keymap.MusicOpen),
		Key:   keymap.MusicOpen,
		Run: func(Host) tea.Cmd {
			return a.withOutsideSong(cp, findSongM, func(s catalog.Song, found bool, err error) tea.Cmd {
				switch {
				case err != nil:
					return a.setStatus(catalogErrText(err), false)
				case !found:
					return a.setStatus(notInCatalogM, false)
				}
				p, ok := songPage(strconv.FormatInt(s.ID, 10), strconv.FormatInt(s.AlbumID, 10), s.Name)
				if !ok {
					return a.setStatus(notInCatalogM, false)
				}
				return a.OpenInMusic(p.kind, p.id, p.name)
			})
		},
	}, true
}

package ui

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Songs Music plays from outside the library (an Apple Music station's)
// play under temporary pids, and Music's scripting often has no artwork for
// them at all: `count of artworks` stays 0 for the whole song (measured on
// a personal station, 2026-10-21), so asking Music again never helps. Their
// now-playing art comes instead from the library album of the same name,
// when there is one, else from the Apple Music catalog, looked up by title
// and artist once Music has said it has none.

const (
	// outsideLookupAfter is how long a song plays before its art is looked
	// up in the catalog, so that skipping through a station doesn't spend
	// the catalog's rate limit on songs nobody waits for.
	outsideLookupAfter = time.Second
	outsideMax         = 64 // songs remembered
)

// outsideArt is the art found for songs from outside the library, by the
// pid Music plays them under. Lookups write it from their goroutines.
type outsideArt struct {
	mu    sync.Mutex
	songs map[string]*outsideSong
}

type outsideSong struct {
	seen   time.Time
	lib    *library.Library // the library ref was matched against
	ref    artRef           // the art to use; zero: none found (yet)
	looked bool             // the catalog was asked for the art

	// The catalog song this one is, for Go to Album, Go to Artist and Open
	// in Music.app (see playing_outside.go): set by whichever lookup ran
	// first. resolved: a search ended without an error; found: it matched
	// song.
	resolved, found bool
	song            catalog.Song
}

// entry is the record of pid, made when there is none. o.mu is held.
func (o *outsideArt) entry(pid string, now time.Time) *outsideSong {
	if o.songs == nil || len(o.songs) > outsideMax {
		o.songs = map[string]*outsideSong{}
	}
	s := o.songs[pid]
	if s == nil {
		s = &outsideSong{seen: now}
		o.songs[pid] = s
	}
	return s
}

// resolvedSong is what is known of the catalog song behind pid: the song,
// whether it matched, and whether a search has settled it.
func (o *outsideArt) resolvedSong(pid string) (song catalog.Song, found, resolved bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s := o.songs[pid]; s != nil {
		return s.song, s.found, s.resolved
	}
	return catalog.Song{}, false, false
}

// noteSong records the outcome of a catalog search for pid's song: the
// song it matched, or none. A failed search settles nothing.
func (o *outsideArt) noteSong(pid string, now time.Time, songs []catalog.Song, ct *bridge.CurrentTrack, err error) {
	if err != nil {
		return
	}
	song, ok := pickSong(songs, ct.Name, ct.Artist, ct.Album, secondsDur(ct.Duration))
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.entry(pid, now)
	s.resolved, s.found, s.song = true, ok, song
}

func secondsDur(sec float64) time.Duration { return time.Duration(sec * float64(time.Second)) }

// playingArt is the art to ask for, for the playing track whose own art is
// cur: cur itself, unless the track is from outside the library and its art
// was found elsewhere (see outsideArt).
func (a *App) playingArt(c *Ctx, cur artRef) artRef {
	ct := c.Player.NowPlaying.Track
	if c.Player.Track != nil || c.Player.Catalog != nil || ct == nil || cur.pid != ct.PID || a.lib == nil {
		return cur
	}
	o := &a.outside
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.entry(ct.PID, a.art.now())
	if s.lib != a.lib {
		s.lib = a.lib
		if ref, ok := libraryArtFor(a.lib, ct); ok {
			s.ref = ref
			artf("now playing %s (%s — %s): the library's %s", ct.PID, ct.Name, ct.Artist, ref.key)
		}
	}
	if s.ref.key != "" {
		return s.ref
	}
	if !s.looked && a.svc.Catalog != nil && a.art.answeredNone(ct.PID) && a.art.now().Sub(s.seen) >= outsideLookupAfter {
		s.looked = true
		if s.resolved && s.found && s.song.Artwork != "" {
			// Go to Album found the song already: no second search
			s.ref = catalogArtRef(s.song)
			return s.ref
		}
		go a.lookupOutsideArt(*ct)
	}
	return cur
}

// lookupOutsideArt finds ct's art in the Apple Music catalog and redraws
// when it does.
func (a *App) lookupOutsideArt(ct bridge.CurrentTrack) {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	res, err := a.svc.Catalog.SearchKinds(ctx, ct.Name+" "+ct.Artist, catalog.Songs, 10)
	cancel()
	a.outside.noteSong(ct.PID, a.art.now(), res.Songs, &ct, err)
	ref, ok := catalogArtFor(res.Songs, &ct)
	switch {
	case err != nil:
		artf("now playing %s (%s — %s): catalog search failed: %v", ct.PID, ct.Name, ct.Artist, err)
	case !ok:
		artf("now playing %s (%s — %s): not in the catalog's %d songs", ct.PID, ct.Name, ct.Artist, len(res.Songs))
	default:
		artf("now playing %s (%s — %s): the catalog's %s", ct.PID, ct.Name, ct.Artist, ref.key)
	}
	if !ok {
		return
	}
	a.outside.mu.Lock()
	if s := a.outside.songs[ct.PID]; s != nil {
		s.ref = ref
	}
	a.outside.mu.Unlock()
	a.art.poke()
}

// libraryArtFor finds the library album ct is from: one of its tracks with
// the same title and artist, or else the album of the same title by the same
// artist.
func libraryArtFor(lib *library.Library, ct *bridge.CurrentTrack) (artRef, bool) {
	title, artist, album := library.Fold(ct.Name), library.Fold(ct.Artist), library.Fold(ct.Album)
	albumArtist := library.Fold(ct.AlbumArtist)
	if albumArtist == "" {
		albumArtist = artist
	}
	if title == "" || artist == "" {
		return artRef{}, false
	}
	var song *library.Album
	for _, t := range lib.Songs {
		al := t.Album
		if al == nil || al.ArtPID == "" || t.FoldTitle != title || t.FoldArtist != artist {
			continue
		}
		if album == "" || t.FoldAlbum == album {
			return artRef{al.Key, al.ArtPID}, true
		}
		if song == nil {
			song = al
		}
	}
	if album != "" {
		for _, al := range lib.Albums {
			if al.ArtPID != "" && al.FoldTitle == album && (al.FoldArtist == albumArtist || al.FoldArtist == artist) {
				return artRef{al.Key, al.ArtPID}, true
			}
		}
	}
	if song != nil {
		// the same recording on another of its albums (a single, a
		// compilation): still its cover, rather than none
		return artRef{song.Key, song.ArtPID}, true
	}
	return artRef{}, false
}

// catalogArtFor picks ct among catalog search results: the same title by
// the same artist, from the same album when one is, else a song from the
// same album. Its art goes by its album's catalog key, like catalog pages.
func catalogArtFor(songs []catalog.Song, ct *bridge.CurrentTrack) (artRef, bool) {
	title, artist, album := library.Fold(ct.Name), library.Fold(ct.Artist), library.Fold(ct.Album)
	sameArtist := func(s catalog.Song) bool {
		f := library.Fold(s.Artist)
		return f != "" && (f == artist || strings.Contains(f, artist) || strings.Contains(artist, f))
	}
	best, score := -1, 0
	for i, s := range songs {
		if s.Artwork == "" || !sameArtist(s) {
			continue
		}
		n := 0
		sameAlbum := album != "" && library.Fold(s.Album) == album
		switch {
		case library.Fold(s.Name) == title && sameAlbum:
			n = 3
		case library.Fold(s.Name) == title:
			n = 2
		case sameAlbum:
			n = 1
		}
		if n > score {
			best, score = i, n
		}
	}
	if best < 0 {
		return artRef{}, false
	}
	return catalogArtRef(songs[best]), true
}

// catalogArtRef is the art of catalog song s, by its album's catalog key.
func catalogArtRef(s catalog.Song) artRef {
	key := player.CatalogAlbumPrefix + strconv.FormatInt(s.AlbumID, 10)
	if s.AlbumID <= 0 {
		key = player.StorePID(strconv.FormatInt(s.ID, 10))
	}
	return artRef{key, s.ArtURL(catalogArtPx)}
}

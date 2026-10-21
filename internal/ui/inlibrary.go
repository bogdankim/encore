package ui

import (
	"strconv"
	"strings"

	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Apple Music items the library already has. Wherever the UI lists catalog
// items (search results, Apple Music pages, Home) it marks the ones the
// library has with one dim check (theme Glyphs.InLib). The rules are the
// library's (library/match.go): songs and playlists by id, albums by title
// and artist, songs without an id on either side by title and artist.
// Library views never mark: everything in them is in the library.
//
// Rows draw every frame, so answers are remembered per library. Only the UI
// goroutine asks.

// libMarks holds the answers for one library; a new library starts over.
type libMarks struct {
	lib   *library.Library
	songs map[int64]bool  // by catalog id
	named map[string]bool // songs without an id, albums ("a"), playlists ("p")
}

var marks libMarks

// libMarksMax bounds the memo; past it, it starts over.
const libMarksMax = 20000

func (m *libMarks) reset(lib *library.Library) {
	if m.lib != lib || len(m.songs)+len(m.named) > libMarksMax {
		m.lib = lib
		m.songs = map[int64]bool{}
		m.named = map[string]bool{}
	}
}

// CatalogSongInLibrary reports whether the library has catalog song id
// (title by artist; id 0 when the source has none).
func (c *Ctx) CatalogSongInLibrary(id int64, title, artist string) bool {
	if c.Lib == nil {
		return false
	}
	marks.reset(c.Lib)
	if id > 0 {
		v, ok := marks.songs[id]
		if !ok {
			v = c.Lib.MatchSong(id, title, artist) != nil
			marks.songs[id] = v
		}
		return v
	}
	return marks.named1("s", title, artist, func() bool { return c.Lib.MatchSong(0, title, artist) != nil })
}

// CatalogAlbumInLibrary reports whether the library has the album title by
// artist.
func (c *Ctx) CatalogAlbumInLibrary(title, artist string) bool {
	if c.Lib == nil {
		return false
	}
	marks.reset(c.Lib)
	return marks.named1("a", title, artist, func() bool { return c.Lib.FindAlbum(title, artist) != nil })
}

// CatalogPlaylistInLibrary reports whether the library has the playlist with
// this catalog id ("pl.…").
func (c *Ctx) CatalogPlaylistInLibrary(id string) bool {
	if c.Lib == nil || id == "" {
		return false
	}
	marks.reset(c.Lib)
	return marks.named1("p", id, "", func() bool { return c.Lib.PlaylistByGlobalID(id) != nil })
}

func (m *libMarks) named1(kind, a, b string, f func() bool) bool {
	k := kind + "\x00" + a + "\x00" + b
	v, ok := m.named[k]
	if !ok {
		v = f()
		m.named[k] = v
	}
	return v
}

// StandInInLibrary reports whether t is a catalog song (a stand-in) the
// library has. A library track is never marked.
func (c *Ctx) StandInInLibrary(t *library.Track) bool {
	if t == nil || !player.IsCatalog(t) {
		return false
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(t.PID, "store:"), 10, 64)
	return c.CatalogSongInLibrary(id, t.Title, t.Artist)
}

// CatalogAlbumTileInLibrary reports whether a is a catalog album (key
// "catalog:<id>") the library has. A library album is never marked.
func (c *Ctx) CatalogAlbumTileInLibrary(a *library.Album) bool {
	return isCatalogAlbum(a) && c.CatalogAlbumInLibrary(a.Title, a.Artist)
}

// HomeItemInLibrary reports whether the library has Home item it: an album,
// a song or a playlist. Stations and artists aren't marked.
func (c *Ctx) HomeItemInLibrary(it home.Item) bool {
	switch it.Type {
	case home.TypeSong:
		id, _ := strconv.ParseInt(it.ID, 10, 64)
		return c.CatalogSongInLibrary(id, it.Title, it.Subtitle)
	case home.TypeAlbum:
		return c.CatalogAlbumInLibrary(it.Title, it.Subtitle)
	case home.TypePlaylist:
		return c.CatalogPlaylistInLibrary(it.ID)
	}
	return false
}

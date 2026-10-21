package ui

import "github.com/bogdankim/encore/internal/library"

// Keys of the library sections. Views for these sections must return these
// from Key() so the sidebar can mark the open section and go.* actions can
// tell the section is already open.
const (
	KeyHome      = "home"
	KeyRecent    = "recent"
	KeyArtists   = "artists"
	KeyAlbums    = "albums"
	KeySongs     = "songs"
	KeyFavorites = "favorites"
)

// viewCtors are the views built in other files of this package (album grid,
// artists, album and artist pages). They register here from an init func.
// A nil constructor makes the section show "… isn't available yet".
var viewCtors struct {
	RecentlyAdded func(lib *library.Library) View
	Artists       func(lib *library.Library) View
	Albums        func(lib *library.Library) View
	Album         func(a *library.Album) View
	Artist        func(a *library.Artist) View
}

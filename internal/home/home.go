// Package home builds Apple Music's Home page from the responses Music.app
// keeps in its URL cache: the personal recommendations ("listen-now") and
// the live radio stations and a few editorial shelves of the New tab
// (groupings name=music). Music fetches both when it shows Home and
// NSURLCache evicts them later, so a Reader copies the parts it keeps to
// encore's cache and serves that copy once Music's is gone.
//
// Only what the Home page shows is kept: shelf titles and the items'
// names, artists or curators, artwork and play ids. Shelves about friends,
// social profiles and concerts are dropped while parsing and never stored.
package home

import (
	"strconv"
	"strings"
	"time"
)

// Item types.
const (
	TypeAlbum    = "album"
	TypePlaylist = "playlist"
	TypeStation  = "station"
	TypeSong     = "song"
	TypeArtist   = "artist"
)

// Shelf kinds. A shelf of one item type gets that type's kind.
const (
	KindRecentlyPlayed = "recently-played"
	KindFavorites      = "favorites"
	KindFeatured       = "featured" // Music's untitled hero card
	KindMixes          = "mixes"    // personal mixes (Your Essentials, Chill, …)
	KindReplay         = "replay"
	KindStations       = "stations"
	KindAlbums         = "albums"
	KindPlaylists      = "playlists"
	KindSongs          = "songs"
	KindLive           = "live" // live radio stations, from the New tab
	KindMixed          = "mixed"
)

// Page is the Home page.
type Page struct {
	// Updated is when Music fetched the newest part of the page.
	Updated time.Time
	// Shelves are the personal recommendations in Music's order, then
	// Live Radio.
	Shelves []Shelf
	// New holds a few shelves of the New tab: new songs, new albums and
	// updated playlists, in Music's order.
	New []Shelf
}

// Shelf is one row of the page.
type Shelf struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Items []Item `json:"items"`
}

// Item is one card on a shelf.
type Item struct {
	Type string `json:"type"`
	// ID is what plays it: a catalog album or song id, pl.… for a
	// playlist, ra.… for a station.
	ID    string `json:"id"`
	Title string `json:"title"`
	// Subtitle is the artist, the playlist's curator, or for a live station
	// the show on now ("Live" when that is unknown or only the station's name).
	Subtitle     string  `json:"subtitle,omitempty"`
	Art          Artwork `json:"art,omitzero"`
	Explicit     bool    `json:"explicit,omitempty"`
	PlaylistType string  `json:"playlistType,omitempty"` // editorial, personal-mix, replay, user-shared
	StationKind  string  `json:"stationKind,omitempty"`  // artistSeeded, artistOnly, query, discovery, user, streaming
	IsLive       bool    `json:"live,omitempty"`
	// Events is a live station's schedule.
	Events []Event `json:"events,omitempty"`

	// NowPlaying is the live station's show on air when the page was
	// loaded, "" when none is.
	NowPlaying string `json:"-"`
	// Tracks are a playlist's song store ids, when Music's cache had them
	// (nil otherwise). They play through the store queue.
	Tracks []string `json:"-"`
}

// Event is a show on a live station.
type Event struct {
	Title string    `json:"title"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Artwork is an image URL template from amp-api, with {w}, {h}, {c} and
// {f} to fill in.
type Artwork struct {
	Template  string `json:"url"`
	Width     int    `json:"w,omitempty"`
	Height    int    `json:"h,omitempty"`
	BgColor   string `json:"bg,omitempty"`   // hex, no #
	TextColor string `json:"text,omitempty"` // textColor1
}

// URL fills the template for an image of at most w×h pixels; "" without
// a template.
func (a Artwork) URL(w, h int) string {
	if a.Template == "" {
		return ""
	}
	return strings.NewReplacer(
		"{w}", strconv.Itoa(w),
		"{h}", strconv.Itoa(h),
		"{c}", "bb",
		"{f}", "jpg",
	).Replace(a.Template)
}

// ArtURL is the item's artwork for a size×size box.
func (i Item) ArtURL(size int) string { return i.Art.URL(size, size) }

// onAir is the event on air at now, or nil.
func onAir(events []Event, now time.Time) *Event {
	for i := range events {
		e := &events[i]
		if !now.Before(e.Start) && now.Before(e.End) {
			return e
		}
	}
	return nil
}

// present fills the fields that depend on the time and on playlist
// tracks, on a copy of the shelves.
func present(shelves []Shelf, now time.Time, tracks map[string][]string) []Shelf {
	out := make([]Shelf, len(shelves))
	for i, s := range shelves {
		s.Items = append([]Item(nil), s.Items...)
		for j := range s.Items {
			it := &s.Items[j]
			it.Events = append([]Event(nil), it.Events...)
			if it.IsLive {
				it.Subtitle = "Live"
				if e := onAir(it.Events, now); e != nil {
					it.NowPlaying = e.Title
					if e.Title != it.Title {
						it.Subtitle = e.Title
					}
				}
			}
			if it.Type == TypePlaylist {
				if t := tracks[it.ID]; len(t) > 0 {
					it.Tracks = append([]string(nil), t...)
				}
			}
		}
		out[i] = s
	}
	return out
}

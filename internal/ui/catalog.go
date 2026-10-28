package ui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Catalog is the Apple Music catalog as the UI uses it: *catalog.Client in
// real mode, an offline fixture in demo mode. Calls run in tea.Cmds, never
// on the UI goroutine.
type Catalog interface {
	Search(ctx context.Context, q string, limit int) (catalog.Results, error)
	// SearchKinds is Search for some kinds only: a request per kind.
	SearchKinds(ctx context.Context, q string, kinds catalog.Kind, limit int) (catalog.Results, error)
	ArtistAlbums(ctx context.Context, artistID int64) (catalog.Artist, []catalog.Album, error)
	ArtistTopSongs(ctx context.Context, artistID int64, limit int) ([]catalog.Song, error)
	AlbumTracks(ctx context.Context, collectionID int64) (catalog.Album, []catalog.Song, error)
	// ArtistPage is what the artist's Apple Music page lists beyond the
	// albums: sections, top songs and their Essentials playlist.
	ArtistPage(ctx context.Context, artistID int64) (catalog.ArtistPage, error)
	PlaylistSongs(ctx context.Context, playlistID string) ([]catalog.Song, error)
	SearchStations(ctx context.Context, q string) ([]catalog.Station, error)
}

const (
	// catalogQuiet is how long a catalog request runs before the UI says
	// it is loading.
	catalogQuiet = 300 * time.Millisecond
	// catalogTimeout bounds one page load; the client's rate limiter may
	// make a request wait up to 15 s for its turn.
	catalogTimeout = 30 * time.Second
	// catalogArtPx is the artwork size kept in catalog items' ArtURL; the
	// art store asks for the size it draws at.
	catalogArtPx = 600

	catalogBusyM    = "Apple Music search is busy, try again in a moment"
	catalogOfflineM = "Can’t reach Apple Music"
	catalogFavM     = "Only songs in your library can be favorites"
)

// catalogErrText is the one line shown for a failed catalog request.
func catalogErrText(err error) string {
	switch {
	case errors.Is(err, catalog.ErrRateLimited):
		return catalogBusyM
	case errors.Is(err, catalog.ErrNotFound):
		return "Not found on Apple Music"
	}
	return catalogOfflineM
}

// catalogResultMsg carries the result of a catalog request back to the UI
// goroutine: apply runs there, on the view or overlay that asked, whether
// or not it is still on screen.
type catalogResultMsg struct {
	apply func(c *Ctx) tea.Cmd
}

// catalogTickMsg redraws once a catalog request has run catalogQuiet, so
// a loading note can appear.
type catalogTickMsg struct{}

func catalogQuietTick() tea.Cmd {
	return tea.Tick(catalogQuiet, func(time.Time) tea.Msg { return catalogTickMsg{} })
}

// The catalog songs, albums and artists the UI has shown, by stand-in pid
// and catalog id, so a stand-in track found in a menu or Up Next leads back
// to its item, album and artist. Only the UI goroutine touches them.
var (
	catalogItems   = map[string]player.CatalogItem{}
	catalogAlbums  = map[int64]catalog.Album{}
	catalogArtists = map[int64]catalog.Artist{}
)

// catalogRegistryMax bounds the registries; past it they start over, and
// the few lookups that miss fall back to what the stand-in carries.
const catalogRegistryMax = 20000

func remember[K comparable, V any](m map[K]V, k K, v V) {
	if len(m) >= catalogRegistryMax {
		clear(m)
	}
	m[k] = v
}

// catalogTrack is the stand-in track for a catalog song, as views list it.
func catalogTrack(s catalog.Song) *library.Track {
	it := player.CatalogItemFromSong(s, catalogArtPx)
	t := player.StandIn(it)
	t.Explicit = s.Explicit
	t.Available = s.Streamable && s.ID > 0 // false: in the catalog, not streamable here
	t.TrackNo, t.Disc = s.TrackNumber, s.DiscNumber
	t.Genre = s.Genre
	if !s.Released.IsZero() {
		t.Year = s.Released.Year()
	}
	remember(catalogItems, t.PID, it)
	return t
}

func catalogTracks(songs []catalog.Song) []*library.Track {
	ts := make([]*library.Track, len(songs))
	for i, s := range songs {
		ts[i] = catalogTrack(s)
	}
	return ts
}

// catalogItemOf returns the catalog item behind stand-in t.
func catalogItemOf(t *library.Track) (player.CatalogItem, bool) {
	if !player.IsCatalog(t) {
		return player.CatalogItem{}, false
	}
	if it, ok := catalogItems[t.PID]; ok {
		return it, true
	}
	it := player.CatalogItem{
		StoreID:  strings.TrimPrefix(t.PID, "store:"),
		Title:    t.Title,
		Artist:   t.Artist,
		Album:    t.AlbumName,
		Duration: t.Duration,
	}
	if t.Album != nil {
		if id := strings.TrimPrefix(t.Album.Key, player.CatalogAlbumPrefix); digits(id) {
			it.AlbumID = id
		}
		it.ArtURL = t.Album.ArtPID
	}
	return it, true
}

// catalogAlbum is the library-shaped album a grid shows for a catalog
// album: key "catalog:<id>", no tracks, art fetched by URL.
func catalogAlbum(a catalog.Album) *library.Album {
	remember(catalogAlbums, a.ID, a)
	return &library.Album{
		Key:        player.CatalogAlbumPrefix + strconv.FormatInt(a.ID, 10),
		Title:      a.Name,
		Artist:     a.Artist,
		Year:       a.Year(),
		Genre:      a.Genre,
		ArtPID:     a.ArtURL(catalogArtPx),
		FoldTitle:  library.Fold(a.Name),
		FoldArtist: library.Fold(a.Artist),
	}
}

// catalogAlbumID is the catalog id in a catalog album's key.
func catalogAlbumID(key string) (int64, bool) {
	s, ok := strings.CutPrefix(key, player.CatalogAlbumPrefix)
	if !ok || !digits(s) {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}

// isCatalogAlbum reports whether a is a catalog album or a stand-in's.
func isCatalogAlbum(a *library.Album) bool {
	return a != nil && strings.HasPrefix(a.Key, player.CatalogAlbumPrefix)
}

func allCatalog(ts []*library.Track) bool {
	for _, t := range ts {
		if !player.IsCatalog(t) {
			return false
		}
	}
	return len(ts) > 0
}

// catalogItemsOf converts stand-ins to items, dropping songs Apple
// doesn't stream here. start is i's place among them, or the next
// playable one's.
func catalogItemsOf(ts []*library.Track, i int) (items []player.CatalogItem, start int) {
	start = -1
	for k, t := range ts {
		it, ok := catalogItemOf(t)
		if !ok || !t.Available {
			continue
		}
		if start < 0 && k >= i {
			start = len(items)
		}
		items = append(items, it)
	}
	return items, start
}

// splitRuns cuts ts into runs of one kind, catalog or library, in order.
func splitRuns(ts []*library.Track) [][]*library.Track {
	var runs [][]*library.Track
	for i := 0; i < len(ts); {
		j := i + 1
		for j < len(ts) && player.IsCatalog(ts[j]) == player.IsCatalog(ts[i]) {
			j++
		}
		runs = append(runs, ts[i:j])
		i = j
	}
	return runs
}

// PlayingTrackPID is the pid views mark as playing: the stand-in pid of
// a catalog song encore started, else Music's pid for the track.
func (c *Ctx) PlayingTrackPID() string {
	if it := c.Player.Catalog; it != nil {
		return player.StorePID(it.StoreID)
	}
	return c.PlayingPID()
}

// Album matching: a catalog album or artist that is also in the library
// links to its library page.

// albumMatchKey, songMatchKey, sameName and sameArtist are the library's own
// matching rules (library/match.go), which the marks for items already in the
// library share.
var (
	albumMatchKey = library.AlbumKey
	songMatchKey  = library.SongKey
	sameName      = library.SameName
	sameArtist    = library.SameArtist
)

// libraryAlbumFor finds the library album with this title by this artist.
func libraryAlbumFor(lib *library.Library, title, artist string) *library.Album {
	return lib.FindAlbum(title, artist)
}

// libraryArtistFor finds the library artist with this name.
func libraryArtistFor(lib *library.Library, name string) *library.Artist {
	if lib == nil || name == "" {
		return nil
	}
	if a := lib.Artist(name); a != nil {
		return a
	}
	f := library.Fold(name)
	for _, a := range lib.Artists {
		if a.FoldName == f {
			return a
		}
	}
	return nil
}

// catalogNavItems are Go to Album and Go to Artist for catalog stand-ins,
// when they share one.
func catalogNavItems(ts []*library.Track) []MenuItem {
	if len(ts) == 0 {
		return nil
	}
	first, _ := catalogItemOf(ts[0])
	albumID, artistID := first.AlbumID, first.ArtistID
	for _, t := range ts[1:] {
		it, _ := catalogItemOf(t)
		if it.AlbumID != albumID {
			albumID = ""
		}
		if it.ArtistID != artistID {
			artistID = ""
		}
	}
	var nav []MenuItem
	if id, err := strconv.ParseInt(albumID, 10, 64); err == nil && id > 0 {
		seed := catalog.Album{ID: id, Name: first.Album, Artist: first.Artist, Artwork: first.ArtURL}
		nav = append(nav, MenuItem{Label: "Go to Album", Run: func(h Host) tea.Cmd { return h.Open(newCatalogAlbumView(seed)) }})
	}
	if id, err := strconv.ParseInt(artistID, 10, 64); err == nil && id > 0 {
		seed := catalog.Artist{ID: id, Name: first.Artist}
		nav = append(nav, MenuItem{Label: "Go to Artist", Run: func(h Host) tea.Cmd { return h.Open(newCatalogArtistView(seed)) }})
	}
	return nav
}

// catalogMenuItems are the menu for catalog songs: the play items, the
// way to their album and artist, and to those in the library, then Open
// in Music.app. Favorites are for library songs only.
func catalogMenuItems(ts []*library.Track) []MenuItem {
	src := player.Source{Kind: player.KindCatalog, Title: ts[0].Title}
	if len(ts) > 1 {
		src.Title = plural(len(ts), "Song", "Songs")
	}
	items := menuGroup(playMenuItems(src, ts), catalogNavItems(ts)...)
	items = menuGroup(items, openInItems(true, subjectOf(ts))...)
	return menuGroup(items, musicMenuItems(ts)...)
}

// PlayingStation is the id of the radio station playing, or "".
func (c *Ctx) PlayingStation() string {
	if st := c.Player.Station; st != nil {
		return st.ID
	}
	return ""
}

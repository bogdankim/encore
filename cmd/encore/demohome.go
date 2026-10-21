package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/home"
)

// demoHome is Apple Music's Home page for --demo, made up from the demo
// catalog: personal shelves with albums, playlists, stations and artists,
// Live Radio, and the New tab's songs, albums and playlists. Its stations
// play through the fake bridge (the live ones as live radio), its albums
// open their demo catalog pages, and one mix carries songs so it plays.
type demoHome struct {
	page func(now time.Time) *home.Page
	now  func() time.Time
}

func (d demoHome) Load(ctx context.Context) (*home.Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.page(d.now()), nil
}

func (demoHome) Changed() bool { return false }

// demoTemplate turns a demo artwork URL into an amp-api style template.
func demoTemplate(url string) home.Artwork {
	return home.Artwork{Template: strings.Replace(url, "100x100bb.jpg", "{w}x{h}{c}.{f}", 1)}
}

func demoHomeArt(key string) home.Artwork { return demoTemplate(demoArtURL(key)) }

func newDemoHome(d *demoCatalog) demoHome {
	byArtist := func(name string) []catalog.Album {
		var out []catalog.Album
		for _, a := range d.albums {
			if a.Artist == name {
				out = append(out, a)
			}
		}
		return out
	}
	album := func(a catalog.Album) home.Item {
		return home.Item{Type: home.TypeAlbum, ID: strconv.FormatInt(a.ID, 10), Title: a.Name, Subtitle: a.Artist, Art: demoTemplate(a.Artwork), Explicit: a.Explicit}
	}
	station := func(id, name, kind string) home.Item {
		return home.Item{Type: home.TypeStation, ID: id, Title: name, StationKind: kind, Art: demoHomeArt("station-" + id)}
	}
	mix := func(id, name string, tracks []string) home.Item {
		return home.Item{Type: home.TypePlaylist, ID: id, Title: name, Subtitle: "Apple Music for You", PlaylistType: "personal-mix", Art: demoHomeArt("mix-" + id), Tracks: tracks}
	}
	playlist := func(id, name string) home.Item {
		return home.Item{Type: home.TypePlaylist, ID: id, Title: name, Subtitle: "Apple Music", PlaylistType: "editorial", Art: demoHomeArt("pl-" + id)}
	}

	var catalogOnly, fromLibrary []catalog.Album
	for _, da := range demoArtists {
		catalogOnly = append(catalogOnly, byArtist(da.name)...)
	}
	for _, a := range d.albums {
		if len(fromLibrary) < 14 && !strings.HasPrefix(a.Artwork, "demo://cover/extra-") {
			fromLibrary = append(fromLibrary, a)
		}
	}
	var extras []catalog.Album
	for _, a := range d.albums {
		if strings.HasPrefix(a.Artwork, "demo://cover/extra-") && !isDemoArtist(a.Artist) {
			extras = append(extras, a)
		}
	}
	albums := func(as []catalog.Album, n int) []home.Item {
		var out []home.Item
		for _, a := range as[:min(n, len(as))] {
			out = append(out, album(a))
		}
		return out
	}

	var artistStations, artists []home.Item
	for _, ar := range d.artists[:min(10, len(d.artists))] {
		artistStations = append(artistStations, station(
			"ra."+strconv.FormatInt(ar.ID, 10), ar.Name+" Station", "artistSeeded"))
	}
	for _, ar := range d.artists[len(d.artists)-min(len(d.artists), 8):] {
		artists = append(artists, home.Item{Type: home.TypeArtist, ID: strconv.FormatInt(ar.ID, 10), Title: ar.Name, Art: demoHomeArt("artist-" + ar.Name)})
	}

	// The favourites mix carries the songs of a catalog-only album, so it
	// plays; the other mixes open in Music, as most do in real use.
	var mixTracks []string
	var songs []home.Item
	for _, s := range d.songs {
		if !isDemoArtist(s.Artist) || !s.Streamable {
			continue
		}
		id := strconv.FormatInt(s.ID, 10)
		if len(mixTracks) < 12 {
			mixTracks = append(mixTracks, id)
		}
		if len(songs) < 16 && s.TrackNumber <= 2 {
			songs = append(songs, home.Item{Type: home.TypeSong, ID: id, Title: s.Name, Subtitle: s.Artist, Art: demoTemplate(s.Artwork), Explicit: s.Explicit})
		}
	}

	// A few songs the demo library has go in among the new ones, so the
	// page shows what is in the library and what isn't.
	var owned []home.Item
	for _, s := range d.songs {
		if len(owned) < 4 && !isDemoArtist(s.Artist) && s.Streamable && s.TrackNumber == 2 && !strings.HasPrefix(s.Artwork, "demo://cover/extra-") {
			owned = append(owned, home.Item{Type: home.TypeSong, ID: strconv.FormatInt(s.ID, 10), Title: s.Name, Subtitle: s.Artist, Art: demoTemplate(s.Artwork), Explicit: s.Explicit})
		}
	}
	for i, it := range owned {
		at := min(1+2*i, len(songs))
		songs = append(songs[:at], append([]home.Item{it}, songs[at:]...)...)
	}

	page := func(now time.Time) *home.Page {
		hour := now.Truncate(time.Hour)
		live := func(id, name string, show string) home.Item {
			it := station(id, name, "streaming")
			it.IsLive = true
			it.Events = []home.Event{{Title: show, Start: hour, End: hour.Add(time.Hour)}}
			it.Subtitle, it.NowPlaying = "Live", show
			if show != name {
				it.Subtitle = show
			}
			return it
		}
		shows := []string{"The Morning Mix", "Northern Lights Radio", "Weekend Sessions", "The Night Shift"}
		show := shows[now.Hour()%len(shows)]
		top := []home.Item{
			station("ra.u-demo", "Your Station", "user"),
			mix("pl.pm-demo-favourites", "Favourites Mix", mixTracks),
		}
		top = append(top, albums(catalogOnly, 4)...)
		top = append(top, station("ra.q-demo-chill", "Chill", "query"))
		recent := append(albums(fromLibrary, 5), station("ra.u-demo", "Your Station", "user"))
		return &home.Page{
			Updated: now.Add(-2*time.Hour - 10*time.Minute),
			Shelves: []home.Shelf{
				{ID: "demo-top", Title: "Top Picks for You", Kind: home.KindMixed, Items: top},
				{ID: "demo-recent", Title: "Recently Played", Kind: home.KindRecentlyPlayed, Items: recent},
				{ID: "demo-stations", Title: "Stations for You", Kind: home.KindStations, Items: artistStations},
				{ID: "demo-mixes", Title: "Playlists Made for You", Kind: home.KindMixes, Items: []home.Item{
					mix("pl.pm-demo-favourites", "Favourites Mix", mixTracks),
					mix("pl.pm-demo-new", "New Music Mix", nil),
					mix("pl.pm-demo-chill", "Chill Mix", nil),
					mix("pl.pm-demo-getup", "Get Up! Mix", nil),
					mix("pl.pm-demo-heavy", "Heavy Rotation Mix", nil),
				}},
				{ID: "demo-albums", Title: "New Releases for You", Kind: home.KindAlbums, Items: albums(extras, 12)},
				{ID: "demo-moods", Title: "Find Your Mood", Kind: home.KindStations, Items: []home.Item{
					station("ra.q-demo-chill", "Chill", "query"),
					station("ra.q-demo-focus", "Focus", "query"),
					station("ra.q-demo-workout", "Workout", "query"),
					station("ra.q-demo-sleep", "Sleep", "query"),
					station("ra.q-demo-party", "Party", "query"),
					station("ra.q-demo-feelgood", "Feel Good", "query"),
				}},
				{ID: "demo-artists", Title: "Artists You Might Like", Kind: home.KindMixed, Items: artists},
				{ID: "demo-live", Title: "Live Radio", Kind: home.KindLive, Items: []home.Item{
					live("ra.978194965", "Apple Music 1", show),
					live("ra.1498155548", "Apple Music Hits", "Apple Music Hits"),
					live("ra.1498157166", "Apple Music Country", "Country Gold Hour"),
				}},
			},
			New: []home.Shelf{
				{ID: "demo-new-songs", Title: "Best New Songs", Kind: home.KindSongs, Items: songs},
				{ID: "demo-new-albums", Title: "New This Week", Kind: home.KindAlbums, Items: albums(catalogOnly[min(4, len(catalogOnly)):], 8)},
				{ID: "demo-new-playlists", Title: "Updated Playlists", Kind: home.KindPlaylists, Items: []home.Item{
					playlist("pl.demo-todays-hits", "Today’s Hits"),
					playlist("pl.demo-new-music-daily", "New Music Daily"),
					playlist("pl.demo-indie-rising", "Indie Rising"),
					playlist("pl.demo-chill-vibes", "Chill Vibes"),
					playlist("pl.demo-alt-ctrl", "ALT CTRL"),
					playlist("pl.demo-pure-focus", "Pure Focus"),
				}},
			},
		}
	}
	return demoHome{page: page, now: time.Now}
}

func isDemoArtist(name string) bool {
	for _, da := range demoArtists {
		if da.name == name {
			return true
		}
	}
	return false
}

// LookupSongs finds songs by id, in the order asked.
func (d *demoCatalog) LookupSongs(ctx context.Context, ids []int64) ([]catalog.Song, error) {
	if err := d.wait(ctx); err != nil {
		return nil, err
	}
	byID := make(map[int64]catalog.Song, len(d.songs))
	for _, s := range d.songs {
		byID[s.ID] = s
	}
	var out []catalog.Song
	for _, id := range ids {
		if s, ok := byID[id]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

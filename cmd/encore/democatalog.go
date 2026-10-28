package main

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"image/jpeg"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
)

// demoCatalog is the Apple Music catalog for --demo, offline: every album
// of the demo library (so "In Your Library" has something to find), one
// more album per library artist that the library doesn't have, and a few
// artists who aren't in the library at all. Answers come after a short
// delay, as from the network.
type demoCatalog struct {
	delay    time.Duration
	artists  []catalog.Artist
	albums   []catalog.Album
	songs    []catalog.Song
	byID     map[int64]int   // album id → index in albums
	tracks   map[int64][]int // album id → song indexes in track order
	byArtist map[int64][]int // artist id → album indexes
	artistOf map[int64]int   // artist id → index in artists
	only     map[int64]bool  // the catalog-only artists
	ids      map[int64]bool
}

// demoArtURL is a demo album's artwork URL; demoArt draws the cover for
// key, as the fake bridge does for library albums.
func demoArtURL(key string) string { return "demo://cover/" + key + "/100x100bb.jpg" }

var demoArtRe = regexp.MustCompile(`^demo://cover/(.+)/(\d+)x\d+bb\.jpg$`)

// demoArt serves demo artwork URLs as JPEG.
func demoArt(_ context.Context, url string) ([]byte, error) {
	m := demoArtRe.FindStringSubmatch(url)
	if m == nil {
		return nil, fmt.Errorf("demo artwork: bad URL %q", url)
	}
	size, _ := strconv.Atoi(m[2])
	var b bytes.Buffer
	if err := jpeg.Encode(&b, bridge.FakeCover(m[1], size), &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// demoExtras are the albums each library artist has on Apple Music but not
// in the demo library: a title and what their songs are called on it.
var demoExtras = []struct{ title, suffix string }{
	{"Live Sessions", " (Live)"},
	{"Acoustic", " (Acoustic)"},
	{"Night Versions", " (Night Version)"},
	{"The Early Tapes", " (Demo)"},
	{"B-Sides & Rarities", " (B-Side)"},
}

// demoArtists are the catalog-only artists: name, genre, and albums
// (title, year, songs).
var demoArtists = []struct {
	name, genre string
	albums      []demoAlbum
}{
	{"Juniper Vale", "Indie Pop", []demoAlbum{
		{"Paper Lanterns", 2025, []string{"Paper Lanterns", "Slow Parade", "Harbour Lights", "Every Window", "Tangerine Sky", "Small Hours", "The Long Way Round", "Kite Weather", "Glasshouse", "Goodnight, Avenue"}},
		{"Coastline Hymns", 2022, []string{"Low Tide", "Salt & Cedar", "Coastline Hymn", "Lighthouse Keeper", "Driftwood", "Saltwater Letters", "Northern Pier", "Fog Bell", "Shell Collector"}},
		{"Weather Vane - Single", 2026, []string{"Weather Vane"}},
		{"First Snow - EP", 2019, []string{"First Snow", "Mitten Weather", "Breath on Glass", "Snow Angel (Japan Bonus Track)"}},
	}},
	{"Northbound Static", "Electronic", []demoAlbum{
		{"Signal Fires", 2024, []string{"Signal Fire", "Low Orbit", "Carrier Wave", "Quiet Satellite", "Magnetic North", "Ground Control", "Aurora Loop", "Static Bloom"}},
		{"Telemetry", 2021, []string{"Telemetry", "Uplink", "Night Freight", "Cold Start", "Parallax", "Afterglow Protocol", "Long Range"}},
	}},
	{"The Marigold Hours", "Alternative", []demoAlbum{
		{"Long Light", 2023, []string{"Long Light", "Golden Hour Again", "Paper Crowns", "Marigold", "Wasted Summer", "Keep the Porch Light On", "Gravel Roads", "Late Bloomer", "Sundown Choir", "All the Way Home", "Ember"}},
		{"Gentle Riot", 2020, []string{"Gentle Riot", "Velvet Alarm", "Shout Quietly", "Riot Lullaby", "Soft Revolt", "Bruised Peach"}},
	}},
}

// demoCompilation is the demo catalog's Various Artists album.
const demoCompilation = "Harbour Lights (Original Soundtrack)"

// demoStations are the radio stations demo search finds.
var demoStations = []catalog.Station{
	{ID: "ra.1461123917", Name: "Lantern FM 1", Artwork: demoArtURL("station-lantern-1")},
	{ID: "ra.1461608125", Name: "Lantern FM 2", Artwork: demoArtURL("station-lantern-2")},
	{ID: "ra.1522038105", Name: "Lantern Slow Focus", Artwork: demoArtURL("station-lantern-slow")},
	{ID: "ra.978194965", Name: "Harbour Radio", Subtitle: "Live", Artwork: demoArtURL("station-harbour")},
}

type demoAlbum struct {
	title string
	year  int
	songs []string
}

// newDemoCatalog builds the demo catalog for lib and registers its songs
// with the fake bridge, so playing them shows their names.
func newDemoCatalog(lib *library.Library, fake *bridge.Fake) *demoCatalog {
	d := &demoCatalog{
		delay:    150 * time.Millisecond,
		byID:     map[int64]int{},
		tracks:   map[int64][]int{},
		byArtist: map[int64][]int{},
		artistOf: map[int64]int{},
		only:     map[int64]bool{},
		ids:      map[int64]bool{},
	}
	artistID := map[string]int64{}
	artist := func(name, genre string) int64 {
		if id, ok := artistID[name]; ok {
			return id
		}
		id := d.id("artist:" + name)
		artistID[name] = id
		d.artistOf[id] = len(d.artists)
		d.artists = append(d.artists, catalog.Artist{ID: id, Name: name, Genre: genre})
		return id
	}
	addAlbum := func(a catalog.Album) int64 {
		a.ID = d.id("album:" + a.Artist + "|" + a.Name)
		d.byID[a.ID] = len(d.albums)
		d.byArtist[a.ArtistID] = append(d.byArtist[a.ArtistID], len(d.albums))
		d.albums = append(d.albums, a)
		return a.ID
	}
	addSong := func(s catalog.Song) int64 {
		s.ID = d.id("song:" + strconv.FormatInt(s.AlbumID, 10) + "|" + strconv.Itoa(s.DiscNumber) + "|" + strconv.Itoa(s.TrackNumber) + "|" + s.Name)
		s.Streamable = !strings.Contains(s.Name, "Bonus Track")
		d.tracks[s.AlbumID] = append(d.tracks[s.AlbumID], len(d.songs))
		d.songs = append(d.songs, s)
		return s.ID
	}
	libSong := map[string]int64{} // library pid → the catalog song it is

	for _, a := range lib.Albums {
		if len(a.Tracks) == 0 || a.Artist == "" {
			continue
		}
		genre := a.Genre
		aid := artist(a.Artist, genre)
		key := cmp.Or(a.PID, a.Key)
		rel := time.Date(max(a.Year, 1970), 3, 1, 0, 0, 0, 0, time.UTC)
		id := addAlbum(catalog.Album{
			ArtistID: aid, Name: a.Title, Artist: a.Artist, Genre: genre,
			TrackCount: len(a.Tracks), Released: rel, Artwork: demoArtURL(key),
		})
		for i, t := range a.Tracks {
			libSong[t.PID] = addSong(catalog.Song{
				AlbumID: id, ArtistID: aid, Name: t.Title, Artist: t.Artist, Album: a.Title,
				Genre: genre, Duration: t.Duration, TrackNumber: cmp.Or(t.TrackNo, i+1), DiscNumber: max(t.Disc, 1),
				Released: rel, Explicit: t.Explicit, Artwork: demoArtURL(key),
			})
		}
	}
	// One album per library artist that the library doesn't have.
	for _, ar := range lib.Artists {
		aid, ok := artistID[ar.Name]
		if !ok || len(ar.Albums) == 0 {
			continue
		}
		var titles []*library.Track
		for _, al := range ar.Albums {
			titles = append(titles, al.Tracks...)
		}
		if len(titles) < 3 {
			continue
		}
		ex := demoExtras[fnv32(ar.Name)%uint32(len(demoExtras))]
		newest := ar.Albums[0]
		rel := time.Date(min(max(newest.Year, 1970)+1, 2026), 9, 1, 0, 0, 0, 0, time.UTC)
		key := "extra-" + ar.Name + "-" + ex.title
		n := min(len(titles), 4+int(fnv32(key)%4))
		id := addAlbum(catalog.Album{
			ArtistID: aid, Name: ex.title, Artist: ar.Name, Genre: newest.Genre,
			TrackCount: n, Released: rel, Artwork: demoArtURL(key),
		})
		for i, t := range titles[:n] {
			addSong(catalog.Song{
				AlbumID: id, ArtistID: aid, Name: t.Title + ex.suffix, Artist: ar.Name, Album: ex.title,
				Genre: newest.Genre, Duration: t.Duration + time.Duration(fnv32(t.PID)%40)*time.Second,
				TrackNumber: i + 1, DiscNumber: 1, Released: rel, Artwork: demoArtURL(key),
			})
		}
	}
	for _, da := range demoArtists {
		aid := artist(da.name, da.genre)
		d.only[aid] = true
		for _, al := range da.albums {
			key := "extra-" + da.name + "-" + al.title
			rel := time.Date(al.year, time.Month(1+fnv32(key)%12), 1, 0, 0, 0, 0, time.UTC)
			id := addAlbum(catalog.Album{
				ArtistID: aid, Name: al.title, Artist: da.name, Genre: da.genre,
				TrackCount: len(al.songs), Released: rel, Artwork: demoArtURL(key),
				Explicit: da.genre == "Alternative",
			})
			for i, name := range al.songs {
				h := fnv32(key + name)
				addSong(catalog.Song{
					AlbumID: id, ArtistID: aid, Name: name, Artist: da.name, Album: al.title,
					Genre: da.genre, Duration: time.Duration(150+h%150) * time.Second,
					TrackNumber: i + 1, DiscNumber: 1, Released: rel,
					Explicit: da.genre == "Alternative" && h%3 == 0, Artwork: demoArtURL(key),
				})
			}
		}
	}

	// A compilation with a song by each catalog-only artist, which their
	// album lookups leave out, as Apple's do.
	comp := catalog.Album{Name: demoCompilation, Artist: catalog.VariousArtists, Genre: "Soundtrack",
		Released: time.Date(2024, 11, 8, 0, 0, 0, 0, time.UTC), Artwork: demoArtURL("extra-" + demoCompilation)}
	comp.TrackCount = len(demoArtists)
	compID := addAlbum(comp)
	for i, da := range demoArtists {
		name := da.albums[0].songs[1] + " (From the Film)"
		addSong(catalog.Song{
			AlbumID: compID, ArtistID: artistID[da.name], Name: name, Artist: da.name, Album: demoCompilation,
			AlbumArtist: catalog.VariousArtists, Genre: "Soundtrack", Duration: time.Duration(170+fnv32(name)%90) * time.Second,
			TrackNumber: i + 1, DiscNumber: 1, Released: comp.Released, Artwork: comp.Artwork,
		})
	}

	if fake != nil {
		// Most library songs know their catalog id, as songs added from
		// Apple Music do; one in five doesn't (a local file, an upload), so
		// the demo shows the match by title and artist too.
		fake.SetStoreIDs(func(t bridge.SnapshotTrack) int64 {
			if fnv32(t.PID)%5 == 0 {
				return 0
			}
			return libSong[t.PID]
		})
		reg := make([]bridge.FakeCatalogSong, len(d.songs))
		for i, s := range d.songs {
			reg[i] = bridge.FakeCatalogSong{
				StoreID: strconv.FormatInt(s.ID, 10), Name: s.Name, Artist: s.Artist,
				Album: s.Album, Duration: s.Duration.Seconds(),
			}
		}
		fake.RegisterCatalog(reg...)
	}
	return d
}

// id is a stable catalog id for key, unique within the catalog.
func (d *demoCatalog) id(key string) int64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	id := int64(h.Sum64()%1_900_000_000) + 100_000_000
	for d.ids[id] {
		id++
	}
	d.ids[id] = true
	return id
}

func fnv32(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func (d *demoCatalog) wait(ctx context.Context) error {
	select {
	case <-time.After(d.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// matchRank ranks a name against folded query terms: -1 when some term
// is missing from fields, 0 when the first field starts with the query,
// 1 when a word in it does, 2 otherwise.
func matchRank(q string, terms []string, fields ...string) int {
	all := ""
	for _, f := range fields {
		all += library.Fold(f) + " "
	}
	for _, t := range terms {
		if !strings.Contains(all, t) {
			return -1
		}
	}
	name := library.Fold(fields[0])
	switch {
	case strings.HasPrefix(name, q):
		return 0
	case strings.Contains(" "+name, " "+terms[0]):
		return 1
	}
	return 2
}

func ranked[T any](xs []T, q string, terms []string, limit int, fields func(T) []string) []T {
	type hit struct {
		x    T
		rank int
	}
	var hits []hit
	for _, x := range xs {
		if r := matchRank(q, terms, fields(x)...); r >= 0 {
			hits = append(hits, hit{x, r})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return cmp.Compare(a.rank, b.rank) })
	out := make([]T, 0, min(limit, len(hits)))
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, h.x)
	}
	return out
}

func (d *demoCatalog) Search(ctx context.Context, q string, limit int) (catalog.Results, error) {
	return d.SearchKinds(ctx, q, catalog.AllKinds, limit)
}

func (d *demoCatalog) SearchKinds(ctx context.Context, q string, kinds catalog.Kind, limit int) (catalog.Results, error) {
	if err := d.wait(ctx); err != nil {
		return catalog.Results{}, err
	}
	f := library.Fold(q)
	if f == "" {
		return catalog.Results{}, nil
	}
	if limit <= 0 {
		limit = 25
	}
	terms := strings.Fields(f)
	var res catalog.Results
	if kinds&catalog.Artists != 0 {
		res.Artists = ranked(d.artists, f, terms, limit, func(a catalog.Artist) []string { return []string{a.Name} })
	}
	if kinds&catalog.Albums != 0 {
		res.Albums = ranked(d.albums, f, terms, limit, func(a catalog.Album) []string { return []string{a.Name, a.Artist} })
	}
	if kinds&catalog.Songs != 0 {
		res.Songs = ranked(d.songs, f, terms, limit, func(s catalog.Song) []string { return []string{s.Name, s.Artist, s.Album} })
	}
	return res, nil
}

func (d *demoCatalog) ArtistAlbums(ctx context.Context, artistID int64) (catalog.Artist, []catalog.Album, error) {
	if err := d.wait(ctx); err != nil {
		return catalog.Artist{}, nil, err
	}
	i, ok := d.artistOf[artistID]
	if !ok {
		return catalog.Artist{}, nil, catalog.ErrNotFound
	}
	var albums []catalog.Album
	for _, k := range d.byArtist[artistID] {
		albums = append(albums, d.albums[k])
	}
	slices.SortStableFunc(albums, func(a, b catalog.Album) int { return b.Released.Compare(a.Released) })
	return d.artists[i], albums, nil
}

func (d *demoCatalog) ArtistTopSongs(ctx context.Context, artistID int64, limit int) ([]catalog.Song, error) {
	if err := d.wait(ctx); err != nil {
		return nil, err
	}
	if _, ok := d.artistOf[artistID]; !ok {
		return nil, catalog.ErrNotFound
	}
	var songs []catalog.Song
	for _, s := range d.songs {
		if s.ArtistID == artistID && s.Streamable {
			songs = append(songs, s)
		}
	}
	// "popularity": a stable shuffle
	slices.SortStableFunc(songs, func(a, b catalog.Song) int {
		return cmp.Compare(fnv32(a.Name+"pop"), fnv32(b.Name+"pop"))
	})
	if limit <= 0 {
		limit = 10
	}
	return songs[:min(limit, len(songs))], nil
}

func (d *demoCatalog) AlbumTracks(ctx context.Context, id int64) (catalog.Album, []catalog.Song, error) {
	if err := d.wait(ctx); err != nil {
		return catalog.Album{}, nil, err
	}
	i, ok := d.byID[id]
	if !ok {
		return catalog.Album{}, nil, catalog.ErrNotFound
	}
	var songs []catalog.Song
	for _, j := range d.tracks[id] {
		songs = append(songs, d.songs[j])
	}
	return d.albums[i], songs, nil
}

// lazyCatalog is the real catalog, made on first use: finding the
// storefront runs `defaults`, which can take up to a second.
type lazyCatalog struct{ get func() *catalog.Client }

func newLazyCatalog() lazyCatalog {
	return lazyCatalog{get: sync.OnceValue(func() *catalog.Client { return catalog.New("") })}
}

func (l lazyCatalog) Search(ctx context.Context, q string, limit int) (catalog.Results, error) {
	return l.get().Search(ctx, q, limit)
}

func (l lazyCatalog) SearchKinds(ctx context.Context, q string, kinds catalog.Kind, limit int) (catalog.Results, error) {
	return l.get().SearchKinds(ctx, q, kinds, limit)
}

func (l lazyCatalog) ArtistAlbums(ctx context.Context, id int64) (catalog.Artist, []catalog.Album, error) {
	return l.get().ArtistAlbums(ctx, id)
}

func (l lazyCatalog) ArtistTopSongs(ctx context.Context, id int64, limit int) ([]catalog.Song, error) {
	return l.get().ArtistTopSongs(ctx, id, limit)
}

func (l lazyCatalog) AlbumTracks(ctx context.Context, id int64) (catalog.Album, []catalog.Song, error) {
	return l.get().AlbumTracks(ctx, id)
}

// ArtistPage gives the catalog-only artists what Apple's pages have: their
// first album as an essential, an Essentials playlist and the Live
// Sessions albums as live ones.
func (d *demoCatalog) ArtistPage(ctx context.Context, artistID int64) (catalog.ArtistPage, error) {
	if err := d.wait(ctx); err != nil {
		return catalog.ArtistPage{}, err
	}
	i, ok := d.artistOf[artistID]
	if !ok {
		return catalog.ArtistPage{}, catalog.ErrNotFound
	}
	p := catalog.ArtistPage{Sections: map[string][]catalog.Album{}}
	for _, k := range d.byArtist[artistID] {
		if a := d.albums[k]; a.Name == "Live Sessions" {
			p.Sections[catalog.SectionLive] = append(p.Sections[catalog.SectionLive], a)
		}
	}
	if d.only[artistID] {
		if ks := d.byArtist[artistID]; len(ks) > 0 {
			p.Sections[catalog.SectionEssentials] = []catalog.Album{d.albums[ks[0]]}
		}
		p.Essentials = &catalog.Playlist{ID: "pl.demo-" + strconv.FormatInt(artistID, 10), Name: d.artists[i].Name + " Essentials", Curator: "Apple Music"}
	}
	return p, nil
}

// PlaylistSongs plays an Essentials playlist as the artist's songs in
// another order.
func (d *demoCatalog) PlaylistSongs(ctx context.Context, pl string) ([]catalog.Song, error) {
	idText, ok := strings.CutPrefix(pl, "pl.demo-")
	artistID, err := strconv.ParseInt(idText, 10, 64)
	if !ok || err != nil {
		return nil, catalog.ErrNotFound
	}
	songs, err := d.ArtistTopSongs(ctx, artistID, 200)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(songs, func(a, b catalog.Song) int {
		return cmp.Compare(fnv32(a.Name+"essential"), fnv32(b.Name+"essential"))
	})
	return songs[:min(15, len(songs))], nil
}

func (d *demoCatalog) SearchStations(ctx context.Context, q string) ([]catalog.Station, error) {
	if err := d.wait(ctx); err != nil {
		return nil, err
	}
	f := library.Fold(q)
	if f == "" {
		return nil, nil
	}
	return ranked(demoStations, f, strings.Fields(f), 10, func(s catalog.Station) []string { return []string{s.Name} }), nil
}

func (l lazyCatalog) ArtistPage(ctx context.Context, id int64) (catalog.ArtistPage, error) {
	return l.get().ArtistPage(ctx, id)
}

func (l lazyCatalog) PlaylistSongs(ctx context.Context, pl string) ([]catalog.Song, error) {
	return l.get().PlaylistSongs(ctx, pl)
}

func (l lazyCatalog) SearchStations(ctx context.Context, q string) ([]catalog.Station, error) {
	return l.get().SearchStations(ctx, q)
}

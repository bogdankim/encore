package catalog

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Apple Music's web pages (music.apple.com) carry what the iTunes Search
// API doesn't: an artist's page sections (Essential Albums, Live Albums,
// Compilations, Appears On, their Essentials playlist), a playlist's songs
// and radio stations in search. Each page embeds its data as JSON in
// <script id="serialized-server-data">; no sign-in or token is needed.
// Shelves there hold at most a dozen items, so the full album list still
// comes from the Search API. See docs/research/README.md, "Web pages".

// DefaultWebBase is the Apple Music web player.
const DefaultWebBase = "https://music.apple.com"

// webCacheVersion starts the cache keys of parsed pages; a new one drops
// entries parsed by older code (v2 added artist photos and album notes).
const webCacheVersion = "web2:"

const (
	webPerMinute = 20
	webBurst     = 4
	maxWebBody   = 8 << 20
)

// Section kinds of an artist page, in the order Apple shows them.
const (
	SectionEssentials   = "essentials"
	SectionAlbums       = "albums"
	SectionSingles      = "singles"
	SectionLive         = "live"
	SectionCompilations = "compilations"
	SectionAppearsOn    = "appears-on"
)

// ArtistPage is what an artist's Apple Music page lists beyond the Search
// API: up to a dozen albums per section, Apple's top songs and the
// artist's Essentials playlist.
type ArtistPage struct {
	// Sections maps a Section* kind to its albums in Apple's order.
	Sections map[string][]Album `json:"sections,omitempty"`
	TopSongs []Song             `json:"topSongs,omitempty"`
	// Artwork is the artist's photo, 100 px; see ResizeArt.
	Artwork string `json:"artwork,omitempty"`
	// Essentials is the "<Artist> Essentials" playlist, when the page has
	// one.
	Essentials *Playlist `json:"essentials,omitempty"`
}

// Playlist is a catalog playlist (pl.…).
type Playlist struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Curator string `json:"curator,omitempty"`
}

// Station is a radio station found on Apple Music: one of Apple's or a
// broadcast station such as NTS Radio 1.
type Station struct {
	ID       string `json:"id"` // ra.…
	Name     string `json:"name"`
	Subtitle string `json:"subtitle,omitempty"`
	Artwork  string `json:"artwork,omitempty"` // 100 px; see ResizeArt
}

// ArtistPage reads the artist's Apple Music page.
func (c *Client) ArtistPage(ctx context.Context, artistID int64) (ArtistPage, error) {
	var p ArtistPage
	err := c.webJSON(ctx, "/"+c.country()+"/artist/"+id(artistID), true, &p, func(ssr []byte) (any, error) {
		return parseArtistPage(ssr)
	})
	return p, err
}

// PlaylistSongs returns the songs of catalog playlist pl (pl.…) in order,
// as many as its page lists (Essentials playlists list all of theirs).
func (c *Client) PlaylistSongs(ctx context.Context, pl string) ([]Song, error) {
	if !validPlaylistID(pl) {
		return nil, ErrNotFound
	}
	var songs []Song
	err := c.webJSON(ctx, "/"+c.country()+"/playlist/"+pl, true, &songs, func(ssr []byte) (any, error) {
		return parsePlaylistSongs(ssr)
	})
	return songs, err
}

// SearchStations finds radio stations matching q, in Apple's order.
func (c *Client) SearchStations(ctx context.Context, q string) ([]Station, error) {
	term := strings.Join(strings.Fields(strings.ToLower(q)), " ")
	if term == "" {
		return nil, nil
	}
	var st []Station
	path := "/" + c.country() + "/search?" + url.Values{"term": {term}}.Encode()
	err := c.webJSON(ctx, path, false, &st, func(ssr []byte) (any, error) {
		return parseStations(ssr)
	})
	return st, err
}

func (c *Client) country() string {
	c.init()
	return c.Country
}

func validPlaylistID(s string) bool {
	rest, ok := strings.CutPrefix(s, "pl.")
	if !ok || rest == "" || len(s) > 128 {
		return false
	}
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// webJSON fills out with the parsed page at path: from memory, then disk
// (lookup pages only), then the network. parse turns the page's
// serialized data into the value that is cached, as JSON.
func (c *Client) webJSON(ctx context.Context, path string, lookup bool, out any, parse func([]byte) (any, error)) error {
	c.init()
	key := webCacheVersion + path
	now := c.clk.Now()
	if body, ok := c.mem.get(key, now); ok {
		return json.Unmarshal(body, out)
	}
	var stale []byte
	if lookup && c.disk != nil {
		if body, fetched, ok := c.disk.get(key); ok {
			if until := fetched.Add(c.DiskTTL); now.Before(until) {
				c.mem.put(key, body, minTime(until, now.Add(c.LookupTTL)))
				return json.Unmarshal(body, out)
			}
			stale = body
		}
	}
	body, err := c.fetchWeb(ctx, path, parse)
	if err != nil {
		if stale != nil && ctx.Err() == nil && !errors.Is(err, ErrNotFound) {
			return json.Unmarshal(stale, out)
		}
		return err
	}
	now = c.clk.Now()
	ttl := c.SearchTTL
	if lookup {
		ttl = c.LookupTTL
		if c.disk != nil {
			c.disk.prune(diskPruneAge, now)
			c.disk.put(key, body, now)
		}
	}
	c.mem.put(key, body, now.Add(ttl))
	return json.Unmarshal(body, out)
}

func (c *Client) fetchWeb(ctx context.Context, path string, parse func([]byte) (any, error)) ([]byte, error) {
	if err := c.webLim.wait(ctx); err != nil {
		return nil, err
	}
	sent := c.clk.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.WebBase+path, nil)
	if err != nil {
		return nil, err
	}
	// The pages are the same for any browser; Apple serves a bare page
	// without the data to clients it doesn't know.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15")
	req.Header.Set("Accept", "text/html")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &RateLimitError{Status: resp.StatusCode, RetryAfter: c.webLim.refused(sent)}
	default:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &HTTPError{Status: resp.StatusCode}
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxWebBody))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	c.webLim.ok(sent)
	ssr, err := serializedData(page)
	if err != nil {
		return nil, err
	}
	v, err := parse(ssr)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

var ssrRe = regexp.MustCompile(`(?s)<script[^>]*\bid="serialized-server-data"[^>]*>(.*?)</script>`)

// serializedData is the JSON a music.apple.com page embeds.
func serializedData(page []byte) ([]byte, error) {
	m := ssrRe.FindSubmatch(page)
	if m == nil {
		return nil, errors.New("catalog: web page without data")
	}
	return bytes.TrimSpace(m[1]), nil
}

// The serialized data, as far as encore reads it.
type ssrDoc struct {
	Data []struct {
		Data struct {
			Sections []ssrSection `json:"sections"`
		} `json:"data"`
	} `json:"data"`
}

type ssrSection struct {
	ID       string            `json:"id"`
	ItemKind string            `json:"itemKind"`
	Items    []json.RawMessage `json:"items"`
}

type ssrLink struct {
	Title string `json:"title"`
	Segue *struct {
		Destination struct {
			ContentDescriptor ssrDescriptor `json:"contentDescriptor"`
		} `json:"destination"`
	} `json:"segue"`
}

// target is the item a link leads to, or a zero descriptor.
func (l ssrLink) target() ssrDescriptor {
	if l.Segue == nil {
		return ssrDescriptor{}
	}
	return l.Segue.Destination.ContentDescriptor
}

type ssrDescriptor struct {
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Identifiers struct {
		StoreAdamID string `json:"storeAdamID"`
	} `json:"identifiers"`
}

type ssrArtwork struct {
	Dictionary struct {
		URL string `json:"url"`
	} `json:"dictionary"`
}

// url is the artwork at 100 px in the Search API's form, so ResizeArt
// works on it.
func (a ssrArtwork) url() string {
	u := a.Dictionary.URL
	if u == "" {
		return ""
	}
	if i := strings.LastIndex(u, "/"); i >= 0 && strings.Contains(u[i:], "{w}") {
		u = u[:i] + "/100x100bb.jpg"
	}
	return u
}

// ssrLockup is an album, playlist or station tile.
type ssrLockup struct {
	ID                string        `json:"id"`
	TitleLinks        []ssrLink     `json:"titleLinks"`
	SubtitleLinks     []ssrLink     `json:"subtitleLinks"`
	Artwork           ssrArtwork    `json:"artwork"`
	ContentDescriptor ssrDescriptor `json:"contentDescriptor"`
	ShowExplicitBadge bool          `json:"showExplicitBadge"`
	// An Essential Albums item wraps its tile.
	SquareLockup *ssrLockup `json:"squareLockup"`
}

func (l *ssrLockup) title() string {
	if len(l.TitleLinks) > 0 {
		return l.TitleLinks[0].Title
	}
	return ""
}

func (l *ssrLockup) subtitle() string {
	if len(l.SubtitleLinks) > 0 {
		return l.SubtitleLinks[0].Title
	}
	return ""
}

// ssrTrack is a song row.
type ssrTrack struct {
	ID                string        `json:"id"`
	Title             string        `json:"title"`
	Duration          int64         `json:"duration"` // ms
	ShowExplicitBadge bool          `json:"showExplicitBadge"`
	IsDisabled        bool          `json:"isDisabled"`
	ArtistName        string        `json:"artistName"`
	SubtitleLinks     []ssrLink     `json:"subtitleLinks"`
	TertiaryLinks     []ssrLink     `json:"tertiaryLinks"`
	Artwork           ssrArtwork    `json:"artwork"`
	ContentDescriptor ssrDescriptor `json:"contentDescriptor"`
}

var (
	albumURLRe = regexp.MustCompile(`/album/[^/]*/(\d+)`)
	yearTailRe = regexp.MustCompile(`\s*[·•]\s*(\d{4})$`)
)

// song is the row as a catalog song. artist names the artist when the row
// doesn't (an artist page's top songs). The Search API fields the page
// lacks stay zero; songs listed there stream in the storefront.
func (t *ssrTrack) song(artist string, artistID int64) (Song, bool) {
	sid, err := strconv.ParseInt(t.ContentDescriptor.Identifiers.StoreAdamID, 10, 64)
	if err != nil || sid <= 0 || t.ContentDescriptor.Kind != "song" {
		return Song{}, false
	}
	s := Song{
		ID:         sid,
		ArtistID:   artistID,
		Name:       t.Title,
		Artist:     artist,
		Duration:   time.Duration(t.Duration) * time.Millisecond,
		Explicit:   t.ShowExplicitBadge,
		Streamable: !t.IsDisabled,
		Artwork:    t.Artwork.url(),
		URL:        t.ContentDescriptor.URL,
	}
	if m := albumURLRe.FindStringSubmatch(t.ContentDescriptor.URL); m != nil {
		s.AlbumID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if len(t.SubtitleLinks) > 0 {
		if a := t.SubtitleLinks[0].target(); a.Kind == "artist" {
			s.Artist = t.SubtitleLinks[0].Title
			s.ArtistID, _ = strconv.ParseInt(a.Identifiers.StoreAdamID, 10, 64)
		} else if s.Album == "" {
			// An artist page: "Album · 2017".
			sub := strings.ReplaceAll(t.SubtitleLinks[0].Title, " ", " ")
			if m := yearTailRe.FindStringSubmatchIndex(sub); m != nil {
				y, _ := strconv.Atoi(sub[m[2]:m[3]])
				s.Released = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
				sub = sub[:m[0]]
			}
			s.Album = strings.TrimSpace(sub)
		}
	}
	if len(t.TertiaryLinks) > 0 {
		s.Album = t.TertiaryLinks[0].Title
	}
	if s.Artist == "" {
		s.Artist = t.ArtistName
	}
	return s, true
}

func sections(ssr []byte) ([]ssrSection, error) {
	var d ssrDoc
	if err := json.Unmarshal(ssr, &d); err != nil {
		return nil, fmt.Errorf("catalog: bad web page data: %w", err)
	}
	var out []ssrSection
	for _, p := range d.Data {
		out = append(out, p.Data.Sections...)
	}
	return out, nil
}

// artistShelves maps the marker in an artist page section's id
// ("artists_657515_default.marker.LiveAlbums-SquareLockupSection") to a
// Section* kind. Titles are localized; markers aren't.
var artistShelves = map[string]string{
	"Essentials":        SectionEssentials,
	"Albums":            SectionAlbums,
	"ArtistSingles":     SectionSingles,
	"LiveAlbums":        SectionLive,
	"CompilationAlbums": SectionCompilations,
	"AppearsOn":         SectionAppearsOn,
}

// marker is the shelf name in a section id, or "".
func marker(id string) string {
	_, rest, ok := strings.Cut(id, ".marker.")
	if !ok {
		return ""
	}
	rest = rest[strings.LastIndex(rest, ".")+1:]
	name, _, _ := strings.Cut(rest, "-")
	return name
}

func parseArtistPage(ssr []byte) (ArtistPage, error) {
	secs, err := sections(ssr)
	if err != nil {
		return ArtistPage{}, err
	}
	var (
		p        ArtistPage
		name     string
		artistID int64
	)
	for _, s := range secs {
		if s.ItemKind != "artistDetailHeader" || len(s.Items) == 0 {
			continue
		}
		var h struct {
			ID            string     `json:"id"`
			Title         string     `json:"title"`
			CircleArtwork ssrArtwork `json:"circleArtwork"`
			Artwork       ssrArtwork `json:"artwork"`
		}
		if json.Unmarshal(s.Items[0], &h) == nil {
			name = h.Title
			artistID, _ = strconv.ParseInt(h.ID, 10, 64)
			p.Artwork = cmp.Or(h.CircleArtwork.url(), h.Artwork.url())
		}
	}
	for _, s := range secs {
		m := marker(s.ID)
		switch {
		case m == "TopSongs":
			p.TopSongs = append(p.TopSongs, topSongs(s, name, artistID)...)
		case m == "ArtistPlaylists" && p.Essentials == nil:
			p.Essentials = essentialsPlaylist(s, name)
		case artistShelves[m] != "":
			kind := artistShelves[m]
			for _, raw := range s.Items {
				var l ssrLockup
				if json.Unmarshal(raw, &l) != nil {
					continue
				}
				if l.SquareLockup != nil {
					l = *l.SquareLockup
				}
				if a, ok := lockupAlbum(&l, kind, name, artistID); ok {
					if p.Sections == nil {
						p.Sections = map[string][]Album{}
					}
					p.Sections[kind] = append(p.Sections[kind], a)
				}
			}
		}
	}
	return p, nil
}

// topSongs reads a Top Songs section: rows of its own, or rows under a
// featured playlist (artistFeaturedContentAndTracks).
func topSongs(s ssrSection, artist string, artistID int64) []Song {
	var rows []ssrTrack
	for _, raw := range s.Items {
		var wrap struct {
			Tracks []ssrTrack `json:"tracks"`
		}
		if json.Unmarshal(raw, &wrap) == nil && len(wrap.Tracks) > 0 {
			rows = append(rows, wrap.Tracks...)
			continue
		}
		var t ssrTrack
		if json.Unmarshal(raw, &t) == nil {
			rows = append(rows, t)
		}
	}
	var songs []Song
	for i := range rows {
		if s, ok := rows[i].song(artist, artistID); ok {
			songs = append(songs, s)
		}
	}
	return songs
}

// essentialsPlaylist is the playlist of an Artist Playlists shelf named
// like "Radiohead Essentials", or nil.
func essentialsPlaylist(s ssrSection, artist string) *Playlist {
	for _, raw := range s.Items {
		var l ssrLockup
		if json.Unmarshal(raw, &l) != nil || l.ContentDescriptor.Kind != "playlist" {
			continue
		}
		t := l.title()
		if strings.HasSuffix(strings.ToLower(t), "essentials") && validPlaylistID(l.ContentDescriptor.Identifiers.StoreAdamID) {
			return &Playlist{ID: l.ContentDescriptor.Identifiers.StoreAdamID, Name: t, Curator: l.subtitle()}
		}
	}
	return nil
}

// lockupAlbum is an album tile as a catalog album. On the artist's own
// shelves the subtitle is the year (or a blurb, for Essential Albums); on
// Appears On it is the album's artist.
func lockupAlbum(l *ssrLockup, kind, artist string, artistID int64) (Album, bool) {
	if l.ContentDescriptor.Kind != "album" {
		return Album{}, false
	}
	aid, err := strconv.ParseInt(l.ContentDescriptor.Identifiers.StoreAdamID, 10, 64)
	if err != nil || aid <= 0 {
		return Album{}, false
	}
	a := Album{
		ID:       aid,
		ArtistID: artistID,
		Name:     l.title(),
		Artist:   artist,
		Explicit: l.ShowExplicitBadge,
		Artwork:  l.Artwork.url(),
		URL:      l.ContentDescriptor.URL,
	}
	sub := l.subtitle()
	if y, err := strconv.Atoi(sub); err == nil && y > 1000 && y < 10000 {
		a.Released = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	} else if kind == SectionEssentials {
		a.Note = sub
	} else if kind == SectionAppearsOn && sub != "" {
		a.Artist, a.ArtistID = sub, 0
		if len(l.SubtitleLinks) > 0 {
			if t := l.SubtitleLinks[0].target(); t.Kind == "artist" {
				a.ArtistID, _ = strconv.ParseInt(t.Identifiers.StoreAdamID, 10, 64)
			}
		}
	}
	return a, true
}

func parsePlaylistSongs(ssr []byte) ([]Song, error) {
	secs, err := sections(ssr)
	if err != nil {
		return nil, err
	}
	var songs []Song
	for _, s := range secs {
		if s.ItemKind != "trackLockup" {
			continue
		}
		for _, raw := range s.Items {
			var t ssrTrack
			if json.Unmarshal(raw, &t) != nil {
				continue
			}
			if song, ok := t.song("", 0); ok {
				songs = append(songs, song)
			}
		}
	}
	if len(songs) == 0 {
		return nil, ErrNotFound
	}
	return songs, nil
}

func parseStations(ssr []byte) ([]Station, error) {
	secs, err := sections(ssr)
	if err != nil {
		return nil, err
	}
	var out []Station
	seen := map[string]bool{}
	for _, s := range secs {
		if !strings.HasSuffix(s.ID, " - station") {
			continue
		}
		for _, raw := range s.Items {
			var l ssrLockup
			if json.Unmarshal(raw, &l) != nil || l.ContentDescriptor.Kind != "radioStation" {
				continue
			}
			id := l.ContentDescriptor.Identifiers.StoreAdamID
			if !strings.HasPrefix(id, "ra.") || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, Station{ID: id, Name: l.title(), Subtitle: l.subtitle(), Artwork: l.Artwork.url()})
		}
	}
	return out, nil
}

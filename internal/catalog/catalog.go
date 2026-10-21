// Package catalog searches and browses the Apple Music catalog through the
// public iTunes Search API (itunes.apple.com/search and /lookup). It needs no
// credentials, so encore can find artists, albums and songs that aren't in the
// user's library.
//
// The API allows roughly 20 calls a minute per IP and answers 403 or 429
// past that, with no Retry-After. The Client rate-limits itself, caches every
// response in memory and lookups on disk, and returns ErrRateLimited when
// Apple refuses anyway.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Artist is a catalog artist. The API has no artist artwork.
type Artist struct {
	ID    int64
	Name  string
	Genre string
	URL   string // music.apple.com page
}

// Album is a catalog album (collection).
type Album struct {
	ID         int64
	ArtistID   int64
	Name       string
	Artist     string
	Genre      string
	TrackCount int
	Released   time.Time
	Explicit   bool
	Artwork    string // 100 px artwork URL as returned; see ArtURL
	URL        string // music.apple.com page
}

// ArtURL returns the album artwork at size×size px (600, 1200 and 3000 all
// work). size <= 0 returns the 100 px URL.
func (a Album) ArtURL(size int) string { return artURL(a.Artwork, size) }

// Song is a catalog song. ID is the Apple Music catalog song id, the one
// playback needs.
type Song struct {
	ID          int64
	AlbumID     int64
	ArtistID    int64
	Name        string
	Artist      string
	Album       string
	Genre       string
	Duration    time.Duration
	TrackNumber int
	DiscNumber  int
	Released    time.Time
	Explicit    bool
	Streamable  bool   // false: in the catalog but not playable in this storefront
	PreviewURL  string // 30 s AAC preview
	Artwork     string // 100 px artwork URL as returned; see ArtURL
	URL         string // music.apple.com page
}

// ArtURL returns the song's album artwork at size×size px. size <= 0 returns
// the 100 px URL.
func (s Song) ArtURL(size int) string { return artURL(s.Artwork, size) }

// Results is one search: up to the requested number of each kind, in Apple's
// relevance order.
type Results struct {
	Artists []Artist
	Albums  []Album
	Songs   []Song
}

// Kind is a set of result kinds for SearchKinds.
type Kind uint8

const (
	Artists Kind = 1 << iota
	Albums
	Songs

	AllKinds = Artists | Albums | Songs
)

// Empty reports whether nothing matched.
func (r Results) Empty() bool { return len(r.Artists)+len(r.Albums)+len(r.Songs) == 0 }

// ErrRateLimited means Apple refused the request (HTTP 403 or 429) or the
// client is still backing off from an earlier refusal. The UI should say
// something like "Apple's search is busy, try again in a moment". Errors
// returned for it are *RateLimitError.
var ErrRateLimited = errors.New("catalog: rate limited")

// RateLimitError says how long until the client will try the network again.
type RateLimitError struct {
	Status     int           // 403 or 429; 0 when no request was sent
	RetryAfter time.Duration // time left in the backoff window
}

func (e *RateLimitError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("catalog: rate limited (HTTP %d), retry in %s", e.Status, e.RetryAfter.Round(time.Second))
	}
	return fmt.Sprintf("catalog: rate limited, retry in %s", e.RetryAfter.Round(time.Second))
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// HTTPError is any other unexpected status, such as 400 for a bad parameter.
type HTTPError struct {
	Status  int
	Message string // the API's errorMessage, when it sent one
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("catalog: HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("catalog: HTTP %d", e.Status)
}

var artSizeRe = regexp.MustCompile(`/\d+x\d+bb\.(jpg|png|webp)$`)

// ResizeArt returns an artwork URL from the API (any size) at size×size px.
// size <= 0, or a URL that doesn't follow Apple's pattern, returns u.
func ResizeArt(u string, size int) string { return artURL(u, size) }

// Year is the album's release year, or 0 when unknown.
func (a Album) Year() int {
	if a.Released.IsZero() {
		return 0
	}
	return a.Released.Year()
}

func artURL(u string, size int) string {
	if size <= 0 || u == "" {
		return u
	}
	n := strconv.Itoa(size)
	return artSizeRe.ReplaceAllString(u, "/"+n+"x"+n+"bb.$1")
}

// response is the envelope of /search and /lookup.
type response struct {
	ResultCount int      `json:"resultCount"`
	Results     []record `json:"results"`
}

// record is one result of any kind; wrapperType says which.
type record struct {
	WrapperType      string `json:"wrapperType"` // artist, collection, track
	Kind             string `json:"kind"`        // song, music-video, … (tracks)
	CollectionType   string `json:"collectionType"`
	ArtistID         int64  `json:"artistId"`
	ArtistName       string `json:"artistName"`
	ArtistLinkURL    string `json:"artistLinkUrl"`
	CollectionID     int64  `json:"collectionId"`
	CollectionName   string `json:"collectionName"`
	CollectionURL    string `json:"collectionViewUrl"`
	CollectionExpl   string `json:"collectionExplicitness"`
	TrackID          int64  `json:"trackId"`
	TrackName        string `json:"trackName"`
	TrackURL         string `json:"trackViewUrl"`
	TrackExpl        string `json:"trackExplicitness"`
	TrackTimeMillis  int64  `json:"trackTimeMillis"`
	TrackNumber      int    `json:"trackNumber"`
	TrackCount       int    `json:"trackCount"`
	DiscNumber       int    `json:"discNumber"`
	IsStreamable     bool   `json:"isStreamable"`
	PreviewURL       string `json:"previewUrl"`
	ArtworkURL100    string `json:"artworkUrl100"`
	ReleaseDate      string `json:"releaseDate"`
	PrimaryGenreName string `json:"primaryGenreName"`
}

func (r *record) isArtist() bool { return r.WrapperType == "artist" }
func (r *record) isAlbum() bool  { return r.WrapperType == "collection" && r.CollectionType == "Album" }
func (r *record) isSong() bool   { return r.WrapperType == "track" && r.Kind == "song" }

func (r *record) artist() Artist {
	return Artist{ID: r.ArtistID, Name: r.ArtistName, Genre: r.PrimaryGenreName, URL: r.ArtistLinkURL}
}

func (r *record) album() Album {
	return Album{
		ID:         r.CollectionID,
		ArtistID:   r.ArtistID,
		Name:       r.CollectionName,
		Artist:     r.ArtistName,
		Genre:      r.PrimaryGenreName,
		TrackCount: r.TrackCount,
		Released:   parseDate(r.ReleaseDate),
		Explicit:   r.CollectionExpl == "explicit",
		Artwork:    r.ArtworkURL100,
		URL:        r.CollectionURL,
	}
}

func (r *record) song() Song {
	return Song{
		ID:          r.TrackID,
		AlbumID:     r.CollectionID,
		ArtistID:    r.ArtistID,
		Name:        r.TrackName,
		Artist:      r.ArtistName,
		Album:       r.CollectionName,
		Genre:       r.PrimaryGenreName,
		Duration:    time.Duration(r.TrackTimeMillis) * time.Millisecond,
		TrackNumber: r.TrackNumber,
		DiscNumber:  r.DiscNumber,
		Released:    parseDate(r.ReleaseDate),
		Explicit:    r.TrackExpl == "explicit",
		Streamable:  r.IsStreamable,
		PreviewURL:  r.PreviewURL,
		Artwork:     r.ArtworkURL100,
		URL:         r.TrackURL,
	}
}

func parseDate(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func decode(body []byte) ([]record, error) {
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("catalog: bad response: %w", err)
	}
	return r.Results, nil
}

// Package lrclib looks up lyrics on lrclib.net with a get -> cleaned get ->
// search fallback chain.
package lrclib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Record struct {
	ID           int64   `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	HasWordSync  bool    `json:"hasWordSync"`
	PlainLyrics  *string `json:"plainLyrics"`
	SyncedLyrics *string `json:"syncedLyrics"`
	Lyricsfile   string  `json:"lyricsfile"`
}

func (r *Record) Synced() bool { return r.SyncedLyrics != nil && *r.SyncedLyrics != "" }

var ErrNotFound = errors.New("lrclib: not found")

type Client struct {
	HTTP      *http.Client
	UserAgent string // required: "encore v0.1.0 (https://github.com/...)"
	Base      string // default https://lrclib.net
}

func (c *Client) do(ctx context.Context, path string, q url.Values, out any) error {
	base := c.Base
	if base == "" {
		base = "https://lrclib.net"
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			err = json.NewDecoder(resp.Body).Decode(out)
			resp.Body.Close()
			return err
		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return ErrNotFound
		case (resp.StatusCode == 429 || resp.StatusCode == 503) && attempt < 2:
			resp.Body.Close()
			wait := retryAfter(resp.Header.Get("Retry-After"))
			wait += time.Duration(rand.Float64() * 0.3 * float64(wait)) // jitter, per LRCLIB docs
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		default:
			resp.Body.Close()
			return fmt.Errorf("lrclib: HTTP %d", resp.StatusCode)
		}
	}
}

func retryAfter(h string) time.Duration {
	if n, err := strconv.Atoi(h); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Second
}

func (c *Client) Get(ctx context.Context, track, artist, album string, dur float64) (*Record, error) {
	q := url.Values{"track_name": {track}, "artist_name": {artist}}
	if album != "" {
		q.Set("album_name", album)
	}
	if dur > 0 {
		q.Set("duration", strconv.Itoa(int(math.Round(dur))))
	}
	var r Record
	if err := c.do(ctx, "/api/get", q, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) Search(ctx context.Context, track, artist string) ([]Record, error) {
	q := url.Values{"track_name": {track}}
	if artist != "" {
		q.Set("artist_name", artist)
	}
	var rs []Record
	err := c.do(ctx, "/api/search", q, &rs)
	return rs, err
}

// Find runs the fallback chain. It returns the best record (synced preferred,
// then instrumental, then plain) or ErrNotFound. Up to 4 requests.
func (c *Client) Find(ctx context.Context, track, artist, album string, dur float64) (*Record, error) {
	var best *Record
	consider := func(r *Record) bool { // true = good enough, stop
		if r == nil {
			return false
		}
		if r.Synced() || r.Instrumental {
			best = r
			return true
		}
		if best == nil {
			best = r
		}
		return false
	}
	ct, ca := CleanTitle(track), PrimaryArtist(artist)

	// 1. exact signature with album
	r, err := c.Get(ctx, track, artist, album, dur)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if consider(r) {
		return best, nil
	}
	// 2. cleaned title + primary artist, no album (drops " - Single", "(Deluxe)")
	if ct != track || ca != artist || album != "" {
		r, err = c.Get(ctx, ct, ca, "", dur)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return best, err
		}
		if consider(r) {
			return best, nil
		}
	}
	// 3. search, keep candidates within ±3s, prefer synced and title match
	rs, err := c.Search(ctx, ct, ca)
	if err == nil {
		var pick *Record
		score := -1
		for i := range rs {
			x := &rs[i]
			if dur > 0 && math.Abs(x.Duration-dur) > 3 {
				continue
			}
			s := 0
			if x.Synced() {
				s += 4
			}
			if Key(CleanTitle(x.TrackName)) == Key(ct) {
				s += 2
			}
			if album != "" && Key(x.AlbumName) == Key(album) {
				s++
			}
			if s > score {
				pick, score = x, s
			}
		}
		if pick != nil && (best == nil || pick.Synced()) {
			best = pick
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return best, nil
}

var (
	featRe   = regexp.MustCompile(`(?i)\s*[\(\[]\s*(feat\.?|ft\.?|featuring|with)\s[^\)\]]*[\)\]]`)
	dashRe   = regexp.MustCompile(`(?i)\s+-\s+((\d{4}\s+)?(remaster(ed)?|re-?mastered|mono|stereo|single|radio edit|edit|live|bonus track)\b.*)$`)
	parenRe  = regexp.MustCompile(`(?i)\s*[\(\[]\s*((\d{4}\s+)?(remaster(ed)?|re-?mastered|mono|stereo)(\s+(version|\d{4}))?|\d{4}\s+remaster)\s*[\)\]]`)
	splitArt = regexp.MustCompile(`(?i)\s*(,|&|\bx\b|\bfeat\.?|\bft\.?|\bfeaturing\b|\bwith\b)\s*`)
	nonWord  = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// CleanTitle strips "(feat. X)", " - Remastered 2011", "(2011 Remaster)".
// It keeps "(Radio Edit)", "(Live)", "(Acoustic)" when parenthesised, since
// those are different recordings and duration filtering handles them.
func CleanTitle(s string) string {
	s = featRe.ReplaceAllString(s, "")
	s = dashRe.ReplaceAllString(s, "")
	s = parenRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// PrimaryArtist returns the first credited artist ("Lady Gaga & Bruno Mars" -> "Lady Gaga").
// Callers should try the full string first: some acts have "&" in their name.
func PrimaryArtist(s string) string {
	if loc := splitArt.FindStringIndex(s); loc != nil && loc[0] > 0 {
		return strings.TrimSpace(s[:loc[0]])
	}
	return s
}

// Key is a cache/compare key close to LRCLIB's own normalisation
// (lowercase, punctuation collapsed to single spaces, apostrophes removed).
func Key(s string) string {
	s = strings.ToLower(strings.NewReplacer("'", "", "’", "").Replace(s))
	return strings.TrimSpace(nonWord.ReplaceAllString(s, " "))
}

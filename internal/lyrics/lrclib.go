package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UserAgent identifies encore to LRCLIB, as its API requires.
const UserAgent = "encore/0.1 (https://github.com/bogdankim/encore)"

// Record is one LRCLIB lyrics record.
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
}

func (r *Record) Synced() bool { return r.SyncedLyrics != nil && *r.SyncedLyrics != "" }

// ErrNotFound means no lyrics exist for the track (a definitive miss, not an
// outage).
var ErrNotFound = errors.New("lyrics: not found")

// HTTPError is an unexpected LRCLIB status. It's always worth retrying later.
type HTTPError struct {
	Status     int
	RetryAfter time.Duration // set for 429/503 we didn't wait out
}

func (e *HTTPError) Error() string { return fmt.Sprintf("lrclib: HTTP %d", e.Status) }

// Client talks to lrclib.net. The zero value works; don't copy one in use.
type Client struct {
	HTTP      *http.Client // default: 10 s timeout
	UserAgent string       // default UserAgent
	Base      string       // default https://lrclib.net

	// MaxRetryWait is the longest Retry-After the client sleeps through
	// before giving up with an HTTPError; default 10 s.
	MaxRetryWait time.Duration

	// After a 429 or 503 every request, not only the one that got it,
	// waits out the Retry-After.
	mu         sync.Mutex
	notBefore  time.Time
	backoffErr int       // the status behind notBefore
	busyAt     time.Time // the last 429 or 503
}

// busyMemory is how long after a 429 or 503 Find stops hedging: an
// overloaded LRCLIB answers everything slowly, and a second request only
// adds to the load.
const busyMemory = 30 * time.Second

func (c *Client) busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.busyAt.IsZero() && time.Since(c.busyAt) < busyMemory
}

// maxAttempts is one try and two retries. A 503 from an overloaded LRCLIB
// takes about 1.1 s to arrive and asks for 1 s more; the first retry waits
// that out, the second twice as long. The lyrics pane doesn't wait for
// them: it says "No lyrics" after 3 s and updates if they come.
const maxAttempts = 3

func (c *Client) do(ctx context.Context, path string, q url.Values, out any) error {
	base := c.Base
	if base == "" {
		base = "https://lrclib.net"
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	ua := c.UserAgent
	if ua == "" {
		ua = UserAgent
	}
	maxWait := c.MaxRetryWait
	if maxWait == 0 {
		maxWait = 10 * time.Second
	}
	for attempt := 1; ; attempt++ {
		if err := c.waitBackoff(ctx, maxWait); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		switch resp.StatusCode {
		case http.StatusOK:
			err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("lrclib: bad response: %w", err)
			}
			return nil
		case http.StatusNotFound:
			resp.Body.Close()
			return ErrNotFound
		case http.StatusTooManyRequests, http.StatusServiceUnavailable:
			resp.Body.Close()
			wait := retryAfter(resp.Header.Get("Retry-After"), time.Now())
			if attempt >= maxAttempts || wait > maxWait {
				c.backoff(resp.StatusCode, wait)
				return &HTTPError{Status: resp.StatusCode, RetryAfter: wait}
			}
			c.backoff(resp.StatusCode, wait<<(attempt-1))
		default:
			resp.Body.Close()
			return &HTTPError{Status: resp.StatusCode}
		}
	}
}

// backoff holds every request until wait from now.
func (c *Client) backoff(status int, wait time.Duration) {
	c.mu.Lock()
	now := time.Now()
	c.busyAt = now
	if until := now.Add(wait); until.After(c.notBefore) {
		c.notBefore, c.backoffErr = until, status
	}
	c.mu.Unlock()
}

// waitBackoff sleeps out a Retry-After plus the up to 30% random jitter
// LRCLIB asks for. A wait longer than maxWait fails at once.
func (c *Client) waitBackoff(ctx context.Context, maxWait time.Duration) error {
	c.mu.Lock()
	wait, status := time.Until(c.notBefore), c.backoffErr
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	if wait > maxWait {
		return &HTTPError{Status: status, RetryAfter: wait}
	}
	wait += time.Duration(rand.Float64() * 0.3 * float64(wait))
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryAfter parses delta-seconds or an HTTP date; it defaults to 1 s.
func retryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if n, err := strconv.Atoi(h); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(0, t.Sub(now))
	}
	return time.Second
}

// Get looks up one signature. dur is in seconds; 0 omits it, though LRCLIB
// matches much better with it (±2 s window).
func (c *Client) Get(ctx context.Context, track, artist, album string, dur float64) (*Record, error) {
	q := url.Values{"track_name": {track}, "artist_name": {artist}}
	if album != "" {
		q.Set("album_name", album)
	}
	if d := int(math.Round(dur)); d >= 1 && d <= 3600 {
		q.Set("duration", strconv.Itoa(d))
	}
	var r Record
	if err := c.do(ctx, "/api/get", q, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Search returns up to 20 records matching title (and artist).
func (c *Client) Search(ctx context.Context, track, artist string) ([]Record, error) {
	q := url.Values{"track_name": {track}}
	if artist != "" {
		q.Set("artist_name", artist)
	}
	var rs []Record
	err := c.do(ctx, "/api/search", q, &rs)
	return rs, err
}

// cleanHedge is how long Find waits on the exact-signature get before also
// sending the cleaned one. Most answers take about 150 ms, so the second
// request usually isn't needed.
const cleanHedge = 250 * time.Millisecond

// Find runs the fallback chain (at most 3 requests, never more than 2 at
// once):
//
//  1. get with the exact signature,
//  2. get with the cleaned title and primary artist, no album, sent when 1
//     misses or hasn't answered within cleanHedge (unless LRCLIB has been
//     overloaded lately),
//  3. search, once both gets have answered without synced lyrics, keeping
//     candidates within ±3 s and scoring synced +4, title match +2, album
//     match +1.
//
// It returns the first synced or instrumental record and otherwise the best
// plain one, the exact get's ahead of the cleaned one's. With nothing found
// it returns ErrNotFound. When a request fails it returns whatever plain
// record it has along with the error, so callers can show it without
// caching it.
func (c *Client) Find(ctx context.Context, track, artist, album string, dur float64) (*Record, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the requests still in flight

	ct, ca := CleanTitle(track), PrimaryArtist(artist)
	const (
		stepExact = iota
		stepClean
		stepSearch
	)
	type answer struct {
		step int
		rec  *Record
		recs []Record
		err  error
	}
	ch := make(chan answer, 3)
	pending := 0
	start := func(step int) {
		pending++
		go func() {
			a := answer{step: step}
			switch step {
			case stepExact:
				a.rec, a.err = c.Get(ctx, track, artist, album, dur)
			case stepClean:
				a.rec, a.err = c.Get(ctx, ct, ca, "", dur)
			default:
				a.recs, a.err = c.Search(ctx, ct, ca)
			}
			ch <- a
		}()
	}

	needClean := ct != track || ca != artist || album != ""
	start(stepExact)
	var hedge <-chan time.Time
	if needClean && !c.busy() {
		t := time.NewTimer(cleanHedge)
		defer t.Stop()
		hedge = t.C
	}
	var plain [2]*Record // per get step
	var fail error       // the first; LRCLIB failing isn't helped by more requests
	cleanStarted, getsLeft := !needClean, 1
	if needClean {
		getsLeft = 2
	}
	startClean := func() {
		hedge, cleanStarted = nil, true
		start(stepClean)
	}
	var picked *Record
	for pending > 0 || hedge != nil {
		select {
		case <-ctx.Done():
			return firstPlain(plain), ctx.Err()
		case <-hedge:
			startClean()
			continue
		case a := <-ch:
			pending--
			if a.err != nil && !errors.Is(a.err, ErrNotFound) && fail == nil {
				fail = a.err
			}
			if a.step == stepSearch {
				picked = pickSearch(a.recs, ct, album, dur)
				continue
			}
			if r := a.rec; r != nil && a.err == nil {
				if r.Synced() || r.Instrumental {
					return r, nil
				}
				if r.PlainLyrics != nil && *r.PlainLyrics != "" {
					plain[a.step] = r
				}
			}
			if fail != nil {
				hedge = nil
				continue
			}
			if getsLeft--; !cleanStarted {
				startClean() // missed: no reason to wait
			}
			if getsLeft == 0 {
				start(stepSearch)
			}
		}
	}
	best := firstPlain(plain)
	if picked != nil && (best == nil || picked.Synced() || picked.Instrumental) {
		best = picked
	}
	if best == nil && fail == nil {
		return nil, ErrNotFound
	}
	return best, fail
}

func firstPlain(rs [2]*Record) *Record {
	if rs[0] != nil {
		return rs[0]
	}
	return rs[1]
}

// pickSearch scores search results: synced +4, title match +2, album match
// +1, only within ±3 s of dur.
func pickSearch(rs []Record, ct, album string, dur float64) *Record {
	var pick *Record
	score := -1
	for i := range rs {
		x := &rs[i]
		if dur > 0 && math.Abs(x.Duration-dur) > 3 {
			continue
		}
		if !x.Synced() && !x.Instrumental && (x.PlainLyrics == nil || *x.PlainLyrics == "") {
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
	return pick
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

// PrimaryArtist returns the first credited artist ("Lady Gaga & Bruno Mars"
// -> "Lady Gaga"). Find tries the full string first: some acts have "&" in
// their name.
func PrimaryArtist(s string) string {
	if loc := splitArt.FindStringIndex(s); loc != nil && loc[0] > 0 {
		return strings.TrimSpace(s[:loc[0]])
	}
	return s
}

// Key is a compare/cache key close to LRCLIB's own normalisation
// (lowercase, punctuation collapsed to single spaces, apostrophes removed).
func Key(s string) string {
	s = strings.ToLower(strings.NewReplacer("'", "", "’", "").Replace(s))
	return strings.TrimSpace(nonWord.ReplaceAllString(s, " "))
}

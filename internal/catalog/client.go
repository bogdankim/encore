package catalog

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UserAgent identifies encore to Apple.
const UserAgent = "encore/0.1 (https://github.com/bogdankim/encore)"

// ErrNotFound means a lookup id matched nothing in this storefront.
var ErrNotFound = errors.New("catalog: not found")

// Defaults for the zero fields of a Client.
const (
	DefaultBase      = "https://itunes.apple.com"
	DefaultSearchTTL = 10 * time.Minute
	DefaultLookupTTL = 24 * time.Hour
	DefaultDiskTTL   = 7 * 24 * time.Hour
	DefaultPerMinute = 18
	DefaultBurst     = 5
	DefaultMaxWait   = 15 * time.Second

	memEntries   = 256
	maxBody      = 8 << 20
	maxLimit     = 200 // the API's cap on limit
	lookupChunk  = 100 // ids per batch lookup
	diskPruneAge = 30 * 24 * time.Hour
)

// Client talks to the iTunes Search API. The zero value works (US
// storefront, no disk cache); New is the usual constructor. Configure it
// before first use. It is safe for concurrent use.
type Client struct {
	HTTP      *http.Client // default: 10 s timeout
	Country   string       // ISO 3166 alpha-2 storefront, e.g. "ca"; default "us"
	UserAgent string       // default UserAgent
	Base      string       // default DefaultBase
	WebBase   string       // default DefaultWebBase

	// CacheDir holds lookup responses as JSON files; "" disables the disk
	// cache. New sets DefaultCacheDir().
	CacheDir string

	SearchTTL time.Duration // in memory; default 10 min
	LookupTTL time.Duration // in memory; default 1 day
	DiskTTL   time.Duration // on disk, lookups only; default 7 days

	PerMinute float64       // sustained request rate; default 18
	Burst     int           // default 5
	MaxWait   time.Duration // longest wait for a token before ErrRateLimited; default 15 s

	clk      clock
	once     sync.Once
	lim      *limiter
	webLim   *limiter
	mem      *memCache
	disk     *diskCache
	mu       sync.Mutex
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	body []byte
	err  error
}

// New returns a client for the given storefront country with the disk cache
// on. An empty country runs DetectCountry.
func New(country string) *Client {
	if country == "" {
		country = DetectCountry()
	}
	return &Client{Country: country, CacheDir: DefaultCacheDir()}
}

func (c *Client) init() {
	c.once.Do(func() {
		if c.clk == nil {
			c.clk = realClock{}
		}
		if c.HTTP == nil {
			c.HTTP = &http.Client{Timeout: 10 * time.Second}
		}
		if c.UserAgent == "" {
			c.UserAgent = UserAgent
		}
		if c.Base == "" {
			c.Base = DefaultBase
		}
		c.Base = strings.TrimRight(c.Base, "/")
		if c.WebBase == "" {
			c.WebBase = DefaultWebBase
		}
		c.WebBase = strings.TrimRight(c.WebBase, "/")
		c.Country = normCountry(c.Country)
		if c.SearchTTL <= 0 {
			c.SearchTTL = DefaultSearchTTL
		}
		if c.LookupTTL <= 0 {
			c.LookupTTL = DefaultLookupTTL
		}
		if c.DiskTTL <= 0 {
			c.DiskTTL = DefaultDiskTTL
		}
		if c.PerMinute <= 0 {
			c.PerMinute = DefaultPerMinute
		}
		if c.Burst <= 0 {
			c.Burst = DefaultBurst
		}
		if c.MaxWait <= 0 {
			c.MaxWait = DefaultMaxWait
		}
		c.lim = newLimiter(c.clk, c.PerMinute, c.Burst, c.MaxWait)
		c.webLim = newLimiter(c.clk, webPerMinute, webBurst, c.MaxWait)
		c.mem = newMemCache(memEntries)
		if c.CacheDir != "" {
			c.disk = &diskCache{dir: c.CacheDir}
		}
		c.inflight = map[string]*call{}
	})
}

func normCountry(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z' {
		return s
	}
	return "us"
}

// Search finds artists, albums and songs matching q, up to limit of each
// (default 25, at most 200). It sends one request per kind, in parallel,
// since the API's multi-entity search drops albums and ignores the per-kind
// limit. On error the Results hold whatever kinds did arrive. A cancelled
// ctx returns ctx.Err() and empty Results.
func (c *Client) Search(ctx context.Context, q string, limit int) (Results, error) {
	return c.SearchKinds(ctx, q, AllKinds, limit)
}

// SearchKinds is Search for the kinds asked for only, one request each:
// finding one album costs a third of a full search.
func (c *Client) SearchKinds(ctx context.Context, q string, kinds Kind, limit int) (Results, error) {
	c.init()
	term := strings.Join(strings.Fields(strings.ToLower(q)), " ")
	if term == "" {
		return Results{}, nil
	}
	if limit <= 0 {
		limit = 25
	}
	limit = min(limit, maxLimit)
	var (
		res  Results
		errs [3]error
		wg   sync.WaitGroup
	)
	run := func(i int, entity string, keep func([]record)) {
		if kinds&(1<<i) == 0 {
			return
		}
		wg.Go(func() {
			body, err := c.get(ctx, "/search", url.Values{
				"term": {term}, "entity": {entity}, "limit": {strconv.Itoa(limit)},
			}, false)
			if err == nil {
				var rs []record
				if rs, err = decode(body); err == nil {
					keep(rs)
				}
			}
			errs[i] = err
		})
	}
	run(0, "musicArtist", func(rs []record) {
		for i := range rs {
			if rs[i].isArtist() {
				res.Artists = append(res.Artists, rs[i].artist())
			}
		}
	})
	run(1, "album", func(rs []record) {
		for i := range rs {
			if rs[i].isAlbum() {
				res.Albums = append(res.Albums, rs[i].album())
			}
		}
	})
	run(2, "song", func(rs []record) {
		for i := range rs {
			if rs[i].isSong() {
				res.Songs = append(res.Songs, rs[i].song())
			}
		}
	})
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Results{}, err
	}
	for _, err := range errs {
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// ArtistAlbums returns the artist and their albums, newest first (the API's
// own order isn't chronological). Singles and EPs are included; Apple
// labels them all "Album".
func (c *Client) ArtistAlbums(ctx context.Context, artistID int64) (Artist, []Album, error) {
	rs, err := c.lookup(ctx, url.Values{"id": {id(artistID)}, "entity": {"album"}, "limit": {strconv.Itoa(maxLimit)}})
	if err != nil {
		return Artist{}, nil, err
	}
	var (
		artist Artist
		found  bool
		albums []Album
		seen   = map[int64]bool{}
	)
	for i := range rs {
		r := &rs[i]
		switch {
		case r.isArtist() && r.ArtistID == artistID:
			artist, found = r.artist(), true
		case r.isAlbum() && !seen[r.CollectionID]:
			seen[r.CollectionID] = true
			albums = append(albums, r.album())
		}
	}
	if !found && len(albums) == 0 {
		return Artist{}, nil, ErrNotFound
	}
	slices.SortStableFunc(albums, func(a, b Album) int {
		if c := b.Released.Compare(a.Released); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return artist, albums, nil
}

// ArtistTopSongs returns up to limit (default 10, at most 200) of the
// artist's songs in Apple's popularity order.
func (c *Client) ArtistTopSongs(ctx context.Context, artistID int64, limit int) ([]Song, error) {
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, maxLimit)
	rs, err := c.lookup(ctx, url.Values{"id": {id(artistID)}, "entity": {"song"}, "limit": {strconv.Itoa(limit)}})
	if err != nil {
		return nil, err
	}
	var songs []Song
	found := false
	for i := range rs {
		switch {
		case rs[i].isArtist():
			found = true
		case rs[i].isSong():
			songs = append(songs, rs[i].song())
		}
	}
	if !found && len(songs) == 0 {
		return nil, ErrNotFound
	}
	return songs, nil
}

// AlbumTracks returns the album and its songs in disc and track order.
// Music videos on the album are left out.
func (c *Client) AlbumTracks(ctx context.Context, collectionID int64) (Album, []Song, error) {
	rs, err := c.lookup(ctx, url.Values{"id": {id(collectionID)}, "entity": {"song"}, "limit": {strconv.Itoa(maxLimit)}})
	if err != nil {
		return Album{}, nil, err
	}
	var (
		album  Album
		found  bool
		tracks []Song
	)
	for i := range rs {
		r := &rs[i]
		switch {
		case r.WrapperType == "collection" && r.CollectionID == collectionID:
			album, found = r.album(), true
		case r.isSong() && r.CollectionID == collectionID:
			tracks = append(tracks, r.song())
		}
	}
	if !found {
		return Album{}, nil, ErrNotFound
	}
	slices.SortStableFunc(tracks, func(a, b Song) int {
		return cmp.Or(cmp.Compare(a.DiscNumber, b.DiscNumber), cmp.Compare(a.TrackNumber, b.TrackNumber))
	})
	return album, tracks, nil
}

// LookupSongs fetches songs by catalog id, 100 per request, and returns them
// in the order asked for. Ids that aren't songs in this storefront are
// skipped. On error it returns the songs found so far.
func (c *Client) LookupSongs(ctx context.Context, ids []int64) ([]Song, error) {
	var uniq []int64
	seen := map[int64]bool{}
	for _, x := range ids {
		if x > 0 && !seen[x] {
			seen[x] = true
			uniq = append(uniq, x)
		}
	}
	byID := map[int64]Song{}
	var err error
	for chunk := range slices.Chunk(uniq, lookupChunk) {
		chunk = slices.Sorted(slices.Values(chunk)) // same set, same cache key
		parts := make([]string, len(chunk))
		for i, x := range chunk {
			parts[i] = id(x)
		}
		var rs []record
		if rs, err = c.lookup(ctx, url.Values{"id": {strings.Join(parts, ",")}}); err != nil {
			break
		}
		for i := range rs {
			if rs[i].isSong() && seen[rs[i].TrackID] {
				byID[rs[i].TrackID] = rs[i].song()
			}
		}
	}
	songs := make([]Song, 0, len(byID))
	for _, x := range uniq {
		if s, ok := byID[x]; ok {
			songs = append(songs, s)
		}
	}
	return songs, err
}

func id(x int64) string { return strconv.FormatInt(x, 10) }

func (c *Client) lookup(ctx context.Context, q url.Values) ([]record, error) {
	c.init()
	body, err := c.get(ctx, "/lookup", q, true)
	if err != nil {
		return nil, err
	}
	return decode(body)
}

// get returns the response body for path?q, from memory, then disk (lookups
// only), then the network. Concurrent gets of one URL share a request. When
// the network fails and an expired disk entry exists, get returns that.
func (c *Client) get(ctx context.Context, path string, q url.Values, lookup bool) ([]byte, error) {
	q.Set("country", c.Country)
	key := path + "?" + q.Encode() // Encode sorts by key, so keys are stable
	now := c.clk.Now()
	if body, ok := c.mem.get(key, now); ok {
		return body, nil
	}
	var stale []byte
	if lookup && c.disk != nil {
		if body, fetched, ok := c.disk.get(key); ok {
			if until := fetched.Add(c.DiskTTL); now.Before(until) {
				c.mem.put(key, body, minTime(until, now.Add(c.LookupTTL)))
				return body, nil
			}
			stale = body
		}
	}
	body, err := c.shared(ctx, key, lookup)
	if err != nil && stale != nil && ctx.Err() == nil {
		return stale, nil
	}
	return body, err
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// shared runs one fetch per key at a time. A waiter whose leader was
// cancelled tries again with its own ctx instead of inheriting the error.
func (c *Client) shared(ctx context.Context, key string, lookup bool) ([]byte, error) {
	for {
		c.mu.Lock()
		if cl := c.inflight[key]; cl != nil {
			c.mu.Unlock()
			select {
			case <-cl.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if isCtxErr(cl.err) && ctx.Err() == nil {
				continue
			}
			return cl.body, cl.err
		}
		cl := &call{done: make(chan struct{})}
		c.inflight[key] = cl
		c.mu.Unlock()

		cl.body, cl.err = c.fetch(ctx, key)
		if cl.err == nil {
			now := c.clk.Now()
			ttl := c.SearchTTL
			if lookup {
				ttl = c.LookupTTL
				if c.disk != nil {
					c.disk.prune(diskPruneAge, now)
					c.disk.put(key, cl.body, now)
				}
			}
			c.mem.put(key, cl.body, now.Add(ttl))
		}
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(cl.done)
		return cl.body, cl.err
	}
}

func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (c *Client) fetch(ctx context.Context, key string) ([]byte, error) {
	if err := c.lim.wait(ctx); err != nil {
		return nil, err
	}
	sent := c.clk.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+key, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
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
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if !json.Valid(body) {
			return nil, errors.New("catalog: bad response: not JSON")
		}
		c.lim.ok(sent)
		return body, nil
	case http.StatusForbidden, http.StatusTooManyRequests:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &RateLimitError{Status: resp.StatusCode, RetryAfter: c.lim.refused(sent)}
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e struct {
			ErrorMessage string `json:"errorMessage"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, &HTTPError{Status: resp.StatusCode, Message: e.ErrorMessage}
	}
}

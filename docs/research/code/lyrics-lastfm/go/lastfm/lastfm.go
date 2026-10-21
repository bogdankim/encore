// Package lastfm is a minimal Last.fm API 2.0 client: request signing,
// desktop auth (getToken -> browser -> poll getSession), now playing and
// batched scrobbles.
package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	APIRoot = "https://ws.audioscrobbler.com/2.0/"
	AuthURL = "https://www.last.fm/api/auth/"
)

// Sign returns api_sig: md5 of name+value pairs sorted by name (byte order),
// with "format" and "callback" excluded, followed by the shared secret.
// Values must be UTF-8 (Go strings already are).
func Sign(params url.Values, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "callback" || k == "api_sig" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys) // byte order: "artist[10]" < "artist[1]" as Last.fm requires
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(params.Get(k))
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// APIError is a Last.fm error response: {"error": 9, "message": "..."}.
type APIError struct {
	Code    int    `json:"error"`
	Message string `json:"message"`
	HTTP    int    `json:"-"`
}

func (e *APIError) Error() string { return fmt.Sprintf("last.fm error %d: %s", e.Code, e.Message) }

// Retryable reports whether the request should be kept and retried later.
func (e *APIError) Retryable() bool {
	return e.Code == 11 || e.Code == 16 || e.Code == 29 || e.HTTP >= 500
}

// NeedsReauth reports whether the session key is dead.
func (e *APIError) NeedsReauth() bool { return e.Code == 9 }

type Client struct {
	Key, Secret, SessionKey string
	HTTP                    *http.Client
	UserAgent               string
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Call performs a signed call. Write methods go as POST form bodies; reads use GET.
// out receives the JSON body on success.
func (c *Client) Call(ctx context.Context, method string, params url.Values, post, withSession bool, out any) error {
	p := url.Values{}
	for k, v := range params {
		p[k] = v
	}
	p.Set("method", method)
	p.Set("api_key", c.Key)
	if withSession {
		p.Set("sk", c.SessionKey)
	}
	p.Set("api_sig", Sign(p, c.Secret))
	p.Set("format", "json") // added after signing; never part of api_sig

	var req *http.Request
	var err error
	if post {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, APIRoot, strings.NewReader(p.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, APIRoot+"?"+p.Encode(), nil)
	}
	if err != nil {
		return err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err // network error: retryable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	// Always inspect the body: errors can come with 200, and malformed POSTs
	// can even get a plain-text legacy "FAILED ..." reply with HTTP 200.
	var apiErr APIError
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
		apiErr.HTTP = resp.StatusCode
		return &apiErr
	}
	if !json.Valid(body) {
		return &APIError{Code: -1, Message: "non-JSON response: " + truncate(string(body), 120), HTTP: resp.StatusCode}
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// --- Desktop auth -----------------------------------------------------------

// GetToken returns an unauthorized request token (valid 60 min, single use).
func (c *Client) GetToken(ctx context.Context) (string, error) {
	var r struct {
		Token string `json:"token"`
	}
	if err := c.Call(ctx, "auth.getToken", nil, false, false, &r); err != nil {
		return "", err
	}
	return r.Token, nil
}

// AuthorizeURL is the page to open in the user's browser.
func (c *Client) AuthorizeURL(token string) string {
	return AuthURL + "?" + url.Values{"api_key": {c.Key}, "token": {token}}.Encode()
}

type Session struct {
	Name       string `json:"name"`
	Key        string `json:"key"`
	Subscriber int    `json:"subscriber"`
}

// PollSession calls auth.getSession until the user approves (error 14 means
// "not authorized yet"), the token expires (15), or ctx ends.
func (c *Client) PollSession(ctx context.Context, token string, every time.Duration) (Session, error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		var r struct {
			Session Session `json:"session"`
		}
		err := c.Call(ctx, "auth.getSession", url.Values{"token": {token}}, false, false, &r)
		if err == nil {
			return r.Session, nil
		}
		var ae *APIError
		if !errors.As(err, &ae) || (ae.Code != 14 && !ae.Retryable()) {
			return Session{}, err // 15 expired, 4 invalid token, 10/13 bad key/secret
		}
		select {
		case <-ctx.Done():
			return Session{}, ctx.Err()
		case <-t.C:
		}
	}
}

// --- Scrobbling -------------------------------------------------------------

type Track struct {
	Artist, Track, Album, AlbumArtist string
	Duration                          int   // seconds, 0 = unknown
	TrackNumber                       int   // 0 = unknown
	Timestamp                         int64 // unix seconds UTC when playback started
}

func (c *Client) UpdateNowPlaying(ctx context.Context, t Track) error {
	p := url.Values{"artist": {t.Artist}, "track": {t.Track}}
	setOpt(p, "album", t.Album)
	setOpt(p, "albumArtist", albumArtist(t))
	if t.Duration > 0 {
		p.Set("duration", strconv.Itoa(t.Duration))
	}
	return c.Call(ctx, "track.updateNowPlaying", p, true, true, nil)
}

// ScrobbleResult holds accepted/ignored counts. Per-item details are left raw
// because Last.fm returns an object for one scrobble and an array for many.
type ScrobbleResult struct {
	Scrobbles struct {
		Attr struct {
			Accepted FlexInt `json:"accepted"`
			Ignored  FlexInt `json:"ignored"`
		} `json:"@attr"`
		Scrobble json.RawMessage `json:"scrobble"`
	} `json:"scrobbles"`
}

// Scrobble sends up to 50 plays, oldest first.
func (c *Client) Scrobble(ctx context.Context, ts []Track) (ScrobbleResult, error) {
	var res ScrobbleResult
	if len(ts) == 0 || len(ts) > 50 {
		return res, fmt.Errorf("batch size %d out of range 1..50", len(ts))
	}
	p := url.Values{}
	for i, t := range ts {
		ix := "[" + strconv.Itoa(i) + "]"
		p.Set("artist"+ix, t.Artist)
		p.Set("track"+ix, t.Track)
		p.Set("timestamp"+ix, strconv.FormatInt(t.Timestamp, 10))
		setOpt(p, "album"+ix, t.Album)
		setOpt(p, "albumArtist"+ix, albumArtist(t))
		if t.Duration > 0 {
			p.Set("duration"+ix, strconv.Itoa(t.Duration))
		}
		if t.TrackNumber > 0 {
			p.Set("trackNumber"+ix, strconv.Itoa(t.TrackNumber))
		}
	}
	err := c.Call(ctx, "track.scrobble", p, true, true, &res)
	return res, err
}

// FlexInt accepts 1 or "1"; Last.fm JSON is loose about number types.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(b []byte) error {
	n, err := strconv.Atoi(strings.Trim(string(b), `"`))
	*f = FlexInt(n)
	return err
}

func albumArtist(t Track) string {
	if t.AlbumArtist == t.Artist {
		return ""
	}
	return t.AlbumArtist
}

func setOpt(p url.Values, k, v string) {
	if v != "" {
		p.Set(k, v)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

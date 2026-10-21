// Package lastfm scrobbles to Last.fm: a minimal API 2.0 client (signing,
// desktop auth, now playing, batched scrobbles), credentials in the
// Keychain, and a Scrobbler that turns player updates into scrobbles through
// a disk queue.
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
	APIRoot          = "https://ws.audioscrobbler.com/2.0/"
	AuthURL          = "https://www.last.fm/api/auth/"
	AccountCreateURL = "https://www.last.fm/api/account/create" // where users get a key and secret
	UserAgent        = "encore/0.1 (https://github.com/bogdankim/encore)"
	MaxBatch         = 50
)

// Sign returns api_sig: md5 of name+value pairs sorted by name (byte order),
// with "format" and "callback" excluded, followed by the shared secret.
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
// Code -1 marks a response that wasn't a usable JSON reply.
type APIError struct {
	Code    int    `json:"error"`
	Message string `json:"message"`
	HTTP    int    `json:"-"`
}

func (e *APIError) Error() string { return fmt.Sprintf("last.fm error %d: %s", e.Code, e.Message) }

// Retryable reports whether the request should be kept and retried later:
// 8 operation failed ("try again"), 11 service offline, 16 temporarily
// unavailable, 29 rate limit, a non-JSON reply, or any 5xx.
func (e *APIError) Retryable() bool {
	return e.Code == 8 || e.Code == 11 || e.Code == 16 || e.Code == 29 || e.Code == -1 || e.HTTP >= 500
}

// NeedsReauth reports whether the user's session is no longer accepted:
// 9 invalid session key, 4 authentication failed.
func (e *APIError) NeedsReauth() bool { return e.Code == 9 || e.Code == 4 }

// BadPayload reports an error that blames the request's content (6 invalid
// parameters, 7 invalid resource) rather than the service or the session.
func (e *APIError) BadPayload() bool { return e.Code == 6 || e.Code == 7 }

// BadConfig reports an invalid API key or signature (wrong secret), or a
// suspended key.
func (e *APIError) BadConfig() bool { return e.Code == 10 || e.Code == 13 || e.Code == 26 }

// Client is a Last.fm API client. Key and Secret are the application's;
// SessionKey is the user's (from auth.getSession).
type Client struct {
	Key, Secret, SessionKey string
	HTTP                    *http.Client // default: 15 s timeout
	UserAgent               string       // default UserAgent
	Root                    string       // default APIRoot
}

var defaultHTTP = &http.Client{Timeout: 15 * time.Second}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

// Call performs a signed call. Write methods go as POST form bodies; reads
// use GET. out receives the JSON body on success.
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

	root := c.Root
	if root == "" {
		root = APIRoot
	}
	var req *http.Request
	var err error
	if post {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, root, strings.NewReader(p.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, root+"?"+p.Encode(), nil)
	}
	if err != nil {
		return err
	}
	ua := c.UserAgent
	if ua == "" {
		ua = UserAgent
	}
	req.Header.Set("User-Agent", ua)
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
	if resp.StatusCode >= 400 {
		return &APIError{Code: -1, Message: "HTTP " + strconv.Itoa(resp.StatusCode), HTTP: resp.StatusCode}
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// --- Desktop auth -----------------------------------------------------------

// GetToken returns an unauthorized request token (valid 60 min, single use).
// A bad key or secret fails here with error 10 or 13.
func (c *Client) GetToken(ctx context.Context) (string, error) {
	var r struct {
		Token string `json:"token"`
	}
	if err := c.Call(ctx, "auth.getToken", nil, false, false, &r); err != nil {
		return "", err
	}
	if r.Token == "" {
		return "", errors.New("last.fm: empty token")
	}
	return r.Token, nil
}

// AuthorizeURL is the page to open in the user's browser.
func (c *Client) AuthorizeURL(token string) string {
	return AuthURL + "?" + url.Values{"api_key": {c.Key}, "token": {token}}.Encode()
}

type Session struct {
	Name       string  `json:"name"`
	Key        string  `json:"key"`
	Subscriber FlexInt `json:"subscriber"`
}

// PollSession calls auth.getSession every interval until the user approves.
// Error 14 (not authorized yet), retryable API errors and network errors keep
// it polling; 15 (token expired), 4 (invalid token) and other API errors stop
// it, as does ctx.
func (c *Client) PollSession(ctx context.Context, token string, every time.Duration) (Session, error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		var r struct {
			Session Session `json:"session"`
		}
		err := c.Call(ctx, "auth.getSession", url.Values{"token": {token}}, false, false, &r)
		if err == nil && r.Session.Key != "" {
			return r.Session, nil
		}
		if ctx.Err() != nil {
			return Session{}, ctx.Err()
		}
		var ae *APIError
		if errors.As(err, &ae) && ae.Code != 14 && !ae.Retryable() {
			return Session{}, err
		}
		select {
		case <-ctx.Done():
			return Session{}, ctx.Err()
		case <-t.C:
		}
	}
}

// --- Scrobbling -------------------------------------------------------------

// Track is one play, for now playing and scrobbles. It's also the on-disk
// queue record.
type Track struct {
	Artist      string `json:"artist"`
	Track       string `json:"track"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumArtist,omitempty"`
	Duration    int    `json:"duration,omitempty"`    // seconds, 0 = unknown
	TrackNumber int    `json:"trackNumber,omitempty"` // 0 = unknown
	Timestamp   int64  `json:"timestamp"`             // unix seconds UTC when playback started
}

func (c *Client) UpdateNowPlaying(ctx context.Context, t Track) error {
	p := url.Values{"artist": {t.Artist}, "track": {t.Track}}
	setOpt(p, "album", t.Album)
	setOpt(p, "albumArtist", albumArtist(t))
	if t.Duration > 0 {
		p.Set("duration", strconv.Itoa(t.Duration))
	}
	if t.TrackNumber > 0 {
		p.Set("trackNumber", strconv.Itoa(t.TrackNumber))
	}
	return c.Call(ctx, "track.updateNowPlaying", p, true, true, nil)
}

// ScrobbleResult holds accepted/ignored counts. Per-item details are left raw
// because Last.fm returns an object for one scrobble and an array for many;
// use Items.
type ScrobbleResult struct {
	Scrobbles struct {
		Attr struct {
			Accepted FlexInt `json:"accepted"`
			Ignored  FlexInt `json:"ignored"`
		} `json:"@attr"`
		Scrobble json.RawMessage `json:"scrobble"`
	} `json:"scrobbles"`
}

// ScrobbleItem is one entry of a scrobble response. IgnoredMessage.Code is 0
// for accepted plays; otherwise 1 artist ignored, 2 track ignored, 3 too
// old, 4 too new, 5 daily limit.
type ScrobbleItem struct {
	Track struct {
		Text string `json:"#text"`
	} `json:"track"`
	IgnoredMessage struct {
		Code FlexInt `json:"code"`
		Text string  `json:"#text"`
	} `json:"ignoredMessage"`
}

// Items decodes the per-play results, in request order.
func (r ScrobbleResult) Items() ([]ScrobbleItem, error) {
	raw := r.Scrobbles.Scrobble
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '[' {
		var items []ScrobbleItem
		err := json.Unmarshal(raw, &items)
		return items, err
	}
	var one ScrobbleItem
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, err
	}
	return []ScrobbleItem{one}, nil
}

// Scrobble sends 1..50 plays, oldest first.
func (c *Client) Scrobble(ctx context.Context, ts []Track) (ScrobbleResult, error) {
	var res ScrobbleResult
	if len(ts) == 0 || len(ts) > MaxBatch {
		return res, fmt.Errorf("batch size %d out of range 1..%d", len(ts), MaxBatch)
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

// FlexInt accepts 1, "1" and ""; Last.fm JSON is loose about number types.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
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

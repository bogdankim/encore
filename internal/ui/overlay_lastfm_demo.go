package ui

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/lastfm"
)

// demoLastfm stands in for last.fm, the Keychain and the browser in --demo,
// so the setup can be walked through without an account. It approves a
// token on the third poll. A key that starts with "bad" is refused, to show
// the error path.
type demoLastfm struct {
	mu    sync.Mutex
	creds lastfm.Credentials
	polls int
}

var demoLastfmWorld = &demoLastfm{}

// demoLastfmEnv is the LastfmEnv the setup gets in --demo: credentials live
// in memory for the session, nothing opens and no request leaves the
// process.
func demoLastfmEnv() LastfmEnv {
	w := demoLastfmWorld
	return LastfmEnv{
		Auth: &lastfm.Auth{
			HTTP:     &http.Client{Transport: w},
			Root:     "https://demo.invalid/2.0/",
			Interval: time.Second,
			Timeout:  2 * time.Minute,
		},
		Load: func() (lastfm.Credentials, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.creds, nil
		},
		Save: func(c lastfm.Credentials) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.creds = c
			return nil
		},
		Delete: func() error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.creds = lastfm.Credentials{}
			return nil
		},
		Open:   func(string) error { return nil },
		Getenv: func(string) string { return "" },
	}
}

// RoundTrip answers the two auth calls the setup makes.
func (w *demoLastfm) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		r.Body.Close()
	}
	q := r.URL.Query()
	body := `{"error":3,"message":"Invalid Method"}`
	switch q.Get("method") {
	case "auth.getToken":
		w.mu.Lock()
		w.polls = 0
		w.mu.Unlock()
		body = `{"token":"demo"}`
		if strings.HasPrefix(q.Get("api_key"), "bad") {
			body = `{"error":10,"message":"Invalid API key - You must be granted a valid key by last.fm"}`
		}
	case "auth.getSession":
		w.mu.Lock()
		w.polls++
		n := w.polls
		w.mu.Unlock()
		body = `{"error":14,"message":"Unauthorized Token - This token has not been authorized"}`
		if n >= 3 {
			body = `{"session":{"name":"encore-demo","key":"demo","subscriber":0}}`
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

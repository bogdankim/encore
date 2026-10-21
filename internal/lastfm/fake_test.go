package lastfm

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

const testSecret = "secret"

// fakeAPI is a Last.fm stand-in. It checks every signature and hands each
// request to the handler for its method, or answers with a default.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	calls    []url.Values
	handlers map[string]func(p url.Values) (int, string)
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, handlers: map[string]func(url.Values) (int, string){}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	p := r.Form
	if p.Get("format") != "json" {
		f.t.Errorf("format=%q", p.Get("format"))
	}
	if Sign(p, testSecret) != p.Get("api_sig") {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":13,"message":"Invalid method signature supplied"}`))
		return
	}
	method := p.Get("method")
	if (method == "track.scrobble" || method == "track.updateNowPlaying") && r.Method != http.MethodPost {
		f.t.Errorf("%s sent as %s", method, r.Method)
	}
	f.mu.Lock()
	f.calls = append(f.calls, p)
	h := f.handlers[method]
	f.mu.Unlock()
	status, body := 200, `{}`
	if h != nil {
		status, body = h(p)
	}
	w.WriteHeader(status)
	w.Write([]byte(body))
}

func (f *fakeAPI) handle(method string, h func(p url.Values) (int, string)) {
	f.mu.Lock()
	f.handlers[method] = h
	f.mu.Unlock()
}

// callsTo returns the logged requests for method.
func (f *fakeAPI) callsTo(method string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, c := range f.calls {
		if c.Get("method") == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeAPI) root() string { return f.srv.URL + "/2.0/" }

// fakeClock drives Now and After by hand.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, waiter{c.now.Add(d), ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
	c.mu.Unlock()
}

// waiting reports whether something is waiting for exactly d from now.
func (c *fakeClock) waiting(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.waiters {
		if w.at.Equal(c.now.Add(d)) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

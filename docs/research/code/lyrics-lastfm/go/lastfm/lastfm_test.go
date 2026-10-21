package lastfm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Vector from https://www.last.fm/api/authspec section 8.
func TestSignDocVector(t *testing.T) {
	p := url.Values{"api_key": {"xxxxxxxxxx"}, "method": {"auth.getSession"}, "token": {"yyyyyy"}, "format": {"json"}}
	if got, want := Sign(p, "ilovecher"), "b87d61da3cda91a8b6746c4aef55d6f8"; got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestSignBatchOrder(t *testing.T) {
	// "artist[10]" must sort before "artist[1]" (plain byte order).
	p := url.Values{"artist[1]": {"b"}, "artist[10]": {"a"}}
	if got, want := Sign(p, ""), Sign(url.Values{"artist[10]": {"a"}, "artist[1]": {"b"}}, ""); got != want {
		t.Fatal("order dependent")
	}
}

func TestCallAgainstFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p := r.Form
		if Sign(p, "secret") != p.Get("api_sig") {
			w.WriteHeader(403)
			w.Write([]byte(`{"error":13,"message":"Invalid method signature supplied"}`))
			return
		}
		switch p.Get("method") {
		case "track.scrobble":
			w.Write([]byte(`{"scrobbles":{"scrobble":{"artist":{"corrected":"0","#text":"Daft Punk"}},"@attr":{"ignored":0,"accepted":1}}}`))
		case "auth.getSession":
			w.Write([]byte(`{"error":14,"message":"This token has not been authorized"}`))
		}
	}))
	defer srv.Close()

	c := &Client{Key: "k", Secret: "secret", SessionKey: "sk", HTTP: rewrite(srv.URL)}
	res, err := c.Scrobble(context.Background(), []Track{{Artist: "Daft Punk", Track: "Get Lucky", Album: "Random Access Memories", Duration: 367, Timestamp: 1760900000}})
	if err != nil || res.Scrobbles.Attr.Accepted != 1 {
		t.Fatalf("scrobble: %+v %v", res, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.PollSession(ctx, "tok", 10*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("poll: want deadline (still error 14), got %v", err)
	}
}

func TestPlayThreshold(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	p := NewPlay(Track{Duration: 600}, t0)
	for i := 1; i <= 239; i++ {
		p.Tick(t0.Add(time.Duration(i)*time.Second), true)
	}
	if p.Eligible() {
		t.Fatal("eligible before 4 min")
	}
	p.Tick(t0.Add(240*time.Second), false) // paused at 4:00
	if !p.Eligible() {
		t.Fatal("should be eligible at 4 min")
	}
	short := NewPlay(Track{Duration: 30}, t0)
	short.Tick(t0.Add(5*time.Second), true)
	if short.Eligible() {
		t.Fatal("30s track must never scrobble")
	}
}

// rewrite sends every request to the test server.
func rewrite(base string) *http.Client {
	return &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		u, _ := url.Parse(base)
		r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
		r.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, "/2.0/")
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

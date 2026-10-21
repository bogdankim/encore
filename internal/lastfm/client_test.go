package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
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
	// Plain byte order: '0' (0x30) sorts before ']' (0x5d), so "artist[10]"
	// comes before "artist[1]".
	p := url.Values{"artist[1]": {"b"}, "artist[10]": {"a"}}
	if got, want := Sign(p, "s"), md5hex("artist[10]aartist[1]bs"); got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestCallErrors(t *testing.T) {
	f := newFakeAPI(t)
	c := &Client{Key: "k", Secret: testSecret, SessionKey: "sk", Root: f.root()}
	ctx := context.Background()

	f.handle("track.updateNowPlaying", func(url.Values) (int, string) {
		return 200, `{"error":9,"message":"Invalid session key - Please re-authenticate"}`
	})
	var ae *APIError
	if err := c.UpdateNowPlaying(ctx, Track{Artist: "a", Track: "t"}); !errors.As(err, &ae) || !ae.NeedsReauth() || ae.HTTP != 200 {
		t.Fatalf("JSON error with HTTP 200: %v", err)
	}

	f.handle("track.updateNowPlaying", func(url.Values) (int, string) { return 200, "FAILED Plugin bug" })
	if err := c.UpdateNowPlaying(ctx, Track{Artist: "a", Track: "t"}); !errors.As(err, &ae) || ae.Code != -1 || !ae.Retryable() {
		t.Fatalf("non-JSON body: %v", err)
	}

	f.handle("track.updateNowPlaying", func(url.Values) (int, string) { return 502, `{}` })
	if err := c.UpdateNowPlaying(ctx, Track{Artist: "a", Track: "t"}); !errors.As(err, &ae) || !ae.Retryable() {
		t.Fatalf("HTTP 502: %v", err)
	}

	bad := &Client{Key: "k", Secret: "wrong", Root: f.root()}
	if _, err := bad.GetToken(ctx); !errors.As(err, &ae) || !ae.BadConfig() {
		t.Fatalf("bad secret: %v", err)
	}
}

func TestScrobbleParams(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", func(p url.Values) (int, string) {
		return 200, `{"scrobbles":{"scrobble":{"artist":{"corrected":"0","#text":"Daft Punk"},"ignoredMessage":{"code":"0","#text":""}},"@attr":{"ignored":0,"accepted":1}}}`
	})
	c := &Client{Key: "k", Secret: testSecret, SessionKey: "sk", Root: f.root()}
	res, err := c.Scrobble(context.Background(), []Track{{Artist: "Daft Punk", Track: "Get Lucky", Album: "RAM", AlbumArtist: "Daft Punk", Duration: 367, TrackNumber: 8, Timestamp: 1760900000}})
	if err != nil || res.Scrobbles.Attr.Accepted != 1 {
		t.Fatalf("scrobble: %+v %v", res, err)
	}
	items, err := res.Items()
	if err != nil || len(items) != 1 || items[0].IgnoredMessage.Code != 0 {
		t.Fatalf("items: %+v %v", items, err)
	}
	p := f.callsTo("track.scrobble")[0]
	if p.Get("sk") != "sk" || p.Get("timestamp[0]") != "1760900000" || p.Get("trackNumber[0]") != "8" || p.Has("albumArtist[0]") {
		t.Errorf("params: %v", p)
	}
	if _, err := c.Scrobble(context.Background(), make([]Track, 51)); err == nil {
		t.Error("51 plays accepted")
	}
}

func TestScrobbleItemsArray(t *testing.T) {
	var r ScrobbleResult
	r.Scrobbles.Scrobble = []byte(`[{"ignoredMessage":{"code":"0","#text":""}},{"ignoredMessage":{"code":"3","#text":"Timestamp too old"}}]`)
	items, err := r.Items()
	if err != nil || len(items) != 2 || items[1].IgnoredMessage.Code != 3 {
		t.Fatalf("%+v %v", items, err)
	}
}

func TestAuthFlow(t *testing.T) {
	f := newFakeAPI(t)
	polls := 0
	f.handle("auth.getToken", func(url.Values) (int, string) { return 200, `{"token":"tok123"}` })
	f.handle("auth.getSession", func(p url.Values) (int, string) {
		if p.Get("token") != "tok123" {
			return 200, `{"error":4,"message":"Invalid authentication token supplied"}`
		}
		if polls++; polls < 3 {
			return 200, `{"error":14,"message":"This token has not been authorized"}`
		}
		return 200, `{"session":{"name":"bogdan","key":"SESSION","subscriber":"0"}}`
	})
	a := &Auth{Root: f.root(), Interval: 5 * time.Millisecond}
	u, token, err := a.BeginAuth(context.Background(), " k ", testSecret)
	if err != nil || token != "tok123" || !strings.HasPrefix(u, AuthURL+"?") || !strings.Contains(u, "token=tok123") || !strings.Contains(u, "api_key=k") {
		t.Fatalf("BeginAuth: %q %q %v", u, token, err)
	}
	creds, err := a.WaitSession(context.Background(), token)
	if err != nil || creds != (Credentials{APIKey: "k", Secret: testSecret, SessionKey: "SESSION", Username: "bogdan"}) || polls != 3 {
		t.Fatalf("WaitSession: %+v %v after %d polls", creds, err, polls)
	}

	// Error 15/4 stops polling at once.
	if _, err := a.WaitSession(context.Background(), "other"); err == nil || errors.Is(err, ErrAuthTimeout) {
		t.Fatalf("invalid token should stop polling: %v", err)
	}

	// Never approved: times out.
	f.handle("auth.getSession", func(url.Values) (int, string) {
		return 200, `{"error":14,"message":"This token has not been authorized"}`
	})
	a.Timeout = 30 * time.Millisecond
	if _, err := a.WaitSession(context.Background(), token); !errors.Is(err, ErrAuthTimeout) {
		t.Fatalf("want ErrAuthTimeout, got %v", err)
	}

	bad := &Auth{Root: f.root()}
	if _, _, err := bad.BeginAuth(context.Background(), "k", "wrong"); err == nil {
		t.Fatal("wrong secret accepted")
	}
}

func TestCredentialsEnv(t *testing.T) {
	t.Setenv(EnvKey, "envkey")
	t.Setenv(EnvSecret, "")
	c := withEnv(Credentials{APIKey: "stored", Secret: "s", SessionKey: "sk"})
	if c.APIKey != "envkey" || c.Secret != "s" || !c.Ready() {
		t.Fatalf("%+v", c)
	}
	if (Credentials{APIKey: "k", Secret: "s"}).Ready() {
		t.Fatal("no session key is not ready")
	}
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

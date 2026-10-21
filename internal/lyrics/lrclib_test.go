package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClean(t *testing.T) {
	cases := map[string]string{
		"Get Lucky (feat. Pharrell Williams & Nile Rodgers)": "Get Lucky",
		"Bohemian Rhapsody - Remastered 2011":                "Bohemian Rhapsody",
		"Bohemian Rhapsody (2011 Remaster)":                  "Bohemian Rhapsody",
		"Song [feat. X]":                                     "Song",
		"Get Lucky (Radio Edit) [feat. Pharrell Williams]":   "Get Lucky (Radio Edit)",
		"Come Together - 2019 Mix":                           "Come Together - 2019 Mix",
		"Here Comes the Sun - Remastered":                    "Here Comes the Sun",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Lady Gaga & Bruno Mars": "Lady Gaga", "Calvin Harris, Rihanna": "Calvin Harris",
		"Daft Punk": "Daft Punk", "Silk Sonic feat. Bruno Mars": "Silk Sonic",
	} {
		if got := PrimaryArtist(in); got != want {
			t.Errorf("PrimaryArtist(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Key("Don’t Stop Me Now!"); got != "dont stop me now" {
		t.Errorf("Key = %q", got)
	}
}

// fakeLRCLIB answers /api/get and /api/search from a table and logs every
// request as "get track|artist|album|duration" or "search track|artist".
type fakeLRCLIB struct {
	mu     sync.Mutex
	log    []string
	get    map[string]*Record // "track|artist|album" -> record
	search []Record
	status int // when non-zero, every request gets this status
	retry  string
	delay  time.Duration
	slow   map[string]time.Duration // extra delay per "track|artist|album" get
	ua     string
}

func (f *fakeLRCLIB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	f.ua = r.Header.Get("User-Agent")
	status, retry, delay := f.status, f.retry, f.delay
	var body any
	switch r.URL.Path {
	case "/api/get":
		delay += f.slow[q.Get("track_name")+"|"+q.Get("artist_name")+"|"+q.Get("album_name")]
		f.log = append(f.log, "get "+q.Get("track_name")+"|"+q.Get("artist_name")+"|"+q.Get("album_name")+"|"+q.Get("duration"))
		if rec := f.get[q.Get("track_name")+"|"+q.Get("artist_name")+"|"+q.Get("album_name")]; rec != nil {
			body = rec
		}
	case "/api/search":
		f.log = append(f.log, "search "+q.Get("track_name")+"|"+q.Get("artist_name"))
		body = f.search
		if f.search == nil {
			body = []Record{}
		}
	}
	f.mu.Unlock()
	time.Sleep(delay)
	if status != 0 {
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.WriteHeader(status)
		return
	}
	if body == nil {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Failed to find specified track","name":"TrackNotFound","statusCode":404}`))
		return
	}
	json.NewEncoder(w).Encode(body)
}

func (f *fakeLRCLIB) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func newFake(t *testing.T) (*fakeLRCLIB, *Client) {
	f := &fakeLRCLIB{get: map[string]*Record{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &Client{Base: srv.URL}
}

func str(s string) *string { return &s }

func TestFindChain(t *testing.T) {
	f, c := newFake(t)
	f.get["Get Lucky|Daft Punk|"] = &Record{ID: 7, TrackName: "Get Lucky", Duration: 369, SyncedLyrics: str("[00:01.00]hi")}
	r, err := c.Find(context.Background(), "Get Lucky (feat. Pharrell Williams)", "Daft Punk", "RAM (Deluxe)", 368.6)
	if err != nil || r.ID != 7 {
		t.Fatalf("Find = %+v, %v", r, err)
	}
	want := []string{
		"get Get Lucky (feat. Pharrell Williams)|Daft Punk|RAM (Deluxe)|369",
		"get Get Lucky|Daft Punk||369",
	}
	if got := f.requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if f.ua != UserAgent {
		t.Errorf("User-Agent = %q", f.ua)
	}
}

func TestFindSearchScoring(t *testing.T) {
	f, c := newFake(t)
	f.get["Song|Artist|Album"] = &Record{ID: 1, PlainLyrics: str("plain words")}
	f.search = []Record{
		{ID: 2, TrackName: "Song", Duration: 300, SyncedLyrics: str("[00:01.00]x")}, // wrong length
		{ID: 3, TrackName: "Other", Duration: 200, SyncedLyrics: str("[00:01.00]x")},
		{ID: 4, TrackName: "Song", Duration: 201, SyncedLyrics: str("[00:01.00]x")},
	}
	r, err := c.Find(context.Background(), "Song", "Artist", "Album", 200)
	if err != nil || r.ID != 4 {
		t.Fatalf("want synced search hit 4, got %+v %v", r, err)
	}

	f.search = nil
	r, err = c.Find(context.Background(), "Song", "Artist", "Album", 200)
	if err != nil || r.ID != 1 {
		t.Fatalf("want the plain get hit, got %+v %v", r, err)
	}

	r, err = c.Find(context.Background(), "Nothing", "Nobody", "", 200)
	if !errors.Is(err, ErrNotFound) || r != nil {
		t.Fatalf("want ErrNotFound, got %+v %v", r, err)
	}
}

func TestRetryAfter(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"id":5,"syncedLyrics":"[00:01.00]ok"}`))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL}
	r, err := c.Get(context.Background(), "a", "b", "", 100)
	if err != nil || r.ID != 5 || n.Load() != 2 {
		t.Fatalf("got %+v %v after %d requests", r, err, n.Load())
	}

	f, c := newFake(t)
	f.status, f.retry = http.StatusServiceUnavailable, "120"
	start := time.Now()
	_, err = c.Get(context.Background(), "a", "b", "", 100)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 503 || he.RetryAfter != 2*time.Minute || time.Since(start) > time.Second {
		t.Fatalf("long Retry-After should fail fast, got %v", err)
	}
	if retryAfter("", time.Now()) != time.Second || retryAfter("3", time.Now()) != 3*time.Second {
		t.Error("retryAfter parsing")
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := retryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); got != 5*time.Second {
		t.Errorf("HTTP-date Retry-After = %v", got)
	}
}

// LRCLIB_LIVE=1 go test ./internal/lyrics -run Live -v
func TestLiveFind(t *testing.T) {
	if os.Getenv("LRCLIB_LIVE") == "" {
		t.Skip("set LRCLIB_LIVE=1")
	}
	c := &Client{}
	for _, tc := range []struct {
		track, artist, album string
		dur                  float64
		synced               bool
		found                bool
	}{
		{"Get Lucky (feat. Pharrell Williams & Nile Rodgers)", "Daft Punk", "Random Access Memories (Deluxe Edition)", 369, true, true},
		{"Alors on danse", "Stromae", "Alors on danse - Single", 206, true, true},
		{"Avril 14th", "Aphex Twin", "Drukqs", 125, false, true},
		{"Not A Song 123", "Nobody Real Xyz", "", 200, false, false},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		start := time.Now()
		r, err := c.Find(ctx, tc.track, tc.artist, tc.album, tc.dur)
		cancel()
		if !tc.found {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: want ErrNotFound, got %v", tc.track, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.track, err)
			continue
		}
		t.Logf("%-40.40s -> id=%d %q/%q dur=%.0f synced=%v instr=%v (%v)", tc.track, r.ID, r.TrackName, r.AlbumName, r.Duration, r.Synced(), r.Instrumental, time.Since(start).Round(time.Millisecond))
		if tc.synced && !r.Synced() {
			t.Errorf("%s: want synced lyrics", tc.track)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI serves the testdata fixtures (captured from itunes.apple.com,
// country=ca, then trimmed) and logs every request.
type fakeAPI struct {
	t      *testing.T
	mu     sync.Mutex
	reqs   []url.URL
	ua     string
	status int           // when non-zero, every request gets this status
	body   string        // with status
	block  chan struct{} // when non-nil, requests wait for it (or their ctx)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, *r.URL)
	f.ua = r.Header.Get("User-Agent")
	status, body, block := f.status, f.body, f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		w.Write([]byte(body))
		return
	}
	q := r.URL.Query()
	name := ""
	switch {
	case r.URL.Path == "/search":
		name = map[string]string{"musicArtist": "search_artist", "album": "search_album", "song": "search_song"}[q.Get("entity")]
	case q.Get("id") == "657515" && q.Get("entity") == "album":
		name = "artist_albums"
	case q.Get("id") == "657515" && q.Get("entity") == "song":
		name = "artist_songs"
	case q.Get("id") == "1097861387" && q.Get("entity") == "song":
		name = "album_tracks"
	case strings.Contains(q.Get("id"), ","):
		name = "songs_batch"
	}
	if name == "" {
		w.Write([]byte(`{"resultCount":0,"results":[]}`))
		return
	}
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		f.t.Error(err)
		w.WriteHeader(500)
		return
	}
	w.Write(data)
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *fakeAPI) set(status int, body string) {
	f.mu.Lock()
	f.status, f.body = status, body
	f.mu.Unlock()
}

type fakeClock struct {
	mu        sync.Mutex
	now       time.Time
	slept     []time.Duration
	failSleep error // returned by Sleep instead of sleeping
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSleep != nil {
		return f.failSleep
	}
	f.now = f.now.Add(d)
	f.slept = append(f.slept, d)
	return nil
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func newTest(t *testing.T) (*Client, *fakeAPI, *fakeClock) {
	t.Helper()
	api := &fakeAPI{t: t}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	clk := newFakeClock()
	c := &Client{Base: srv.URL, HTTP: srv.Client(), Country: "CA", CacheDir: t.TempDir(), clk: clk}
	return c, api, clk
}

func TestSearchParses(t *testing.T) {
	c, api, _ := newTest(t)
	res, err := c.Search(context.Background(), "  RadioHead ", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artists) != 3 || len(res.Albums) != 3 || len(res.Songs) != 3 {
		t.Fatalf("got %d artists, %d albums, %d songs", len(res.Artists), len(res.Albums), len(res.Songs))
	}
	a := res.Artists[0]
	if a.ID != 657515 || a.Name != "Radiohead" || a.Genre != "Alternative" || !strings.HasPrefix(a.URL, "https://music.apple.com/ca/artist/radiohead/") {
		t.Errorf("artist = %+v", a)
	}
	al := res.Albums[0]
	if al.ID != 1097861387 || al.Name != "OK Computer" || al.Artist != "Radiohead" || al.ArtistID != 657515 ||
		al.TrackCount != 12 || al.Explicit || !al.Released.Equal(time.Date(1997, 5, 21, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("album = %+v", al)
	}
	s := res.Songs[0]
	if s.ID != 1097861834 || s.Name != "Let Down" || s.AlbumID != 1097861387 || s.Album != "OK Computer" ||
		s.TrackNumber != 5 || s.DiscNumber != 1 || s.Duration != 299560*time.Millisecond ||
		!s.Streamable || s.Explicit || s.PreviewURL == "" || s.Released.Year() != 1997 {
		t.Errorf("song = %+v", s)
	}
	if api.count() != 3 {
		t.Errorf("%d requests, want 3", api.count())
	}
	var entities []string
	for _, u := range api.reqs {
		q := u.Query()
		if u.Path != "/search" || q.Get("term") != "radiohead" || q.Get("country") != "ca" || q.Get("limit") != "3" {
			t.Errorf("request %s", u.String())
		}
		entities = append(entities, q.Get("entity"))
	}
	slices.Sort(entities)
	if !slices.Equal(entities, []string{"album", "musicArtist", "song"}) {
		t.Errorf("entities %v", entities)
	}
	if api.ua != UserAgent {
		t.Errorf("User-Agent %q", api.ua)
	}

	if res, err := c.Search(context.Background(), "   ", 3); err != nil || !res.Empty() || api.count() != 3 {
		t.Errorf("blank query: %+v %v, %d requests", res, err, api.count())
	}
}

func TestSearchKindsSendsOneRequestPerKind(t *testing.T) {
	c, api, _ := newTest(t)
	res, err := c.SearchKinds(context.Background(), "ok computer radiohead", Albums, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Albums) == 0 || len(res.Artists) != 0 || len(res.Songs) != 0 {
		t.Fatalf("got %d artists, %d albums, %d songs", len(res.Artists), len(res.Albums), len(res.Songs))
	}
	if api.count() != 1 || api.reqs[0].Query().Get("entity") != "album" {
		t.Fatalf("requests %v", api.reqs)
	}
	// a full search for the same text reuses the cached album answer
	if _, err := c.Search(context.Background(), "ok computer radiohead", 5); err != nil || api.count() != 3 {
		t.Errorf("full search after an album search: %v, %d requests", err, api.count())
	}
}

func TestArtistAlbumsNewestFirst(t *testing.T) {
	c, _, _ := newTest(t)
	artist, albums, err := c.ArtistAlbums(context.Background(), 657515)
	if err != nil {
		t.Fatal(err)
	}
	if artist.Name != "Radiohead" || artist.ID != 657515 {
		t.Errorf("artist = %+v", artist)
	}
	var names []string
	for i, a := range albums {
		names = append(names, a.Name)
		if i > 0 && a.Released.After(albums[i-1].Released) {
			t.Errorf("%s (%v) after %s", a.Name, a.Released, albums[i-1].Name)
		}
	}
	want := []string{"A Moon Shaped Pool", "The King of Limbs - Live from the Basement", "In Rainbows", "Kid A", "OK Computer", "The Bends"}
	if !slices.Equal(names, want) {
		t.Errorf("albums %q\nwant %q", names, want)
	}
}

func TestArtistTopSongs(t *testing.T) {
	c, api, _ := newTest(t)
	songs, err := c.ArtistTopSongs(context.Background(), 657515, 3)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range songs {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"Creep", "Karma Police", "High and Dry"}) {
		t.Errorf("top songs %q", names)
	}
	if q := api.reqs[0].Query(); q.Get("limit") != "3" || q.Get("entity") != "song" {
		t.Errorf("request %s", api.reqs[0].String())
	}
}

func TestAlbumTracksOrdered(t *testing.T) {
	c, api, _ := newTest(t)
	// Serve the tracks back to front; AlbumTracks must restore the order.
	data, _ := os.ReadFile("testdata/album_tracks.json")
	var raw struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(raw.Results[1:])
	reversed, _ := json.Marshal(raw)
	api.set(200, string(reversed))

	album, tracks, err := c.AlbumTracks(context.Background(), 1097861387)
	if err != nil {
		t.Fatal(err)
	}
	if album.Name != "OK Computer" || album.TrackCount != 12 {
		t.Errorf("album = %+v", album)
	}
	var names []string
	for _, s := range tracks {
		names = append(names, s.Name)
	}
	want := []string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien", "Exit Music (For a Film)", "Let Down"}
	if !slices.Equal(names, want) {
		t.Errorf("tracks %q", names)
	}
}

func TestLookupSongs(t *testing.T) {
	c, api, _ := newTest(t)
	// 1097862870 is Kid A, an album: the batch lookup returns it, and
	// LookupSongs drops it.
	songs, err := c.LookupSongs(context.Background(), []int64{1097862870, 1097861834, 1097861834})
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 1 || songs[0].Name != "Let Down" {
		t.Errorf("songs = %+v", songs)
	}
	if got := api.reqs[0].Query().Get("id"); got != "1097861834,1097862870" {
		t.Errorf("id = %q, want sorted", got)
	}
	if songs, err := c.LookupSongs(context.Background(), nil); err != nil || len(songs) != 0 || api.count() != 1 {
		t.Errorf("empty lookup: %v %v", songs, err)
	}
}

func TestNotFoundAndHTTPError(t *testing.T) {
	c, api, _ := newTest(t)
	if _, _, err := c.AlbumTracks(context.Background(), 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("AlbumTracks(42) err = %v", err)
	}
	if _, _, err := c.ArtistAlbums(context.Background(), 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("ArtistAlbums(42) err = %v", err)
	}
	api.set(400, `{"errorMessage":"Invalid value(s) for key(s): [resultEntity]"}`)
	_, err := c.ArtistTopSongs(context.Background(), 1, 5)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 400 || !strings.Contains(he.Message, "resultEntity") {
		t.Errorf("err = %v", err)
	}
	if errors.Is(err, ErrRateLimited) {
		t.Error("400 counted as rate limited")
	}
}

func TestArtURL(t *testing.T) {
	base := "https://is1-ssl.mzstatic.com/image/thumb/Music116/v4/07/60/ba/x/634904078164.png/100x100bb.jpg"
	a := Album{Artwork: base}
	if got := a.ArtURL(600); got != strings.TrimSuffix(base, "100x100bb.jpg")+"600x600bb.jpg" {
		t.Errorf("600: %s", got)
	}
	if got := a.ArtURL(0); got != base {
		t.Errorf("0: %s", got)
	}
	if got := (Song{Artwork: strings.Replace(base, ".jpg", ".png", 1)}).ArtURL(3000); !strings.HasSuffix(got, "/3000x3000bb.png") {
		t.Errorf("png: %s", got)
	}
	if got := (Song{}).ArtURL(600); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := (Album{Artwork: "https://example.com/cover.jpg"}).ArtURL(600); got != "https://example.com/cover.jpg" {
		t.Errorf("unknown shape rewritten: %s", got)
	}
}

func TestCaching(t *testing.T) {
	c, api, clk := newTest(t)
	ctx := context.Background()

	if _, err := c.Search(ctx, "radiohead", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(ctx, "Radiohead", 3); err != nil {
		t.Fatal(err)
	}
	if api.count() != 3 {
		t.Fatalf("second search hit the network: %d requests", api.count())
	}
	if _, _, err := c.AlbumTracks(ctx, 1097861387); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.AlbumTracks(ctx, 1097861387); err != nil {
		t.Fatal(err)
	}
	if api.count() != 4 {
		t.Fatalf("second album lookup hit the network: %d requests", api.count())
	}

	// Searches expire from memory after 10 minutes.
	clk.Advance(11 * time.Minute)
	if _, err := c.Search(ctx, "radiohead", 3); err != nil {
		t.Fatal(err)
	}
	if api.count() != 7 {
		t.Fatalf("expired search: %d requests, want 7", api.count())
	}

	// A new client (a restart) finds lookups on disk, but not searches.
	c2 := &Client{Base: c.Base, HTTP: c.HTTP, Country: "ca", CacheDir: c.CacheDir, clk: clk}
	if _, tracks, err := c2.AlbumTracks(ctx, 1097861387); err != nil || len(tracks) != 5 {
		t.Fatalf("disk hit: %d tracks, %v", len(tracks), err)
	}
	if api.count() != 7 {
		t.Fatalf("disk cache missed: %d requests", api.count())
	}
	if _, err := c2.Search(ctx, "radiohead", 3); err != nil {
		t.Fatal(err)
	}
	if api.count() != 10 {
		t.Fatalf("search after restart: %d requests, want 10", api.count())
	}

	// Another storefront is another cache entry.
	c3 := &Client{Base: c.Base, HTTP: c.HTTP, Country: "us", CacheDir: c.CacheDir, clk: clk}
	c3.AlbumTracks(ctx, 1097861387)
	if api.count() != 11 {
		t.Fatalf("country not in cache key: %d requests", api.count())
	}

	// After 7 days the disk entry is refetched, and when that fails the
	// stale copy is still served.
	clk.Advance(8 * 24 * time.Hour)
	api.set(500, "")
	c4 := &Client{Base: c.Base, HTTP: c.HTTP, Country: "ca", CacheDir: c.CacheDir, clk: clk}
	album, _, err := c4.AlbumTracks(ctx, 1097861387)
	if err != nil || album.Name != "OK Computer" {
		t.Fatalf("stale fallback: %+v %v", album, err)
	}
	if api.count() != 12 {
		t.Fatalf("stale entry not refetched: %d requests", api.count())
	}
}

func TestConcurrentLookupsShareRequest(t *testing.T) {
	c, api, _ := newTest(t)
	c.CacheDir = ""
	api.block = make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range 5 {
		wg.Go(func() { _, _, errs[i] = c.AlbumTracks(context.Background(), 1097861387) })
	}
	for api.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(api.block)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if api.count() != 1 {
		t.Errorf("%d requests, want 1", api.count())
	}
}

func TestSearchCancels(t *testing.T) {
	c, api, _ := newTest(t)
	api.block = make(chan struct{}) // never answers
	defer close(api.block)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var (
		res Results
		err error
	)
	go func() {
		res, err = c.Search(ctx, "radiohead", 3)
		close(done)
	}()
	for api.count() < 3 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Search didn't return after cancel")
	}
	if !errors.Is(err, context.Canceled) || !res.Empty() {
		t.Errorf("res=%+v err=%v", res, err)
	}
	// A cancelled search isn't cached: the next one goes out again.
	api.mu.Lock()
	api.block = nil
	api.mu.Unlock()
	if res, err := c.Search(context.Background(), "radiohead", 3); err != nil || res.Empty() {
		t.Errorf("after cancel: %v", err)
	}
}

func TestLimiterBucket(t *testing.T) {
	clk := newFakeClock()
	l := newLimiter(clk, 18, 5, 15*time.Second)
	ctx := context.Background()
	for range 5 {
		if err := l.wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(clk.slept) != 0 {
		t.Fatalf("burst slept %v", clk.slept)
	}
	// 18/min = one token every 3⅓ s.
	if err := l.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if len(clk.slept) != 1 || (clk.slept[0]-10*time.Second/3).Abs() > time.Millisecond {
		t.Fatalf("sixth request slept %v, want 3.33s", clk.slept)
	}
	// A minute idle refills the burst but no more.
	clk.Advance(time.Minute)
	for range 5 {
		l.wait(ctx)
	}
	if len(clk.slept) != 1 {
		t.Fatalf("refilled burst slept %v", clk.slept[1:])
	}

	// Waits past MaxWait fail fast instead of queueing.
	l = newLimiter(clk, 18, 1, 5*time.Second)
	l.wait(ctx)
	l.tokens = -1 // someone already holds the next token
	err := l.wait(ctx)
	var rl *RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) || rl.RetryAfter < 6*time.Second {
		t.Fatalf("over MaxWait: %v", err)
	}

	// A wait cancelled mid-sleep gives its token back.
	l = newLimiter(clk, 18, 1, 15*time.Second)
	l.wait(ctx)
	clk.failSleep = context.Canceled
	if err := l.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	clk.failSleep = nil
	if l.tokens != 0 {
		t.Errorf("tokens %v after cancel, want 0", l.tokens)
	}
}

func TestRateLimitBackoff(t *testing.T) {
	c, api, clk := newTest(t)
	ctx := context.Background()
	api.set(403, "")

	// Fresh ids each time so nothing comes from cache.
	next := int64(1)
	try := func() error {
		next++
		_, err := c.ArtistTopSongs(ctx, next, 5)
		return err
	}
	for i, want := range []time.Duration{5, 10, 20, 40, 60, 60} {
		want *= time.Second
		n := api.count()
		err := try()
		var rl *RateLimitError
		if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) || rl.Status != 403 || rl.RetryAfter != want {
			t.Fatalf("refusal %d: %v, want 403 retry in %v", i+1, err, want)
		}
		if api.count() != n+1 {
			t.Fatalf("refusal %d: %d requests", i+1, api.count()-n)
		}
		// Inside the window nothing goes out.
		clk.Advance(want - time.Second)
		err = try()
		if !errors.As(err, &rl) || rl.Status != 0 || rl.RetryAfter != time.Second {
			t.Fatalf("in window %d: %v", i+1, err)
		}
		if api.count() != n+1 {
			t.Fatalf("request sent during backoff")
		}
		clk.Advance(time.Second)
	}

	// Success resets the backoff; 429 counts as a refusal too.
	api.set(0, "")
	if _, err := c.ArtistTopSongs(ctx, 657515, 3); err != nil {
		t.Fatal(err)
	}
	api.set(429, "")
	var rl *RateLimitError
	if err := try(); !errors.As(err, &rl) || rl.Status != 429 || rl.RetryAfter != 5*time.Second {
		t.Fatalf("after success: %v", err)
	}
	if !strings.Contains(rl.Error(), "429") {
		t.Errorf("message %q", rl.Error())
	}
}

func TestDetectCountry(t *testing.T) {
	type reply struct {
		out string
		err error
	}
	fail := reply{err: errors.New("exit status 1")}
	nested := `{
    "{\"phoneNumber\":\"\",\"dsid\":\"1\",\"storefrontID\":\"143455\"}" =     {
        phoneNumber = "";
        storefrontID = 143455;
    };
}`
	cases := []struct {
		name               string
		top, nested, local reply
		lang, want         string
	}{
		{"top-level key", reply{out: "143444\n"}, fail, fail, "", "gb"},
		{"with language suffix", reply{out: "143462-9,29\n"}, fail, fail, "", "jp"},
		{"nested status", fail, reply{out: nested}, reply{out: "en_US\n"}, "", "ca"},
		{"unknown id falls to locale", reply{out: "999999\n"}, fail, reply{out: "de_AT\n"}, "", "at"},
		{"locale region override", fail, fail, reply{out: "en_US@rg=gbzzzz\n"}, "", "gb"},
		{"script in locale", fail, fail, reply{out: "zh-Hans_TW\n"}, "", "tw"},
		{"LANG", fail, fail, fail, "fr_FR.UTF-8", "fr"},
		{"nothing", fail, fail, reply{out: "en\n"}, "C", "us"},
	}
	for _, tc := range cases {
		run := func(_ context.Context, args ...string) (string, error) {
			var r reply
			switch strings.Join(args, " ") {
			case "read com.apple.itunescloud storefrontID":
				r = tc.top
			case "read com.apple.itunescloud ICDefaultsKeyLastKnownSubscriptionStatusBaseCacheKey":
				r = tc.nested
			case "read -g AppleLocale":
				r = tc.local
			default:
				t.Errorf("%s: unexpected defaults %v", tc.name, args)
				r = fail
			}
			return r.out, r.err
		}
		if got := detectCountry(context.Background(), run, tc.lang); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCountryNormalised(t *testing.T) {
	for in, want := range map[string]string{"CA": "ca", " gb ": "gb", "": "us", "usa": "us", "1x": "us"} {
		if got := normCountry(in); got != want {
			t.Errorf("normCountry(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLive talks to the real API (about 7 requests). Run it with
// CATALOG_LIVE=1 go test -run Live -v ./internal/catalog/
func TestLive(t *testing.T) {
	if os.Getenv("CATALOG_LIVE") != "1" {
		t.Skip("set CATALOG_LIVE=1 to hit itunes.apple.com")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cc := DetectCountry()
	t.Logf("DetectCountry() = %q", cc)
	c := &Client{Country: cc, CacheDir: t.TempDir()}

	res, err := c.Search(ctx, "radiohead", 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("search: %d artists, %d albums, %d songs; first song %q (%d)", len(res.Artists), len(res.Albums), len(res.Songs), res.Songs[0].Name, res.Songs[0].ID)
	if len(res.Artists) == 0 || len(res.Albums) == 0 || len(res.Songs) == 0 {
		t.Fatal("empty kind")
	}
	artist, albums, err := c.ArtistAlbums(ctx, 657515)
	if err != nil || len(albums) == 0 {
		t.Fatalf("ArtistAlbums: %v", err)
	}
	t.Logf("%s: %d albums, newest %q (%s)", artist.Name, len(albums), albums[0].Name, albums[0].Released.Format("2006-01-02"))
	top, err := c.ArtistTopSongs(ctx, 657515, 5)
	if err != nil || len(top) == 0 {
		t.Fatalf("ArtistTopSongs: %v", err)
	}
	t.Logf("top song %q", top[0].Name)
	album, tracks, err := c.AlbumTracks(ctx, 1097861387)
	if err != nil || len(tracks) != album.TrackCount {
		t.Fatalf("AlbumTracks: %d of %d, %v", len(tracks), album.TrackCount, err)
	}
	songs, err := c.LookupSongs(ctx, []int64{1097861834, 1097861769})
	if err != nil || len(songs) != 2 || songs[0].ID != 1097861834 {
		t.Fatalf("LookupSongs: %+v %v", songs, err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, album.ArtURL(600), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		t.Errorf("artwork 600: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

// One Search sends three requests at once. When Apple refuses all three, that
// is one refusal: the backoff is 5 s, and the next refusal after the window
// doubles it to 10 s, not 40 s.
func TestParallelRefusalsCountOnce(t *testing.T) {
	c, api, clk := newTest(t)
	ctx := context.Background()
	release := make(chan struct{})
	api.mu.Lock()
	api.status, api.block = 403, release
	api.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := c.Search(ctx, "radiohead", 5)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for api.count() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("%d of 3 requests in flight", api.count())
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	var rl *RateLimitError
	if err := <-done; !errors.As(err, &rl) || rl.Status != 403 || rl.RetryAfter != 5*time.Second {
		t.Fatalf("search: %v, want 403 retry in 5s", err)
	}
	c.lim.mu.Lock()
	strikes := c.lim.strikes
	c.lim.mu.Unlock()
	if strikes != 1 {
		t.Fatalf("strikes = %d after one search, want 1", strikes)
	}

	api.mu.Lock()
	api.block = nil
	api.mu.Unlock()
	clk.Advance(5 * time.Second)
	if _, err := c.ArtistTopSongs(ctx, 42, 5); !errors.As(err, &rl) || rl.RetryAfter != 10*time.Second {
		t.Fatalf("next refusal: %v, want retry in 10s", err)
	}

	// A late success from before the window doesn't reset the run either.
	c.lim.ok(clk.Now().Add(-time.Second))
	c.lim.mu.Lock()
	strikes = c.lim.strikes
	c.lim.mu.Unlock()
	if strikes != 2 {
		t.Fatalf("strikes = %d after a stale success, want 2", strikes)
	}
}

func TestAppearances(t *testing.T) {
	songs := []Song{
		{ID: 1, AlbumID: 10, Album: "Death Stranding (Songs from the Video Game)", AlbumArtist: "Various Artists"},
		{ID: 2, AlbumID: 20, Album: "ross."},
		{ID: 3, AlbumID: 10, Album: "Death Stranding (Songs from the Video Game)", AlbumArtist: "Various Artists"},
		{ID: 4, AlbumID: 30, Album: "Duets", AlbumArtist: "Someone Else"},
		{ID: 5, AlbumID: 40, Album: "Known", AlbumArtist: "Various Artists"},
	}
	comps, others := Appearances(songs, map[int64]bool{40: true})
	if len(comps) != 1 || comps[0].ID != 10 || comps[0].Artist != VariousArtists {
		t.Errorf("compilations = %+v", comps)
	}
	if len(others) != 1 || others[0].ID != 30 || others[0].Artist != "Someone Else" {
		t.Errorf("appears on = %+v", others)
	}
}

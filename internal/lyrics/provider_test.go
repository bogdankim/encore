package lyrics

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type stubProvider struct {
	name  string
	after time.Duration
	lyr   *Lyrics
	err   error
	calls atomic.Int32
}

func (p *stubProvider) Name() string { return p.name }

func (p *stubProvider) Lookup(ctx context.Context, q Query) (*Lyrics, error) {
	p.calls.Add(1)
	t := time.NewTimer(p.after)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
	}
	if p.lyr == nil && p.err == nil {
		return nil, ErrNotFound
	}
	return p.lyr, p.err
}

type deferredStub struct {
	*stubProvider
	delay time.Duration
}

func (d deferredStub) Delay() time.Duration { return d.delay }

func synced(src string) *Lyrics { return FromText("[00:01.00]"+src, src) }
func plainText(src string) *Lyrics {
	return &Lyrics{Plain: src + " words", Source: src}
}

func TestLookupPrefersEarlierProvider(t *testing.T) {
	ctx := context.Background()
	ms := time.Millisecond

	// Both synced: the first provider wins although it answers later.
	a := &stubProvider{name: "a", after: 100 * ms, lyr: synced("a")}
	b := &stubProvider{name: "b", after: 10 * ms, lyr: synced("b")}
	l, err := lookup(ctx, []Provider{a, b}, q1, false, nil)
	if err != nil || l.Source != "a" {
		t.Fatalf("got %+v %v, want a's", l, err)
	}

	// The first one is too slow: b's synced result is taken after the window.
	a = &stubProvider{name: "a", after: 5 * time.Second, lyr: synced("a")}
	start := time.Now()
	l, err = lookup(ctx, []Provider{a, b}, q1, false, nil)
	if took := time.Since(start); err != nil || l.Source != "b" || took > syncedWindow+300*ms {
		t.Fatalf("got %+v %v after %v, want b's within the window", l, err, took)
	}

	// Synced beats plain whatever the order; the plain one is shown first.
	a = &stubProvider{name: "a", after: 10 * ms, lyr: plainText("a")}
	b = &stubProvider{name: "b", after: 150 * ms, lyr: synced("b")}
	var shown []*Lyrics
	l, err = lookup(ctx, []Provider{a, b}, q1, false, func(l *Lyrics) { shown = append(shown, l) })
	if err != nil || l.Source != "b" || len(shown) != 1 || shown[0].Source != "a" {
		t.Fatalf("got %+v %v, interim %v", l, err, shown)
	}

	// A failure next to a plain result keeps the error, so it isn't cached.
	a = &stubProvider{name: "a", after: 10 * ms, err: &HTTPError{Status: 503}}
	b = &stubProvider{name: "b", after: 10 * ms, lyr: plainText("b")}
	l, err = lookup(ctx, []Provider{a, b}, q1, false, nil)
	var he *HTTPError
	if l == nil || l.Source != "b" || !errors.As(err, &he) {
		t.Fatalf("got %+v %v", l, err)
	}
}

func TestLookupDeferredProvider(t *testing.T) {
	ctx := context.Background()
	ms := time.Millisecond

	// The first provider has synced lyrics in time: the deferred one isn't asked.
	a := &stubProvider{name: "a", after: 20 * ms, lyr: synced("a")}
	m := &stubProvider{name: "m", lyr: plainText("m")}
	if l, _ := lookup(ctx, []Provider{a, deferredStub{m, time.Second}}, q1, false, nil); l.Source != "a" || m.calls.Load() != 0 {
		t.Fatalf("got %+v, %d deferred calls", l, m.calls.Load())
	}

	// The first one misses: the deferred one starts at once, not after its delay.
	a = &stubProvider{name: "a", after: 20 * ms}
	start := time.Now()
	l, err := lookup(ctx, []Provider{a, deferredStub{m, time.Second}}, q1, false, nil)
	if took := time.Since(start); err != nil || l.Source != "m" || took > 500*ms {
		t.Fatalf("got %+v %v after %v", l, err, took)
	}

	// The first one is slow: the deferred one starts after its delay and its
	// plain lyrics are shown while the first keeps looking.
	a = &stubProvider{name: "a", after: 400 * ms, lyr: synced("a")}
	var shownAt time.Duration
	start = time.Now()
	l, _ = lookup(ctx, []Provider{a, deferredStub{m, 100 * ms}}, q1, false, func(*Lyrics) { shownAt = time.Since(start) })
	if l.Source != "a" || shownAt < 100*ms || shownAt > 350*ms {
		t.Fatalf("got %+v, plain shown at %v", l, shownAt)
	}

	// A prefetch doesn't ask the deferred one while the first is still looking.
	m.calls.Store(0)
	if l, _ = lookup(ctx, []Provider{a, deferredStub{m, 100 * ms}}, q1, true, nil); l.Source != "a" || m.calls.Load() != 0 {
		t.Fatalf("prefetch: got %+v, %d deferred calls", l, m.calls.Load())
	}
}

func TestNextShowsPlainThenSynced(t *testing.T) {
	slow := &stubProvider{name: SourceLRCLIB, after: 200 * time.Millisecond, lyr: synced(SourceLRCLIB)}
	fast := &stubProvider{name: SourceMusic, after: 5 * time.Millisecond, lyr: plainText(SourceMusic)}
	s := NewService(Options{CacheDir: t.TempDir(), Providers: []Provider{slow, fast}, Now: newClock().Now})
	defer s.Close()
	ctx := context.Background()

	r := s.Next(ctx, q1, nil)
	if r.Final || r.Lyrics == nil || r.Lyrics.Source != SourceMusic {
		t.Fatalf("want interim plain, got %+v", r)
	}
	r = s.Next(ctx, q1, r.Lyrics)
	if !r.Final || r.Err != nil || !r.Lyrics.Synced {
		t.Fatalf("want final synced, got %+v", r)
	}
	if l, err := s.Get(ctx, q1); err != nil || !l.Synced || slow.calls.Load() != 1 {
		t.Fatalf("synced result should be cached: %+v %v, %d calls", l, err, slow.calls.Load())
	}
}

func TestNextStaleCacheFirst(t *testing.T) {
	s, f, clk := newTestService(t, nil)
	ctx := context.Background()
	f.get["Song|Artist|Album"] = &Record{ID: 1, PlainLyrics: str("old plain")}
	s.Get(ctx, q1)
	clk.Add(8 * 24 * time.Hour) // plain is stale: look again
	f.delay = 300 * time.Millisecond
	f.get["Song|Artist|Album"] = &Record{ID: 2, SyncedLyrics: str("[00:01.00]new")}

	start := time.Now()
	r := s.Next(ctx, q1, nil)
	if r.Final || r.Lyrics.Plain != "old plain" || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("want the stale entry at once, got %+v after %v", r, time.Since(start))
	}
	if r = s.Next(ctx, q1, r.Lyrics); !r.Final || !r.Lyrics.Synced {
		t.Fatalf("want synced upgrade, got %+v", r)
	}
}

func TestFindHedgesSlowExactGet(t *testing.T) {
	f, c := newFake(t)
	f.slow = map[string]time.Duration{"Get Lucky (feat. X)|Daft Punk|RAM": 800 * time.Millisecond}
	f.get["Get Lucky|Daft Punk|"] = &Record{ID: 7, SyncedLyrics: str("[00:01.00]hi")}
	start := time.Now()
	r, err := c.Find(context.Background(), "Get Lucky (feat. X)", "Daft Punk", "RAM", 369)
	if took := time.Since(start); err != nil || r.ID != 7 || took < cleanHedge || took > 700*time.Millisecond {
		t.Fatalf("Find = %+v %v after %v", r, err, took)
	}

	// No hedging while LRCLIB is overloaded: the cleaned get waits for the
	// exact one.
	c.backoff(http.StatusServiceUnavailable, 0)
	start = time.Now()
	r, err = c.Find(context.Background(), "Get Lucky (feat. X)", "Daft Punk", "RAM", 369)
	if took := time.Since(start); err != nil || r.ID != 7 || took < 800*time.Millisecond {
		t.Fatalf("busy Find = %+v %v after %v", r, err, took)
	}
}

func TestFindStopsWhenLRCLIBFails(t *testing.T) {
	f, c := newFake(t)
	f.status, f.retry = http.StatusServiceUnavailable, "0"
	_, err := c.Find(context.Background(), "Song", "Artist", "Album", 200)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 503 {
		t.Fatalf("want 503, got %v", err)
	}
	// the exact get, tried maxAttempts times; no cleaned get, no search
	if got := f.requests(); len(got) != maxAttempts {
		t.Fatalf("requests: %v", got)
	}
}

func TestBackoffHoldsOtherRequests(t *testing.T) {
	_, c := newFake(t)
	c.backoff(http.StatusTooManyRequests, 200*time.Millisecond)
	start := time.Now()
	if _, err := c.Get(context.Background(), "a", "b", "", 100); !errors.Is(err, ErrNotFound) || time.Since(start) < 200*time.Millisecond {
		t.Fatalf("request didn't wait out the backoff: %v after %v", err, time.Since(start))
	}
	c.backoff(http.StatusTooManyRequests, time.Minute)
	start = time.Now()
	_, err := c.Get(context.Background(), "a", "b", "", 100)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 429 || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("a long backoff should fail fast, got %v", err)
	}
}

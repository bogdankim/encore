package lyrics

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type musicStub struct {
	calls atomic.Int32
	text  string
	err   error
}

func (m *musicStub) lyrics(ctx context.Context, pid string) (string, error) {
	m.calls.Add(1)
	return m.text, m.err
}

var q1 = Query{PID: "AAAA", Title: "Song", Artist: "Artist", Album: "Album", Duration: 200 * time.Second}

func newTestService(t *testing.T, music *musicStub) (*Service, *fakeLRCLIB, *fakeClock) {
	f, c := newFake(t)
	clk := newClock()
	opts := Options{CacheDir: t.TempDir(), LRCLIB: c, Now: clk.Now}
	if music != nil {
		opts.MusicLyrics = music.lyrics
	}
	s := NewService(opts)
	t.Cleanup(s.Close)
	return s, f, clk
}

func TestChainOrder(t *testing.T) {
	music := &musicStub{text: "music words"}
	s, f, _ := newTestService(t, music)
	ctx := context.Background()

	demo := map[string]string{"DEMO": "[00:01.00]demo line"}
	s.opts.Demo = func(pid string) string { return demo[pid] }
	l, err := s.Get(ctx, Query{PID: "DEMO", Title: "X", Artist: "Y"})
	if err != nil || l.Source != SourceDemo || !l.Synced {
		t.Fatalf("demo: %+v %v", l, err)
	}
	if len(f.requests()) != 0 || music.calls.Load() != 0 {
		t.Fatal("demo hit must not reach other providers")
	}

	// Demo has nothing for q1, LRCLIB misses: Music answers.
	l, err = s.Get(ctx, q1)
	if err != nil || l.Source != SourceMusic || l.Plain != "music words" {
		t.Fatalf("music fallback: %+v %v", l, err)
	}
	if len(f.requests()) == 0 || music.calls.Load() != 1 {
		t.Fatalf("want LRCLIB then Music, got %v requests, %d music calls", f.requests(), music.calls.Load())
	}

	// LRCLIB synced wins without asking Music.
	q2 := Query{PID: "BBBB", Title: "Hit", Artist: "Band", Duration: 180 * time.Second}
	f.get["Hit|Band|"] = &Record{ID: 9, SyncedLyrics: str("[00:01.00]one\n[00:02.00]two"), PlainLyrics: str("one\ntwo")}
	l, err = s.Get(ctx, q2)
	if err != nil || l.Source != SourceLRCLIB || !l.Synced || len(l.Lines) != 2 || l.LRCLIBID != 9 {
		t.Fatalf("lrclib: %+v %v", l, err)
	}
	if music.calls.Load() != 1 {
		t.Fatal("Music asked although LRCLIB had synced lyrics")
	}

	// Nothing anywhere.
	music.text = ""
	if _, err := s.Get(ctx, Query{PID: "CCCC", Title: "None", Artist: "Nobody"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestInstrumental(t *testing.T) {
	s, f, _ := newTestService(t, nil)
	f.get["Song|Artist|Album"] = &Record{ID: 3, Instrumental: true}
	l, err := s.Get(context.Background(), q1)
	if err != nil || !l.Instrumental || l.Synced {
		t.Fatalf("got %+v %v", l, err)
	}
	f.get = map[string]*Record{}
	if l, err := s.Get(context.Background(), q1); err != nil || !l.Instrumental {
		t.Fatalf("instrumental should come from cache: %+v %v", l, err)
	}
}

func TestCacheTTL(t *testing.T) {
	ctx := context.Background()
	day := 24 * time.Hour

	t.Run("synced kept forever", func(t *testing.T) {
		s, f, clk := newTestService(t, nil)
		f.get["Song|Artist|Album"] = &Record{ID: 1, SyncedLyrics: str("[00:01.00]a")}
		s.Get(ctx, q1)
		n := len(f.requests())
		clk.Add(400 * day)
		l, err := s.Get(ctx, q1)
		if err != nil || !l.Synced || len(f.requests()) != n {
			t.Fatalf("refetched synced lyrics: %v %v", f.requests(), err)
		}
	})

	t.Run("plain retried after 7 days", func(t *testing.T) {
		s, f, clk := newTestService(t, nil)
		f.get["Song|Artist|Album"] = &Record{ID: 1, PlainLyrics: str("plain")}
		s.Get(ctx, q1)
		n := len(f.requests())
		clk.Add(6 * day)
		if l, _ := s.Get(ctx, q1); l == nil || l.Plain != "plain" || len(f.requests()) != n {
			t.Fatal("plain should be fresh for 7 days")
		}
		f.get["Song|Artist|Album"] = &Record{ID: 2, SyncedLyrics: str("[00:01.00]now synced")}
		clk.Add(2 * day)
		if l, _ := s.Get(ctx, q1); l == nil || !l.Synced {
			t.Fatalf("want synced upgrade after 7 days, got %+v", l)
		}
	})

	t.Run("not found: 24 h, then 7 days", func(t *testing.T) {
		s, f, clk := newTestService(t, nil)
		s.Get(ctx, q1)
		n := len(f.requests())
		clk.Add(23 * time.Hour)
		if _, err := s.Get(ctx, q1); !errors.Is(err, ErrNotFound) || len(f.requests()) != n {
			t.Fatal("first miss should be cached for 24 h")
		}
		clk.Add(2 * time.Hour)
		s.Get(ctx, q1)
		if len(f.requests()) == n {
			t.Fatal("first miss should expire after 24 h")
		}
		n = len(f.requests())
		clk.Add(6 * day)
		if _, err := s.Get(ctx, q1); !errors.Is(err, ErrNotFound) || len(f.requests()) != n {
			t.Fatal("repeated miss should be cached for 7 days")
		}
		clk.Add(2 * day)
		f.get["Song|Artist|Album"] = &Record{ID: 1, SyncedLyrics: str("[00:01.00]found")}
		if l, err := s.Get(ctx, q1); err != nil || !l.Synced {
			t.Fatalf("want lyrics after 7 days, got %v", err)
		}
	})

	t.Run("transient errors only in memory for 5 minutes", func(t *testing.T) {
		s, f, clk := newTestService(t, nil)
		f.status = http.StatusInternalServerError
		_, err := s.Get(ctx, q1)
		var he *HTTPError
		if !errors.As(err, &he) || errors.Is(err, ErrNotFound) {
			t.Fatalf("want HTTP error, got %v", err)
		}
		n := len(f.requests())
		clk.Add(4 * time.Minute)
		if _, err2 := s.Get(ctx, q1); err2 == nil || len(f.requests()) != n {
			t.Fatal("transient error should be remembered for 5 minutes")
		}
		entries, _ := os.ReadDir(s.opts.CacheDir)
		if len(entries) != 0 {
			t.Fatalf("transient error reached the disk cache: %v", entries)
		}
		f.status = 0
		f.get["Song|Artist|Album"] = &Record{ID: 1, SyncedLyrics: str("[00:01.00]back")}
		clk.Add(2 * time.Minute)
		if l, err := s.Get(ctx, q1); err != nil || !l.Synced {
			t.Fatalf("want retry after 5 minutes, got %v", err)
		}
	})

	t.Run("stale plain survives an outage", func(t *testing.T) {
		s, f, clk := newTestService(t, nil)
		f.get["Song|Artist|Album"] = &Record{ID: 1, PlainLyrics: str("plain")}
		s.Get(ctx, q1)
		clk.Add(8 * day)
		f.status, f.retry = http.StatusServiceUnavailable, "60"
		if l, err := s.Get(ctx, q1); err != nil || l.Plain != "plain" {
			t.Fatalf("want stale plain, got %+v %v", l, err)
		}
	})
}

func TestEnablingLRCLIBRetriesCachedMiss(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s := NewService(Options{CacheDir: dir, Now: clk.Now})
	if _, err := s.Get(context.Background(), q1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	s.Close()
	f, c := newFake(t)
	f.get["Song|Artist|Album"] = &Record{ID: 1, SyncedLyrics: str("[00:01.00]a")}
	s = NewService(Options{CacheDir: dir, LRCLIB: c, Now: clk.Now})
	defer s.Close()
	if l, err := s.Get(context.Background(), q1); err != nil || !l.Synced {
		t.Fatalf("cached miss without LRCLIB should be retried once it's on: %v", err)
	}
}

func TestDedupe(t *testing.T) {
	s, f, _ := newTestService(t, nil)
	f.delay = 100 * time.Millisecond
	f.get["Song|Artist|Album"] = &Record{ID: 1, SyncedLyrics: str("[00:01.00]a")}
	var wg sync.WaitGroup
	results := make([]*Lyrics, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = s.Get(context.Background(), q1)
		}()
	}
	wg.Wait()
	if got := f.requests(); len(got) != 1 {
		t.Fatalf("want one shared request, got %v", got)
	}
	for i, r := range results {
		if r == nil || r != results[0] {
			t.Fatalf("result %d not shared: %v", i, r)
		}
	}
}

func TestCancelledGetStillFillsCache(t *testing.T) {
	s, f, _ := newTestService(t, nil)
	f.delay = 100 * time.Millisecond
	f.get["Song|Artist|Album"] = &Record{ID: 1, SyncedLyrics: str("[00:01.00]a")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Get(ctx, q1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
	waitFor(t, func() bool { _, ok := s.load(q1.key()); return ok })
	f.delay = 0
	n := len(f.requests())
	if l, err := s.Get(context.Background(), q1); err != nil || !l.Synced || len(f.requests()) != n {
		t.Fatal("abandoned lookup should have filled the cache")
	}
}

func TestPrefetch(t *testing.T) {
	s, f, _ := newTestService(t, nil)
	f.get["Song|Artist|Album"] = &Record{ID: 1, SyncedLyrics: str("[00:01.00]a")}
	q2 := Query{PID: "BBBB", Title: "Two", Artist: "Artist", Duration: 100 * time.Second}
	f.get["Two|Artist|"] = &Record{ID: 2, SyncedLyrics: str("[00:01.00]b")}
	s.Prefetch(Query{PID: "OLD", Title: "Old", Artist: "Artist"})
	s.Prefetch(q1, q2) // replaces the pending one
	waitFor(t, func() bool { _, ok1 := s.load(q1.key()); _, ok2 := s.load(q2.key()); return ok1 && ok2 })
	for _, r := range f.requests() {
		if r[:7] == "get Old" {
			t.Fatalf("superseded prefetch ran: %v", f.requests())
		}
	}
	n := len(f.requests())
	if l, err := s.Get(context.Background(), q1); err != nil || !l.Synced || len(f.requests()) != n {
		t.Fatal("prefetched lyrics should be cached")
	}
}

func TestOffsets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "lyrics-offsets.json")
	o := LoadOffsets(path)
	if o.Get("A") != 0 {
		t.Fatal("empty store")
	}
	o.Set("A", 300*time.Millisecond)
	o.Set("B", -1200*time.Millisecond)
	o.Set("C", 100*time.Millisecond)
	o.Set("C", 0)
	if o.Get("A") != 300*time.Millisecond {
		t.Fatal("Set/Get")
	}
	if err := o.Flush(); err != nil {
		t.Fatal(err)
	}
	o2 := LoadOffsets(path)
	if o2.Get("A") != 300*time.Millisecond || o2.Get("B") != -1200*time.Millisecond || o2.Get("C") != 0 {
		t.Fatalf("reload: %v", o2.ms)
	}
	// The debounced save also lands without Flush.
	o2.Set("D", time.Second)
	waitFor(t, func() bool { return LoadOffsets(path).Get("D") == time.Second })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

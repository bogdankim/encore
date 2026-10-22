package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/ui"
)

func TestRefresherDebouncesAndSwaps(t *testing.T) {
	f := bridge.NewFakeWithClock(1, time.Now)
	defer f.Close()
	var cur atomic.Pointer[library.Library]
	cur.Store(library.Build(&bridge.Snapshot{}))

	var mu sync.Mutex
	var got []tea.Msg
	r := newRefresher(f, t.TempDir()+"/library.cache", &cur)
	r.debounce = 30 * time.Millisecond
	r.send = func(m tea.Msg) { mu.Lock(); got = append(got, m); mu.Unlock() }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx, false)
	for i := 0; i < 5; i++ {
		r.trigger()
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("%d refreshes for one burst, want 1", len(got))
	}
	msg, ok := got[0].(ui.LibraryMsg)
	if !ok || msg.Lib == nil || msg.Index == nil || cur.Load() != msg.Lib || len(msg.Lib.Songs) == 0 {
		t.Fatalf("bad refresh: %#v", got[0])
	}
	if _, err := library.LoadCache(r.cachePath); err != nil {
		t.Fatalf("cache not saved: %v", err)
	}
}

func TestRefresherSpacesRefreshesByTheLastOne(t *testing.T) {
	f := bridge.NewFakeWithClock(1, time.Now)
	defer f.Close()
	var cur atomic.Pointer[library.Library]
	cur.Store(library.Build(&bridge.Snapshot{}))

	var n atomic.Int32
	r := newRefresher(f, "", &cur)
	r.debounce = 5 * time.Millisecond
	r.gapFactor = 2
	r.lastTook, r.lastAt = 100*time.Millisecond, time.Now() // a slow refresh just finished
	r.send = func(tea.Msg) { n.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx, false)
	r.trigger()
	time.Sleep(120 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatal("refreshed inside the gap")
	}
	time.Sleep(250 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("%d refreshes after the gap, want 1", n.Load())
	}
}

// failingClient is a bridge client whose snapshots fail.
type failingClient struct{ bridge.Client }

func (failingClient) Snapshot(context.Context) (*bridge.Snapshot, error) {
	return nil, context.DeadlineExceeded
}

func TestFailedRefreshDoesNotHoldBackTheNext(t *testing.T) {
	var cur atomic.Pointer[library.Library]
	r := newRefresher(failingClient{}, "", &cur)
	r.send = func(tea.Msg) {}
	r.refresh(context.Background())
	if wait := r.untilNext(time.Now()); wait > 0 {
		t.Fatalf("a failed refresh made the next wait %v", wait)
	}
}

func TestRefreshGapIsCapped(t *testing.T) {
	r := newRefresher(nil, "", nil)
	now := time.Now()
	r.lastAt, r.lastTook = now, 10*time.Minute
	if wait := r.untilNext(now); wait != refreshGapMax {
		t.Fatalf("wait %v, want the cap %v", wait, refreshGapMax)
	}
}

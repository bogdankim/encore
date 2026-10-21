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

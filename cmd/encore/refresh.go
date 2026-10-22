package main

import (
	"context"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/ui"
)

// libraryDebounce is how long the library must stay quiet before a
// refresh: Music reports changes in bursts.
const libraryDebounce = 5 * time.Second

// refreshGapFactor spaces refreshes out by this many times what the last one
// took, so a big library (a snapshot takes about 87 µs a track in the bridge,
// 9 s at 100,000 tracks, and then a decode and a rebuild here) isn't read
// again every few minutes because Music rewrote its database for a play
// count. A 5,000-track library, at 0.7 s a refresh, waits 14 s.
const refreshGapFactor = 20

// refreshGapMax caps that wait.
const refreshGapMax = 5 * time.Minute

// refresher takes library snapshots in the background, saves them to the
// cache and hands the UI a new library.
type refresher struct {
	client    bridge.Client
	cachePath string // "" keeps no cache (demo)
	current   *atomic.Pointer[library.Library]
	send      func(tea.Msg)
	kick      chan struct{}
	debounce  time.Duration

	gapFactor int           // refreshGapFactor
	lastAt    time.Time     // when the last refresh finished
	lastTook  time.Duration // how long it took
}

func newRefresher(c bridge.Client, cachePath string, cur *atomic.Pointer[library.Library]) *refresher {
	return &refresher{client: c, cachePath: cachePath, current: cur, kick: make(chan struct{}, 1), debounce: libraryDebounce, gapFactor: refreshGapFactor}
}

// trigger asks for a refresh once things settle. It never blocks.
func (r *refresher) trigger() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// run refreshes on triggers, debounced; with now it refreshes at once first.
func (r *refresher) run(ctx context.Context, now bool) {
	if now {
		r.refresh(ctx)
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
			timer.Reset(r.debounce)
		case <-timer.C:
			if wait := r.untilNext(time.Now()); wait > 0 {
				timer.Reset(wait)
				continue
			}
			r.refresh(ctx)
		}
	}
}

// untilNext is how long a refresh asked for now has to wait for the gap
// after the last one.
func (r *refresher) untilNext(now time.Time) time.Duration {
	return r.lastAt.Add(min(time.Duration(r.gapFactor)*r.lastTook, refreshGapMax)).Sub(now)
}

func (r *refresher) refresh(ctx context.Context) {
	start := time.Now()
	sctx, cancel := context.WithTimeout(ctx, bridge.SnapshotTimeout)
	defer cancel()
	snap, err := r.client.Snapshot(sctx)
	if err != nil {
		if ctx.Err() == nil {
			r.send(ui.StatusMsg{Text: "Couldn't read the library: " + err.Error(), Err: true})
		}
		return
	}
	if r.cachePath != "" {
		if err := library.SaveCache(r.cachePath, snap); err != nil {
			r.send(ui.StatusMsg{Text: "Couldn't save the library cache: " + err.Error(), Err: true})
		}
	}
	lib := library.Build(snap)
	idx := search.NewIndex(lib)
	r.current.Store(lib)
	r.send(ui.LibraryMsg{Lib: lib, Index: idx})
	// Only a refresh that worked spaces the next one out: a snapshot that
	// timed out at 30 s must not hold retries off for 10 minutes.
	r.lastAt, r.lastTook = time.Now(), time.Since(start)
}

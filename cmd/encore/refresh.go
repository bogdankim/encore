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

// refresher takes library snapshots in the background, saves them to the
// cache and hands the UI a new library.
type refresher struct {
	client    bridge.Client
	cachePath string // "" keeps no cache (demo)
	current   *atomic.Pointer[library.Library]
	send      func(tea.Msg)
	kick      chan struct{}
	debounce  time.Duration
}

func newRefresher(c bridge.Client, cachePath string, cur *atomic.Pointer[library.Library]) *refresher {
	return &refresher{client: c, cachePath: cachePath, current: cur, kick: make(chan struct{}, 1), debounce: libraryDebounce}
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
			r.refresh(ctx)
		}
	}
}

func (r *refresher) refresh(ctx context.Context) {
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
}

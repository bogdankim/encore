package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

// countingClient counts the artwork requests that reach the bridge.
type countingClient struct {
	bridge.Client
	calls atomic.Int64
}

func (c *countingClient) Artwork(ctx context.Context, pid string, size int) (bridge.Artwork, error) {
	c.calls.Add(1)
	return c.Client.Artwork(ctx, pid, size)
}

// Tiles that were drawn once keep drawing from their encoded payloads after
// the renderer let go of the decoded images, without another request to the
// bridge or another decode.
func TestGridTilesSurviveEvictedImages(t *testing.T) {
	t.Setenv("TMUX", "")
	lib := shellLibrary(t)
	f := bridge.NewFakeWithClock(1, time.Now)
	cl := &countingClient{Client: f}
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	r := art.NewRenderer(art.Options{Protocol: art.ProtoIIP})
	a := New(Options{
		Client: cl, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th }, Art: r,
	})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 42})
	a.nav.open(a.section(KeyRecent))
	waitFor(t, func() bool {
		a.View()
		return len(tilePlaces(a)) >= 10
	})
	// let the rest of the visible tiles, the rows prefetched beyond them and
	// the player bar finish
	for range 3 {
		a.art.idle(t)
		a.View()
	}
	a.art.idle(t)
	// A tile cut off by the grid's edge is cut from the decoded image, so it
	// goes back to half blocks until that is loaded again. The others don't.
	whole := func() (keys []string, cut int) {
		for _, p := range tilePlaces(a) {
			if p.crop.Empty() || p.crop == p.box {
				keys = append(keys, p.key)
			} else {
				cut++
			}
		}
		return keys, cut
	}
	want, cut := whole()
	if len(want) < 5 || cut == 0 {
		t.Fatalf("%d whole tiles and %d cut; the test needs both", len(want), cut)
	}
	calls := cl.calls.Load()

	// push every decoded image out of the renderer's cache
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})
	png.Encode(&buf, img)
	for i := range 200 {
		if err := r.Prepare(fmt.Sprintf("evict%d", i), buf.Bytes(), 2, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range want {
		if r.Has(k) {
			t.Fatalf("%s is still decoded; the test proves nothing", k)
		}
	}

	a.View()
	got, _ := whole()
	if len(got) != len(want) {
		t.Fatalf("%d whole tile images after the decoded ones were evicted, %d before", len(got), len(want))
	}
	time.Sleep(100 * time.Millisecond)
	if n := cl.calls.Load(); n-calls > int64(cut) {
		t.Fatalf("%d more artwork requests, only the %d cut tiles needed one", n-calls, cut)
	}
	// and the cut ones come back as images
	waitFor(t, func() bool {
		a.View()
		_, c := whole()
		return len(tilePlaces(a)) == len(want)+cut && c == cut
	})
}

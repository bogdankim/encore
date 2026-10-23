package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
)

// scriptedClient answers artwork requests from a script, one step per
// request, and the last step for every request after that.
type scriptedClient struct {
	bridge.Client
	mu    sync.Mutex
	steps []func() (bridge.Artwork, error)
	calls int
}

func (c *scriptedClient) Artwork(ctx context.Context, pid string, size int) (bridge.Artwork, error) {
	c.mu.Lock()
	i := min(c.calls, len(c.steps)-1)
	c.calls++
	step := c.steps[i]
	c.mu.Unlock()
	return step()
}

func (c *scriptedClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func writeCover(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.Set(x, y, color.NRGBA{R: uint8(x * 8), G: uint8(y * 8), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var (
	stepFail = func() (bridge.Artwork, error) { return bridge.Artwork{}, errors.New("bridge: timed out") }
	stepNone = func() (bridge.Artwork, error) { return bridge.Artwork{}, nil }
)

// storeClock drives an artStore's clock and its wake-ups by hand.
type storeClock struct {
	mu    sync.Mutex
	t     time.Time
	wakes []time.Duration
}

func (c *storeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *storeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func (c *storeClock) after(d time.Duration, f func()) {
	c.mu.Lock()
	c.wakes = append(c.wakes, d)
	c.mu.Unlock()
	f()
}

func (c *storeClock) lastWake() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.wakes) == 0 {
		return 0
	}
	return c.wakes[len(c.wakes)-1]
}

func newScriptedStore(t *testing.T, steps ...func() (bridge.Artwork, error)) (*artStore, *scriptedClient, *storeClock) {
	t.Helper()
	cl := &scriptedClient{steps: steps}
	clk := &storeClock{t: time.Unix(1000, 0)}
	s := newArtStore(cl, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	s.now, s.after = clk.now, clk.after
	return s, cl, clk
}

// idle waits until no load is queued or under way.
func (s *artStore) idle(t *testing.T) {
	t.Helper()
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.queue) == 0 && len(s.busy) == 0
	})
}

func drain(s *artStore) {
	select {
	case <-s.ready:
	default:
	}
}

// A tile whose load failed (the bridge timed out, or was busy reloading its
// index) asks again by itself after a short wait, and is not blank for the
// half minute it used to be: the store wakes the UI when the retry is due.
func TestTileRetriesAFailedLoadByItself(t *testing.T) {
	cover := writeCover(t)
	s, cl, clk := newScriptedStore(t, stepFail, stepFail, func() (bridge.Artwork, error) {
		return bridge.Artwork{Path: cover, W: 32, H: 32}, nil
	})
	if _, ok := s.block("k", "pid", 10, 5, ArtTile); ok {
		t.Fatal("block was ready before any load")
	}
	s.idle(t)
	if got := clk.lastWake(); got != tileRetryFirst {
		t.Fatalf("woke after %v, want the first retry after %v", got, tileRetryFirst)
	}
	select {
	case <-s.ready:
	case <-time.After(time.Second):
		t.Fatal("the failure did not wake the UI for the retry")
	}

	// before the retry is due the tile waits, with no request
	clk.add(tileRetryFirst - time.Millisecond)
	s.block("k", "pid", 10, 5, ArtTile)
	if cl.count() != 1 {
		t.Fatalf("%d requests inside the retry time, want 1", cl.count())
	}
	// due: asked again; failing twice doubles the wait
	clk.add(2 * time.Millisecond)
	s.block("k", "pid", 10, 5, ArtTile)
	s.idle(t)
	if cl.count() != 2 {
		t.Fatalf("%d requests after the retry was due, want 2", cl.count())
	}
	if got := clk.lastWake(); got != 2*tileRetryFirst {
		t.Fatalf("second wake after %v, want %v", got, 2*tileRetryFirst)
	}
	clk.add(2*tileRetryFirst + time.Millisecond)
	s.block("k", "pid", 10, 5, ArtTile)
	s.idle(t)
	b, ok := s.block("k", "pid", 10, 5, ArtTile)
	if !ok || b == "" {
		t.Fatal("the tile did not load after the bridge recovered")
	}
	if len(s.failed) != 0 {
		t.Fatalf("failures kept after a success: %v", s.failed)
	}
}

// Many failures wait at most artRetryAfter between tries.
func TestTileRetryWaitIsCapped(t *testing.T) {
	var f failure
	for n := 1; n < 40; n++ {
		f.n = n
		if d := f.retry(false); d > artRetryAfter || d < tileRetryFirst {
			t.Fatalf("retry after %d failures is %v", n, d)
		}
	}
	f.n = 40
	if f.retry(false) != artRetryAfter {
		t.Fatalf("retry after many failures is %v, want %v", f.retry(false), artRetryAfter)
	}
	if f.retry(true) != heroRetryAfter {
		t.Fatalf("hero retry is %v, want %v", f.retry(true), heroRetryAfter)
	}
}

// "No artwork" for a tile is asked again soon, and a later answer with the
// cover replaces it: a bridge that could not tell yet (its library index
// reloading, a snapshot still being read) must not leave the tile blank for
// ten minutes.
func TestTileNoneIsCorrectedByALaterAnswer(t *testing.T) {
	cover := writeCover(t)
	s, cl, clk := newScriptedStore(t, stepNone, func() (bridge.Artwork, error) {
		return bridge.Artwork{Path: cover, W: 32, H: 32}, nil
	})
	s.block("k", "pid", 10, 5, ArtTile)
	s.idle(t)
	if !s.answeredNone("pid") {
		t.Fatal("the none answer was not recorded")
	}
	if got := clk.lastWake(); got != tileNoneRetry {
		t.Fatalf("woke after %v, want %v", got, tileNoneRetry)
	}
	clk.add(tileNoneRetry - time.Second)
	s.block("k", "pid", 10, 5, ArtTile)
	if cl.count() != 1 {
		t.Fatal("asked again before the retry was due")
	}
	clk.add(2 * time.Second)
	s.block("k", "pid", 10, 5, ArtTile)
	s.idle(t)
	if _, ok := s.block("k", "pid", 10, 5, ArtTile); !ok {
		t.Fatal("the tile did not load from the later answer")
	}
	if s.answeredNone("pid") {
		t.Fatal("the none answer outlived the cover")
	}
}

// An album that really has no cover costs two more requests, then stands for
// noneTTL.
func TestTileNoneSettlesAfterAFewAnswers(t *testing.T) {
	s, cl, clk := newScriptedStore(t, stepNone)
	ask := func() {
		s.block("k", "pid", 10, 5, ArtTile)
		s.idle(t)
	}
	ask()
	clk.add(tileNoneRetry + time.Second)
	ask()
	if got := clk.lastWake(); got != tileNoneRetryLater {
		t.Fatalf("second answer woke after %v, want %v", got, tileNoneRetryLater)
	}
	clk.add(tileNoneRetryLater + time.Second)
	ask()
	if got := clk.lastWake(); got != noneTTL {
		t.Fatalf("third answer woke after %v, want %v", got, noneTTL)
	}
	n := cl.count()
	clk.add(noneTTL / 2)
	ask()
	if cl.count() != n {
		t.Fatal("asked again inside the TTL")
	}
	clk.add(noneTTL)
	ask()
	if cl.count() != n+1 {
		t.Fatal("not asked again after the TTL")
	}
}

// A cover file the cache trimming removed between the bridge's answer and the
// read is asked for again, not a failed load.
func TestLoadBridgeAsksAgainWhenTheFileIsGone(t *testing.T) {
	cover := writeCover(t)
	gone := filepath.Join(t.TempDir(), "gone.jpg")
	s, cl, _ := newScriptedStore(t,
		func() (bridge.Artwork, error) { return bridge.Artwork{Path: gone}, nil },
		func() (bridge.Artwork, error) { return bridge.Artwork{Path: cover}, nil })
	data, none, err := s.loadBridge("pid", 256)
	if err != nil || none || len(data) == 0 {
		t.Fatalf("loadBridge = %d bytes, none %v, err %v", len(data), none, err)
	}
	if cl.count() != 2 {
		t.Fatalf("%d requests, want 2", cl.count())
	}
	// a file that stays gone is an error, after one more try
	s2, cl2, _ := newScriptedStore(t, func() (bridge.Artwork, error) { return bridge.Artwork{Path: gone}, nil })
	if _, _, err := s2.loadBridge("pid", 256); err == nil {
		t.Fatal("a missing file loaded")
	}
	if cl2.count() != 2 {
		t.Fatalf("%d requests for a file that stays gone, want 2", cl2.count())
	}
}

// artIdle waits until the app's art store has nothing queued or under way.
func artIdle(t *testing.T, a *App) { a.art.idle(t) }

// tilesDrawn runs a frame and counts the grid tiles it placed as images.
func tilesDrawn(a *App) int {
	a.View()
	return len(tilePlaces(a))
}

// Covers load again when the renderer let go of them: the decoded images and
// the encoded payloads both gone, as after a long scroll or a cell-size
// change, nothing in the store still thinks they are ready.
func TestGridTilesReloadWhenImagesAndPayloadsAreGone(t *testing.T) {
	t.Setenv("TMUX", "")
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.nav.open(a.section(KeyRecent))
	waitFor(t, func() bool { return tilesDrawn(a) >= 10 })
	artIdle(t, a)
	want := tilesDrawn(a)

	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	png.Encode(&buf, img)
	for i := range 200 {
		if err := a.artR.Prepare(fmt.Sprintf("evict%d", i), buf.Bytes(), 2, 1); err != nil {
			t.Fatal(err)
		}
	}
	// a new cell size drops every encoded payload and scaled box
	a.artR.SetCellSize(art.CellSize{W: 11, H: 23})
	a.cell = art.CellSize{W: 11, H: 23}
	a.art.cellSizeChanged()

	if got := tilesDrawn(a); got >= want {
		t.Fatalf("%d tile images straight after the reset, %d before: the test proves nothing", got, want)
	}
	waitFor(t, func() bool {
		select {
		case <-a.art.ready:
		default:
		}
		return tilesDrawn(a) >= want
	})
}

// Art that finishes while a key repeats is held for the frame cap, and the
// flush shows it: the tile is not lost with the held frame.
func TestArtArrivingDuringARepeatShowsAtTheFlush(t *testing.T) {
	t.Setenv("TMUX", "")
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.o.FrameCap = 33 * time.Millisecond
	a.nav.open(a.section(KeyRecent))
	first := a.View().Content // asks for every visible tile
	artIdle(t, a)
	if len(tilePlaces(a)) != 0 {
		t.Fatal("tiles were images before they loaded")
	}

	now := time.Now()
	a.gate.lastIn, a.gate.repeat, a.gate.rendered = now, now, now
	_, cmd := a.Update(artMsg{})
	if cmd == nil || !a.gate.held {
		t.Fatal("art arriving mid-burst was not held for the cap")
	}
	if held := a.View().Content; held != first {
		t.Fatal("the held frame was rebuilt")
	}
	// the flush the gate asked for builds the frame with the arrived art
	a.Update(frameFlushMsg{})
	a.View()
	if n := len(tilePlaces(a)); n < 10 {
		t.Fatalf("%d tile images after the flush, want the loaded tiles", n)
	}
}

// Art asked for ahead of need queues behind everything on screen, in any
// frame, and does not wake the UI when it arrives; the screen asking for the
// same art raises it.
func TestPrefetchQueuesBehindTheScreenAndStaysQuiet(t *testing.T) {
	cl := &scriptedClient{steps: []func() (bridge.Artwork, error){stepNone}}
	s := newArtStore(cl, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	s.started = true // no workers: the queue is inspected by hand
	s.frame()
	s.prefetch("ahead", "p1", 10, 5)
	s.block("shown", "p2", 10, 5, ArtTile)
	s.prefetch("ahead2", "p3", 10, 5)
	s.block("shown2", "p4", 10, 5, ArtTile)
	var order []string
	for len(s.queue) > 0 {
		i := s.firstLocked()
		order = append(order, s.queue[i].req.key)
		s.queue = append(s.queue[:i], s.queue[i+1:]...)
	}
	want := []string{"shown", "shown2", "ahead", "ahead2"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("served in the order %v, want %v", order, want)
	}

	// the screen asking for prefetched art raises it
	s.busy, s.lowBusy = map[artReq]bool{}, map[artReq]bool{}
	s.prefetch("a", "p5", 10, 5)
	req := artReq{key: "a", pid: "p5", w: 10, h: 5}
	if !s.lowBusy[req] {
		t.Fatal("a prefetch is not low priority")
	}
	s.frame()
	s.block("a", "p5", 10, 5, ArtTile)
	if s.lowBusy[req] || s.queue[0].low {
		t.Fatal("a tile on screen stayed low priority")
	}
}

func TestPrefetchedCoverLoadsWithoutWakingTheUI(t *testing.T) {
	cover := writeCover(t)
	s, _, _ := newScriptedStore(t, func() (bridge.Artwork, error) { return bridge.Artwork{Path: cover, W: 32, H: 32}, nil })
	s.prefetch("k", "pid", 10, 5)
	s.idle(t)
	time.Sleep(20 * time.Millisecond)
	select {
	case <-s.ready:
		t.Fatal("a prefetched cover woke the UI")
	default:
	}
	if _, ok := s.block("k", "pid", 10, 5, ArtTile); !ok {
		t.Fatal("the prefetched cover was not kept")
	}
	// the same cover asked for from the screen wakes it
	s.block("k2", "pid2", 10, 5, ArtTile)
	s.idle(t)
	select {
	case <-s.ready:
	case <-time.After(time.Second):
		t.Fatal("a cover on screen did not wake the UI")
	}
}

// A settled album grid loads the covers of the rows above and below the
// visible ones, so scrolling a row finds them ready; while it scrolls fast
// it asks for none.
func TestGridPrefetchesRowsBeyondTheEdges(t *testing.T) {
	t.Setenv("TMUX", "")
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.nav.open(a.section(KeyRecent))
	a.View()
	g := albumGridOf(t, a)
	per := g.geo.cols
	rows := g.rowsVisible()
	if g.totalRows() < rows+2*gridPrefetchRows+2 {
		t.Skip("the fake library is too small for the grid to have rows beyond its edges")
	}
	// scroll down so there are rows above and below
	for range rows + 2 {
		g.moveRows(1)
	}
	a.art.idle(t)
	a.View()
	first := (g.top + rows) * per // the first tile below the visible rows (a partly visible row aside)
	beyond := g.items[first+per : min(first+2*per, len(g.items))]
	waitFor(t, func() bool {
		a.art.idle(t)
		for _, al := range beyond {
			if !a.art.r.Ready(tileImageKey(al.Key), g.geo.tileW, g.geo.artH) {
				a.View()
				return false
			}
		}
		return true
	})
}

func albumGridOf(t *testing.T, a *App) *AlbumGrid {
	t.Helper()
	v, ok := a.nav.cur.(*AlbumsView)
	if !ok {
		t.Fatalf("view is %T, want the albums view", a.nav.cur)
	}
	return &v.grid
}

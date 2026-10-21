package art

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

const tmuxIIP = "\x1bPtmux;\x1b\x1b]1337;"

// hybrid returns a Renderer for WezTerm inside tmux: sixel, with tile:
// images through passthrough, and a clock the test moves. Its hero image
// "hero" (6×3 at 2,0) and tile "tile:a" (10×5 at 20,10) are placed and on
// screen.
func hybrid(t *testing.T) (*Renderer, *fakeClock) {
	t.Helper()
	r := NewRenderer(Options{Protocol: ProtoSixel, Passthrough: true, Tmux: true, TilePrefix: "tile:", Cell: CellSize{W: 8, H: 16}})
	clock := &fakeClock{t: time.Unix(1000, 0)}
	r.now = clock.now
	r.Resize(80, 24)
	data := pngBytes(t, testArt(64, 64))
	r.Block("hero", data, 6, 3)
	r.Block("tile:a", data, 10, 5)
	place(r)
	out := drawSixels(t, r, 1)
	if !strings.Contains(out, tmuxIIP) {
		t.Fatalf("tile not drawn as an iTerm2 image through passthrough: %.200q", out)
	}
	return r, clock
}

// fakeClock is a clock the test moves; the Output's timers read it too.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func place(r *Renderer) {
	r.Place("hero", 2, 0, 6, 3)
	r.Place("tile:a", 20, 10, 10, 5)
	r.Frame(strings.Repeat("\n", 23))
}

func TestHybridProtocols(t *testing.T) {
	r, _ := hybrid(t)
	if r.ImageLimit() != 0 || !r.Fragile() {
		t.Fatalf("ImageLimit %d Fragile %v; tiles are not sixel and vanish on tmux redraws", r.ImageLimit(), r.Fragile())
	}
	if !r.sixelKey("hero") || r.sixelKey("tile:a") || !r.passthroughKey("tile:a") {
		t.Fatal("hero must be sixel, tiles passthrough")
	}
	// art = "sixel" (no passthrough): everything sixel, nine at most
	all := NewRenderer(Options{Protocol: ProtoSixel, Tmux: true, TilePrefix: "tile:"})
	if !all.sixelKey("tile:a") || all.ImageLimit() != TmuxImageLimit || all.Fragile() {
		t.Fatal("explicit sixel must keep every image sixel")
	}
	// outside tmux the prefix means nothing
	if NewRenderer(Options{Protocol: ProtoSixel, Passthrough: true, TilePrefix: "tile:"}).passthroughKey("tile:a") {
		t.Fatal("passthrough outside tmux")
	}
}

// Output that erases the hero's line makes tmux drop the sixel image and
// redraw the pane, dropping passthrough output meanwhile: the hero is sent
// again at once, the tile only after redrawHold.
func TestHybridHoldsTilesWhileTmuxRedraws(t *testing.T) {
	r, clock := hybrid(t)
	var dmg damage
	dmg.mark(1, 40, 80) // EL at column 40 of the hero's second row
	dmg.line(1)
	place(r)
	out := r.ops(&dmg)
	if !strings.Contains(out, "\x1bP") || strings.Contains(out, "1337") {
		t.Fatalf("want the hero sixel and no tile: %.200q", out)
	}
	place(r)
	var none damage
	if out := r.ops(&none); strings.Contains(out, "1337") {
		t.Fatal("tile drawn while tmux redraws")
	}
	clock.add(redrawHold + time.Millisecond)
	place(r)
	if out := r.ops(&none); !strings.Contains(out, tmuxIIP) {
		t.Fatalf("tile not drawn after the hold: %.200q", out)
	}

	// cells printed on the hero's row elsewhere don't touch it: no hold
	dmg.reset()
	dmg.mark(1, 40, 50)
	place(r)
	if out := r.ops(&dmg); strings.Contains(out, "\x1bP") || !r.holdUntil.Before(clock.now()) {
		t.Fatalf("printing beside the hero redrew it or held tiles: %.100q", out)
	}

	// a scroll in a pane narrower than its window: tmux redraws the region
	r.SetPane(PaneState{Split: true})
	dmg.reset()
	dmg.scroll(5, 20, 1)
	place(r)
	if out := r.ops(&dmg); strings.Contains(out, "1337") {
		t.Fatal("tile drawn under a scroll tmux redraws itself")
	}
}

// Nothing goes through passthrough while the pane is hidden: it would land
// on whatever window the client shows. Back in view, the tiles come back
// once tmux has redrawn the pane.
// With the pane's place on the terminal known, a passthrough image moves
// the terminal's cursor there itself.
func TestHybridOrigin(t *testing.T) {
	r, clock := hybrid(t)
	r.SetPane(PaneState{Origin: Cell{X: 41, Y: 1}, Located: true})
	r.Invalidate()
	clock.add(redrawHold + time.Millisecond)
	place(r)
	var none damage
	out := r.ops(&none)
	// tile:a at (20, 10) in the pane: row 12, column 62 on the terminal,
	// erased there first, all inside the passthrough
	if !strings.Contains(out, "\x1bPtmux;\x1b\x1b7\x1b\x1b[m\x1b\x1b[12;62H\x1b\x1b[10X") ||
		!strings.Contains(out, "\x1b\x1b[16;62H\x1b\x1b[10X\x1b\x1b[12;62H\x1b\x1b]1337;") {
		t.Fatalf("no erase and cursor move inside the passthrough: %.300q", out)
	}
	if strings.Contains(out, "\x1b[21;21H          ") {
		t.Fatal("spaces printed through tmux; tmux may write them after the image")
	}
}

func TestHybridHiddenPane(t *testing.T) {
	r, clock := hybrid(t)
	r.SetPane(PaneState{Hidden: true})
	r.Invalidate()
	place(r)
	var none damage
	out := r.ops(&none)
	if strings.Contains(out, "1337") {
		t.Fatal("tile drawn while the pane is hidden")
	}
	if !strings.Contains(out, "\x1bP") {
		t.Fatal("sixel is kept by tmux and may be drawn while hidden")
	}
	clock.add(time.Second)
	place(r)
	if strings.Contains(r.ops(&none), "1337") {
		t.Fatal("tile drawn while the pane is hidden")
	}
	r.SetPane(PaneState{})
	place(r)
	if strings.Contains(r.ops(&none), "1337") {
		t.Fatal("tile drawn before tmux is done redrawing")
	}
	clock.add(redrawHold + time.Millisecond)
	place(r)
	if !strings.Contains(r.ops(&none), tmuxIIP) {
		t.Fatal("tile not drawn again")
	}

	// a hook signal: the same, without a visibility change
	r.TmuxRedrawn()
	place(r)
	if strings.Contains(r.ops(&none), "1337") {
		t.Fatal("tile drawn before tmux is done redrawing")
	}
	clock.add(redrawHold + time.Millisecond)
	place(r)
	if !strings.Contains(r.ops(&none), tmuxIIP) {
		t.Fatal("tile not drawn after a tmux redraw")
	}
}

// Bubble Tea repaints a line that holds a wide character on every frame:
// EL from column 0, then every cell up to the last non-blank one. Images
// staying on that line keep their cells: the EL skips them and spaces over
// them become cursor moves.
func TestTrackerSkipsStayingImages(t *testing.T) {
	tr := newTracker(40, 10)
	tr.dmg.reset()
	tr.stay = func() *staying {
		return &staying{spans: map[int][]ival{4: {{10, 20}}}, sixel: map[int]bool{}}
	}
	in := "\x1b[5;1H\x1b[K\x1b[3Cab" + strings.Repeat(" ", 20) + "|"
	out := string(tr.feed([]byte(in)))
	if tr.dmg.hits(rect{10, 4, 10, 1}) {
		t.Fatalf("staying image cells damaged; wrote %q", out)
	}
	if !tr.dmg.hits(rect{0, 4, 10, 1}) || !tr.dmg.hits(rect{20, 4, 20, 1}) {
		t.Fatal("cells around the image not damaged")
	}
	want := "\x1b[5;1H" + "\x1b[5;1H\x1b[10X\x1b[5;21H\x1b[K\x1b[5;1H" + "\x1b[3Cab" + "     " + "\x1b[10C" + "     " + "|"
	if out != want {
		t.Fatalf("wrote\n%q\nwant\n%q", out, want)
	}
	if tr.x != 26 || tr.y != 4 {
		t.Fatalf("cursor at %d,%d", tr.x, tr.y)
	}

	// another row is untouched by the rewrite
	tr.dmg.reset()
	if out := tr.feed([]byte("\x1b[6;1H\x1b[K")); string(out) != "\x1b[6;1H\x1b[K" {
		t.Fatalf("rewrote a row without images: %q", out)
	}

	// inside tmux, on a row with a sixel image tmux keeps, erases are
	// spaces: an EL or ECH would make tmux drop the image
	tr = newTracker(40, 10)
	tr.dmg.reset()
	tr.tmux = true
	tr.stay = func() *staying {
		return &staying{spans: map[int][]ival{4: {{10, 20}}}, sixel: map[int]bool{4: true}}
	}
	out = string(tr.feed([]byte("\x1b[5;1H\x1b[K\x1b[5;25H\x1b[3X")))
	if strings.Contains(out, "\x1b[K") || strings.Contains(out, "X") {
		t.Fatalf("erase left on a sixel row: %q", out)
	}
	if tr.dmg.hitsLine(rect{10, 4, 10, 1}) {
		t.Fatal("the sixel image was hit")
	}
}

// lockedBuf is a bytes.Buffer the Output's timers may write to.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) take() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.b.String()
	l.b.Reset()
	return s
}

// End to end through Output: a frame that repaints the tile's row from
// column 0 sends no image again.
func TestOutputKeepsImagesOnRepaintedRows(t *testing.T) {
	r, clock := hybrid(t)
	var buf lockedBuf
	o := NewOutput(&buf, r)
	defer o.Close()
	// entering the alt screen clears it, and tmux drops the hero: the tile
	// waits for tmux to redraw the pane
	place(r)
	o.Write([]byte("\x1b[?1049h"))
	if s := buf.take(); !strings.Contains(s, "\x1bP") || strings.Contains(s, "1337") {
		t.Fatalf("want the hero and no tile: %.200q", s)
	}
	clock.add(redrawHold + time.Millisecond)
	place(r)
	o.Write([]byte("\x1b[1;1H"))
	if s := buf.take(); !strings.Contains(s, tmuxIIP) {
		t.Fatalf("tile not drawn after the hold: %.200q", s)
	}
	place(r)
	o.Write([]byte("\x1b[12;1H\x1b[K\x1b[3C夏のプ   |"))
	if s := buf.take(); strings.Contains(s, "1337") || strings.Contains(s, "\x1bP") {
		t.Fatalf("image sent again for a repaint beside it: %.200q", s)
	}
}

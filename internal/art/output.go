package art

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// fallbackDelay is how long Output waits for a frame write before drawing
// pending images on its own (a frame whose text did not change produces no
// write, e.g. when only the image behind a blank block changed).
const fallbackDelay = 40 * time.Millisecond

// Output is the terminal writer to give Bubble Tea (tea.WithOutput). It
// passes everything through and, after every write, draws the IIP or sixel
// images the write disturbed or that are new, plus pending kitty uploads. When the frame
// is wrapped in synchronized output (mode 2026, which Bubble Tea enables when
// the terminal reports it) the images go inside the same update, so the
// terminal never shows a frame without them.
//
// Images are only drawn while the alternate screen is active (View.AltScreen),
// so nothing is left behind in the shell's scrollback.
//
// Output implements term.File when the wrapped writer does (an *os.File), so
// Bubble Tea still sees a TTY.
type Output struct {
	w io.Writer
	r *Renderer

	mu sync.Mutex // serialises writes; taken before Renderer.mu
	t  *tracker

	timerMu sync.Mutex // poke runs under Renderer.mu, so it must not take mu
	timer   *time.Timer
	armed   bool
	later   *time.Timer // pokeAfter
}

// NewOutput wraps w (usually os.Stdout) for r.
func NewOutput(w io.Writer, r *Renderer) *Output {
	o := &Output{w: w, r: r, t: newTracker(80, 24)}
	o.t.tmux = r.tmux
	if r.proto.Placed() {
		o.t.stay = r.staying
	}
	r.mu.Lock()
	if r.width > 0 && r.height > 0 {
		o.t.resize(r.width, r.height)
	}
	r.notify = o.poke
	r.notifyAfter = o.pokeAfter
	r.mu.Unlock()
	return o
}

var esu = []byte(ansi.ResetModeSynchronizedOutput)

// Write implements io.Writer.
func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.syncSize()
	q := o.t.feed(p) // may skip the cells of images that stay
	redo := o.t.takeRedo()
	if !o.t.alt {
		o.t.dmg.reset()
		return o.write(p, q)
	}
	ops := ""
	if len(redo) > 0 {
		ops = o.r.redo(redo, o.t.staying().spans)
	}
	ops += o.r.ops(&o.t.dmg)
	o.t.dmg.reset()
	if ops == "" {
		return o.write(p, q)
	}
	if bytes.HasSuffix(q, esu) {
		buf := make([]byte, 0, len(q)+len(ops))
		buf = append(buf, q[:len(q)-len(esu)]...)
		buf = append(buf, ops...)
		buf = append(buf, esu...)
		if _, err := o.w.Write(buf); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	n, err := o.write(p, q)
	if err != nil {
		return n, err
	}
	_, err = io.WriteString(o.w, ansi.SetModeSynchronizedOutput+ops+ansi.ResetModeSynchronizedOutput)
	return n, err
}

// write writes q, which stands for p, and reports progress through p.
func (o *Output) write(p, q []byte) (int, error) {
	n, err := o.w.Write(q)
	if n == len(q) {
		return len(p), err
	}
	return min(n, len(p)), err
}

// syncSize keeps the tracker's screen size in step with the Renderer.
func (o *Output) syncSize() {
	o.r.mu.Lock()
	w, h := o.r.width, o.r.height
	o.r.mu.Unlock()
	if w > 0 && h > 0 && (w != o.t.w || h != o.t.h) {
		o.t.resize(w, h)
	}
}

// poke is called by the Renderer when something is waiting to be drawn.
func (o *Output) poke() {
	o.timerMu.Lock()
	defer o.timerMu.Unlock()
	if o.armed {
		return
	}
	o.armed = true
	if o.timer == nil {
		o.timer = time.AfterFunc(fallbackDelay, o.fallback)
		return
	}
	o.timer.Reset(fallbackDelay)
}

// pokeAfter is poke for images held back for d (see Renderer.TmuxRedrawn).
func (o *Output) pokeAfter(d time.Duration) {
	o.timerMu.Lock()
	defer o.timerMu.Unlock()
	if o.later == nil {
		o.later = time.AfterFunc(d, o.fallback)
		return
	}
	o.later.Reset(d)
}

func (o *Output) fallback() {
	o.timerMu.Lock()
	o.armed = false
	o.timerMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.t.alt {
		return
	}
	o.syncSize()
	var none damage
	if ops := o.r.ops(&none); ops != "" {
		_, _ = io.WriteString(o.w, ansi.SetModeSynchronizedOutput+ops+ansi.ResetModeSynchronizedOutput)
	}
}

// Read reads from the wrapped writer when it is also a reader.
func (o *Output) Read(p []byte) (int, error) {
	if r, ok := o.w.(io.Reader); ok {
		return r.Read(p)
	}
	return 0, errors.ErrUnsupported
}

// Close stops the fallback timer. The wrapped writer is left open; Bubble Tea
// does not own stdout.
func (o *Output) Close() error {
	o.timerMu.Lock()
	defer o.timerMu.Unlock()
	if o.timer != nil {
		o.timer.Stop()
	}
	if o.later != nil {
		o.later.Stop()
	}
	return nil
}

// Fd returns the wrapped file's descriptor, or an invalid one.
func (o *Output) Fd() uintptr {
	if f, ok := o.w.(term.File); ok {
		return f.Fd()
	}
	return ^uintptr(0)
}

// CellSizeQuery asks the terminal for its cell size in pixels (XTWINOPS 16).
// Send it with tea.Raw at startup and on resize; the reply arrives as a
// message that CellSizeFromMsg understands.
const CellSizeQuery = "\x1b[16t"

// CellSizeFromMsg extracts the cell size from the terminal's reply to
// CellSizeQuery.
func CellSizeFromMsg(msg tea.Msg) (CellSize, bool) {
	if e, ok := msg.(uv.CellSizeEvent); ok && e.Width > 0 && e.Height > 0 {
		return CellSize{W: e.Width, H: e.Height}, true
	}
	return CellSize{}, false
}

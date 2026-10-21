package art

import (
	"bytes"
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/art/internal/vt"
)

// These tests run a real Bubble Tea v2 program whose layout puts a list next
// to an IIP image, feed its output through Output into a terminal emulator
// that treats image tiles like WezTerm does (they belong to cells), and check
// the screen after scrolling, overlays, image changes and resizes.

const (
	artX, artY       = 34, 2
	artCols, artRows = 20, 10
)

var ovl = rect{x: 28, y: 6, w: 16, h: 5}

type lockedTerm struct {
	mu  sync.Mutex
	vt  *vt.Terminal
	log io.Writer
}

func (l *lockedTerm) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.log != nil {
		fmt.Fprintf(l.log, "WRITE %q\n", p)
	}
	return l.vt.Write(p)
}

type scrollMsg int
type toggleMsg struct{}
type keyMsg string

type spikeModel struct {
	r       *Renderer
	w, h    int
	off     int
	overlay bool
	key     string
	shorten bool // shorten one list row, to make the renderer use EL near the art
}

func (m *spikeModel) Init() tea.Cmd { return nil }

func (m *spikeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.r.Resize(msg.Width, msg.Height)
	case scrollMsg:
		m.off = max(0, m.off+int(msg))
	case toggleMsg:
		m.overlay = !m.overlay
	case keyMsg:
		m.key = string(msg)
	case tea.KeyPressMsg:
		if msg.String() == "s" {
			m.shorten = !m.shorten
		}
	}
	return m, nil
}

func (m *spikeModel) View() tea.View {
	lines := make([]string, m.h)
	art := strings.Split(m.r.Block(m.key, nil, artCols, artRows), "\n")
	for y := range m.h {
		item := fmt.Sprintf("  item %03d  the quick brown fox", y+m.off)
		if m.shorten && y == 4 {
			item = "  x"
		}
		line := padTo(item, 30) + "    "
		if y >= artY && y < artY+artRows && len(art) == artRows {
			line += art[y-artY]
		}
		lines[y] = ansi.Truncate(line, m.w, "")
	}
	m.r.Place(m.key, artX, artY, artCols, artRows)
	if m.overlay {
		box := []string{"╭──────────────╮", "│   overlay    │", "│              │", "│              │", "╰──────────────╯"}
		for i, b := range box {
			y := ovl.y + i
			left := padTo(ansi.Truncate(lines[y], ovl.x, ""), ovl.x)
			right := ansi.TruncateLeft(lines[y], ovl.x+ovl.w, "")
			lines[y] = left + b + right
		}
		m.r.Occlude(ovl.x, ovl.y, ovl.w, ovl.h)
	}
	s := strings.Join(lines, "\n")
	m.r.Frame(s)
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

func padTo(s string, w int) string {
	if d := w - ansi.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

type harness struct {
	t    *testing.T
	r    *Renderer
	term *lockedTerm
	p    *tea.Program
	in   *io.PipeWriter
	done chan struct{}
}

func startSpike(t *testing.T, syncd bool) *harness {
	t.Helper()
	r := NewRenderer(Options{Protocol: ProtoIIP})
	for _, k := range []string{"a", "b"} {
		c := map[string]color.NRGBA{"a": {200, 40, 60, 255}, "b": {40, 90, 200, 255}}[k]
		img := testArt(200, 200)
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i] = (img.Pix[i] + c.R) / 2
			img.Pix[i+2] = (img.Pix[i+2] + c.B) / 2
		}
		if err := r.Prepare(k, pngBytes(t, img), artCols, artRows); err != nil {
			t.Fatal(err)
		}
	}
	// Without a tty input Bubble Tea assumes the tty maps LF to CR LF.
	term := &lockedTerm{vt: vt.New(60, 20)}
	term.vt.ONLCR = true
	if path := os.Getenv("ART_TEST_LOG"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		term.log = f
	}
	out := NewOutput(term, r)
	out.t.onlcr = true
	pr, pw := io.Pipe()
	m := &spikeModel{r: r, key: "a"}
	p := tea.NewProgram(m,
		tea.WithInput(pr),
		tea.WithOutput(out),
		tea.WithWindowSize(60, 20),
		tea.WithoutSignals(),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "TERM_PROGRAM=WezTerm"}),
	)
	h := &harness{t: t, r: r, term: term, p: p, in: pw, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		if _, err := p.Run(); err != nil {
			t.Errorf("program: %v", err)
		}
	}()
	if syncd {
		// Answer Bubble Tea's DECRQM 2026 query: supported, currently off.
		go func() { _, _ = pw.Write([]byte("\x1b[?2026;2$y")) }()
	}
	t.Cleanup(func() {
		p.Quit()
		_ = pw.Close()
		<-h.done
		_ = out.Close()
	})
	h.settle()
	return h
}

func (h *harness) settle() { settleOutput() }

func (h *harness) send(msg tea.Msg) {
	h.p.Send(msg)
	h.settle()
}

func (h *harness) images() int {
	h.term.mu.Lock()
	defer h.term.mu.Unlock()
	return len(h.term.vt.Images)
}

// check verifies that every cell of the art box shows the right tile of the
// right image, except under the overlay, which must show the overlay text.
func (h *harness) check(step, key string, overlay bool) {
	h.t.Helper()
	h.term.mu.Lock()
	defer h.term.mu.Unlock()
	term := h.term.vt
	box := rect{artX, artY, artCols, artRows}
	box = box.intersect(rect{0, 0, term.W, term.H})
	for y := box.y; y < box.y+box.h; y++ {
		for x := box.x; x < box.x+box.w; x++ {
			c := term.At(x, y)
			under := overlay && !ovl.intersect(rect{x, y, 1, 1}).empty()
			if under {
				if c.Image != 0 {
					h.t.Fatalf("%s: image tile over the overlay at %d,%d\n%s", step, x, y, h.dump())
				}
				continue
			}
			img, ok := term.ImageByID(c.Image)
			if !ok {
				h.t.Fatalf("%s: no image at %d,%d\n%s", step, x, y, h.dump())
			}
			if img.X+c.TX != x || img.Y+c.TY != y {
				h.t.Fatalf("%s: tile at %d,%d belongs at %d,%d (image moved)\n%s", step, x, y, img.X+c.TX, img.Y+c.TY, h.dump())
			}
			// The tile must come from the expected crop of the expected art.
			crop := rect{img.X - artX, img.Y - artY, img.Cols, img.Rows}
			want, _ := h.r.payloads.get(pieceKey{boxKey{key, artCols, artRows}, crop, h.r.cell})
			if !bytes.Equal(img.Data, want) {
				h.t.Fatalf("%s: image at %d,%d is not %q crop %+v", step, x, y, key, crop)
			}
		}
	}
	for y := range term.H {
		for x := range term.W {
			if c := term.At(x, y); c.Image != 0 && box.intersect(rect{x, y, 1, 1}).empty() {
				h.t.Fatalf("%s: stray image fragment at %d,%d\n%s", step, x, y, h.dump())
			}
		}
	}
	if overlay {
		if got := term.Text(ovl.y + 1); !strings.Contains(got, "│   overlay    │") {
			h.t.Fatalf("%s: overlay text missing: %q", step, got)
		}
	}
}

func (h *harness) dump() string {
	var b strings.Builder
	for y := range h.term.vt.H {
		for x := range h.term.vt.W {
			c := h.term.vt.At(x, y)
			switch {
			case c.Image != 0:
				b.WriteByte('#')
			case c.Content == "":
				b.WriteByte('_')
			default:
				b.WriteString(c.Content)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestSpikeIIPStaysIntact(t *testing.T) {
	for _, syncd := range []bool{false, true} {
		t.Run(fmt.Sprintf("syncd=%v", syncd), func(t *testing.T) {
			h := startSpike(t, syncd)
			h.check("first frame", "a", false)

			for i := range 5 {
				h.send(scrollMsg(1))
				h.check(fmt.Sprintf("scroll down %d", i), "a", false)
			}
			h.send(scrollMsg(-3))
			h.check("scroll up", "a", false)

			h.send(tea.KeyPressMsg{Code: 's', Text: "s"})
			h.check("shorter row beside the art", "a", false)

			h.send(toggleMsg{})
			h.check("overlay open", "a", true)
			h.send(scrollMsg(2))
			h.check("scroll under overlay", "a", true)
			h.send(toggleMsg{})
			h.check("overlay closed", "a", false)

			h.send(keyMsg("b"))
			h.check("next image", "b", false)

			h.term.mu.Lock()
			h.term.vt.Resize(50, 16)
			h.term.mu.Unlock()
			h.send(tea.WindowSizeMsg{Width: 50, Height: 16})
			h.check("resize smaller", "b", false)

			h.term.mu.Lock()
			h.term.vt.Resize(70, 24)
			h.term.mu.Unlock()
			h.send(tea.WindowSizeMsg{Width: 70, Height: 24})
			h.check("resize larger", "b", false)

			// Nothing changed: nothing may be sent.
			n := h.images()
			h.send(scrollMsg(0))
			if got := h.images(); got != n {
				t.Fatalf("idle frame re-sent %d images", got-n)
			}
		})
	}
}

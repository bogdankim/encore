package art

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/art/internal/vt"
)

// staysAt is a tracker.stay that keeps an image on rows y0..y1-1, columns
// x0..x1-1.
func staysAt(x0, y0, x1, y1 int) func() *staying {
	return func() *staying {
		st := &staying{spans: map[int][]ival{}, sixel: map[int]bool{}}
		for y := y0; y < y1; y++ {
			st.spans[y] = []ival{{x0, x1}}
		}
		return st
	}
}

// Bubble Tea scrolls rows whose text moved up or down. Rows beside a block
// of art are blank there, so it scrolls the art with them; the tracker drops
// such a scroll and asks for the rows to be repainted instead.
func TestTrackerDropsScrollsOverStayingImages(t *testing.T) {
	cases := []struct {
		name, in, out string
		onlcr         bool
		rows          []int
	}{
		{"LF at the bottom margin", "\x1b[3;12r\x1b[12;1H\n\x1b[1;24r", "\x1b[3;12r\x1b[12;1H\x1b[1;24r", false, rows(2, 12)},
		{"LF with ONLCR keeps the CR", "\x1b[3;12r\x1b[12;1H\n\x1b[1;24r", "\x1b[3;12r\x1b[12;1H\r\x1b[1;24r", true, rows(2, 12)},
		{"DL", "\x1b[4;1H\x1b[2M", "\x1b[4;1H", false, rows(3, 24)},
		{"IL", "\x1b[4;1H\x1b[L", "\x1b[4;1H", false, rows(3, 24)},
		{"SU", "\x1b[S", "", false, rows(0, 24)},
		{"RI at the top margin", "\x1b[3;12r\x1b[3;1H\x1bM", "\x1b[3;12r\x1b[3;1H", false, rows(2, 12)},
		{"DCH right of the cursor", "\x1b[6;1H\x1b[3P", "\x1b[6;1H", false, []int{5}},
		{"ICH right of the cursor", "\x1b[6;1H\x1b[2@", "\x1b[6;1H", false, []int{5}},
	}
	for _, c := range cases {
		tr := newTracker(40, 24)
		tr.alt, tr.onlcr = true, c.onlcr
		tr.dmg.reset()
		tr.stay = staysAt(2, 5, 10, 9)
		if got := string(tr.feed([]byte(c.in))); got != c.out {
			t.Errorf("%s: wrote %q, want %q", c.name, got, c.out)
		}
		if tr.dmg.hits(rect{2, 5, 8, 4}) || len(tr.dmg.scrolls) > 0 {
			t.Errorf("%s: image damaged: %+v", c.name, tr.dmg)
		}
		if got := tr.takeRedo(); !slices.Equal(got, c.rows) {
			t.Errorf("%s: repaint rows %v, want %v", c.name, got, c.rows)
		}
	}

	// rows without images scroll as usual
	tr := newTracker(40, 24)
	tr.alt = true
	tr.dmg.reset()
	tr.stay = staysAt(2, 5, 10, 9)
	in := "\x1b[12;20r\x1b[20;1H\n\x1b[14;1H\x1b[M\x1b[1;24r\x1b[3;12H\x1b[2P"
	if got := string(tr.feed([]byte(in))); got != in {
		t.Fatalf("rewrote scrolls away from the image: %q", got)
	}
	if len(tr.dmg.scrolls) != 2 || tr.takeRedo() != nil {
		t.Fatalf("scrolls %+v", tr.dmg.scrolls)
	}

	// inside tmux an insert or delete anywhere on the image's row is held:
	// tmux may rewrite the line from its first column
	tr = newTracker(40, 24)
	tr.alt, tr.tmux = true, true
	tr.dmg.reset()
	tr.stay = staysAt(2, 5, 10, 9)
	if got := string(tr.feed([]byte("\x1b[6;20H\x1b[P"))); got != "\x1b[6;20H" {
		t.Fatalf("tmux DCH beside the image: %q", got)
	}
	if got := tr.takeRedo(); !slices.Equal(got, []int{5}) {
		t.Fatalf("repaint rows %v", got)
	}
}

func rows(lo, hi int) []int {
	var r []int
	for y := lo; y < hi; y++ {
		r = append(r, y)
	}
	return r
}

// The repaint writes the frame's text around images, padded with spaces,
// and never erases.
func TestRepaintAround(t *testing.T) {
	line := "ab" + strings.Repeat(" ", 6) + "\x1b[1mcd\x1b[m"
	got := repaintAround([]string{"", line}, 1, 14, []ival{{2, 8}}, nil)
	if strings.Contains(got, "\x1b[K") || strings.Contains(got, "X") {
		t.Fatalf("repaint erases: %q", got)
	}
	v := vt.New(14, 3)
	_, _ = v.Write([]byte("\x1b[2;1H" + strings.Repeat("#", 14) + got))
	if row := v.Text(1); row != "ab######cd    " {
		t.Fatalf("row reads %q", row)
	}
}

// lyricsModel is full-screen lyrics in miniature: art on the left, lines of
// text on the right that move up two rows at a time, as the current lyric
// advances. Bubble Tea scrolls those rows in hardware, art and all.
type lyricsModel struct {
	r    *Renderer
	w, h int
	line int
}

type advance struct{}

const (
	lyrArtX, lyrArtY, lyrArtW, lyrArtH = 2, 3, 16, 8
	lyrTextX                           = 24
)

func (m *lyricsModel) Init() tea.Cmd { return nil }

func (m *lyricsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.r.Resize(msg.Width, msg.Height)
	case advance:
		m.line++
	}
	return m, nil
}

// lyricRow is the text right of the art on row y.
func (m *lyricsModel) lyricRow(y int) string {
	if y%2 == 1 {
		return ""
	}
	n := y/2 + m.line
	if n%3 == 0 {
		return fmt.Sprintf("line %d, a longer one", n)
	}
	return fmt.Sprintf("line %d", n)
}

func (m *lyricsModel) View() tea.View {
	art := strings.Split(m.r.Block("a", nil, lyrArtW, lyrArtH), "\n")
	lines := make([]string, m.h)
	for y := range m.h {
		left := strings.Repeat(" ", lyrTextX)
		if y >= lyrArtY && y < lyrArtY+lyrArtH && len(art) == lyrArtH {
			left = strings.Repeat(" ", lyrArtX) + art[y-lyrArtY] + strings.Repeat(" ", lyrTextX-lyrArtX-lyrArtW)
		}
		lines[y] = ansi.Truncate(padTo(left+m.lyricRow(y), m.w), m.w, "")
	}
	m.r.Place("a", lyrArtX, lyrArtY, lyrArtW, lyrArtH)
	s := strings.Join(lines, "\n")
	m.r.Frame(s)
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

// A lyric line advancing next to the art sends no image again, keeps every
// art cell, and leaves the text right.
func TestLyricsScrollKeepsArt(t *testing.T) {
	for _, syncd := range []bool{false, true} {
		t.Run(fmt.Sprintf("syncd=%v", syncd), func(t *testing.T) {
			r := NewRenderer(Options{Protocol: ProtoIIP})
			if err := r.Prepare("a", pngBytes(t, testArt(200, 100)), lyrArtW, lyrArtH); err != nil {
				t.Fatal(err)
			}
			m := &lyricsModel{r: r}
			term, quit := runModel(t, r, m, 60, 20, syncd)
			defer quit()
			check := func(step string) {
				t.Helper()
				term.mu.Lock()
				defer term.mu.Unlock()
				v := term.vt
				for y := range v.H {
					for x := range v.W {
						c := v.At(x, y)
						in := x >= lyrArtX && x < lyrArtX+lyrArtW && y >= lyrArtY && y < lyrArtY+lyrArtH
						img, ok := v.ImageByID(c.Image)
						switch {
						case in && (!ok || img.X+c.TX != x || img.Y+c.TY != y):
							t.Fatalf("%s: art cell %d,%d lost its tile", step, x, y)
						case !in && c.Image != 0:
							t.Fatalf("%s: stray image tile at %d,%d", step, x, y)
						}
					}
					if got, want := strings.TrimRight(v.Text(y)[lyrTextX:], " "), m.lyricRow(y); got != want {
						t.Fatalf("%s: row %d reads %q, want %q", step, y, got, want)
					}
				}
			}
			check("first frame")
			term.mu.Lock()
			sent := len(term.vt.Images)
			term.mu.Unlock()
			for i := range 6 {
				term.send(advance{})
				check(fmt.Sprintf("line %d", i+1))
			}
			term.mu.Lock()
			defer term.mu.Unlock()
			if n := len(term.vt.Images) - sent; n != 0 {
				t.Fatalf("lyric lines advancing sent the art %d more times", n)
			}
		})
	}
}

// modelTerm is a running program's terminal.
type modelTerm struct {
	*lockedTerm
	p *tea.Program
}

func (m modelTerm) send(msg tea.Msg) {
	m.p.Send(msg)
	settleOutput()
}

// settleOutput waits for a frame and the images after it to be written.
func settleOutput() { time.Sleep(120 * time.Millisecond) }

// runModel runs m on a w×h vt behind an Output for r.
func runModel(t *testing.T, r *Renderer, m tea.Model, w, h int, syncd bool) (modelTerm, func()) {
	t.Helper()
	term := &lockedTerm{vt: vt.New(w, h)}
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
	p := tea.NewProgram(m,
		tea.WithInput(pr),
		tea.WithOutput(out),
		tea.WithWindowSize(w, h),
		tea.WithoutSignals(),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "TERM_PROGRAM=WezTerm"}),
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := p.Run(); err != nil {
			t.Errorf("program: %v", err)
		}
	}()
	if syncd {
		go func() { _, _ = pw.Write([]byte("\x1b[?2026;2$y")) }()
	}
	settleOutput()
	return modelTerm{term, p}, func() {
		p.Quit()
		_ = pw.Close()
		<-done
		_ = out.Close()
	}
}

// When an image goes away, the rows it leaves fragments on are repainted
// around the images that stay, so those are not sent again.
func TestRepaintKeepsOtherImages(t *testing.T) {
	r := NewRenderer(Options{Protocol: ProtoIIP})
	r.Resize(40, 6)
	for _, k := range []string{"a", "b"} {
		if err := r.Prepare(k, pngBytes(t, testArt(60, 30)), 6, 3); err != nil {
			t.Fatal(err)
		}
	}
	frame := strings.Repeat(strings.Repeat(" ", 40)+"\n", 5) + strings.Repeat(" ", 40)
	r.Place("a", 2, 1, 6, 3)
	r.Place("b", 20, 1, 6, 3)
	r.Frame(frame)
	if ops := r.ops(&damage{}); strings.Count(ops, "1337;File") != 2 {
		t.Fatalf("first frame: %d images", strings.Count(ops, "1337;File"))
	}
	r.Place("a", 2, 1, 6, 3)
	r.Frame(frame)
	ops := r.ops(&damage{})
	if strings.Contains(ops, "1337;File") {
		t.Fatalf("the image that stays was sent again: %.120q", ops)
	}
	v := vt.New(40, 6)
	_, _ = v.Write([]byte(strings.Repeat("#", 40*6)))
	_, _ = v.Write([]byte(ops))
	for y := 1; y < 4; y++ {
		if row := v.Text(y); row[2:8] != "######" || strings.TrimSpace(row[20:26]) != "" {
			t.Fatalf("row %d: %q", y, row)
		}
	}
}

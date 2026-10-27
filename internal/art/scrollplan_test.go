package art

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// gridModel is the album grid in miniature: a sidebar of text on the left and
// tiles of art, three to a row, with a title and an artist line under each.
// The tiles end at gridBot; the row that is cut off there is cropped by an
// occluder, as the app does it.
type gridModel struct {
	r       *Renderer
	w, h    int
	top     int
	overlay bool
	// extra is an image that holds still next to the grid, rows 3 to 5.
	extra bool
}

type gridScroll int

type gridOverlay struct{}

const (
	gTileW, gTileH = 10, 4
	gPitch         = gTileH + 2
	gTop, gBot     = 2, 22 // grid rows gTop..gBot-1
	gX0, gStride   = 16, 12
	gCols          = 3
	gRows          = 30
)

var gOverlay = rect{x: 24, y: 8, w: 14, h: 6}

func gKey(r, c int) string { return fmt.Sprintf("t%d.%d", r, c) }

func (m *gridModel) Init() tea.Cmd { return nil }

func (m *gridModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.r.Resize(msg.Width, msg.Height)
	case gridScroll:
		m.top = max(0, m.top+int(msg))
	case gridOverlay:
		m.overlay = !m.overlay
	}
	return m, nil
}

// text is what row y shows besides the art: the sidebar, and the grid's
// titles and artists.
func (m *gridModel) text(y int) string {
	line := padTo(fmt.Sprintf("side %02d", y), gX0)
	if m.extra && y >= 3 && y < 6 {
		line = padTo("", gX0)
	}
	if y >= gTop && y < gBot {
		r, j := m.top+(y-gTop)/gPitch, (y-gTop)%gPitch
		for c := range gCols {
			cell := strings.Repeat(" ", gTileW)
			switch j {
			case gTileH:
				cell = padTo(fmt.Sprintf("title %d.%d", r, c), gTileW)
			case gTileH + 1:
				cell = padTo(fmt.Sprintf("by %d.%d", r, c), gTileW)
			}
			line += padTo(cell, gStride)
		}
	}
	if m.overlay && y >= gOverlay.y && y < gOverlay.y+gOverlay.h {
		line = padTo(line, gOverlay.x+gOverlay.w)
		line = ansi.Truncate(line, gOverlay.x, "") + strings.Repeat("o", gOverlay.w) + ansi.TruncateLeft(line, gOverlay.x+gOverlay.w, "")
	}
	return ansi.Truncate(padTo(line, m.w), m.w, "")
}

func (m *gridModel) View() tea.View {
	lines := make([]string, m.h)
	for y := range m.h {
		lines[y] = m.text(y)
	}
	for r := m.top; r < gRows; r++ {
		y := gTop + (r-m.top)*gPitch
		if y >= gBot {
			break
		}
		for c := range gCols {
			m.r.Place(gKey(r, c), gX0+c*gStride, y, gTileW, gTileH)
			if cut := y + gTileH - gBot; cut > 0 {
				m.r.Occlude(gX0+c*gStride, gBot, gTileW, cut)
			}
		}
	}
	if m.extra {
		m.r.Place("extra", 2, 3, 8, 3)
	}
	if m.overlay {
		m.r.Occlude(gOverlay.x, gOverlay.y, gOverlay.w, gOverlay.h)
	}
	s := strings.Join(lines, "\n")
	m.r.Frame(s)
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

// gridCheck verifies the screen against the model: the text of every row, an
// image tile of the right tile (and the right part of it) in every cell of
// every visible piece, and no image tile anywhere else.
func gridCheck(t *testing.T, term modelTerm, m *gridModel, step string) {
	t.Helper()
	term.mu.Lock()
	defer term.mu.Unlock()
	v := term.vt
	want := map[[2]int]string{} // cell -> tile key, for cells that hold art
	tileAt := map[[2]int][2]int{}
	for r := m.top; r < gRows; r++ {
		y := gTop + (r-m.top)*gPitch
		if y >= gBot {
			break
		}
		for c := range gCols {
			x := gX0 + c*gStride
			for yy := y; yy < min(y+gTileH, gBot); yy++ {
				for xx := x; xx < x+gTileW; xx++ {
					if m.overlay && !gOverlay.intersect(rect{xx, yy, 1, 1}).empty() {
						continue
					}
					want[[2]int{xx, yy}] = gKey(r, c)
					tileAt[[2]int{xx, yy}] = [2]int{xx - x, yy - y}
				}
			}
		}
	}
	for y := range v.H {
		if got, wantText := v.Text(y), m.text(y); got != wantText {
			t.Fatalf("%s: row %d reads %q, want %q\n%s", step, y, got, wantText, dumpVT(term))
		}
		for x := range v.W {
			c := v.At(x, y)
			key, art := want[[2]int{x, y}]
			if !art {
				if m.extra && x >= 2 && x < 10 && y >= 3 && y < 6 {
					continue
				}
				if c.Image != 0 {
					t.Fatalf("%s: stray image tile at %d,%d\n%s", step, x, y, dumpVT(term))
				}
				continue
			}
			img, ok := v.ImageByID(c.Image)
			if !ok {
				t.Fatalf("%s: no image at %d,%d (%s)\n%s", step, x, y, key, dumpVT(term))
			}
			// the image is a crop of the tile; find which, and check that
			// this cell is the tile's cell
			at := tileAt[[2]int{x, y}]
			found := false
			for r0 := range gTileH {
				for h := 1; h <= gTileH-r0; h++ {
					for x0 := range gTileW {
						for w := 1; w <= gTileW-x0; w++ {
							if c.TY+r0 != at[1] || c.TX+x0 != at[0] || img.Rows != h || img.Cols != w {
								continue
							}
							p, ok := m.r.payloads.get(pieceKey{boxKey{key, gTileW, gTileH}, rect{x0, r0, w, h}, m.r.cell})
							found = found || ok && bytes.Equal(p, img.Data)
						}
					}
				}
			}
			if !found {
				t.Fatalf("%s: cell %d,%d is not %s's cell %v\n%s", step, x, y, key, at, dumpVT(term))
			}
		}
	}
}

func dumpVT(term modelTerm) string {
	var b strings.Builder
	for y := range term.vt.H {
		for x := range term.vt.W {
			c := term.vt.At(x, y)
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

func gridRenderer(t *testing.T, noScroll bool) *Renderer {
	t.Helper()
	r := NewRenderer(Options{Protocol: ProtoIIP, NoScroll: noScroll})
	for row := range gRows {
		for c := range gCols {
			if err := r.Prepare(gKey(row, c), pngBytes(t, testArt(64+row, 64+c)), gTileW, gTileH); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := r.Prepare("extra", pngBytes(t, testArt(64, 64)), 8, 3); err != nil {
		t.Fatal(err)
	}
	return r
}

// A grid that scrolls moves its images in the terminal: the tiles of the new
// row and the rest of the one that was cut off are the only ones sent, the
// text is right, and nothing is left behind.
func TestGridScrollCarriesImages(t *testing.T) {
	steps := []struct {
		name string
		do   tea.Msg
		max  int // images sent, at most
	}{
		{"down", gridScroll(1), 2 * gCols},
		{"down again", gridScroll(1), 2 * gCols},
		{"up", gridScroll(-1), 2 * gCols},
		{"overlay opens", gridOverlay{}, 3 * gCols},
		{"down under the overlay", gridScroll(1), 3 * gCols},
		{"overlay closes", gridOverlay{}, 3 * gCols},
		{"down two rows", gridScroll(2), 3 * gCols},
		{"up two rows", gridScroll(-2), 3 * gCols},
		{"a page", gridScroll(5), 5 * gCols},
		{"back to the top", gridScroll(-9), 5 * gCols},
	}
	for _, syncd := range []bool{false, true} {
		t.Run(fmt.Sprintf("syncd=%v", syncd), func(t *testing.T) {
			r := gridRenderer(t, false)
			m := &gridModel{r: r}
			term, quit := runModel(t, r, m, 60, 24, syncd)
			defer quit()
			gridCheck(t, term, m, "first frame")
			for _, s := range steps {
				term.mu.Lock()
				before := len(term.vt.Images)
				term.mu.Unlock()
				term.send(s.do)
				gridCheck(t, term, m, s.name)
				term.mu.Lock()
				sent := len(term.vt.Images) - before
				term.mu.Unlock()
				t.Logf("%s: %d images", s.name, sent)
				if syncd && sent > s.max {
					t.Errorf("%s: sent %d images, want at most %d", s.name, sent, s.max)
				}
			}
			// a resize forgets what is on screen; scrolling goes on after it
			term.mu.Lock()
			term.vt.Resize(70, 26)
			term.mu.Unlock()
			term.send(tea.WindowSizeMsg{Width: 70, Height: 26})
			gridCheck(t, term, m, "resize")
			for _, d := range []gridScroll{1, 1, -1} {
				term.send(d)
				gridCheck(t, term, m, fmt.Sprintf("scroll %d after resize", d))
			}
		})
	}
}

// Without the scroll every tile is sent again, which is what it saves.
func TestGridScrollOffSendsEveryTile(t *testing.T) {
	r := gridRenderer(t, true)
	m := &gridModel{r: r}
	term, quit := runModel(t, r, m, 60, 24, true)
	defer quit()
	term.mu.Lock()
	before := len(term.vt.Images)
	term.mu.Unlock()
	term.send(gridScroll(1))
	gridCheck(t, term, m, "down")
	term.mu.Lock()
	defer term.mu.Unlock()
	if sent := len(term.vt.Images) - before; sent < 3*gCols {
		t.Fatalf("sent %d images without scrolling", sent)
	}
}

// An image that holds still among the scrolled rows is no reason to scroll:
// the terminal would move it too.
func TestGridScrollNeedsStillImagesOutside(t *testing.T) {
	r := gridRenderer(t, false)
	m := &gridModel{r: r, extra: true}
	term, quit := runModel(t, r, m, 60, 24, true)
	defer quit()
	gridCheck(t, term, m, "first frame")
	term.send(gridScroll(1))
	gridCheck(t, term, m, "down")
	term.send(gridScroll(1))
	gridCheck(t, term, m, "down again")
}

func TestPlanScroll(t *testing.T) {
	tile := func(k string, x, y, h int) piece {
		box := rect{x, y, 10, 4}
		return piece{k, box, rect{x, y, 10, h}}
	}
	set := func(ps ...piece) map[piece]bool {
		m := map[piece]bool{}
		for _, p := range ps {
			m[p] = true
		}
		return m
	}
	prev := []piece{tile("a", 0, 2, 4), tile("b", 12, 2, 4), tile("c", 0, 8, 4), tile("d", 12, 8, 4), tile("e", 0, 14, 2), tile("f", 12, 14, 2)}
	now := []piece{tile("c", 0, 2, 4), tile("d", 12, 2, 4), tile("e", 0, 8, 4), tile("f", 12, 8, 4), tile("g", 0, 14, 2), tile("h", 12, 14, 2)}
	p := planScroll(set(prev...), now)
	if p == nil || p.dy != 6 || p.top != 2 || p.bot != 15 {
		t.Fatalf("plan %+v", p)
	}
	if got := p.carried[now[0]]; len(got) != 1 || got[0] != (rect{0, 2, 10, 4}) {
		t.Fatalf("c carried %v", got)
	}
	// e was cut at 2 rows and now has 4: the 2 it had come along
	if got := p.carried[now[2]]; len(got) != 1 || got[0] != (rect{0, 8, 10, 2}) {
		t.Fatalf("e carried %v", got)
	}
	if p.carried[now[4]] != nil {
		t.Fatalf("new tile carried")
	}
	if got := p.kept(now[2], set(prev...), func(rect) bool { return false }); len(got) != 1 {
		t.Fatalf("kept %v", got)
	}
	if got := p.kept(now[2], set(prev...), func(r rect) bool { return r.y == 8 }); len(got) != 0 {
		t.Fatalf("a part written over is kept: %v", got)
	}
	if got := without(now[2].vis, p.carried[now[2]]); len(got) != 1 || got[0] != (rect{0, 10, 10, 2}) {
		t.Fatalf("rest of e: %v", got)
	}
	if seq := p.sequence(); seq != "\x1b7\x1b[3;16r\x1b[6S\x1b[r\x1b8" {
		t.Fatalf("sequence %q", seq)
	}
	up := planScroll(set(now...), prev)
	// the rows e and f had (4) don't fit the 2 they would have left: they are
	// not carried, and the rows they would be cut at are not scrolled
	if up == nil || up.dy != -6 || up.sequence() != "\x1b7\x1b[3;12r\x1b[6T\x1b[r\x1b8" {
		t.Fatalf("scroll up: %+v", up)
	}

	// no plan: nothing moved, a still piece among the scrolled rows, a piece
	// across the edge, or too little to carry
	if planScroll(set(prev...), prev) != nil {
		t.Fatal("planned a scroll of nothing")
	}
	still := tile("s", 30, 6, 4)
	if planScroll(set(append(slicesClone(prev), still)...), append(slicesClone(now), still)) != nil {
		t.Fatal("planned a scroll over a still image")
	}
	outside := tile("s", 30, 20, 4)
	if planScroll(set(append(slicesClone(prev), outside)...), append(slicesClone(now), outside)) == nil {
		t.Fatal("a still image outside the rows stopped the scroll")
	}
	across := tile("s", 30, 12, 4)
	if planScroll(set(append(slicesClone(prev), across)...), append(slicesClone(now), across)) != nil {
		t.Fatal("planned a scroll across an image on the edge")
	}
	if planScroll(set(prev[:1]...), now[:1]) != nil {
		t.Fatal("planned a scroll that carries nothing")
	}
}

func slicesClone(p []piece) []piece { return append([]piece(nil), p...) }

func TestScrollsOrClears(t *testing.T) {
	for in, want := range map[string]bool{
		"\x1b[?2026h\x1b[3;4Hhello\x1b[K\x1b[?2026l": false,
		"\x1b[3;4H\x1b[J":  false,
		"a\nb":             true,
		"\x1b[S":           true,
		"\x1b[2T":          true,
		"\x1b[3;9r":        true,
		"\x1b[4M":          true,
		"\x1bM":            true,
		"\x1b[2J":          true,
		"\x1b[?1049h":      true,
		"\x1b[?25l":        false,
		"\x1b[1;5H\x1b[2K": false,
	} {
		if got := scrollsOrClears([]byte(in)); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

func TestSameCells(t *testing.T) {
	for _, c := range []struct {
		a, b string
		w    int
		want bool
	}{
		{"   ", "   ", 3, true},
		{"\x1b[m   ", "   ", 3, true},
		{"\x1b[38;2;139;139;147m\x1b[m   ", "\x1b[38;2;74;74;82m   \x1b[m", 3, true},
		{"\x1b[48;2;42;42;48m   \x1b[m", "   ", 3, false}, // a background
		{"\x1b[7m   \x1b[m", "   ", 3, false},             // reversed
		{"\x1b[4m   \x1b[m", "   ", 3, false},             // underlined
		{"abc", "abd", 3, false},
		{"abc", "abc", 3, true},
		{"\x1b[1mabc\x1b[m", "abc", 3, false},
		{"\x1b[2C   ", "   ", 3, false}, // a cursor move is not a blank
	} {
		if got := sameCells(c.a, c.b, c.w); got != c.want {
			t.Errorf("sameCells(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

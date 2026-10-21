// Package vt is a small terminal emulator for tests. It models what matters
// for inline images: a cell grid where an iTerm2 image is a set of tiles
// attached to cells (as in WezTerm and xterm.js's image addon), and the
// operations that write, erase, shift and scroll cells. Colours and other
// attributes are ignored.
package vt

import (
	"bytes"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Cell is one screen cell.
type Cell struct {
	Content string // "" for the right half of a wide character
	Image   int    // 0, or the id of the image whose tile is here
	TX, TY  int    // tile coordinates within that image
}

// Image is one received OSC 1337 File image.
type Image struct {
	ID         int
	X, Y       int // cell it was drawn at
	Cols, Rows int
	Data       []byte
}

// Terminal is the emulator. Feed it bytes with Write.
type Terminal struct {
	W, H   int
	Cells  [][]Cell
	Images []Image
	// ONLCR makes LF also return the carriage, like a tty with output
	// post-processing on.
	ONLCR bool

	x, y       int
	wrap       bool
	noAutowrap bool
	top, bot   int
	sx, sy     int
	last       string
	state      byte
	p          *ansi.Parser
	alt        [][]Cell
	inAlt      bool
}

// New returns a w×h terminal.
func New(w, h int) *Terminal {
	t := &Terminal{W: w, H: h, p: ansi.NewParser()}
	t.Cells = grid(w, h)
	t.bot = h - 1
	return t
}

func grid(w, h int) [][]Cell {
	g := make([][]Cell, h)
	for y := range g {
		g[y] = blankRow(w)
	}
	return g
}

func blankRow(w int) []Cell {
	r := make([]Cell, w)
	for i := range r {
		r[i] = Cell{Content: " "}
	}
	return r
}

// Resize changes the size, keeping the top-left of the grid.
func (t *Terminal) Resize(w, h int) {
	g := grid(w, h)
	for y := 0; y < min(h, t.H); y++ {
		copy(g[y], t.Cells[y][:min(w, t.W)])
	}
	t.Cells, t.W, t.H = g, w, h
	t.top, t.bot = 0, h-1
	t.x, t.y, t.wrap = min(t.x, w-1), min(t.y, h-1), false
}

// Write implements io.Writer.
func (t *Terminal) Write(b []byte) (int, error) {
	n := len(b)
	for len(b) > 0 {
		seq, width, k, state := ansi.DecodeSequence(b, t.state, t.p)
		t.state = state
		if k <= 0 {
			break
		}
		b = b[k:]
		if state != ansi.NormalState || len(seq) == 0 {
			continue
		}
		switch {
		case width > 0:
			t.print(string(seq), width)
		case seq[0] == ansi.ESC && len(seq) > 1:
			t.escape(seq)
		case len(seq) == 1 && seq[0] < 0x20:
			t.control(seq[0])
		}
	}
	return n, nil
}

// Text returns row y as plain text.
func (t *Terminal) Text(y int) string {
	var b strings.Builder
	for _, c := range t.Cells[y] {
		b.WriteString(c.Content)
	}
	return b.String()
}

// At returns the cell at (x, y).
func (t *Terminal) At(x, y int) Cell { return t.Cells[y][x] }

// ImageByID returns the image with the given id.
func (t *Terminal) ImageByID(id int) (Image, bool) {
	if id <= 0 || id > len(t.Images) {
		return Image{}, false
	}
	return t.Images[id-1], true
}

func (t *Terminal) print(g string, w int) {
	if t.wrap && !t.noAutowrap {
		t.x = 0
		t.index()
	}
	t.wrap = false
	if t.x+w > t.W {
		if t.noAutowrap || t.x == 0 {
			return
		}
		t.x = 0
		t.index()
	}
	t.Cells[t.y][t.x] = Cell{Content: g}
	for i := 1; i < w; i++ {
		t.Cells[t.y][t.x+i] = Cell{}
	}
	t.last = g
	t.x += w
	if t.x >= t.W {
		t.x, t.wrap = t.W-1, true
	}
}

func (t *Terminal) control(c byte) {
	switch c {
	case '\r':
		t.x, t.wrap = 0, false
	case '\n', '\v', '\f':
		t.index()
		t.wrap = false
		if t.ONLCR && c == '\n' {
			t.x = 0
		}
	case '\b':
		t.x, t.wrap = max(0, t.x-1), false
	case '\t':
		t.x, t.wrap = min(t.W-1, (t.x/8+1)*8), false
	}
}

func (t *Terminal) index() {
	if t.y == t.bot {
		t.scrollUp(t.top, t.bot, 1)
	} else if t.y < t.H-1 {
		t.y++
	}
}

// scrollUp moves rows top..bot up by n, blank rows entering at the bottom.
func (t *Terminal) scrollUp(top, bot, n int) {
	for range n {
		copy(t.Cells[top:bot+1], t.Cells[top+1:bot+1])
		t.Cells[bot] = blankRow(t.W)
	}
}

func (t *Terminal) scrollDown(top, bot, n int) {
	for range n {
		copy(t.Cells[top+1:bot+1], t.Cells[top:bot])
		t.Cells[top] = blankRow(t.W)
	}
}

func (t *Terminal) erase(y, x0, x1 int) {
	for x := max(0, x0); x < min(x1, t.W); x++ {
		t.Cells[y][x] = Cell{Content: " "}
	}
}

func (t *Terminal) escape(seq []byte) {
	switch seq[1] {
	case '[':
		t.csi()
	case ']':
		t.osc(seq)
	case '7':
		t.sx, t.sy = t.x, t.y
	case '8':
		t.x, t.y, t.wrap = min(t.sx, t.W-1), min(t.sy, t.H-1), false
	case 'D':
		t.index()
	case 'E':
		t.x = 0
		t.index()
	case 'M':
		if t.y == t.top {
			t.scrollDown(t.top, t.bot, 1)
		} else if t.y > 0 {
			t.y--
		}
	}
}

func (t *Terminal) param(i, def int) int {
	v, _ := t.p.Param(i, def)
	if v == 0 && def > 0 {
		return def
	}
	return v
}

func (t *Terminal) csi() {
	cmd := ansi.Cmd(t.p.Command())
	if cmd.Prefix() == '?' {
		set := cmd.Final() == 'h'
		if cmd.Final() != 'h' && cmd.Final() != 'l' {
			return
		}
		for _, p := range t.p.Params() {
			switch p.Param(0) {
			case 7:
				t.noAutowrap = !set
			case 1049:
				switch {
				case set && !t.inAlt:
					t.alt, t.Cells = t.Cells, grid(t.W, t.H)
				case !set && t.inAlt:
					t.Cells = t.alt
				}
				t.inAlt = set
			}
		}
		return
	}
	if cmd.Prefix() != 0 || cmd.Intermediate() != 0 {
		return
	}
	n := t.param(0, 1)
	switch cmd.Final() {
	case 'A':
		t.y = max(0, t.y-n)
	case 'B', 'e':
		t.y = min(t.H-1, t.y+n)
	case 'C', 'a':
		t.x = min(t.W-1, t.x+n)
	case 'D':
		t.x = max(0, t.x-n)
	case 'E':
		t.x, t.y = 0, min(t.H-1, t.y+n)
	case 'F':
		t.x, t.y = 0, max(0, t.y-n)
	case 'G', '`':
		t.x = max(0, min(n-1, t.W-1))
	case 'd':
		t.y = max(0, min(n-1, t.H-1))
	case 'H', 'f':
		t.y = max(0, min(t.param(0, 1)-1, t.H-1))
		t.x = max(0, min(t.param(1, 1)-1, t.W-1))
	case 'J':
		switch t.param(0, 0) {
		case 0:
			t.erase(t.y, t.x, t.W)
			for y := t.y + 1; y < t.H; y++ {
				t.erase(y, 0, t.W)
			}
		case 1:
			for y := range t.y {
				t.erase(y, 0, t.W)
			}
			t.erase(t.y, 0, t.x+1)
		default:
			for y := range t.H {
				t.erase(y, 0, t.W)
			}
		}
	case 'K':
		switch t.param(0, 0) {
		case 0:
			t.erase(t.y, t.x, t.W)
		case 1:
			t.erase(t.y, 0, t.x+1)
		default:
			t.erase(t.y, 0, t.W)
		}
	case 'X':
		t.erase(t.y, t.x, t.x+n)
	case '@':
		row := t.Cells[t.y]
		n = min(n, t.W-t.x)
		copy(row[t.x+n:], row[t.x:t.W-n])
		t.erase(t.y, t.x, t.x+n)
	case 'P':
		row := t.Cells[t.y]
		n = min(n, t.W-t.x)
		copy(row[t.x:], row[t.x+n:])
		t.erase(t.y, t.W-n, t.W)
	case 'L':
		if t.y >= t.top && t.y <= t.bot {
			t.scrollDown(t.y, t.bot, n)
		}
	case 'M':
		if t.y >= t.top && t.y <= t.bot {
			t.scrollUp(t.y, t.bot, n)
		}
	case 'S':
		t.scrollUp(t.top, t.bot, n)
	case 'T':
		t.scrollDown(t.top, t.bot, n)
	case 'b':
		if t.last != "" {
			w := ansi.StringWidth(t.last)
			for range n {
				t.print(t.last, max(w, 1))
			}
		}
		return
	case 'r':
		top, bot := t.param(0, 1)-1, t.param(1, t.H)-1
		if top < bot && bot < t.H {
			t.top, t.bot = top, bot
		} else {
			t.top, t.bot = 0, t.H-1
		}
		t.x, t.y = 0, 0
	}
	t.wrap = false
}

// osc handles OSC 1337 File= inline images; other OSCs are ignored.
func (t *Terminal) osc(seq []byte) {
	body := bytes.TrimPrefix(seq, []byte("\x1b]"))
	body = bytes.TrimSuffix(bytes.TrimSuffix(body, []byte("\a")), []byte("\x1b\\"))
	rest, ok := bytes.CutPrefix(body, []byte("1337;File="))
	if !ok {
		return
	}
	args, payload, _ := bytes.Cut(rest, []byte(":"))
	cols, rows, inline := 0, 0, false
	for _, kv := range strings.Split(string(args), ";") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "width":
			cols, _ = strconv.Atoi(v)
		case "height":
			rows, _ = strconv.Atoi(v)
		case "inline":
			inline = v == "1"
		}
	}
	if !inline || cols <= 0 || rows <= 0 {
		return
	}
	data, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		return
	}
	id := len(t.Images) + 1
	t.Images = append(t.Images, Image{ID: id, X: t.x, Y: t.y, Cols: cols, Rows: rows, Data: data})
	for r := range rows {
		for c := range cols {
			if x, y := t.x+c, t.y+r; x < t.W && y < t.H {
				t.Cells[y][x] = Cell{Content: " ", Image: id, TX: c, TY: r}
			}
		}
	}
}

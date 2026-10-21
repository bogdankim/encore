package art

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

func envOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		env  func(string) string
		want Protocol
	}{
		{"wezterm", envOf("TERM_PROGRAM", "WezTerm", "TERM", "xterm-256color"), ProtoIIP},
		{"iterm", envOf("TERM_PROGRAM", "iTerm.app"), ProtoIIP},
		{"kitty window", envOf("KITTY_WINDOW_ID", "1", "TERM", "xterm-256color"), ProtoKitty},
		{"kitty term", envOf("TERM", "xterm-kitty"), ProtoKitty},
		{"ghostty", envOf("TERM_PROGRAM", "ghostty", "TERM", "xterm-ghostty"), ProtoKitty},
		{"apple terminal", envOf("TERM_PROGRAM", "Apple_Terminal"), ProtoBlocks},
		{"unknown", envOf("TERM", "xterm-256color"), ProtoBlocks},
		{"empty", envOf(), ProtoBlocks},
	}
	for _, c := range cases {
		if got := Detect(c.env); got != c.want {
			t.Errorf("%s: Detect = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResolve(t *testing.T) {
	wez := envOf("TERM_PROGRAM", "WezTerm")
	tmux := envOf("TERM_PROGRAM", "tmux", "TMUX", "x", "TMUX_PANE", "%3")
	noTmux := func(...string) (string, error) { return "", errors.New("no tmux") }
	wezSixel := fakeTmux(map[string]string{"display-message": "WezTerm 20240203-110809-5046fc22\txterm-256color\tRGB,sixel,sync"})
	cases := []struct {
		setting string
		env     func(string) string
		run     TmuxRunner
		want    Protocol
		pass    bool
		err     bool
	}{
		{"", wez, nil, ProtoIIP, false, false},
		{"auto", wez, nil, ProtoIIP, false, false},
		{"Auto", tmux, noTmux, ProtoBlocks, false, false},
		{"auto", tmux, wezSixel, ProtoIIP, true, false}, // every image through passthrough
		{"iip", tmux, nil, ProtoIIP, true, false},
		{"kitty", tmux, nil, ProtoKitty, true, false},
		{"sixel", tmux, nil, ProtoSixel, false, false}, // every image sixel
		{"kitty", wez, nil, ProtoKitty, false, false},
		{"sixel", wez, nil, ProtoSixel, false, false},
		{"blocks", wez, nil, ProtoBlocks, false, false},
		{"off", wez, nil, ProtoOff, false, false},
		{"jpeg", wez, nil, ProtoBlocks, false, true},
	}
	for _, c := range cases {
		ch, err := Resolve(c.setting, c.env, c.run)
		if ch.Protocol != c.want || ch.Passthrough != c.pass || (err != nil) != c.err {
			t.Errorf("Resolve(%q) = %+v, %v; want %v, passthrough %v, err=%v", c.setting, ch, err, c.want, c.pass, c.err)
		}
	}
}

func solid(w, h int, c color.NRGBA) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return m
}

func testArt(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x ^ y) & 0xff), 255})
		}
	}
	return m
}

func TestBlocksShape(t *testing.T) {
	src := testArt(300, 300)
	for _, sz := range [][2]int{{1, 1}, {6, 3}, {12, 6}, {13, 7}, {40, 20}, {41, 9}} {
		cols, rows := sz[0], sz[1]
		s := Blocks(src, cols, rows)
		lines := strings.Split(s, "\n")
		if len(lines) != rows {
			t.Fatalf("%dx%d: %d lines", cols, rows, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != cols {
				t.Fatalf("%dx%d: line %d is %d wide", cols, rows, i, w)
			}
			if !strings.HasSuffix(l, "\x1b[m") {
				t.Fatalf("%dx%d: line %d does not reset the pen", cols, rows, i)
			}
		}
		if lipgloss.Height(s) != rows || lipgloss.Width(s) != cols {
			t.Fatalf("%dx%d: block is %dx%d", cols, rows, lipgloss.Width(s), lipgloss.Height(s))
		}
	}
}

func TestBlocksColours(t *testing.T) {
	s := Blocks(solid(10, 10, color.NRGBA{250, 88, 106, 255}), 2, 1)
	if want := "\x1b[38;2;250;88;106;48;2;250;88;106m▀▀\x1b[m"; s != want {
		t.Fatalf("solid: got %q, want %q", s, want)
	}

	// A one-pixel black/white checkerboard averages to linear 0.5, which is
	// sRGB 188, not the 128 a naive byte average gives.
	m := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			v := uint8(255 * ((x + y) & 1))
			m.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	if s := Blocks(m, 1, 1); !strings.Contains(s, "38;2;188;188;188") {
		t.Fatalf("checkerboard: got %q, want grey 188", s)
	}

	// Transparent halves show the terminal background.
	half := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	half.SetNRGBA(0, 1, color.NRGBA{255, 0, 0, 255})
	half.SetNRGBA(1, 0, color.NRGBA{0, 0, 255, 255})
	if s := Blocks(half, 2, 1); s != "\x1b[38;2;255;0;0m▄\x1b[38;2;0;0;255m▀\x1b[m" {
		t.Fatalf("transparent: got %q", s)
	}
	if s := Blocks(image.NewNRGBA(image.Rect(0, 0, 2, 2)), 2, 1); s != "  " {
		t.Fatalf("fully transparent: got %q", s)
	}
}

func TestBlocksCoverCrop(t *testing.T) {
	// A wide image keeps its centre: left third red, middle green, right blue.
	m := image.NewNRGBA(image.Rect(0, 0, 300, 100))
	for y := range 100 {
		for x := range 300 {
			c := [3]color.NRGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}[x/100]
			m.SetNRGBA(x, y, c)
		}
	}
	s := Blocks(m, 2, 1)
	if !strings.Contains(s, "38;2;0;255;0;48;2;0;255;0") || strings.Contains(s, "255;0;0") {
		t.Fatalf("cover crop: got %q, want only the green centre", s)
	}
}

func BenchmarkBlocks12x6(b *testing.B) {
	src := testArt(600, 600)
	for b.Loop() {
		Blocks(src, 12, 6)
	}
}

func BenchmarkBlocks40x20(b *testing.B) {
	src := testArt(600, 600)
	for b.Loop() {
		Blocks(src, 40, 20)
	}
}

func TestIIPFormat(t *testing.T) {
	data := []byte("\x89PNG fake payload")
	s := IIP(data, 12, 6)
	want := "\x1b]1337;File=inline=1;size=" + strconv.Itoa(len(data)) +
		";width=12;height=6;preserveAspectRatio=1;doNotMoveCursor=1:" +
		base64.StdEncoding.EncodeToString(data) + "\a"
	if s != want {
		t.Fatalf("got %q\nwant %q", s, want)
	}
	if p := Placement(3, 1, "X"); p != "\x1b7\x1b[2;4HX\x1b8" {
		t.Fatalf("Placement: %q", p)
	}
}

func TestKittyTransmitChunks(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 10000)
	s := KittyTransmit(42, payload)
	re := regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\")
	chunks := re.FindAllStringSubmatch(s, -1)
	if len(chunks) != 4 {
		t.Fatalf("%d chunks, want 4", len(chunks))
	}
	var data strings.Builder
	for i, c := range chunks {
		keys, chunk := c[1], c[2]
		if len(chunk) > kitty.MaxChunkSize {
			t.Errorf("chunk %d is %d bytes", i, len(chunk))
		}
		if i < len(chunks)-1 && len(chunk)%4 != 0 {
			t.Errorf("chunk %d length %d is not a multiple of 4", i, len(chunk))
		}
		last := i == len(chunks)-1
		switch {
		case i == 0 && keys != "a=t,f=100,t=d,i=42,q=2,m=1":
			t.Errorf("first chunk keys %q", keys)
		case i > 0 && !last && keys != "q=2,m=1":
			t.Errorf("chunk %d keys %q", i, keys)
		case last && keys != "q=2,m=0":
			t.Errorf("last chunk keys %q", keys)
		}
		data.WriteString(chunk)
	}
	got, err := base64.StdEncoding.DecodeString(data.String())
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("chunks do not reassemble: %v", err)
	}
	if s := KittyTransmit(1, []byte("x")); s != "\x1b_Ga=t,f=100,t=d,i=1,q=2,m=0;eA==\x1b\\" {
		t.Fatalf("single chunk: %q", s)
	}
}

func TestKittyPlaceholders(t *testing.T) {
	id := uint32(0x01_02_03)
	s := KittyPlaceholders(id, 4, 2)
	lines := strings.Split(s, "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines", len(lines))
	}
	for r, l := range lines {
		if !strings.HasPrefix(l, "\x1b[38;2;1;2;3m") {
			t.Errorf("line %d lacks the id colour: %q", r, l)
		}
		if w := ansi.StringWidth(l); w != 4 {
			t.Errorf("line %d is %d cells wide", r, w)
		}
		plain := []rune(ansi.Strip(l))
		if len(plain) != 12 {
			t.Fatalf("line %d has %d runes", r, len(plain))
		}
		for c := range 4 {
			cell := plain[c*3 : c*3+3]
			if cell[0] != kitty.Placeholder || cell[1] != kitty.Diacritic(r) || cell[2] != kitty.Diacritic(c) {
				t.Errorf("cell %d,%d = %U", c, r, cell)
			}
		}
	}
	if got := KittyPlace(7, 6, 3); got != "\x1b_Ga=p,U=1,i=7,c=6,r=3,q=2\x1b\\" {
		t.Errorf("KittyPlace: %q", got)
	}
	if got := KittyDelete(7); got != "\x1b_Ga=d,d=I,i=7,q=2\x1b\\" {
		t.Errorf("KittyDelete: %q", got)
	}
}

func pngBytes(t testing.TB, m image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRendererBlocksCache(t *testing.T) {
	r := NewRenderer(Options{Protocol: ProtoBlocks})
	data := pngBytes(t, testArt(64, 64))
	a := r.Block("k", data, 6, 3)
	if b := r.Block("k", nil, 6, 3); b != a {
		t.Fatal("cached block differs")
	}
	if lipgloss.Width(a) != 6 || lipgloss.Height(a) != 3 {
		t.Fatalf("block is %dx%d", lipgloss.Width(a), lipgloss.Height(a))
	}
	if s := r.Block("missing", nil, 4, 2); s != "    \n    " {
		t.Fatalf("missing key: %q", s)
	}
	if s := r.Block("bad", []byte("not an image"), 2, 1); s != "  " {
		t.Fatalf("bad data: %q", s)
	}
	if s := NewRenderer(Options{Protocol: ProtoOff}).Block("k", data, 3, 1); s != "   " {
		t.Fatalf("off: %q", s)
	}
}

func TestRendererKitty(t *testing.T) {
	r := NewRenderer(Options{Protocol: ProtoKitty})
	data := pngBytes(t, testArt(64, 64))
	s := r.Block("k", data, 6, 3)
	if ansi.StringWidth(strings.Split(s, "\n")[0]) != 6 || lipgloss.Height(s) != 3 {
		t.Fatalf("placeholder block has the wrong shape: %q", s)
	}
	var none damage
	oob := r.ops(&none)
	if !strings.HasPrefix(oob, "\x1b_Ga=t,f=100,t=d,i=1,q=2,") || !strings.Contains(oob, "\x1b_Ga=p,U=1,i=1,c=6,r=3,q=2\x1b\\") {
		t.Fatalf("upload not queued: %.80q", oob)
	}
	if r.Block("k", nil, 6, 3) != s || r.ops(&none) != "" {
		t.Fatal("second Block uploaded again")
	}
	if s2 := r.Block("k", nil, 4, 2); !strings.HasPrefix(s2, "\x1b[38;2;0;0;2m") {
		t.Fatalf("new size should get its own id: %q", s2)
	}

	pt := NewRenderer(Options{Protocol: ProtoKitty, Passthrough: true})
	pt.Block("k", data, 2, 1)
	if out := pt.ops(&none); !strings.HasPrefix(out, "\x1bPtmux;\x1b\x1b_G") {
		t.Fatalf("passthrough not applied: %.40q", out)
	}
}

func TestRendererLimit(t *testing.T) {
	r := NewRenderer(Options{Protocol: ProtoIIP, Cell: CellSize{W: 20, H: 42}})
	r.Limit("tile:", 240)
	data := pngBytes(t, testArt(256, 256))
	size := func(key string) image.Point {
		t.Helper()
		if err := r.Prepare(key, data, 19, 9); err != nil {
			t.Fatal(err)
		}
		b, ok := r.boxes.get(boxKey{key, 19, 9})
		if !ok {
			t.Fatalf("%s: no box", key)
		}
		return b.Bounds().Size()
	}
	if got := size("tile:a"); got != image.Pt(240, 238) {
		t.Fatalf("tile box %v, want 240x238", got)
	}
	if got := size("hero"); got != image.Pt(380, 378) {
		t.Fatalf("hero box %v, want 380x378", got)
	}
	r.Limit("tile:", 0)
	if got := size("tile:a"); got != image.Pt(380, 378) {
		t.Fatalf("tile box after removing the limit %v, want 380x378", got)
	}
}

func TestTracker(t *testing.T) {
	tr := newTracker(20, 5)
	tr.dmg.reset()
	tr.feed([]byte("\x1b[2;3Hab"))
	if !tr.dmg.hits(rect{2, 1, 2, 1}) || tr.dmg.hits(rect{4, 1, 5, 1}) || tr.dmg.hits(rect{0, 0, 20, 1}) {
		t.Fatalf("print damage wrong: %+v", tr.dmg)
	}
	tr.dmg.reset()
	tr.feed([]byte("\x1b[K"))
	if !tr.dmg.hits(rect{19, 1, 1, 1}) || tr.dmg.hits(rect{0, 1, 4, 1}) {
		t.Fatalf("EL damage wrong: %+v", tr.dmg)
	}
	tr.dmg.reset()
	tr.feed([]byte("\x1b[2;4r\x1b[4;1H\n\x1b[r"))
	if !tr.dmg.hits(rect{10, 1, 1, 1}) || !tr.dmg.hits(rect{10, 3, 1, 1}) || tr.dmg.hits(rect{0, 0, 20, 1}) || tr.dmg.hits(rect{0, 4, 20, 1}) {
		t.Fatalf("scroll region damage wrong: %+v", tr.dmg)
	}
	tr.dmg.reset()
	tr.feed([]byte("\x1b[5;20Hxy")) // pending wrap, then wraps and scrolls
	if !tr.dmg.all && !tr.dmg.hits(rect{0, 0, 1, 1}) {
		t.Fatalf("wrap at the bottom must scroll: %+v", tr.dmg)
	}
	tr.dmg.reset()
	tr.feed([]byte("\x1b[1;1H\x1b[3b"))
	if !tr.dmg.hits(rect{2, 0, 1, 1}) || tr.dmg.hits(rect{3, 0, 1, 1}) {
		t.Fatalf("REP damage wrong: %+v", tr.dmg)
	}
	tr.dmg.reset()
	tr.feed([]byte("\x1b[H\x1b[2J"))
	if !tr.dmg.all {
		t.Fatal("ED 2 must damage everything")
	}
}

func TestRectSubtract(t *testing.T) {
	r := rect{0, 0, 10, 10}
	parts := r.subtract(rect{3, 4, 4, 2})
	area := 0
	for _, p := range parts {
		area += p.w * p.h
		if !p.intersect(rect{3, 4, 4, 2}).empty() {
			t.Fatalf("part %+v overlaps the hole", p)
		}
	}
	if area != 100-8 {
		t.Fatalf("area %d", area)
	}
	if got := r.subtract(rect{20, 20, 2, 2}); len(got) != 1 || got[0] != r {
		t.Fatalf("disjoint: %+v", got)
	}
	if got := r.subtract(rect{-1, -1, 12, 12}); len(got) != 0 {
		t.Fatalf("covered: %+v", got)
	}
}

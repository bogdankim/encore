package art

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// decodeSixel is a small sixel decoder for the tests: it understands what
// encodeSixel writes (raster attributes, RGB colour definitions, #, !, $, -).
func decodeSixel(t *testing.T, s []byte) *image.NRGBA {
	t.Helper()
	if !bytes.HasPrefix(s, []byte("\x1bP")) || !bytes.HasSuffix(s, []byte("\x1b\\")) {
		t.Fatalf("not a DCS string: %q…", s[:min(len(s), 20)])
	}
	q := bytes.IndexByte(s, 'q')
	body := s[q+1 : len(s)-2]
	num := func(i int) (int, int) {
		j := i
		for j < len(body) && body[j] >= '0' && body[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(string(body[i:j]))
		return n, j
	}
	var w, h int
	if body[0] != '"' {
		t.Fatal("no raster attributes")
	}
	i := 1
	var vals [4]int
	for k := range 4 {
		vals[k], i = num(i)
		if k < 3 {
			if body[i] != ';' {
				t.Fatalf("bad raster attributes at %d", i)
			}
			i++
		}
	}
	w, h = vals[2], vals[3]
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	pal := map[int]color.NRGBA{}
	var cur color.NRGBA
	x, y := 0, 0
	put := func(v byte, n int) {
		for range n {
			for b := range 6 {
				if v&(1<<b) != 0 {
					if x >= w || y+b >= h {
						t.Fatalf("pixel (%d,%d) outside %dx%d", x, y+b, w, h)
					}
					out.SetNRGBA(x, y+b, cur)
				}
			}
			x++
		}
	}
	for i < len(body) {
		c := body[i]
		switch {
		case c == '#':
			var reg int
			reg, i = num(i + 1)
			if i < len(body) && body[i] == ';' {
				var p [4]int
				for k := range 4 {
					p[k], i = num(i + 1)
				}
				pal[reg] = color.NRGBA{fromPercent(p[1]), fromPercent(p[2]), fromPercent(p[3]), 255}
			}
			cur = pal[reg]
		case c == '!':
			var n int
			n, i = num(i + 1)
			put(body[i]-'?', n)
			i++
		case c == '$':
			x = 0
			i++
		case c == '-':
			x, y = 0, y+6
			i++
		case c >= '?' && c <= '~':
			put(c-'?', 1)
			i++
		default:
			t.Fatalf("unexpected %q at %d", c, i)
		}
	}
	return out
}

func psnr(a, b *image.NRGBA) float64 {
	var se float64
	n := 0
	for i := 0; i < len(a.Pix); i += 4 {
		for c := range 3 {
			d := float64(a.Pix[i+c]) - float64(b.Pix[i+c])
			se += d * d
			n++
		}
	}
	if se == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/(se/float64(n)))
}

func gradient(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / max(w-1, 1)), uint8(y * 255 / max(h-1, 1)), uint8((x + y) * 255 / max(w+h-2, 1)), 255})
		}
	}
	return m
}

func TestSixelRoundTrip(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {7, 5}, {64, 48}, {50, 113}} {
		m := gradient(sz[0], sz[1])
		got := decodeSixel(t, Sixel(m))
		if got.Bounds() != m.Bounds() {
			t.Fatalf("%v: decoded %v", sz, got.Bounds())
		}
		for i := 3; i < len(got.Pix); i += 4 {
			if got.Pix[i] != 255 {
				t.Fatalf("%v: pixel %d not painted", sz, i/4)
			}
		}
		if p := psnr(m, got); p < 30 {
			t.Errorf("%v: PSNR %.1f dB, want ≥ 30", sz, p)
		}
	}
}

func TestSixelFewColoursExact(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 40, 13))
	cols := []color.NRGBA{{255, 0, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}}
	for y := range 13 {
		for x := range 40 {
			m.SetNRGBA(x, y, cols[(x/10+y/4)%3])
		}
	}
	s := Sixel(m)
	got := decodeSixel(t, s)
	if !bytes.Equal(got.Pix, m.Pix) {
		t.Fatal("a three-colour image did not survive exactly")
	}
	if !bytes.Contains(s, []byte("!10")) {
		t.Errorf("runs are not compressed: %q", s)
	}
}

func TestSixelPaletteLimit(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for y := range 128 {
		for x := range 128 {
			m.SetNRGBA(x, y, color.NRGBA{uint8(x * 2), uint8(y * 2), uint8(x ^ y), 255})
		}
	}
	pal := medianCut(m, sixelColors)
	if len(pal) > sixelColors || len(pal) < sixelColors/2 {
		t.Fatalf("palette has %d colours", len(pal))
	}
}

// TestSixelMeasure prints size and quality against JPEG for real artwork in
// ENCORE_SIXEL_MEASURE (a directory of PNG or JPEG files).
func TestSixelMeasure(t *testing.T) {
	dir := os.Getenv("ENCORE_SIXEL_MEASURE")
	if dir == "" {
		t.Skip("set ENCORE_SIXEL_MEASURE to a directory of artwork")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*-256.png"))
	if len(files) > 12 {
		files = files[:12]
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		for _, px := range [][2]int{{240, 240}, {320, 320}} {
			box := resample(m, coverRect(m.Bounds(), px[0], px[1]), px[0], px[1]).nrgba()
			j, _ := encodeImage(box, DefaultQuality)
			jm, _, _ := image.Decode(bytes.NewReader(j))
			fmt.Printf("%s %dx%d iip-jpeg %6d B base64 (%.1f dB)\n", filepath.Base(f), px[0], px[1], (len(j)+2)/3*4, psnr(box, toNRGBA(jm)))
			for _, v := range []struct {
				n int
				d float32
			}{{256, 0.75}, {256, 0.5}, {256, 0}, {128, 0.5}, {64, 0.5}} {
				s := sixelWith(box, v.n, v.d)
				got := decodeSixel(t, s)
				fmt.Printf("    sixel %3d colours dither %.2f %6d B (%.1f dB)\n", v.n, v.d, len(s), psnr(box, got))
			}
		}
	}
}

// sixelSizes returns the raster sizes ("W;H") of the sixel images in s.
func sixelSizes(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "q\"1;1;")
		if i < 0 {
			return out
		}
		s = s[i+6:]
		j := strings.IndexByte(s, '#')
		out = append(out, s[:j])
	}
}

// drawSixels runs r's output pass until n sixel images have been drawn
// (they are encoded in the background) and returns all of it.
func drawSixels(t *testing.T, r *Renderer, n int) string {
	t.Helper()
	var none damage
	var all strings.Builder
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		all.WriteString(r.ops(&none))
		if len(sixelSizes(all.String())) >= n {
			return all.String()
		}
	}
	t.Fatalf("only %d of %d sixel images drawn", len(sixelSizes(all.String())), n)
	return ""
}

func TestRendererSixel(t *testing.T) {
	r := NewRenderer(Options{Protocol: ProtoSixel, Tmux: true})
	r.Resize(80, 24)
	data := pngBytes(t, testArt(256, 256))
	r.Limit("tile:", 64) // ignored: a sixel image's pixels are its cells
	if b := r.Block("tile:a", data, 6, 3); b != blank(6, 3) {
		t.Fatalf("sixel block is not blank: %q", b)
	}
	r.Place("tile:a", 10, 5, 6, 3)
	r.Frame(strings.Repeat("\n", 23))
	var none damage
	if out := r.ops(&none); strings.Contains(out, "\x1bP") {
		t.Fatal("drew a sixel image before the cell size was known")
	}

	r.SetCellSize(CellSize{W: 9, H: 19})
	r.Place("tile:a", 10, 5, 6, 3)
	r.Frame(strings.Repeat("\n", 23))
	out := drawSixels(t, r, 1)
	// 3 rows of 19 px is 57; tmux counts whole six-pixel bands, so 54
	if got := sixelSizes(out); len(got) != 1 || got[0] != "54;54" {
		t.Fatalf("sixel sizes %q, want [54;54]", got)
	}
	if strings.Contains(out, "\x1b[6X") {
		t.Fatal("erased with ECH, which makes tmux drop every image on the line")
	}
	if !strings.Contains(out, "\x1b[6;11H      ") || !strings.HasSuffix(out, "\x1b\\\x1b8") {
		t.Fatalf("cells not cleared with spaces, or cursor not restored: %.120q", out)
	}
	if strings.Contains(out, "tmux;") {
		t.Fatal("sixel must not be wrapped for passthrough; tmux draws it itself")
	}

	// cut by an overlay over its right half: the piece is the left half
	r.Place("tile:a", 10, 5, 6, 3)
	r.Occlude(13, 0, 20, 24)
	r.Frame(strings.Repeat("\n", 23))
	if got := sixelSizes(drawSixels(t, r, 1)); len(got) != 1 || got[0] != "27;54" {
		t.Fatalf("cropped sixel sizes %q, want [27;54]", got)
	}

	// outside tmux the image is the cells' exact size
	plain := NewRenderer(Options{Protocol: ProtoSixel, Cell: CellSize{W: 9, H: 19}})
	plain.Resize(80, 24)
	plain.Block("k", data, 6, 3)
	plain.Place("k", 0, 0, 6, 3)
	plain.Frame(strings.Repeat("\n", 23))
	if got := sixelSizes(drawSixels(t, plain, 1)); len(got) != 1 || got[0] != "54;57" {
		t.Fatalf("sixel sizes outside tmux %q, want [54;57]", got)
	}
}

func TestRendererSixelBadgeCut(t *testing.T) {
	// a badge over the bottom-left of a box: full-height strip right of it,
	// the column above it; no piece ends in the middle of the art
	r := NewRenderer(Options{Protocol: ProtoSixel, Tmux: true, Cell: CellSize{W: 8, H: 15}})
	r.Resize(80, 24)
	r.Block("k", pngBytes(t, testArt(64, 64)), 10, 5)
	r.Place("k", 0, 0, 10, 5)
	r.Occlude(0, 4, 3, 1)
	r.Frame(strings.Repeat("\n", 23))
	got := sixelSizes(drawSixels(t, r, 2))
	slices.Sort(got)
	if want := []string{"24;60", "56;72"}; !slices.Equal(got, want) {
		t.Fatalf("pieces %q, want %q", got, want)
	}
}

func TestSixelTransparent(t *testing.T) {
	m := gradient(30, 12)
	for y := range 12 {
		for x := 10; x < 20; x++ {
			m.Pix[m.PixOffset(x, y)+3] = 0
		}
	}
	got := decodeSixel(t, Sixel(m))
	for y := range 12 {
		for x := range 30 {
			a := got.Pix[got.PixOffset(x, y)+3]
			if want := x < 10 || x >= 20; (a == 255) != want {
				t.Fatalf("(%d,%d): alpha %d, painted want %v", x, y, a, want)
			}
		}
	}
}

func TestTrackerLineErase(t *testing.T) {
	tr := newTracker(40, 10)
	tr.tmux = true
	for _, seq := range []string{"\x1b[3;30H\x1b[K", "\x1b[3;30H\x1b[2X", "\x1b[3;30H\x1b[@", "\x1b[3;30H\x1b[P", "\x1b[3;2H\x1b[1K"} {
		tr.dmg.reset()
		tr.feed([]byte(seq))
		if !tr.dmg.hitsLine(rect{10, 2, 5, 1}) {
			t.Errorf("%q: a sixel image elsewhere on the line is not hit", seq)
		}
		if tr.dmg.hitsLine(rect{0, 3, 40, 1}) {
			t.Errorf("%q: damaged another line", seq)
		}
	}
	// tmux passes erases on to the terminal as they are, so cells outside
	// them keep their iTerm2 images; inserts and deletes can rewrite the line
	for seq, want := range map[string]bool{"\x1b[3;30H\x1b[K": false, "\x1b[3;30H\x1b[2X": false, "\x1b[3;30H\x1b[@": true, "\x1b[3;30H\x1b[P": true} {
		tr.dmg.reset()
		tr.feed([]byte(seq))
		if got := tr.dmg.hits(rect{10, 2, 5, 1}); got != want {
			t.Errorf("%q: cells before it damaged %v, want %v", seq, got, want)
		}
	}
	tr.dmg.reset()
	tr.feed([]byte("\x1b[3;30Hab"))
	if tr.dmg.hitsLine(rect{10, 2, 5, 1}) {
		t.Error("printing damages only the printed cells")
	}
}

package art

import (
	"image"
	"slices"
	"strconv"
)

// Sixel encoding. The image is cut to at most sixelColors colours with
// median cut, mapped onto them with damped Floyd-Steinberg dithering, and
// written band by band (six pixel rows each), one pass per colour present in
// the band, with run-length compression. The raster attributes give the exact
// pixel size, so tmux and the terminal know how many cells the image covers.

// sixelColors is the palette size. 256 registers are what every sixel
// terminal has; tmux keeps up to 1024.
const sixelColors = 256

// sixelDither scales the diffused quantisation error. Full strength turns
// flat areas into visible grain; half strength still breaks up banding in
// gradients and measured as close to the source (PSNR) as any other on real
// album art.
const sixelDither = 0.5

// Sixel returns m as a sixel image (DCS … ST) of exactly m's pixel size,
// opaque, with at most 256 colours.
func Sixel(m image.Image) []byte { return sixelWith(m, sixelColors, sixelDither) }

func sixelWith(m image.Image, colors int, dither float32) []byte {
	q := quantize(toNRGBA(m), colors, dither)
	if q == nil {
		return nil
	}
	return q.encode(image.Rect(0, 0, q.w, q.h))
}

func transparent(a uint8) bool { return a < 128 }

// sixelImage is an image reduced to a palette, ready to encode whole or in
// parts. The Renderer keeps one per box, so the pieces an overlay cuts a box
// into are only encoded, not quantised again, and match the whole image
// pixel for pixel.
type sixelImage struct {
	w, h  int
	idx   []uint8
	pal   []sixelRGB
	clear bool // idx clearIdx marks transparent pixels
}

// clearIdx is the index of transparent pixels (alpha below one half), which
// are left unpainted. Images with any get at most 255 colours.
const clearIdx = 255

func quantize(px *image.NRGBA, colors int, dither float32) *sixelImage {
	w, h := px.Bounds().Dx(), px.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	clear := !opaque(px)
	if clear {
		colors = min(colors, clearIdx)
	}
	pal := medianCut(px, colors)
	return &sixelImage{w, h, ditherTo(px, pal, dither), pal, clear}
}

// encode writes the part of the image inside r (in pixels).
func (s *sixelImage) encode(r image.Rectangle) []byte {
	r = r.Intersect(image.Rect(0, 0, s.w, s.h))
	if r.Empty() {
		return nil
	}
	idx := s.idx
	if r != image.Rect(0, 0, s.w, s.h) {
		idx = make([]uint8, 0, r.Dx()*r.Dy())
		for y := r.Min.Y; y < r.Max.Y; y++ {
			idx = append(idx, s.idx[y*s.w+r.Min.X:y*s.w+r.Max.X]...)
		}
	}
	return encodeSixel(idx, r.Dx(), r.Dy(), s.pal, s.clear)
}

func toNRGBA(m image.Image) *image.NRGBA {
	if n, ok := m.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	b := m.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			out.Set(x, y, m.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

// sixelRGB is a palette colour, already rounded to the percent steps sixel
// colour definitions carry.
type sixelRGB struct{ r, g, b uint8 }

func toPercent(v uint8) int { return (int(v)*100 + 127) / 255 }

func fromPercent(p int) uint8 { return uint8((p*255 + 50) / 100) }

// histogram bins pixels at 5 bits per channel.
const binBits = 5

func bin(r, g, b uint8) int {
	return int(r>>(8-binBits))<<(2*binBits) | int(g>>(8-binBits))<<binBits | int(b>>(8-binBits))
}

type binStat struct {
	n          int
	r, g, b    int // sums of the 8-bit values
	key        int
	cr, cg, cb uint8 // the bin's coordinates
}

// cutBox is a box of histogram bins for median cut.
type cutBox struct {
	bins []binStat
	n    int
	axis int // the longest side, and its length in bins
	long int
}

func newCutBox(bins []binStat, n int) cutBox {
	c := cutBox{bins: bins, n: n}
	c.axis, c.long = c.span()
	return c
}

func (c *cutBox) span() (axis int, length int) {
	var lo, hi [3]uint8
	lo = [3]uint8{255, 255, 255}
	for _, s := range c.bins {
		v := [3]uint8{s.cr, s.cg, s.cb}
		for i := range 3 {
			lo[i], hi[i] = min(lo[i], v[i]), max(hi[i], v[i])
		}
	}
	for i := range 3 {
		if l := int(hi[i]) - int(lo[i]); l > length {
			axis, length = i, l
		}
	}
	return axis, length
}

// medianCut picks up to n colours for px: the box with the most pixels times
// the longest side is split at its median until there are n boxes.
func medianCut(px *image.NRGBA, n int) []sixelRGB {
	var cnt, sr, sg, sb [1 << (3 * binBits)]int32
	w, h := px.Bounds().Dx(), px.Bounds().Dy()
	for y := range h {
		row := px.Pix[y*px.Stride : y*px.Stride+w*4]
		for x := 0; x < len(row); x += 4 {
			if transparent(row[x+3]) {
				continue
			}
			r, g, b := row[x], row[x+1], row[x+2]
			k := bin(r, g, b)
			cnt[k]++
			sr[k] += int32(r)
			sg[k] += int32(g)
			sb[k] += int32(b)
		}
	}
	var all []binStat
	for k := range cnt {
		if cnt[k] > 0 {
			all = append(all, binStat{
				n: int(cnt[k]), r: int(sr[k]), g: int(sg[k]), b: int(sb[k]), key: k,
				cr: uint8(k >> (2 * binBits)), cg: uint8(k >> binBits & (1<<binBits - 1)), cb: uint8(k & (1<<binBits - 1)),
			})
		}
	}
	total := 0
	for _, s := range all {
		total += s.n
	}
	if total == 0 {
		return nil
	}
	boxes := []cutBox{newCutBox(all, total)}
	for len(boxes) < n {
		best, bestScore := -1, 0
		for i := range boxes {
			if len(boxes[i].bins) < 2 {
				continue
			}
			if score := boxes[i].n * boxes[i].long; score > bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			break
		}
		a, b := split(boxes[best], boxes[best].axis)
		boxes[best] = a
		boxes = append(boxes, b)
	}
	pal := make([]sixelRGB, 0, len(boxes))
	seen := map[sixelRGB]bool{}
	for _, c := range boxes {
		var r, g, b int
		for _, s := range c.bins {
			r += s.r
			g += s.g
			b += s.b
		}
		col := sixelRGB{
			fromPercent(toPercent(uint8(r / c.n))),
			fromPercent(toPercent(uint8(g / c.n))),
			fromPercent(toPercent(uint8(b / c.n))),
		}
		if !seen[col] {
			seen[col] = true
			pal = append(pal, col)
		}
	}
	return pal
}

// split cuts c along axis at the pixel median.
func split(c cutBox, axis int) (cutBox, cutBox) {
	bins := slices.Clone(c.bins)
	val := func(s binStat) uint8 { return [3]uint8{s.cr, s.cg, s.cb}[axis] }
	slices.SortStableFunc(bins, func(a, b binStat) int { return int(val(a)) - int(val(b)) })
	half, acc, at := c.n/2, 0, 1
	for i, s := range bins {
		acc += s.n
		if acc >= half {
			at = i + 1
			break
		}
	}
	at = min(max(at, 1), len(bins)-1)
	na := 0
	for _, s := range bins[:at] {
		na += s.n
	}
	return newCutBox(bins[:at:at], na), newCutBox(bins[at:], c.n-na)
}

// ditherTo maps px onto pal with damped Floyd-Steinberg error diffusion and
// returns one palette index per pixel.
func ditherTo(px *image.NRGBA, pal []sixelRGB, dither float32) []uint8 {
	w, h := px.Bounds().Dx(), px.Bounds().Dy()
	out := make([]uint8, w*h)
	// nearest colour per 5-bit bin centre, filled on demand
	near := make([]int16, 1<<(3*binBits))
	for i := range near {
		near[i] = -1
	}
	lookup := func(r, g, b int) int {
		k := bin(uint8(r), uint8(g), uint8(b))
		if c := near[k]; c >= 0 {
			return int(c)
		}
		const half = 1 << (7 - binBits)
		cr, cg, cb := r&^(2*half-1)|half, g&^(2*half-1)|half, b&^(2*half-1)|half
		best, bestD := 0, 1<<30
		for i, p := range pal {
			dr, dg, db := cr-int(p.r), cg-int(p.g), cb-int(p.b)
			// weighted for perceived brightness
			if d := 3*dr*dr + 4*dg*dg + 2*db*db; d < bestD {
				best, bestD = i, d
			}
		}
		near[k] = int16(best)
		return best
	}
	// error rows, three channels, with one cell of padding on each side
	cur := make([]float32, (w+2)*3)
	next := make([]float32, (w+2)*3)
	for y := range h {
		row := px.Pix[y*px.Stride:]
		for x := range w {
			if transparent(row[x*4+3]) {
				out[y*w+x] = clearIdx
				continue
			}
			e := cur[(x+1)*3:]
			r := clamp8(float32(row[x*4]) + e[0])
			g := clamp8(float32(row[x*4+1]) + e[1])
			b := clamp8(float32(row[x*4+2]) + e[2])
			i := lookup(r, g, b)
			out[y*w+x] = uint8(i)
			p := pal[i]
			er := float32(r-int(p.r)) * dither
			eg := float32(g-int(p.g)) * dither
			eb := float32(b-int(p.b)) * dither
			for c, v := range [3]float32{er, eg, eb} {
				cur[(x+2)*3+c] += v * 7 / 16
				next[x*3+c] += v * 3 / 16
				next[(x+1)*3+c] += v * 5 / 16
				next[(x+2)*3+c] += v * 1 / 16
			}
		}
		cur, next = next, cur
		clear(next)
	}
	return out
}

func clamp8(v float32) int {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return int(v + 0.5)
}

// encodeSixel writes the indexed image. P2=1 leaves pixels no colour sets
// alone (only the rows past the bottom of the last band); the raster
// attributes declare square pixels and the exact size.
func encodeSixel(idx []uint8, w, h int, pal []sixelRGB, hasClear bool) []byte {
	out := make([]byte, 0, w*h/2+len(pal)*18+64)
	out = append(out, "\x1bP0;1;0q\"1;1;"...)
	out = strconv.AppendInt(out, int64(w), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(h), 10)
	var inUse [256]bool
	for _, c := range idx {
		inUse[c] = true
	}
	if hasClear {
		inUse[clearIdx] = false
	}
	for i, c := range pal {
		if !inUse[i] {
			continue
		}
		out = append(out, '#')
		out = strconv.AppendInt(out, int64(i), 10)
		out = append(out, ";2;"...)
		out = strconv.AppendInt(out, int64(toPercent(c.r)), 10)
		out = append(out, ';')
		out = strconv.AppendInt(out, int64(toPercent(c.g)), 10)
		out = append(out, ';')
		out = strconv.AppendInt(out, int64(toPercent(c.b)), 10)
	}
	// planes[c*w+x] holds the sixel of colour c at column x in the current
	// band; only the columns between lo[c] and hi[c] are ever set, and they
	// are cleared again once written.
	planes := make([]byte, len(pal)*w)
	lo := make([]int, len(pal))
	hi := make([]int, len(pal))
	var used []int
	var seen [256]bool
	for y0 := 0; y0 < h; y0 += 6 {
		if y0 > 0 {
			out = append(out, '-')
		}
		for _, c := range used {
			seen[c] = false
		}
		used = used[:0]
		for y := y0; y < min(y0+6, h); y++ {
			bit := byte(1) << (y - y0)
			for x, c := range idx[y*w : y*w+w] {
				if hasClear && c == clearIdx {
					continue
				}
				ci := int(c)
				if !seen[ci] {
					seen[ci] = true
					lo[ci], hi[ci] = x, x
					used = append(used, ci)
				}
				planes[ci*w+x] |= bit
				lo[ci], hi[ci] = min(lo[ci], x), max(hi[ci], x)
			}
		}
		slices.Sort(used)
		for n, c := range used {
			if n > 0 {
				out = append(out, '$')
			}
			out = append(out, '#')
			out = strconv.AppendInt(out, int64(c), 10)
			out = appendRun(out, 0, lo[c]) // skip to the first pixel
			plane := planes[c*w : c*w+w]
			run, prev := 0, byte(0)
			for x := lo[c]; x <= hi[c]; x++ {
				if v := plane[x]; run > 0 && v == prev {
					run++
					continue
				}
				out = appendRun(out, prev, run)
				prev, run = plane[x], 1
			}
			out = appendRun(out, prev, run)
			clear(plane[lo[c] : hi[c]+1])
		}
	}
	return append(out, "\x1b\\"...)
}

// appendRun writes n sixels of value v, compressed when that is shorter.
func appendRun(out []byte, v byte, n int) []byte {
	ch := '?' + v
	switch {
	case n <= 0:
	case n <= 3:
		for range n {
			out = append(out, ch)
		}
	default:
		out = append(out, '!')
		out = strconv.AppendInt(out, int64(n), 10)
		out = append(out, ch)
	}
	return out
}

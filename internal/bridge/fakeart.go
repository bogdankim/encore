package bridge

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// coverVersion is part of cached file names; bump it when renderCover changes.
const coverVersion = "v1"

// cachedCover returns dir/<version>-<key>-<size>.png, rendering it first if
// needed. Writes go through a temp file, so concurrent callers are safe.
func cachedCover(dir, key string, size int) (string, error) {
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%d.png", coverVersion, key, size))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("fake artwork: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".cover-*")
	if err != nil {
		return "", fmt.Errorf("fake artwork: %w", err)
	}
	err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(tmp, renderCover(key, size))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("fake artwork: %w", err)
	}
	return path, nil
}

type rgb [3]float64

// oklch converts an OKLCH colour (hue in degrees) to clamped sRGB.
func oklch(l, c, h float64) rgb {
	hr := h * math.Pi / 180
	a, b := c*math.Cos(hr), c*math.Sin(hr)
	l_ := l + 0.3963377774*a + 0.2158037573*b
	m_ := l - 0.1055613458*a - 0.0638541728*b
	s_ := l - 0.0894841775*a - 1.2914855480*b
	L, M, S := l_*l_*l_, m_*m_*m_, s_*s_*s_
	lin := rgb{
		+4.0767416621*L - 3.3077115913*M + 0.2309699292*S,
		-1.2684380046*L + 2.6097574011*M - 0.3413193965*S,
		-0.0041960863*L - 0.7034186147*M + 1.7076147010*S,
	}
	for i, v := range lin {
		v = min(max(v, 0), 1)
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		lin[i] = v
	}
	return lin
}

const (
	shapeCircle = iota
	shapeBand   // a straight band, optionally rotated
	shapeArch   // a rectangle with a semicircular top
)

type shape struct {
	kind        int
	cx, cy, r   float64 // circle and arch (r = half width)
	off, hw     float64 // band: centre offset from the middle, half width
	cos, sin    float64 // band rotation
	bottom      float64 // arch
	col         rgb
	alpha, soft float64
}

func (s *shape) dist(u, v float64) float64 {
	switch s.kind {
	case shapeBand:
		x, y := u-0.5, v-0.5
		return math.Abs(-s.sin*x+s.cos*y-s.off) - s.hw
	case shapeArch:
		circle := math.Hypot(u-s.cx, v-s.cy) - s.r
		hy := (s.bottom - s.cy) / 2
		dx, dy := math.Abs(u-s.cx)-s.r, math.Abs(v-(s.cy+hy))-hy
		box := math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0)
		return math.Min(circle, box)
	}
	return math.Hypot(u-s.cx, v-s.cy) - s.r
}

// renderCover draws a deterministic cover for key: a muted ground and two to
// four soft-edged shapes in a three-colour palette.
func renderCover(key string, size int) *image.NRGBA {
	r := pidRand(key, "art")
	f := r.Float64
	jit := func(lo, hi float64) float64 { return lo + f()*(hi-lo) }
	h := f() * 360

	var bg, c1, c2 rgb
	switch r.IntN(4) {
	case 0: // dark ground, analogous shapes
		bg, c1, c2 = oklch(jit(0.2, 0.3), 0.03, h), oklch(0.74, 0.12, h+25), oklch(0.52, 0.1, h-35)
	case 1: // paper ground
		bg, c1, c2 = oklch(jit(0.9, 0.95), 0.025, h+60), oklch(0.63, 0.14, h), oklch(0.3, 0.06, h+190)
	case 2: // coloured ground, one hue
		bg, c1, c2 = oklch(jit(0.52, 0.62), 0.11, h), oklch(0.85, 0.07, h+10), oklch(0.32, 0.08, h-10)
	default: // muted complementary
		bg, c1, c2 = oklch(jit(0.36, 0.46), 0.05, h), oklch(0.8, 0.1, h+180), oklch(0.6, 0.07, h+150)
	}

	crisp := 0.006
	var shapes []shape
	switch r.IntN(5) {
	case 0: // sun over a horizon
		shapes = append(shapes,
			shape{kind: shapeCircle, cx: jit(0.3, 0.7), cy: jit(0.32, 0.5), r: jit(0.17, 0.3), col: c1, alpha: 1, soft: crisp},
			shape{kind: shapeBand, cos: 1, off: jit(0.25, 0.35), hw: 0.2, col: c2, alpha: 1, soft: crisp})
		if r.IntN(2) == 0 {
			shapes = append(shapes, shape{kind: shapeBand, cos: 1, off: shapes[1].off - 0.2 + 0.04, hw: 0.008, col: c1, alpha: 0.6, soft: crisp})
		}
	case 1: // overlapping discs
		shapes = append(shapes,
			shape{kind: shapeCircle, cx: jit(0.3, 0.45), cy: jit(0.3, 0.45), r: jit(0.24, 0.32), col: c1, alpha: 1, soft: crisp},
			shape{kind: shapeCircle, cx: jit(0.55, 0.7), cy: jit(0.55, 0.7), r: jit(0.2, 0.28), col: c2, alpha: 0.82, soft: crisp})
		if r.IntN(2) == 0 {
			shapes = append(shapes, shape{kind: shapeCircle, cx: jit(0.65, 0.8), cy: jit(0.2, 0.32), r: jit(0.05, 0.08), col: c1, alpha: 1, soft: crisp})
		}
	case 2: // stripes and a disc
		angle := []float64{0, 0.44, -0.44, math.Pi / 2}[r.IntN(4)]
		n := 3 + r.IntN(3)
		w := jit(0.05, 0.08)
		for i := range n {
			col := c1
			if i%2 == 1 {
				col = c2
			}
			shapes = append(shapes, shape{kind: shapeBand, cos: math.Cos(angle), sin: math.Sin(angle),
				off: (float64(i) - float64(n-1)/2) * w * 2.6, hw: w, col: col, alpha: 0.95, soft: crisp})
		}
		shapes = append(shapes, shape{kind: shapeCircle, cx: jit(0.25, 0.75), cy: jit(0.25, 0.75), r: jit(0.12, 0.2), col: bg, alpha: 0.9, soft: crisp})
	case 3: // arch with a sun inside
		w := jit(0.2, 0.28)
		cy := jit(0.38, 0.48)
		shapes = append(shapes,
			shape{kind: shapeArch, cx: 0.5, cy: cy, r: w, bottom: 0.84, col: c1, alpha: 1, soft: crisp},
			shape{kind: shapeCircle, cx: 0.5, cy: cy + jit(-0.02, 0.06), r: w * jit(0.35, 0.55), col: c2, alpha: 1, soft: crisp},
			shape{kind: shapeBand, cos: 1, off: 0.42, hw: 0.08, col: c2, alpha: 0.9, soft: crisp})
	default: // soft blobs
		for i, col := range []rgb{c1, c2, c1} {
			shapes = append(shapes, shape{kind: shapeCircle, cx: jit(0.15, 0.85), cy: jit(0.15, 0.85), r: jit(0.22, 0.36),
				col: col, alpha: []float64{0.9, 0.85, 0.5}[i], soft: jit(0.12, 0.22)})
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	px := 1 / float64(size)
	for y := range size {
		v := (float64(y) + 0.5) * px
		row := img.Pix[y*img.Stride:]
		for x := range size {
			u := (float64(x) + 0.5) * px
			c := bg
			for i := range shapes {
				s := &shapes[i]
				soft := math.Max(s.soft, px)
				d := s.dist(u, v)
				if d >= soft {
					continue
				}
				a := s.alpha
				if d > -soft {
					t := (soft - d) / (2 * soft)
					a *= t * t * (3 - 2*t)
				}
				for k := range c {
					c[k] += (s.col[k] - c[k]) * a
				}
			}
			p := row[x*4 : x*4+4 : x*4+4]
			p[0], p[1], p[2], p[3] = uint8(c[0]*255+0.5), uint8(c[1]*255+0.5), uint8(c[2]*255+0.5), 255
		}
	}
	return img
}

// FakeCover renders the demo cover for key (an album pid, or any string) at
// size×size px, the same one Fake.Artwork serves for that album pid.
func FakeCover(key string, size int) image.Image {
	if size <= 0 {
		size = 600
	}
	return renderCover(key, min(size, 2048))
}

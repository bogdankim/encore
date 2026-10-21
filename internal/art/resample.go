package art

import (
	"image"
	"image/color"
	"math"
)

// Resampling happens in linear light with premultiplied alpha, so averaging
// a dark and a bright pixel gives the perceived mid-tone instead of the muddy
// result of averaging sRGB bytes.

var srgbToLinear [256]float32

const linSteps = 4095

var linearToSRGB [linSteps + 1]uint8

func init() {
	for i := range srgbToLinear {
		c := float64(i) / 255
		if c <= 0.04045 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		srgbToLinear[i] = float32(c)
	}
	for i := range linearToSRGB {
		c := float64(i) / linSteps
		if c <= 0.0031308 {
			c *= 12.92
		} else {
			c = 1.055*math.Pow(c, 1/2.4) - 0.055
		}
		linearToSRGB[i] = uint8(math.Round(c * 255))
	}
}

func encodeSRGB(lin float32) uint8 {
	switch {
	case lin <= 0:
		return 0
	case lin >= 1:
		return 255
	}
	return linearToSRGB[int(lin*linSteps+0.5)]
}

// linImage is premultiplied linear RGBA, four float32 per pixel.
type linImage struct {
	w, h int
	pix  []float32
}

// at returns the pixel at (x, y) as straight (not premultiplied) sRGB bytes.
func (m *linImage) at(x, y int) (r, g, b, a uint8) {
	p := m.pix[(y*m.w+x)*4:]
	al := p[3]
	if al <= 1.0/512 {
		return 0, 0, 0, 0
	}
	inv := 1 / al
	return encodeSRGB(p[0] * inv), encodeSRGB(p[1] * inv), encodeSRGB(p[2] * inv), uint8(min(al, 1)*255 + 0.5)
}

// nrgba converts m to an 8-bit image for encoding.
func (m *linImage) nrgba() *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, m.w, m.h))
	for y := range m.h {
		row := out.Pix[y*out.Stride:]
		for x := range m.w {
			r, g, b, a := m.at(x, y)
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = r, g, b, a
		}
	}
	return out
}

// coverRect returns the largest centred sub-rectangle of r with the aspect
// ratio w:h, so scaling it to w×h fills the target without distortion.
func coverRect(r image.Rectangle, w, h int) image.Rectangle {
	sw, sh := r.Dx(), r.Dy()
	if sw <= 0 || sh <= 0 || w <= 0 || h <= 0 {
		return r
	}
	if sw*h > sh*w { // source is wider: crop the sides
		nw := max(1, int(math.Round(float64(sh)*float64(w)/float64(h))))
		x0 := r.Min.X + (sw-nw)/2
		return image.Rect(x0, r.Min.Y, x0+nw, r.Max.Y)
	}
	nh := max(1, int(math.Round(float64(sw)*float64(h)/float64(w))))
	y0 := r.Min.Y + (sh-nh)/2
	return image.Rect(r.Min.X, y0, r.Max.X, y0+nh)
}

// span is the run of source pixels that make up one output pixel.
type span struct {
	first, n int
	w        []float32 // weights, summing to 1
}

// spans computes, for each of n output pixels, the overlapping source pixels
// of [0, src) and their coverage, normalised.
func spans(src, n int) []span {
	out := make([]span, n)
	scale := float64(src) / float64(n)
	for i := range out {
		f0, f1 := float64(i)*scale, float64(i+1)*scale
		if scale < 1 { // upscaling: sample the one source pixel under the centre
			c := min(int((f0+f1)/2), src-1)
			out[i] = span{first: c, n: 1, w: []float32{1}}
			continue
		}
		first := int(f0)
		last := min(int(math.Ceil(f1)), src) - 1
		s := span{first: first, n: last - first + 1, w: make([]float32, last-first+1)}
		for j := first; j <= last; j++ {
			lo, hi := math.Max(f0, float64(j)), math.Min(f1, float64(j+1))
			s.w[j-first] = float32((hi - lo) / scale)
		}
		out[i] = s
	}
	return out
}

// resample area-averages the sr part of src into a w×h linear image.
func resample(src image.Image, sr image.Rectangle, w, h int) *linImage {
	sr = sr.Intersect(src.Bounds())
	out := &linImage{w: w, h: h, pix: make([]float32, w*h*4)}
	if sr.Empty() || w <= 0 || h <= 0 {
		return out
	}
	sw, sh := sr.Dx(), sr.Dy()
	xs, ys := spans(sw, w), spans(sh, h)

	srcRow := make([]float32, sw*4)
	hrow := make([]float32, w*4)
	lastY := -1
	for oy, ysp := range ys {
		dst := out.pix[oy*w*4 : (oy+1)*w*4]
		for k := range ysp.n {
			sy := ysp.first + k
			if sy != lastY {
				readRow(src, sr.Min.X, sr.Min.Y+sy, sw, srcRow)
				reduceRow(srcRow, xs, hrow)
				lastY = sy
			}
			wt := ysp.w[k]
			for i, v := range hrow {
				dst[i] += v * wt
			}
		}
	}
	return out
}

func reduceRow(src []float32, xs []span, dst []float32) {
	for ox, sp := range xs {
		var r, g, b, a float32
		p := src[sp.first*4:]
		for k, wt := range sp.w {
			q := p[k*4 : k*4+4]
			r += q[0] * wt
			g += q[1] * wt
			b += q[2] * wt
			a += q[3] * wt
		}
		d := dst[ox*4 : ox*4+4]
		d[0], d[1], d[2], d[3] = r, g, b, a
	}
}

// readRow converts n pixels of row y starting at x0 into premultiplied linear
// RGBA, with fast paths for the types image/png and image/jpeg decode into.
func readRow(src image.Image, x0, y, n int, dst []float32) {
	put := func(i int, r, g, b, a uint8) {
		d := dst[i*4 : i*4+4]
		al := float32(a) / 255
		d[0], d[1], d[2], d[3] = srgbToLinear[r]*al, srgbToLinear[g]*al, srgbToLinear[b]*al, al
	}
	switch m := src.(type) {
	case *image.NRGBA:
		p := m.Pix[m.PixOffset(x0, y):]
		for i := range n {
			put(i, p[i*4], p[i*4+1], p[i*4+2], p[i*4+3])
		}
	case *image.RGBA:
		p := m.Pix[m.PixOffset(x0, y):]
		for i := range n {
			r, g, b, a := p[i*4], p[i*4+1], p[i*4+2], p[i*4+3]
			if a != 0 && a != 255 {
				r, g, b = unpremul(r, a), unpremul(g, a), unpremul(b, a)
			}
			put(i, r, g, b, a)
		}
	case *image.YCbCr:
		for i := range n {
			yi, ci := m.YOffset(x0+i, y), m.COffset(x0+i, y)
			r, g, b := color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])
			put(i, r, g, b, 255)
		}
	case *image.Gray:
		p := m.Pix[m.PixOffset(x0, y):]
		for i := range n {
			put(i, p[i], p[i], p[i], 255)
		}
	default:
		for i := range n {
			c := color.NRGBAModel.Convert(src.At(x0+i, y)).(color.NRGBA)
			put(i, c.R, c.G, c.B, c.A)
		}
	}
}

func unpremul(c, a uint8) uint8 {
	return uint8(min(255, (int(c)*255+int(a)/2)/int(a)))
}

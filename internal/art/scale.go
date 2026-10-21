package art

import "image"

// Cover scales img to exactly w×h pixels, cropping it to that shape around
// its centre first, with the same linear-light resampling the renderer uses.
func Cover(img image.Image, w, h int) *image.NRGBA {
	return resample(img, coverRect(img.Bounds(), w, h), w, h).nrgba()
}

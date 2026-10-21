package ui

import (
	"strings"

	"github.com/bogdankim/encore/internal/library"
)

const historyLimit = 64

// history is the content navigation stack with back and forward lists, like a
// browser. Opening a view with the current view's key keeps the current one.
type history struct {
	back []View
	cur  View
	fwd  []View
}

func (h *history) open(v View) {
	if v == nil {
		return
	}
	if h.cur != nil {
		if h.cur.Key() == v.Key() {
			return
		}
		h.back = append(h.back, h.cur)
		if len(h.back) > historyLimit {
			h.back = h.back[len(h.back)-historyLimit:]
		}
	}
	h.cur = v
	h.fwd = h.fwd[:0]
}

func (h *history) goBack() bool {
	if len(h.back) == 0 {
		return false
	}
	h.fwd = append(h.fwd, h.cur)
	h.cur = h.back[len(h.back)-1]
	h.back = h.back[:len(h.back)-1]
	return true
}

func (h *history) goForward() bool {
	if len(h.fwd) == 0 {
		return false
	}
	h.back = append(h.back, h.cur)
	h.cur = h.fwd[len(h.fwd)-1]
	h.fwd = h.fwd[:len(h.fwd)-1]
	return true
}

// reindex rebinds every view to lib, dropping views whose subject is gone.
// When the current one is gone, the nearest earlier view takes its place.
func (h *history) reindex(lib *library.Library) {
	rebind := func(vs []View) []View {
		out := vs[:0]
		for _, v := range vs {
			if nv := v.Reindex(lib); nv != nil {
				out = append(out, nv)
			}
		}
		return out
	}
	h.back = rebind(h.back)
	h.fwd = rebind(h.fwd)
	if h.cur != nil {
		h.cur = h.cur.Reindex(lib)
	}
	if h.cur == nil && len(h.back) > 0 {
		h.cur = h.back[len(h.back)-1]
		h.back = h.back[:len(h.back)-1]
	}
}

// backGlyph goes back in history from a detail page.
const backGlyph = "‹"

// BackMark draws the back glyph and a space at l's current column when
// c.Back is set, and tells the app where it is: a click there goes back and
// hovering it names it in the footer. l starts at view column x0 on view
// row y. titleLine calls it; views with their own header call it right
// before the title.
func (c *Ctx) BackMark(l *line, x0, y int) {
	if c == nil || !c.Back {
		return
	}
	x := x0 + l.col()
	if c.backHover {
		l.raw(l.p.paint(toneText, surfHover, backGlyph), 1).pad(1)
	} else {
		l.add(toneDim, backGlyph).pad(1)
	}
	if c.backAt != nil {
		c.backAt(x, y)
	}
}

// isDetail reports whether a view key is a detail page rather than a
// library section.
func isDetail(key string) bool {
	for _, p := range []string{"album:", "artist:", "playlist:", "catalog-album:", "catalog-artist:"} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

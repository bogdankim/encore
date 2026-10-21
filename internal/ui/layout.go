package ui

import "image"

// region is a part of the screen for hit testing.
type region uint8

const (
	regionNone region = iota
	regionBar
	regionRail
	regionSidebar
	regionSideRule
	regionContent
	regionRightRule
	regionRight
	regionFooter
)

// Size thresholds from docs/ui.md, "Layout".
const (
	compactBarBelow = 20 // rows: the player bar collapses to one line
	compactBarUnder = 60 // cols: the same
	footerFrom      = 24 // rows: the footer shows
	sidebarFrom     = 90 // cols: the sidebar shows; under it the rail does
	rightFrom       = 100
	wideRightFrom   = 160
	rightWidth      = 40
	wideRightWidth  = 48
	minRightWidth   = 36
	maxRightWidth   = 80
	minContentWidth = 40
	// roomyContent is what the content keeps next to a right pane: the
	// sidebar folds to the rail first, then the pane narrows to its minimum.
	roomyContent    = 70
	railW           = 3
	defaultSidebarW = 22
	minSidebarWidth = 14
	maxSidebarWidth = 40
	tooSmallW       = 40
	tooSmallH       = 8
	contentPadLeft  = 2
	contentPadRight = 1
	middlePadTop    = 1
	rightPadLeft    = 2
	rightPadRight   = 1
)

// layoutOpts are the user's pane choices; the window size may override them.
type layoutOpts struct {
	sidebar   bool
	sidebarW  int
	right     bool // a right pane is open
	rightW    int  // 0: the default for the window width
	fullRight bool // the right pane takes the whole middle (full-screen lyrics)
}

// Layout is every rectangle on screen for one window size. Regions are whole
// columns (for hit testing); the *Body rects are what components draw into.
type Layout struct {
	W, H    int
	Small   bool // too small to draw anything but a notice
	Compact bool // one-line player bar

	Bar     image.Rectangle
	BarRule int // row of the rule under the bar

	Middle image.Rectangle

	Sidebar, SideRule  image.Rectangle // SideRule is also the rail's rule
	Rail, RailBody     image.Rectangle
	Content, RightRule image.Rectangle
	Right, Footer      image.Rectangle
	SidebarBody        image.Rectangle
	ContentBody        image.Rectangle
	RightBody          image.Rectangle
	SidebarOn, RightOn bool
	RailOn             bool // the sidebar is folded to a column of icons
	ContentOn          bool
}

func computeLayout(w, h int, o layoutOpts) Layout {
	l := Layout{W: w, H: h}
	if w < tooSmallW || h < tooSmallH {
		l.Small = true
		return l
	}
	barH := 3
	if h < compactBarBelow || w < compactBarUnder {
		barH, l.Compact = 1, true
	}
	l.Bar = image.Rect(0, 0, w, barH)
	l.BarRule = barH
	bottom := h
	if h >= footerFrom {
		l.Footer = image.Rect(0, h-1, w, h)
		bottom = h - 1
	}
	l.Middle = image.Rect(0, barH+1, w, bottom)
	top := l.Middle.Min.Y

	if o.right && o.fullRight {
		l.RightOn = true
		l.Right = l.Middle
		l.RightBody = inset(l.Right, middlePadTop, rightPadLeft, rightPadRight)
		return l
	}

	// The sidebar keeps its width or folds to the rail; it never narrows to
	// make room. With a right pane open the content keeps roomyContent
	// columns: the sidebar folds first, then the pane narrows.
	sw := sidebarWidth(o.sidebarW)
	rw := 0
	if o.right && w >= rightFrom {
		rw = rightWidth
		if w >= wideRightFrom {
			rw = wideRightWidth
		}
		if o.rightW > 0 {
			rw = o.rightW
		}
		rw = min(max(rw, minRightWidth), maxRightWidth)
	}
	full := o.sidebar && w >= sidebarFrom && w-sw-1 >= minContentWidth
	if full && rw > 0 && w-sw-1-rw-1 < roomyContent {
		full = false
	}
	leftW := railW + 1
	if full {
		leftW = sw + 1
	}
	if rw > 0 {
		if w-leftW-rw-1 < roomyContent {
			rw = max(minRightWidth, w-leftW-1-roomyContent)
		}
	}

	x0, x1 := 0, w
	if rw > 0 {
		l.RightOn = true
		l.Right = image.Rect(w-rw, top, w, bottom)
		l.RightRule = image.Rect(w-rw-1, top, w-rw, bottom)
		l.RightBody = inset(l.Right, middlePadTop, rightPadLeft, rightPadRight)
		x1 = w - rw - 1
	}
	if full {
		l.SidebarOn = true
		l.Sidebar = image.Rect(0, top, sw, bottom)
		l.SidebarBody = inset(l.Sidebar, middlePadTop, 0, 0)
	} else {
		sw = railW
		l.RailOn = true
		l.Rail = image.Rect(0, top, railW, bottom)
		l.RailBody = inset(l.Rail, middlePadTop, 0, 0)
	}
	l.SideRule = image.Rect(sw, top, sw+1, bottom)
	x0 = sw + 1
	l.ContentOn = true
	l.Content = image.Rect(x0, top, x1, bottom)
	l.ContentBody = inset(l.Content, middlePadTop, contentPadLeft, contentPadRight)
	return l
}

// sidebarWidth is the user's sidebar width, or the default, within bounds.
func sidebarWidth(w int) int {
	if w <= 0 {
		w = defaultSidebarW
	}
	return min(max(w, minSidebarWidth), maxSidebarWidth)
}

func inset(r image.Rectangle, top, left, right int) image.Rectangle {
	out := image.Rect(r.Min.X+left, r.Min.Y+top, r.Max.X-right, r.Max.Y)
	if out.Dx() < 0 || out.Dy() < 0 {
		return image.Rectangle{Min: out.Min, Max: out.Min}
	}
	return out
}

// Hit returns the region under a cell.
func (l Layout) Hit(x, y int) region {
	p := image.Pt(x, y)
	switch {
	case l.Small:
		return regionNone
	case p.In(l.Bar):
		return regionBar // the rule under it is not part of it
	case p.In(l.Footer):
		return regionFooter
	case l.SidebarOn && p.In(l.Sidebar):
		return regionSidebar
	case l.RailOn && p.In(l.Rail):
		return regionRail
	case (l.SidebarOn || l.RailOn) && p.In(l.SideRule):
		return regionSideRule
	case l.ContentOn && p.In(l.Content):
		return regionContent
	case l.RightOn && !l.RightRule.Empty() && p.In(l.RightRule):
		return regionRightRule
	case l.RightOn && p.In(l.Right):
		return regionRight
	}
	return regionNone
}

// body is the drawing rect of a region, which mouse events are local to.
func (l Layout) body(r region) image.Rectangle {
	switch r {
	case regionBar:
		return l.Bar
	case regionSidebar:
		return l.SidebarBody
	case regionRail:
		return l.RailBody
	case regionContent:
		return l.ContentBody
	case regionRight:
		return l.RightBody
	case regionFooter:
		return l.Footer
	}
	return image.Rectangle{}
}

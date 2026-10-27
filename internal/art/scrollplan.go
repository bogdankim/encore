package art

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// A grid that scrolls a row moves every tile image up by the row's height.
// The terminal can do that itself: WezTerm, iTerm2 and xterm.js attach an
// inline image to the cells it covers, so a scroll of a region that holds
// the images carries them along. scrollPlan describes such a scroll. The
// Output writes it ahead of Bubble Tea's frame, repaints the text of the
// rows it scrolled from the frame (Bubble Tea does not know they moved), and
// draws only the parts of images the scroll did not bring: the tiles of the
// row that came into view, and the rest of a tile that was cut off at the
// bottom before.

// scrollPlan is a vertical scroll of rows top..bot (inclusive) by dy rows up
// (down when dy < 0) that brings some of the new frame's images to their
// places.
type scrollPlan struct {
	top, bot, dy int
	// carried is, per piece of the new frame, the rectangles of it the
	// scroll brings in place: the image the terminal already holds there.
	carried map[piece][]rect
}

// sequence is what scrolls the region, leaving the cursor, pen and margins
// as they were.
func (p *scrollPlan) sequence() string {
	n, final := p.dy, "S"
	if n < 0 {
		n, final = -n, "T"
	}
	return "\x1b7\x1b[" + strconv.Itoa(p.top+1) + ";" + strconv.Itoa(p.bot+1) + "r\x1b[" +
		strconv.Itoa(n) + final + "\x1b[r\x1b8"
}

// minCarried is how many pieces a scroll must bring in place to be worth
// it.
const minCarried = 2

type scrollKey struct {
	key  string
	w, h int
	x    int
}

func keyOf(p piece) scrollKey { return scrollKey{p.key, p.box.w, p.box.h, p.box.x} }

// planScroll finds a vertical scroll that turns the pieces on screen into
// the pieces of the frame being drawn, or nil. Pieces match when they show
// the same image in the same column, only some rows higher or lower, and
// the new piece shows all of what the old one did (a tile that was cut off
// at the bottom and now has more room, not one that would be cut off by the
// scroll: xterm.js drops an image a scroll cuts in two). It is only a plan
// when everything else holds still: a piece that stays put must be outside
// the scrolled rows, and no piece that is in them may be left cut by the
// edge of the rows.
func planScroll(prev map[piece]bool, pieces []piece) *scrollPlan {
	if len(prev) == 0 || len(pieces) == 0 {
		return nil
	}
	now := make(map[piece]bool, len(pieces))
	for _, p := range pieces {
		now[p] = true
	}
	before := map[scrollKey][]piece{}
	for q := range prev {
		before[keyOf(q)] = append(before[keyOf(q)], q)
	}
	// moves returns the pieces of the old frame that move onto p by dy rows
	// and show nothing p does not.
	moves := func(p piece, dy int) []piece {
		var out []piece
		for _, q := range before[keyOf(p)] {
			if q.box.y-p.box.y == dy && shifted(q.vis, dy).intersect(p.vis) == shifted(q.vis, dy) {
				out = append(out, q)
			}
		}
		return out
	}
	votes := map[int]int{}
	for _, p := range pieces {
		if prev[p] {
			continue
		}
		seen := map[int]bool{}
		for _, q := range before[keyOf(p)] {
			dy := q.box.y - p.box.y
			if dy != 0 && !seen[dy] && len(moves(p, dy)) > 0 {
				seen[dy] = true
				votes[dy]++
			}
		}
	}
	dy, best := 0, 0
	for d, n := range votes {
		if n > best || n == best && abs(d) < abs(dy) {
			dy, best = d, n
		}
	}
	if best < minCarried {
		return nil
	}

	carried := map[piece][]rect{}
	top, bot := 1<<30, -1
	for _, p := range pieces {
		if prev[p] {
			continue
		}
		for _, q := range moves(p, dy) {
			s := shifted(q.vis, dy)
			top, bot = min(top, q.vis.y, s.y), max(bot, q.vis.y+q.vis.h-1, s.y+s.h-1)
			carried[p] = append(carried[p], s)
		}
	}
	if len(carried) < minCarried {
		return nil
	}
	inside := func(v rect) bool { return v.y >= top && v.y+v.h-1 <= bot }
	outside := func(v rect) bool { return v.y > bot || v.y+v.h-1 < top }
	for q := range prev {
		switch {
		case outside(q.vis):
			continue
		case !inside(q.vis), now[q]:
			return nil // across the edge of the scrolled rows, or still inside them
		}
		if s := shifted(q.vis, dy); !inside(s) && !outside(s) {
			return nil // the scroll would cut it in two
		}
	}
	return &scrollPlan{top: top, bot: bot, dy: dy, carried: carried}
}

// shifted is v moved up by dy rows (down when negative).
func shifted(v rect, dy int) rect { return rect{v.x, v.y - dy, v.w, v.h} }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// kept returns the parts of pc that stay where they are in this frame: what
// the plan carries there, or all of it when it was drawn there already and
// nothing has touched it. A part Bubble Tea's output wrote over (hit reports
// it) is not kept.
func (p *scrollPlan) kept(pc piece, prev map[piece]bool, hit func(rect) bool) []rect {
	if p != nil {
		if rs, ok := p.carried[pc]; ok {
			var out []rect
			for _, rc := range rs {
				if !hit(rc) {
					out = append(out, rc)
				}
			}
			return out
		}
	}
	if prev[pc] && !hit(pc.vis) {
		return []rect{pc.vis}
	}
	return nil
}

// without returns r minus the rectangles in cut.
func without(r rect, cut []rect) []rect {
	rest := []rect{r}
	for _, c := range cut {
		var next []rect
		for _, x := range rest {
			next = append(next, x.subtract(c)...)
		}
		rest = next
	}
	return rest
}

// canScroll reports whether the Renderer may plan scrolls: images that belong
// to cells the terminal moves with them, drawn directly (tmux keeps its own
// idea of the screen), and not turned off.
func (r *Renderer) canScroll() bool {
	return r.proto == ProtoIIP && !r.tmux && !r.passthrough && !r.noScroll
}

// takePlan plans the scroll for the frame being drawn and keeps it for the
// output that follows, see Output.Write. nil when there is none.
func (r *Renderer) takePlan() *scrollPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plan = nil
	if !r.canScroll() || r.dirtyAll || r.width <= 0 || r.height <= 0 || len(r.lines) == 0 {
		return nil
	}
	if p := planScroll(r.shown, r.pieces); p != nil && p.top >= 0 && p.bot < r.height {
		r.plan = p
	}
	return r.plan
}

// scrollsOrClears reports whether Bubble Tea's output b scrolls the screen
// or clears it, which a planned scroll must not be combined with.
func scrollsOrClears(b []byte) bool {
	var st byte
	p := ansi.NewParser()
	for len(b) > 0 {
		seq, _, n, state := ansi.DecodeSequence(b, st, p)
		st = state
		if n <= 0 {
			break
		}
		b = b[n:]
		if state != ansi.NormalState || len(seq) == 0 {
			continue
		}
		switch {
		case len(seq) == 1 && (seq[0] == '\n' || seq[0] == '\v' || seq[0] == '\f'):
			return true
		case seq[0] == ansi.ESC && len(seq) > 1:
			switch seq[1] {
			case 'D', 'E', 'M', 'c':
				return true
			case '[':
				cmd := ansi.Cmd(p.Command())
				switch cmd.Final() {
				case 'S', 'T', 'L', 'M', 'r':
					if cmd.Prefix() == 0 {
						return true
					}
				case 'J':
					if cmd.Prefix() == 0 {
						if v, _ := p.Param(0, 0); v != 0 {
							return true
						}
					}
				case 'h', 'l':
					if cmd.Prefix() == '?' && slices.ContainsFunc(p.Params(), func(q ansi.Param) bool {
						v := q.Param(0)
						return v == 47 || v == 1047 || v == 1049
					}) {
						return true
					}
				}
			}
		}
	}
	return false
}

// shows returns, for the cells of row y between two columns, what the
// terminal holds there once the plan has scrolled the screen: the text of
// the row that moved there, or a blank row when none did.
func (p *scrollPlan) shows(screen []string, y, width int) func(lo, hi int) string {
	src := y + p.dy
	line := strings.Repeat(" ", max(width, 0))
	if src >= p.top && src <= p.bot && src < len(screen) {
		line = screen[src]
	}
	return func(lo, hi int) string { return ansi.Cut(line, lo, hi) }
}

// sameCells reports whether two renderings of the same w cells look the
// same: equal, or both plain blanks. A blank with a background, a reverse
// or an underline is not plain.
func sameCells(a, b string, w int) bool {
	if a == b {
		return true
	}
	return plainBlank(a, w) && plainBlank(b, w)
}

func plainBlank(s string, w int) bool {
	if strings.Trim(ansi.Strip(s), " ") != "" || ansi.StringWidth(s) > w {
		return false
	}
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			return true
		}
		s = s[i+2:]
		end := strings.IndexAny(s, "@ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
		if end < 0 {
			return false
		}
		if s[end] != 'm' {
			return false // a cursor move or an erase: not a plain blank
		}
		params := strings.FieldsFunc(s[:end], func(r rune) bool { return r == ';' || r == ':' })
		for j := 0; j < len(params); j++ {
			switch n, _ := strconv.Atoi(params[j]); {
			case n == 38 || n == 58:
				if j+1 < len(params) && params[j+1] == "2" {
					j += 4
				} else {
					j += 2
				}
			case n == 0 || n == 1 || n == 2 || n == 3 || n == 22 || n == 23 || n == 39 || n == 49 || n == 59 || n >= 30 && n <= 37 || n >= 90 && n <= 97:
			default:
				return false // 4 underline, 7 reverse, 9 strike, 40 to 48 backgrounds
			}
		}
		s = s[end+1:]
	}
}

// shownFrame records that Bubble Tea has written the frame last committed:
// its text is what the terminal shows.
func (r *Renderer) shownFrame() {
	r.mu.Lock()
	r.screen = r.lines
	r.mu.Unlock()
}

// forgetFrame records that the terminal's text is not known (the alternate
// screen is off).
func (r *Renderer) forgetFrame() {
	r.mu.Lock()
	r.screen = nil
	r.mu.Unlock()
}

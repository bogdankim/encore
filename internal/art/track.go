package art

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// tracker follows the cursor through the bytes the program writes to the
// terminal and records which cells those bytes disturbed: cells printed over,
// erased (EL, ED, ECH), shifted (ICH, DCH) or scrolled (LF/RI at a margin, SU,
// SD, IL, DL). IIP image fragments live in cells, so any of these removes or
// moves part of an image; the Output re-sends exactly the images that were hit.
//
// It understands what Bubble Tea's renderer (ultraviolet) emits. An unknown
// sequence is assumed not to move the cursor.
type tracker struct {
	w, h       int
	x, y       int
	wrap       bool // pending wrap: the last print filled the last column
	noAutowrap bool
	top, bot   int // scroll margins, inclusive
	sx, sy     int // DECSC
	swrap      bool
	lastWidth  int  // width of the last printed grapheme, for REP
	alt        bool // the alternate screen is active; images are only drawn there
	onlcr      bool // the tty turns LF into CR LF (Bubble Tea without a tty input)
	// tmux: the program runs inside tmux. An erase on a line makes tmux drop
	// every sixel image on that line (recorded with damage.line), and an
	// insert or delete makes it rewrite the line from its first column when
	// the pane is narrower than the window.
	tmux bool
	// stay, when set, reports the images on screen that stay where they
	// are in the frame being written (see feed). It is asked once per write.
	stay func() *staying
	st   *staying // this write's answer, nil until asked
	repl []byte   // what to write instead of the sequence just interpreted
	// undo records what the sequence just interpreted did when it could not
	// be rewritten after all (it began in an earlier write).
	undo func()
	// redo holds rows to repaint from the frame after this write, around the
	// images that stay (see holdRows).
	redo map[int]bool

	state byte
	p     *ansi.Parser
	dmg   damage
}

func newTracker(w, h int) *tracker {
	t := &tracker{p: ansi.NewParser()}
	t.resize(w, h)
	return t
}

func (t *tracker) resize(w, h int) {
	t.w, t.h = max(w, 1), max(h, 1)
	t.top, t.bot = 0, t.h-1
	t.x, t.y = min(t.x, t.w-1), min(t.y, t.h-1)
	t.wrap = false
	t.dmg.setAll()
}

// staying is the cells of images that are on screen and stay where they are
// in the frame being written: per row, sorted column spans. The frame has
// blanks there. sixel holds the rows with an image tmux keeps.
type staying struct {
	spans map[int][]ival
	sixel map[int]bool
}

// covers reports whether cell (x, y) is in a span.
func (s *staying) covers(x, y int) bool {
	for _, v := range s.spans[y] {
		if x >= v.lo && x < v.hi {
			return true
		}
	}
	return false
}

// from returns row y's spans that start at column x or later.
func (s *staying) from(y, x int) []ival {
	var out []ival
	for _, v := range s.spans[y] {
		if v.lo >= x {
			out = append(out, v)
		}
	}
	return out
}

func (t *tracker) staying() *staying {
	if t.st == nil {
		t.st = t.stay()
	}
	return t.st
}

// feed interprets b, accumulating damage, and returns what to write in its
// place: b itself, or a copy that leaves the cells of images that stay alone
// (see tracker.stay). Bubble Tea's renderer repaints every line that holds a
// wide character on every frame: EL, then every cell from column 0 to the
// line's last non-blank one, blanks included. Without the copy, every image
// on such a line would be erased, and sent again, each frame. In it, a space
// printed over such an image is a cursor move instead, and an EL skips the
// image's cells.
func (t *tracker) feed(b []byte) []byte {
	t.st = nil
	var out []byte // nil until something is rewritten
	in := b
	done := 0    // bytes of in copied to out or replaced
	skipped := 0 // spaces skipped since done, not yet written as a cursor move
	flush := func(at int) {
		if out == nil {
			out = make([]byte, 0, len(in)+64)
		}
		if skipped > 0 && at > done {
			out = append(out, "\x1b["+strconv.Itoa(skipped)+"C"...)
			skipped = 0
		}
		out = append(out, in[done:at]...)
	}
	for len(b) > 0 {
		seq, width, n, state := ansi.DecodeSequence(b, t.state, t.p)
		t.state = state
		if n <= 0 {
			break
		}
		at := len(in) - len(b)
		b = b[n:]
		if state != ansi.NormalState || len(seq) == 0 {
			continue // incomplete sequence; the rest arrives with the next write
		}
		whole := at+len(seq) == len(in)-len(b) // the sequence is all in this write
		if width == 1 && whole && len(seq) == 1 && seq[0] == ' ' && t.stay != nil &&
			!t.wrap && t.x+1 < t.w && t.staying().covers(t.x, t.y) {
			if at > done || skipped == 0 {
				flush(at)
			}
			skipped++
			done = at + 1
			t.x++
			t.lastWidth = 1
			continue
		}
		switch {
		case width > 0:
			t.print(width)
		case seq[0] == ansi.ESC && len(seq) > 1:
			t.escape(seq)
		case len(seq) == 1 && seq[0] < 0x20:
			t.control(seq[0])
		}
		if t.repl != nil {
			if whole && (seq[0] == ansi.ESC || len(seq) == 1) {
				flush(at)
				if skipped > 0 {
					out = append(out, "\x1b["+strconv.Itoa(skipped)+"C"...)
					skipped = 0
				}
				out = append(out, t.repl...)
				done = at + len(seq)
			} else if t.undo != nil {
				t.undo() // not rewritten after all
			}
			t.repl, t.undo = nil, nil
		}
	}
	if out == nil {
		return in
	}
	flush(len(in))
	if skipped > 0 {
		out = append(out, "\x1b["+strconv.Itoa(skipped)+"C"...)
	}
	return out
}

func (t *tracker) print(width int) {
	if t.wrap && !t.noAutowrap {
		t.x = 0
		t.index()
	}
	t.wrap = false
	if t.x+width > t.w && !t.noAutowrap && t.x > 0 {
		t.x = 0
		t.index()
	}
	t.dmg.mark(t.y, t.x, min(t.x+width, t.w))
	t.lastWidth = width
	t.x += width
	if t.x >= t.w {
		t.x = t.w - 1
		t.wrap = true
	}
}

func (t *tracker) control(c byte) {
	switch c {
	case '\r':
		t.x, t.wrap = 0, false
	case '\n', '\v', '\f':
		if t.y == t.bot && t.holdRows(t.top, t.bot) {
			// the scroll is dropped; with ONLCR the tty still owes the CR
			t.repl = []byte{}
			if t.onlcr && c == '\n' {
				t.repl = []byte{'\r'}
			}
			t.undo = func() { t.dmg.scroll(t.top, t.bot, 1) }
		} else {
			t.index()
		}
		t.wrap = false
		if t.onlcr && c == '\n' {
			t.x = 0
		}
	case '\b':
		t.x, t.wrap = max(0, t.x-1), false
	case '\t':
		t.x, t.wrap = min(t.w-1, (t.x/8+1)*8), false
	}
}

// index moves down one row, scrolling the margin region at its bottom edge.
func (t *tracker) index() {
	switch {
	case t.y == t.bot:
		t.dmg.scroll(t.top, t.bot, 1)
	case t.y < t.h-1:
		t.y++
	}
}

func (t *tracker) reverseIndex() {
	switch {
	case t.y == t.top:
		t.dmg.scroll(t.top, t.bot, -1)
	case t.y > 0:
		t.y--
	}
}

// scrollIndex is IND (n 1) or RI (n -1): it moves the cursor, or scrolls
// the margins at their edge. A scroll that would move images that stay is
// written as repl instead.
func (t *tracker) scrollIndex(n int, repl []byte) {
	edge := t.bot
	if n < 0 {
		edge = t.top
	}
	if t.y != edge || !t.holdRows(t.top, t.bot) {
		if n > 0 {
			t.index()
		} else {
			t.reverseIndex()
		}
		return
	}
	t.repl = repl
	top, bot := t.top, t.bot
	t.undo = func() { t.dmg.scroll(top, bot, n) }
}

// scroll records a scroll of rows top..bot by n (SU, SD, IL, DL), or drops
// it when it would move images that stay.
func (t *tracker) scroll(top, bot, n int) {
	if t.holdRows(top, bot) {
		t.repl = []byte{}
		t.undo = func() { t.dmg.scroll(top, bot, n) }
		return
	}
	t.dmg.scroll(top, bot, n)
}

// holdRows reports whether rows top..bot hold an image that stays where it
// is in the frame being written, and if so marks them for repainting.
// Bubble Tea scrolls rows whose text moved up or down (a lyric line
// advancing), and the rows beside a block of art are blank there, so it
// scrolls the art with them: the terminal moves the image tiles, and the
// image would have to be sent again on every scroll. Such a scroll is
// dropped instead, and the rows are repainted from the frame after the write
// (see Renderer.redo), around the images; whatever the renderer writes there
// in between is overwritten.
func (t *tracker) holdRows(top, bot int) bool {
	if t.stay == nil || !t.alt || bot < top {
		return false
	}
	st := t.staying()
	hit := false
	for y := top; y <= bot && !hit; y++ {
		hit = len(st.spans[y]) > 0
	}
	if hit {
		if t.redo == nil {
			t.redo = map[int]bool{}
		}
		for y := top; y <= bot; y++ {
			t.redo[y] = true
		}
	}
	return hit
}

// holdCells is holdRows for an insert or delete of characters at the cursor,
// which shifts cells from column lo (the cursor, or 0 inside tmux, which may
// rewrite the whole line) to the right edge: when an image that stays sits
// there, the shift is dropped and the row repainted from the frame.
func (t *tracker) holdCells(lo int) bool {
	if t.stay == nil || !t.alt {
		return false
	}
	for _, v := range t.staying().spans[t.y] {
		if v.hi > lo {
			if t.redo == nil {
				t.redo = map[int]bool{}
			}
			t.redo[t.y] = true
			return true
		}
	}
	return false
}

// takeRedo returns the rows to repaint after this write, sorted, and clears
// them.
func (t *tracker) takeRedo() []int {
	if len(t.redo) == 0 {
		return nil
	}
	rows := make([]int, 0, len(t.redo))
	for y := range t.redo {
		if y >= 0 && y < t.h {
			rows = append(rows, y)
		}
	}
	slices.Sort(rows)
	t.redo = nil
	return rows
}

func (t *tracker) escape(seq []byte) {
	switch seq[1] {
	case '[':
		t.csi()
	case ']', 'P', '_', '^', 'X':
		// OSC, DCS, APC, PM, SOS: no cell effects (images we draw ourselves
		// never pass through here).
	case '7':
		t.sx, t.sy, t.swrap = t.x, t.y, t.wrap
	case '8':
		t.x, t.y, t.wrap = min(t.sx, t.w-1), min(t.sy, t.h-1), t.swrap
	case 'D':
		t.scrollIndex(1, []byte{})
		t.wrap = false
	case 'E':
		t.x, t.wrap = 0, false
		t.scrollIndex(1, []byte{'\r'})
	case 'M':
		t.scrollIndex(-1, []byte{})
		t.wrap = false
	case 'c':
		t.top, t.bot, t.x, t.y, t.wrap, t.noAutowrap = 0, t.h-1, 0, 0, false, false
		t.dmg.setAll()
	}
}

func (t *tracker) param(i, def int) int {
	v, _ := t.p.Param(i, def)
	if v == 0 && def > 0 {
		return def // CSI 0 A means 1, like a missing parameter
	}
	return v
}

func (t *tracker) csi() {
	cmd := ansi.Cmd(t.p.Command())
	if cmd.Prefix() == '?' {
		t.privateMode(cmd.Final())
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
		t.y = min(t.h-1, t.y+n)
	case 'C', 'a':
		t.x = min(t.w-1, t.x+n)
	case 'D':
		t.x = max(0, t.x-n)
	case 'E':
		t.x, t.y = 0, min(t.h-1, t.y+n)
	case 'F':
		t.x, t.y = 0, max(0, t.y-n)
	case 'G', '`':
		t.x = clamp(n-1, 0, t.w-1)
	case 'd':
		t.y = clamp(n-1, 0, t.h-1)
	case 'H', 'f':
		t.y = clamp(t.param(0, 1)-1, 0, t.h-1)
		t.x = clamp(t.param(1, 1)-1, 0, t.w-1)
	case 'J':
		switch t.param(0, 0) {
		case 0:
			t.erase(t.x, t.w)
			t.dmg.rows(t.y+1, t.h)
		case 1:
			t.dmg.rows(0, t.y)
			t.erase(0, t.x+1)
		default:
			t.dmg.setAll()
		}
	case 'K':
		switch t.param(0, 0) {
		case 0:
			t.eraseRight()
		case 1:
			t.erase(0, t.x+1)
		default:
			t.erase(0, t.w)
		}
	case 'X':
		t.eraseChars(n)
	case '@', 'P':
		lo := t.x
		if t.tmux {
			lo = 0
		}
		if t.holdCells(lo) {
			t.repl = []byte{}
			t.undo = func() { t.erase(lo, t.w) }
		} else {
			t.erase(lo, t.w)
		}
	case 'L':
		if t.y >= t.top && t.y <= t.bot {
			t.scroll(t.y, t.bot, -n)
		}
	case 'M':
		if t.y >= t.top && t.y <= t.bot {
			t.scroll(t.y, t.bot, n)
		}
	case 'S':
		t.scroll(t.top, t.bot, n)
	case 'T':
		t.scroll(t.top, t.bot, -n)
	case 'b':
		for range n {
			t.print(max(t.lastWidth, 1))
		}
		t.wrap = t.wrap && t.x == t.w-1
		return
	case 'r':
		top, bot := t.param(0, 1)-1, t.param(1, t.h)-1
		if top < bot && bot < t.h {
			t.top, t.bot = top, bot
		} else {
			t.top, t.bot = 0, t.h-1
		}
		t.x, t.y = 0, 0
	case 's':
		t.sx, t.sy, t.swrap = t.x, t.y, t.wrap
	case 'u':
		t.x, t.y, t.wrap = min(t.sx, t.w-1), min(t.sy, t.h-1), t.swrap
	default:
		return
	}
	t.wrap = false
}

// eraseRight records an erase from the cursor to the end of the line (EL).
func (t *tracker) eraseRight() { t.eraseAround(t.w, true) }

// eraseChars records an erase of n characters at the cursor (ECH).
func (t *tracker) eraseChars(n int) { t.eraseAround(min(t.x+n, t.w), false) }

// eraseAround records an erase from the cursor to column hi, or rewrites it
// (t.repl) to leave the cells of images that stay alone: erases around
// them, written as spaces on a row with an image tmux keeps (tmux drops
// every such image on a line where anything is erased).
func (t *tracker) eraseAround(hi int, toEnd bool) {
	var spans []ival
	var spaces bool
	if t.stay != nil && !t.wrap {
		st := t.staying()
		for _, v := range st.from(t.y, t.x) {
			if v.lo < hi {
				spans = append(spans, v)
			}
		}
		spaces = t.tmux && st.sixel[t.y]
	}
	if len(spans) == 0 && !spaces {
		t.erase(t.x, hi)
		return
	}
	var b strings.Builder
	x := t.x
	for _, s := range spans {
		if s.lo > x {
			t.blank(&b, x, s.lo, false, spaces)
		}
		x = max(x, min(s.hi, hi))
	}
	if x < hi {
		t.blank(&b, x, hi, toEnd, spaces)
	}
	t.moveBack(&b)
	t.repl = []byte(b.String())
	t.undo = func() { t.erase(t.x, t.w) }
}

// blank writes what clears columns lo..hi of the cursor's row into b and
// records it: spaces, or an erase (EL when toEnd, else ECH). It leaves the
// terminal's cursor anywhere; moveBack puts it back.
func (t *tracker) blank(b *strings.Builder, lo, hi int, toEnd, spaces bool) {
	b.WriteString("\x1b[" + strconv.Itoa(t.y+1) + ";" + strconv.Itoa(lo+1) + "H")
	switch {
	case spaces:
		b.WriteString(strings.Repeat(" ", hi-lo))
		t.dmg.mark(t.y, lo, hi)
	case toEnd:
		b.WriteString("\x1b[K")
		t.erase(lo, hi)
	default:
		b.WriteString("\x1b[" + strconv.Itoa(hi-lo) + "X")
		t.erase(lo, hi)
	}
}

// moveBack puts the terminal's cursor back where the tracker has it.
func (t *tracker) moveBack(b *strings.Builder) {
	b.WriteString("\x1b[" + strconv.Itoa(t.y+1) + ";" + strconv.Itoa(t.x+1) + "H")
}

// erase records an erase, insert or delete of columns lo..hi on the cursor's
// line.
func (t *tracker) erase(lo, hi int) {
	t.dmg.mark(t.y, lo, hi)
	if t.tmux {
		t.dmg.line(t.y)
	}
}

func (t *tracker) privateMode(final byte) {
	if final != 'h' && final != 'l' {
		return
	}
	set := final == 'h'
	for _, p := range t.p.Params() {
		switch p.Param(0) {
		case 7:
			t.noAutowrap = !set
		case 47, 1047, 1049:
			t.dmg.setAll()
			t.alt = set
			if set {
				t.top, t.bot = 0, t.h-1
			}
		}
	}
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

// damage is a set of disturbed cells: per row, sorted disjoint [lo, hi)
// column intervals, or everything. Scrolls are also kept in order, because
// they carry image fragments to rows outside the image's box.
type damage struct {
	all     bool
	byRow   map[int][]ival
	scrolls []scrollOp
	// lines had something erased, inserted or deleted (tracked inside tmux
	// only, see hitsLine)
	lines map[int]bool
}

// scrollOp moves rows top..bot (inclusive) up by n (down when n < 0).
type scrollOp struct{ top, bot, n int }

func (d *damage) scroll(top, bot, n int) {
	if bot < top || n == 0 {
		return
	}
	d.rows(top, bot+1)
	if !d.all {
		d.scrolls = append(d.scrolls, scrollOp{top, bot, n})
	}
}

// shift records a scroll the Output carries out itself, ahead of the frame:
// it moves fragments like any other scroll, but marks nothing damaged, since
// the images are drawn after it from what it brought.
func (d *damage) shift(top, bot, n int) {
	if bot < top || n == 0 || d.all {
		return
	}
	d.scrolls = append(d.scrolls, scrollOp{top, bot, n})
}

// moved returns where the fragments of r ended up after the scrolls.
func (d *damage) moved(r rect) []rect {
	frags := []rect{r}
	for _, s := range d.scrolls {
		region := rect{-1 << 20, s.top, 1 << 21, s.bot - s.top + 1}
		var next []rect
		for _, f := range frags {
			in := f.intersect(region)
			if in.empty() {
				next = append(next, f)
				continue
			}
			next = append(next, f.subtract(region)...)
			in.y -= s.n
			if in = in.intersect(region); !in.empty() {
				next = append(next, in)
			}
		}
		frags = next
	}
	return frags
}

type ival struct{ lo, hi int }

func (d *damage) setAll() { d.all, d.byRow, d.scrolls, d.lines = true, nil, nil, nil }

func (d *damage) reset() { d.all, d.byRow, d.scrolls, d.lines = false, nil, nil, nil }

// line records an erase, insert or delete on line y.
func (d *damage) line(y int) {
	if d.all || y < 0 {
		return
	}
	if d.lines == nil {
		d.lines = map[int]bool{}
	}
	d.lines[y] = true
}

// hitsLine is hits for an image tmux keeps (sixel): tmux drops every one
// of those on a line where anything was erased, inserted or deleted.
func (d *damage) hitsLine(r rect) bool {
	if d.hits(r) {
		return true
	}
	for y := r.y; y < r.y+r.h; y++ {
		if d.lines[y] {
			return true
		}
	}
	return false
}

func (d *damage) empty() bool { return !d.all && len(d.byRow) == 0 }

func (d *damage) rows(y0, y1 int) {
	for y := y0; y < y1; y++ {
		d.mark(y, 0, 1<<30)
	}
}

func (d *damage) mark(y, lo, hi int) {
	if d.all || hi <= lo || y < 0 {
		return
	}
	if d.byRow == nil {
		d.byRow = map[int][]ival{}
	}
	iv := d.byRow[y]
	out := make([]ival, 0, len(iv)+1)
	i := 0
	for ; i < len(iv) && iv[i].hi < lo; i++ {
		out = append(out, iv[i])
	}
	for ; i < len(iv) && iv[i].lo <= hi; i++ {
		lo, hi = min(lo, iv[i].lo), max(hi, iv[i].hi)
	}
	out = append(out, ival{lo, hi})
	d.byRow[y] = append(out, iv[i:]...)
}

// hits reports whether any damaged cell lies inside r.
func (d *damage) hits(r rect) bool {
	if d.all {
		return true
	}
	for y := r.y; y < r.y+r.h; y++ {
		for _, v := range d.byRow[y] {
			if v.lo < r.x+r.w && v.hi > r.x {
				return true
			}
		}
	}
	return false
}

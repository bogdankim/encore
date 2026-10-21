package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/lyrics"
	"github.com/bogdankim/encore/internal/player"
)

func init() { paneCtors.Lyrics = newLyricsPane }

const (
	lyricsQuiet      = 300 * time.Millisecond // no loading notice before this
	lyricsGiveUp     = 3 * time.Second        // "No lyrics" after this; the lookup goes on
	lyricsPrefetch   = 2                      // upcoming tracks looked up ahead
	lyricsSnapBack   = 3 * time.Second
	lyricsScrollDur  = 280 * time.Millisecond
	lyricsFrame      = time.Second / 30
	lyricsBreakMin   = 3 * time.Second // shorter breaks show no dots
	lyricsOffsetStep = 100 * time.Millisecond
	lyricsOffsetMax  = 30 * time.Second
	lyricsBodyTop    = 2 // the pane's title line and a blank row
	lyricsDots       = "·  ·  ·"
)

// lyricsAppleChecks are when, after a track starts, the view asks Music's
// cache again for Apple's lyrics: Music fetches them as the track starts,
// sometimes a moment after encore has looked.
var lyricsAppleChecks = [...]time.Duration{time.Second, 2500 * time.Millisecond, 5 * time.Second, 10 * time.Second}

// paneShownMsg goes to a right pane or overlay when it opens, so it can start
// work (a lyrics lookup) without waiting for the next player event.
type paneShownMsg struct{}

type lyricsResultMsg struct {
	from    *lyricsView
	gen     int
	pid     string
	lyr     *lyrics.Lyrics
	err     error
	interim bool // a better answer may follow
}

type wakeKind uint8

const (
	wakeFrame   wakeKind = iota // scroll animation
	wakeLoading                 // show the loading notice
	wakeSnap                    // end free scrolling
	wakeGiveUp                  // stop saying "Looking for lyrics…"
	wakeApple                   // look for Apple's lyrics again
)

type lyricsWakeMsg struct {
	v    *lyricsView
	kind wakeKind
	gen  int
}

type lyricsState uint8

const (
	lyricsIdle lyricsState = iota // nothing playing
	lyricsOff
	lyricsLoading
	lyricsReady
	lyricsMissing // also while a slow lookup goes on after lyricsGiveUp
	lyricsFailed
)

// lyricStyle is how a lyricsView lays lines out.
type lyricStyle struct {
	center   bool
	margin   int // columns left blank on each side
	indent   int // continuation rows of a wrapped line (left-aligned only)
	lineGap  int // blank rows between lines
	breakGap int // rows an empty line (a stanza break) takes
}

var (
	paneLyricStyle = lyricStyle{margin: 2, indent: 2, breakGap: 1}
	fullLyricStyle = lyricStyle{center: true, lineGap: 1, breakGap: 1}
)

type lyricRow struct {
	line int    // index into the lyric lines, -1 for spacing
	text string // "" for spacing and stanza breaks
	cont bool   // a continuation row of a wrapped line
	off  int    // byte offset of text in the line's text, -1 if unknown
}

type lyricLayout struct {
	lyr   *lyrics.Lyrics
	w     int
	rows  []lyricRow
	first []int        // first row of each line
	width []int        // widest row of each line
	words [][]wordSpan // for lines with timed words: where each word is in the text
	texts []string     // for lines with timed words: the trimmed text
}

type scrollAnim struct {
	from, to int
	at       time.Time
}

func (a scrollAnim) pos(now time.Time) int {
	t := float64(now.Sub(a.at)) / float64(lyricsScrollDur)
	if t >= 1 || a.from == a.to {
		return a.to
	}
	t = max(t, 0)
	e := 1 - math.Pow(1-t, 3)
	return a.from + int(math.Round(float64(a.to-a.from)*e))
}

func (a scrollAnim) done(now time.Time) bool {
	return a.from == a.to || now.Sub(a.at) >= lyricsScrollDur
}

// lyricsView is the lyrics of the playing track, shared by the pane and the
// full-screen overlay: the lookup, the wrapped layout, following the
// current line, free scrolling, hover and click to seek.
type lyricsView struct {
	svc Services
	sty lyricStyle

	pid   string
	store string // the stand-in pid of a playing Apple Music song, else ""
	query lyrics.Query
	gen   int
	state lyricsState
	lyr   *lyrics.Lyrics
	since time.Time

	lay  lyricLayout
	w, h int
	top  int // as last rendered

	anim   scrollAnim
	animOK bool
	frame  bool // a frame wake is pending

	manual    bool
	manualTop int
	manualGen int
	kbd       bool // moved with keys: cursor is the selected line
	cursor    int  // the keyboard cursor, a line with text

	hoverY int
}

func newLyricsView(s Services, sty lyricStyle) *lyricsView {
	return &lyricsView{svc: s, sty: sty, hoverY: -1}
}

func (v *lyricsView) wake(d time.Duration, kind wakeKind, gen int) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return lyricsWakeMsg{v: v, kind: kind, gen: gen} })
}

func (v *lyricsView) update(msg tea.Msg, c *Ctx) tea.Cmd {
	var cmds []tea.Cmd
	switch m := msg.(type) {
	case lyricsResultMsg:
		// a result from the other lyrics view for the same track is as good
		if m.pid != v.pid || (m.from == v && m.gen != v.gen) {
			break
		}
		if m.from == v && m.interim {
			cmds = append(cmds, v.next(m.lyr))
		}
		v.setResult(m.lyr, m.err, m.interim)
	case lyricsWakeMsg:
		if m.v != v {
			break
		}
		switch m.kind {
		case wakeFrame:
			v.frame = false
		case wakeSnap:
			if v.manual && m.gen == v.manualGen {
				v.endManual(c.Now)
			}
		case wakeGiveUp:
			if m.gen == v.gen && v.state == lyricsLoading {
				v.state = lyricsMissing
			}
		case wakeApple:
			if m.gen == v.gen {
				cmds = append(cmds, v.checkApple())
			}
		}
	}
	if c.PlayingPID() != v.pid {
		cmds = append(cmds, v.fetch(c))
	}
	v.place(c)
	if !v.anim.done(c.Now) && !v.frame {
		v.frame = true
		cmds = append(cmds, v.wake(lyricsFrame, wakeFrame, 0))
	}
	return tea.Batch(cmds...)
}

// fetch starts the lookup for the playing track and prefetches the next
// ones.
func (v *lyricsView) fetch(c *Ctx) tea.Cmd {
	v.gen++
	v.pid = c.PlayingPID()
	v.store = ""
	if it := c.Player.Catalog; it != nil {
		v.store = player.StorePID(it.StoreID)
	}
	v.lyr, v.lay = nil, lyricLayout{}
	v.manual, v.kbd, v.animOK, v.top = false, false, false, 0
	switch {
	case v.pid == "":
		v.state = lyricsIdle
		return nil
	case v.svc.Lyrics == nil:
		v.state = lyricsOff
		return nil
	case strings.HasPrefix(v.pid, "store:"):
		// An Apple Music song Music hasn't started yet: it reports the
		// song under a temporary pid once it plays, and the lookup goes
		// out then, with the pid Music knows it by.
		v.state, v.since = lyricsLoading, c.Now
		return v.wake(lyricsQuiet, wakeLoading, 0)
	}
	v.state, v.since = lyricsLoading, c.Now
	v.query = nowPlayingQuery(c.Player)
	cmds := []tea.Cmd{v.next(nil), v.wake(lyricsQuiet, wakeLoading, 0), v.wake(lyricsGiveUp, wakeGiveUp, v.gen)}
	for _, d := range lyricsAppleChecks {
		cmds = append(cmds, v.wake(d, wakeApple, v.gen))
	}
	if pl, svc, n := v.svc.Player, v.svc.Lyrics, min(c.Player.Upcoming, lyricsPrefetch); pl != nil && n > 0 {
		cmds = append(cmds, func() tea.Msg {
			qs := make([]lyrics.Query, 0, n)
			for i := range n {
				if t := pl.UpcomingAt(i); t != nil {
					qs = append(qs, trackQuery(t))
				}
			}
			svc.Prefetch(qs...)
			return nil
		})
	}
	return tea.Batch(cmds...)
}

// next waits for the first answer for the playing track better than prev.
func (v *lyricsView) next(prev *lyrics.Lyrics) tea.Cmd {
	svc, q, gen := v.svc.Lyrics, v.query, v.gen
	return func() tea.Msg {
		r := svc.Next(context.Background(), q, prev)
		return lyricsResultMsg{from: v, gen: gen, pid: q.PID, lyr: r.Lyrics, err: r.Err, interim: !r.Final}
	}
}

// checkApple asks Music's cache for Apple's lyrics, unless they're on
// screen already.
func (v *lyricsView) checkApple() tea.Cmd {
	if v.state == lyricsReady && v.lyr.Source == lyrics.SourceApple {
		return nil
	}
	svc, q, gen := v.svc.Lyrics, v.query, v.gen
	return func() tea.Msg {
		l := svc.Apple(q)
		if l == nil {
			return nil
		}
		return lyricsResultMsg{from: v, gen: gen, pid: q.PID, lyr: l}
	}
}

// setResult takes an answer from the lookup. Lyrics on screen are only
// replaced by better ones (plain by synced, anyone's by Apple's); a final
// miss only replaces the loading notice.
func (v *lyricsView) setResult(l *lyrics.Lyrics, err error, interim bool) {
	ok := err == nil && l != nil && (l.Instrumental || (l.Synced && len(l.Lines) > 0) || l.Plain != "")
	switch {
	case ok && (v.state != lyricsReady || lyrics.Better(l, v.lyr)):
		if v.state == lyricsReady {
			v.manual, v.kbd, v.animOK = false, false, false // different lines now
		}
		v.state, v.lyr = lyricsReady, l
	case ok, interim, v.state == lyricsReady:
		// keep what's on screen
	case err == nil, errors.Is(err, lyrics.ErrNotFound):
		v.state = lyricsMissing
	default:
		v.state = lyricsFailed
	}
}

func nowPlayingQuery(s player.Snapshot) lyrics.Query {
	if t := s.Track; t != nil {
		return trackQuery(t)
	}
	ct := s.NowPlaying.Track
	if ct == nil {
		return lyrics.Query{}
	}
	q := lyrics.Query{
		PID: ct.PID, Title: ct.Name, Artist: ct.Artist, Album: ct.Album,
		Duration: time.Duration(ct.Duration * float64(time.Second)),
	}
	if s.Catalog != nil {
		q.StoreID = s.Catalog.StoreID
	}
	return q
}

func trackQuery(t *library.Track) lyrics.Query {
	q := lyrics.Query{PID: t.PID, Title: t.Title, Artist: t.Artist, Album: t.AlbumName, Duration: t.Duration}
	if player.IsCatalog(t) {
		q.StoreID = strings.TrimPrefix(t.PID, player.StorePID(""))
	}
	return q
}

// offsetKey is what the lyric offset is saved under: the pid, or for an
// Apple Music song, which plays under a new temporary pid each time, its
// stand-in pid.
func (v *lyricsView) offsetKey() string {
	if v.store != "" {
		return v.store
	}
	return v.pid
}

func (v *lyricsView) offset() time.Duration {
	if v.svc.Offsets == nil || v.offsetKey() == "" {
		return 0
	}
	return v.svc.Offsets.Get(v.offsetKey())
}

func (v *lyricsView) adjustOffset(d time.Duration) {
	if v.svc.Offsets == nil || v.offsetKey() == "" {
		return
	}
	off := min(max(v.offset()+d, -lyricsOffsetMax), lyricsOffsetMax)
	v.svc.Offsets.Set(v.offsetKey(), off)
}

// meta is the dim part of the title line, only when it says something:
// "unsynced" or a non-zero offset.
func (v *lyricsView) meta() string {
	if v.state != lyricsReady {
		return ""
	}
	var sync, off string
	if !v.lyr.Synced && !v.lyr.Instrumental {
		sync = "unsynced"
	}
	if d := v.offset(); d != 0 && v.lyr.Synced {
		off = formatOffset(d)
	}
	return joinMeta(sync, off)
}

func formatOffset(d time.Duration) string {
	s := fmt.Sprintf("%+.1f s", d.Seconds())
	return strings.Replace(s, "-", "−", 1)
}

// synced reports whether the view shows timed lines.
func (v *lyricsView) synced() bool {
	return v.state == lyricsReady && v.lyr.Synced && !v.lyr.Instrumental
}

func (v *lyricsView) layout() *lyricLayout {
	if v.lay.lyr != v.lyr || v.lay.w != v.w {
		v.lay = buildLyricLayout(v.lyr, v.w, v.sty)
	}
	return &v.lay
}

// buildLyricLayout wraps the lines to w cells: word wrap, a word longer
// than the width is broken.
func buildLyricLayout(l *lyrics.Lyrics, w int, sty lyricStyle) lyricLayout {
	lay := lyricLayout{lyr: l, w: w}
	if l == nil || l.Instrumental {
		return lay
	}
	var texts []string
	if l.Synced {
		texts = make([]string, len(l.Lines))
		for i, ln := range l.Lines {
			texts[i] = ln.Text
		}
	} else {
		texts = l.PlainLines()
	}
	tw := max(w-2*sty.margin, 1)
	lay.first = make([]int, len(texts))
	lay.width = make([]int, len(texts))
	gap := func(line, n int) {
		for range n {
			lay.rows = append(lay.rows, lyricRow{line: line})
		}
	}
	prevText, prevBreak := false, false
	for i, raw := range texts {
		text := strings.TrimSpace(raw)
		if text == "" {
			lay.first[i] = len(lay.rows)
			// leading breaks of synced lyrics are the intro; trailing ones only
			// clear the highlight
			last := i == len(texts)-1
			if prevBreak || last || (!prevText && !l.Synced) {
				continue
			}
			if prevText {
				gap(-1, sty.lineGap)
			}
			lay.first[i] = len(lay.rows)
			gap(i, sty.breakGap)
			prevText, prevBreak = false, true
			continue
		}
		if prevText || prevBreak {
			gap(-1, sty.lineGap)
		}
		lay.first[i] = len(lay.rows)
		at := 0
		for j, r := range wrapLyric(text, tw, sty.indent) {
			off := -1
			if k := strings.Index(text[at:], r); k >= 0 {
				off, at = at+k, at+k+len(r)
			}
			lay.rows = append(lay.rows, lyricRow{line: i, text: r, cont: j > 0, off: off})
			ind := 0
			if j > 0 {
				ind = sty.indent
			}
			lay.width[i] = max(lay.width[i], Width(r)+ind)
		}
		prevText, prevBreak = true, false
		if l.Synced && len(l.Lines[i].Words) > 0 {
			if lay.words == nil {
				lay.words = make([][]wordSpan, len(texts))
				lay.texts = make([]string, len(texts))
			}
			lay.words[i], lay.texts[i] = wordSpans(text, l.Lines[i].Words), text
		}
	}
	return lay
}

// wrapLyric word-wraps text to w cells; continuation rows are indent cells
// narrower.
func wrapLyric(text string, w, indent int) []string {
	rows := strings.Split(ansi.Wrap(text, w, ""), "\n")
	if indent > 0 && len(rows) > 1 && w-indent >= 8 && strings.HasPrefix(text, rows[0]) {
		rest := strings.TrimLeft(text[len(rows[0]):], " ")
		rows = append(rows[:1], strings.Split(ansi.Wrap(rest, w-indent, ""), "\n")...)
	}
	out := rows[:0]
	for _, r := range rows {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func (v *lyricsView) anchor() int { return v.h * 2 / 5 }

func (v *lyricsView) maxTop() int {
	return max(0, len(v.layout().rows)-1-v.anchor())
}

func (v *lyricsView) clampTop(top int) int { return min(max(top, 0), v.maxTop()) }

// current is the index of the active line, or -1.
func (v *lyricsView) current(c *Ctx) int {
	if !v.synced() {
		return -1
	}
	return v.lyr.LineAt(c.Player.Position + v.svc.LyricsLead + v.offset())
}

// place works out the top row for this moment: following the current line
// at the anchor (animated), the track position for unsynced text, or where
// the user scrolled to.
func (v *lyricsView) place(c *Ctx) int {
	if v.state != lyricsReady || v.lyr.Instrumental || v.w <= 0 || v.h <= 0 {
		return 0
	}
	lay := v.layout()
	target := 0
	if v.lyr.Synced {
		if cur := v.current(c); cur >= 0 {
			target = max(0, lay.first[cur]-v.anchor())
		}
	} else if n := len(lay.rows) - v.h; n > 0 && c.Player.Duration > 0 {
		f := min(max(float64(c.Player.Position)/float64(c.Player.Duration), 0), 1)
		target = int(math.Round(f * float64(n)))
	}
	if v.manual {
		if v.kbd {
			v.manualTop = v.cursorTop()
		}
		return v.clampTop(v.manualTop)
	}
	switch {
	case !v.animOK:
		v.anim, v.animOK = scrollAnim{from: target, to: target, at: c.Now}, true
	case target != v.anim.to:
		from := v.anim.pos(c.Now)
		if abs(target-from) > v.h {
			from = target
		}
		v.anim = scrollAnim{from: from, to: target, at: c.Now}
	}
	return v.anim.pos(c.Now)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// notice is the one-line state shown instead of lyrics, if any.
func (v *lyricsView) notice(c *Ctx) (string, bool) {
	switch v.state {
	case lyricsIdle:
		return "Nothing playing", true
	case lyricsOff:
		return "Lyrics are turned off", true
	case lyricsLoading:
		if c.Now.Sub(v.since) < lyricsQuiet {
			return "", true
		}
		return "Looking for lyrics…", true
	case lyricsMissing:
		return "No lyrics for this song", true
	case lyricsFailed:
		return "Lyrics aren't available right now", true
	}
	if v.lyr.Instrumental {
		return "Instrumental", true
	}
	if len(v.layout().rows) == 0 {
		return "No lyrics for this song", true
	}
	return "", false
}

// render draws the lyrics into exactly w×h cells, one string per row.
func (v *lyricsView) render(w, h int, c *Ctx) []string {
	out := make([]string, max(h, 0))
	if w <= 0 || h <= 0 {
		return out
	}
	if w != v.w || h != v.h {
		v.w, v.h, v.animOK = w, h, false
	}
	p := paintsFor(c.Theme)
	blank := strings.Repeat(" ", w)
	if msg, ok := v.notice(c); ok {
		for y := range out {
			out[y] = blank
		}
		if msg != "" {
			out[(h-1)/3] = Center(p.paint(toneDim, surfNone, msg), w)
		}
		return out
	}
	top := v.place(c)
	v.top = top
	lay := v.layout()
	cur := v.current(c)
	hover := v.hoverLine()
	dots := cur >= 0 && v.isBreak(cur)
	// the line comes LyricsLead early; its words fill on their own time
	var fill *lineFill
	if v.synced() {
		fill = v.fill(c, cur)
	}
	for y := range out {
		r := top + y
		if r < 0 || r >= len(lay.rows) {
			out[y] = blank
			continue
		}
		row := lay.rows[r]
		switch {
		case row.text == "" && dots && row.line == cur && r == lay.first[cur]:
			out[y] = v.paintRow(p, w, lyricsDots, lyricPen{t: toneDim}, false, row)
		case row.text == "":
			out[y] = blank
		default:
			pen := lyricPen{t: toneText}
			if v.synced() {
				switch {
				case row.line == cur:
					pen.t = toneAccentBold
					if fill != nil && row.off >= 0 {
						pen.fill, pen.off = fill, row.off
					}
				case row.line < cur:
					pen.past = true
				default:
					pen.t = toneDim
				}
			}
			out[y] = v.paintRow(p, w, row.text, pen, row.line == hover, row)
		}
	}
	return out
}

// lyricPen is how a lyric row is painted: the current line in accent bold,
// lines to come in dim, sung lines (past) a step quieter. With timed words
// the current line fills word by word instead (see lineFill).
type lyricPen struct {
	t    tone
	past bool
	fill *lineFill // the current line's fill; nil: the whole row in t
	off  int       // where the row starts in the line's text, for fill
}

func (l lyricPen) paint(p *paints, sf surface, s string) string {
	switch {
	case s == "":
		return p.paint(l.t, sf, s)
	case l.past:
		return pastPrefix(p, sf) + s + ansi.ResetStyle
	case l.fill != nil:
		return l.fill.paint(p, sf, s, l.off)
	}
	return p.paint(l.t, sf, s)
}

// pastSGR caches the sung-line prefix per paints.
var pastSGR atomic.Pointer[pastCache]

type pastCache struct {
	p   *paints
	pre [numSurfaces]string
}

// pastPrefix is the style of sung lines: a third of the way from faint to
// dim. Faint alone (#4A4A52 on a dark background) is about 1.7:1, too low to
// read as text; this is about 2.5:1, still clearly behind the dim lines to
// come. Without colour it is faint text.
func pastPrefix(p *paints, sf surface) string {
	if c := pastSGR.Load(); c != nil && c.p == p {
		return c.pre[sf]
	}
	t := p.theme
	c := &pastCache{p: p}
	fg := t.Faint
	if mid, ok := mixColor(t.P.Faint, t.P.Dim, 1.0/3); ok && isColor(t.Faint.GetForeground()) {
		fg = lipgloss.NewStyle().Foreground(mid)
	}
	surfs := [numSurfaces]lipgloss.Style{lipgloss.NewStyle(), t.Selected, t.Hovered, t.Surface}
	for i, s := range surfs {
		c.pre[i] = sgr(fg, s)
	}
	pastSGR.Store(c)
	return c.pre[sf]
}

// mixColor is a + (b-a)*f in sRGB.
func mixColor(a, b color.Color, f float64) (color.Color, bool) {
	if a == nil || b == nil {
		return nil, false
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8((float64(x>>8) + (float64(y>>8)-float64(x>>8))*f) + 0.5)
	}
	return color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}, true
}

// isBreak reports whether line i is an instrumental break long enough to
// mark.
func (v *lyricsView) isBreak(i int) bool {
	ls := v.lyr.Lines
	if strings.TrimSpace(ls[i].Text) != "" || i+1 >= len(ls) {
		return false
	}
	return ls[i+1].Start-ls[i].Start >= lyricsBreakMin
}

// paintRow draws one row of text; a hovered line gets the hover surface,
// across the row in the pane and behind the text block when centred.
func (v *lyricsView) paintRow(p *paints, w int, text string, t lyricPen, hovered bool, row lyricRow) string {
	m := v.sty.margin
	tw := max(w-2*m, 1)
	text = ansi.Truncate(text, tw, "…")
	sw := Width(text)
	sf := surfNone
	if hovered {
		sf = surfHover
	}
	var b strings.Builder
	if v.sty.center {
		x := (w - sw) / 2
		if !hovered || row.line < 0 {
			return p.spaces(surfNone, x) + t.paint(p, surfNone, text) + p.spaces(surfNone, w-x-sw)
		}
		span := min(v.lay.width[row.line]+4, w)
		sx := (w - span) / 2
		after := max(sx+span-x-sw, 0)
		b.WriteString(p.spaces(surfNone, sx))
		b.WriteString(p.spaces(sf, x-sx))
		b.WriteString(t.paint(p, sf, text))
		b.WriteString(p.spaces(sf, after))
		b.WriteString(p.spaces(surfNone, w-x-sw-after))
		return b.String()
	}
	x := m
	if row.cont {
		x += v.sty.indent
	}
	x = min(x, w-sw)
	b.WriteString(p.spaces(sf, x))
	b.WriteString(t.paint(p, sf, text))
	b.WriteString(p.spaces(sf, w-x-sw))
	return b.String()
}

// hoverLine is the keyboard cursor while moving with keys, else the line
// the pointer is on; -1 when none. The pointer only lights timed lines.
func (v *lyricsView) hoverLine() int {
	if v.manual && v.kbd {
		return v.cursor
	}
	if !v.synced() {
		return -1
	}
	return v.lineAtRow(v.hoverY)
}

// textLine reports whether line i has text on screen: one the cursor can
// sit on.
func (v *lyricsView) textLine(i int) bool {
	lay := v.layout()
	if i < 0 || i >= len(lay.first) {
		return false
	}
	r := lay.first[i]
	return r < len(lay.rows) && lay.rows[r].line == i && lay.rows[r].text != ""
}

// lineNear is the line with text nearest layout row r, or -1.
func (v *lyricsView) lineNear(r int) int {
	rows := v.layout().rows
	r = min(max(r, 0), len(rows)-1)
	for d := 0; d < len(rows); d++ {
		for _, x := range [2]int{r + d, r - d} {
			if x >= 0 && x < len(rows) && rows[x].text != "" {
				return rows[x].line
			}
		}
	}
	return -1
}

// stepLine moves n lines with text from line from, stopping at the first
// and the last.
func (v *lyricsView) stepLine(from, n int) int {
	d := 1
	if n < 0 {
		d, n = -1, -n
	}
	at := from
	for i := from + d; n > 0 && i >= 0 && i < len(v.layout().first); i += d {
		if v.textLine(i) {
			at, n = i, n-1
		}
	}
	return at
}

// lineAtRow is the timed line drawn on row y, or -1.
func (v *lyricsView) lineAtRow(y int) int {
	if !v.synced() || y < 0 || y >= v.h {
		return -1
	}
	rows := v.layout().rows
	if r := v.top + y; r >= 0 && r < len(rows) && rows[r].text != "" {
		return rows[r].line
	}
	return -1
}

func (v *lyricsView) seekTo(line int, c *Ctx) tea.Cmd {
	if !v.synced() || line < 0 || line >= len(v.lyr.Lines) {
		return nil
	}
	if v.manual {
		v.endManual(c.Now)
	}
	if v.svc.Player != nil {
		v.svc.Player.Seek(max(0, v.lyr.Lines[line].Start-v.offset()))
	}
	return nil
}

// browsable reports whether there are lines on screen to scroll through.
func (v *lyricsView) browsable() bool {
	return v.state == lyricsReady && !v.lyr.Instrumental && v.w > 0 && v.h > 0 && len(v.layout().rows) > 0
}

// scroll moves the view freely by d rows (the wheel); the view returns to
// the current line 3 s after the last scroll.
func (v *lyricsView) scroll(d int) tea.Cmd {
	if !v.browsable() {
		return nil
	}
	if !v.manual {
		v.manual, v.manualTop = true, v.top
	}
	v.manualTop = v.clampTop(v.manualTop + d)
	v.top = v.manualTop
	v.kbd = false
	return v.holdManual()
}

// holdManual (re)starts the 3 s before the view returns to the current line,
// so it never snaps back while the user is still moving.
func (v *lyricsView) holdManual() tea.Cmd {
	v.manualGen++
	return v.wake(lyricsSnapBack, wakeSnap, v.manualGen)
}

// cursorTop is the top row that shows the keyboard cursor at the anchor, or
// as near it as the ends of the lyrics allow: at the top the cursor walks up
// to the first line, at the bottom down to the last.
func (v *lyricsView) cursorTop() int {
	return v.clampTop(v.layout().first[v.cursor] - v.anchor())
}

// cursorStart is the line keyboard moves start from: the cursor, else the
// current line, the line the pointer is on, or the line at the anchor.
func (v *lyricsView) cursorStart(c *Ctx) int {
	if v.manual && v.kbd && v.textLine(v.cursor) {
		return v.cursor
	}
	lay := v.layout()
	if v.manual {
		if l := v.lineAtRow(v.hoverY); l >= 0 {
			return l
		}
		return v.lineNear(v.top + v.anchor())
	}
	if v.lyr.Synced {
		if cur := v.current(c); cur >= 0 {
			return v.lineNear(lay.first[cur])
		}
		return v.lineNear(v.top) // the intro
	}
	// unsynced text: as far down the visible rows as the track has played
	f := 0.0
	if c.Player.Duration > 0 {
		f = min(max(float64(c.Player.Position)/float64(c.Player.Duration), 0), 1)
	}
	return v.lineNear(v.top + int(math.Round(f*float64(v.h-1))))
}

// cursorTo puts the keyboard cursor on line and the view under it.
func (v *lyricsView) cursorTo(line int) tea.Cmd {
	if !v.textLine(line) {
		return nil
	}
	v.manual, v.kbd, v.cursor = true, true, line
	v.manualTop = v.cursorTop()
	v.top = v.manualTop
	return v.holdManual()
}

// cursorLines moves the cursor n lines.
func (v *lyricsView) cursorLines(n int, c *Ctx) tea.Cmd {
	if !v.browsable() {
		return nil
	}
	return v.cursorTo(v.stepLine(v.cursorStart(c), n))
}

// cursorRows moves the cursor to the line about d rows away, at least one
// line.
func (v *lyricsView) cursorRows(d int, c *Ctx) tea.Cmd {
	if !v.browsable() || d == 0 {
		return nil
	}
	from := v.cursorStart(c)
	if from < 0 {
		return nil
	}
	to := v.lineNear(v.layout().first[from] + d)
	if to == from || (to-from > 0) != (d > 0) {
		to = v.stepLine(from, d/abs(d))
	}
	return v.cursorTo(to)
}

// cursorEnd moves the cursor to the first line (or the last, with last).
func (v *lyricsView) cursorEnd(last bool) tea.Cmd {
	if !v.browsable() {
		return nil
	}
	r := 0
	if last {
		r = len(v.layout().rows) - 1
	}
	return v.cursorTo(v.lineNear(r))
}

func (v *lyricsView) endManual(now time.Time) {
	v.manual, v.kbd = false, false
	v.anim, v.animOK = scrollAnim{from: v.manualTop, to: v.manualTop, at: now}, true
}

// action handles the keys shared by the pane and the overlay.
func (v *lyricsView) action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	half := max(v.h/2, 1)
	switch a {
	case keymap.NavDown:
		return true, v.cursorLines(n, c)
	case keymap.NavUp:
		return true, v.cursorLines(-n, c)
	case keymap.NavHalfDown:
		return true, v.cursorRows(half*n, c)
	case keymap.NavHalfUp:
		return true, v.cursorRows(-half*n, c)
	case keymap.NavPageDown:
		return true, v.cursorRows(v.h*n, c)
	case keymap.NavPageUp:
		return true, v.cursorRows(-v.h*n, c)
	case keymap.NavTop:
		return true, v.cursorEnd(false)
	case keymap.NavBottom:
		return true, v.cursorEnd(true)
	case keymap.Open:
		if v.manual && v.kbd {
			return true, v.seekTo(v.cursor, c)
		}
	case keymap.Back:
		if v.manual {
			v.endManual(c.Now)
			return true, nil
		}
	case keymap.LyricsOffsetUp:
		v.adjustOffset(lyricsOffsetStep)
		return true, nil
	case keymap.LyricsOffsetDown:
		v.adjustOffset(-lyricsOffsetStep)
		return true, nil
	}
	return false, nil
}

// mouse handles pointer events in the lyrics' own coordinates.
func (v *lyricsView) mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	inside := ev.X >= 0 && ev.X < v.w && ev.Y >= 0 && ev.Y < v.h
	switch ev.Kind {
	case MouseMotion:
		v.hoverY = -1
		if inside && ev.Button == tea.MouseNone {
			v.hoverY = ev.Y
			if v.lineAtRow(ev.Y) >= 0 {
				c.Hint("Play from Here") // a click seeks to the line
			}
		}
	case MouseWheel:
		if inside {
			v.hoverY = ev.Y
		}
		return v.scroll(ev.wheelDelta() * wheelRows)
	case MousePress:
		if inside && ev.Button == tea.MouseLeft {
			return v.seekTo(v.lineAtRow(ev.Y), c)
		}
	}
	return nil
}

// lyricsPane is the right pane: a title line and the lyrics.
type lyricsPane struct{ v *lyricsView }

func newLyricsPane(s Services) RightPane {
	return &lyricsPane{v: newLyricsView(s, paneLyricStyle)}
}

func (p *lyricsPane) KeyContext() keymap.Context { return keymap.Lyrics }

func (p *lyricsPane) Update(msg tea.Msg, c *Ctx) tea.Cmd { return p.v.update(msg, c) }

func (p *lyricsPane) Render(w, h int, c *Ctx) string {
	if w <= 0 || h <= 0 {
		return Blank(w, h)
	}
	lines := make([]string, 0, h)
	lines = append(lines, titleLine(c, w, "Lyrics", p.v.meta()))
	if h > 1 {
		lines = append(lines, strings.Repeat(" ", w))
	}
	if h > lyricsBodyTop {
		lines = append(lines, p.v.render(w, h-lyricsBodyTop, c)...)
	}
	return Lines(lines, w, h)
}

// FooterHints are the pane's keys: move the cursor, nudge the offset, seek
// to the cursor's line.
func (p *lyricsPane) FooterHints(c *Ctx) []FooterHint {
	return []FooterHint{
		{Keys: keysHint(c, keymap.NavDown, keymap.NavUp), Label: "scroll"},
		{Keys: keysJoin(c, " ", keymap.LyricsOffsetDown, keymap.LyricsOffsetUp), Label: "offset"},
		{Action: keymap.Open, Label: "seek"},
	}
}

func (p *lyricsPane) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	return p.v.action(a, n, c)
}

func (p *lyricsPane) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	return p.v.mouse(ev.local(0, lyricsBodyTop), c)
}

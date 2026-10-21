package ui

import (
	"image"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

func init() { paneCtors.Queue = newQueuePane }

const (
	queueBodyTop = 2 // title line and a blank row
	queueTimeW   = 5 // "59:59"
	// queueOneLineW is the narrowest pane that puts title, artist and time on
	// one line. Narrower, as at the usual 40–48 columns, a track takes two
	// rows, the title over the artist, so neither is cut to a stub.
	queueOneLineW = 60
)

// queueSig is what makes the pane's copy of the upcoming counts and window
// stale.
type queueSig struct {
	ver     uint64
	repeat  bridge.RepeatMode
	shuffle bool
}

type qKind uint8

const (
	qNone qKind = iota
	qNow
	qQueuedLabel
	qNextLabel
	qTrack
)

// queueRows maps body rows to what they show without building a row list:
// the playing track and a blank, the "Queued" label and the queued tracks,
// a blank, then the "Next from" label and the rest of the context. Each
// track takes th rows.
type queueRows struct {
	now               bool
	th                int
	n, queued         int
	qLabel, qStart    int // -1 when nothing is queued
	nextLabel, nStart int // -1 when the context has nothing left
	total             int
}

func makeQueueRows(now bool, n, queued, th int) queueRows {
	th = max(th, 1)
	r := queueRows{now: now, th: th, n: n, queued: queued, qLabel: -1, qStart: -1, nextLabel: -1, nStart: -1}
	y := 0
	if now {
		y = th + 1
	}
	if queued > 0 {
		r.qLabel, r.qStart = y, y+1
		y += 1 + queued*th
		if n > queued {
			y++
		}
	}
	if n > queued {
		r.nextLabel, r.nStart = y, y+1
		y += 1 + (n-queued)*th
	}
	r.total = y
	return r
}

// at returns what body row y shows, for tracks the index into Upcoming(),
// and which of the track's rows it is.
func (r queueRows) at(y int) (k qKind, i, sub int) {
	switch {
	case y < 0 || y >= r.total:
		return qNone, -1, 0
	case r.now && y < r.th:
		return qNow, -1, y
	case y == r.qLabel:
		return qQueuedLabel, -1, 0
	case r.qStart >= 0 && y >= r.qStart && y < r.qStart+r.queued*r.th:
		d := y - r.qStart
		return qTrack, d / r.th, d % r.th
	case y == r.nextLabel:
		return qNextLabel, -1, 0
	case r.nStart >= 0 && y >= r.nStart:
		d := y - r.nStart
		return qTrack, r.queued + d/r.th, d % r.th
	}
	return qNone, -1, 0
}

// rowOf is the first body row of track i.
func (r queueRows) rowOf(i int) int {
	if i < r.queued {
		return r.qStart + i*r.th
	}
	return r.nStart + (i-r.queued)*r.th
}

// indexAt is the track a drop at body row y lands on: the track under it,
// or the next one below a label or blank.
func (r queueRows) indexAt(y int) int {
	if r.n == 0 {
		return 0
	}
	switch k, i, _ := r.at(y); {
	case k == qTrack:
		return i
	case y < 0, k == qNow, k == qQueuedLabel:
		return 0
	case y >= r.total:
		return r.n - 1
	}
	return min(r.queued, r.n-1) // the "Next from" label or the blank above it
}

// queuePane is Up Next: the playing track, what the user queued and the rest
// of the context. Any upcoming row can be moved; a context row moved by hand
// becomes a queued one (see player.MoveUpcoming).
//
// It never copies the whole upcoming list, which for a Songs context is the
// library: counts and the length come from the player, and only the rows on
// screen are fetched.
type queuePane struct {
	svc Services

	sig    queueSig
	loaded bool
	stale  bool
	n      int // len(Upcoming())
	queued int
	dur    time.Duration

	win     []*library.Track // Upcoming()[winFrom:], as far as fetched
	winFrom int

	cursor int // index into Upcoming()
	top    int // first body row shown
	hover  int // track under the pointer, -1
	w, bh  int
	press  pressGuard

	drag struct {
		on       bool
		from, to int
	}
}

func newQueuePane(s Services) RightPane {
	return &queuePane{svc: s, hover: -1}
}

func (q *queuePane) KeyContext() keymap.Context { return keymap.Queue }

func (q *queuePane) Update(msg tea.Msg, c *Ctx) tea.Cmd {
	if _, ok := msg.(LibraryMsg); ok {
		q.stale = true
	}
	return nil
}

func (q *queuePane) currentSig() queueSig {
	pl := q.svc.Player
	if pl == nil {
		return queueSig{}
	}
	s := queueSig{ver: pl.UpcomingVersion()}
	np := pl.State().NowPlaying
	s.repeat, s.shuffle = np.Repeat, np.Shuffle
	return s
}

// load reads the counts and the length from the player rather than the
// frame's snapshot, which can lag behind an edit made in this pane.
func (q *queuePane) load() {
	q.loaded, q.stale = true, false
	q.n, q.queued, q.dur = 0, 0, 0
	q.win, q.winFrom = nil, 0
	pl := q.svc.Player
	if pl == nil {
		return
	}
	q.sig = q.currentSig()
	s := pl.State()
	q.n, q.queued = s.Upcoming, min(s.Queued, s.Upcoming)
	q.dur = pl.UpcomingDuration()
	q.cursor = min(max(q.cursor, 0), max(q.n-1, 0))
}

func (q *queuePane) ensure() {
	if !q.loaded || q.stale || q.currentSig() != q.sig {
		q.load()
	}
}

// track is Upcoming()[i], fetched with the rows around it.
func (q *queuePane) track(i int) *library.Track {
	if i < 0 || i >= q.n || q.svc.Player == nil {
		return nil
	}
	if i >= q.winFrom && i < q.winFrom+len(q.win) {
		return q.win[i-q.winFrom]
	}
	span := max(q.bh, 16)
	q.winFrom = max(i-span, 0)
	end := min(i+2*span, q.n)
	q.win = q.win[:0]
	for j := q.winFrom; j < end; j++ {
		t := q.svc.Player.UpcomingAt(j)
		if t == nil {
			break
		}
		q.win = append(q.win, t)
	}
	if i-q.winFrom < len(q.win) {
		return q.win[i-q.winFrom]
	}
	return nil
}

// trackH is the rows one track takes at the pane's width.
func (q *queuePane) trackH() int {
	if q.w >= queueOneLineW {
		return 1
	}
	return 2
}

func (q *queuePane) rows(c *Ctx) queueRows {
	return makeQueueRows(c.Player.NowPlaying.Track != nil, q.n, q.queued, q.trackH())
}

func (q *queuePane) meta() string {
	if q.n == 0 {
		return ""
	}
	return songsMeta(q.n, q.dur)
}

func (q *queuePane) clampTop(r queueRows) {
	q.top = min(max(q.top, 0), max(r.total-q.bh, 0))
}

// reveal scrolls so the cursor, and the label above the first row of a
// section, are on screen.
func (q *queuePane) reveal(r queueRows) {
	if q.n == 0 || q.bh <= 0 {
		return
	}
	y := r.rowOf(q.cursor)
	end := y + r.th - 1
	up := y
	if k, _, _ := r.at(y - 1); k == qQueuedLabel || k == qNextLabel {
		up = y - 1
	}
	if q.cursor == 0 {
		up = 0
	}
	switch {
	case up < q.top:
		q.top = up
	case end >= q.top+q.bh:
		q.top = end - q.bh + 1
	}
	q.clampTop(r)
}

func (q *queuePane) Render(w, h int, c *Ctx) string {
	if w <= 0 || h <= 0 {
		return Blank(w, h)
	}
	q.w, q.bh = w, max(h-queueBodyTop, 0)
	q.ensure()
	p := paintsFor(c.Theme)
	lines := make([]string, 0, h)
	lines = append(lines, titleLine(c, w, "Up Next", q.meta()))
	if h > 1 {
		lines = append(lines, strings.Repeat(" ", w))
	}
	r := q.rows(c)
	q.clampTop(r)
	if q.n == 0 {
		lines = append(lines, q.renderEmpty(p, w, q.bh, r, c)...)
		return Lines(lines, w, h)
	}

	thumbY, thumbH := scrollThumb(r.total, q.bh, q.top)
	g := &c.Theme.G
	ins := q.insertRow(r) // the drag's insertion line, as a body row
	for i := 0; i < q.bh; i++ {
		bar := " "
		if thumbH > 0 {
			if i >= thumbY && i < thumbY+thumbH {
				bar = p.paint(toneDim, surfNone, g.ScrollThumb)
			} else {
				bar = p.paint(toneFaint, surfNone, g.ScrollTrack)
			}
		}
		y := q.top + i
		switch {
		case ins >= 0 && y == ins:
			lines = append(lines, q.insertLine(p, w-1)+bar)
			continue
		case ins >= 0 && y > ins:
			y--
		}
		lines = append(lines, q.row(p, r, y, w-1, c)+bar)
	}
	return Lines(lines, w, h)
}

func (q *queuePane) renderEmpty(p *paints, w, bh int, r queueRows, c *Ctx) []string {
	out := make([]string, 0, bh)
	y := 0
	if r.now && bh > 0 {
		for sub := 0; sub < r.th && len(out) < bh; sub++ {
			out = append(out, q.row(p, r, sub, w, c))
		}
		y = r.th + 1
	}
	msg := "Nothing up next"
	if st := c.Player.Station; st != nil {
		// Music picks a station's songs, so there is nothing to list.
		msg = "Station: " + st.Name
	}
	for len(out) < bh {
		if len(out) == y+(bh-1-y)/3 {
			out = append(out, Center(p.paint(toneDim, surfNone, msg), w))
		} else {
			out = append(out, strings.Repeat(" ", w))
		}
	}
	return out
}

// row draws body row y, w cells wide.
func (q *queuePane) row(p *paints, r queueRows, y, w int, c *Ctx) string {
	k, i, sub := r.at(y)
	g := &c.Theme.G
	switch k {
	case qNow:
		l := newLine(p, surfNone, w)
		if sub == 0 {
			l.add(toneAccent, g.Playing)
		}
		l.to(2)
		title, artist, d := nowPlayingInfo(c.Player)
		q.trackText(l, r.th, sub, title, artist, d, toneText)
		return l.String()
	case qQueuedLabel:
		return newLine(p, surfNone, w).pad(2).add(toneDim, "Queued").String()
	case qNextLabel:
		label := "Next from: " + c.Player.Source.Title
		if c.Player.Source.Title == "" {
			label = "Next"
		}
		return newLine(p, surfNone, w).pad(2).add(toneDim, label).String()
	case qTrack:
		t := q.track(i)
		if t == nil {
			break
		}
		sel := i == q.cursor
		dragged := q.drag.on && i == q.drag.from
		sf := surfNone
		switch {
		case dragged, sel && c.Focused:
			sf = surfSel
		case i == q.hover && !q.drag.on:
			sf = surfHover
		}
		l := newLine(p, sf, w)
		if sub == 0 {
			switch {
			case sel && c.Focused:
				l.add(toneAccent, g.Marker)
			case sel:
				l.add(toneDim, g.Marker)
			}
		}
		l.to(2)
		main := toneText
		if !t.Available {
			main = toneFaint
		}
		q.trackText(l, r.th, sub, t.Title, t.Artist, t.Duration, main)
		return l.String()
	}
	return strings.Repeat(" ", w)
}

// queueCols is where the title, artist and time go in a row w cells wide
// that starts its text at x: the same for every row, so the columns line up.
func queueCols(x, w int) (titleW, artistX, artistW, timeX int) {
	timeX = w - 1 - queueTimeW
	avail := timeX - 2 - x
	if avail < 20 {
		return max(avail, 0), 0, 0, timeX
	}
	titleW = avail * 3 / 5
	artistX = x + titleW + 2
	return titleW, artistX, timeX - 2 - artistX, timeX
}

// trackText writes row sub of a track from the line's current column: the
// title, the artist and the time right-aligned in fixed columns, or with two
// rows per track the title and time over the artist.
func (q *queuePane) trackText(l *line, th, sub int, title, artist string, d time.Duration, main tone) {
	tm := ""
	if d > 0 {
		tm = clock(d)
	}
	x, w := l.col(), l.w
	if th > 1 {
		timeX := w - 1 - queueTimeW
		if sub == 0 {
			l.fit(main, title, timeX-2-x)
			l.to(timeX).right(toneDim, tm, queueTimeW)
		} else {
			l.fit(toneDim, artist, timeX-2-x)
		}
		return
	}
	titleW, artistX, artistW, timeX := queueCols(x, w)
	l.fit(main, title, titleW)
	if artistW > 0 {
		l.to(artistX).fit(toneDim, artist, artistW)
	}
	l.to(timeX).right(toneDim, tm, queueTimeW)
}

func nowPlayingInfo(s player.Snapshot) (title, artist string, d time.Duration) {
	if t := s.Track; t != nil {
		return t.Title, t.Artist, t.Duration
	}
	if ct := s.NowPlaying.Track; ct != nil {
		return ct.Name, ct.Artist, s.Duration
	}
	return "", "", 0
}

// insertRow is where the drag's insertion line goes, as a body row, or -1.
func (q *queuePane) insertRow(r queueRows) int {
	d := q.drag
	if !d.on || d.to == d.from {
		return -1
	}
	if d.to < d.from {
		return r.rowOf(d.to)
	}
	return r.rowOf(d.to) + r.th
}

func (q *queuePane) insertLine(p *paints, w int) string {
	if w < 4 {
		return strings.Repeat(" ", w)
	}
	return "  " + p.paint(toneAccent, surfNone, strings.Repeat("─", w-3)) + " "
}

// move moves Upcoming()[from] to to and keeps the cursor on it.
func (q *queuePane) move(from, to int, c *Ctx) {
	to = min(max(to, 0), q.n-1)
	if from == to || q.svc.Player == nil {
		return
	}
	q.svc.Player.MoveUpcoming(from, to)
	q.cursor = to
	q.load()
	q.reveal(q.rows(c))
}

// FooterHints are the queue's keys: reorder, remove, menu, play.
func (q *queuePane) FooterHints(c *Ctx) []FooterHint {
	return []FooterHint{
		{Keys: keysHint(c, keymap.QueueMoveDown, keymap.QueueMoveUp), Label: "move"},
		{Action: keymap.QueueRemove, Label: "remove"},
		{Action: keymap.Menu, Label: "menu"},
		{Action: keymap.Open, Label: "play"},
	}
}

func (q *queuePane) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	q.ensure()
	r := q.rows(c)
	last := q.n - 1
	move := func(to int) {
		q.cursor = min(max(to, 0), max(last, 0))
		q.reveal(r)
	}
	half := max(q.bh/2, 1)
	pl := q.svc.Player
	switch a {
	case keymap.NavDown:
		move(q.cursor + n)
	case keymap.NavUp:
		move(q.cursor - n)
	case keymap.NavHalfDown:
		move(q.cursor + half*n)
	case keymap.NavHalfUp:
		move(q.cursor - half*n)
	case keymap.NavPageDown:
		move(q.cursor + q.bh*n)
	case keymap.NavPageUp:
		move(q.cursor - q.bh*n)
	case keymap.NavTop:
		move(0)
	case keymap.NavBottom:
		move(last)
	case keymap.Open:
		if last >= 0 {
			return true, q.jump(q.cursor)
		}
	case keymap.QueueRemove:
		if last < 0 || pl == nil {
			return true, nil
		}
		pl.RemoveFromQueue(q.cursor)
		q.load()
		q.reveal(q.rows(c))
	case keymap.QueueMoveDown:
		if last >= 0 {
			q.move(q.cursor, q.cursor+n, c)
		}
	case keymap.QueueMoveUp:
		if last >= 0 {
			q.move(q.cursor, q.cursor-n, c)
		}
	case keymap.TrackFavorite:
		if t := q.track(q.cursor); t != nil {
			return true, c.Host.ToggleFavorite([]*library.Track{t})
		}
	case keymap.Menu:
		if last >= 0 {
			x := c.Origin.X + 2
			y := c.Origin.Y + queueBodyTop + r.rowOf(q.cursor) + r.th - 1 - q.top // the menu opens below the whole track
			return true, q.menu(x, y, q.cursor, c)
		}
	default:
		return false, nil
	}
	return true, nil
}

// subject is the selected row's song.
func (q *queuePane) subject(c *Ctx) Subject {
	q.ensure()
	if t := q.track(q.cursor); t != nil {
		return Subject{track: t}
	}
	return Subject{}
}

// menu opens the row menu for Upcoming()[i].
func (q *queuePane) menu(x, y, i int, c *Ctx) tea.Cmd {
	t := q.track(i)
	if t == nil {
		return nil
	}
	ts := []*library.Track{t}
	// The menu runs later; by then the track may have moved.
	at := func(h Host, do func(i int)) tea.Cmd {
		if j := q.indexOf(t, i); j >= 0 {
			do(j)
			return nil
		}
		return h.Status("That song is no longer up next")
	}
	pl := q.svc.Player
	items := []MenuItem{
		{Label: "Play Now", Key: keymap.Open, Run: func(h Host) tea.Cmd {
			return at(h, func(j int) { q.jump(j) })
		}},
		{Label: "Move to Top", Run: func(h Host) tea.Cmd {
			return at(h, func(j int) { pl.MoveUpcoming(j, 0); q.cursor = 0; q.top = 0 })
		}},
		{Label: "Remove from Up Next", Key: keymap.QueueRemove, Run: func(h Host) tea.Cmd {
			return at(h, func(j int) { pl.RemoveFromQueue(j) })
		}},
	}
	if player.IsCatalog(t) {
		// an Apple Music song: its catalog pages, and no Favorite
		items = menuGroup(menuGroup(items, catalogNavItems(ts)...), openInItems(true, Subject{track: t})...)
		items = menuGroup(items, musicMenuItems(ts)...)
		return c.Host.PushOverlay(NewMenu(x, y, nil, items...))
	}
	var nav []MenuItem
	if t.Album != nil {
		nav = append(nav, MenuItem{Label: "Go to Album", Run: func(h Host) tea.Cmd { return h.OpenAlbum(t.Album) }})
	}
	if t.Group != nil {
		nav = append(nav, MenuItem{Label: "Go to Artist", Run: func(h Host) tea.Cmd { return h.OpenArtist(t.Group) }})
	}
	items = menuGroup(menuGroup(items, nav...), openInItems(false, Subject{track: t})...)
	items = append(items, MenuSeparator, MenuItem{
		Label: "Favorite", Key: keymap.TrackFavorite, favOf: ts,
		Run: func(h Host) tea.Cmd { return h.ToggleFavorite(ts) },
	})
	return c.Host.PushOverlay(NewMenu(x, y, nil, items...))
}

// indexOf finds t in Upcoming(), looking at hint first.
func (q *queuePane) indexOf(t *library.Track, hint int) int {
	pl := q.svc.Player
	if pl == nil {
		return -1
	}
	if pl.UpcomingAt(hint) == t {
		return hint
	}
	for j, u := range pl.UpcomingN(hint + 64) {
		if u == t {
			return j
		}
	}
	return -1
}

func (q *queuePane) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	q.ensure()
	r := q.rows(c)
	ev = ev.local(0, queueBodyTop)
	y := q.top + ev.Y
	inside := ev.X >= 0 && ev.X < q.w && ev.Y >= 0 && ev.Y < q.bh
	k, i := qNone, -1
	if inside {
		k, i, _ = r.at(y)
	}
	switch ev.Kind {
	case MouseMotion:
		if q.drag.on && ev.Button == tea.MouseLeft {
			// past the edges the list scrolls under the pointer
			switch {
			case ev.Y < 0:
				q.top--
			case ev.Y >= q.bh:
				q.top++
			}
			q.clampTop(r)
			q.drag.to = r.indexAt(q.top + min(max(ev.Y, 0), q.bh-1))
			return nil
		}
		q.hover = -1
		if k == qTrack {
			q.hover = i
		}
	case MouseWheel:
		q.top += ev.wheelDelta() * wheelRows
		q.clampTop(r)
		q.hover = -1
		if k, i, _ := r.at(q.top + ev.Y); inside && k == qTrack {
			q.hover = i
		}
	case MouseRelease:
		d := q.drag
		q.drag.on = false
		if d.on && d.to != d.from {
			q.move(d.from, d.to, c)
		}
	case MousePress:
		double := q.press.double(ev)
		if k == qNow && ev.Button == tea.MouseRight {
			if h, ok := c.Host.(nowMenuHost); ok {
				return h.NowPlayingMenu(ev.ScreenX, ev.ScreenY)
			}
		}
		if k != qTrack {
			return nil
		}
		q.cursor = i
		switch {
		case ev.Button == tea.MouseRight:
			return q.menu(ev.ScreenX, ev.ScreenY, i, c)
		case ev.Button != tea.MouseLeft:
		case double:
			q.drag.on = false
			return q.jump(i)
		default:
			q.drag.on, q.drag.from, q.drag.to = true, i, i
		}
	}
	return nil
}

func (q *queuePane) jump(i int) tea.Cmd {
	if q.svc.Player == nil || i < 0 || i >= q.n {
		return nil
	}
	q.svc.Player.JumpTo(i)
	q.cursor, q.top = 0, 0
	q.load()
	return nil
}

// queueDrawer is Up Next under the right pane's width (100 columns): the
// pane itself, drawn over the right of the content behind a rule, at the
// pane's usual width. The rail and the content stay as they are, so
// nothing reflows to make room; esc, a click outside or space q closes it.
type queueDrawer struct{ a *App }

// toggleQueueDrawer opens the drawer, or closes it when it is on top.
func (a *App) toggleQueueDrawer() tea.Cmd {
	if _, ok := a.topOverlay().(*queueDrawer); ok {
		return a.CloseOverlay()
	}
	d := &queueDrawer{a: a}
	cmd := a.PushOverlay(d)
	if u, ok := a.paneOf(rightQueue).(interface {
		Update(tea.Msg, *Ctx) tea.Cmd
	}); ok {
		cmd = tea.Batch(cmd, u.Update(paneShownMsg{}, d.ctx()))
	}
	return cmd
}

func (d *queueDrawer) pane() RightPane { return d.a.paneOf(rightQueue) }

func (d *queueDrawer) Rect(w, h int) image.Rectangle {
	m := d.a.lay.Middle
	// at most up to the rail's rule, which the drawer's own rule replaces
	dw := min(rightWidth+1, w-railW)
	if dw <= 1+rightPadLeft+rightPadRight || m.Empty() {
		return image.Rectangle{}
	}
	return image.Rect(w-dw, m.Min.Y, w, m.Max.Y)
}

// body is where the pane draws, in screen cells.
func (d *queueDrawer) body() image.Rectangle {
	r := d.Rect(d.a.w, d.a.h)
	if r.Empty() {
		return r
	}
	return image.Rect(r.Min.X+1+rightPadLeft, r.Min.Y+middlePadTop, r.Max.X-rightPadRight, r.Max.Y)
}

// ctx is the pane's Ctx: focused while the drawer is on top, with its
// origin at the pane's top left.
func (d *queueDrawer) ctx() *Ctx {
	c := d.a.overlayCtx(d)
	c.Origin = d.body().Min
	return c
}

func (d *queueDrawer) Render(c *Ctx) string {
	r, b := d.Rect(d.a.w, d.a.h), d.body()
	p := paintsFor(c.Theme)
	rule := p.paint(toneFaint, surfNone, c.Theme.G.RuleV)
	var body []string
	if b.Dx() > 0 && b.Dy() > 0 {
		body = strings.Split(Box(d.pane().Render(b.Dx(), b.Dy(), d.ctx()), b.Dx(), b.Dy()), "\n")
	}
	lines := make([]string, r.Dy())
	for y := range lines {
		text := strings.Repeat(" ", r.Dx()-1)
		if j := y - middlePadTop; j >= 0 && j < len(body) {
			text = strings.Repeat(" ", rightPadLeft) + body[j] + strings.Repeat(" ", rightPadRight)
		}
		lines[y] = rule + text
	}
	return strings.Join(lines, "\n")
}

func (d *queueDrawer) CapturesInput() bool               { return false }
func (d *queueDrawer) KeyContext() keymap.Context        { return keymap.Queue }
func (d *queueDrawer) Key(tea.KeyPressMsg, *Ctx) tea.Cmd { return nil }

// Update is empty: the pane hears every message from the app already.
func (d *queueDrawer) Update(tea.Msg, *Ctx) tea.Cmd { return nil }

func (d *queueDrawer) Close() { d.pane().Mouse(leave, d.ctx()) }

func (d *queueDrawer) Action(act keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch act {
	case keymap.Back, keymap.InputCancel:
		return false, nil // the app closes the drawer
	case keymap.PaneQueue:
		return true, d.a.CloseOverlay()
	}
	return d.pane().Action(act, n, d.ctx())
}

func (d *queueDrawer) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	r, b := d.Rect(d.a.w, d.a.h), d.body()
	return d.pane().Mouse(ev.local(b.Min.X-r.Min.X, b.Min.Y-r.Min.Y), d.ctx())
}

// FooterHints are the pane's: the drawer is Up Next with the keys it has
// as a pane.
func (d *queueDrawer) FooterHints(c *Ctx) []FooterHint {
	if fh, ok := d.pane().(footerHinter); ok {
		return fh.FooterHints(d.ctx())
	}
	return nil
}

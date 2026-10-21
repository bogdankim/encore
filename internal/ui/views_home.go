package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
)

// Home is Apple Music's Home page as Music last showed it: shelves of
// albums, playlists, stations, songs and artists, one row of tiles each,
// then a few shelves of the New tab under a "New" heading. j/k move between
// shelves, h/l along one; each shelf keeps its own scroll. The page reads
// Music's cache through HomeSource and checks it again every homePoll while
// it is on screen.

// HomeSource is the Home page: *home.Reader in real mode, a fixture in
// --demo. Both calls may touch the disk; they run in tea.Cmds.
type HomeSource interface {
	Load(ctx context.Context) (*home.Page, error)
	Changed() bool
}

const (
	// homePoll is how often an open Home checks Music's cache.
	homePoll = 10 * time.Second
	// homeLiveEvery reloads every this many polls even when the cache
	// didn't change, so live stations show the show on air now.
	homeLiveEvery = 6
	homeTimeout   = 10 * time.Second

	// Shelf tiles are a little smaller than the album grid's, so a row
	// shows more of a shelf.
	homeTileMin    = 13
	homeTileMax    = 19
	homeTileTarget = 16
	// homePad is the column tiles and shelf titles start at; column 0 holds
	// the "‹" of a shelf scrolled to the right.
	homePad = 2

	homeEmptyM = "Open Home in Music once and it will show up here."
	homeErrM   = "Can’t read Music’s Home page"
	homeNewM   = "New"
)

// homeShelf is a shelf and where it is scrolled to.
type homeShelf struct {
	home.Shelf
	isNew bool
	cur   int // selected item
	off   int // first item shown
}

// homeBlock is a run of lines on the page: a heading or a shelf.
type homeBlock struct {
	heading string
	shelf   int // index into HomeView.shelves when heading is ""
}

// homeRowKind says what a line of the last frame shows.
type homeRowKind uint8

const (
	rowNone homeRowKind = iota
	rowHeading
	rowTitle
	rowArt
	rowCaption
	rowSub
	rowUnder
)

type homeRow struct {
	kind  homeRowKind
	shelf int
	j     int // art row within the tile
}

// HomeView is the Home page.
type HomeView struct {
	page    *home.Page
	shelves []*homeShelf
	blocks  []homeBlock
	sel     int // selected shelf
	top     int // first block shown

	loading   bool
	loadSince time.Time
	err       error

	// polling is set while a poll tick is on its way; shown is set by
	// Render, so a tick can tell the page is still on screen.
	polling, shown bool
	pollGen, polls int

	hover struct {
		shelf, item int
		badge       bool
	}
	guard pressGuard

	w, h int
	geo  gridGeom
	rows []homeRow // what each line below the title shows, from the last Render

	scrolledAt, fastAt time.Time
}

// NewHomeView is Home, showing p until it loads (p may be nil).
func NewHomeView(p *home.Page) *HomeView {
	v := &HomeView{}
	v.hover.shelf, v.hover.item = -1, -1
	if p != nil {
		v.setPage(p)
	}
	return v
}

func (v *HomeView) Key() string { return KeyHome }

func (v *HomeView) Title(c *Ctx) (string, string) {
	if v.page == nil || v.page.Updated.IsZero() {
		return "Home", ""
	}
	return "Home", "updated " + ago(c.Now, v.page.Updated)
}

// ago says how long before now t was: "just now", "5 min ago", "2 h ago",
// "yesterday", "3 days ago", or the date past a week.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + " min ago"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + " h ago"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return itoa(int(d/(24*time.Hour))) + " days ago"
	case t.Year() == now.Year():
		return t.Local().Format("Jan 2")
	}
	return t.Local().Format("Jan 2, 2006")
}

func (v *HomeView) Reindex(*library.Library) View { return v }

// Page is the page shown, or nil.
func (v *HomeView) Page() *home.Page { return v.page }

// setPage shows p, keeping the selected shelf, each shelf's selected item
// and the scroll position by shelf and item id where they are still there.
func (v *HomeView) setPage(p *home.Page) {
	old := map[string]*homeShelf{}
	for _, s := range v.shelves {
		old[shelfKey(s)] = s
	}
	selKey, topKey := "", ""
	if s := v.cur(); s != nil {
		selKey = shelfKey(s)
	}
	if v.top >= 0 && v.top < len(v.blocks) {
		if b := v.blocks[v.top]; b.heading == "" {
			topKey = shelfKey(v.shelves[b.shelf])
		} else {
			topKey = "heading:" + b.heading
		}
	}

	v.page = p
	v.shelves, v.blocks = v.shelves[:0], v.blocks[:0]
	add := func(s home.Shelf, isNew bool) {
		if len(s.Items) == 0 {
			return
		}
		hs := &homeShelf{Shelf: s, isNew: isNew}
		if o := old[shelfKey(hs)]; o != nil {
			hs.cur, hs.off = o.cur, o.off
			if o.cur < len(o.Items) {
				if i := itemIndex(s.Items, o.Items[o.cur]); i >= 0 {
					hs.off += i - o.cur
					hs.cur = i
				}
			}
		}
		v.blocks = append(v.blocks, homeBlock{shelf: len(v.shelves)})
		v.shelves = append(v.shelves, hs)
	}
	for _, s := range p.Shelves {
		add(s, false)
	}
	for i, s := range p.New {
		if i == 0 || len(v.shelves) == 0 || !v.shelves[len(v.shelves)-1].isNew {
			v.blocks = append(v.blocks, homeBlock{heading: homeNewM})
		}
		add(s, true)
	}
	if n := len(v.blocks); n > 0 && v.blocks[n-1].heading != "" {
		v.blocks = v.blocks[:n-1] // a heading over nothing
	}

	v.sel, v.top = 0, 0
	for i, s := range v.shelves {
		if shelfKey(s) == selKey {
			v.sel = i
		}
	}
	for i, b := range v.blocks {
		if b.heading == "" && shelfKey(v.shelves[b.shelf]) == topKey || b.heading != "" && "heading:"+b.heading == topKey {
			v.top = i
		}
	}
	v.hover.shelf, v.hover.item = -1, -1
	v.clampAll()
}

// shelfKey identifies a shelf across reloads: its id, else its title.
func shelfKey(s *homeShelf) string {
	if s.ID != "" {
		return s.ID
	}
	return "title:" + s.Title
}

func itemIndex(items []home.Item, it home.Item) int {
	for i, x := range items {
		if x.Type == it.Type && x.ID == it.ID {
			return i
		}
	}
	return -1
}

func (v *HomeView) cur() *homeShelf {
	if v.sel < 0 || v.sel >= len(v.shelves) {
		return nil
	}
	return v.shelves[v.sel]
}

// Selection is the selected item, or ok=false.
func (v *HomeView) Selection() (home.Shelf, home.Item, bool) {
	s := v.cur()
	if s == nil || s.cur < 0 || s.cur >= len(s.Items) {
		return home.Shelf{}, home.Item{}, false
	}
	return s.Shelf, s.Items[s.cur], true
}

// cols is how many tiles a row shows.
func (v *HomeView) cols() int { return max(v.geo.cols, 1) }

// clamp keeps the shelf's selection and scroll in range, with the
// selection in view.
func (s *homeShelf) clamp(cols int) {
	n := len(s.Items)
	s.cur = min(max(s.cur, 0), max(n-1, 0))
	if s.cur < s.off {
		s.off = s.cur
	}
	if s.cur >= s.off+cols {
		s.off = s.cur - cols + 1
	}
	s.off = min(max(s.off, 0), max(n-cols, 0))
}

// scroll moves the row by d tiles and keeps the selection in view.
func (s *homeShelf) scroll(d, cols int) bool {
	off := min(max(s.off+d, 0), max(len(s.Items)-cols, 0))
	if off == s.off {
		return false
	}
	s.off = off
	s.cur = min(max(s.cur, off), off+cols-1)
	return true
}

func (v *HomeView) clampAll() {
	cols := v.cols()
	for _, s := range v.shelves {
		s.clamp(cols)
	}
	v.sel = min(max(v.sel, 0), max(len(v.shelves)-1, 0))
	v.clampTop()
}

// blockH is the height of block b: a heading and a blank line, or a
// shelf's title, its tiles and the gap under them.
func (v *HomeView) blockH(b homeBlock) int {
	if b.heading != "" {
		return 2
	}
	return 1 + v.geo.pitch() + 1
}

// bodyH is how many lines the shelves may use: all but the title line, the
// blank under it, and the last line, which stays blank as under the grid.
func (v *HomeView) bodyH() int { return max(v.h-listTop-1, 1) }

// lastTop is the largest top that still fills the page.
func (v *HomeView) lastTop() int {
	h := 0
	for i := len(v.blocks) - 1; i >= 0; i-- {
		h += v.blockH(v.blocks[i])
		if h-1 > v.bodyH() { // the last block's gap line may hang off
			return i + 1
		}
	}
	return 0
}

func (v *HomeView) clampTop() {
	v.top = min(max(v.top, 0), max(min(v.lastTop(), len(v.blocks)-1), 0))
}

// blockOf is the block index of shelf i.
func (v *HomeView) blockOf(i int) int {
	for k, b := range v.blocks {
		if b.heading == "" && b.shelf == i {
			return k
		}
	}
	return 0
}

// ensureVisible scrolls the page so the selected shelf shows whole, with
// the heading above it when it is the first under one.
func (v *HomeView) ensureVisible() {
	if len(v.blocks) == 0 || v.h == 0 {
		return
	}
	b := v.blockOf(v.sel)
	first := b
	if b > 0 && v.blocks[b-1].heading != "" {
		first = b - 1
	}
	if first < v.top {
		v.top = first
	}
	for v.top < b {
		h := 0
		for k := v.top; k <= b; k++ {
			h += v.blockH(v.blocks[k])
		}
		if h-1 <= v.bodyH() {
			break
		}
		v.top++
	}
	v.clampTop()
}

// shelvesVisible is about how many shelves fit on the page.
func (v *HomeView) shelvesVisible() int {
	return max(1, v.bodyH()/max(v.blockH(homeBlock{}), 1))
}

// moveShelf moves the selection n shelves down (up when negative), to the
// same column on screen.
func (v *HomeView) moveShelf(n int) {
	from := v.cur()
	if from == nil {
		return
	}
	col := from.cur - from.off
	v.sel = min(max(v.sel+n, 0), len(v.shelves)-1)
	to := v.shelves[v.sel]
	if to != from {
		to.cur = min(to.off+col, len(to.Items)-1)
		to.clamp(v.cols())
	}
	v.ensureVisible()
}

func (v *HomeView) selectShelf(i int) {
	v.moveShelf(i - v.sel)
}

func (v *HomeView) resize(w, h int) {
	if w == v.w && h == v.h {
		return
	}
	v.w, v.h = w, h
	v.geo = layoutTiles(w, homePad, homeTileMin, homeTileMax, homeTileTarget)
	v.clampAll()
	v.ensureVisible()
}

// Init loads the page when it has none and starts checking Music's cache
// while the page is on screen.
func (v *HomeView) Init(c *Ctx) tea.Cmd {
	var cmds []tea.Cmd
	if v.page == nil && !v.loading {
		cmds = append(cmds, v.reload(c))
	}
	if !v.polling && c.Home != nil {
		v.polling = true
		v.pollGen++
		cmds = append(cmds, v.pollTick())
	}
	return tea.Batch(cmds...)
}

// reload reads the page again in the background.
func (v *HomeView) reload(c *Ctx) tea.Cmd {
	src := c.Home
	if src == nil {
		return nil
	}
	v.loading, v.loadSince = true, c.Now
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), homeTimeout)
		defer cancel()
		p, err := src.Load(ctx)
		return catalogResultMsg{apply: func(*Ctx) tea.Cmd {
			v.loaded(p, err)
			return nil
		}}
	}, catalogQuietTick())
}

func (v *HomeView) pollTick() tea.Cmd {
	gen := v.pollGen
	return tea.Tick(homePoll, func(time.Time) tea.Msg {
		return catalogResultMsg{apply: func(c *Ctx) tea.Cmd { return v.poll(c, gen) }}
	})
}

// poll runs every homePoll while the page was drawn since the last one:
// it reads the page again when Music's cache changed, and every
// homeLiveEvery polls for what is on air.
func (v *HomeView) poll(c *Ctx, gen int) tea.Cmd {
	if gen != v.pollGen {
		return nil
	}
	if !v.shown || c.Home == nil {
		v.polling = false
		return nil
	}
	v.shown = false
	v.polls++
	src, force := c.Home, v.page == nil || v.polls%homeLiveEvery == 0
	return func() tea.Msg {
		var p *home.Page
		var err error
		read := force || src.Changed()
		if read {
			ctx, cancel := context.WithTimeout(context.Background(), homeTimeout)
			p, err = src.Load(ctx)
			cancel()
		}
		return catalogResultMsg{apply: func(*Ctx) tea.Cmd {
			if read {
				v.loaded(p, err)
			}
			if gen != v.pollGen {
				return nil
			}
			return v.pollTick()
		}}
	}
}

// loaded takes a Load result. A failure keeps the page already shown.
func (v *HomeView) loaded(p *home.Page, err error) {
	v.loading = false
	switch {
	case err == nil && p != nil:
		v.err = nil
		v.setPage(p)
	case v.page == nil:
		v.err = err
	}
}

// emptyNote is the sentence shown without a page, or "" while it loads
// quietly.
func (v *HomeView) emptyNote(c *Ctx) string {
	switch {
	case c.Home == nil || errors.Is(v.err, home.ErrNotCached):
		return homeEmptyM
	case v.loading:
		if c.Now.Sub(v.loadSince) >= catalogQuiet {
			return "Loading…"
		}
		return ""
	case v.err != nil:
		return homeErrM
	case v.page != nil:
		return homeEmptyM // a page without shelves
	}
	return ""
}

func (v *HomeView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	s := v.cur()
	if s == nil {
		return false, nil
	}
	top := v.top
	n = max(n, 1)
	cols := v.cols()
	switch a {
	case keymap.NavDown, keymap.InputNext:
		v.moveShelf(n)
	case keymap.NavUp, keymap.InputPrev:
		v.moveShelf(-n)
	case keymap.NavTop:
		v.selectShelf(0)
	case keymap.NavBottom:
		v.selectShelf(len(v.shelves) - 1)
	case keymap.NavHalfDown:
		v.moveShelf(n * max(1, v.shelvesVisible()/2))
	case keymap.NavHalfUp:
		v.moveShelf(-n * max(1, v.shelvesVisible()/2))
	case keymap.NavPageDown:
		v.moveShelf(n * v.shelvesVisible())
	case keymap.NavPageUp:
		v.moveShelf(-n * v.shelvesVisible())
	case keymap.FocusLeft:
		if s.cur == 0 {
			return false, nil // focus moves on, as at a grid's edge
		}
		s.cur = max(s.cur-n, 0)
		s.clamp(cols)
		v.ensureVisible()
	case keymap.FocusRight:
		if s.cur >= len(s.Items)-1 {
			return false, nil
		}
		s.cur = min(s.cur+n, len(s.Items)-1)
		s.clamp(cols)
		v.ensureVisible()
	case keymap.Open:
		v.ensureVisible()
		return true, v.activate(c, v.sel, s.cur, false)
	case keymap.PagePlay:
		return true, v.activate(c, v.sel, s.cur, true)
	case keymap.PageShuffle, keymap.QueueAdd, keymap.QueuePlayNext, keymap.Menu, keymap.MusicOpen:
		x, y := v.tileCell(v.sel, s.cur)
		return true, homeItemCommand(c, a, s.Shelf, s.cur, c.Origin.X+x, c.Origin.Y+y+1)
	default:
		return false, nil
	}
	return true, v.scrolled(top, c)
}

// scrolled notes a scroll of the page from block top, for fast scrolling
// (see AlbumGrid.scrolled).
func (v *HomeView) scrolled(top int, c *Ctx) tea.Cmd {
	if v.top == top {
		return nil
	}
	fast := !v.scrolledAt.IsZero() && c.Now.Sub(v.scrolledAt) < scrollSettle
	v.scrolledAt = c.Now
	if !fast {
		return nil
	}
	v.fastAt = c.Now
	return tea.Tick(scrollSettle, func(time.Time) tea.Msg { return gridSettledMsg{} })
}

func (v *HomeView) settled(now time.Time) bool {
	return v.fastAt.IsZero() || now.Sub(v.fastAt) >= scrollSettle
}

// tileCell is the local cell where item i of shelf k has its caption, or
// the shelf's title line when it isn't on screen.
func (v *HomeView) tileCell(k, i int) (x, y int) {
	for y, r := range v.rows {
		if r.shelf == k && r.kind == rowCaption {
			s := v.shelves[k]
			return v.geo.colX(min(max(i-s.off, 0), v.cols()-1)), listTop + y
		}
	}
	return homePad, listTop
}

// hit is what a local cell is on: shelf and item (-1 for none), the play
// badge, or a shelf's scroll arrows (-1 left, +1 right).
type homeHit struct {
	shelf, item int
	badge       bool
	arrow       int
}

func (v *HomeView) hitTest(x, y int) homeHit {
	h := homeHit{shelf: -1, item: -1}
	y -= listTop
	if y < 0 || y >= len(v.rows) || x < 0 || x >= v.w {
		return h
	}
	r := v.rows[y]
	switch r.kind {
	case rowArt, rowCaption, rowSub:
	default:
		return h
	}
	h.shelf = r.shelf
	s := v.shelves[r.shelf]
	if r.kind == rowArt {
		switch {
		case x < homePad-1 && s.off > 0:
			h.arrow = -1
			return h
		case x >= contentRight(v.w) && s.off+v.cols() < len(s.Items):
			h.arrow = 1
			return h
		}
	}
	col := v.geo.colAt(x)
	if col < 0 || s.off+col >= len(s.Items) {
		return h
	}
	h.item = s.off + col
	h.badge = r.kind == rowArt && r.j == v.geo.artH-1 && x-v.geo.colX(col) < playBadgeW && homeCanPlay(s.Items[h.item])
	return h
}

func (v *HomeView) setHover(h homeHit) {
	v.hover.shelf, v.hover.item, v.hover.badge = h.shelf, h.item, h.badge
	if h.item < 0 {
		v.hover.shelf = -1
	}
}

func (v *HomeView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	h := v.hitTest(ev.X, ev.Y)
	switch ev.Kind {
	case MouseWheel:
		top := v.top
		dx := 0
		switch {
		case ev.Button == tea.MouseWheelRight:
			dx = 1
		case ev.Button == tea.MouseWheelLeft:
			dx = -1
		case ev.Mod&tea.ModShift != 0:
			dx = ev.wheelDelta()
		}
		if dx != 0 {
			if h.shelf >= 0 {
				v.shelves[h.shelf].scroll(dx, v.cols())
			}
		} else {
			v.top += ev.wheelDelta()
			v.clampTop()
		}
		v.setHover(v.hitTest(ev.X, ev.Y))
		return v.scrolled(top, c)
	case MouseMotion:
		v.setHover(h)
		switch {
		case h.arrow != 0:
			c.Hint("Scroll Shelf")
		case h.badge:
			c.Hint(homePlayName(v.shelves[h.shelf].Items[h.item]))
		case h.item >= 0 && homeOpensInMusic(v.shelves[h.shelf].Items[h.item]):
			c.Hint("Opens in Music.app")
		}
	case MousePress:
		if ev.Button != tea.MouseLeft && ev.Button != tea.MouseRight {
			return nil
		}
		if v.guard.double(ev) && ev.Button == tea.MouseLeft {
			return nil // a double-click is one click: the first played or opened it
		}
		if h.arrow != 0 {
			v.shelves[h.shelf].scroll(h.arrow*v.cols(), v.cols())
			return nil
		}
		if h.item < 0 {
			return nil
		}
		v.sel = h.shelf
		s := v.shelves[h.shelf]
		s.cur = h.item
		if ev.Button == tea.MouseRight {
			return homeItemCommand(c, keymap.Menu, s.Shelf, h.item, ev.ScreenX, ev.ScreenY)
		}
		return v.activate(c, h.shelf, h.item, h.badge)
	}
	return nil
}

// ClearHover drops the hover highlight (the pointer left the page).
func (v *HomeView) ClearHover() { v.hover.shelf, v.hover.item, v.hover.badge = -1, -1, false }

// activate does what a click on item i of shelf k does, or with play its
// play badge.
func (v *HomeView) activate(c *Ctx, k, i int, play bool) tea.Cmd {
	if k < 0 || k >= len(v.shelves) {
		return nil
	}
	s := v.shelves[k]
	if i < 0 || i >= len(s.Items) {
		return nil
	}
	return homeActivate(c, s.Shelf, i, play)
}

func (v *HomeView) Selected(c *Ctx) []*library.Track {
	s := v.cur()
	if s == nil {
		return nil
	}
	ts, i := homeSongTracks(s.Shelf, s.cur)
	if i < 0 {
		return nil
	}
	return ts[i : i+1]
}

// FooterHints: move, what enter does to the selected item, and Open in
// Music.app where enter does something else.
func (v *HomeView) FooterHints(c *Ctx) []FooterHint {
	hints := []FooterHint{{Keys: keysHint(c, keymap.FocusLeft, keymap.NavDown, keymap.NavUp, keymap.FocusRight), Label: "move"}}
	if _, it, ok := v.Selection(); ok {
		hints = append(hints, FooterHint{Action: keymap.Open, Label: homeOpenVerb(it)})
		if it.Type == home.TypeAlbum || it.Type == home.TypeArtist {
			hints = append(hints, FooterHint{Action: keymap.PagePlay, Label: "play"})
		}
		if _, ok := homeMusicPage(it); ok && !homeOpensInMusic(it) {
			hints = append(hints, FooterHint{Action: keymap.MusicOpen, Label: "open in Music.app"})
		}
	}
	return hints
}

func (v *HomeView) Render(w, h int, c *Ctx) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	v.shown = true
	v.resize(w, h)
	v.clampTop()
	title, meta := v.Title(c)
	head := []string{titleLine(c, w, title, meta), ""}
	if h <= listTop {
		return Lines(head, w, h)
	}
	if len(v.shelves) == 0 {
		v.rows = v.rows[:0]
		return Lines(head, w, listTop) + "\n" + noteState(c, v.emptyNote(c), w, h-listTop)
	}
	return Lines(head, w, listTop) + "\n" + Lines(v.renderBody(c.at(0, listTop)), w, h-listTop)
}

// renderBody draws the shelves from v.top into the lines under the title.
func (v *HomeView) renderBody(c *Ctx) []string {
	t := c.Theme
	p := paintsFor(t)
	geo := v.geo
	bh := v.bodyH()
	crisp := v.settled(c.Now)
	lines := make([]string, 0, bh)
	v.rows = v.rows[:0]
	emit := func(s string, r homeRow) {
		lines = append(lines, s)
		v.rows = append(v.rows, r)
	}
	var b strings.Builder
	row := func(s *homeShelf, n int, cell func(k int, it home.Item) string, left, right string) string {
		b.Reset()
		x := 0
		if left != "" {
			b.WriteString(left)
			x = Width(ansi.Strip(left))
		}
		for k := range n {
			b.WriteString(strings.Repeat(" ", max(geo.colX(k)-x, 0)))
			b.WriteString(cell(k, s.Items[s.off+k]))
			x = geo.colX(k) + geo.tileW
		}
		if right != "" {
			b.WriteString(strings.Repeat(" ", max(v.w-1-x, 0)))
			b.WriteString(right)
		}
		return b.String()
	}
	for bi := v.top; bi < len(v.blocks) && len(lines) < bh; bi++ {
		blk := v.blocks[bi]
		if blk.heading != "" {
			emit(newLine(p, surfNone, v.w).add(toneDim, blk.heading).String(), homeRow{kind: rowHeading, shelf: -1})
			if len(lines) < bh {
				emit("", homeRow{shelf: -1})
			}
			continue
		}
		k := blk.shelf
		s := v.shelves[k]
		emit(newLine(p, surfNone, v.w).pad(homePad).add(toneBold, s.Title).String(), homeRow{kind: rowTitle, shelf: k})
		if len(lines) >= bh {
			break
		}
		n := min(v.cols(), len(s.Items)-s.off)
		y := len(lines)
		vis := min(geo.artH, bh-y)
		tiles := make([]RowTile, n)
		for j := range n {
			it := s.Items[s.off+j]
			tiles[j] = RowTile{Key: homeArtKey(it), PID: it.ArtURL(catalogArtPx), X: geo.colX(j)}
		}
		whole := c.TileRow(tiles, y, geo.tileW, geo.artH, vis, crisp)
		arts := make([][]string, n)
		for j := range n {
			if !whole {
				arts[j] = strings.Split(c.TileArt(tiles[j].Key, tiles[j].PID, tiles[j].X, y, geo.tileW, geo.artH, vis, crisp), "\n")
			}
			if k == v.hover.shelf && s.off+j == v.hover.item && vis == geo.artH && homeCanPlay(s.Items[s.off+j]) {
				c.CoverArt(tiles[j].X, y+geo.artH-1, playBadgeW, 1)
			}
		}
		mid := (geo.artH - 1) / 2
		for j := 0; j < geo.artH && len(lines) < bh; j++ {
			left, right := "", ""
			if j == mid && s.off > 0 {
				left = p.paint(toneFaint, surfNone, "‹")
			}
			if j == mid && s.off+n < len(s.Items) {
				right = p.paint(toneFaint, surfNone, "›")
			}
			emit(row(s, n, func(q int, it home.Item) string {
				out := strings.Repeat(" ", geo.tileW)
				if j < len(arts[q]) {
					out = Fit(arts[q][j], geo.tileW)
				}
				if j == geo.artH-1 && k == v.hover.shelf && s.off+q == v.hover.item && homeCanPlay(it) {
					out = playBadge(t, v.hover.badge) + ansi.Cut(out, playBadgeW, geo.tileW)
				}
				return out
			}, left, right), homeRow{kind: rowArt, shelf: k, j: j})
		}
		look := func(q int) (surface, tone, itemState) {
			i := s.off + q
			sf, tn, sel := surfNone, toneText, statePlain
			if k == v.hover.shelf && i == v.hover.item {
				sf = surfHover
			}
			if k == v.sel && i == s.cur {
				tn, sel = toneBold, stateSelected
				if c.Focused {
					sel = stateFocused
				}
			}
			return sf, tn, sel
		}
		if len(lines) < bh {
			emit(row(s, n, func(q int, it home.Item) string {
				sf, tn, _ := look(q)
				l := newLine(p, sf, geo.tileW)
				if homePlaying(c, it) {
					l.add(toneAccent, t.G.Playing).pad(1)
				}
				tileTitle(l, tn, it.Title, c.HomeItemInLibrary(it), &t.G)
				return l.String()
			}, "", ""), homeRow{kind: rowCaption, shelf: k})
		}
		if len(lines) < bh {
			emit(row(s, n, func(q int, it home.Item) string {
				sf, _, _ := look(q)
				l := newLine(p, sf, geo.tileW)
				homeSubtitle(l, it)
				return l.String()
			}, "", ""), homeRow{kind: rowSub, shelf: k})
		}
		if len(lines) < bh {
			emit(row(s, n, func(q int, _ home.Item) string {
				_, _, sel := look(q)
				l := newLine(p, surfNone, geo.tileW)
				switch sel {
				case stateFocused:
					l.add(toneAccent, strings.Repeat(underlineGlyph, geo.tileW))
				case stateSelected:
					l.add(toneFaint, strings.Repeat(underlineGlyph, geo.tileW))
				}
				return l.String()
			}, "", ""), homeRow{kind: rowUnder, shelf: k})
		}
		if len(lines) < bh {
			emit("", homeRow{shelf: -1})
		}
	}
	return lines
}

// homeSubtitle draws a tile's second caption line: the artist or curator;
// for a live station "Live" and the show on air; "Station" for a station
// with nothing else to say.
func homeSubtitle(l *line, it home.Item) {
	switch {
	case it.Type == home.TypeStation && it.IsLive:
		l.add(toneText, "Live")
		if show := it.NowPlaying; show != "" && show != it.Title {
			l.add(toneDim, " · "+show)
		}
	case it.Subtitle != "":
		l.add(toneDim, it.Subtitle)
	case it.Type == home.TypeStation:
		l.add(toneDim, "Station")
	case it.Type == home.TypeArtist:
		l.add(toneDim, "Artist")
	}
}

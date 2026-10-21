package ui

import (
	"image"
	"math/rand/v2"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

// Tile widths the grid aims for, in cells. Art is tileW×tileW/2 cells, which
// is square in half blocks.
const (
	tileMinW   = 16
	tileMaxW   = 22
	tileTarget = 19
	tileTiny   = 6 // below this the grid falls back to the list layout
)

// gridGeom is the tile layout for one width.
type gridGeom struct {
	pad   int // left margin
	cols  int
	tileW int
	gap   int
	extra int // gaps, from the last one back, that are one cell wider
	artH  int
}

// pitch is the height of one tile row: art, title, artist and a line that
// carries the selection underline.
func (g gridGeom) pitch() int { return g.artH + 3 }

// stride is the distance between the left edges of neighbouring tiles
// (plus one where a gap is wider, see colX).
func (g gridGeom) stride() int { return g.tileW + g.gap }

func (g gridGeom) usable() bool { return g.cols >= 2 && g.tileW >= tileTiny }

// colX is where column k starts.
func (g gridGeom) colX(k int) int {
	return g.pad + k*g.stride() + max(0, k-(g.cols-1-g.extra))
}

// colAt is the column whose tile covers x, or -1 (a gap or the margin).
func (g gridGeom) colAt(x int) int {
	for k := range g.cols {
		if x0 := g.colX(k); x >= x0 && x < x0+g.tileW {
			return k
		}
	}
	return -1
}

// right is where the last column ends.
func (g gridGeom) right() int { return g.colX(g.cols-1) + g.tileW }

// layoutGrid fits at least two columns of tiles between pad and
// contentRight(w), so the grid's last column ends where every view's
// content does. It picks the column count, tile width (tileMinW to
// tileMaxW, near tileTarget) and gap that fill the width best; cells left
// over (fewer than the gaps) widen the last gaps by one.
func layoutGrid(w, pad int) gridGeom { return layoutTiles(w, pad, tileMinW, tileMaxW, tileTarget) }

// layoutTiles is layoutGrid for tiles minW to maxW cells wide, near target.
func layoutTiles(w, pad, minW, maxW, target int) gridGeom {
	avail := contentRight(w) - pad
	base := 3
	if avail < 60 {
		base = 2
	}
	n0 := max(2, (avail+base+(target+base)/2)/(target+base))
	best, bestScore := gridGeom{}, -1
	for n := max(2, n0-1); n <= n0+1; n++ {
		for gap := 2; gap <= 4; gap++ {
			t := min((avail-(n-1)*gap)/n, maxW)
			if t < 1 || t < minW && n > 2 {
				continue
			}
			spare := avail - n*t - (n-1)*gap
			score := 2*cellDist(gap, base) + cellDist(t, target)
			if spare > 0 {
				score++ // uneven gaps
			}
			if spare > n-1 {
				score += 100 * spare // can't spread it: a ragged right edge
			}
			if bestScore < 0 || score < bestScore {
				best = gridGeom{pad: pad, cols: n, tileW: t, gap: gap, extra: min(spare, n-1)}
				bestScore = score
			}
		}
	}
	if bestScore < 0 {
		t := max((avail-base)/2, 0)
		best = gridGeom{pad: pad, cols: 2, tileW: t, gap: base}
	}
	best.artH = best.tileW / 2
	return best
}

// cellDist is how far apart a and b are.
func cellDist(a, b int) int { return max(a-b, b-a) }

// AlbumGrid is a virtualized album grid with a list layout, cursor, hover and
// tile hit testing. Only visible rows are rendered.
type AlbumGrid struct {
	items  []*library.Album
	cursor int
	top    int  // first visible tile row (grid) or row (list)
	hover  int  // -1 when the pointer is not over an item
	onPlay bool // the pointer is on the hovered tile's play badge
	list   bool
	pad    int
	// Passive hides the cursor, for a grid that shares a page with a list
	// that has it.
	Passive bool

	w, h int
	geo  gridGeom
	// scrolledAt is when the grid last scrolled. A scroll that follows
	// another within scrollSettle is fast scrolling: it sets fastAt, and
	// tiles are half blocks until scrollSettle after it, because every step
	// would otherwise send each visible tile's image again.
	scrolledAt, fastAt time.Time
}

// scrollSettle is how long the grid waits after a scroll before it swaps
// half blocks for images.
const scrollSettle = 150 * time.Millisecond

// gridSettledMsg redraws the grid once scrolling has stopped.
type gridSettledMsg struct{}

// playBadgeW is the width of the play badge on a hovered tile.
const playBadgeW = 3

// NewAlbumGrid returns an empty grid with pad cells of margin on the left;
// on the right it ends at contentRight like every view.
func NewAlbumGrid(pad int) AlbumGrid { return AlbumGrid{pad: pad, hover: -1} }

// Items returns the albums shown.
func (g *AlbumGrid) Items() []*library.Album { return g.items }

// Len is the number of albums shown.
func (g *AlbumGrid) Len() int { return len(g.items) }

// Cursor is the selected index.
func (g *AlbumGrid) Cursor() int { return g.cursor }

// IsList reports whether the list layout is on.
func (g *AlbumGrid) IsList() bool { return g.list }

// SetList switches between the grid and list layouts.
func (g *AlbumGrid) SetList(on bool) {
	g.list = on
	g.ensureVisible()
}

// Selected returns the album under the cursor, or nil.
func (g *AlbumGrid) Selected() *library.Album {
	if g.cursor < 0 || g.cursor >= len(g.items) {
		return nil
	}
	return g.items[g.cursor]
}

// SetItems replaces the albums and keeps the cursor on the same album when it
// is still there, otherwise at the same index.
func (g *AlbumGrid) SetItems(items []*library.Album) {
	key := ""
	if a := g.Selected(); a != nil {
		key = a.Key
	}
	old := g.cursor
	g.items, g.hover = items, -1
	if !g.Select(key) {
		g.cursor = max(min(old, len(items)-1), 0)
	}
	g.ensureVisible()
}

// Reset replaces the albums and moves to the top.
func (g *AlbumGrid) Reset(items []*library.Album) {
	g.items, g.cursor, g.top, g.hover = items, 0, 0, -1
}

// Select moves the cursor to the album with key, if present.
func (g *AlbumGrid) Select(key string) bool {
	for i, a := range g.items {
		if a.Key == key {
			g.cursor = i
			g.ensureVisible()
			return true
		}
	}
	return false
}

func (g *AlbumGrid) gridMode() bool { return !g.list && g.geo.usable() }

func (g *AlbumGrid) perRow() int {
	if g.gridMode() {
		return g.geo.cols
	}
	return 1
}

// rowsVisible is how many whole rows fit.
func (g *AlbumGrid) rowsVisible() int {
	if g.gridMode() {
		return max(1, g.tilesH()/g.geo.pitch())
	}
	return max(1, g.h-1) // header row
}

// tilesH is how many rows the tiles may use: all but the last, which stays
// blank so captions and art never sit on the footer.
func (g *AlbumGrid) tilesH() int {
	if g.h > g.geo.pitch() {
		return g.h - 1
	}
	return g.h
}

func (g *AlbumGrid) totalRows() int {
	per := g.perRow()
	return (len(g.items) + per - 1) / per
}

func (g *AlbumGrid) clampTop() {
	g.top = max(min(g.top, g.totalRows()-g.rowsVisible()), 0)
}

func (g *AlbumGrid) ensureVisible() {
	r := g.cursor / g.perRow()
	vis := g.rowsVisible()
	if r < g.top {
		g.top = r
	}
	if r >= g.top+vis {
		g.top = r - vis + 1
	}
	g.clampTop()
}

// moveRows moves the cursor by delta rows, keeping the column where it can.
func (g *AlbumGrid) moveRows(delta int) {
	if len(g.items) == 0 {
		return
	}
	per := g.perRow()
	row, col := g.cursor/per, g.cursor%per
	last := len(g.items) - 1
	row = min(max(row+delta, 0), last/per)
	g.cursor = min(row*per+col, last)
	g.ensureVisible()
}

func (g *AlbumGrid) moveTo(i int) {
	if len(g.items) == 0 {
		return
	}
	g.cursor = min(max(i, 0), len(g.items)-1)
	g.ensureVisible()
}

// Scroll moves the viewport by n rows without moving the cursor.
func (g *AlbumGrid) Scroll(n int) {
	g.top += n
	g.clampTop()
}

// Action handles navigation and album commands. h/l at the grid edge (and
// always in the list layout) are not handled, so focus can move on.
func (g *AlbumGrid) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	top := g.top
	ok, cmd := g.action(a, n, c)
	return ok, tea.Batch(cmd, g.scrolled(top, c))
}

// scrolled notes a scroll from row top. During fast scrolling it asks for a
// redraw once scrolling has settled.
func (g *AlbumGrid) scrolled(top int, c *Ctx) tea.Cmd {
	if g.top == top || !g.gridMode() {
		return nil
	}
	fast := !g.scrolledAt.IsZero() && c.Now.Sub(g.scrolledAt) < scrollSettle
	g.scrolledAt = c.Now
	if !fast {
		return nil
	}
	g.fastAt = c.Now
	return tea.Tick(scrollSettle, func(time.Time) tea.Msg { return gridSettledMsg{} })
}

// settled reports whether tiles may be images: the grid is not in the
// middle of fast scrolling.
func (g *AlbumGrid) settled(now time.Time) bool {
	return g.fastAt.IsZero() || now.Sub(g.fastAt) >= scrollSettle
}

func (g *AlbumGrid) action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	n = max(n, 1)
	per := g.perRow()
	switch a {
	case keymap.NavDown, keymap.InputNext:
		g.moveRows(n)
	case keymap.NavUp, keymap.InputPrev:
		g.moveRows(-n)
	case keymap.NavTop:
		g.moveTo(0)
	case keymap.NavBottom:
		g.moveTo(len(g.items) - 1)
	case keymap.NavHalfDown:
		g.moveRows(n * max(1, g.rowsVisible()/2))
	case keymap.NavHalfUp:
		g.moveRows(-n * max(1, g.rowsVisible()/2))
	case keymap.NavPageDown:
		g.moveRows(n * g.rowsVisible())
	case keymap.NavPageUp:
		g.moveRows(-n * g.rowsVisible())
	case keymap.FocusLeft:
		col := g.cursor % per
		if per == 1 || col == 0 || len(g.items) == 0 {
			return false, nil
		}
		g.moveTo(g.cursor - min(n, col))
	case keymap.FocusRight:
		col := g.cursor % per
		if per == 1 || col == per-1 || g.cursor >= len(g.items)-1 {
			return false, nil
		}
		g.moveTo(g.cursor + min(n, per-1-col, len(g.items)-1-g.cursor))
	case keymap.ViewToggleGrid:
		g.SetList(!g.list)
	case keymap.Open, keymap.QueueAdd, keymap.QueuePlayNext, keymap.Menu, keymap.PagePlay, keymap.PageShuffle:
		al := g.Selected()
		if al == nil {
			return true, nil
		}
		return true, g.command(a, al, c)
	default:
		return false, nil
	}
	return true, nil
}

func (g *AlbumGrid) command(a keymap.Action, al *library.Album, c *Ctx) tea.Cmd {
	if isCatalogAlbum(al) {
		p := c.Origin.Add(g.cellOf(g.cursor))
		if g.gridMode() {
			p.Y++
		}
		return catalogAlbumAction(a, al, c, p.X, p.Y)
	}
	switch a {
	case keymap.Open:
		return c.Host.OpenAlbum(al)
	case keymap.PagePlay:
		return playAlbum(c, al)
	case keymap.PageShuffle:
		if len(al.Tracks) == 0 {
			return nil
		}
		src := player.Source{Kind: player.KindAlbum, ID: al.Key, Title: al.Title}
		return shufflePlay(c, src, al.Tracks, rand.IntN(len(al.Tracks)))
	case keymap.QueueAdd:
		return c.Host.AddToQueue(al.Tracks)
	case keymap.QueuePlayNext:
		return c.Host.PlayNext(al.Tracks)
	case keymap.Menu:
		p := c.Origin.Add(g.cellOf(g.cursor))
		if g.gridMode() {
			p.Y++ // the menu opens under the caption's artist line
		}
		return c.Host.Menu(p.X, p.Y, al.Tracks)
	}
	return nil
}

// cellOf is the local cell where item i's title starts.
func (g *AlbumGrid) cellOf(i int) image.Point {
	per := g.perRow()
	row := i/per - g.top
	if g.gridMode() {
		return image.Pt(g.geo.colX(i%per), row*g.geo.pitch()+g.geo.artH)
	}
	return image.Pt(g.pad+tableGutter, row+1)
}

// HitTest maps a local cell to an item index, or -1.
func (g *AlbumGrid) HitTest(x, y int) int {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return -1
	}
	var i int
	if g.gridMode() {
		geo := g.geo
		if y >= g.tilesH() {
			return -1
		}
		if y%geo.pitch() > geo.artH+1 || x < geo.pad {
			return -1
		}
		col := geo.colAt(x)
		if col < 0 {
			return -1
		}
		i = (g.top+y/geo.pitch())*geo.cols + col
	} else {
		if y == 0 || x < g.pad || x >= contentRight(g.w) {
			return -1
		}
		i = g.top + y - 1
	}
	if i >= len(g.items) {
		return -1
	}
	return i
}

// onBadge reports whether the local cell is on tile i's play badge, at the
// bottom left of its art.
func (g *AlbumGrid) onBadge(i, x, y int) bool {
	if i < 0 || !g.gridMode() {
		return false
	}
	geo := g.geo
	col := geo.colAt(x)
	return col >= 0 && y%geo.pitch() == geo.artH-1 && x-geo.colX(col) < playBadgeW
}

func (g *AlbumGrid) setHover(x, y int) {
	g.hover = g.HitTest(x, y)
	g.onPlay = g.onBadge(g.hover, x, y)
}

// Mouse handles the pointer in local coordinates. In the grid a click opens
// the album and a click on the hovered tile's play badge plays it; in the
// list a click selects and a double-click opens. Right-click opens the menu.
func (g *AlbumGrid) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	switch ev.Kind {
	case MouseWheel:
		top := g.top
		step := wheelRows
		if g.gridMode() {
			step = 1
		}
		g.Scroll(step * ev.wheelDelta())
		g.setHover(ev.X, ev.Y)
		return g.scrolled(top, c)
	case MouseMotion:
		g.setHover(ev.X, ev.Y)
		if g.onPlay {
			c.Hint("Play Album")
		}
	case MousePress:
		i := g.HitTest(ev.X, ev.Y)
		if i < 0 {
			return nil
		}
		g.cursor = i
		al := g.items[i]
		if isCatalogAlbum(al) {
			switch {
			case ev.Button == tea.MouseRight:
				return catalogAlbumAction(keymap.Menu, al, c, ev.ScreenX, ev.ScreenY)
			case ev.Button != tea.MouseLeft:
			case g.onBadge(i, ev.X, ev.Y):
				return catalogAlbumAction(keymap.PlayerPlayPause, al, c, 0, 0)
			case g.gridMode() || ev.Double:
				return c.Host.OpenAlbum(al)
			}
			return nil
		}
		switch {
		case ev.Button == tea.MouseRight:
			return c.Host.Menu(ev.ScreenX, ev.ScreenY, al.Tracks)
		case ev.Button != tea.MouseLeft:
		case g.onBadge(i, ev.X, ev.Y):
			return playAlbum(c, al)
		case g.gridMode() || ev.Double:
			return c.Host.OpenAlbum(al)
		}
	}
	return nil
}

// pressGuard keeps a double-click whole within one view. A single click on
// a tile opens the album, so the second press of a double-click lands on the
// album page; without the guard it would play whatever track is under it.
type pressGuard struct {
	seen bool
	x, y int
}

// double reports whether press ev completes a double-click whose first
// press this view also received.
func (g *pressGuard) double(ev MouseEvent) bool {
	d := ev.Double && g.seen && ev.X == g.x && ev.Y == g.y
	g.seen, g.x, g.y = true, ev.X, ev.Y
	return d
}

// playAlbum plays al from its first track.
func playAlbum(c *Ctx, al *library.Album) tea.Cmd {
	src := player.Source{Kind: player.KindAlbum, ID: al.Key, Title: al.Title}
	return c.Host.Play(src, al.Tracks, 0)
}

// ClearHover drops the hover highlight (the pointer left the grid).
func (g *AlbumGrid) ClearHover() { g.hover, g.onPlay = -1, false }

func (g *AlbumGrid) resize(w, h int) {
	if w == g.w && h == g.h {
		return
	}
	g.w, g.h = w, h
	g.geo = layoutGrid(w, g.pad)
	g.ensureVisible()
}

// Render draws exactly w×h cells. empty is the sentence shown when there are
// no albums.
func (g *AlbumGrid) Render(w, h int, c *Ctx, empty string) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	g.resize(w, h)
	g.clampTop()
	if len(g.items) == 0 {
		return emptyState(c, empty, w, h)
	}
	if g.gridMode() {
		return Lines(g.renderTiles(c), w, h) // Lines pads the blank last row
	}
	return Lines(g.renderList(c), w, h)
}

// emptyState is one dim sentence centred in a w×h block, a third of the way
// down like the track table's.
func emptyState(c *Ctx, msg string, w, h int) string {
	if c.LibraryLoading {
		msg = "Loading your library…"
	}
	lines := make([]string, h)
	lines[(h-1)/3] = Center(paintsFor(c.Theme).paint(toneDim, surfNone, msg), w)
	return Lines(lines, w, h)
}

// itemState is how a tile or row is highlighted.
type itemState uint8

const (
	statePlain itemState = iota
	stateHover
	stateSelected // selected, pane not focused
	stateFocused  // selected in the focused pane
)

// itemStateOf is the highlight for item i given the cursor and hover.
func itemStateOf(i, cursor, hover int, focused bool) itemState {
	switch {
	case i == cursor && focused:
		return stateFocused
	case i == cursor:
		return stateSelected
	case i == hover:
		return stateHover
	}
	return statePlain
}

// surface is the row background: sel when focused, hover under the pointer.
func (s itemState) surface() surface {
	switch s {
	case stateFocused:
		return surfSel
	case stateHover:
		return surfHover
	}
	return surfNone
}

// marker draws the one-cell selection marker: accent when focused, dim when
// the pane is not.
func (s itemState) marker(l *line, glyph string) {
	switch s {
	case stateFocused:
		l.add(toneAccent, glyph)
	case stateSelected:
		l.add(toneDim, glyph)
	default:
		l.pad(1)
	}
}

func (g *AlbumGrid) state(i int, c *Ctx) itemState {
	cursor := g.cursor
	if g.Passive {
		cursor = -1
	}
	return itemStateOf(i, cursor, g.hover, c.Focused)
}

// tileLook is how tile i's caption is drawn. Selection is the underline
// (sel: stateFocused, stateSelected or statePlain), so the caption's
// background only ever shows the hover.
func (g *AlbumGrid) tileLook(i int, c *Ctx) (sf surface, title tone, sel itemState) {
	sf, title, sel = surfNone, toneText, statePlain
	if i == g.hover {
		sf = surfHover
	}
	if i == g.cursor && !g.Passive {
		title, sel = toneBold, stateSelected
		if c.Focused {
			sel = stateFocused
		}
	}
	return sf, title, sel
}

func (g *AlbumGrid) renderTiles(c *Ctx) []string {
	t := c.Theme
	p := paintsFor(t)
	geo := g.geo
	playing := playingAlbumKey(c)
	crisp := g.settled(c.Now)
	th := g.tilesH()
	lines := make([]string, 0, th)
	var b strings.Builder
	line := func(cell func(k int) string, n int) {
		b.Reset()
		x := 0
		for k := range n {
			b.WriteString(strings.Repeat(" ", geo.colX(k)-x))
			b.WriteString(cell(k))
			x = geo.colX(k) + geo.tileW
		}
		lines = append(lines, b.String())
	}
	for r := g.top; len(lines) < th; r++ {
		start := r * geo.cols
		if start >= len(g.items) {
			break
		}
		row := g.items[start:min(start+geo.cols, len(g.items))]
		y := len(lines)
		vis := min(geo.artH, th-y)
		arts := make([][]string, len(row))
		whole := g.tileRow(c, row, y, vis, crisp)
		for k, a := range row {
			x := geo.colX(k)
			if whole {
				arts[k] = nil // blank under the row's image
			} else {
				arts[k] = strings.Split(c.TileArt(a.Key, a.ArtPID, x, y, geo.tileW, geo.artH, vis, crisp), "\n")
			}
			if start+k == g.hover && vis == geo.artH {
				c.CoverArt(x, y+geo.artH-1, playBadgeW, 1)
			}
		}
		for j := 0; j < geo.artH && len(lines) < th; j++ {
			line(func(k int) string {
				s := strings.Repeat(" ", geo.tileW)
				if j < len(arts[k]) {
					s = Fit(arts[k][j], geo.tileW)
				}
				if j == geo.artH-1 && start+k == g.hover {
					s = playBadge(t, g.onPlay) + ansi.Cut(s, playBadgeW, geo.tileW)
				}
				return s
			}, len(row))
		}
		if len(lines) < th {
			line(func(k int) string {
				a := row[k]
				sf, title, _ := g.tileLook(start+k, c)
				l := newLine(p, sf, geo.tileW)
				if a.Key == playing {
					l.add(toneAccent, t.G.Playing).pad(1)
				}
				return l.add(title, a.Title).String()
			}, len(row))
		}
		if len(lines) < th {
			line(func(k int) string {
				sf, _, _ := g.tileLook(start+k, c)
				return newLine(p, sf, geo.tileW).add(toneDim, row[k].Artist).String()
			}, len(row))
		}
		if len(lines) < th {
			line(func(k int) string {
				l := newLine(p, surfNone, geo.tileW)
				_, _, sel := g.tileLook(start+k, c)
				switch sel {
				case stateFocused:
					l.add(toneAccent, strings.Repeat(underlineGlyph, geo.tileW))
				case stateSelected:
					l.add(toneFaint, strings.Repeat(underlineGlyph, geo.tileW))
				}
				return l.String()
			}, len(row))
		}
	}
	return lines
}

// tileRow draws the art of a row of tiles as one image, where the terminal
// keeps only a few (see Ctx.TileRow), and reports whether it did.
func (g *AlbumGrid) tileRow(c *Ctx, row []*library.Album, y, vis int, crisp bool) bool {
	tiles := make([]RowTile, len(row))
	for k, a := range row {
		tiles[k] = RowTile{Key: a.Key, PID: a.ArtPID, X: g.geo.colX(k)}
	}
	return c.TileRow(tiles, y, g.geo.tileW, g.geo.artH, vis, crisp)
}

// underlineGlyph draws the selection underline under a tile's caption.
const underlineGlyph = "▔"

// playBadge is the play button drawn over a hovered tile's art: the play
// glyph cut out of an accent chip, brighter under the pointer.
func playBadge(t *theme.Theme, hot bool) string {
	st := ansi.Style{}.Reverse(true)
	if isColor(t.P.Accent) {
		st = st.ForegroundColor(t.P.Accent)
	}
	if hot {
		st = st.Bold()
	}
	return st.String() + " " + t.G.Playing + " " + ansi.ResetStyle
}

// albumListCols are the list layout's column widths.
type albumListCols struct{ title, artist, year, songs int }

func (g *AlbumGrid) listCols() albumListCols {
	const year, songs = 4, 5
	rest := g.listW() - tableGutter - 3*columnGap - year - songs
	title := rest * 9 / 20
	return albumListCols{title: max(title, 0), artist: max(rest-title, 0), year: year, songs: songs}
}

// listW is the list layout's width: from the margin to contentRight.
func (g *AlbumGrid) listW() int { return max(contentRight(g.w)-g.pad, 0) }

func (g *AlbumGrid) renderList(c *Ctx) []string {
	t := c.Theme
	p := paintsFor(t)
	cols := g.listCols()
	pad := strings.Repeat(" ", g.pad)
	inner := g.listW()
	playing := playingAlbumKey(c)
	lines := make([]string, 0, g.h)
	lines = append(lines, pad+newLine(p, surfNone, inner).pad(tableGutter).
		fit(toneDim, "Title", cols.title).pad(columnGap).
		fit(toneDim, "Artist", cols.artist).pad(columnGap).
		right(toneDim, "Year", cols.year).pad(columnGap).
		right(toneDim, "Songs", cols.songs).String())
	for i := g.top; i < len(g.items) && len(lines) < g.h; i++ {
		a := g.items[i]
		st := g.state(i, c)
		l := newLine(p, st.surface(), inner)
		st.marker(l, t.G.Marker)
		main := toneText
		if a.Key == playing {
			l.add(toneAccent, t.G.Playing)
			main = toneAccent
		} else {
			l.pad(1)
		}
		l.pad(1).fit(main, a.Title, cols.title).pad(columnGap).
			fit(toneDim, a.Artist, cols.artist).pad(columnGap).
			right(toneDim, yearText(a.Year), cols.year).pad(columnGap).
			right(toneDim, count(len(a.Tracks)), cols.songs)
		lines = append(lines, pad+l.String())
	}
	return lines
}

// playingAlbumKey is the key of the album the playing track belongs to.
func playingAlbumKey(c *Ctx) string {
	if it := c.Player.Catalog; it != nil {
		if it.AlbumID == "" {
			return ""
		}
		return player.CatalogAlbumPrefix + it.AlbumID
	}
	tr := c.Player.Track
	if tr == nil && c.Lib != nil {
		tr = c.Lib.Track(c.PlayingPID())
	}
	if tr == nil || tr.Album == nil {
		return ""
	}
	return tr.Album.Key
}

func yearText(y int) string {
	if y <= 0 {
		return ""
	}
	return itoa(y)
}

// inlineFilter is the one-line filter under a view's title.
type inlineFilter struct {
	query   string
	editing bool
}

// Active reports whether the filter is shown (being edited or applied).
func (f *inlineFilter) Active() bool { return f.editing || f.query != "" }

// Edit applies a key press to the query and reports whether it changed.
// esc and enter are left to the caller.
func (f *inlineFilter) Edit(msg tea.KeyPressMsg) bool {
	old := f.query
	switch msg.String() {
	case "backspace", "ctrl+h":
		if r := []rune(f.query); len(r) > 0 {
			f.query = string(r[:len(r)-1])
		}
	case "ctrl+w":
		f.query = deleteWord(f.query)
	case "ctrl+u":
		f.query = ""
	case "space":
		f.query += " "
	default:
		if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			f.query += msg.Text
		}
	}
	return f.query != old
}

// Paste appends pasted text while editing and reports whether the query
// changed.
func (f *inlineFilter) Paste(s string) bool {
	s = singleLine(s)
	if !f.editing || s == "" {
		return false
	}
	f.query += s
	return true
}

// Render draws the filter like the track table's: the search glyph, the query
// and, while editing, a caret. It is blank when the filter is off.
func (f *inlineFilter) Render(c *Ctx, w int) string {
	l := newLine(paintsFor(c.Theme), surfNone, w)
	if !f.Active() {
		return l.String()
	}
	l.add(toneDim, c.Theme.G.Search).pad(1)
	if f.editing {
		return l.add(toneText, f.query).add(toneAccent, "▏").String()
	}
	return l.add(toneDim, f.query).String()
}

// keysHint joins the first key of each action with "/" ("j/k"), skipping
// unbound ones.
func keysHint(c *Ctx, acts ...keymap.Action) string {
	if c == nil || c.Keys == nil {
		return ""
	}
	var ks []string
	for _, a := range acts {
		if k := c.Keys.KeysFor(a); len(k) > 0 {
			ks = append(ks, k[0])
		}
	}
	return strings.Join(ks, "/")
}

// moveHint is the footer's "move" hint: four ways in a grid, two in a list.
func (g *AlbumGrid) moveHint(c *Ctx) FooterHint {
	if g.gridMode() {
		return FooterHint{Keys: keysHint(c, keymap.FocusLeft, keymap.NavDown, keymap.NavUp, keymap.FocusRight), Label: "move"}
	}
	return FooterHint{Keys: keysHint(c, keymap.NavDown, keymap.NavUp), Label: "move"}
}

// filterHint is "/ filter", or "esc clear filter" while one is applied.
func filterHint(f *inlineFilter) FooterHint {
	if f.query != "" {
		return FooterHint{Keys: "esc", Label: "clear filter"}
	}
	return FooterHint{Action: keymap.Filter, Label: "filter"}
}

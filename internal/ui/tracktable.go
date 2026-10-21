package ui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/theme"
)

// Column is a track table column.
type Column uint8

const (
	ColTitle Column = iota
	ColArtist
	ColAlbum
	ColPlays
	ColAdded
	ColTime
	ColNumber // track number, for album track lists
	ColHeart  // favorite, two cells right before Time
	numColumns
)

var columnNames = [numColumns]string{"Title", "Artist", "Album", "Plays", "Added", "Time", "#", ""}

// wideFrom is the table width from which TableOptions.Wide columns show.
const wideFrom = 150

// TableOptions configure a TrackTable.
type TableOptions struct {
	// Source is the play context for enter and double-click. Its ID is only
	// passed on while the table shows the tracks in their own order.
	Source player.Source
	// Natural means the tracks have an order of their own (a playlist, an
	// album) that sorting can return to. Otherwise the tracks arrive sorted
	// by SortedBy, ascending.
	Natural  bool
	SortedBy library.SortField
	// Columns default to Title, Artist, Album, Time. Wide columns are added
	// before Time when the table is at least 150 cells wide.
	Columns []Column
	Wide    []Column
	// NoHeart hides the favorite column, for lists where every row would
	// show one (Favorites).
	NoHeart bool
	// Empty is the sentence shown when there are no tracks.
	Empty string
}

// TrackTable is a virtualized, sortable, filterable list of tracks. Only the
// visible rows are rendered, and rendered rows are cached by track, width and
// state, so it stays fast with any library size.
type TrackTable struct {
	opts TableOptions

	src    []*library.Track // as given
	sorted []*library.Track // src in the current sort order
	rows   []*library.Track // sorted, filtered

	sortSet bool
	sortCol Column
	desc    bool // with !sortSet: the table's own order, reversed

	query  string
	typing bool

	cursor, top int
	hover       int // body row index under the pointer, -1 for none
	hoverCol    int // header column under the pointer, -1 for none
	scrollDrag  bool
	press       pressGuard
	bodyH       int // body rows at the last render, for page motions
	width       int // width at the last render, for hit testing

	timeW int
	dur   time.Duration

	cache      map[rowKey]string
	cacheTheme *theme.Theme
	layW       int
	cols       []colSpec
}

type colSpec struct {
	c     Column
	x, w  int
	right bool
}

type rowKey struct {
	t  *library.Track
	w  int
	st uint8
}

const (
	rowSel uint8 = 1 << iota
	rowFocus
	rowHover
	rowPlaying
	rowFav
)

const (
	tableGutter = 3 // marker, playing glyph, space
	columnGap   = 2
	wheelRows   = 3
	maxRowCache = 2048
)

// NewTrackTable returns a table over ts.
func NewTrackTable(ts []*library.Track, opts TableOptions) *TrackTable {
	if len(opts.Columns) == 0 {
		opts.Columns = []Column{ColTitle, ColArtist, ColAlbum, ColTime}
	}
	t := &TrackTable{opts: opts, hover: -1, hoverCol: -1}
	if !opts.Natural {
		t.sortSet = true
		t.sortCol = sortColumn(opts.SortedBy)
	}
	t.setSource(ts)
	return t
}

func (t *TrackTable) setSource(ts []*library.Track) {
	t.src = ts
	t.dur = 0
	var longest time.Duration
	for _, tr := range ts {
		t.dur += tr.Duration
		longest = max(longest, tr.Duration)
	}
	t.timeW = max(clockWidth(longest), len(columnNames[ColTime]))
	t.layW = 0
	t.cache = nil
	t.resort()
}

// SetTracks replaces the tracks (after a library refresh), keeping the sort,
// the filter and the selected track.
func (t *TrackTable) SetTracks(ts []*library.Track) {
	pid, off := t.anchor()
	t.setSource(ts)
	t.restore(pid, off)
}

// anchor remembers the selected track and its screen offset.
func (t *TrackTable) anchor() (string, int) {
	if tr := t.Current(); tr != nil {
		return tr.PID, t.cursor - t.top
	}
	return "", 0
}

// restore puts the cursor back on pid at offset off from the top. When pid
// is gone (a refresh removed it), the cursor keeps its index and the view
// its scroll position, clamped to the new rows.
func (t *TrackTable) restore(pid string, off int) {
	for i, tr := range t.rows {
		if tr.PID == pid {
			t.cursor = i
			t.top = max(0, i-off)
			break
		}
	}
	t.clamp()
}

func (t *TrackTable) resort() {
	switch f, ok := sortField(t.sortCol); {
	case !t.sortSet && t.desc:
		t.sorted = slices.Clone(t.src)
		slices.Reverse(t.sorted)
	case !t.sortSet || !ok:
		t.sorted = t.src
	case !t.opts.Natural && f == t.opts.SortedBy && !t.desc:
		t.sorted = t.src
	default:
		t.sorted = library.SortTracks(t.src, f, t.desc)
	}
	t.refilter()
}

func (t *TrackTable) refilter() {
	if t.query == "" {
		t.rows = t.sorted
	} else {
		t.rows = search.FilterTracks(t.sorted, t.query)
	}
	t.clamp()
}

func (t *TrackTable) clamp() {
	n := len(t.rows)
	t.cursor = min(max(t.cursor, 0), max(n-1, 0))
	t.top = min(max(t.top, 0), max(n-max(t.bodyH, 1), 0))
}

// Len is the number of rows shown; Total is the number of tracks.
func (t *TrackTable) Len() int   { return len(t.rows) }
func (t *TrackTable) Total() int { return len(t.src) }

// Duration is the total length of all tracks.
func (t *TrackTable) Duration() time.Duration { return t.dur }

// Rows are the tracks in display order.
func (t *TrackTable) Rows() []*library.Track { return t.rows }

// Filtered reports whether a filter query narrows the rows.
func (t *TrackTable) Filtered() bool { return t.query != "" }

// FilterShown reports whether the filter line is visible.
func (t *TrackTable) FilterShown() bool { return t.typing || t.query != "" }

// Typing reports whether the filter input has focus.
func (t *TrackTable) Typing() bool { return t.typing }

// Current is the selected track, or nil.
func (t *TrackTable) Current() *library.Track {
	if t.cursor >= 0 && t.cursor < len(t.rows) {
		return t.rows[t.cursor]
	}
	return nil
}

// Selected returns the selected track as a slice.
func (t *TrackTable) Selected() []*library.Track {
	if tr := t.Current(); tr != nil {
		return []*library.Track{tr}
	}
	return nil
}

// Select moves the cursor to the track with pid, if shown.
func (t *TrackTable) Select(pid string) bool {
	for i, tr := range t.rows {
		if tr.PID == pid {
			t.moveTo(i)
			return true
		}
	}
	return false
}

// Center puts the cursor on the track with pid and scrolls it to the middle
// of a body bodyH rows tall, when the track is shown.
func (t *TrackTable) Center(pid string, bodyH int) bool {
	if pid == "" {
		return false
	}
	for i, tr := range t.rows {
		if tr.PID == pid {
			t.bodyH = max(bodyH, 1)
			t.cursor, t.top = i, i-t.bodyH/2
			t.clamp()
			return true
		}
	}
	return false
}

func (t *TrackTable) moveTo(i int) {
	t.cursor = i
	t.clamp()
	t.scrollToCursor()
}

func (t *TrackTable) scrollToCursor() {
	if t.bodyH <= 0 {
		return
	}
	h := t.bodyH
	off := min(2, h/4)
	if t.cursor < t.top+off {
		t.top = t.cursor - off
	}
	if t.cursor > t.top+h-1-off {
		t.top = t.cursor - h + 1 + off
	}
	t.clamp()
}

// source is the play context for the rows as shown. A playlist reordered or
// filtered here loses its ID, so the player can't fall back to Music's own
// playlist order.
func (t *TrackTable) source() player.Source {
	src := t.opts.Source
	if src.Kind == player.KindPlaylist && (t.sortSet || t.desc || t.query != "") {
		src.ID = ""
	}
	return src
}

// Play starts the row at i.
func (t *TrackTable) play(i int, c *Ctx) tea.Cmd {
	if i < 0 || i >= len(t.rows) || c.Host == nil {
		return nil
	}
	if !t.rows[i].Available {
		return c.Host.Status(player.MsgUnavailable)
	}
	return c.Host.Play(t.source(), t.rows, i)
}

// Action handles navigation and track actions.
func (t *TrackTable) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	page := max(t.bodyH, 1)
	switch a {
	case keymap.NavDown, keymap.InputNext:
		t.moveTo(t.cursor + n)
	case keymap.NavUp, keymap.InputPrev:
		t.moveTo(t.cursor - n)
	case keymap.NavTop:
		t.moveTo(0)
	case keymap.NavBottom:
		t.moveTo(len(t.rows) - 1)
	case keymap.NavHalfDown:
		t.scrollBy(n * page / 2)
	case keymap.NavHalfUp:
		t.scrollBy(-n * page / 2)
	case keymap.NavPageDown:
		t.scrollBy(n * page)
	case keymap.NavPageUp:
		t.scrollBy(-n * page)
	case keymap.Open:
		return true, t.play(t.cursor, c)
	case keymap.InputSubmit:
		t.typing = false
		if t.query == "" {
			t.refilter()
		}
	case keymap.InputCancel:
		t.clearFilter()
	case keymap.Filter:
		t.typing = true
	case keymap.Back:
		if t.query == "" && !t.typing {
			return false, nil
		}
		t.clearFilter()
	case keymap.TrackFavorite:
		if sel := t.Selected(); sel != nil && c.Host != nil {
			return true, c.Host.ToggleFavorite(sel)
		}
	case keymap.QueueAdd:
		if sel := t.Selected(); sel != nil && c.Host != nil {
			return true, c.Host.AddToQueue(sel)
		}
	case keymap.QueuePlayNext:
		if sel := t.Selected(); sel != nil && c.Host != nil {
			return true, c.Host.PlayNext(sel)
		}
	case keymap.ViewSort:
		t.cycleSort()
		return true, t.sortStatus(c)
	case keymap.ViewSortRev:
		t.reverseSort()
		return true, t.sortStatus(c)
	case keymap.Menu:
		if sel := t.Selected(); sel != nil && c.Host != nil {
			x := c.Origin.X + tableGutter
			y := c.Origin.Y + 1 + t.cursor - t.top
			return true, c.Host.Menu(x, y, sel)
		}
	default:
		return false, nil
	}
	return true, nil
}

// scrollBy moves the view and the cursor together, like ctrl+d in vim.
func (t *TrackTable) scrollBy(d int) {
	off := t.cursor - t.top
	t.cursor += d
	t.top += d
	t.clamp()
	if t.cursor-t.top != off {
		t.top = t.cursor - off
		t.clamp()
	}
	if h := t.bodyH; h > 0 {
		t.cursor = min(max(t.cursor, t.top), t.top+h-1)
		t.clamp()
	}
}

func (t *TrackTable) clearFilter() {
	pid, _ := t.anchor()
	t.typing = false
	if t.query == "" {
		return
	}
	t.query = ""
	t.refilter()
	t.restore(pid, max(t.bodyH, 1)/3)
}

// Key edits the filter query while typing.
func (t *TrackTable) Key(msg tea.KeyPressMsg) {
	q := []rune(t.query)
	switch msg.String() {
	case "backspace", "ctrl+h":
		if len(q) > 0 {
			q = q[:len(q)-1]
		}
	case "ctrl+w", "alt+backspace":
		q = []rune(deleteWord(string(q)))
	case "ctrl+u":
		q = q[:0]
	default:
		if msg.Text == "" || msg.Mod&(tea.ModCtrl|tea.ModAlt) != 0 {
			return
		}
		q = append(q, []rune(msg.Text)...)
	}
	t.setQuery(string(q))
}

// Paste appends pasted text to the query while typing.
func (t *TrackTable) Paste(s string) {
	if t.typing {
		t.setQuery(t.query + singleLine(s))
	}
}

func (t *TrackTable) setQuery(q string) {
	if q == t.query {
		return
	}
	t.query = q
	t.refilter()
	t.cursor, t.top = 0, 0
}

// Mouse handles pointer events; row 0 is the header.
func (t *TrackTable) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	n, w := len(t.rows), t.width
	inBody := ev.Y >= 1 && ev.Y <= t.bodyH && ev.X >= 0 && ev.X < w
	idx := -1
	if inBody && t.top+ev.Y-1 < n {
		idx = t.top + ev.Y - 1
	}
	onBar := ev.X == w-1 && n > t.bodyH && inBody

	switch ev.Kind {
	case MouseMotion:
		if t.scrollDrag && ev.Button == tea.MouseLeft {
			t.scrollTo(ev.Y - 1)
			return nil
		}
		t.hover, t.hoverCol = -1, -1
		switch {
		case ev.Y == 0 && ev.X >= 0 && ev.X < w:
			t.hoverCol = t.columnAt(ev.X)
		case !onBar:
			t.hover = idx
		}
		t.hint(ev.X, c)
	case MouseWheel:
		t.top += ev.wheelDelta() * wheelRows
		t.clamp()
		if inBody && t.top+ev.Y-1 < n {
			t.hover = t.top + ev.Y - 1
		}
		t.hint(ev.X, c)
	case MouseRelease:
		t.scrollDrag = false
	case MousePress:
		t.scrollDrag = false
		double := t.press.double(ev)
		switch {
		case ev.Y == 0 && ev.Button == tea.MouseLeft:
			if col := t.columnAt(ev.X); col >= 0 {
				t.toggleSort(Column(col))
			}
		case onBar && ev.Button == tea.MouseLeft:
			t.scrollDrag = true
			t.scrollTo(ev.Y - 1)
		case idx < 0:
		case ev.Button == tea.MouseLeft && t.heartAt(ev.X) && t.rows[idx].Available:
			t.cursor = idx
			if c.Host != nil {
				return c.Host.ToggleFavorite(t.Selected())
			}
		case ev.Button == tea.MouseLeft:
			t.cursor = idx
			if double {
				return t.play(idx, c)
			}
		case ev.Button == tea.MouseRight:
			t.cursor = idx
			if c.Host != nil {
				return c.Host.Menu(ev.ScreenX, ev.ScreenY, t.Selected())
			}
		}
	}
	return nil
}

// scrollTo maps a scrollbar row to a scroll position.
func (t *TrackTable) scrollTo(y int) {
	h := max(t.bodyH, 1)
	n := len(t.rows)
	if n <= h {
		return
	}
	y = min(max(y, 0), h-1)
	t.top = y * (n - h) / max(h-1, 1)
	t.clamp()
	t.cursor = min(max(t.cursor, t.top), t.top+h-1)
}

// columnAt is the sortable column whose header covers x, or -1.
func (t *TrackTable) columnAt(x int) int {
	for _, cs := range t.cols {
		if cs.c != ColHeart && x >= cs.x-1 && x < cs.x+cs.w+1 {
			return int(cs.c)
		}
	}
	return -1
}

// heartAt reports whether x is in the favorite column.
func (t *TrackTable) heartAt(x int) bool {
	for _, cs := range t.cols {
		if cs.c == ColHeart {
			return x >= cs.x && x < cs.x+cs.w
		}
	}
	return false
}

// HoverTip is the hint for what the pointer rests on at column x: what a
// click on the hovered column header does, "Not available" on a track
// Music can't play, the favorite toggle on the heart, else "".
func (t *TrackTable) HoverTip(x int, c *Ctx) string {
	if t.hoverCol >= 0 {
		return t.headerTip(Column(t.hoverCol), c)
	}
	if t.hover < 0 || t.hover >= len(t.rows) {
		return ""
	}
	return trackTip(t.rows[t.hover], t.heartAt(x), c)
}

// trackTip is the hint for a track row: "Not available" when Music can't
// play it, the favorite toggle when onHeart, else "".
func trackTip(tr *library.Track, onHeart bool, c *Ctx) string {
	switch {
	case !tr.Available:
		return "Not available"
	case onHeart && c.IsFavorite(tr):
		return c.HintFor("Unfavorite", keymap.TrackFavorite)
	case onHeart:
		return c.HintFor(keymap.Name(keymap.TrackFavorite), keymap.TrackFavorite)
	}
	return ""
}

// headerTip says what a click on col's header does (see toggleSort).
func (t *TrackTable) headerTip(col Column, c *Ctx) string {
	f, ok := sortField(col)
	switch {
	case !ok:
		return ""
	case t.sortSet && t.sortCol == col && t.desc != defaultDesc(f) && t.opts.Natural:
		return "Playlist Order"
	case t.sortSet && t.sortCol == col:
		return c.HintFor(keymap.Name(keymap.ViewSortRev), keymap.ViewSortRev)
	}
	return "Sort by " + columnNames[col]
}

// hint names what the pointer rests on in the footer (see Ctx.Hint).
func (t *TrackTable) hint(x int, c *Ctx) {
	if s := t.HoverTip(x, c); s != "" {
		c.Hint(s)
	}
}

// toggleSort is a header click: sort by col in its natural direction, then
// reverse, then (for tables with an order of their own) back to that order.
func (t *TrackTable) toggleSort(col Column) {
	pid, off := t.anchor()
	f, ok := sortField(col)
	switch {
	case !ok:
		if !t.opts.Natural {
			return
		}
		t.sortSet, t.desc = false, false
	case t.sortSet && t.sortCol == col && t.desc == defaultDesc(f):
		t.desc = !t.desc
	case t.sortSet && t.sortCol == col && t.opts.Natural:
		t.sortSet, t.desc = false, false
	default:
		t.sortSet, t.sortCol, t.desc = true, col, defaultDesc(f)
	}
	t.resort()
	t.restore(pid, off)
}

// sortCycle is the order view.sort steps through; tables with an order of
// their own (playlists) add it at the end.
var sortCycle = []Column{ColTitle, ColArtist, ColAlbum, ColTime, ColPlays, ColAdded}

// cycleSort moves to the next sort field, in that field's natural direction.
func (t *TrackTable) cycleSort() {
	pid, off := t.anchor()
	i := -1
	if t.sortSet {
		i = slices.Index(sortCycle, t.sortCol)
	}
	switch {
	case i+1 < len(sortCycle):
		col := sortCycle[i+1]
		f, _ := sortField(col)
		t.sortSet, t.sortCol, t.desc = true, col, defaultDesc(f)
	case t.opts.Natural:
		t.sortSet, t.desc = false, false
	default:
		t.sortSet, t.sortCol, t.desc = true, sortCycle[0], false
	}
	t.resort()
	t.restore(pid, off)
}

// reverseSort flips the direction of the current sort.
func (t *TrackTable) reverseSort() {
	if !t.sortSet && !t.opts.Natural {
		return
	}
	pid, off := t.anchor()
	t.desc = !t.desc
	t.resort()
	t.restore(pid, off)
}

// sortStatus says how the table is sorted only when the header can't: the
// sort column is off screen (Plays and Added in a narrow table) or a
// playlist runs in reverse. Otherwise the header arrow already shows it.
func (t *TrackTable) sortStatus(c *Ctx) tea.Cmd {
	if c.Host == nil || t.sortShown() {
		return nil
	}
	return c.Host.Status(t.sortLabel())
}

// sortShown reports whether the header shows the current sort.
func (t *TrackTable) sortShown() bool {
	if !t.sortSet {
		return !t.desc
	}
	for _, cs := range t.cols {
		if cs.c == t.sortCol {
			return true
		}
	}
	return false
}

func (t *TrackTable) sortLabel() string {
	if !t.sortSet {
		if t.desc {
			return "Playlist order, reversed"
		}
		return "Playlist order"
	}
	dir := [2]string{"A to Z", "Z to A"}
	switch t.sortCol {
	case ColTime:
		dir = [2]string{"shortest first", "longest first"}
	case ColPlays:
		dir = [2]string{"fewest first", "most first"}
	case ColAdded:
		dir = [2]string{"oldest first", "newest first"}
	}
	d := dir[0]
	if t.desc {
		d = dir[1]
	}
	return "Sorted by " + strings.ToLower(columnNames[t.sortCol]) + ", " + d
}

func sortField(c Column) (library.SortField, bool) {
	switch c {
	case ColTitle:
		return library.ByTitle, true
	case ColArtist:
		return library.ByArtist, true
	case ColAlbum:
		return library.ByAlbum, true
	case ColTime:
		return library.ByTime, true
	case ColPlays:
		return library.ByPlays, true
	case ColAdded:
		return library.ByAdded, true
	}
	return 0, false
}

func sortColumn(f library.SortField) Column {
	switch f {
	case library.ByArtist:
		return ColArtist
	case library.ByAlbum:
		return ColAlbum
	case library.ByTime:
		return ColTime
	case library.ByPlays:
		return ColPlays
	case library.ByAdded:
		return ColAdded
	}
	return ColTitle
}

// defaultDesc: counts and dates sort biggest/newest first.
func defaultDesc(f library.SortField) bool {
	return f == library.ByPlays || f == library.ByAdded || f == library.ByLastPlayed
}

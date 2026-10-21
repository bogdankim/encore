package ui

import (
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/search"
)

func init() {
	viewCtors.Albums = func(lib *library.Library) View { return NewAlbumsView(lib) }
	viewCtors.RecentlyAdded = func(lib *library.Library) View { return NewRecentView(lib) }
	viewCtors.Artists = func(lib *library.Library) View { return NewArtistsView(lib) }
	viewCtors.Album = func(a *library.Album) View { return NewAlbumView(a) }
	viewCtors.Artist = func(a *library.Artist) View { return NewArtistView(a) }
}

// listTop is how many rows sit above an album grid or artist list: the title
// line (with the controls) and the filter line, blank when no filter shows.
const listTop = 2

// albumSorts is the order the sort control cycles through, with the
// direction each field starts in.
var albumSorts = []struct {
	by    library.SortField
	desc  bool
	label string
}{
	{library.ByTitle, false, "Title"},
	{library.ByArtist, false, "Artist"},
	{library.ByYear, true, "Year"},
	{library.ByAdded, true, "Recently Added"},
}

// AlbumsView is the Albums and Recently Added pages: an album grid (or list)
// under a title line that carries the sort and layout controls on the right.
type AlbumsView struct {
	key, title string
	recent     bool // Recently Added: fixed newest-first order, no sort control

	lib    *library.Library // the library the view was last indexed with
	sort   int              // index into albumSorts
	desc   bool
	all    []*library.Album // sorted, unfiltered
	total  int
	filter inlineFilter
	grid   AlbumGrid

	zones  []albumZone // clickable controls on the title line, from the last Render
	hoverZ int
}

// albumZone is a clickable span on one line of a view, and what the footer
// calls it while the pointer rests on it.
type albumZone struct {
	x0, x1, y int
	do        func() tea.Cmd
	hint      string
}

// zoneAt returns the index of the zone under (x, y), or -1.
func zoneAt(zones []albumZone, x, y int) int {
	for i, z := range zones {
		if y == z.y && x >= z.x0 && x < z.x1 {
			return i
		}
	}
	return -1
}

// NewAlbumsView is the Albums page, sorted by title.
func NewAlbumsView(lib *library.Library) *AlbumsView {
	v := &AlbumsView{key: KeyAlbums, title: "Albums", grid: NewAlbumGrid(0), hoverZ: -1}
	v.Reindex(lib)
	return v
}

// NewRecentView is the Recently Added page, newest first.
func NewRecentView(lib *library.Library) *AlbumsView {
	v := &AlbumsView{key: KeyRecent, title: "Recently Added", recent: true, grid: NewAlbumGrid(0), hoverZ: -1}
	v.Reindex(lib)
	return v
}

func (v *AlbumsView) Key() string { return v.key }

func (v *AlbumsView) Title(c *Ctx) (string, string) {
	switch {
	case v.filter.query != "":
		return v.title, count(v.grid.Len()) + " of " + count(v.total)
	case v.total == 0:
		return v.title, ""
	}
	return v.title, count(v.total)
}

// ListLayout reports whether the list layout is on, for saving UI state.
func (v *AlbumsView) ListLayout() bool { return v.grid.IsList() }

// SetListLayout restores a saved layout choice.
func (v *AlbumsView) SetListLayout(on bool) { v.grid.SetList(on) }

func (v *AlbumsView) Reindex(lib *library.Library) View {
	v.lib = lib
	src := lib.Albums
	if v.recent {
		src = lib.RecentlyAdded
	}
	v.total = len(src)
	s := albumSorts[v.sort]
	if !v.recent && (s.by != library.ByTitle || v.desc) {
		src = library.SortAlbums(src, s.by, v.desc)
	}
	v.all = src
	v.grid.SetItems(search.FilterAlbums(v.all, v.filter.query))
	return v
}

func (v *AlbumsView) cycleSort(lib *library.Library) {
	v.sort = (v.sort + 1) % len(albumSorts)
	v.desc = albumSorts[v.sort].desc
	v.resort(lib)
}

func (v *AlbumsView) flipSort(lib *library.Library) {
	v.desc = !v.desc
	v.resort(lib)
}

// setSort sorts by albumSorts[i] in that field's usual direction.
func (v *AlbumsView) setSort(i int, lib *library.Library) {
	v.sort, v.desc = i, albumSorts[i].desc
	v.resort(lib)
}

// sortMenu opens the sort choices under the sort control, whose right edge
// is screen column x on row y:
// the fields, the active one checked, and Reverse.
func (v *AlbumsView) sortMenu(x, y int, c *Ctx) tea.Cmd {
	items := make([]MenuItem, 0, len(albumSorts)+2)
	for i, s := range albumSorts {
		mark := "  "
		if i == v.sort {
			mark = "✓ "
		}
		items = append(items, MenuItem{Label: mark + s.label, Run: func(Host) tea.Cmd {
			v.setSort(i, v.lib)
			return nil
		}})
	}
	items = append(items, MenuSeparator, MenuItem{Label: "  Reverse", Key: keymap.ViewSortRev, Run: func(Host) tea.Cmd {
		v.flipSort(v.lib)
		return nil
	}})
	m := NewMenu(x, y, nil, items...)
	if mo, ok := m.(*MenuOverlay); ok {
		m = rightAligned{mo, x}
	}
	return c.Host.PushOverlay(m)
}

// rightAligned is a menu whose right edge lines up with a control's right
// edge (right, exclusive), so it drops straight down from the control.
type rightAligned struct {
	*MenuOverlay
	right int
}

func (m rightAligned) Rect(screenW, screenH int) image.Rectangle {
	r := m.MenuOverlay.Rect(screenW, screenH)
	return r.Add(image.Pt(max(min(m.right, screenW)-r.Max.X, -r.Min.X), 0))
}

// sortLabel says how the albums are sorted, for the status line.
func (v *AlbumsView) sortLabel() string {
	s := albumSorts[v.sort]
	asc, desc := "A to Z", "Z to A"
	if s.by == library.ByYear || s.by == library.ByAdded {
		asc, desc = "oldest first", "newest first"
	}
	d := asc
	if v.desc {
		d = desc
	}
	return "Sorted by " + strings.ToLower(s.label) + ", " + d
}

// resort re-sorts and keeps the selected album in view.
func (v *AlbumsView) resort(lib *library.Library) {
	key := ""
	if a := v.grid.Selected(); a != nil {
		key = a.Key
	}
	v.Reindex(lib)
	if !v.grid.Select(key) {
		v.grid.moveTo(0)
	}
}

func (v *AlbumsView) applyFilter() {
	v.grid.Reset(search.FilterAlbums(v.all, v.filter.query))
}

// clearFilter drops the query and stays on the album that was selected.
func (v *AlbumsView) clearFilter() {
	key := ""
	if a := v.grid.Selected(); a != nil {
		key = a.Key
	}
	v.filter = inlineFilter{}
	v.grid.Reset(v.all)
	v.grid.Select(key)
}

// CapturesInput reports whether the filter field has the keyboard.
func (v *AlbumsView) CapturesInput() bool { return v.filter.editing }

// InputKey receives raw key presses while the filter is being edited.
func (v *AlbumsView) InputKey(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.clearFilter()
	case "enter":
		v.filter.editing = false
	default:
		if v.filter.Edit(msg) {
			v.applyFilter()
		}
	}
	return nil
}

// Paste appends pasted text to the filter while it is being edited.
func (v *AlbumsView) Paste(s string) {
	if v.filter.Paste(s) {
		v.applyFilter()
	}
}

func (v *AlbumsView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.Filter:
		v.filter.editing = true
		return true, nil
	case keymap.InputCancel:
		v.clearFilter()
		return true, nil
	case keymap.InputSubmit:
		v.filter.editing = false
		return true, nil
	case keymap.Back:
		if v.filter.Active() {
			v.clearFilter()
			return true, nil
		}
		return false, nil
	case keymap.ViewSort, keymap.ViewSortRev:
		if v.recent {
			return true, c.Host.Status("Recently Added is always newest first")
		}
		if a == keymap.ViewSort {
			v.cycleSort(c.Lib)
		} else {
			v.flipSort(c.Lib)
		}
		if len(v.zones) > 0 {
			return true, nil // the title line shows the sort
		}
		return true, c.Host.Status(v.sortLabel())
	}
	return v.grid.Action(a, n, c.at(0, listTop))
}

func (v *AlbumsView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	v.hoverZ = zoneAt(v.zones, ev.X, ev.Y)
	if v.hoverZ >= 0 && ev.Kind == MouseMotion {
		c.Hint(v.zones[v.hoverZ].hint)
	}
	if ev.Y < listTop {
		v.grid.ClearHover()
		if ev.Kind != MousePress || ev.Button != tea.MouseLeft {
			return nil
		}
		if v.hoverZ >= 0 {
			if v.hoverZ == 0 && !v.recent {
				z := v.zones[0]
				return v.sortMenu(c.Origin.X+z.x1, c.Origin.Y+z.y, c)
			}
			return v.zones[v.hoverZ].do()
		}
		if ev.Y == 1 && v.filter.Active() {
			v.filter.editing = true
		}
		return nil
	}
	return v.grid.Mouse(ev.local(0, listTop), c.at(0, listTop))
}

func (v *AlbumsView) Render(w, h int, c *Ctx) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	empty := "No albums in your library"
	if v.filter.query != "" {
		empty = "Nothing matches “" + v.filter.query + "”"
	}
	head := []string{v.titleRow(w, c), v.filter.Render(c, w)}
	if h <= listTop {
		return Lines(head, w, h)
	}
	return Lines(head, w, listTop) + "\n" + v.grid.Render(w, h-listTop, c.at(0, listTop), empty)
}

// barItem is one clickable control on the title line. A segment is one half
// of the Grid/List switch: padded, on the sel background when active.
type barItem struct {
	text    string
	active  bool
	segment bool
	gap     int // blank cells before it
	do      func() tea.Cmd
	hint    string
}

func (v *AlbumsView) controls(c *Ctx) []barItem {
	var items []barItem
	if !v.recent {
		s := albumSorts[v.sort]
		arrow := "↑"
		if v.desc {
			arrow = "↓"
		}
		// A click opens the sort menu (see Mouse); do is the keyboard's o.
		items = append(items, barItem{text: s.label + " " + arrow, hint: c.HintFor(keymap.Name(keymap.ViewSort), keymap.ViewSort),
			do: func() tea.Cmd { v.cycleSort(c.Lib); return nil }})
	}
	list := v.grid.IsList()
	return append(items,
		barItem{text: " Grid ", active: !list, segment: true, gap: 3, hint: c.HintFor("Grid", keymap.ViewToggleGrid),
			do: func() tea.Cmd { v.grid.SetList(false); return nil }},
		barItem{text: " List ", active: list, segment: true, hint: c.HintFor("List", keymap.ViewToggleGrid),
			do: func() tea.Cmd { v.grid.SetList(true); return nil }},
	)
}

// titleRow draws the title and meta on the left and the controls on the
// right, ending at contentRight like the grid and the list under them, so
// they stay put when the layout switches. It records the zones.
func (v *AlbumsView) titleRow(w int, c *Ctx) string {
	title, meta := v.Title(c)
	items := v.controls(c)
	items[0].gap = 0
	cw := 0
	for _, it := range items {
		cw += it.gap + Width(it.text)
	}
	x := contentRight(w) - cw
	v.zones = v.zones[:0]
	if x < Width(title)+4 {
		return titleLine(c, w, title, meta)
	}
	p := paintsFor(c.Theme)
	l := newLine(p, surfNone, w)
	l.raw(titleLine(c, x-2, title, meta), x-2)
	l.to(x)
	for i, it := range items {
		l.pad(it.gap)
		hot := i == v.hoverZ
		tn, sf := toneText, surfNone
		switch {
		case it.segment && it.active:
			sf = surfSel
		case it.segment && hot:
			sf = surfHover
		case it.segment:
			tn = toneDim
		case hot:
			sf = surfHover
		}
		x0 := l.col()
		l.raw(p.paint(tn, sf, it.text), Width(it.text))
		v.zones = append(v.zones, albumZone{x0, l.col(), 0, it.do, it.hint})
	}
	return l.String()
}

func (v *AlbumsView) Selected(c *Ctx) []*library.Track {
	if a := v.grid.Selected(); a != nil {
		return a.Tracks
	}
	return nil
}

// subject is the selected album.
func (v *AlbumsView) subject(c *Ctx) Subject {
	if a := v.grid.Selected(); a != nil {
		return Subject{album: a}
	}
	return Subject{}
}

// FooterHints are the grid's keys: move, open an album, filter.
func (v *AlbumsView) FooterHints(c *Ctx) []FooterHint {
	return []FooterHint{v.grid.moveHint(c), {Action: keymap.Open, Label: "open"}, filterHint(&v.filter)}
}

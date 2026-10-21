package ui

import (
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

type sbKind uint8

const (
	sbBlank sbKind = iota
	sbHeader
	sbSearch
	sbSection
	sbPlaylist
	sbFolder
)

type sbEntry struct {
	kind   sbKind
	label  string
	key    string // view key, for marking the open section
	action keymap.Action
	pl     *library.Playlist
	depth  int
}

// selectable entries take the keyboard cursor. Search is only clicked (the
// keyboard has its own search keys).
func (e sbEntry) selectable() bool { return e.kind >= sbSection }

var librarySections = []sbEntry{
	{kind: sbSection, label: "Home", key: KeyHome, action: keymap.GoHome},
	{kind: sbSection, label: "Recently Added", key: KeyRecent, action: keymap.GoRecent},
	{kind: sbSection, label: "Artists", key: KeyArtists, action: keymap.GoArtists},
	{kind: sbSection, label: "Albums", key: KeyAlbums, action: keymap.GoAlbums},
	{kind: sbSection, label: "Songs", key: KeySongs, action: keymap.GoSongs},
	{kind: sbSection, label: "Favorites", key: KeyFavorites, action: keymap.GoFavorites},
}

// sidebar is the Library sections and the playlist tree.
type sidebar struct {
	entries []sbEntry
	open    map[string]bool // expanded folders by pid
	cursor  int
	top     int
	hover   int
	height  int
	lib     *library.Library

	railH     int // rows the rail was drawn with
	railHover int // hovered rail item, -1 for none
}

func newSidebar(lib *library.Library) *sidebar {
	s := &sidebar{open: map[string]bool{}, hover: -1, railHover: -1}
	s.setLibrary(lib)
	return s
}

func (s *sidebar) setLibrary(lib *library.Library) {
	var cur string
	if s.cursor < len(s.entries) {
		cur = s.entries[s.cursor].id()
	}
	s.lib = lib
	s.rebuild()
	s.selectID(cur)
}

func (e sbEntry) id() string {
	if e.pl != nil {
		return "playlist:" + e.pl.PID
	}
	if e.key != "" {
		return e.key
	}
	return e.label
}

func (s *sidebar) rebuild() {
	s.entries = s.entries[:0]
	s.entries = append(s.entries,
		sbEntry{kind: sbSearch, label: "Search", action: keymap.SearchAll},
		sbEntry{kind: sbBlank},
		sbEntry{kind: sbHeader, label: "Library"})
	s.entries = append(s.entries, librarySections...)
	if s.lib != nil && len(s.lib.Playlists) > 0 {
		s.entries = append(s.entries, sbEntry{kind: sbBlank}, sbEntry{kind: sbHeader, label: "Playlists"})
		var add func(ps []*library.Playlist, depth int)
		add = func(ps []*library.Playlist, depth int) {
			for _, p := range ps {
				if p.Special == "favorites" {
					continue // Library's Favorites already shows these songs
				}
				e := sbEntry{kind: sbPlaylist, label: p.Name, key: "playlist:" + p.PID, pl: p, depth: depth}
				if p.Kind == "folder" {
					e.kind = sbFolder
				}
				s.entries = append(s.entries, e)
				if e.kind == sbFolder && s.open[p.PID] {
					add(p.Children, depth+1)
				}
			}
		}
		add(s.lib.Playlists, 0)
	}
	s.clamp()
}

func (s *sidebar) selectID(id string) {
	for i, e := range s.entries {
		if e.selectable() && e.id() == id {
			s.cursor = i
			s.scrollToCursor()
			return
		}
	}
	s.clamp()
}

// syncTo moves the cursor to the entry of the open view, if it has one.
func (s *sidebar) syncTo(key string) {
	for i, e := range s.entries {
		if e.selectable() && e.key == key && key != "" {
			s.cursor = i
			s.scrollToCursor()
			return
		}
	}
}

func (s *sidebar) clamp() {
	if s.cursor >= len(s.entries) {
		s.cursor = len(s.entries) - 1
	}
	if s.cursor < 0 || !s.entries[s.cursor].selectable() {
		s.cursor = s.step(max(s.cursor, 0), 1)
	}
	s.top = min(max(s.top, 0), max(len(s.entries)-max(s.height, 1), 0))
}

// step finds the next selectable entry from i in direction d (i included).
func (s *sidebar) step(i, d int) int {
	for j := i; j >= 0 && j < len(s.entries); j += d {
		if s.entries[j].selectable() {
			return j
		}
	}
	for j := i; j >= 0 && j < len(s.entries); j -= d {
		if s.entries[j].selectable() {
			return j
		}
	}
	return 0
}

func (s *sidebar) move(n int) {
	d := 1
	if n < 0 {
		d, n = -1, -n
	}
	i := s.cursor
	for ; n > 0; n-- {
		j := i + d
		for j >= 0 && j < len(s.entries) && !s.entries[j].selectable() {
			j += d
		}
		if j < 0 || j >= len(s.entries) {
			break
		}
		i = j
	}
	s.cursor = i
	s.scrollToCursor()
}

func (s *sidebar) scrollToCursor() {
	if s.height <= 0 {
		return // not drawn yet; render clamps once the height is known
	}
	h := s.height
	if s.cursor < s.top {
		s.top = s.cursor
		// keep a section header in view with its first item
		if s.top > 0 && !s.entries[s.top-1].selectable() && s.entries[s.top-1].kind == sbHeader {
			s.top--
		}
	}
	if s.cursor >= s.top+h {
		s.top = s.cursor - h + 1
	}
	s.top = min(max(s.top, 0), max(len(s.entries)-h, 0))
}

// action handles keys while the sidebar is focused.
func (s *sidebar) action(a *App, act keymap.Action, n int) (bool, tea.Cmd) {
	page := max(s.height, 1)
	switch act {
	case keymap.NavDown:
		s.move(n)
	case keymap.NavUp:
		s.move(-n)
	case keymap.NavTop:
		s.cursor = s.step(0, 1)
		s.scrollToCursor()
	case keymap.NavBottom:
		s.cursor = s.step(len(s.entries)-1, -1)
		s.scrollToCursor()
	case keymap.NavHalfDown:
		s.move(n * page / 2)
	case keymap.NavHalfUp:
		s.move(-n * page / 2)
	case keymap.NavPageDown:
		s.move(n * page)
	case keymap.NavPageUp:
		s.move(-n * page)
	case keymap.Open:
		return true, s.activate(a, s.cursor)
	case keymap.FocusRight:
		if e := s.cur(); e.kind == sbFolder && !s.open[e.pl.PID] {
			s.toggle(e.pl.PID)
			return true, nil
		}
		return false, nil
	case keymap.FocusLeft:
		if e := s.cur(); e.kind == sbFolder && s.open[e.pl.PID] {
			s.toggle(e.pl.PID)
			return true, nil
		}
		return false, nil
	case keymap.Menu:
		e := s.cur()
		if e.pl == nil {
			return true, nil
		}
		return true, a.PlaylistMenu(a.lay.SidebarBody.Min.X+3, a.lay.SidebarBody.Min.Y+s.cursor-s.top, e.pl)
	default:
		return false, nil
	}
	return true, nil
}

func (s *sidebar) cur() sbEntry {
	if s.cursor >= 0 && s.cursor < len(s.entries) {
		return s.entries[s.cursor]
	}
	return sbEntry{}
}

func (s *sidebar) toggle(pid string) {
	id := s.cur().id()
	s.open[pid] = !s.open[pid]
	s.rebuild()
	s.selectID(id)
}

// activate opens entry i: a section or playlist opens its view and moves
// focus to it, from the keyboard and the mouse alike; a folder expands or
// collapses.
func (s *sidebar) activate(a *App, i int) tea.Cmd {
	if i < 0 || i >= len(s.entries) {
		return nil
	}
	e := s.entries[i]
	s.cursor = i
	switch e.kind {
	case sbFolder:
		s.toggle(e.pl.PID)
		return nil
	case sbSearch:
		return a.Do(e.action)
	case sbSection:
		cmd := a.Do(e.action)
		a.setFocus(focusContent)
		return cmd
	case sbPlaylist:
		cmd := a.OpenPlaylist(e.pl)
		a.setFocus(focusContent)
		return cmd
	}
	return nil
}

func (s *sidebar) mouse(a *App, ev MouseEvent) tea.Cmd {
	idx := -1
	if ev.Y >= 0 && ev.Y < s.height && s.top+ev.Y < len(s.entries) && ev.X >= 0 {
		idx = s.top + ev.Y
	}
	switch ev.Kind {
	case MouseMotion:
		s.hover = -1
		if idx >= 0 && (s.entries[idx].selectable() || s.entries[idx].kind == sbSearch) {
			s.hover = idx
		}
	case MouseWheel:
		s.top += ev.wheelDelta() * wheelRows
		s.top = min(max(s.top, 0), max(len(s.entries)-max(s.height, 1), 0))
	case MousePress:
		if idx >= 0 && s.entries[idx].kind == sbSearch && ev.Button == tea.MouseLeft {
			return a.Do(keymap.SearchAll)
		}
		if idx < 0 || !s.entries[idx].selectable() {
			return nil
		}
		switch ev.Button {
		case tea.MouseLeft:
			return s.activate(a, idx)
		case tea.MouseRight:
			if e := s.entries[idx]; e.pl != nil {
				s.cursor = idx
				return a.PlaylistMenu(ev.ScreenX, ev.ScreenY, e.pl)
			}
		}
	}
	return nil
}

// playingKey is the key of the sidebar entry that owns the play context:
// Songs, Favorites or a playlist.
func playingKey(src player.Source) string {
	switch src.Kind {
	case player.KindSongs:
		if src.ID == "" && src.Title == "Songs" {
			return KeySongs
		}
	case player.KindFavorites:
		return KeyFavorites
	case player.KindPlaylist:
		return "playlist:" + src.ID
	}
	return ""
}

// render draws the sidebar into w×h. active is the open view's key.
func (s *sidebar) render(w, h int, focused bool, active string, c *Ctx) string {
	playing := ""
	if c.Player.NowPlaying.Track != nil {
		playing = playingKey(c.Player.Source)
	}
	if s.height != h {
		s.height = h
		s.scrollToCursor()
	}
	s.clamp()
	p := paintsFor(c.Theme)
	g := &c.Theme.G
	lines := make([]string, 0, h)
	for i := 0; i < h; i++ {
		idx := s.top + i
		if idx >= len(s.entries) {
			lines = append(lines, strings.Repeat(" ", w))
			continue
		}
		e := s.entries[idx]
		sel := idx == s.cursor
		sf := surfNone
		switch {
		case sel && focused:
			sf = surfSel
		case idx == s.hover && e.kind != sbSearch:
			sf = surfHover
		}
		l := newLine(p, sf, w)
		switch {
		case sel && focused:
			l.add(toneAccent, g.Marker)
		case sel && e.key != active:
			l.add(toneDim, g.Marker)
		default:
			l.pad(1)
		}
		tn := toneText
		if e.key != "" && e.key == active {
			tn = toneAccent
		}
		switch e.kind {
		case sbHeader:
			l.add(toneDim, e.label)
		case sbSearch:
			tn := toneDim
			if idx == s.hover {
				tn = toneText
			}
			l.add(tn, g.Search).pad(1).add(tn, e.label)
		case sbSection, sbPlaylist:
			l.pad(2 + 2*e.depth)
			labelWithMark(l, tn, e.label, playing != "" && e.key == playing, g.Playing)
		case sbFolder:
			glyph := g.Disclosure
			if s.open[e.pl.PID] {
				glyph = g.DisclosureOpen
			}
			l.pad(2*e.depth).add(toneDim, glyph).pad(1)
			labelWithMark(l, tn, e.label, false, "")
		}
		lines = append(lines, l.String())
	}
	return strings.Join(lines, "\n")
}

// labelWithMark adds label to l, followed by a dim playing glyph when mark
// is set; the label gives way so the glyph always fits. Either way the last
// cell stays blank, so a cut name never touches the rule.
func labelWithMark(l *line, tn tone, label string, mark bool, glyph string) {
	if !mark {
		if room := l.w - l.n - 1; ansi.StringWidth(label) > room {
			label = truncate(label, max(room, 0))
		}
		l.add(tn, label)
		return
	}
	room := l.w - l.n - 1 - ansi.StringWidth(glyph) - 1
	if room <= 0 {
		l.add(tn, truncate(label, max(l.w-l.n-1, 0)))
		return
	}
	if ansi.StringWidth(label) > room {
		label = ansi.Truncate(label, room, "…")
	}
	l.add(tn, label).pad(1).add(toneDim, glyph)
}

// railItem is one icon of the rail, the folded sidebar.
type railItem struct {
	label  string // for the hover hint
	key    string // the view it opens, for the active state
	action keymap.Action
	nerd   string // icon in the Nerd Font set
	plain  string // icon in plain Unicode
}

var railItems = []railItem{
	{label: "Search", action: keymap.SearchAll},
	{label: "Home", key: KeyHome, action: keymap.GoHome, nerd: "\U000F02DC", plain: "⌂"},
	{label: "Recently Added", key: KeyRecent, action: keymap.GoRecent, nerd: "\U000F0150", plain: "◷"},
	{label: "Artists", key: KeyArtists, action: keymap.GoArtists, nerd: "\U000F0CE9", plain: "⚇"},
	{label: "Albums", key: KeyAlbums, action: keymap.GoAlbums, nerd: "\U000F0025", plain: "◉"},
	{label: "Songs", key: KeySongs, action: keymap.GoSongs, nerd: "\U000F0F74", plain: "♪"},
	{label: "Favorites", key: KeyFavorites, action: keymap.GoFavorites, nerd: "\U000F02D5", plain: "♡"},
	{label: "Playlists", key: "playlist:", action: keymap.GoPlaylists, nerd: "\U000F0CB9", plain: "≡"},
}

func (it railItem) glyph(g *theme.Glyphs) string {
	switch {
	case it.action == keymap.SearchAll:
		return g.Search
	case g.Nerd:
		return it.nerd
	}
	return it.plain
}

// railRows maps the rail's rows to items (-1 for a gap): Search, the
// Library sections and Playlists. With room, every icon gets a blank row
// below it and the groups two, so neighbouring icons never touch; with less,
// only the groups are apart.
func railRows(h int) []int {
	n := len(railItems)
	spaced := []int{0, -1, -1}
	for i := 1; i < n-1; i++ {
		spaced = append(spaced, i, -1)
	}
	spaced = append(spaced, -1, n-1)
	grouped := []int{0, -1}
	for i := 1; i < n-1; i++ {
		grouped = append(grouped, i)
	}
	grouped = append(grouped, -1, n-1)
	tight := make([]int, n)
	for i := range tight {
		tight[i] = i
	}
	for _, rows := range [][]int{spaced, grouped, tight} {
		if h >= len(rows) {
			return rows
		}
	}
	return tight[:max(h, 0)]
}

// railAt returns the item at a rail row, or -1.
func (s *sidebar) railAt(x, y int) int {
	if x < 0 || x >= railW || y < 0 {
		return -1
	}
	rows := railRows(s.railH)
	if y >= len(rows) {
		return -1
	}
	return rows[y]
}

// railActive reports whether item it stands for the open view.
func railActive(it railItem, active string) bool {
	if it.key == "" || active == "" {
		return false
	}
	if it.key == "playlist:" {
		return strings.HasPrefix(active, it.key)
	}
	return it.key == active
}

// renderRail draws the rail into railW×h: dim icons, the open section's in
// accent on the selection surface, the hovered one on the hover surface.
func (s *sidebar) renderRail(h int, active string, c *Ctx) string {
	s.railH = h
	p := paintsFor(c.Theme)
	g := &c.Theme.G
	rows := railRows(h)
	lines := make([]string, h)
	for y := range lines {
		i := -1
		if y < len(rows) {
			i = rows[y]
		}
		if i < 0 {
			lines[y] = strings.Repeat(" ", railW)
			continue
		}
		it := railItems[i]
		// the open section's icon sits on the selection block, as its row
		// does in the sidebar; accent alone is easy to miss in a column of
		// dim icons
		sf, tn := surfNone, toneDim
		switch {
		case railActive(it, active):
			sf, tn = surfSel, toneAccent
		case i == s.railHover:
			sf, tn = surfHover, toneText
		}
		l := newLine(p, sf, railW)
		l.pad(1).add(tn, it.glyph(g))
		lines[y] = l.String()
	}
	return strings.Join(lines, "\n")
}

// railMouse handles the rail: hover names the icon in the footer, a click
// opens its section, and Playlists opens the sidebar as a drawer.
func (s *sidebar) railMouse(a *App, ev MouseEvent) tea.Cmd {
	i := s.railAt(ev.X, ev.Y)
	switch ev.Kind {
	case MouseMotion:
		s.railHover = i
		if i >= 0 {
			it := railItems[i]
			a.setHint(a.hintText(it.label, it.action))
		}
	case MousePress:
		if i < 0 || ev.Button != tea.MouseLeft {
			return nil
		}
		if it := railItems[i]; it.action == keymap.GoPlaylists {
			return a.openDrawer(true)
		} else {
			return a.Do(it.action)
		}
	}
	return nil
}

// drawer is the full sidebar shown over the content while the rail is on.
// Choosing a section or playlist closes it.
type drawer struct{ a *App }

func (a *App) openDrawer(toPlaylists bool) tea.Cmd {
	if _, ok := a.topOverlay().(*drawer); ok {
		return nil
	}
	s := a.sidebar
	s.hover, s.railHover = -1, -1
	if a.nav.cur != nil {
		s.syncTo(a.nav.cur.Key())
	}
	if toPlaylists {
		for i, e := range s.entries {
			if e.pl != nil {
				s.cursor = i
				break
			}
		}
	}
	return a.PushOverlay(&drawer{a: a})
}

func (d *drawer) width() int { return sidebarWidth(d.a.sidebarW) }

func (d *drawer) Rect(w, h int) image.Rectangle {
	m := d.a.lay.Middle
	return image.Rect(0, m.Min.Y, min(d.width()+1, w), m.Max.Y)
}

func (d *drawer) Render(c *Ctx) string {
	r := d.Rect(d.a.w, d.a.h)
	sw, h := r.Dx()-1, r.Dy()
	p := paintsFor(c.Theme)
	rule := p.paint(toneFaint, surfNone, c.Theme.G.RuleV)
	active := ""
	if d.a.nav.cur != nil {
		active = d.a.nav.cur.Key()
	}
	body := strings.Split(d.a.sidebar.render(sw, max(h-middlePadTop, 0), true, active, c), "\n")
	lines := make([]string, h)
	for y := range lines {
		text := strings.Repeat(" ", sw)
		if j := y - middlePadTop; j >= 0 && j < len(body) {
			text = body[j]
		}
		lines[y] = text + rule
	}
	return strings.Join(lines, "\n")
}

func (d *drawer) CapturesInput() bool               { return false }
func (d *drawer) KeyContext() keymap.Context        { return keymap.Sidebar }
func (d *drawer) Key(tea.KeyPressMsg, *Ctx) tea.Cmd { return nil }
func (d *drawer) Update(tea.Msg, *Ctx) tea.Cmd      { return nil }
func (d *drawer) Close()                            { d.a.sidebar.hover = -1 }

func (d *drawer) Action(act keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	a, s := d.a, d.a.sidebar
	switch act {
	case keymap.Back, keymap.InputCancel:
		return false, nil // the app closes the drawer
	case keymap.SidebarToggle, keymap.GoPlaylists:
		return true, a.CloseOverlay()
	case keymap.Open:
		return true, d.choose(s.cursor)
	}
	if ok, cmd := s.action(a, act, n); ok {
		return true, cmd
	}
	return false, nil
}

// choose activates entry i; anything but a folder closes the drawer first,
// so a search it opens lands on top.
func (d *drawer) choose(i int) tea.Cmd {
	s := d.a.sidebar
	if i < 0 || i >= len(s.entries) {
		return nil
	}
	if s.entries[i].kind != sbFolder {
		d.a.CloseOverlay()
	}
	return s.activate(d.a, i)
}

func (d *drawer) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	s := d.a.sidebar
	ev = ev.local(0, middlePadTop)
	if ev.X >= d.width() {
		ev.X, ev.Y = -1, -1
	}
	if ev.Kind == MousePress && ev.Button == tea.MouseLeft {
		if idx := s.top + ev.Y; ev.Y >= 0 && ev.Y < s.height && idx < len(s.entries) && (s.entries[idx].selectable() || s.entries[idx].kind == sbSearch) {
			return d.choose(idx)
		}
		return nil
	}
	return s.mouse(d.a, ev)
}

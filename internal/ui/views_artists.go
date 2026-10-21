package ui

import (
	"cmp"
	"image"
	"math/rand/v2"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
)

// An artist page lists its topSongs most played songs.
const topSongs = 5

// artistPage is one artist: name, counts, top songs and the album grid. It is
// the right half of Artists and the whole of an artist view.
type artistPage struct {
	artist  *library.Artist
	songs   []*library.Track // every track, most played first
	top     *TrackTable      // the top songs' columns and rows
	songCur int
	inGrid  bool
	hover   int // song index under the pointer, -1 when none
	grid    AlbumGrid

	w, h   int
	songsY int // first song line, from the last Render
	songsN int // songs shown
	gridY  int
	zones  []albumZone // header buttons, from the last Render
	hoverZ int
	press  pressGuard

	// Set for an Apple Music artist (CatalogArtistView): the top songs
	// come from Apple rather than play counts, there are topN of them,
	// the header is head's, and the albums' empty state is empty's.
	cat   bool
	topN  int
	head  func(w, h int, c *Ctx) pageHeader
	empty func(c *Ctx) string
}

func newArtistPage(a *library.Artist) artistPage {
	p := artistPage{grid: NewAlbumGrid(headerHang), hover: -1, hoverZ: -1}
	p.bind(a)
	return p
}

// bind shows a. Rebinding the same artist (from a new library) keeps the
// album cursor.
func (p *artistPage) bind(a *library.Artist) {
	same := p.artist != nil && a != nil && p.artist.Name == a.Name
	p.artist = a
	p.songs = p.songs[:0]
	if a == nil {
		p.grid.Reset(nil)
		p.top = nil
		return
	}
	for _, al := range a.Albums {
		p.songs = append(p.songs, al.Tracks...)
	}
	slices.SortStableFunc(p.songs, func(x, y *library.Track) int {
		if c := cmp.Compare(y.Plays, x.Plays); c != 0 {
			return c
		}
		return y.LastPlayed.Compare(x.LastPlayed)
	})
	top := p.songs[:min(topSongs, len(p.songs))]
	cols := []Column{ColTitle, ColAlbum, ColTime}
	if oneAlbum(top) {
		cols = []Column{ColTitle, ColTime}
	}
	p.top = NewTrackTable(top, TableOptions{Source: p.source(), Natural: true, Columns: cols})
	if same {
		p.grid.SetItems(a.Albums)
		return
	}
	p.grid.Reset(a.Albums)
	p.songCur, p.inGrid, p.hover = 0, false, -1
}

// bindCatalog shows an Apple Music artist: albums (catalog albums, no
// tracks) and top songs (stand-ins) as they arrive. Rebinding the same
// artist keeps the cursors.
func (p *artistPage) bindCatalog(name string, albums []*library.Album, songs []*library.Track) {
	same := p.artist != nil && p.artist.Name == name
	p.artist = &library.Artist{Name: name, Albums: albums, FoldName: library.Fold(name)}
	p.songs = append(p.songs[:0], songs...)
	top := p.songs[:min(p.topCount(), len(p.songs))]
	cols := []Column{ColTitle, ColAlbum, ColTime}
	if len(top) > 0 && oneAlbum(top) {
		cols = []Column{ColTitle, ColTime}
	}
	p.top = NewTrackTable(top, TableOptions{Source: p.source(), Natural: true, Columns: cols, NoHeart: true, LibMark: true})
	if same {
		p.grid.SetItems(albums)
		return
	}
	p.grid.Reset(albums)
	p.songCur, p.inGrid, p.hover = 0, false, -1
}

// topCount is how many top songs the page lists.
func (p *artistPage) topCount() int {
	if p.topN > 0 {
		return p.topN
	}
	return topSongs
}

func artistMeta(a *library.Artist) string {
	if a == nil {
		return ""
	}
	return joinMeta(plural(len(a.Albums), "album", "albums"), plural(a.Tracks, "song", "songs"))
}

func (p *artistPage) source() player.Source {
	if p.cat {
		return player.Source{Kind: player.KindCatalog, ID: "artist:" + p.artist.Name, Title: p.artist.Name}
	}
	return player.Source{Kind: player.KindArtist, ID: p.artist.Name, Title: p.artist.Name}
}

// allTracks is every song of the artist, album by album as the page lists
// them; for an Apple Music artist, the top songs.
func (p *artistPage) allTracks() []*library.Track {
	if p.cat {
		return p.songs
	}
	var ts []*library.Track
	for _, al := range p.artist.Albums {
		ts = append(ts, al.Tracks...)
	}
	return ts
}

// playAll plays every song of the artist in album order.
func (p *artistPage) playAll(c *Ctx) tea.Cmd {
	return c.Host.Play(p.source(), p.allTracks(), 0)
}

// shuffleAll plays the artist's songs shuffled, from a random one.
func (p *artistPage) shuffleAll(c *Ctx) tea.Cmd {
	ts := p.allTracks()
	if len(ts) == 0 {
		return nil
	}
	return shufflePlay(c, p.source(), ts, rand.IntN(len(ts)))
}

// header draws the artist's name, counts and Play / Shuffle beside the
// newest album's art, and records the button zones.
func (p *artistPage) header(w, h int, c *Ctx) []string {
	if p.head != nil {
		p.zones = p.zones[:0]
		return p.head(w, h, c).render(w, h, c, p.hoverZ, &p.zones)
	}
	a := p.artist
	hd := pageHeader{
		title:   a.Name,
		meta:    []string{artistMeta(a)},
		tail:    openInTail(c, keymap.OpenArtistInCatalog, Subject{artist: a}),
		buttons: pageButtons(c, "Artist", func() tea.Cmd { return p.playAll(c) }, func() tea.Cmd { return p.shuffleAll(c) }),
	}
	if len(a.Albums) > 0 {
		hd.art, hd.artPID = a.Albums[0].Key, a.Albums[0].ArtPID // Albums are newest first
	}
	p.zones = p.zones[:0]
	return hd.render(w, h, c, p.hoverZ, &p.zones)
}

func (p *artistPage) Selected() []*library.Track {
	if p.artist == nil {
		return nil
	}
	if p.inGrid {
		if a := p.grid.Selected(); a != nil {
			return a.Tracks
		}
		return nil
	}
	if p.songCur < len(p.songs) {
		return []*library.Track{p.songs[p.songCur]}
	}
	return nil
}

func (p *artistPage) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	if p.artist == nil {
		return false, nil
	}
	switch a {
	case keymap.PagePlay: // the header's, not the selected album's
		return true, p.playAll(c)
	case keymap.PageShuffle:
		return true, p.shuffleAll(c)
	}
	n = max(n, 1)
	if p.inGrid {
		leave := p.songsN > 0 && (a == keymap.NavTop ||
			a == keymap.NavUp && p.grid.Cursor() < p.grid.perRow())
		if leave {
			p.inGrid, p.songCur = false, p.songsN-1
			if a == keymap.NavTop {
				p.songCur = 0
			}
			return true, nil
		}
		cc := c.at(0, p.gridY)
		cc.Focused = true
		return p.grid.Action(a, n, cc)
	}
	switch a {
	case keymap.NavDown:
		if p.songCur+n < p.songsN {
			p.songCur += n
		} else if p.grid.Len() > 0 {
			p.inGrid = true
		} else {
			p.songCur = p.songsN - 1
		}
	case keymap.NavHalfDown, keymap.NavPageDown, keymap.NavBottom:
		if p.grid.Len() == 0 {
			p.songCur = p.songsN - 1
			return true, nil
		}
		p.inGrid = true
		if a == keymap.NavBottom {
			p.grid.moveTo(p.grid.Len() - 1)
		}
	case keymap.NavUp, keymap.NavHalfUp, keymap.NavPageUp, keymap.NavTop:
		p.songCur = max(p.songCur-n, 0)
		if a != keymap.NavUp {
			p.songCur = 0
		}
	case keymap.Open:
		return true, c.Host.Play(p.source(), p.songs, p.songCur)
	case keymap.TrackFavorite:
		return true, c.Host.ToggleFavorite(p.Selected())
	case keymap.QueueAdd:
		return true, c.Host.AddToQueue(p.Selected())
	case keymap.QueuePlayNext:
		return true, c.Host.PlayNext(p.Selected())
	case keymap.Menu:
		pt := c.Origin.Add(image.Pt(tableGutter, p.songsY+p.songCur))
		return true, c.Host.Menu(pt.X, pt.Y, p.Selected())
	default:
		return false, nil
	}
	return true, nil
}

// subject is the selected album in the grid or the selected top song, else
// the artist (a library one: an Apple Music page says who in its view).
func (p *artistPage) subject() Subject {
	switch {
	case p.artist == nil:
		return Subject{}
	case p.inGrid:
		if a := p.grid.Selected(); a != nil {
			return Subject{album: a}
		}
	case p.songCur < p.songsN && p.songCur < len(p.songs):
		return Subject{track: p.songs[p.songCur]}
	}
	if p.cat {
		return Subject{}
	}
	return Subject{artist: p.artist}
}

// songAt maps a local cell to a song index, or -1.
func (p *artistPage) songAt(x, y int) int {
	if y < p.songsY || y >= p.songsY+p.songsN || x < 0 || x >= p.songsW() {
		return -1
	}
	return y - p.songsY
}

func (p *artistPage) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	if p.artist == nil {
		return nil
	}
	p.hoverZ = zoneAt(p.zones, ev.X, ev.Y)
	if p.hoverZ >= 0 && ev.Kind == MouseMotion {
		c.Hint(p.zones[p.hoverZ].hint)
	}
	if ev.Kind == MousePress && p.hoverZ >= 0 && ev.Button == tea.MouseLeft {
		p.press.double(ev)
		return p.zones[p.hoverZ].do()
	}
	if ev.Y >= p.gridY && p.gridY > 0 {
		p.hover = -1
		ge := ev.local(0, p.gridY)
		if ev.Kind == MousePress && p.grid.HitTest(ge.X, ge.Y) >= 0 {
			p.inGrid = true
		}
		return p.grid.Mouse(ge, c.at(0, p.gridY))
	}
	p.grid.ClearHover()
	i := p.songAt(ev.X, ev.Y)
	switch ev.Kind {
	case MouseMotion, MouseWheel:
		p.hover = i
		if i >= 0 && ev.Kind == MouseMotion {
			if s := trackTip(p.songs[i], p.onHeart(ev.X), c); s != "" {
				c.Hint(s)
			}
		}
	case MousePress:
		double := p.press.double(ev)
		if i < 0 {
			return nil
		}
		p.songCur, p.inGrid = i, false
		switch {
		case ev.Button == tea.MouseRight:
			return c.Host.Menu(ev.ScreenX, ev.ScreenY, p.Selected())
		case ev.Button == tea.MouseLeft && p.onHeart(ev.X) && p.songs[i].Available:
			return c.Host.ToggleFavorite(p.Selected())
		case ev.Button == tea.MouseLeft && double:
			return c.Host.Play(p.source(), p.songs, i)
		}
	}
	return nil
}

// Render draws the page; focused is whether the page has the keyboard.
func (p *artistPage) Render(w, h int, c *Ctx, focused bool) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	p.w, p.h = w, h
	if p.artist == nil {
		return emptyState(c, "No artist selected", w, h)
	}
	pt := paintsFor(c.Theme)
	label := func(s string) string { return newLine(pt, surfNone, w).pad(headerHang).add(toneDim, s).String() }
	lines := append(p.header(w, h, c), "")
	n := min(p.topCount(), len(p.songs))
	if h-len(lines)-(n+2)-1 < layoutGrid(w, headerHang).artH+2 {
		n = 0 // too short: the albums matter more
	}
	p.songsN = n
	p.songsY = len(lines) + 1
	if n > 0 {
		p.songCur = min(p.songCur, n-1)
		lines = append(lines, label("Top Songs"))
		cursor := p.songCur
		if p.inGrid {
			cursor = -1
		}
		lines = append(lines, p.top.listRows(p.songsW(), cursor, p.hover, focused, c)...)
		lines = append(lines, "")
	} else {
		p.inGrid = true
	}
	lines = append(lines, label("Albums"))
	p.gridY = len(lines)
	if p.gridY >= h {
		return Lines(lines, w, h)
	}
	cc := c.at(0, p.gridY)
	cc.Focused = focused && p.inGrid
	p.grid.Passive = !p.inGrid
	empty := "No albums"
	if p.empty != nil {
		empty = p.empty(c)
		cc.LibraryLoading = false
	}
	return Lines(lines, w, p.gridY) + "\n" + p.grid.Render(w, h-p.gridY, cc, empty)
}

// songsW is the top songs' width: the page's, so they end where the album
// grid under them does.
func (p *artistPage) songsW() int { return p.w }

// onHeart reports whether local column x is on the favorite column.
func (p *artistPage) onHeart(x int) bool { return p.top != nil && p.top.heartAt(x) }

// oneAlbum reports whether every track is from the same album.
func oneAlbum(ts []*library.Track) bool {
	for _, t := range ts {
		if t.Album != ts[0].Album || t.AlbumName != ts[0].AlbumName {
			return false
		}
	}
	return true
}

// ArtistsView is the Artists page: an artist list on the left and the
// selected artist's page on the right, under the title line.
type ArtistsView struct {
	all, items []*library.Artist
	cursor     int
	top        int
	hover      int
	filter     inlineFilter
	right      bool // the artist page has the keyboard
	press      pressGuard
	page       artistPage

	w, h, listW, listH int
}

// artistsGap is the blank space each side of the rule between the columns.
const artistsGap = 2

// NewArtistsView lists every artist.
func NewArtistsView(lib *library.Library) *ArtistsView {
	v := &ArtistsView{hover: -1, page: newArtistPage(nil)}
	v.Reindex(lib)
	return v
}

func (v *ArtistsView) Key() string { return KeyArtists }

func (v *ArtistsView) Title(c *Ctx) (string, string) {
	switch {
	case v.filter.query != "":
		return "Artists", count(len(v.items)) + " of " + count(len(v.all))
	case len(v.all) == 0:
		return "Artists", ""
	}
	return "Artists", count(len(v.all))
}

func (v *ArtistsView) selected() *library.Artist {
	if v.cursor < 0 || v.cursor >= len(v.items) {
		return nil
	}
	return v.items[v.cursor]
}

func (v *ArtistsView) Reindex(lib *library.Library) View {
	name := ""
	if a := v.selected(); a != nil {
		name = a.Name
	}
	v.all = lib.Artists
	v.items = search.FilterArtists(v.all, v.filter.query)
	v.cursor = max(slices.IndexFunc(v.items, func(a *library.Artist) bool { return a.Name == name }), 0)
	v.page.bind(v.selected())
	v.ensureVisible()
	return v
}

func (v *ArtistsView) ensureVisible() {
	if v.listH <= 0 {
		return
	}
	if v.cursor < v.top {
		v.top = v.cursor
	}
	if v.cursor >= v.top+v.listH {
		v.top = v.cursor - v.listH + 1
	}
	v.clampTop()
}

func (v *ArtistsView) clampTop() {
	v.top = max(min(v.top, len(v.items)-v.listH), 0)
}

func (v *ArtistsView) moveTo(i int) {
	if len(v.items) == 0 {
		return
	}
	i = min(max(i, 0), len(v.items)-1)
	if i != v.cursor {
		v.cursor = i
		v.page.bind(v.selected())
	}
	v.ensureVisible()
}

// setItems shows items with the cursor on name, or on the first artist.
func (v *ArtistsView) setItems(items []*library.Artist, name string) {
	v.items = items
	v.top = 0
	v.cursor = max(slices.IndexFunc(items, func(a *library.Artist) bool { return a.Name == name }), 0)
	v.page.bind(v.selected())
	v.ensureVisible()
}

func (v *ArtistsView) clearFilter() {
	name := ""
	if a := v.selected(); a != nil {
		name = a.Name
	}
	v.filter = inlineFilter{}
	v.setItems(v.all, name)
}

func (v *ArtistsView) applyFilter() {
	v.setItems(search.FilterArtists(v.all, v.filter.query), "")
}

// CapturesInput reports whether the filter field has the keyboard.
func (v *ArtistsView) CapturesInput() bool { return v.filter.editing }

// InputKey receives raw key presses while the filter is being edited.
func (v *ArtistsView) InputKey(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
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
func (v *ArtistsView) Paste(s string) {
	if v.filter.Paste(s) {
		v.applyFilter()
	}
}

// pageX is where the artist page starts.
func (v *ArtistsView) pageX() int { return v.listW + 1 + artistsGap }

func (v *ArtistsView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.Filter:
		v.filter.editing, v.right = true, false
		return true, nil
	case keymap.InputCancel:
		v.clearFilter()
		return true, nil
	case keymap.InputSubmit:
		v.filter.editing = false
		return true, nil
	}
	if v.right {
		handled, cmd := v.page.Action(a, n, c.at(v.pageX(), listTop))
		if !handled && (a == keymap.FocusLeft || a == keymap.Back) {
			v.right = false
			return true, nil
		}
		return handled, cmd
	}
	n = max(n, 1)
	page := max(v.listH, 1)
	switch a {
	case keymap.NavDown, keymap.InputNext:
		v.moveTo(v.cursor + n)
	case keymap.NavUp, keymap.InputPrev:
		v.moveTo(v.cursor - n)
	case keymap.NavTop:
		v.moveTo(0)
	case keymap.NavBottom:
		v.moveTo(len(v.items) - 1)
	case keymap.NavHalfDown:
		v.moveTo(v.cursor + n*max(page/2, 1))
	case keymap.NavHalfUp:
		v.moveTo(v.cursor - n*max(page/2, 1))
	case keymap.NavPageDown:
		v.moveTo(v.cursor + n*page)
	case keymap.NavPageUp:
		v.moveTo(v.cursor - n*page)
	case keymap.Open, keymap.FocusRight:
		if v.selected() == nil || v.listW >= v.w {
			return a == keymap.Open, nil
		}
		v.right = true
	case keymap.Back:
		if v.filter.Active() {
			v.clearFilter()
			return true, nil
		}
		return false, nil
	case keymap.QueueAdd:
		return true, c.Host.AddToQueue(v.Selected(c))
	case keymap.QueuePlayNext:
		return true, c.Host.PlayNext(v.Selected(c))
	case keymap.PagePlay, keymap.PageShuffle:
		if v.selected() == nil {
			return true, nil
		}
		return v.page.Action(a, n, c) // the selected artist's page
	case keymap.Menu:
		pt := c.Origin.Add(image.Pt(2, listTop+v.cursor-v.top))
		return true, c.Host.Menu(pt.X, pt.Y, v.Selected(c))
	default:
		return false, nil
	}
	return true, nil
}

// artistAt maps a local cell in the list column to an artist index, or -1.
func (v *ArtistsView) artistAt(x, y int) int {
	y -= listTop
	if x < 0 || x >= v.listW || y < 0 || y >= v.listH {
		return -1
	}
	i := v.top + y
	if i >= len(v.items) {
		return -1
	}
	return i
}

func (v *ArtistsView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	if ev.Y >= listTop && ev.X >= v.pageX() && v.listW < v.w {
		v.hover = -1
		if ev.Kind == MousePress {
			v.right = true
		}
		return v.page.Mouse(ev.local(v.pageX(), listTop), c.at(v.pageX(), listTop))
	}
	v.page.hover, v.page.hoverZ = -1, -1
	v.page.grid.ClearHover()
	i := v.artistAt(ev.X, ev.Y)
	switch ev.Kind {
	case MouseWheel:
		v.top += wheelRows * ev.wheelDelta()
		v.clampTop()
		v.hover = v.artistAt(ev.X, ev.Y)
	case MouseMotion:
		v.hover = i
	case MousePress:
		double := v.press.double(ev)
		if ev.Y == 1 && ev.Button == tea.MouseLeft && v.filter.Active() {
			v.filter.editing = true
			return nil
		}
		if i < 0 {
			return nil
		}
		v.moveTo(i)
		v.right = double && v.listW < v.w
		if ev.Button == tea.MouseRight {
			return c.Host.Menu(ev.ScreenX, ev.ScreenY, v.Selected(c))
		}
	}
	return nil
}

// artistListWidth is the list column's width (the rule follows it).
func artistListWidth(w int) int { return min(max(w*3/10, 20), 30) }

func (v *ArtistsView) Render(w, h int, c *Ctx) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	v.w, v.h = w, h
	title, meta := v.Title(c)
	head := []string{titleLine(c, w, title, meta), v.filter.Render(c, w)}
	if h <= listTop {
		return Lines(head, w, h)
	}
	bh := h - listTop
	if len(v.all) == 0 {
		return Lines(head, w, listTop) + "\n" + emptyState(c, "No artists in your library", w, bh)
	}
	v.listW = artistListWidth(w)
	if w-v.pageX() < 24 {
		v.listW, v.right = w, false // too narrow for the page: list only
	}
	v.listH = bh
	v.ensureVisible()

	list := v.renderList(c, bh)
	body := list
	if v.listW < w {
		rule := VRule(c.Theme.Faint, c.Theme.G.RuleV, bh)
		gap := Blank(artistsGap, bh)
		page := v.page.Render(w-v.pageX(), bh, c.at(v.pageX(), listTop), c.Focused && v.right)
		body = joinColumns(list, rule, gap, page)
	}
	return Lines(head, w, listTop) + "\n" + body
}

func (v *ArtistsView) renderList(c *Ctx, h int) string {
	t := c.Theme
	p := paintsFor(t)
	inner := v.listW
	if v.listW < v.w {
		inner -= artistsGap
	}
	lines := make([]string, 0, h)
	if len(v.items) == 0 {
		lines = append(lines, newLine(p, surfNone, inner).pad(tableGutter).add(toneDim, "No matches").String())
	}
	nameW := inner - 1 - tableGutter // a blank cell before the rule
	if v.listW >= v.w {
		nameW = contentRight(inner) - tableGutter
	}
	focused := c.Focused && !v.right
	for i := v.top; i < len(v.items) && len(lines) < h; i++ {
		st := itemStateOf(i, v.cursor, v.hover, focused)
		l := newLine(p, st.surface(), inner)
		st.marker(l, t.G.Marker)
		l.to(tableGutter).fit(toneText, v.items[i].Name, nameW)
		lines = append(lines, l.String())
	}
	return Lines(lines, v.listW, h)
}

// joinColumns places equally tall blocks side by side.
func joinColumns(blocks ...string) string {
	split := make([][]string, len(blocks))
	n := 0
	for i, b := range blocks {
		split[i] = strings.Split(b, "\n")
		n = max(n, len(split[i]))
	}
	var b strings.Builder
	for y := range n {
		if y > 0 {
			b.WriteByte('\n')
		}
		for _, col := range split {
			if y < len(col) {
				b.WriteString(col[y])
			}
		}
	}
	return b.String()
}

func (v *ArtistsView) Selected(c *Ctx) []*library.Track {
	if v.right {
		return v.page.Selected()
	}
	a := v.selected()
	if a == nil {
		return nil
	}
	var ts []*library.Track
	for _, al := range a.Albums {
		ts = append(ts, al.Tracks...)
	}
	return ts
}

// ArtistView is one artist's page at full width (Go to Artist). The page's
// own header stands in for the title line.
type ArtistView struct {
	name string
	page artistPage
}

// NewArtistView opens artist a.
func NewArtistView(a *library.Artist) *ArtistView {
	return &ArtistView{name: a.Name, page: newArtistPage(a)}
}

func (v *ArtistView) Key() string { return "artist:" + v.name }

func (v *ArtistView) Title(c *Ctx) (string, string) { return v.name, artistMeta(v.page.artist) }

func (v *ArtistView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	return v.page.Action(a, n, c)
}

func (v *ArtistView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd { return v.page.Mouse(ev, c) }

func (v *ArtistView) Render(w, h int, c *Ctx) string { return v.page.Render(w, h, c, c.Focused) }

func (v *ArtistView) Selected(c *Ctx) []*library.Track { return v.page.Selected() }

func (v *ArtistView) subject(c *Ctx) Subject { return v.page.subject() }

// subject is the page's selection while it has the keyboard, else the
// selected artist.
func (v *ArtistsView) subject(c *Ctx) Subject {
	if v.right {
		return v.page.subject()
	}
	if a := v.selected(); a != nil {
		return Subject{artist: a}
	}
	return Subject{}
}

func (v *ArtistView) Reindex(lib *library.Library) View {
	a := lib.Artist(v.name)
	if a == nil {
		return nil
	}
	v.page.bind(a)
	return v
}

// FooterHints follow the part of the page that has the keyboard (enter
// plays a top song and opens an album), then the header's Play and
// Shuffle.
func (p *artistPage) FooterHints(c *Ctx) []FooterHint {
	if p.inGrid {
		return append([]FooterHint{p.grid.moveHint(c), {Action: keymap.Open, Label: "open"}}, pageHints...)
	}
	return append([]FooterHint{{Keys: keysHint(c, keymap.NavDown, keymap.NavUp), Label: "move"}, {Action: keymap.Open, Label: "play"}}, pageHints...)
}

// FooterHints are the list's keys, or the artist page's while it has the
// keyboard.
func (v *ArtistsView) FooterHints(c *Ctx) []FooterHint {
	if v.right {
		return append(v.page.FooterHints(c)[:2], FooterHint{Action: keymap.FocusLeft, Label: "back"})
	}
	return []FooterHint{
		{Keys: keysHint(c, keymap.NavDown, keymap.NavUp), Label: "move"},
		{Action: keymap.Open, Label: "open"},
		filterHint(&v.filter),
	}
}

func (v *ArtistView) FooterHints(c *Ctx) []FooterHint { return v.page.FooterHints(c) }

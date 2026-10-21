package ui

import (
	"context"
	"errors"
	"image"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/theme"
)

const (
	searchGroupLimit = 5   // hits per kind for scope All
	searchListLimit  = 300 // hits for a single scope
	searchHistoryLen = 8
)

// searchHistory holds recently submitted queries, newest first. It outlives
// a single overlay; only the UI goroutine touches it.
var searchHistory []string

func rememberSearch(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return
	}
	searchHistory = slices.DeleteFunc(searchHistory, func(s string) bool { return strings.EqualFold(s, q) })
	searchHistory = append([]string{q}, searchHistory...)
	if len(searchHistory) > searchHistoryLen {
		searchHistory = searchHistory[:searchHistoryLen]
	}
}

// scopeCatalog is the Apple Music tab: the catalog instead of the library.
const scopeCatalog = search.Playlists + 1

type searchScope struct {
	scope             search.Scope
	name, placeholder string
}

var searchScopes = []searchScope{
	{search.All, "All", "Search"},
	{search.Songs, "Songs", "Search songs"},
	{search.Albums, "Albums", "Search albums"},
	{search.Artists, "Artists", "Search artists"},
	{search.Playlists, "Playlists", "Search playlists"},
	{scopeCatalog, "Apple Music", "Search Apple Music"},
}

func searchScopeIndex(s search.Scope) int {
	for i, sc := range searchScopes {
		if sc.scope == s {
			return i
		}
	}
	return 0
}

// scopes are the tabs this overlay shows: all of them, or without Apple
// Music when there is no catalog.
func (s *SearchOverlay) scopes() []searchScope {
	if s.cat == nil {
		return searchScopes[:len(searchScopes)-1]
	}
	return searchScopes
}

const (
	// catalogDebounce is how long typing must stop before the Apple Music
	// tab asks the catalog: Apple allows about 20 requests a minute, and a
	// search costs three.
	catalogDebounce = 350 * time.Millisecond
	// catalogSearchLimit is how many artists, albums and songs a catalog
	// search asks for.
	catalogSearchLimit = 10
)

// catalogDebounceMsg fires catalogDebounce after a keystroke in the Apple
// Music tab; gen is the overlay's catalog generation at that keystroke.
type catalogDebounceMsg struct {
	owner *SearchOverlay
	gen   uint64
}

// catalogSearchMsg carries a catalog search's results.
type catalogSearchMsg struct {
	owner *SearchOverlay
	gen   uint64
	query string
	res   catalog.Results
	err   error
}

// searchResultsMsg carries the hits for query generation gen of one overlay.
type searchResultsMsg struct {
	owner *SearchOverlay
	gen   uint64
	res   search.Results
}

type searchRowKind uint8

const (
	srSpacer searchRowKind = iota
	srHeading
	srSong
	srAlbum
	srArtist
	srPlaylist
	srRecent
	srCatPrompt // All: "Search Apple Music for “…”"
	srCatSong
	srCatAlbum
	srCatArtist
	srNote // a dim line, not selectable
)

type searchRow struct {
	kind  searchRowKind
	text  string       // heading or recent query
	scope search.Scope // heading: the scope it expands to
	i     int          // index into the Results slice of its kind
	top   bool         // shown under Top results
}

func (r searchRow) selectable() bool { return r.kind >= srSong && r.kind != srNote }

// SearchOverlay is the search overlay: an input, scope tabs and live results.
type SearchOverlay struct {
	scope search.Scope
	in    ovInput
	gen   uint64
	res   search.Results
	rows  []searchRow
	cur   ovCursor

	w, h    int
	tabs    []image.Rectangle // local rects of the scope tabs, from the last render
	listY   int               // local row of the first result row
	listH   int
	pending bool          // a search for the current text is running
	idx     *search.Index // the index the shown results came from

	// The Apple Music tab. catRes answers catQuery; catSongs are the
	// stand-ins for its songs.
	cat       Catalog
	catGen    uint64
	catRes    catalog.Results
	catQuery  string
	catErr    error
	catSongs  []*library.Track
	catWait   bool      // typed; the debounce is running
	catBusy   bool      // a request is out
	catSince  time.Time // when it went out
	catSlow   bool      // it has run catalogQuiet: "Searching Apple Music…"
	catCancel context.CancelFunc
}

// NewSearch opens search in scope, without the Apple Music tab.
func NewSearch(scope search.Scope) Overlay { return NewSearchIn(scope, nil) }

// NewSearchIn opens search in scope, with an Apple Music tab searching
// cat when cat is not nil.
func NewSearchIn(scope search.Scope, cat Catalog) *SearchOverlay {
	s := &SearchOverlay{scope: scope, cur: ovCursor{Hover: -1}, cat: cat}
	if scope == scopeCatalog && cat == nil {
		s.scope = search.All
	}
	s.in = newOvInput(searchScopes[searchScopeIndex(s.scope)].placeholder)
	s.rebuild()
	return s
}

// Close cancels a catalog search in flight.
func (s *SearchOverlay) Close() { s.stopCatalog() }

func (s *SearchOverlay) stopCatalog() {
	if s.catCancel != nil {
		s.catCancel()
		s.catCancel = nil
	}
	s.catBusy, s.catSlow = false, false
}

func (s *SearchOverlay) Rect(screenW, screenH int) image.Rectangle {
	w := ovSize(screenW, 70, min(88, screenW-4), 110)
	fullH := ovSize(screenH, 60, min(22, screenH-4), 40)
	h := fullH
	switch n := s.listRows(); {
	case n == 0:
		h = 2 + 3 // the input, a blank line and the tabs
	case n > 0:
		h = min(fullH, 2+searchHeaderH+max(n, searchMinRows)+2) // the rows and the hints
	case s.h > 0:
		h = s.h // a search is running: keep the size until it answers
	}
	// The top stays where the full-height overlay has it, so the input
	// doesn't move when results arrive.
	r := ovPlace(screenW, screenH, w, fullH)
	r.Max.Y = r.Min.Y + min(h, r.Dy())
	s.w, s.h = r.Dx(), r.Dy()
	return r
}

// searchHeaderH is the input, a blank line, the tabs and the rule.
const searchHeaderH = 4

// searchMinRows is the fewest list rows the overlay shrinks to around a
// few results or the "No results" note.
const searchMinRows = 3

// listRows is the number of list rows the overlay sizes itself to: the
// rows, one line for the "No results" note, 0 for an empty query with no
// recent searches, or -1 while the first search for the text is running.
func (s *SearchOverlay) listRows() int {
	if s.scope == scopeCatalog {
		switch {
		case len(s.rows) > 0:
			return len(s.rows)
		case s.query() == "":
			return 0
		case s.catSlow || s.catErr != nil || s.catQuery != "":
			return 1 // the searching, busy or "No results" line
		}
		return -1
	}
	switch {
	case len(s.rows) > 0:
		return len(s.rows)
	case s.query() == "":
		return 0
	case s.pending || s.res.Query == "":
		return -1
	}
	return 1
}

// Paste puts pasted text in the field and searches.
func (s *SearchOverlay) Paste(text string, c *Ctx) tea.Cmd {
	if s.in.Paste(text) {
		return s.run(c)
	}
	return nil
}

func (s *SearchOverlay) CapturesInput() bool { return true }

// query is the trimmed input text.
func (s *SearchOverlay) query() string { return strings.TrimSpace(s.in.Value()) }

// run starts a search for the current text and scope.
func (s *SearchOverlay) run(c *Ctx) tea.Cmd {
	if s.scope == scopeCatalog {
		return s.runCatalog()
	}
	s.gen++
	q := s.query()
	if q == "" || c.Index == nil {
		s.pending = false
		s.res = search.Results{Scope: s.scope}
		s.rebuild()
		return nil
	}
	s.pending = true
	s.idx = c.Index
	gen, scope, idx := s.gen, s.scope, c.Index
	limit := searchListLimit
	if scope == search.All {
		limit = searchGroupLimit
	}
	return func() tea.Msg {
		return searchResultsMsg{owner: s, gen: gen, res: idx.Search(q, scope, limit)}
	}
}

// runCatalog searches Apple Music for the text once typing stops for
// catalogDebounce. The results on screen stay until new ones arrive, unless
// the search takes catalogQuiet.
func (s *SearchOverlay) runCatalog() tea.Cmd {
	s.catGen++
	s.stopCatalog()
	q := s.query()
	if q == "" || q == s.catQuery && s.catErr == nil {
		s.catWait = false
		s.rebuild()
		return nil
	}
	s.catWait = true
	gen := s.catGen
	return tea.Tick(catalogDebounce, func(time.Time) tea.Msg { return catalogDebounceMsg{owner: s, gen: gen} })
}

// startCatalog sends the search once the debounce has run out.
func (s *SearchOverlay) startCatalog(c *Ctx) tea.Cmd {
	s.catWait = false
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	s.catCancel = cancel
	s.catBusy, s.catSince = true, c.Now
	cat, q, gen := s.cat, s.query(), s.catGen
	return tea.Batch(catalogQuietTick(), func() tea.Msg {
		defer cancel()
		res, err := cat.Search(ctx, q, catalogSearchLimit)
		return catalogSearchMsg{owner: s, gen: gen, query: q, res: res, err: err}
	})
}

func (s *SearchOverlay) updateCatalog(msg tea.Msg, c *Ctx) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case catalogDebounceMsg:
		if m.owner == s && m.gen == s.catGen && s.scope == scopeCatalog && s.cat != nil {
			return s.startCatalog(c), true
		}
		return nil, true
	case catalogSearchMsg:
		if m.owner != s || m.gen != s.catGen {
			return nil, true
		}
		s.catBusy, s.catSlow, s.catCancel = false, false, nil
		if errors.Is(m.err, context.Canceled) {
			return nil, true
		}
		s.catRes, s.catQuery, s.catErr = m.res, m.query, m.err
		if m.err != nil && !m.res.Empty() {
			s.catErr = nil // show what did arrive
		}
		s.catSongs = catalogTracks(m.res.Songs)
		s.rebuild()
		return nil, true
	case catalogTickMsg:
		if s.catBusy && !s.catSlow && c.Now.Sub(s.catSince) >= catalogQuiet {
			s.catSlow = true
			s.rebuild()
		}
		return nil, true
	}
	return nil, false
}

func (s *SearchOverlay) Update(msg tea.Msg, c *Ctx) tea.Cmd {
	if cmd, ok := s.updateCatalog(msg, c); ok {
		return cmd
	}
	m, ok := msg.(searchResultsMsg)
	if !ok {
		if c.Index != s.idx && s.query() != "" && s.scope != scopeCatalog {
			return s.run(c) // the library was swapped under us
		}
		return nil
	}
	if m.owner != s || m.gen != s.gen {
		return nil
	}
	s.pending = false
	s.res = m.res
	s.rebuild()
	return nil
}

// rebuild lays out the rows for the current results and resets the cursor.
func (s *SearchOverlay) rebuild() {
	s.rows = s.rows[:0]
	if s.scope == scopeCatalog {
		s.rebuildCatalog()
	} else {
		s.rebuildLibrary()
	}
	s.cur.First(len(s.rows), s.ok)
}

func (s *SearchOverlay) addRecent() {
	if s.query() == "" && len(searchHistory) > 0 {
		s.rows = append(s.rows, searchRow{kind: srHeading, text: "Recent"})
		for _, q := range searchHistory {
			s.rows = append(s.rows, searchRow{kind: srRecent, text: q})
		}
	}
}

// rebuildCatalog lays out the Apple Music tab: artists, albums, songs.
// While a slow search runs, nothing (the list says it is searching).
func (s *SearchOverlay) rebuildCatalog() {
	add := func(r searchRow) { s.rows = append(s.rows, r) }
	switch {
	case s.query() == "":
		s.addRecent()
		return
	case s.catSlow:
		return
	}
	r := &s.catRes
	for _, g := range []struct {
		kind searchRowKind
		name string
		n    int
	}{
		{srCatArtist, "Artists", len(r.Artists)},
		{srCatAlbum, "Albums", len(r.Albums)},
		{srCatSong, "Songs", len(r.Songs)},
	} {
		if g.n == 0 {
			continue
		}
		if len(s.rows) > 0 {
			add(searchRow{kind: srSpacer})
		}
		add(searchRow{kind: srHeading, text: g.name})
		for i := range g.n {
			add(searchRow{kind: g.kind, i: i})
		}
	}
}

func (s *SearchOverlay) rebuildLibrary() {
	add := func(r searchRow) { s.rows = append(s.rows, r) }
	r := &s.res
	switch {
	case s.query() == "" || r.Query == "":
		s.addRecent()
	case r.Scope != search.All:
		kind := map[search.Scope]searchRowKind{search.Songs: srSong, search.Albums: srAlbum, search.Artists: srArtist, search.Playlists: srPlaylist}[r.Scope]
		for i := range searchResultLen(r, kind) {
			add(searchRow{kind: kind, i: i})
		}
	default:
		topOf := map[searchRowKind]int{}
		if len(r.Top) > 0 {
			add(searchRow{kind: srHeading, text: "Top results"})
			for _, t := range r.Top {
				k := srSong + searchRowKind(t.Kind)
				topOf[k] = t.Index
				add(searchRow{kind: k, i: t.Index, top: true})
			}
		}
		for _, g := range []struct {
			kind  searchRowKind
			name  string
			scope search.Scope
		}{
			{srSong, "Songs", search.Songs},
			{srAlbum, "Albums", search.Albums},
			{srArtist, "Artists", search.Artists},
			{srPlaylist, "Playlists", search.Playlists},
		} {
			n := searchResultLen(r, g.kind)
			top, hasTop := topOf[g.kind]
			if n == 0 || n == 1 && hasTop {
				continue
			}
			if len(s.rows) > 0 {
				add(searchRow{kind: srSpacer})
			}
			add(searchRow{kind: srHeading, text: g.name, scope: g.scope})
			for i := range n {
				if hasTop && i == top {
					continue
				}
				add(searchRow{kind: g.kind, i: i})
			}
		}
		if s.cat != nil {
			// The library can't have everything: the last group offers
			// Apple Music, without asking it until the user picks it.
			if len(s.rows) == 0 {
				add(searchRow{kind: srNote, text: "Nothing in your library"})
			}
			add(searchRow{kind: srSpacer})
			add(searchRow{kind: srHeading, text: "Apple Music", scope: scopeCatalog})
			add(searchRow{kind: srCatPrompt})
		}
	}
}

func searchResultLen(r *search.Results, k searchRowKind) int {
	switch k {
	case srSong:
		return len(r.Songs)
	case srAlbum:
		return len(r.Albums)
	case srArtist:
		return len(r.Artists)
	case srPlaylist:
		return len(r.Playlists)
	}
	return 0
}

func (s *SearchOverlay) ok(i int) bool { return s.rows[i].selectable() }

func (s *SearchOverlay) setScope(sc search.Scope, c *Ctx) tea.Cmd {
	if sc == s.scope || sc == scopeCatalog && s.cat == nil {
		return nil
	}
	if s.scope == scopeCatalog {
		s.catGen++ // drop a pending debounce or search
		s.stopCatalog()
		s.catWait = false
	}
	s.scope = sc
	s.in.m.Placeholder = searchScopes[searchScopeIndex(sc)].placeholder
	return s.run(c)
}

func (s *SearchOverlay) cycleScope(d int, c *Ctx) tea.Cmd {
	scopes := s.scopes()
	n := len(scopes)
	i := 0
	for k, sc := range scopes {
		if sc.scope == s.scope {
			i = k
		}
	}
	return s.setScope(scopes[(i+d+n)%n].scope, c)
}

func (s *SearchOverlay) move(d int) {
	s.cur.Step(d, len(s.rows), s.ok)
	s.cur.Follow(s.listH, len(s.rows), s.ok)
}

func (s *SearchOverlay) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.InputNext, keymap.NavDown:
		s.move(n)
	case keymap.InputPrev, keymap.NavUp:
		s.move(-n)
	case keymap.InputNextScope:
		return true, s.cycleScope(1, c)
	case keymap.InputPrevScope:
		return true, s.cycleScope(-1, c)
	case keymap.InputSubmit, keymap.Open:
		return true, s.activate(s.cur.Sel, c)
	case keymap.InputCancel, keymap.Back:
		return true, c.Host.CloseOverlay()
	default:
		return false, nil
	}
	return true, nil
}

func (s *SearchOverlay) Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	switch msg.String() {
	case "pgdown":
		s.move(max(1, s.listH-1))
		return nil
	case "pgup":
		s.move(-max(1, s.listH-1))
		return nil
	}
	if s.in.Key(msg) {
		return s.run(c)
	}
	return nil
}

// songs is the context for playing a song hit: every song shown.
func (s *SearchOverlay) songs() []*library.Track {
	ts := make([]*library.Track, len(s.res.Songs))
	for i, h := range s.res.Songs {
		ts[i] = h.Track
	}
	return ts
}

func (s *SearchOverlay) activate(i int, c *Ctx) tea.Cmd {
	if i < 0 || i >= len(s.rows) || !s.rows[i].selectable() {
		return nil
	}
	row := s.rows[i]
	if row.kind == srRecent {
		s.in.SetValue(row.text)
		return s.run(c)
	}
	q := s.query()
	if row.kind == srCatPrompt {
		return s.setScope(scopeCatalog, c)
	}
	rememberSearch(q)
	res := s.res
	switch row.kind {
	case srCatSong:
		songs := s.catSongs
		if !songs[row.i].Available {
			return c.Host.Status("This song isn’t available to stream")
		}
		return ovClose(c, func(h Host) tea.Cmd {
			return h.Play(player.Source{Kind: player.KindCatalog, Title: q}, songs, row.i)
		})
	case srCatAlbum:
		al := s.catRes.Albums[row.i]
		return ovClose(c, func(h Host) tea.Cmd { return h.Open(newCatalogAlbumView(al)) })
	case srCatArtist:
		ar := s.catRes.Artists[row.i]
		return ovClose(c, func(h Host) tea.Cmd { return h.Open(newCatalogArtistView(ar)) })
	case srSong:
		songs := s.songs()
		return ovClose(c, func(h Host) tea.Cmd {
			return h.Play(player.Source{Kind: player.KindSearch, Title: q}, songs, row.i)
		})
	case srAlbum:
		return ovClose(c, func(h Host) tea.Cmd { return h.OpenAlbum(res.Albums[row.i].Album) })
	case srArtist:
		return ovClose(c, func(h Host) tea.Cmd { return h.OpenArtist(res.Artists[row.i].Artist) })
	case srPlaylist:
		return ovClose(c, func(h Host) tea.Cmd { return h.OpenPlaylist(res.Playlists[row.i].Playlist) })
	}
	return nil
}

func (s *SearchOverlay) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	row := -1
	if y := ev.Y - s.listY; y >= 0 && y < s.listH && s.cur.Off+y < len(s.rows) {
		row = s.cur.Off + y
	}
	switch ev.Kind {
	case MouseMotion:
		s.cur.Hover = -1
		if row >= 0 && s.rows[row].selectable() {
			s.cur.Hover = row
		}
	case MouseWheel:
		s.cur.Scroll(ovWheel(ev), s.listH, len(s.rows))
	case MousePress:
		for i, r := range s.tabs {
			if image.Pt(ev.X, ev.Y).In(r) {
				return s.setScope(s.scopes()[i].scope, c)
			}
		}
		if row < 0 {
			return nil
		}
		r := s.rows[row]
		switch {
		case r.kind == srHeading && r.scope != search.All:
			return s.setScope(r.scope, c)
		case !r.selectable():
			return nil
		case ev.Button == tea.MouseRight:
			s.cur.Sel = row
			return s.menu(r, ev.ScreenX, ev.ScreenY, c)
		case ev.Button == tea.MouseLeft:
			s.cur.Sel = row
			return s.activate(row, c)
		}
	}
	return nil
}

// menu opens the context menu for result r at a screen cell: the play
// items and Open in for songs, albums and artists of both kinds.
func (s *SearchOverlay) menu(r searchRow, x, y int, c *Ctx) tea.Cmd {
	switch r.kind {
	case srSong:
		return c.Host.Menu(x, y, []*library.Track{s.res.Songs[r.i].Track})
	case srCatSong:
		return c.Host.Menu(x, y, []*library.Track{s.catSongs[r.i]})
	case srAlbum:
		return c.Host.Menu(x, y, s.res.Albums[r.i].Album.Tracks)
	case srArtist:
		a := s.res.Artists[r.i].Artist
		var ts []*library.Track
		for _, al := range a.Albums {
			ts = append(ts, al.Tracks...)
		}
		if len(ts) == 0 {
			return nil
		}
		return c.Host.PushOverlay(newMenuFor(x, y, ts, Subject{artist: a}))
	case srCatAlbum:
		return catalogAlbumAction(keymap.Menu, catalogAlbum(s.catRes.Albums[r.i]), c, x, y)
	case srCatArtist:
		ar := s.catRes.Artists[r.i]
		return c.Host.PushOverlay(NewMenu(x, y, nil,
			MenuItem{Label: "Go to Artist", Run: func(h Host) tea.Cmd { return h.Open(newCatalogArtistView(ar)) }},
			MenuSeparator,
			openInItem(keymap.OpenArtistInLibrary, Subject{catArtist: ar}),
		))
	}
	return nil
}

func (s *SearchOverlay) Render(c *Ctx) string {
	t := c.Theme
	w, h := s.w, s.h
	if w == 0 {
		w, h = 80, 24
	}
	inner, ih := w-2, h-2
	if inner < 4 || ih < 1 {
		return ovFrame(t, w, h, nil)
	}
	body := []string{s.in.View(t, t.G.Search, inner)}
	header := 1
	n := s.listRows()
	switch {
	case n == 0 && ih >= 3:
		// just the input and the tabs
		body = append(body, "", s.tabRow(t, inner))
		header = 3
	case ih >= 8 || n > 0 && ih >= searchHeaderH+1:
		body = append(body, "", s.tabRow(t, inner), ovRule(t, inner))
		header = searchHeaderH
	default:
		s.tabs = nil
	}
	hints := ih >= 12 || n > 0 && ih >= header+max(n, searchMinRows)+2
	s.listY = 1 + header
	s.listH = max(0, ih-header)
	if hints {
		s.listH -= 2
	}
	s.cur.Clamp(s.listH, len(s.rows))
	body = append(body, s.list(c, inner)...)
	if hints {
		for len(body) < ih-1 {
			body = append(body, "")
		}
		body = append(body, ovHints(t, inner, s.hints()...))
	}
	return ovFrame(t, w, h, body)
}

func (s *SearchOverlay) hints() []ovHint {
	if !slices.ContainsFunc(s.rows, searchRow.selectable) {
		return []ovHint{{"tab", "scope"}, {"esc", "close"}}
	}
	verb := "open"
	if s.cur.Sel < len(s.rows) {
		switch s.rows[s.cur.Sel].kind {
		case srSong, srCatSong:
			verb = "play"
		case srRecent, srCatPrompt:
			verb = "search"
		}
	}
	return []ovHint{{"↑↓", "select"}, {"enter", verb}, {"tab", "scope"}, {"esc", "close"}}
}

func (s *SearchOverlay) tabRow(t *theme.Theme, inner int) string {
	var b strings.Builder
	s.tabs = s.tabs[:0]
	x := 2 // border + padding
	for i, sc := range s.scopes() {
		if i > 0 {
			b.WriteString("   ")
			x += 3
		}
		st := t.Dim
		if sc.scope == s.scope {
			st = t.AccentB
		}
		b.WriteString(st.Render(sc.name))
		w := Width(sc.name)
		s.tabs = append(s.tabs, image.Rect(x, 3, x+w, 4))
		x += w
	}
	return ovPad(b.String(), inner)
}

func (s *SearchOverlay) list(c *Ctx, inner int) []string {
	t := c.Theme
	lines := make([]string, 0, s.listH)
	q := s.query()
	if len(s.rows) == 0 {
		note := ""
		switch {
		case q == "" || s.listH <= 0:
		case s.scope == scopeCatalog:
			note = s.catalogNote(q)
		case !s.pending && s.res.Query != "":
			note = "No results for “" + q + "”"
		}
		if note != "" {
			for range (s.listH - 1) / 2 {
				lines = append(lines, "")
			}
			lines = append(lines, ovCenterLine(t, note, inner))
		}
		return lines
	}
	// the title column fits the titles in view
	pw := 0
	for _, r := range s.rows[s.cur.Off:min(len(s.rows), s.cur.Off+s.listH)] {
		pw = max(pw, Width(s.primaryText(r)))
	}
	cols := searchCols(inner-3, pw)
	for y := 0; y < s.listH && s.cur.Off+y < len(s.rows); y++ {
		i := s.cur.Off + y
		r := s.rows[i]
		bar := ovBar(t, y, s.listH, s.cur.Off, len(s.rows))
		switch r.kind {
		case srSpacer:
			lines = append(lines, ovPad("", inner-1)+bar)
		case srHeading, srNote:
			lines = append(lines, ovPad(t.Dim.Render(r.text), inner-1)+bar)
		default:
			lines = append(lines, ovRow(t, s.cells(c, r, cols), inner-1, i == s.cur.Sel, i == s.cur.Hover)+bar)
		}
	}
	return lines
}

// catalogNote is the Apple Music tab's line when it lists nothing.
func (s *SearchOverlay) catalogNote(q string) string {
	switch {
	case s.catSlow:
		return "Searching Apple Music…"
	case s.catErr != nil:
		return catalogErrText(s.catErr)
	case s.catQuery != "" && !s.catWait:
		return "No results on Apple Music for “" + s.catQuery + "”"
	}
	return ""
}

// searchCols splits the content width into primary, secondary and time
// columns with two-cell gutters. The title is what tells rows apart, so it
// may take up to 58% of the width, but no more than the longest title
// needs: artist · album gets the rest.
func searchCols(w, primaryW int) [3]int {
	const timeW = 7
	rest := max(0, w-timeW-4)
	p := min(rest, max(rest/2, w*58/100), max(primaryW, 12))
	return [3]int{p, rest - p, timeW}
}

// primaryText is the plain text of r's first column.
func (s *SearchOverlay) primaryText(r searchRow) string {
	switch r.kind {
	case srRecent:
		return r.text
	case srSong:
		return s.res.Songs[r.i].Track.Title
	case srAlbum:
		return s.res.Albums[r.i].Album.Title
	case srArtist:
		return s.res.Artists[r.i].Artist.Name
	case srPlaylist:
		return s.res.Playlists[r.i].Playlist.Name
	case srCatPrompt:
		return "Search Apple Music for “" + s.query() + "”"
	case srCatSong:
		return s.catRes.Songs[r.i].Name
	case srCatAlbum:
		return s.catRes.Albums[r.i].Name
	case srCatArtist:
		return s.catRes.Artists[r.i].Name
	}
	return ""
}

// searchAlbumMin is the fewest cells an album name gets after the artist;
// with less room the album is left out rather than cut to "· …".
const searchAlbumMin = 8

func (s *SearchOverlay) cells(c *Ctx, r searchRow, cols [3]int) string {
	t := c.Theme
	// Matches are bold, not accent: the accent is kept for what's playing
	// and the selection.
	match := t.Bold
	sep := " " + t.G.Dot + " "
	kindLabel := func(label string) string {
		if r.top {
			return label + sep
		}
		return ""
	}
	var primary, secondary, right string
	switch r.kind {
	case srRecent:
		primary = t.Text.Render(r.text)
	case srSong:
		h := s.res.Songs[r.i]
		tr := h.Track
		primary = ovMark(tr.Title, h.TitleMatches, t.Text, match)
		secondary = t.Dim.Render(kindLabel("Song")) + ovMark(tr.Artist, h.ArtistMatches, t.Dim, match)
		if !r.top && tr.AlbumName != "" && cols[1]-Width(secondary)-Width(sep) >= min(searchAlbumMin, Width(tr.AlbumName)) {
			secondary += t.Dim.Render(sep) + ovMark(tr.AlbumName, h.AlbumMatches, t.Dim, match)
		}
		right = t.Dim.Render(clock(tr.Duration))
		if tr.PID == c.PlayingTrackPID() {
			right = t.Accent.Render(t.G.Playing) + " " + right
		}
	case srAlbum:
		h := s.res.Albums[r.i]
		a := h.Album
		primary = ovMark(a.Title, h.TitleMatches, t.Text, match)
		secondary = t.Dim.Render(kindLabel("Album")) + ovMark(a.Artist, h.ArtistMatches, t.Dim, match)
		if y := strconv.Itoa(a.Year); a.Year > 0 && Width(secondary)+Width(sep+y) <= cols[1] {
			secondary += t.Dim.Render(sep + y) // whole or not at all
		}
	case srArtist:
		h := s.res.Artists[r.i]
		a := h.Artist
		primary = ovMark(a.Name, h.NameMatches, t.Text, match)
		secondary = t.Dim.Render(kindLabel("Artist") + plural(len(a.Albums), "album", "albums"))
	case srPlaylist:
		h := s.res.Playlists[r.i]
		p := h.Playlist
		primary = ovMark(p.Name, h.NameMatches, t.Text, match)
		count := plural(len(p.Tracks), "song", "songs")
		if p.Kind == "folder" {
			count = plural(len(p.Children), "playlist", "playlists")
		}
		secondary = t.Dim.Render(kindLabel("Playlist") + count)
	case srCatPrompt:
		primary = t.Text.Render(s.primaryText(r))
	case srCatSong:
		song, tr := s.catRes.Songs[r.i], s.catSongs[r.i]
		hl := search.Highlight(s.catQuery, song.Name, song.Artist, song.Album)
		main, sub := t.Text, t.Dim
		if !tr.Available {
			main = t.Dim
		}
		primary = ovMark(song.Name, hl[0], main, match)
		secondary = ovMark(song.Artist, hl[1], sub, match)
		if song.Album != "" && cols[1]-Width(secondary)-Width(sep) >= min(searchAlbumMin, Width(song.Album)) {
			secondary += t.Dim.Render(sep) + ovMark(song.Album, hl[2], sub, match)
		}
		right = t.Dim.Render(clock(song.Duration))
		if tr.PID == c.PlayingTrackPID() {
			right = t.Accent.Render(t.G.Playing) + " " + right
		} else if c.CatalogSongInLibrary(song.ID, song.Name, song.Artist) {
			right = searchMark(t, right, cols[2])
		}
	case srCatAlbum:
		a := s.catRes.Albums[r.i]
		hl := search.Highlight(s.catQuery, a.Name, a.Artist)
		primary = ovMark(a.Name, hl[0], t.Text, match)
		secondary = ovMark(a.Artist, hl[1], t.Dim, match)
		if y := strconv.Itoa(a.Year()); a.Year() > 0 && Width(secondary)+Width(sep+y) <= cols[1] {
			secondary += t.Dim.Render(sep + y)
		}
		if c.CatalogAlbumInLibrary(a.Name, a.Artist) {
			right = searchMark(t, "", cols[2])
		}
	case srCatArtist:
		a := s.catRes.Artists[r.i]
		hl := search.Highlight(s.catQuery, a.Name)
		primary = ovMark(a.Name, hl[0], t.Text, match)
		secondary = t.Dim.Render(a.Genre)
	}
	return Row(2, Cell{Text: primary, W: cols[0]}, Cell{Text: secondary, W: cols[1]}, Cell{Text: right, W: cols[2], Right: true})
}

// searchMark is the last column of an Apple Music row the library has: the
// check at the column's left edge, in the same place on every row, and rest
// (the time, already styled) right-aligned after it. A rest that leaves no
// room for the check stays alone.
func searchMark(t *theme.Theme, rest string, w int) string {
	gap := w - 1 - Width(rest)
	if gap < 1 {
		return rest
	}
	return t.Dim.Render(t.G.InLib) + strings.Repeat(" ", gap) + rest
}

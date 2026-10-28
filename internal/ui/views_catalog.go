package ui

import (
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Apple Music pages: an album or an artist from the catalog, laid out like
// the library's pages. They load in the background when opened (Init) and
// say so only after catalogQuiet.

// catalogTopSongs is how many top songs a catalog artist page lists.
const catalogTopSongs = 10

// catalogArtistSongs is how many of an artist's songs a catalog artist
// page looks up: past the top songs, they show the compilations and other
// artists' albums the artist is on.
const catalogArtistSongs = 200

var errNoCatalog = errors.New("catalog: off")

// loadState is where a page's catalog request is.
type loadState uint8

const (
	loadIdle loadState = iota
	loadRunning
	loadDone
	loadFailed
)

// catalogLoad tracks one request of a page.
type catalogLoad struct {
	state loadState
	since time.Time
	err   error
	gen   int
}

// start begins a request unless one ran or is running; it reports the
// generation to tag the result with, or ok=false.
func (l *catalogLoad) start(now time.Time) (gen int, ok bool) {
	if l.state == loadRunning || l.state == loadDone {
		return 0, false
	}
	l.gen++
	l.state, l.since, l.err = loadRunning, now, nil
	return l.gen, true
}

// finish records a result for generation gen; false means it is stale.
func (l *catalogLoad) finish(gen int, err error) bool {
	if gen != l.gen || l.state != loadRunning {
		return false
	}
	l.state, l.err = loadDone, err
	if err != nil {
		l.state = loadFailed
	}
	return true
}

// note is the one dim line for the request: "Loading…" once it has run
// catalogQuiet, the error when it failed, else "".
func (l *catalogLoad) note(now time.Time) string {
	switch l.state {
	case loadRunning:
		if now.Sub(l.since) >= catalogQuiet {
			return "Loading…"
		}
	case loadFailed:
		return catalogErrText(l.err)
	}
	return ""
}

// catalogCmd runs fn against the catalog off the UI goroutine and applies
// its result back on it.
func catalogCmd(cat Catalog, fn func(ctx context.Context, cat Catalog) func(c *Ctx) tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
		defer cancel()
		return catalogResultMsg{apply: fn(ctx, cat)}
	}
}

// withCatalogAlbum loads album id's songs and calls do with their
// stand-ins on the UI goroutine. A failure shows in the footer.
func withCatalogAlbum(cat Catalog, id int64, do func(c *Ctx, al catalog.Album, ts []*library.Track) tea.Cmd) tea.Cmd {
	if cat == nil {
		return func() tea.Msg { return StatusMsg{Text: catalogOfflineM} }
	}
	return catalogCmd(cat, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
		al, songs, err := cat.AlbumTracks(ctx, id)
		return func(c *Ctx) tea.Cmd {
			if err != nil {
				return c.Host.Status(catalogErrText(err))
			}
			return do(c, al, catalogTracks(songs))
		}
	})
}

// catalogAlbumAction does act for a catalog album shown in a grid, whose
// songs aren't loaded: open it, play it (PlayerPlayPause, from the play
// badge), queue it, or open its menu at the screen cell (x, y).
func catalogAlbumAction(act keymap.Action, al *library.Album, c *Ctx, x, y int) tea.Cmd {
	id, ok := catalogAlbumID(al.Key)
	if !ok {
		return nil
	}
	cat := c.Catalog
	src := player.Source{Kind: player.KindCatalog, ID: al.Key, Title: al.Title}
	play := func(c *Ctx, _ catalog.Album, ts []*library.Track) tea.Cmd { return c.Host.Play(src, ts, 0) }
	next := func(c *Ctx, _ catalog.Album, ts []*library.Track) tea.Cmd { return c.Host.PlayNext(ts) }
	add := func(c *Ctx, _ catalog.Album, ts []*library.Track) tea.Cmd { return c.Host.AddToQueue(ts) }
	shuffle := func(c *Ctx, _ catalog.Album, ts []*library.Track) tea.Cmd {
		if len(ts) == 0 {
			return nil
		}
		return shufflePlay(c, src, ts, rand.IntN(len(ts)))
	}
	switch act {
	case keymap.Open:
		return c.Host.OpenAlbum(al)
	case keymap.PlayerPlayPause, keymap.PagePlay:
		return withCatalogAlbum(cat, id, play)
	case keymap.PageShuffle:
		return withCatalogAlbum(cat, id, shuffle)
	case keymap.QueuePlayNext:
		return withCatalogAlbum(cat, id, next)
	case keymap.QueueAdd:
		return withCatalogAlbum(cat, id, add)
	case keymap.Menu:
		items := []MenuItem{
			{Label: "Play", Key: keymap.Open, Run: func(Host) tea.Cmd { return withCatalogAlbum(cat, id, play) }},
			{Label: keymap.Name(keymap.QueuePlayNext), Key: keymap.QueuePlayNext, Run: func(Host) tea.Cmd { return withCatalogAlbum(cat, id, next) }},
			{Label: keymap.Name(keymap.QueueAdd), Key: keymap.QueueAdd, Run: func(Host) tea.Cmd { return withCatalogAlbum(cat, id, add) }},
			MenuSeparator,
			{Label: "Go to Album", Run: func(h Host) tea.Cmd { return h.OpenAlbum(al) }},
		}
		items = menuGroup(items, openInItems(true, Subject{album: al})...)
		if p, ok := musicPageOf(Subject{album: al}); ok {
			items = menuGroup(items, musicMenuItem(p))
		}
		return c.Host.PushOverlay(NewMenu(x, y, nil, items...))
	}
	return nil
}

// noteState is one dim line centred in a w×h block, a third of the way
// down, like an empty state.
func noteState(c *Ctx, msg string, w, h int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	lines := make([]string, h)
	if msg != "" {
		lines[(h-1)/3] = Center(paintsFor(c.Theme).paint(toneDim, surfNone, msg), w)
	}
	return Lines(lines, w, h)
}

// appleMusicTail is a catalog page's last header line: "Apple Music", and
// the way to the same album or artist in the library when it is there,
// which act also takes.
func appleMusicTail(c *Ctx, act keymap.Action, open func() tea.Cmd) []headerLine {
	tail := []headerLine{{text: "Apple Music", tone: toneDim}}
	if open != nil {
		tail = append(tail, headerLine{text: "In Your Library", tone: toneDim, link: open, hint: c.HintFor(keymap.Name(act), act)})
	}
	return tail
}

// CatalogAlbumView is an Apple Music album: the album page's header and
// track list, for songs that need not be in the library. They play as a
// catalog context; there is no favorite column.
type CatalogAlbumView struct {
	*AlbumView
	seed     catalog.Album // what the link that opened it knew
	info     catalog.Album // as loaded
	load     catalogLoad
	lib      *library.Library // the library libAlbum was found in
	libAlbum *library.Album   // the same album in the library, or nil
}

// newCatalogAlbumView opens catalog album seed.ID; the rest of seed fills
// the header until the album loads.
func newCatalogAlbumView(seed catalog.Album) *CatalogAlbumView {
	v := &CatalogAlbumView{seed: seed, info: seed}
	v.AlbumView = NewAlbumView(v.pageAlbum(nil))
	v.noHeart, v.libMark, v.kind = true, true, player.KindCatalog
	return v
}

// pageAlbum is the album the track list shows: the catalog album with
// stand-ins for its songs.
func (v *CatalogAlbumView) pageAlbum(ts []*library.Track) *library.Album {
	a := catalogAlbum(v.info)
	if a.ArtPID == "" {
		a.ArtPID = catalog.ResizeArt(v.seed.Artwork, catalogArtPx)
	}
	a.Tracks = ts
	for _, t := range ts {
		a.Duration += t.Duration
		t.Album = a
	}
	return a
}

func (v *CatalogAlbumView) Key() string {
	return "catalog-album:" + strconv.FormatInt(v.seed.ID, 10)
}

func (v *CatalogAlbumView) Title(c *Ctx) (string, string) { return v.info.Name, v.info.Artist }

// Init loads the album's songs, the first time and after a failure.
func (v *CatalogAlbumView) Init(c *Ctx) tea.Cmd {
	v.match(c.Lib)
	gen, ok := v.load.start(c.Now)
	if !ok {
		return nil
	}
	if c.Catalog == nil {
		v.load.finish(gen, errNoCatalog)
		return nil
	}
	id := v.seed.ID
	return tea.Batch(catalogQuietTick(), catalogCmd(c.Catalog, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
		al, songs, err := cat.AlbumTracks(ctx, id)
		return func(c *Ctx) tea.Cmd {
			if !v.load.finish(gen, err) || err != nil {
				return nil
			}
			v.info = al
			v.bind(v.pageAlbum(catalogTracks(songs)))
			v.placed = false // put the cursor on the playing song
			v.lib = nil
			v.match(c.Lib)
			return nil
		}
	}))
}

// match finds the album in lib, once per library.
func (v *CatalogAlbumView) match(lib *library.Library) {
	if lib == v.lib {
		return
	}
	v.lib = lib
	v.libAlbum = libraryAlbumFor(lib, v.info.Name, v.info.Artist)
	if v.libAlbum == nil {
		v.libAlbum = libraryAlbumOfSongs(lib, v.album.Tracks)
	}
}

// libraryAlbumOfSongs is the library album of the first of the catalog
// songs ts the library has by id, for an album whose title the library
// spells differently.
func libraryAlbumOfSongs(lib *library.Library, ts []*library.Track) *library.Album {
	for _, t := range ts {
		id, _ := strconv.ParseInt(strings.TrimPrefix(t.PID, "store:"), 10, 64)
		if l := lib.SongByStoreID(id); l != nil && l.Album != nil {
			return l.Album
		}
	}
	return nil
}

func (v *CatalogAlbumView) Reindex(lib *library.Library) View {
	v.lib = nil
	v.match(lib)
	return v
}

func (v *CatalogAlbumView) Render(w, h int, c *Ctx) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	v.w, v.h = w, h
	lw := v.listW()
	lines := v.header(lw, h, c)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	v.listY = len(lines)
	v.listH = max(h-v.listY, 0)
	note := v.load.note(c.Now)
	if v.load.state == loadDone && len(v.album.Tracks) == 0 {
		note = "No songs"
	}
	if v.load.state != loadDone || note != "" {
		v.listH = 0
		if v.listY >= h {
			return Lines(lines, w, h)
		}
		return Lines(lines, w, v.listY) + "\n" + Box(noteState(c, note, lw, h-v.listY), w, h-v.listY)
	}
	if !v.placed {
		v.placed = true
		v.showPlaying(c)
	}
	v.clampTop()
	lines = append(lines, v.trackLines(lw, c)...)
	return Lines(lines, w, h)
}

// header is the album page's header with what Apple says about it: the
// artist (a link to their Apple Music page), genre and year, the songs,
// and where it comes from.
func (v *CatalogAlbumView) header(w, h int, c *Ctx) []string {
	a, info := v.album, v.info
	v.zones = v.zones[:0]
	hd := pageHeader{
		art: a.Key, artPID: a.ArtPID,
		title: info.Name,
		sub:   headerLine{text: info.Artist, tone: toneText},
		meta:  []string{joinMeta(info.Genre, yearText(info.Year()))},
	}
	if info.ArtistID > 0 {
		artist := catalog.Artist{ID: info.ArtistID, Name: info.Artist}
		hd.sub.hint = "Go to Artist"
		hd.sub.link = func() tea.Cmd { return c.Host.Open(newCatalogArtistView(artist)) }
	}
	if v.load.state == loadDone {
		hd.meta = append(hd.meta, songsMeta(len(a.Tracks), a.Duration))
	}
	var open func() tea.Cmd
	if lib := v.libAlbum; lib != nil {
		open = func() tea.Cmd { return c.Host.OpenAlbum(lib) }
	}
	hd.tail = appleMusicTail(c, keymap.OpenAlbumInLibrary, open)
	if p, ok := albumPage(strconv.FormatInt(info.ID, 10), info.Name); ok {
		hd.tail = append(hd.tail, musicTail(c, p)...)
	}
	if v.playable() {
		hd.buttons = pageButtons(c, "Album", func() tea.Cmd { return v.play(0, c) }, func() tea.Cmd { return v.shuffle(c) })
	}
	return hd.render(w, h, c, v.hoverZ, &v.zones)
}

// playable reports whether some song on the album streams here.
func (v *CatalogAlbumView) playable() bool {
	for _, t := range v.album.Tracks {
		if t.Available {
			return true
		}
	}
	return false
}

// Action is the album page's, with the header's Play while nothing is
// listed yet doing nothing.
func (v *CatalogAlbumView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	if v.load.state != loadDone {
		return false, nil
	}
	return v.AlbumView.Action(a, n, c)
}

// subject is the selected song, or the album while it loads.
func (v *CatalogAlbumView) subject(c *Ctx) Subject {
	if t := v.selectedTrack(); t != nil && v.load.state == loadDone {
		return Subject{track: t}
	}
	return Subject{album: catalogAlbum(v.info)}
}

func (v *CatalogAlbumView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	return v.AlbumView.Mouse(ev, c)
}

// CatalogArtistView is an Apple Music artist: their newest album's art,
// name, genre and album count, Play / Shuffle for the top songs, the top
// songs and their Essentials playlist, and the albums newest first in
// sections: Albums, Singles & EPs, Live Albums, Compilations, Appears On
// and Essential Albums.
type CatalogArtistView struct {
	seed      catalog.Artist
	info      catalog.Artist
	page      artistPage
	own       []catalog.Album // the album lookup: their own releases, newest first
	albums    []*library.Album
	songs     []catalog.Song // the song lookup, most popular first
	web       catalog.ArtistPage
	albumLoad catalogLoad
	songLoad  catalogLoad
	webLoad   catalogLoad
	ess       catalogLoad // the Essentials playlist's songs
	essSongs  []*library.Track
	lib       *library.Library
	libArtist *library.Artist
}

// essentialsLabel names the song section of an artist's Essentials
// playlist.
const essentialsLabel = "Essentials"

// newCatalogArtistView opens catalog artist seed.ID.
func newCatalogArtistView(seed catalog.Artist) *CatalogArtistView {
	v := &CatalogArtistView{seed: seed, info: seed}
	v.page = newArtistPage(nil)
	v.page.cat, v.page.topN = true, catalogTopSongs
	v.page.head = v.header
	v.page.empty = func(c *Ctx) string {
		if n := v.albumLoad.note(c.Now); n != "" {
			return n
		}
		if v.albumLoad.state == loadDone {
			return "No albums"
		}
		return ""
	}
	v.rebind()
	return v
}

// rebind lays the page out again from what has loaded so far.
func (v *CatalogArtistView) rebind() {
	hadSongs := len(v.page.songs) > 0
	v.page.bindCatalog(v.info.Name, v.sections(), v.songSections())
	if !hadSongs && len(v.page.songs) > 0 {
		v.page.inGrid = false // the songs came in after the albums
	}
}

// sections sorts the albums into the page's sections. The album lookup
// has every release of their own but calls them all albums; Apple's page
// says which are live albums and compilations and adds Appears On and
// Essential Albums; the song lookup finds the soundtracks and Various
// Artists compilations they are on, which neither lists.
func (v *CatalogArtistView) sections() []pageSection {
	byID := map[int64]*library.Album{}
	for i, a := range v.own {
		byID[a.ID] = v.albums[i]
	}
	in := func(kind string) map[int64]bool {
		m := map[int64]bool{}
		for _, a := range v.web.Sections[kind] {
			m[a.ID] = true
		}
		return m
	}
	live, comps, singles := in(catalog.SectionLive), in(catalog.SectionCompilations), in(catalog.SectionSingles)
	others := in(catalog.SectionAppearsOn)
	var albums, single, liveAl, compAl, appears, essential []*library.Album
	for i, a := range v.own {
		al := v.albums[i]
		switch {
		case others[a.ID] || a.ArtistID > 0 && a.ArtistID != v.seed.ID:
			// The lookup lists albums led by others they are on.
			appears = append(appears, al)
		case live[a.ID]:
			liveAl = append(liveAl, al)
		case comps[a.ID]:
			compAl = append(compAl, al)
		case singles[a.ID] || isSingle(a.Name):
			single = append(single, al)
		default:
			albums = append(albums, al)
		}
	}
	// What only Apple's page or the songs know.
	listed := func(a catalog.Album) *library.Album {
		if al := byID[a.ID]; al != nil {
			return al
		}
		al := catalogAlbum(a)
		byID[a.ID] = al
		return al
	}
	extra := func(kind string, to *[]*library.Album) {
		for _, a := range v.web.Sections[kind] {
			if _, own := byID[a.ID]; !own || kind == catalog.SectionEssentials {
				*to = append(*to, listed(a))
			}
		}
	}
	extra(catalog.SectionLive, &liveAl)
	extra(catalog.SectionCompilations, &compAl)
	extra(catalog.SectionAppearsOn, &appears)
	extra(catalog.SectionEssentials, &essential)
	skip := map[int64]bool{}
	for id := range byID {
		skip[id] = true
	}
	vaComps, theirs := catalog.Appearances(v.songs, skip)
	for _, a := range vaComps {
		compAl = append(compAl, listed(a))
	}
	for _, a := range theirs {
		appears = append(appears, listed(a))
	}
	return albumSections(secAlbums.withAlbums(albums), secSingles.withAlbums(single), secLive.withAlbums(liveAl),
		secCompilations.withAlbums(compAl), secAppearsOn.withAlbums(appears), secEssentials.withAlbums(essential))
}

// songSections are Top Songs (Apple's, else the song lookup's order) and
// the Essentials playlist when the artist has one, loaded when chosen.
func (v *CatalogArtistView) songSections() []pageSection {
	var secs []pageSection
	top := v.web.TopSongs
	if len(top) == 0 {
		top = v.songs
	}
	if len(top) > 0 {
		secs = append(secs, pageSection{label: "Top Songs", short: "Top Songs", songs: catalogTracks(top)})
	}
	if pl := v.web.Essentials; pl != nil {
		s := pageSection{label: essentialsLabel, short: essentialsLabel, songs: v.essSongs}
		switch v.ess.state {
		case loadIdle:
			id := pl.ID
			s.load = func(c *Ctx) tea.Cmd { return v.loadEssentials(c, id) }
		case loadFailed:
			s.note = catalogErrText(v.ess.err)
		case loadDone:
			if len(v.essSongs) == 0 {
				s.note = "No songs"
			}
		}
		secs = append(secs, s)
	}
	return secs
}

// loadEssentials loads the songs of the Essentials playlist id.
func (v *CatalogArtistView) loadEssentials(c *Ctx, id string) tea.Cmd {
	gen, ok := v.ess.start(c.Now)
	if !ok || c.Catalog == nil {
		return nil
	}
	return catalogCmd(c.Catalog, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
		songs, err := cat.PlaylistSongs(ctx, id)
		return func(c *Ctx) tea.Cmd {
			if !v.ess.finish(gen, err) {
				return nil
			}
			v.essSongs = catalogTracks(songs)
			note := ""
			switch {
			case err != nil:
				note = catalogErrText(err)
			case len(songs) == 0:
				note = "No songs"
			}
			v.page.fillSongs(essentialsLabel, v.essSongs, note)
			return nil
		}
	})
}

func (v *CatalogArtistView) Key() string {
	return "catalog-artist:" + strconv.FormatInt(v.seed.ID, 10)
}

func (v *CatalogArtistView) Title(c *Ctx) (string, string) {
	return v.info.Name, v.meta()
}

func (v *CatalogArtistView) meta() string {
	n := ""
	if v.albumLoad.state == loadDone {
		n = plural(len(v.page.sectionAlbums(secAlbums.label)), "album", "albums")
	}
	return joinMeta(v.info.Genre, n)
}

// Init loads the albums, the songs and Apple's page, the first time and
// after a failure.
func (v *CatalogArtistView) Init(c *Ctx) tea.Cmd {
	v.match(c.Lib)
	var cmds []tea.Cmd
	id := v.seed.ID
	if gen, ok := v.albumLoad.start(c.Now); ok {
		if c.Catalog == nil {
			v.albumLoad.finish(gen, errNoCatalog)
		} else {
			cmds = append(cmds, catalogCmd(c.Catalog, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
				ar, albums, err := cat.ArtistAlbums(ctx, id)
				return func(c *Ctx) tea.Cmd {
					if !v.albumLoad.finish(gen, err) || err != nil {
						return nil
					}
					if ar.Name != "" {
						v.info = ar
					}
					v.own = albums
					v.albums = v.albums[:0]
					for _, a := range albums {
						v.albums = append(v.albums, catalogAlbum(a))
					}
					v.rebind()
					v.lib = nil
					v.match(c.Lib)
					return nil
				}
			}))
		}
	}
	if gen, ok := v.songLoad.start(c.Now); ok {
		if c.Catalog == nil {
			v.songLoad.finish(gen, errNoCatalog)
		} else {
			cmds = append(cmds, catalogCmd(c.Catalog, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
				songs, err := cat.ArtistTopSongs(ctx, id, catalogArtistSongs)
				return func(c *Ctx) tea.Cmd {
					if !v.songLoad.finish(gen, err) || err != nil {
						return nil
					}
					v.songs = songs
					v.rebind()
					return nil
				}
			}))
		}
	}
	if gen, ok := v.webLoad.start(c.Now); ok && c.Catalog != nil {
		cmds = append(cmds, catalogCmd(c.Catalog, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
			page, err := cat.ArtistPage(ctx, id)
			return func(c *Ctx) tea.Cmd {
				// Without Apple's page the lookups still make a page.
				if !v.webLoad.finish(gen, err) || err != nil {
					return nil
				}
				v.web = page
				v.rebind()
				return nil
			}
		}))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(append(cmds, catalogQuietTick())...)
}

// match finds the artist in lib, once per library.
func (v *CatalogArtistView) match(lib *library.Library) {
	if lib == v.lib {
		return
	}
	v.lib = lib
	v.libArtist = libraryArtistFor(lib, v.info.Name)
}

func (v *CatalogArtistView) Reindex(lib *library.Library) View {
	v.match(lib)
	return v
}

func (v *CatalogArtistView) header(w, h int, c *Ctx) pageHeader {
	hd := pageHeader{title: v.info.Name}
	if m := v.meta(); m != "" {
		hd.meta = []string{m}
	}
	if al := v.page.artist; al != nil && len(al.Albums) > 0 {
		hd.art, hd.artPID = al.Albums[0].Key, al.Albums[0].ArtPID // Albums first, newest first
	} else {
		hd.art = v.Key() // a calm square until the albums arrive
	}
	var open func() tea.Cmd
	if lib := v.libArtist; lib != nil {
		open = func() tea.Cmd { return c.Host.OpenArtist(lib) }
	}
	hd.tail = appleMusicTail(c, keymap.OpenArtistInLibrary, open)
	if v.playable() {
		hd.buttons = pageButtons(c, "Artist", func() tea.Cmd { return v.page.playAll(c) }, func() tea.Cmd { return v.page.shuffleAll(c) })
	}
	return hd
}

func (v *CatalogArtistView) playable() bool {
	for _, t := range v.page.allTracks() {
		if t.Available {
			return true
		}
	}
	return false
}

func (v *CatalogArtistView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	return v.page.Action(a, n, c)
}

func (v *CatalogArtistView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd { return v.page.Mouse(ev, c) }

// subject is the selected song or album, else the artist.
func (v *CatalogArtistView) subject(c *Ctx) Subject {
	if s := v.page.subject(); !s.empty() {
		return s
	}
	return Subject{catArtist: v.info}
}

func (v *CatalogArtistView) Render(w, h int, c *Ctx) string {
	return v.page.Render(w, h, c, c.Focused)
}

func (v *CatalogArtistView) Selected(c *Ctx) []*library.Track { return v.page.Selected() }

func (v *CatalogArtistView) FooterHints(c *Ctx) []FooterHint { return v.page.FooterHints(c) }

package ui

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Open Album / Open Artist in Apple Music and in Library: the album or the
// artist of a song, album or artist, on its other page. A song's album
// opens with the song selected; its artist is the song's lead artist. A
// library item's Apple Music page is found with one catalog search when
// asked for, never sooner, and the answer is kept; a catalog item's
// library page is found in memory.

const (
	notInCatalogM = "Not found in Apple Music"
	notInLibraryM = "Not in Your Library"
	lookingM      = "Looking in Apple Music…"
	nothingM      = "Select a song, album or artist to open"
	noAlbumM      = "Select a song or album to open its album"
	noArtistM     = "No artist page for this one"

	// matchLimit is how many results of one kind a match search asks for.
	matchLimit = 25
)

// Subject is what Open in acts on: one song (a library track or a catalog
// stand-in), an album (a library album or a catalog one, key
// "catalog:<id>"), a library artist or an Apple Music artist.
type Subject struct {
	track     *library.Track
	album     *library.Album
	artist    *library.Artist
	catArtist catalog.Artist
}

func (s Subject) empty() bool {
	return s.track == nil && s.album == nil && s.artist == nil && s.catArtist.ID <= 0
}

// fromCatalog reports whether s is an Apple Music item.
func (s Subject) fromCatalog() bool {
	switch {
	case s.track != nil:
		return player.IsCatalog(s.track)
	case s.album != nil:
		return isCatalogAlbum(s.album)
	}
	return s.catArtist.ID > 0
}

// subjecter is a view or pane whose selection is more than its Selected
// tracks say: a grid's album, an artist in a list.
type subjecter interface {
	subject(c *Ctx) Subject
}

// subjectOf reads a selection as a subject: one song, every song of one
// album, or every song of one artist.
func subjectOf(ts []*library.Track) Subject {
	switch {
	case len(ts) == 0:
		return Subject{}
	case len(ts) == 1:
		return Subject{track: ts[0]}
	}
	album, artist := ts[0].Album, ts[0].Group
	for _, t := range ts {
		if t.Album != album {
			album = nil
		}
		if t.Group != artist {
			artist = nil
		}
	}
	switch {
	case album != nil && len(ts) == len(album.Tracks):
		return Subject{album: album}
	case artist != nil && len(ts) == artist.Tracks:
		return Subject{artist: artist}
	}
	return Subject{}
}

// opensArtist reports whether act opens an artist page rather than an
// album's; inLibrary whether it opens the library page.
func opensArtist(act keymap.Action) bool {
	return act == keymap.OpenArtistInCatalog || act == keymap.OpenArtistInLibrary
}

func inLibrary(act keymap.Action) bool {
	return act == keymap.OpenAlbumInLibrary || act == keymap.OpenArtistInLibrary
}

// openIn opens the album or the artist of s (see opensArtist) in Apple
// Music or in the library (see inLibrary).
func openIn(h Host, act keymap.Action, cat Catalog, lib *library.Library, s Subject) tea.Cmd {
	artist := opensArtist(act)
	switch {
	case s.empty():
		return h.Status(nothingM)
	case !artist && !s.hasAlbum():
		return h.Status(noAlbumM)
	case inLibrary(act):
		return openInLibrary(h, lib, s, artist)
	case artist:
		return openArtistInCatalog(h, cat, lib, s)
	}
	return openInCatalog(h, cat, s)
}

// hasAlbum reports whether s has an album to open: a song or an album.
func (s Subject) hasAlbum() bool { return s.track != nil || s.album != nil }

// openInLibrary opens the library page of s's album (a song on it,
// selected) or of its artist.
func openInLibrary(h Host, lib *library.Library, s Subject, artist bool) tea.Cmd {
	s, ok := libraryTarget(lib, s, artist)
	switch {
	case !ok:
		return h.Status(notInLibraryM)
	case s.track != nil && s.track.Album != nil:
		v := NewAlbumView(s.track.Album)
		v.want = trackWant{pid: s.track.PID}
		return h.Open(v)
	case s.track != nil:
		return h.OpenArtist(s.track.Group)
	case s.album != nil:
		return h.OpenAlbum(s.album)
	}
	return h.OpenArtist(s.artist)
}

// libraryTarget is the library page Open in Library opens for s: the
// artist of s, or its album with s's song on it. A library item is its
// own; a catalog one is found the way "In Your Library" finds it, the
// album by title and artist, the artist by name, a song by title on that
// album or anywhere by its artist.
func libraryTarget(lib *library.Library, s Subject, artist bool) (Subject, bool) {
	if artist {
		a := libraryArtistOf(lib, s)
		return Subject{artist: a}, a != nil
	}
	if !s.fromCatalog() {
		return Subject{track: s.track, album: s.album}, s.hasAlbum()
	}
	switch {
	case lib == nil:
	case s.track != nil:
		it, _ := catalogItemOf(s.track)
		if a := libraryAlbumFor(lib, it.Album, it.Artist); a != nil {
			if t := songOn(a, it.Title); t != nil {
				return Subject{track: t}, true
			}
			return Subject{album: a}, true
		}
		if t := librarySongFor(lib, it.Title, it.Artist, ""); t != nil {
			return Subject{track: t}, true
		}
	case s.album != nil:
		if a := libraryAlbumFor(lib, s.album.Title, s.album.Artist); a != nil {
			return Subject{album: a}, true
		}
	}
	return Subject{}, false
}

// libraryArtistOf is the library artist of s: a song's lead artist, an
// album's artist, the artist itself. A library song or album nobody else
// is credited with falls back to the artist it is listed under.
func libraryArtistOf(lib *library.Library, s Subject) *library.Artist {
	switch {
	case s.artist != nil:
		return s.artist
	case s.catArtist.ID > 0:
		return libraryArtistForCredit(lib, s.catArtist.Name)
	case s.track != nil:
		if a := libraryArtistForCredit(lib, trackArtist(s.track)); a != nil || s.fromCatalog() {
			return a
		}
		return s.track.Group
	case s.album != nil:
		if a := libraryArtistForCredit(lib, s.album.Artist); a != nil || s.fromCatalog() {
			return a
		}
		if len(s.album.Tracks) > 0 {
			return s.album.Tracks[0].Group
		}
	}
	return nil
}

// trackArtist is the artist credit of t, or its album's when it has none.
func trackArtist(t *library.Track) string {
	if t.Artist == "" && t.Album != nil {
		return t.Album.Artist
	}
	return t.Artist
}

// libraryArtistForCredit finds the library artist an artist credit names:
// the whole credit, its lead artist without the featured ones, or the
// longest artist name it starts with ("Daft Punk & Pharrell Williams").
func libraryArtistForCredit(lib *library.Library, credit string) *library.Artist {
	if lib == nil || credit == "" {
		return nil
	}
	if a := libraryArtistFor(lib, credit); a != nil {
		return a
	}
	lead := leadArtist(credit)
	if a := libraryArtistFor(lib, lead); a != nil {
		return a
	}
	n := len(library.Fold(lead))
	var best *library.Artist
	for _, a := range lib.Artists {
		if f := library.Fold(a.Name); len(f) < n && sameArtist(a.Name, lead) && (best == nil || len(f) > len(library.Fold(best.Name))) {
			best = a
		}
	}
	return best
}

// featRe is where an artist credit starts on the artists it features.
var featRe = regexp.MustCompile(`(?i)\s*(?:[(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s|\s(?:feat\.?|ft\.?|featuring)\s).*$`)

// leadArtist is an artist credit without the artists it features:
// "Dua Lipa feat. DaBaby" is Dua Lipa. A band name with "&", "and" or
// "with" stays whole.
func leadArtist(credit string) string {
	if lead := strings.TrimSpace(featRe.ReplaceAllString(credit, "")); lead != "" {
		return lead
	}
	return strings.TrimSpace(credit)
}

// songOn is the song on album a with this title, notes aside.
func songOn(a *library.Album, title string) *library.Track {
	key := songMatchKey(title)
	if key == "" {
		return nil
	}
	for _, t := range a.Tracks {
		if songMatchKey(t.Title) == key {
			return t
		}
	}
	return nil
}

// librarySongFor finds the library song with this title by this artist,
// on the album of that name when the library has it.
func librarySongFor(lib *library.Library, title, artist, album string) *library.Track {
	key := songMatchKey(title)
	if key == "" {
		return nil
	}
	if a := libraryAlbumFor(lib, album, artist); a != nil {
		if t := songOn(a, title); t != nil {
			return t
		}
	}
	for _, t := range lib.Songs {
		// the key is the folded title without its notes: a prefix
		if strings.HasPrefix(t.FoldTitle, key) && songMatchKey(t.Title) == key && sameArtist(t.Artist, artist) {
			return t
		}
	}
	return nil
}

// catalogMatch is what a library item turned out to be on Apple Music:
// an album page (with song selected on it) or an artist page.
type catalogMatch struct {
	found  bool
	album  catalog.Album
	artist catalog.Artist
	song   int64
}

// The matches found so far, by matchKey, and the lookups running. Only
// the UI goroutine touches them.
var (
	catalogMatches  = map[string]catalogMatch{}
	catalogMatching = map[string]bool{}
)

func matchKey(s Subject) string {
	switch {
	case s.track != nil:
		return "song:" + s.track.PID
	case s.album != nil:
		return "album:" + s.album.Key
	case s.artist != nil:
		return "artist:" + s.artist.Name
	}
	return ""
}

// openInCatalog opens s's Apple Music page: a song on its album's page.
// For a library item that means one search the first time; later the
// page opens at once.
func openInCatalog(h Host, cat Catalog, s Subject) tea.Cmd {
	switch {
	case s.empty():
		return h.Status(nothingM)
	case s.fromCatalog():
		return openCatalogPage(h, s)
	case cat == nil:
		return h.Status(catalogOfflineM)
	}
	key := matchKey(s)
	if m, ok := catalogMatches[key]; ok {
		return m.open(h, s)
	}
	if t := s.track; t != nil && t.Album != nil {
		// the album was looked up already: the song is on it
		if m, ok := catalogMatches[matchKey(Subject{album: t.Album})]; ok && m.found {
			return m.open(h, s)
		}
	}
	if catalogMatching[key] {
		return h.Status(lookingM)
	}
	catalogMatching[key] = true
	return tea.Batch(h.Status(lookingM), catalogCmd(cat, func(ctx context.Context, cat Catalog) func(*Ctx) tea.Cmd {
		m, err := findInCatalog(ctx, cat, s)
		return func(c *Ctx) tea.Cmd {
			delete(catalogMatching, key)
			if err != nil {
				return c.Host.Status(catalogErrText(err))
			}
			remember(catalogMatches, key, m)
			done := c.Host.Status("") // the page says it was found
			return tea.Batch(done, m.open(c.Host, s))
		}
	}))
}

// openArtistInCatalog opens the Apple Music page of s's artist: a song's
// lead artist, an album's artist. A catalog item knows its artist; a
// library song or album whose match was found already does too. Else the
// artist is looked up by name, one search the first time.
func openArtistInCatalog(h Host, cat Catalog, lib *library.Library, s Subject) tea.Cmd {
	if s.fromCatalog() {
		ar, ok := catalogArtistOf(s)
		if !ok {
			return h.Status(noArtistM)
		}
		return h.Open(newCatalogArtistView(ar))
	}
	if ar, ok := knownCatalogArtist(s); ok {
		return h.Open(newCatalogArtistView(ar))
	}
	if s.artist == nil {
		var credit string
		if s.track != nil {
			credit = trackArtist(s.track)
		} else {
			credit = s.album.Artist
		}
		a := libraryArtistForCredit(lib, credit)
		if a == nil {
			a = &library.Artist{Name: leadArtist(credit)}
		}
		s = Subject{artist: a}
	}
	return openInCatalog(h, cat, s)
}

// catalogArtistOf is the Apple Music artist of catalog subject s, from
// the ids its song or album carries.
func catalogArtistOf(s Subject) (catalog.Artist, bool) {
	switch {
	case s.track != nil:
		it, _ := catalogItemOf(s.track)
		id, err := strconv.ParseInt(it.ArtistID, 10, 64)
		if err != nil || id <= 0 {
			return catalog.Artist{}, false
		}
		return artistSeed(id, it.Artist), true
	case s.album != nil:
		id, _ := catalogAlbumID(s.album.Key)
		if al := catalogAlbums[id]; al.ArtistID > 0 {
			return artistSeed(al.ArtistID, al.Artist), true
		}
		return catalog.Artist{}, false
	}
	return s.catArtist, s.catArtist.ID > 0
}

// knownCatalogArtist is the Apple Music artist of library song or album
// s when the match found for it says: a song's own, or its album's when
// the album is by the same artist.
func knownCatalogArtist(s Subject) (catalog.Artist, bool) {
	var ms []catalogMatch
	switch t := s.track; {
	case t != nil:
		ms = append(ms, catalogMatches[matchKey(s)])
		if t.Album != nil && sameArtist(t.Album.Artist, trackArtist(t)) {
			ms = append(ms, catalogMatches[matchKey(Subject{album: t.Album})])
		}
	case s.album != nil:
		ms = append(ms, catalogMatches[matchKey(s)])
	}
	for _, m := range ms {
		if m.found && m.album.ArtistID > 0 {
			return artistSeed(m.album.ArtistID, leadArtist(m.album.Artist)), true
		}
	}
	return catalog.Artist{}, false
}

// artistSeed is what a catalog artist page starts from: the artist as
// last seen, or its id and name.
func artistSeed(id int64, name string) catalog.Artist {
	if a, ok := catalogArtists[id]; ok {
		return a
	}
	return catalog.Artist{ID: id, Name: name}
}

// open shows the match's page, or says there is none.
func (m catalogMatch) open(h Host, s Subject) tea.Cmd {
	switch {
	case !m.found:
		return h.Status(notInCatalogM)
	case m.artist.ID > 0:
		return h.Open(newCatalogArtistView(m.artist))
	}
	v := newCatalogAlbumView(m.album)
	if s.track != nil {
		v.want = trackWant{title: s.track.Title}
		if m.song > 0 {
			v.want.pid = player.StorePID(strconv.FormatInt(m.song, 10))
		}
	}
	return h.Open(v)
}

// openCatalogPage opens catalog subject s's own page: a song on its
// album's page with the song selected.
func openCatalogPage(h Host, s Subject) tea.Cmd {
	switch {
	case s.track != nil:
		it, _ := catalogItemOf(s.track)
		id, err := strconv.ParseInt(it.AlbumID, 10, 64)
		if err != nil || id <= 0 {
			return h.Status("No album page for this song")
		}
		seed, ok := catalogAlbums[id]
		if !ok {
			seed = catalog.Album{ID: id, Name: it.Album, Artist: it.Artist, Artwork: it.ArtURL}
		}
		v := newCatalogAlbumView(seed)
		v.want = trackWant{pid: s.track.PID}
		return h.Open(v)
	case s.album != nil:
		return h.OpenAlbum(s.album)
	}
	return h.Open(newCatalogArtistView(s.catArtist))
}

// findInCatalog looks library subject s up in the catalog, off the UI
// goroutine: one search of one kind.
func findInCatalog(ctx context.Context, cat Catalog, s Subject) (catalogMatch, error) {
	switch {
	case s.track != nil:
		t := s.track
		artist := t.Artist
		if artist == "" && t.Album != nil {
			artist = t.Album.Artist
		}
		res, err := cat.SearchKinds(ctx, searchTitle(t.Title)+" "+artist, catalog.Songs, matchLimit)
		if err != nil {
			return catalogMatch{}, err
		}
		song, ok := pickSong(res.Songs, t.Title, artist, t.AlbumName, t.Duration)
		if !ok {
			return catalogMatch{}, nil
		}
		al := catalog.Album{ID: song.AlbumID, ArtistID: song.ArtistID, Name: song.Album, Artist: song.Artist, Artwork: song.Artwork}
		return catalogMatch{found: true, album: al, song: song.ID}, nil
	case s.album != nil:
		a := s.album
		res, err := cat.SearchKinds(ctx, searchTitle(a.Title)+" "+a.Artist, catalog.Albums, matchLimit)
		if err != nil {
			return catalogMatch{}, err
		}
		al, ok := pickAlbum(res.Albums, a.Title, a.Artist, a.Year, len(a.Tracks))
		return catalogMatch{found: ok, album: al}, nil
	default:
		res, err := cat.SearchKinds(ctx, s.artist.Name, catalog.Artists, matchLimit)
		if err != nil {
			return catalogMatch{}, err
		}
		ar, ok := pickArtist(res.Artists, s.artist.Name)
		return catalogMatch{found: ok, artist: ar}, nil
	}
}

// pickAlbum is the catalog album that is this library album: the same
// title once edition notes are set aside, by the same artist. Among
// several, the exact title, the release year and the track count decide;
// a tie goes to Apple's order.
func pickAlbum(cands []catalog.Album, title, artist string, year, tracks int) (catalog.Album, bool) {
	key, exact := albumMatchKey(title), library.Fold(title)
	best, bestScore := -1, -1
	for i, c := range cands {
		if c.ID <= 0 || albumMatchKey(c.Name) != key || !sameArtist(c.Artist, artist) {
			continue
		}
		score := 0
		if library.Fold(c.Name) == exact {
			score += 4
		}
		if year > 0 && c.Year() == year {
			score += 2
		}
		if tracks > 0 {
			switch d := abs(c.TrackCount - tracks); {
			case d == 0:
				score += 2
			case d <= 2:
				score++
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return catalog.Album{}, false
	}
	return cands[best], true
}

// pickSong is the catalog song that is this library song: the same title
// (a featured artist or remaster note aside) by the same artist. The
// album, the length and whether it streams here decide among several.
func pickSong(cands []catalog.Song, title, artist, album string, dur time.Duration) (catalog.Song, bool) {
	key, albumKey := songMatchKey(title), albumMatchKey(album)
	best, bestScore := -1, -1
	for i, c := range cands {
		if c.ID <= 0 || c.AlbumID <= 0 || songMatchKey(c.Name) != key || !sameArtist(c.Artist, artist) {
			continue
		}
		score := 0
		if album != "" && albumMatchKey(c.Album) == albumKey {
			score += 4
		}
		if dur > 0 && c.Duration > 0 {
			switch d := (c.Duration - dur).Abs(); {
			case d <= 2*time.Second:
				score += 2
			case d <= 10*time.Second:
				score++
			}
		}
		if c.Streamable {
			score++
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return catalog.Song{}, false
	}
	return cands[best], true
}

// pickArtist is the first catalog artist with this name, else the
// longest name it starts with: "Daft Punk" for "Daft Punk & Pharrell
// Williams".
func pickArtist(cands []catalog.Artist, name string) (catalog.Artist, bool) {
	for _, c := range cands {
		if c.ID > 0 && sameName(c.Name, name) {
			return c, true
		}
	}
	n := len(library.Fold(name))
	best := -1
	for i, c := range cands {
		if f := len(library.Fold(c.Name)); c.ID > 0 && f < n && sameArtist(c.Name, name) &&
			(best < 0 || f > len(library.Fold(cands[best].Name))) {
			best = i
		}
	}
	if best < 0 {
		return catalog.Artist{}, false
	}
	return cands[best], true
}

// sameName compares artist names folded, with a leading "The" optional.
func sameName(a, b string) bool {
	fa, fb := library.Fold(a), library.Fold(b)
	return fa != "" && strings.TrimPrefix(fa, "the ") == strings.TrimPrefix(fb, "the ")
}

// artistJoins separate a lead artist from the others credited.
var artistJoins = []string{" & ", ", ", " and ", " x ", " feat. ", " feat ", " ft. ", " featuring ", " with "}

// sameArtist reports whether two artist credits name the same lead
// artist: "Daft Punk" and "Daft Punk & Pharrell Williams" do.
func sameArtist(a, b string) bool {
	if sameName(a, b) {
		return true
	}
	fa, fb := library.Fold(a), library.Fold(b)
	if len(fa) > len(fb) {
		fa, fb = fb, fa
	}
	if fa == "" {
		return false
	}
	for _, j := range artistJoins {
		if strings.HasPrefix(fb, fa+j) {
			return true
		}
	}
	return false
}

var (
	// songNoteRe is a note that doesn't make a song another recording: a
	// featured artist or a remaster.
	songNoteRe = regexp.MustCompile(`(?i)\s*[(\[](feat\.?|ft\.?|featuring|with)\s[^)\]]*[)\]]|\s*[(\[][^)\]]*\bremaster(ed)?\b[^)\]]*[)\]]|\s+-\s+(\d{4}\s+)?remaster(ed)?(\s+\d{4})?(\s+version)?$`)
	// searchNoteRe is any trailing note in brackets, left out of a
	// search: Apple may write it differently.
	searchNoteRe = regexp.MustCompile(`\s*[(\[][^)\]]*[)\]]\s*$`)
)

// songMatchKey folds a song title for matching, without a featured
// artist or remaster note. "(Live)" and "(Acoustic)" stay: they are other
// recordings.
func songMatchKey(title string) string {
	return library.Fold(strings.TrimSpace(songNoteRe.ReplaceAllString(title, "")))
}

// searchTitle is a title as a search term: without trailing notes in
// brackets and " - Single" or " - EP".
func searchTitle(title string) string {
	s := strings.TrimSpace(title)
	for {
		t := searchNoteRe.ReplaceAllString(s, "")
		t = strings.TrimSuffix(strings.TrimSuffix(t, " - Single"), " - EP")
		if t == s || t == "" {
			return s
		}
		s = t
	}
}

// openInItems are a menu's Open in items for s, in Apple Music or in the
// library: Open Album for a song or album, then Open Artist. The menu
// drops those that can't apply (see MenuOverlay.Init).
func openInItems(inLib bool, s Subject) []MenuItem {
	if s.empty() {
		return nil
	}
	album, artist := keymap.OpenAlbumInCatalog, keymap.OpenArtistInCatalog
	if inLib {
		album, artist = keymap.OpenAlbumInLibrary, keymap.OpenArtistInLibrary
	}
	var items []MenuItem
	if s.hasAlbum() {
		items = append(items, openInItem(album, s))
	}
	return append(items, openInItem(artist, s))
}

// openInItem is the menu item for act on s.
func openInItem(act keymap.Action, s Subject) MenuItem {
	return MenuItem{
		Label:  keymap.Name(act),
		Key:    act,
		Run:    func(h Host) tea.Cmd { return h.OpenIn(act, s) },
		openIn: &s,
	}
}

// openInApplies reports whether a menu's Open in item can do anything:
// Open Album needs a song or album, Apple Music needs the catalog (or a
// catalog item that knows its artist), the library needs the album or
// artist in it.
func openInApplies(act keymap.Action, s Subject, c *Ctx) bool {
	artist := opensArtist(act)
	switch {
	case s.empty(), !artist && !s.hasAlbum():
		return false
	case inLibrary(act):
		_, ok := libraryTarget(c.Lib, s, artist)
		return ok
	case s.fromCatalog():
		_, ok := catalogArtistOf(s)
		return ok || !artist
	}
	return c.Catalog != nil
}

// OpenIn opens the album or artist of s in Apple Music or the library
// (see openIn).
func (a *App) OpenIn(act keymap.Action, s Subject) tea.Cmd {
	return openIn(a, act, a.svc.Catalog, a.lib, s)
}

// focusedSubject is what Open in acts on from the keyboard: the focused
// view's or pane's selection.
func (a *App) focusedSubject() Subject {
	switch a.focus {
	case focusSidebar:
		return Subject{}
	case focusRight:
		if p, ok := a.pane().(subjecter); ok {
			return p.subject(a.ctxFor(focusRight, a.lay.RightBody.Min))
		}
		return Subject{}
	}
	v := a.nav.cur
	if v == nil {
		return Subject{}
	}
	c := a.viewCtx()
	if sv, ok := v.(subjecter); ok {
		return sv.subject(c)
	}
	return subjectOf(v.Selected(c))
}

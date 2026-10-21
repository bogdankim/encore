package library

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

// Display names for groups that have no tag of their own.
const (
	VariousArtists = "Various Artists"
	UnknownArtist  = "Unknown Artist"
	UnknownAlbum   = "Unknown Album"
)

// Build indexes a snapshot. The snapshot is not modified and may be dropped
// afterwards; the Library shares its strings.
func Build(s *bridge.Snapshot) *Library {
	b := &builder{
		folds:  make(map[string]string, len(s.Tracks)/2),
		albums: make(map[string]*albumAcc, len(s.Tracks)/8),
		groups: make(map[string]*groupAcc, len(s.Tracks)/20),
	}
	l := &Library{
		Built:      time.Now(),
		byPID:      make(map[string]*Track, len(s.Tracks)),
		albumByKey: make(map[string]*Album, len(s.Tracks)/8),
	}

	tracks := make([]Track, len(s.Tracks))
	l.All = make([]*Track, len(s.Tracks))
	for i := range s.Tracks {
		t := &tracks[i]
		b.fillTrack(t, &s.Tracks[i])
		l.All[i] = t
		l.byPID[t.PID] = t
		b.addToAlbum(t, &s.Tracks[i])
	}

	albums := make([]Album, len(b.albumOrder))
	for i, acc := range b.albumOrder {
		a := &albums[i]
		b.finishAlbum(a, acc)
		l.albumByKey[a.Key] = a
		b.addToGroup(a, acc)
	}

	l.artistByNm = make(map[string]*Artist, len(b.groupOrder))
	artists := make([]Artist, len(b.groupOrder))
	for i, g := range b.groupOrder {
		ar := &artists[i]
		finishGroup(ar, g)
		g.artist = ar
		l.artistByNm[ar.Name] = ar
		if len(g.listed) > 0 {
			l.Artists = append(l.Artists, ar)
		}
	}

	for _, acc := range b.albumOrder {
		for _, t := range acc.tracks {
			t.Group = acc.group.artist
		}
	}

	rankKeys(tracks, albums)

	for _, acc := range b.albumOrder {
		if acc.lib > 0 {
			l.Albums = append(l.Albums, acc.album)
		}
	}
	slices.SortFunc(l.Albums, compareAlbumTitle)
	l.RecentlyAdded = slices.Clone(l.Albums)
	sortStable(l.RecentlyAdded, func(a, b *Album) int {
		return b.Added.Compare(a.Added)
	})
	slices.SortFunc(l.Artists, func(a, b *Artist) int {
		if c := CompareKeys(a.SortName, b.SortName); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})

	for _, t := range l.All {
		if t.InLibrary {
			l.Songs = append(l.Songs, t)
		}
	}
	sortStable(l.Songs, compareTrackTitle)
	for _, t := range l.Songs {
		if t.Favorited {
			l.Favorites = append(l.Favorites, t)
		}
	}

	buildPlaylists(l, s.Playlists)
	return l
}

type builder struct {
	folds      map[string]string
	albums     map[string]*albumAcc
	albumOrder []*albumAcc
	groups     map[string]*groupAcc
	groupOrder []*groupAcc
}

// albumAcc collects one album's tracks while building.
type albumAcc struct {
	key    string
	album  *Album
	group  *groupAcc
	tracks []*Track // every track, library or not
	snap   []*bridge.SnapshotTrack
	lib    int // in-library tracks
}

// groupAcc collects one artist's albums while building.
type groupAcc struct {
	artist *Artist
	names  []weighted // display spellings seen, by track count
	sorts  []weighted
	albums []*Album
	listed []*Album
}

type weighted struct {
	s string
	n int
}

func addWeight(ws []weighted, s string, n int) []weighted {
	for i := range ws {
		if ws[i].s == s {
			ws[i].n += n
			return ws
		}
	}
	return append(ws, weighted{s, n})
}

func heaviest(ws []weighted) string {
	best := 0
	for i := range ws {
		if ws[i].n > ws[best].n {
			best = i
		}
	}
	if len(ws) == 0 {
		return ""
	}
	return ws[best].s
}

// fold caches folds of strings that repeat across tracks (artists, albums).
func (b *builder) fold(s string) string {
	if s == "" {
		return ""
	}
	if f, ok := b.folds[s]; ok {
		return f
	}
	f := Fold(s)
	b.folds[s] = f
	return f
}

func unixTime(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func (b *builder) fillTrack(t *Track, st *bridge.SnapshotTrack) {
	*t = Track{
		PID:         st.PID,
		Title:       st.Title,
		Artist:      st.Artist,
		AlbumArtist: st.AlbumArtist,
		AlbumName:   st.Album,
		AlbumPID:    st.AlbumPID,
		Genre:       st.Genre,
		Composer:    st.Composer,
		Year:        st.Year,
		TrackNo:     st.Track,
		TrackCount:  st.TrackCount,
		Disc:        st.Disc,
		DiscCount:   st.DiscCount,
		Duration:    time.Duration(st.Duration) * time.Millisecond,
		Added:       unixTime(st.Added),
		LastPlayed:  unixTime(st.LastPlayed),
		Plays:       st.Plays,
		Rating:      st.Rating,
		Favorited:   st.Favorited,
		Disliked:    st.Disliked,
		Explicit:    st.Explicit,
		InLibrary:   st.InLibrary,
		Available:   st.Available,
		Kind:        st.Kind,
		HasArt:      st.Art,
	}
	t.FoldTitle = Fold(st.Title)
	t.FoldArtist = b.fold(st.Artist)
	t.FoldAlbum = b.fold(st.Album)
	t.SortTitle = sortKey(b.sortFold(st.SortTitle, t.FoldTitle))
	t.SortArtist = sortKey(b.sortFold(st.SortArtist, t.FoldArtist))
	t.SortAlbum = sortKey(b.sortFold(st.SortAlbum, t.FoldAlbum))
}

// sortFold returns the folded Music sort field, or fallback when it is empty.
func (b *builder) sortFold(field, fallback string) string {
	if field == "" {
		return fallback
	}
	return b.fold(field)
}

func (b *builder) addToAlbum(t *Track, st *bridge.SnapshotTrack) {
	key := st.AlbumPID
	if key == "" {
		who := b.fold(st.AlbumArtist)
		if who == "" && !st.Compilation {
			who = t.FoldArtist
		}
		key = who + "|" + t.FoldAlbum
		if st.Compilation {
			key += "|c"
		}
	}
	acc := b.albums[key]
	if acc == nil {
		acc = &albumAcc{key: key}
		b.albums[key] = acc
		b.albumOrder = append(b.albumOrder, acc)
	}
	acc.tracks = append(acc.tracks, t)
	acc.snap = append(acc.snap, st)
	if t.InLibrary {
		acc.lib++
	}
}

func (b *builder) finishAlbum(a *Album, acc *albumAcc) {
	acc.album = a
	members, snaps := acc.tracks, acc.snap
	if acc.lib > 0 && acc.lib < len(members) {
		members = make([]*Track, 0, acc.lib)
		snaps = make([]*bridge.SnapshotTrack, 0, acc.lib)
		for i, t := range acc.tracks {
			if t.InLibrary {
				members = append(members, t)
				snaps = append(snaps, acc.snap[i])
			}
		}
	}
	slices.SortStableFunc(members, compareAlbumOrder)
	for _, t := range acc.tracks {
		t.Album = a
	}

	a.Key = acc.key
	a.PID = acc.snap[0].AlbumPID
	a.Tracks = members

	var titles, albumArtists, artists, sortAlbums, sortAlbumArtists, sortArtists, genres []weighted
	years := make([]int, 0, len(snaps))
	for _, st := range snaps {
		titles = addNonEmpty(titles, st.Album)
		albumArtists = addNonEmpty(albumArtists, st.AlbumArtist)
		artists = addWeight(artists, st.Artist, 1)
		sortAlbums = addNonEmpty(sortAlbums, st.SortAlbum)
		sortAlbumArtists = addNonEmpty(sortAlbumArtists, st.SortAlbumArtist)
		sortArtists = addNonEmpty(sortArtists, st.SortArtist)
		genres = addNonEmpty(genres, st.Genre)
		if st.Compilation {
			a.Compilation = true
		}
		if st.Year > 0 {
			years = append(years, st.Year)
		}
	}

	a.Title = heaviest(titles)
	if a.Title == "" {
		a.Title = UnknownAlbum
	}
	a.Genre = heaviest(genres)
	a.Year = commonYear(years)

	var sortArtist string
	switch aa := heaviest(albumArtists); {
	case aa != "":
		a.Artist = aa
		sortArtist = heaviest(sortAlbumArtists)
	case a.Compilation:
		a.Artist = VariousArtists
	default:
		a.Artist, sortArtist = b.commonArtist(members, artists, sortArtists)
	}
	a.FoldTitle = Fold(a.Title)
	a.FoldArtist = b.fold(a.Artist)
	a.SortTitle = sortKey(b.sortFold(heaviest(sortAlbums), a.FoldTitle))
	a.SortArtist = sortKey(b.sortFold(sortArtist, a.FoldArtist))

	for _, t := range members {
		a.Duration += t.Duration
		if t.Added.After(a.Added) {
			a.Added = t.Added
		}
	}
	a.ArtPID = a.PID
	if a.ArtPID == "" {
		a.ArtPID = members[0].PID
		for _, t := range members {
			if t.HasArt {
				a.ArtPID = t.PID
				break
			}
		}
	}
}

// commonArtist is the display artist of an album without an album artist: the
// track artist when every track has the same one (in its most common
// spelling), otherwise Various Artists.
func (b *builder) commonArtist(members []*Track, artists, sortArtists []weighted) (name, sort string) {
	fold := members[0].FoldArtist
	for _, t := range members[1:] {
		if t.FoldArtist != fold {
			return VariousArtists, ""
		}
	}
	if fold == "" {
		return UnknownArtist, ""
	}
	return heaviest(artists), heaviest(sortArtists)
}

func addNonEmpty(ws []weighted, s string) []weighted {
	if s == "" {
		return ws
	}
	return addWeight(ws, s, 1)
}

// commonYear is the most frequent year, the latest one on a tie.
func commonYear(years []int) int {
	best, bestN := 0, 0
	for _, y := range years {
		n := 0
		for _, z := range years {
			if z == y {
				n++
			}
		}
		if n > bestN || n == bestN && y > best {
			best, bestN = y, n
		}
	}
	return best
}

// groupName is the artist an album is listed under.
func groupName(a *Album) string {
	if a.Compilation {
		return VariousArtists
	}
	return a.Artist
}

func (b *builder) addToGroup(a *Album, acc *albumAcc) {
	name := groupName(a)
	key := b.fold(name)
	g := b.groups[key]
	if g == nil {
		g = &groupAcc{}
		b.groups[key] = g
		b.groupOrder = append(b.groupOrder, g)
	}
	weight := len(a.Tracks)
	g.names = addWeight(g.names, name, weight)
	sort := a.SortArtist
	if name != a.Artist {
		sort = sortKey(b.fold(name))
	}
	g.sorts = addWeight(g.sorts, sort, weight)
	g.albums = append(g.albums, a)
	acc.group = g
	if acc.lib > 0 {
		g.listed = append(g.listed, a)
	}
}

func finishGroup(ar *Artist, g *groupAcc) {
	ar.Name = heaviest(g.names)
	ar.FoldName = Fold(ar.Name)
	ar.SortName = heaviest(g.sorts)
	ar.Albums = g.listed
	if len(ar.Albums) == 0 {
		ar.Albums = g.albums
	}
	slices.SortFunc(ar.Albums, func(x, y *Album) int {
		if c := cmp.Compare(y.Year, x.Year); c != 0 {
			return c
		}
		if c := CompareKeys(x.SortTitle, y.SortTitle); c != 0 {
			return c
		}
		return cmp.Compare(x.Key, y.Key)
	})
	for _, a := range ar.Albums {
		ar.Tracks += len(a.Tracks)
	}
}

// rankKeys assigns every track and album its position in the library-wide
// order of titles, artists and album names.
func rankKeys(tracks []Track, albums []Album) {
	titles := make(map[string]int32, len(tracks))
	artists := make(map[string]int32, len(albums))
	names := make(map[string]int32, len(albums))
	for i := range tracks {
		t := &tracks[i]
		titles[t.SortTitle] = 0
		artists[t.SortArtist] = 0
		names[t.SortAlbum] = 0
	}
	for i := range albums {
		a := &albums[i]
		artists[a.SortArtist] = 0
		names[a.SortTitle] = 0
	}
	var wg sync.WaitGroup
	for _, m := range []map[string]int32{titles, artists, names} {
		wg.Go(func() { assignRanks(m) })
	}
	wg.Wait()
	for i := range tracks {
		t := &tracks[i]
		t.titleRank = titles[t.SortTitle]
		t.artistRank = artists[t.SortArtist]
		t.albumRank = names[t.SortAlbum]
	}
	for i := range albums {
		a := &albums[i]
		a.titleRank = names[a.SortTitle]
		a.artistRank = artists[a.SortArtist]
	}
}

func assignRanks(m map[string]int32) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, CompareKeys)
	for i, k := range keys {
		m[k] = int32(i + 1)
	}
}

// excludedPlaylist reports playlists that duplicate the library itself.
func excludedPlaylist(p *bridge.SnapshotPlaylist) bool {
	return p.Special == "library" || p.Special == "music"
}

func buildPlaylists(l *Library, src []bridge.SnapshotPlaylist) {
	pls := make([]Playlist, 0, len(src))
	parents := make([]string, 0, len(src))
	for i := range src {
		sp := &src[i]
		if excludedPlaylist(sp) {
			continue
		}
		p := Playlist{
			PID:      sp.PID,
			Name:     sp.Name,
			Kind:     sp.Kind,
			Special:  sp.Special,
			FoldName: Fold(sp.Name),
		}
		if sp.Kind != "folder" {
			p.Tracks = make([]*Track, 0, len(sp.Items))
			for _, pid := range sp.Items {
				if t := l.byPID[pid]; t != nil {
					p.Tracks = append(p.Tracks, t)
					p.Duration += t.Duration
				}
			}
		}
		pls = append(pls, p)
		parents = append(parents, sp.ParentPID)
	}

	l.plByPID = make(map[string]*Playlist, len(pls))
	for i := range pls {
		l.plByPID[pls[i].PID] = &pls[i]
	}
	for i := range pls {
		p := &pls[i]
		parent := l.plByPID[parents[i]]
		if parent == nil || parent.Kind != "folder" || isAncestor(p, parent) {
			l.Playlists = append(l.Playlists, p)
			continue
		}
		p.Parent = parent
		parent.Children = append(parent.Children, p)
	}
	for _, p := range l.Playlists {
		flatten(l, p)
	}

	for _, p := range l.PlaylistsFlat {
		for _, t := range p.Tracks {
			if !t.InLibrary && t.Container == "" {
				t.Container = p.PID
			}
		}
	}
}

// isAncestor reports whether p is parent or one of its ancestors.
func isAncestor(p, parent *Playlist) bool {
	for q := parent; q != nil; q = q.Parent {
		if q == p {
			return true
		}
	}
	return false
}

// flatten appends p and its descendants depth-first and sums folder durations.
func flatten(l *Library, p *Playlist) {
	l.PlaylistsFlat = append(l.PlaylistsFlat, p)
	for _, c := range p.Children {
		flatten(l, c)
		if p.Kind == "folder" {
			p.Duration += c.Duration
		}
	}
}

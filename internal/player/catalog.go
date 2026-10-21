package player

import (
	"strconv"
	"strings"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
)

// Catalog playback: Apple Music songs from outside the library, played by
// store ID through Client.PlayStore.
//
// In the model a catalog song is a stand-in *library.Track whose PID is
// "store:<id>" (see StorePID), so the context, the user queue, shuffle and Up
// Next treat both kinds alike. Music plays catalog songs under temporary
// persistent IDs, so reports are mapped back to the stand-in by name, artist
// and duration (canon).

// KindCatalog is a context of catalog songs: a catalog album, artist or search.
const KindCatalog Kind = "catalog"

// CatalogItem is a catalog song encore can play.
type CatalogItem struct {
	StoreID  string // Apple Music catalog song id (catalog.Song.ID)
	Title    string
	Artist   string
	Album    string
	AlbumID  string // catalog album id; optional
	ArtistID string // catalog artist id; optional
	Duration time.Duration
	ArtURL   string // album artwork URL; optional
}

// CatalogItemFromSong converts a catalog search or lookup result. artSize is
// the artwork size in px for ArtURL (see catalog.Song.ArtURL).
func CatalogItemFromSong(s catalog.Song, artSize int) CatalogItem {
	it := CatalogItem{
		Title:    s.Name,
		Artist:   s.Artist,
		Album:    s.Album,
		Duration: s.Duration,
		ArtURL:   s.ArtURL(artSize),
	}
	if s.ID > 0 {
		it.StoreID = formatID(s.ID)
	}
	if s.AlbumID > 0 {
		it.AlbumID = formatID(s.AlbumID)
	}
	if s.ArtistID > 0 {
		it.ArtistID = formatID(s.ArtistID)
	}
	return it
}

const storePrefix = "store:"

// StorePID is the pid of a catalog song's stand-in track.
func StorePID(storeID string) string { return storePrefix + storeID }

// IsCatalog reports whether t is a catalog song's stand-in rather than a
// library track. Stand-ins have Album and Group set, but neither is in the
// library, and the bridge knows nothing about their PID.
func IsCatalog(t *library.Track) bool { return t != nil && isStorePID(t.PID) }

func isStorePID(pid string) bool { return strings.HasPrefix(pid, storePrefix) }

// storeIDOf returns the store id in a stand-in's pid.
func storeIDOf(pid string) string { return strings.TrimPrefix(pid, storePrefix) }

// catalogEntry is a catalog song the model refers to.
type catalogEntry struct {
	item CatalogItem
	t    *library.Track
	// Folded once for matching reports against it.
	title, artist string
}

// CatalogItem returns the catalog song behind t, or nil when t is not one.
func (p *Player) CatalogItem(t *library.Track) *CatalogItem {
	if !IsCatalog(t) {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.catalogItem(t.PID)
}

func (p *Player) catalogItem(pid string) *CatalogItem {
	e := p.items[pid]
	if e == nil {
		return nil
	}
	it := e.item
	return &it
}

// PlayCatalog plays songs[start] and makes songs the context, like
// PlayContext for library tracks. Items without a store ID are left out. The
// user queue is kept and plays after the first song.
func (p *Player) PlayCatalog(src Source, songs []CatalogItem, start int) {
	defer p.lock()()
	start = max(start, 0)
	ts := make([]*library.Track, 0, len(songs))
	first := -1
	for i, s := range songs {
		if !validStoreID(s.StoreID) {
			continue
		}
		if first < 0 && i >= start {
			first = len(ts)
		}
		ts = append(ts, p.standIn(s))
	}
	if first < 0 {
		p.setErr(MsgNothingPlayable)
		return
	}
	p.setContext(src, ts, first)
	p.issue()
}

// PlayNextCatalog puts catalog songs at the front of the user queue.
func (p *Player) PlayNextCatalog(songs ...CatalogItem) {
	defer p.lock()()
	p.touch()
	p.queue = append(p.standIns(songs), p.queue...)
}

// AddCatalogToQueue appends catalog songs to the user queue.
func (p *Player) AddCatalogToQueue(songs ...CatalogItem) {
	defer p.lock()()
	p.touch()
	p.queue = append(p.queue, p.standIns(songs)...)
}

func (p *Player) standIns(songs []CatalogItem) []*library.Track {
	out := make([]*library.Track, 0, len(songs))
	for _, s := range songs {
		if validStoreID(s.StoreID) {
			out = append(out, p.standIn(s))
		}
	}
	return out
}

// standIn returns the model track for s, registering it.
func (p *Player) standIn(s CatalogItem) *library.Track {
	pid := StorePID(s.StoreID)
	if e := p.items[pid]; e != nil && e.item == s {
		return e.t
	}
	t := StandIn(s)
	if p.items == nil {
		p.items = map[string]*catalogEntry{}
	}
	p.items[pid] = &catalogEntry{item: s, t: t, title: t.FoldTitle, artist: t.FoldArtist}
	return t
}

// CatalogAlbumPrefix starts the Album.Key of a catalog song's stand-in;
// the catalog album id follows when the item has one.
const CatalogAlbumPrefix = "catalog:"

// StandIn returns a stand-in track for s, as the model holds catalog songs:
// pid StorePID(s.StoreID), an Album keyed CatalogAlbumPrefix+AlbumID whose
// ArtPID is s.ArtURL, and a Group named for the artist. Neither is in the
// library. Each call returns a new track.
func StandIn(s CatalogItem) *library.Track {
	key := s.AlbumID
	if key == "" {
		key = s.Artist + "|" + s.Album
	}
	return &library.Track{
		PID:         StorePID(s.StoreID),
		Title:       s.Title,
		Artist:      s.Artist,
		AlbumArtist: s.Artist,
		AlbumName:   s.Album,
		Duration:    s.Duration,
		Available:   true,
		Kind:        "catalog",
		HasArt:      s.ArtURL != "",
		Album:       &library.Album{Key: CatalogAlbumPrefix + key, Title: s.Album, Artist: s.Artist, ArtPID: s.ArtURL},
		Group:       &library.Artist{Name: s.Artist, SortName: library.Fold(s.Artist), FoldName: library.Fold(s.Artist)},
		SortTitle:   library.Fold(s.Title),
		SortArtist:  library.Fold(s.Artist),
		SortAlbum:   library.Fold(s.Album),
		FoldTitle:   library.Fold(s.Title),
		FoldArtist:  library.Fold(s.Artist),
		FoldAlbum:   library.Fold(s.Album),
	}
}

// pruneCatalog forgets catalog songs the model no longer refers to.
func (p *Player) pruneCatalog() {
	if len(p.items) == 0 {
		return
	}
	keep := make(map[string]bool, len(p.items))
	mark := func(t *library.Track) {
		if IsCatalog(t) {
			keep[t.PID] = true
		}
	}
	mark(p.now)
	for _, t := range p.orig {
		mark(t)
	}
	for _, t := range p.queue {
		mark(t)
	}
	for _, pid := range p.issued {
		keep[pid] = true
	}
	for pid := range p.items {
		if !keep[pid] {
			delete(p.items, pid)
		}
	}
}

// canon maps the track Music reports to the pid the model knows it by. A
// catalog song plays under a temporary persistent ID, so a report that isn't
// one of the pids we issued is matched by name, artist and duration against
// the catalog songs we issued, preferring the one Music should be on.
func (p *Player) canon(ct *bridge.CurrentTrack) string {
	if ct == nil {
		return ""
	}
	if !p.issuedStore {
		return ct.PID
	}
	if ct.PID == p.canonRaw {
		return p.canonPID
	}
	pid := ct.PID
	if !p.isIssued(pid) {
		if m := p.matchCatalog(ct); m != "" {
			pid = m
		}
	}
	p.canonRaw, p.canonPID = ct.PID, pid
	return pid
}

func (p *Player) isIssued(pid string) bool {
	for _, x := range p.issued {
		if x == pid {
			return true
		}
	}
	return false
}

func (p *Player) matchCatalog(ct *bridge.CurrentTrack) string {
	name, artist := library.Fold(ct.Name), library.Fold(ct.Artist)
	if name == "" {
		return ""
	}
	if p.pending != nil && p.sameSong(p.pending.pid, name, artist, ct.Duration) {
		return p.pending.pid
	}
	best, bestD := "", -1
	base := p.issuedIdx + 1
	for k, pid := range p.issued {
		if !p.sameSong(pid, name, artist, ct.Duration) {
			continue
		}
		d := abs(k - base)
		if k == 0 && base >= len(p.issued) {
			d = 0 // repeat all wraps to the start
		}
		if bestD < 0 || d < bestD {
			best, bestD = pid, d
		}
	}
	return best
}

// sameSong reports whether a report of name and artist (folded) and dur
// seconds is the catalog song pid. Names must agree; the artist or a close
// duration confirms it. The search API's durations are within a second or
// two of Music's.
func (p *Player) sameSong(pid, name, artist string, dur float64) bool {
	e := p.items[pid]
	if e == nil || e.title == "" {
		return false
	}
	diff := -1.0 // unknown
	if dur > 0 && e.item.Duration > 0 {
		diff = dur - e.item.Duration.Seconds()
		if diff < 0 {
			diff = -diff
		}
	}
	artistOK := artist != "" && e.artist != "" &&
		(artist == e.artist || strings.Contains(artist, e.artist) || strings.Contains(e.artist, artist))
	near := diff >= 0 && diff <= 2
	switch {
	case name == e.title:
		return near || (artistOK && diff <= 10)
	case strings.Contains(name, e.title) || strings.Contains(e.title, name):
		return artistOK && near
	}
	return false
}

func validStoreID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func formatID(n int64) string { return strconv.FormatInt(n, 10) }

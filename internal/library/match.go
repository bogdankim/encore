package library

import (
	"regexp"
	"strings"
)

// Matching Apple Music catalog items to the library.
//
// Library songs carry the catalog id of the song when Music knows it
// (Track.StoreID, from iTunesLibrary's storeItemID) and library playlists the
// cloud id the catalog uses (Playlist.GlobalID, "pl.…"). Albums carry neither,
// and neither do local files, uploads or snapshots read through Apple Events,
// so names decide there. The rules, which the UI shares:
//
//   - A song matches by catalog id. Without an id on either side it matches
//     by title and lead artist (SongKey, SameArtist). A library song that has
//     an id of its own but another one than the catalog song's is another
//     release of the song (the single, a compilation) and doesn't match.
//   - An album matches by title and artist (AlbumKey, SameName); it counts as
//     in the library when the library lists it, which is when at least one of
//     its songs is in the library.
//   - A playlist matches by cloud id only.
//
// The index is built once with the library, so a lookup costs a map access
// and a handful of string compares.

// matchIndex is what Build precomputes for the lookups below. Only songs in
// the library (Track.InLibrary) are indexed: a song that sits in a playlist
// without being added isn't "in your library".
type matchIndex struct {
	byStore  map[int64]*Track
	byTitle  map[string][]*Track // by SongKey of the title
	byAlbum  map[string][]*Album // by AlbumKey of the title, in l.Albums order
	byGlobal map[string]*Playlist
}

func (l *Library) buildMatch() {
	m := &l.match
	m.byStore = make(map[int64]*Track, len(l.Songs))
	m.byTitle = make(map[string][]*Track, len(l.Songs))
	m.byAlbum = make(map[string][]*Album, len(l.Albums))
	m.byGlobal = make(map[string]*Playlist)
	for _, t := range l.Songs {
		if t.StoreID > 0 {
			if _, dup := m.byStore[t.StoreID]; !dup {
				m.byStore[t.StoreID] = t
			}
		}
		if k := trackSongKey(t); k != "" {
			m.byTitle[k] = append(m.byTitle[k], t)
		}
	}
	for _, a := range l.Albums {
		if k := AlbumKey(a.Title); k != "" {
			m.byAlbum[k] = append(m.byAlbum[k], a)
		}
	}
	for _, p := range l.PlaylistsFlat {
		if p.GlobalID != "" && p.Kind != "folder" {
			if _, dup := m.byGlobal[p.GlobalID]; !dup {
				m.byGlobal[p.GlobalID] = p
			}
		}
	}
}

// trackSongKey is SongKey(t.Title), skipping the note patterns when the title
// has no brackets or dashes to hold one.
func trackSongKey(t *Track) string {
	if !strings.ContainsAny(t.Title, "([") && !strings.Contains(t.Title, " - ") {
		return t.FoldTitle
	}
	return SongKey(t.Title)
}

// SongByStoreID returns the library song with this Apple Music catalog id, or
// nil.
func (l *Library) SongByStoreID(id int64) *Track {
	if l == nil || id <= 0 {
		return nil
	}
	return l.match.byStore[id]
}

// PlaylistByGlobalID returns the library playlist with this cloud id
// ("pl.…"), or nil.
func (l *Library) PlaylistByGlobalID(id string) *Playlist {
	if l == nil || id == "" {
		return nil
	}
	return l.match.byGlobal[id]
}

// FindAlbum returns the library album with this title by this artist, or nil.
// Edition notes and a " - Single" or " - EP" suffix don't count (AlbumKey);
// the artist is compared folded, "The" aside.
func (l *Library) FindAlbum(title, artist string) *Album {
	if l == nil || title == "" {
		return nil
	}
	for _, a := range l.match.byAlbum[AlbumKey(title)] {
		if SameName(a.Artist, artist) {
			return a
		}
	}
	return nil
}

// FindSong returns the library song with this title by this artist, or nil,
// whether or not it has a catalog id of its own: the loose match "Open in
// Library" uses. A featured artist or a remaster note doesn't count (SongKey).
func (l *Library) FindSong(title, artist string) *Track {
	return l.findSong(0, title, artist)
}

// MatchSong returns the library song that is catalog song storeID, or nil:
// the song with that id, else (see the rules at the top) the song with this
// title by this artist that has no catalog id to tell it apart by. storeID 0
// is FindSong.
func (l *Library) MatchSong(storeID int64, title, artist string) *Track {
	if t := l.SongByStoreID(storeID); t != nil {
		return t
	}
	return l.findSong(storeID, title, artist)
}

func (l *Library) findSong(storeID int64, title, artist string) *Track {
	if l == nil {
		return nil
	}
	key := SongKey(title)
	if key == "" {
		return nil
	}
	for _, t := range l.match.byTitle[key] {
		if storeID > 0 && t.StoreID > 0 {
			continue // another release of the song
		}
		if SameArtist(t.Artist, artist) {
			return t
		}
	}
	return nil
}

var (
	editionRe = regexp.MustCompile(`\s*[(\[][^)\]]*\b(edition|remaster(ed)?|deluxe|expanded|version|explicit|clean|bonus)\b[^)\]]*[)\]]\s*$`)

	// songNoteRe is a note that doesn't make a song another recording: a
	// featured artist or a remaster.
	songNoteRe = regexp.MustCompile(`(?i)\s*[(\[](feat\.?|ft\.?|featuring|with)\s[^)\]]*[)\]]|\s*[(\[][^)\]]*\bremaster(ed)?\b[^)\]]*[)\]]|\s+-\s+(\d{4}\s+)?remaster(ed)?(\s+\d{4})?(\s+version)?$`)

	// artistJoins separate a lead artist from the others credited.
	artistJoins = []string{" & ", ", ", " and ", " x ", " feat. ", " feat ", " ft. ", " featuring ", " with "}
)

// AlbumKey folds an album title for matching: case, diacritics, a trailing
// " - Single" or " - EP", and an edition note such as "(Deluxe Edition)" or
// "[Remastered]" don't count. "Live (at Home)" stays: it is not an edition
// note.
func AlbumKey(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	for {
		t := editionRe.ReplaceAllString(s, "")
		t = strings.TrimSuffix(strings.TrimSuffix(t, " - single"), " - ep")
		if t == s {
			break
		}
		s = t
	}
	return Fold(s)
}

// SongKey folds a song title for matching, without a featured artist or
// remaster note. "(Live)" and "(Acoustic)" stay: they are other recordings.
func SongKey(title string) string {
	return Fold(strings.TrimSpace(songNoteRe.ReplaceAllString(title, "")))
}

// SameName compares artist names folded, with a leading "The" optional.
func SameName(a, b string) bool {
	fa, fb := Fold(a), Fold(b)
	return fa != "" && strings.TrimPrefix(fa, "the ") == strings.TrimPrefix(fb, "the ")
}

// SameArtist reports whether two artist credits name the same lead artist:
// "Daft Punk" and "Daft Punk & Pharrell Williams" do.
func SameArtist(a, b string) bool {
	if SameName(a, b) {
		return true
	}
	fa, fb := Fold(a), Fold(b)
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

// Package library is encore's in-memory index of the Music library. A Library is
// built once from a bridge.Snapshot and never mutated afterwards, so it can be
// shared across goroutines freely; a refresh builds a new Library and swaps it.
package library

import "time"

// Track is one song. PID is Music.app's persistent ID in 16-char uppercase hex.
type Track struct {
	PID         string
	Title       string
	Artist      string
	AlbumArtist string // as tagged; may be empty
	AlbumName   string
	AlbumPID    string
	Genre       string
	Composer    string
	Year        int
	TrackNo     int
	TrackCount  int
	Disc        int
	DiscCount   int
	Duration    time.Duration
	Added       time.Time
	LastPlayed  time.Time // zero = never
	Plays       int
	Rating      int // 0..100
	Favorited   bool
	Disliked    bool
	Explicit    bool
	InLibrary   bool // part of "library playlist 1"; false for playlist-only items
	Available   bool // false for cloud items that are no longer available
	Kind        string
	HasArt      bool

	// Container is a playlist that holds this track when InLibrary is false.
	Container string

	Album *Album  // never nil after Build
	Group *Artist // the artist this track is listed under (album artist group); never nil

	// Precomputed for sorting and search: folded (lowercase, no diacritics) and,
	// for sort keys, with a leading "the " removed.
	SortTitle, SortArtist, SortAlbum string
	FoldTitle, FoldArtist, FoldAlbum string

	// Positions of SortTitle, SortArtist and SortAlbum in the library-wide key
	// order (1-based; equal keys share a rank, 0 = not built by Build). They
	// turn view sorts into integer compares.
	titleRank, artistRank, albumRank int32
}

// Album groups tracks by album persistent ID (fallback key: album artist + name).
type Album struct {
	Key         string // AlbumPID or the fallback key
	PID         string // may be empty
	Title       string
	Artist      string // display artist: album artist, else the common track artist, else "Various Artists"
	Year        int
	Genre       string
	Compilation bool
	// Tracks are ordered by disc, then track number, then title. Only in-library
	// tracks are listed, except for an album made only of playlist-only items.
	Tracks   []*Track
	Duration time.Duration
	Added    time.Time // newest Added among its tracks
	ArtPID   string    // a pid to request artwork with (album PID or a track PID)

	SortTitle, SortArtist string
	FoldTitle, FoldArtist string

	titleRank, artistRank int32 // see Track
}

// Artist groups albums by display album artist.
type Artist struct {
	Name     string
	Albums   []*Album // newest first; in-library albums unless the artist has none
	Tracks   int      // number of tracks across Albums
	SortName string
	FoldName string
}

// Playlist is a user, smart, subscription or folder playlist.
type Playlist struct {
	PID      string
	Parent   *Playlist // nil at the top level
	Name     string
	Kind     string // user, smart, folder, subscription, genius
	Special  string
	Tracks   []*Track // playlist order; empty for folders
	Children []*Playlist
	Duration time.Duration
	FoldName string
}

// Library is the whole index. Slices are already sorted for display.
type Library struct {
	Built time.Time

	All           []*Track    // every item from the snapshot, in snapshot order
	Songs         []*Track    // InLibrary tracks, sorted by title (Apple Music's Songs view)
	Favorites     []*Track    // favorited InLibrary tracks, sorted by title
	Albums        []*Album    // sorted by title, then artist
	Artists       []*Artist   // sorted by name
	RecentlyAdded []*Album    // newest first
	Playlists     []*Playlist // top level, in Music's order; folders hold children
	PlaylistsFlat []*Playlist // depth-first, folders included

	byPID      map[string]*Track
	albumByKey map[string]*Album
	artistByNm map[string]*Artist
	plByPID    map[string]*Playlist
}

// Track returns the track with this pid, or nil.
func (l *Library) Track(pid string) *Track { return l.byPID[pid] }

// Album returns the album with this key, or nil.
func (l *Library) Album(key string) *Album { return l.albumByKey[key] }

// Artist returns the artist with this exact display name, or nil.
func (l *Library) Artist(name string) *Artist { return l.artistByNm[name] }

// Playlist returns the playlist with this pid, or nil.
func (l *Library) Playlist(pid string) *Playlist { return l.plByPID[pid] }

// Package bridge talks to encore-bridge, the Swift helper that owns Music.app,
// iTunesLibrary and other macOS APIs. See docs/design.md, "Bridge protocol".
package bridge

import (
	"context"
	"time"
)

// PlayerState is Music.app's transport state.
type PlayerState string

const (
	Playing PlayerState = "playing"
	Paused  PlayerState = "paused"
	Stopped PlayerState = "stopped"
)

// RepeatMode mirrors Music.app's song repeat.
type RepeatMode string

const (
	RepeatOff RepeatMode = "off"
	RepeatOne RepeatMode = "one"
	RepeatAll RepeatMode = "all"
)

// CurrentTrack is the subset of the playing track's properties encore needs.
type CurrentTrack struct {
	PID         string  `json:"pid"`
	Name        string  `json:"name"`
	Artist      string  `json:"artist"`
	Album       string  `json:"album"`
	AlbumArtist string  `json:"albumArtist"`
	Duration    float64 `json:"duration"` // seconds
	Favorited   bool    `json:"favorited"`
	Disliked    bool    `json:"disliked"`
}

// NowPlaying is a snapshot of the player.
type NowPlaying struct {
	State    PlayerState   `json:"state"`
	Position float64       `json:"position"` // seconds
	At       int64         `json:"at"`       // unix ms when Position was read
	Volume   int           `json:"volume"`
	Shuffle  bool          `json:"shuffle"`
	Repeat   RepeatMode    `json:"repeat"`
	Track    *CurrentTrack `json:"track"`
}

// AirPlayDevice is one AirPlay output known to Music.app.
type AirPlayDevice struct {
	ID        int    `json:"id"` // not stable: Music renumbers devices; match by PID
	PID       string `json:"pid"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // homepod, computer, speaker, tv, unknown
	Selected  bool   `json:"selected"`
	Active    bool   `json:"active"`
	Available bool   `json:"available"`
	Volume    int    `json:"volume"`
}

// SnapshotTrack is one library item as written by library.snapshot.
type SnapshotTrack struct {
	PID             string `json:"pid"`
	Title           string `json:"title"`
	Artist          string `json:"artist"`
	AlbumArtist     string `json:"albumArtist"`
	Album           string `json:"album"`
	AlbumPID        string `json:"albumPid"`
	Genre           string `json:"genre"`
	Composer        string `json:"composer"`
	Year            int    `json:"year"`
	Track           int    `json:"track"`
	TrackCount      int    `json:"trackCount"`
	Disc            int    `json:"disc"`
	DiscCount       int    `json:"discCount"`
	Duration        int64  `json:"duration"` // ms
	Added           int64  `json:"added"`    // unix seconds
	LastPlayed      int64  `json:"lastPlayed"`
	Plays           int    `json:"plays"`
	Rating          int    `json:"rating"`
	Favorited       bool   `json:"favorited"`
	Disliked        bool   `json:"disliked"`
	Explicit        bool   `json:"explicit"`
	Compilation     bool   `json:"compilation"`
	InLibrary       bool   `json:"inLibrary"`
	Available       bool   `json:"available"`
	Kind            string `json:"kind"` // subscription, uploaded, purchased, local
	Art             bool   `json:"art"`
	SortTitle       string `json:"sortTitle"`
	SortArtist      string `json:"sortArtist"`
	SortAlbum       string `json:"sortAlbum"`
	SortAlbumArtist string `json:"sortAlbumArtist"`
	// StoreID is the song's Apple Music catalog id (iTunesLibrary's private
	// storeItemID), 0 for local files, uploads and snapshots read through
	// Apple Events.
	StoreID int64 `json:"storeId,omitempty"`
}

// SnapshotPlaylist is one playlist as written by library.snapshot.
type SnapshotPlaylist struct {
	PID       string   `json:"pid"`
	ParentPID string   `json:"parentPid"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`    // user, smart, folder, subscription, genius
	Special   string   `json:"special"` // "", library, music, purchased, favorites, ...
	Items     []string `json:"items"`
	// GlobalID is the playlist's cloud id ("pl.…"), the id the Apple Music
	// catalog and Home use; "" for a playlist that has none.
	GlobalID string `json:"globalId,omitempty"`
}

// Snapshot is the whole library.
type Snapshot struct {
	GeneratedAt int64              `json:"generatedAt"`
	Tracks      []SnapshotTrack    `json:"tracks"`
	Playlists   []SnapshotPlaylist `json:"playlists"`
}

// Hello is the handshake result.
type Hello struct {
	Version      string `json:"version"`
	MusicRunning bool   `json:"musicRunning"`
	Automation   string `json:"automation"` // granted, denied, undetermined, pending (AutomationPending)
	// Library is the Media & Apple Music permission, which the bridge needs
	// to read the library and artwork directly: granted, denied, unknown.
	// Without it the bridge reads them from Music, slower.
	Library string `json:"library"`
}

// Artwork is a resized PNG written to disk by the bridge. Path is empty when the
// item has no artwork.
type Artwork struct {
	Path string `json:"path"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

// Event is something the bridge pushed without being asked.
type Event interface{ isEvent() }

// PlayerEvent carries a fresh NowPlaying.
type PlayerEvent struct{ NowPlaying NowPlaying }

// AirPlayEvent carries the device list after a change.
type AirPlayEvent struct{ Devices []AirPlayDevice }

// MusicEvent reports Music.app launching or quitting.
type MusicEvent struct{ Running bool }

// LibraryEvent means the library changed; take a new snapshot.
type LibraryEvent struct{}

// PermissionsEvent reports the permissions when the bridge starts waiting
// for a prompt (Automation "pending") and after the user answered it. The
// fields are those of Hello.
type PermissionsEvent struct{ Automation, Library string }

// ExitEvent means the bridge process died. The client restarts it; Err says why.
type ExitEvent struct{ Err error }

func (PlayerEvent) isEvent()      {}
func (AirPlayEvent) isEvent()     {}
func (MusicEvent) isEvent()       {}
func (LibraryEvent) isEvent()     {}
func (ExitEvent) isEvent()        {}
func (PermissionsEvent) isEvent() {}

// Error is an error returned by the bridge. Code is an Apple Event error code
// (negative) or a bridge code (1 bad request, 2 unknown method, 3 internal,
// 4 expired, 5 catalog request refused).
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Common Apple Event error codes.
const (
	ErrMusicNotRunning   = -600
	ErrNotFound          = -1728
	ErrAutomationDenied  = -1743
	ErrAutomationConsent = -1744
)

// ErrExpired is the bridge's code for a request it skipped because its
// deadline passed while it was queued. The client has stopped waiting by
// then, so callers normally see their own timeout instead.
const ErrExpired = 4

// ErrStore is the bridge's code for a catalog request (PlayStore or
// PlayStation) that MediaRemote refused or never answered. Message says why.
const ErrStore = 5

// Client is what the rest of encore uses. Both the real process client and the
// demo fake implement it. All methods are safe for concurrent use and must never
// be called from Bubble Tea's Update or View; call them inside tea.Cmds.
type Client interface {
	Hello(ctx context.Context) (Hello, error)
	Snapshot(ctx context.Context) (*Snapshot, error)
	State(ctx context.Context) (NowPlaying, error)

	PlayPause(ctx context.Context) error
	Play(ctx context.Context) error
	Pause(ctx context.Context) error
	Next(ctx context.Context) error
	Previous(ctx context.Context) error
	Seek(ctx context.Context, seconds float64) error
	SetVolume(ctx context.Context, volume int) error
	SetRepeat(ctx context.Context, mode RepeatMode) error
	SetShuffle(ctx context.Context, enabled bool) error
	// PlayTracks replaces the queue playlist with pids (in order) and plays the
	// first. containers maps pids that aren't in the library to a playlist pid
	// that contains them.
	PlayTracks(ctx context.Context, pids []string, containers map[string]string) error
	PlayPlaylist(ctx context.Context, pid string) error
	// PlayStore plays Apple Music catalog songs by store ID (the catalog song
	// id), ids[start:] in order, whether or not they are in the library. Music
	// starts streaming a few seconds after it returns and reports the songs
	// under temporary persistent IDs.
	PlayStore(ctx context.Context, ids []string, start int) error
	// PlayStation plays an Apple Music radio station by station ID ("ra.…"):
	// a personal, artist, genre or live station. Music picks every song, so
	// nothing upcoming can be read; Next moves on within the station and
	// Previous does whatever Music allows. A live station reports position 0
	// and duration 0.
	PlayStation(ctx context.Context, id string) error

	SetFavorite(ctx context.Context, pid string, value bool) error
	SetDislike(ctx context.Context, pid string, value bool) error
	Lyrics(ctx context.Context, pid string) (string, error)

	AirPlayDevices(ctx context.Context) ([]AirPlayDevice, error)
	SelectAirPlay(ctx context.Context, ids []int) error
	SetAirPlayVolume(ctx context.Context, id int, volume int) error

	// Artwork returns a square PNG of at most size px for a track or album pid,
	// cached on disk.
	Artwork(ctx context.Context, pid string, size int) (Artwork, error)
	// PlaylistArtwork returns a square PNG of at most size px of the cover
	// Music shows for a playlist (Apple's, the user's, or one Music made),
	// cached on disk. Path is empty when the playlist has none.
	PlaylistArtwork(ctx context.Context, pid string, size int) (Artwork, error)

	// Events delivers pushed events. The channel is closed by Close.
	Events() <-chan Event
	Close() error
}

// DefaultTimeout bounds ordinary calls. Snapshot and PlayTracks use longer ones.
const DefaultTimeout = 3 * time.Second

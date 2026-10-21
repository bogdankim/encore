package ui

import (
	"context"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/lastfm"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/lyrics"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
)

// Services are the long-lived objects an overlay may need beyond what Ctx
// carries every frame.
type Services struct {
	Client    bridge.Client
	Player    *player.Player
	Lyrics    *lyrics.Service   // nil when lyrics are disabled
	Offsets   *lyrics.Offsets   // per-track lyric offsets
	Scrobbler *lastfm.Scrobbler // nil until wired
	// LyricsLead highlights each synced line this long before its
	// timestamp (config lyrics.lead_ms).
	LyricsLead time.Duration
	// WordLead starts filling each timed word this long before its
	// timestamp (config lyrics.word_lead_ms).
	WordLead time.Duration
	// FakeLRC supplies synced lyrics in demo mode.
	FakeLRC func(pid string) string
	// Catalog searches and browses Apple Music; nil leaves out the Apple
	// Music search scope and pages.
	Catalog Catalog
	// Home is Apple Music's Home page; nil leaves Home with its empty state.
	Home HomeSource
	// OpenMusic shows an Apple Music item in Music.app (kind "playlist" and
	// its id); nil when encore can't.
	OpenMusic func(ctx context.Context, kind, id string) error
}

// overlayCtors are the overlays built in overlay_*.go. They register here
// from an init func; a nil constructor makes its action show "… isn't
// available yet".
var overlayCtors struct {
	// Search opens the search overlay on a scope.
	Search func(s Services, scope search.Scope) Overlay
	// Palette is the command palette (Host.Do runs the chosen action).
	Palette func(s Services) Overlay
	// Help is the searchable list of bindings (Ctx.Keys.Bindings).
	Help func(s Services) Overlay
	// Menu is the context menu for tracks, anchored at a screen cell.
	Menu func(s Services, screenX, screenY int, tracks []*library.Track) Overlay
	// AirPlay is the device picker.
	AirPlay func(s Services) Overlay
	// Lastfm is the last.fm setup.
	Lastfm func(s Services) Overlay
	// LyricsFull is full-screen lyrics.
	LyricsFull func(s Services) Overlay
}

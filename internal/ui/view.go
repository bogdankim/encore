package ui

import (
	"image"
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/theme"
)

// View is a page in the content area (Songs, an album, a playlist...). Views are
// values owned by the navigation stack; Render must return exactly w×h cells.
type View interface {
	// Key identifies the view for history and de-duplication ("songs",
	// "album:<key>", "playlist:<pid>").
	Key() string
	// Title is the view name and a dim meta line ("3,158 songs · 9 days").
	// Views draw it themselves (see titleLine) as part of Render.
	Title(c *Ctx) (title, meta string)
	// Action handles a keymap action while the content pane is focused.
	Action(a keymap.Action, n int, c *Ctx) (handled bool, cmd tea.Cmd)
	// Mouse handles a pointer event in view-local coordinates (0,0 is the top
	// left of the area passed to Render).
	Mouse(ev MouseEvent, c *Ctx) tea.Cmd
	// Render draws the view into exactly w×h cells.
	Render(w, h int, c *Ctx) string
	// Selected returns the tracks that queue/menu/favorite actions apply to.
	Selected(c *Ctx) []*library.Track
	// Reindex returns the view rebound to a new library (cursor kept by PID/key),
	// or nil when its subject no longer exists.
	Reindex(lib *library.Library) View
}

// Overlay is a floating panel above everything else (search, palette, help,
// context menu, pickers). The top overlay receives input first.
type Overlay interface {
	// Rect is where the overlay draws, given the screen size.
	Rect(screenW, screenH int) image.Rectangle
	// Render draws exactly the Rect's size.
	Render(c *Ctx) string
	// CapturesInput reports whether a text field has focus; then keys are fed
	// through keymap.Input and unconsumed keys go to Key.
	CapturesInput() bool
	// Action handles a keymap action. Return handled=false to let it fall
	// through to the app (only global actions should).
	Action(a keymap.Action, n int, c *Ctx) (handled bool, cmd tea.Cmd)
	// Key receives raw key presses that the keymap didn't consume (text input).
	Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd
	// Mouse handles pointer events inside Rect, in overlay-local coordinates.
	Mouse(ev MouseEvent, c *Ctx) tea.Cmd
	// Update receives every non-input message (async results such as search
	// hits), so overlays can own their own commands.
	Update(msg tea.Msg, c *Ctx) tea.Cmd
}

// MouseKind is the kind of pointer event.
type MouseKind uint8

const (
	MousePress MouseKind = iota
	MouseRelease
	MouseMotion // motion with or without a button held
	MouseWheel
)

// MouseEvent is a normalised pointer event in local coordinates.
type MouseEvent struct {
	Kind   MouseKind
	Button tea.MouseButton // MouseLeft, MouseRight, MouseWheelUp, MouseWheelDown, MouseNone...
	X, Y   int
	Double bool // second press within 350 ms on the same cell
	Mod    tea.KeyMod
	// ScreenX/ScreenY are absolute, for opening menus at the pointer.
	ScreenX, ScreenY int
}

// ArtMode chooses how artwork is drawn.
type ArtMode uint8

const (
	// ArtTile is for grids and small thumbnails: half blocks, always diff-safe.
	ArtTile ArtMode = iota
	// ArtHero is for the player bar and page headers: the best protocol the
	// terminal supports (iTerm2 images on WezTerm).
	ArtHero
)

// Host is the app, as seen by views and overlays. Every method returns a
// command (possibly nil) and never blocks.
type Host interface {
	// Play starts tracks[i] within the context of tracks.
	Play(src player.Source, tracks []*library.Track, i int) tea.Cmd
	PlayNext(tracks []*library.Track) tea.Cmd
	AddToQueue(tracks []*library.Track) tea.Cmd
	ToggleFavorite(tracks []*library.Track) tea.Cmd
	// OpenInMusic shows an Apple Music item in Music.app (kind "album",
	// "playlist" or "song" and its id; name is for the footer).
	OpenInMusic(kind, id, name string) tea.Cmd

	Open(v View) tea.Cmd
	OpenAlbum(a *library.Album) tea.Cmd
	OpenArtist(a *library.Artist) tea.Cmd
	OpenPlaylist(p *library.Playlist) tea.Cmd
	// OpenIn opens the album or the artist of song, album or artist s in
	// Apple Music or in the library, as act says
	// (keymap.OpenAlbumInCatalog and the like).
	OpenIn(act keymap.Action, s Subject) tea.Cmd

	PushOverlay(o Overlay) tea.Cmd
	CloseOverlay() tea.Cmd
	// Menu opens the context menu for tracks at a screen position.
	Menu(screenX, screenY int, tracks []*library.Track) tea.Cmd
	// Status shows a short message in the footer for a few seconds.
	Status(msg string) tea.Cmd
	// Do runs any keymap action as if it had been pressed (used by the palette
	// and by clickable controls).
	Do(a keymap.Action) tea.Cmd

	// Art returns a w×h block for artwork, identified by key (album key or
	// track pid) and the pid to fetch it with. While the image loads it
	// returns a neutral placeholder and schedules the load; the app re-renders
	// when it arrives.
	Art(key, pid string, w, h int, mode ArtMode) string
}

// Ctx is passed to views and overlays on every call. It is rebuilt cheaply per
// frame and must not be retained.
type Ctx struct {
	Theme   *theme.Theme
	Lib     *library.Library
	Index   *search.Index
	Keys    *keymap.Engine
	Player  player.Snapshot
	Focused bool // the receiving pane has keyboard focus
	Now     time.Time
	Host    Host
	// Catalog is the Apple Music catalog, or nil when it is off.
	Catalog Catalog
	// Home is Apple Music's Home page, or nil.
	Home HomeSource

	// Origin is the screen position of the receiving pane's top left cell,
	// for opening menus at a row.
	Origin image.Point
	// LibraryLoading is set until the first library snapshot arrives, so empty
	// states can say "Loading" instead of "Nothing here".
	LibraryLoading bool

	// Back is set for a detail page (album, artist, playlist) the user can
	// go back from: draw BackMark before its title.
	Back bool

	// TrueColor is set when the terminal takes 24-bit colour, so a colour
	// can change smoothly across a cell; TermFG is the terminal's
	// foreground colour, nil until it reports it.
	TrueColor bool
	TermFG    color.Color

	fav       map[string]bool // favorite changes not yet in Lib
	art       artSink         // where HeroArt reports placements
	hint      func(string)    // see Hint
	backAt    func(x, y int)  // see BackMark
	backHover bool
}

// PlayingPID is the pid of the track Music is playing, or "".
func (c *Ctx) PlayingPID() string {
	if t := c.Player.NowPlaying.Track; t != nil {
		return t.PID
	}
	return ""
}

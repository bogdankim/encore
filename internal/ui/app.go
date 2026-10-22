package ui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/lastfm"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/theme"
)

// Options wire the UI to the rest of encore.
type Options struct {
	Client bridge.Client
	Player *player.Player
	// Library may be empty while the first snapshot loads; set Loading then.
	Library *library.Library
	Loading bool
	// Index is built in the background when nil.
	Index *search.Index
	Keys  *keymap.Engine
	// Theme builds the theme for a dark or light terminal background. It is
	// called with dark=true at start and again once the terminal reports its
	// background.
	Theme        func(dark bool) *theme.Theme
	SidebarWidth int
	NoMouse      bool
	// Status is shown in the footer at start (config warnings).
	Status string
	// RefreshLibrary asks for a new library snapshot (debounced by the
	// caller); used after favorites change. May be nil.
	RefreshLibrary func()
	// Services the panes and overlays use beyond the client and player
	// (lyrics, offsets, scrobbler, demo lyrics). Client and Player are
	// filled in from the fields above.
	Services Services
	// Art draws album art; nil shows placeholders. Run the program with
	// tea.WithOutput(art.NewOutput(os.Stdout, Art)).
	Art *art.Renderer
	// ArtFetch loads Apple Music artwork by URL; nil fetches over HTTP.
	// ArtCacheDir keeps it on disk (~/Library/Caches/encore/art); "" keeps
	// it in memory only.
	ArtFetch    ArtFetch
	ArtCacheDir string
	// HomePage is Apple Music's Home page read before start, or nil.
	HomePage *home.Page
	// Start is the section encore opens on (KeyRecent and the like); ""
	// is Recently Added. Home without shelves falls back to Recently Added.
	Start string
}

// LibraryMsg delivers a refreshed library; send it with Program.Send.
type LibraryMsg struct {
	Lib   *library.Library
	Index *search.Index // nil: the UI builds it
}

// BridgeMsg forwards a bridge event: AirPlay changes, Music launching or
// quitting, bridge restarts. Player events go to Player.Observe instead.
type BridgeMsg struct{ Event bridge.Event }

// StatusMsg shows a line in the footer for a few seconds.
type StatusMsg struct {
	Text string
	Err  bool
}

type (
	playerMsg        struct{}
	tickMsg          struct{ gen int }
	statusExpiredMsg struct{ gen int }
	helloMsg         struct {
		hello bridge.Hello
		err   error
	}
	airplayMsg struct{ devs []bridge.AirPlayDevice }
	lastfmMsg  struct{ st lastfm.Status }
	indexMsg   struct {
		lib *library.Library
		idx *search.Index
	}
	favoriteMsg struct {
		pids []string
		was  bool
		err  error
	}
)

type focusArea uint8

const (
	focusSidebar focusArea = iota
	focusContent
	focusRight
)

const (
	tickEvery   = time.Second / 8
	frameEvery  = time.Second / 30 // while lyric words fill
	statusFor   = 3 * time.Second
	automationM = "Allow encore to control Music: Privacy & Security → Automation"
	waitingM    = "Waiting for permission to control Music…"
	libraryM    = "Reading the library through Music (slower). Allow encore: Privacy & Security → Media & Apple Music"
)

// App is the root Bubble Tea model.
type App struct {
	o      Options
	client bridge.Client
	svc    Services
	player *player.Player
	keys   *keymap.Engine
	theme  *theme.Theme

	lib        *library.Library
	idx        *search.Index
	libLoading bool
	fav        map[string]bool

	snap      player.Snapshot
	lastErrAt time.Time
	now       time.Time

	w, h      int
	lay       Layout
	sidebarOn bool
	sidebarW  int
	rightW    int
	focus     focusArea

	nav      history
	sections map[string]View
	sidebar  *sidebar

	right      rightKind
	fullLyrics bool
	panes      map[rightKind]RightPane

	trueColor bool        // the terminal takes 24-bit colour
	termFG    color.Color // the terminal's foreground, once it reports it

	overlays []Overlay
	pending  keymap.Result

	art         *artStore
	artR        *art.Renderer
	cell        art.CellSize
	frameArt    []artPlace // hero art drawn in the frame being built
	frameCovers []artCover // overlays drawn over it
	// nowArt is what the now-playing art shows, by block size (see
	// Ctx.NowPlayingArt).
	nowArt map[image.Point]*nowArt
	// outside is the art found for songs from outside the library (see
	// App.playingArt).
	outside outsideArt
	// lastPlaced is the hero art placed in the last frame, for the art log.
	lastPlaced string

	bar       barState
	barLayout barLayout
	airplay   []bridge.AirPlayDevice

	clicks clickTracker
	drag   region
	hover  region
	hint   string // the hovered control's name, see Ctx.Hint
	// back is the screen cell of the open page's back glyph (Ctx.BackMark)
	// in the last frame; backOn is false when it had none.
	back              image.Point
	backOn, backHover bool
	overlayDrag       bool
	wkRows            []whichKeyRow
	// swallow drops the second press of a double-click whose first press
	// changed the view or opened or closed an overlay, and swallowRelease
	// that press's release (see swallowed).
	swallow, swallowRelease bool

	ticking bool
	tickGen int
	blurred bool

	status struct {
		text   string
		err    bool
		gen    int
		sticky string
	}
}

var _ Host = (*App)(nil)

// New builds the app. The library, player, client, keys and theme are
// required.
func New(o Options) *App {
	a := &App{
		o:          o,
		client:     o.Client,
		svc:        o.Services,
		player:     o.Player,
		keys:       o.Keys,
		theme:      o.Theme(true),
		lib:        o.Library,
		idx:        o.Index,
		fav:        map[string]bool{},
		sidebarOn:  true,
		sidebarW:   o.SidebarWidth,
		focus:      focusContent,
		sections:   map[string]View{},
		panes:      map[rightKind]RightPane{},
		libLoading: o.Loading,
		now:        time.Now(),
		art:        newArtStore(o.Client, o.Art),
		artR:       o.Art,
		cell:       art.DefaultCellSize,
	}
	a.art.urls = urlArt{fetch: o.ArtFetch, dir: o.ArtCacheDir}
	if a.lib == nil {
		a.lib = library.Build(&bridge.Snapshot{})
	}
	if a.keys == nil {
		a.keys = keymap.New()
	}
	a.sidebar = newSidebar(a.lib)
	start := o.Start
	if start == KeyHome && (o.HomePage == nil || len(o.HomePage.Shelves)+len(o.HomePage.New) == 0) {
		start = ""
	}
	if start == "" {
		start = KeyRecent
	}
	if a.section(start) == nil {
		start = KeySongs
	}
	a.nav.open(a.section(start))
	a.sidebar.syncTo(start)
	a.status.text = o.Status
	a.snap = a.player.State()
	return a
}

// Init starts the player subscription and the first bridge reads.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.waitPlayer(), a.art.wait(), tea.RequestBackgroundColor, tea.RequestForegroundColor, a.fetchState(), a.fetchAirPlay(), a.hello(), a.initView()}
	if a.artR != nil {
		cmds = append(cmds, tea.Raw(art.CellSizeQuery))
	}
	if sc := a.svc.Scrobbler; sc != nil {
		a.bar.lastfmIssue = lastfmIssue(sc.Status())
		cmds = append(cmds, a.waitLastfm())
	}
	if a.idx == nil {
		cmds = append(cmds, buildIndex(a.lib))
	}
	if a.status.text != "" {
		cmds = append(cmds, a.expireStatus())
	}
	return tea.Batch(cmds...)
}

func (a *App) waitPlayer() tea.Cmd {
	ch := a.player.Changes()
	return func() tea.Msg {
		<-ch
		return playerMsg{}
	}
}

// waitLastfm delivers the scrobbler's next status.
func (a *App) waitLastfm() tea.Cmd {
	ch := a.svc.Scrobbler.Updates()
	return func() tea.Msg {
		st, ok := <-ch
		if !ok {
			return nil
		}
		return lastfmMsg{st}
	}
}

// lastfmIssue reports whether scrobbling needs the user: the session or the
// API key was rejected, or scrobbles are piling up while offline.
func lastfmIssue(st lastfm.Status) bool {
	switch st.State {
	case lastfm.StateReauth, lastfm.StateConfig:
		return true
	case lastfm.StateOffline:
		return st.Queued > 0
	}
	return false
}

func (a *App) fetchState() tea.Cmd {
	client, pl := a.client, a.player
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), bridge.DefaultTimeout)
		defer cancel()
		np, err := client.State(ctx)
		if err != nil {
			return StatusMsg{Text: humanize(err), Err: true}
		}
		pl.Observe(bridge.PlayerEvent{NowPlaying: np})
		return nil
	}
}

func (a *App) fetchAirPlay() tea.Cmd {
	client := a.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), bridge.DefaultTimeout)
		defer cancel()
		devs, err := client.AirPlayDevices(ctx)
		if err != nil {
			return nil
		}
		return airplayMsg{devs}
	}
}

func (a *App) hello() tea.Cmd {
	client := a.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), bridge.DefaultTimeout)
		defer cancel()
		h, err := client.Hello(ctx)
		return helloMsg{h, err}
	}
}

func buildIndex(lib *library.Library) tea.Cmd {
	return func() tea.Msg { return indexMsg{lib, search.NewIndex(lib)} }
}

// Update routes messages; see docs/ui.md, "Root model".
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	view := a.viewKey()
	m, cmd := a.update(msg)
	if a.viewKey() != view {
		// a hover hint names a control of the view that was just replaced
		a.hint, a.backHover = "", false
	}
	if _, ok := msg.(tickMsg); !ok && !a.ticking {
		// opening lyrics while the terminal is in the background starts it
		cmd = tea.Batch(cmd, a.ensureTick())
	}
	return m, cmd
}

// viewKey is the open view's key, or "" with none.
func (a *App) viewKey() string {
	if a.nav.cur == nil {
		return ""
	}
	return a.nav.cur.Key()
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a.now = time.Now()
	if cs, ok := art.CellSizeFromMsg(msg); ok {
		if a.artR != nil && cs != a.cell {
			a.cell = cs
			a.artR.SetCellSize(cs)
			a.art.cellSizeChanged()
		}
		return a, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		a.relayout()
		a.settleQueueDrawer()
		return a, tea.Batch(a.artResize(msg.Width, msg.Height), a.toOverlays(msg))
	case artMsg:
		return a, a.art.wait()
	case lastfmMsg:
		a.bar.lastfmIssue = lastfmIssue(msg.st)
		return a, tea.Batch(a.waitLastfm(), a.toOverlays(msg))
	case tea.BackgroundColorMsg:
		if t := a.o.Theme(msg.IsDark()); t != nil {
			a.theme = t
		}
		return a, a.toOverlays(msg)
	case tea.ColorProfileMsg:
		a.trueColor = msg.Profile == colorprofile.TrueColor
		return a, nil
	case tea.ForegroundColorMsg:
		a.termFG = msg.Color
		return a, nil
	case tea.FocusMsg:
		a.blurred = false
		a.refreshSnap()
		return a, tea.Batch(a.ensureTick(), a.toOverlays(msg), a.redrawFragileArt())
	case artRedrawMsg:
		a.artR.TmuxRedrawn()
		return a, nil
	case tea.BlurMsg:
		a.blurred = true
		return a, a.toOverlays(msg)
	case tea.KeyPressMsg:
		a.clearStatus()
		return a, a.handleKey(msg)
	case tea.PasteMsg:
		return a, a.handlePaste(msg)
	case tea.MouseMsg:
		ev, ok := a.clicks.normalize(a.now, msg)
		if !ok || a.swallowed(ev) {
			return a, nil
		}
		onBack := a.onBackMark(msg.Mouse())
		switch m := msg.(type) {
		case tea.MouseMotionMsg:
			a.hint = "" // the control under the pointer sets it again
			a.backHover = onBack
		case tea.MouseClickMsg:
			a.clearStatus()
			if cmd, ok := a.historyButton(m.Mouse()); ok {
				return a, cmd
			}
			if onBack && m.Button == tea.MouseLeft {
				a.backHover = false
				return a, a.global(keymap.HistoryBack, 1)
			}
		}
		ovs := len(a.overlays)
		cmd := a.handleMouse(ev)
		if len(a.overlays) != ovs {
			a.hint = ""
		}
		if a.backHover {
			a.setHint(a.hintText("Back", keymap.HistoryBack))
		}
		return a, cmd
	case keymap.TimeoutMsg:
		r := a.keys.HandleTimeout(msg)
		a.pending = keymap.Result{}
		if r.Action != "" {
			return a, a.dispatch(r.Action, r.Count)
		}
		return a, nil
	case playerMsg:
		return a, tea.Batch(a.waitPlayer(), a.onPlayerChange())
	case tickMsg:
		return a, a.onTick(msg)
	case LibraryMsg:
		a.art.forget()
		return a, a.setLibrary(msg.Lib, msg.Index)
	case indexMsg:
		if msg.lib == a.lib {
			a.idx = msg.idx
		}
		return a, a.broadcast(msg)
	case helloMsg:
		return a, tea.Batch(a.onHello(msg), a.toOverlays(msg))
	case airplayMsg:
		a.setAirPlay(msg.devs)
		return a, a.broadcast(msg)
	case BridgeMsg:
		var fwd tea.Msg = msg
		if ev, ok := msg.Event.(bridge.AirPlayEvent); ok {
			fwd = ev // the AirPlay picker takes the event itself
		}
		return a, tea.Batch(a.onBridge(msg.Event), a.broadcast(fwd))
	case StatusMsg:
		return a, tea.Batch(a.setStatus(msg.Text, msg.Err), a.toOverlays(msg))
	case statusExpiredMsg:
		if msg.gen == a.status.gen {
			a.status.text = ""
		}
		return a, nil
	case favoriteMsg:
		return a, tea.Batch(a.onFavorite(msg), a.toOverlays(msg))
	case catalogResultMsg:
		return a, msg.apply(a.viewCtx())
	}
	return a, a.broadcast(msg)
}

// toOverlays hands a message the app handled itself to the open overlays.
func (a *App) toOverlays(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, o := range a.overlays {
		cmds = append(cmds, o.Update(msg, a.overlayCtx(o)))
	}
	return tea.Batch(cmds...)
}

// broadcast hands a message to every overlay and pane that wants it.
func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	cmds := []tea.Cmd{a.toOverlays(msg)}
	for k, p := range a.panes {
		if u, ok := p.(interface {
			Update(tea.Msg, *Ctx) tea.Cmd
		}); ok {
			c := a.ctxFor(focusRight, a.lay.RightBody.Min)
			c.Focused = c.Focused && a.right == k
			cmds = append(cmds, u.Update(msg, c))
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) relayout() {
	a.lay = computeLayout(a.w, a.h, layoutOpts{
		sidebar:   a.sidebarOn,
		sidebarW:  a.sidebarW,
		right:     a.right != rightNone,
		rightW:    a.rightW,
		fullRight: a.fullLyrics,
	})
	switch {
	case a.focus == focusSidebar && !a.lay.SidebarOn,
		a.focus == focusRight && !a.lay.RightOn:
		a.focus = focusContent
	case a.focus == focusContent && !a.lay.ContentOn && a.lay.RightOn:
		a.focus = focusRight
	}
}

// settleQueueDrawer turns the Up Next drawer into the pane once the window
// is wide enough for it.
func (a *App) settleQueueDrawer() {
	if _, ok := a.topOverlay().(*queueDrawer); !ok || !a.paneFits() {
		return
	}
	a.CloseOverlay()
	a.right = rightQueue
	a.fullLyrics = false
	a.relayout()
}

func (a *App) refreshSnap() {
	a.snap = a.player.State()
	switch {
	case a.snap.Loading && a.bar.loadingSince.IsZero():
		a.bar.loadingSince = a.now
	case !a.snap.Loading:
		a.bar.loadingSince = time.Time{}
	}
}

func (a *App) onPlayerChange() tea.Cmd {
	a.refreshSnap()
	var cmd tea.Cmd
	if a.snap.LastError != "" && a.snap.ErrorAt.After(a.lastErrAt) {
		a.lastErrAt = a.snap.ErrorAt
		cmd = a.setStatus(a.snap.LastError, true)
	}
	return tea.Batch(cmd, a.ensureTick(), a.broadcast(playerMsg{}))
}

func (a *App) wantTick() bool {
	if a.blurred && !a.lyricsShown() {
		return false // nothing moves on screen that anyone watches
	}
	return a.snap.NowPlaying.State == bridge.Playing || a.snap.Loading
}

// lyricsShown reports whether lyrics are on screen: they keep moving while
// the terminal is in the background, where people often follow them.
func (a *App) lyricsShown() bool {
	if a.paneShown(rightLyrics) {
		return true
	}
	for _, o := range a.overlays {
		if _, ok := o.(*lyricsFull); ok {
			return true
		}
	}
	return false
}

func (a *App) ensureTick() tea.Cmd {
	if a.ticking || !a.wantTick() {
		return nil
	}
	a.ticking = true
	a.tickGen++
	return tick(a.tickGen, a.tickRate())
}

func tick(gen int, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{gen} })
}

// framer is a pane or overlay that sometimes wants frames faster than the
// player tick: lyrics while a word-timed line fills.
type framer interface{ wantsFrames(c *Ctx) bool }

// tickRate is how often the playing tick comes: frameEvery while lyrics on
// screen fill word by word, else tickEvery.
func (a *App) tickRate() time.Duration {
	for _, o := range a.overlays {
		if f, ok := o.(framer); ok && f.wantsFrames(a.overlayCtx(o)) {
			return frameEvery
		}
	}
	if a.paneShown(rightLyrics) {
		if f, ok := a.panes[rightLyrics].(framer); ok && f.wantsFrames(a.ctxFor(focusRight, a.lay.RightBody.Min)) {
			return frameEvery
		}
	}
	return tickEvery
}

func (a *App) onTick(msg tickMsg) tea.Cmd {
	if msg.gen != a.tickGen {
		return nil
	}
	a.refreshSnap()
	if !a.wantTick() {
		a.ticking = false
		return nil
	}
	cmd := a.broadcast(msg)
	return tea.Batch(tick(a.tickGen, a.tickRate()), cmd)
}

func (a *App) onHello(msg helloMsg) tea.Cmd {
	if msg.err != nil {
		return a.setStatus(humanize(msg.err), true)
	}
	a.setPermissions(msg.hello.Automation, msg.hello.Library)
	return nil
}

// setPermissions keeps a missing or pending permission in the footer,
// Automation first: without it nothing works.
func (a *App) setPermissions(automation, library string) {
	switch {
	case automation == "denied":
		a.status.sticky = automationM
	case automation == bridge.AutomationPending:
		a.status.sticky = waitingM
	case library == "denied":
		a.status.sticky = libraryM
	case a.status.sticky == automationM || a.status.sticky == libraryM || a.status.sticky == waitingM:
		a.status.sticky = ""
	}
}

func (a *App) onBridge(ev bridge.Event) tea.Cmd {
	switch ev := ev.(type) {
	case bridge.AirPlayEvent:
		a.setAirPlay(ev.Devices)
	case bridge.AirPlayRepairedEvent:
		return a.setStatus(reconnectedText(ev), !ev.OK)
	case bridge.MusicEvent:
		if ev.Running {
			return tea.Batch(a.fetchState(), a.fetchAirPlay())
		}
	case bridge.PermissionsEvent:
		a.setPermissions(ev.Automation, ev.Library)
	case bridge.ExitEvent:
		return a.setStatus("The Music helper stopped; restarting it", true)
	}
	return nil
}

// reconnectedText is the footer line after the bridge reconnected AirPlay
// because Music had fallen back to the Mac's speakers.
func reconnectedText(ev bridge.AirPlayRepairedEvent) string {
	names := "AirPlay"
	if n := len(ev.Devices); n == 1 {
		names = ev.Devices[0]
	} else if n > 1 {
		names = strings.Join(ev.Devices[:n-1], ", ") + " and " + ev.Devices[n-1]
	}
	if ev.OK {
		return "Reconnected to " + names
	}
	return "Couldn’t reconnect to " + names
}

func (a *App) setAirPlay(devs []bridge.AirPlayDevice) {
	a.airplay = devs
	a.bar.air, a.bar.airRemote = airPlayLabel(devs)
}

func (a *App) setLibrary(lib *library.Library, idx *search.Index) tea.Cmd {
	if lib == nil {
		return nil
	}
	a.lib, a.idx, a.libLoading = lib, idx, false
	search.ResetFilters() // its corpora point into the old library
	for pid, v := range a.fav {
		if t := lib.Track(pid); t == nil || t.Favorited == v {
			delete(a.fav, pid)
		}
	}
	for k, v := range a.sections {
		if nv := v.Reindex(lib); nv != nil {
			a.sections[k] = nv
		} else {
			delete(a.sections, k)
		}
	}
	a.nav.reindex(lib)
	if a.nav.cur == nil {
		a.nav.open(a.section(KeySongs))
	}
	a.sidebar.setLibrary(lib)
	a.refreshSnap()
	var cmds []tea.Cmd
	if idx == nil {
		cmds = append(cmds, buildIndex(lib))
	}
	cmds = append(cmds, a.broadcast(LibraryMsg{Lib: lib, Index: idx}))
	return tea.Batch(cmds...)
}

// section returns the view for a library section, reusing the open one so
// its scroll position and sort survive.
func (a *App) section(key string) View {
	if v, ok := a.sections[key]; ok {
		return v
	}
	var v View
	switch key {
	case KeySongs:
		v = newSongsView(a.lib)
	case KeyFavorites:
		v = newFavoritesView(a.lib)
	case KeyHome:
		v = NewHomeView(a.o.HomePage)
	case KeyRecent:
		if viewCtors.RecentlyAdded != nil {
			v = viewCtors.RecentlyAdded(a.lib)
		}
	case KeyArtists:
		if viewCtors.Artists != nil {
			v = viewCtors.Artists(a.lib)
		}
	case KeyAlbums:
		if viewCtors.Albums != nil {
			v = viewCtors.Albums(a.lib)
		}
	}
	if v != nil {
		a.sections[key] = v
	}
	return v
}

func (a *App) setStatus(text string, isErr bool) tea.Cmd {
	a.status.text, a.status.err = text, isErr
	a.hint = ""
	return a.expireStatus()
}

// clearStatus drops the status line: the next key or click replaces it.
func (a *App) clearStatus() {
	if a.status.text != "" {
		a.status.text = ""
		a.status.gen++
	}
}

// historyButton maps the mouse's back and forward buttons to history.
func (a *App) historyButton(m tea.Mouse) (tea.Cmd, bool) {
	var act keymap.Action
	switch m.Button {
	case tea.MouseBackward:
		act = keymap.HistoryBack
	case tea.MouseForward:
		act = keymap.HistoryForward
	default:
		return nil, false
	}
	if len(a.overlays) > 0 {
		if act == keymap.HistoryBack {
			return a.CloseOverlay(), true
		}
		return nil, true
	}
	return a.global(act, 1), true
}

func (a *App) expireStatus() tea.Cmd {
	a.status.gen++
	gen := a.status.gen
	return tea.Tick(statusFor, func(time.Time) tea.Msg { return statusExpiredMsg{gen} })
}

func (a *App) setFocus(f focusArea) {
	switch {
	case f == focusSidebar && !a.lay.SidebarOn && a.lay.W > 0:
		return
	case f == focusRight && !a.lay.RightOn && a.lay.W > 0:
		return
	}
	if f == focusSidebar && a.focus != focusSidebar && a.nav.cur != nil {
		a.sidebar.syncTo(a.nav.cur.Key())
	}
	a.focus = f
}

func (a *App) topOverlay() Overlay {
	if len(a.overlays) == 0 {
		return nil
	}
	return a.overlays[len(a.overlays)-1]
}

func (a *App) baseCtx() *Ctx {
	return &Ctx{
		Theme: a.theme, Lib: a.lib, Index: a.idx, Keys: a.keys,
		Player: a.snap, Now: a.now, Host: a, Catalog: a.svc.Catalog, Home: a.svc.Home,
		LibraryLoading: a.libLoading, fav: a.fav, hint: a.setHint,
		TrueColor: a.trueColor, TermFG: a.termFG,
		art: artSink{place: a.placeArt, clip: image.Rect(0, 0, a.w, a.h)},
	}
}

func (a *App) ctxFor(f focusArea, origin image.Point) *Ctx {
	c := a.baseCtx()
	c.Focused = a.focus == f
	c.Origin = origin
	switch f {
	case focusSidebar:
		c.art.clip = a.lay.SidebarBody
	case focusContent:
		c.art.clip = a.lay.ContentBody
	case focusRight:
		c.art.clip = a.lay.RightBody
	}
	return c
}

func (a *App) overlayCtx(o Overlay) *Ctx {
	c := a.baseCtx()
	r := o.Rect(a.w, a.h)
	c.Origin = r.Min
	c.Focused = o == a.topOverlay()
	c.art.clip = r
	for i, ov := range a.overlays {
		if ov == o {
			c.art.layer = i + 1
		}
	}
	return c
}

func (a *App) viewCtx() *Ctx {
	c := a.ctxFor(focusContent, a.lay.ContentBody.Min)
	if a.nav.cur != nil && isDetail(a.nav.cur.Key()) && len(a.nav.back) > 0 {
		c.Back = true
		c.backHover = a.backHover
		origin := c.Origin
		c.backAt = func(x, y int) {
			a.back, a.backOn = origin.Add(image.Pt(x, y)), true
		}
	}
	return c
}

// onBackMark reports whether the pointer is on the open page's back glyph
// (or the cell either side of it), with nothing open on top.
func (a *App) onBackMark(m tea.Mouse) bool {
	if !a.backOn || len(a.overlays) > 0 || a.pending.Pending || a.drag != regionNone {
		return false
	}
	return m.Y == a.back.Y && m.X >= a.back.X-1 && m.X <= a.back.X+1
}

func (a *App) pane() RightPane { return a.paneOf(a.right) }

// paneOf returns the pane of kind k, building it the first time.
func (a *App) paneOf(k rightKind) RightPane {
	if k == rightNone {
		return nil
	}
	if p, ok := a.panes[k]; ok {
		return p
	}
	var p RightPane
	s := a.services()
	switch k {
	case rightLyrics:
		if paneCtors.Lyrics != nil {
			p = paneCtors.Lyrics(s)
		} else {
			p = placeholderPane{title: "Lyrics", note: "Lyrics aren't available yet", ctx: keymap.Lyrics}
		}
	case rightQueue:
		if paneCtors.Queue != nil {
			p = paneCtors.Queue(s)
		} else {
			p = placeholderPane{title: "Up Next", note: "Up Next isn't available yet", ctx: keymap.Queue}
		}
	}
	a.panes[k] = p
	return p
}

func (a *App) services() Services {
	s := a.svc
	s.Client, s.Player = a.client, a.player
	return s
}

// humanize turns a bridge error into one line for the status.
func humanize(err error) string {
	var be *bridge.Error
	if errors.As(err, &be) {
		switch be.Code {
		case bridge.ErrAutomationDenied:
			return automationM
		case bridge.ErrAutomationConsent:
			return waitingM
		case bridge.ErrMusicNotRunning:
			return "Music isn't running"
		}
		if be.Message != "" {
			return be.Message
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Music didn't respond in time"
	}
	return strings.TrimPrefix(err.Error(), "bridge: ")
}

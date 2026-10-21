// Command preview-overlays shows encore's overlays over a dim fake screen with
// the demo library, for screenshots and hands-on checks without the app shell.
//
//	go run ./cmd/preview-overlays -overlay search|palette|help|menu|airplay|lastfm [-scope songs]
//
// Host calls are recorded in the bottom line. With every overlay closed, s, p,
// h, m, a and l reopen one; q quits. The AirPlay picker runs against the demo
// bridge; last.fm setup runs against a local stand-in for the API that
// rejects keys starting with "bad" and approves the second poll as "demo".
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/lastfm"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/theme"
	"github.com/bogdankim/encore/internal/ui"
)

func main() {
	which := flag.String("overlay", "search", "search, palette, help, menu, airplay or lastfm")
	scope := flag.String("scope", "all", "search scope: all, songs, albums, artists, playlists")
	flag.Parse()

	fake := bridge.NewFakeWithClock(1, time.Now)
	snap, err := fake.Snapshot(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lib := library.Build(snap)
	m := &model{
		theme: theme.New(theme.Lookup("encore", true), theme.GlyphsFor("auto", os.Getenv("TERM_PROGRAM")), os.Getenv("NO_COLOR") != ""),
		lib:   lib,
		idx:   search.NewIndex(lib),
		keys:  keymap.New(),
		w:     140, h: 42,
	}
	m.host = &host{m: m}
	m.client = bridge.NewFake(1)
	defer m.client.Close()
	api := httptest.NewServer(fakeLastfm())
	defer api.Close()
	dir, err := os.MkdirTemp("", "encore-preview")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	m.scrobbler = lastfm.NewScrobbler(lastfm.Credentials{}, filepath.Join(dir, "queue.json"), nil, lastfm.WithAPIRoot(api.URL+"/2.0/"))
	defer m.scrobbler.Close()
	var p *tea.Program
	keychain := &store{}
	m.lastfmEnv = ui.LastfmEnv{
		Auth:   &lastfm.Auth{Root: api.URL + "/2.0/", Interval: 1500 * time.Millisecond},
		Load:   keychain.load,
		Save:   keychain.save,
		Delete: keychain.delete,
		Open: func(url string) error {
			p.Send(openedMsg(url))
			return nil
		},
		Getenv: func(string) string { return "" },
	}
	m.open(*which, parseScope(*scope))
	p = tea.NewProgram(m)
	go func() {
		for ev := range m.client.Events() {
			if ev, ok := ev.(bridge.AirPlayEvent); ok {
				p.Send(ev)
			}
		}
	}()
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseScope(s string) search.Scope {
	switch s {
	case "songs":
		return search.Songs
	case "albums":
		return search.Albums
	case "artists":
		return search.Artists
	case "playlists":
		return search.Playlists
	}
	return search.All
}

type model struct {
	theme    *theme.Theme
	lib      *library.Library
	idx      *search.Index
	keys     *keymap.Engine
	host     *host
	overlays []ui.Overlay
	rects    []image.Rectangle
	w, h     int
	status   string

	client    *bridge.Fake
	scrobbler *lastfm.Scrobbler
	lastfmEnv ui.LastfmEnv
	initCmd   tea.Cmd

	lastPress    time.Time
	lastX, lastY int
	lastButton   tea.MouseButton
}

func (m *model) open(which string, scope search.Scope) {
	switch which {
	case "palette":
		m.overlays = append(m.overlays, ui.NewPalette())
	case "help":
		m.overlays = append(m.overlays, ui.NewHelp())
	case "menu":
		album := m.lib.RecentlyAdded[0]
		m.overlays = append(m.overlays, ui.NewMenu(m.w/3, 8, album.Tracks[:1]))
	case "airplay":
		m.overlays = append(m.overlays, ui.NewAirPlay(ui.Services{Client: m.client}))
	case "lastfm":
		m.overlays = append(m.overlays, ui.NewLastfmSetupEnv(m.scrobbler, m.lastfmEnv))
	default:
		m.overlays = append(m.overlays, ui.NewSearch(scope))
	}
	if o, ok := m.top().(interface{ Init(*ui.Ctx) tea.Cmd }); ok {
		m.initCmd = o.Init(m.ctx())
	}
}

// openedMsg reports a URL the overlay asked to open in the browser.
type openedMsg string

// store stands in for the Keychain.
type store struct {
	mu sync.Mutex
	c  lastfm.Credentials
}

func (s *store) load() (lastfm.Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c, nil
}

func (s *store) save(c lastfm.Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c = c
	return nil
}

func (s *store) delete() error { return s.save(lastfm.Credentials{}) }

// fakeLastfm answers auth.getToken (error 10 for keys starting with "bad")
// and auth.getSession (not yet approved, then approved as "demo").
func fakeLastfm() http.Handler {
	var polls atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("method") {
		case "auth.getToken":
			if strings.HasPrefix(r.Form.Get("api_key"), "bad") {
				w.Write([]byte(`{"error":10,"message":"Invalid API key - You must be granted a valid key by last.fm"}`))
				return
			}
			polls.Store(0)
			w.Write([]byte(`{"token":"preview-token"}`))
		case "auth.getSession":
			if polls.Add(1) < 2 {
				w.Write([]byte(`{"error":14,"message":"Unauthorized Token - This token has not been authorized"}`))
				return
			}
			w.Write([]byte(`{"session":{"name":"demo","key":"preview-session","subscriber":0}}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
}

func (m *model) ctx() *ui.Ctx {
	return &ui.Ctx{Theme: m.theme, Lib: m.lib, Index: m.idx, Keys: m.keys, Focused: true, Now: time.Now(), Host: m.host}
}

func (m *model) top() ui.Overlay {
	if len(m.overlays) == 0 {
		return nil
	}
	return m.overlays[len(m.overlays)-1]
}

func (m *model) Init() tea.Cmd { return m.initCmd }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case keymap.TimeoutMsg:
		if r := m.keys.HandleTimeout(msg); r.Action != "" {
			return m, m.action(r.Action, r.Count)
		}
	case tea.MouseMsg:
		return m, m.mouse(msg)
	case openedMsg:
		m.host.record("open %s", string(msg))
	default:
		if o := m.top(); o != nil {
			return m, o.Update(msg, m.ctx())
		}
	}
	return m, nil
}

func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		return tea.Quit
	}
	o := m.top()
	if o == nil {
		switch msg.String() {
		case "s":
			m.open("search", search.All)
		case "p":
			m.open("palette", 0)
		case "h":
			m.open("help", 0)
		case "m":
			m.open("menu", 0)
		case "a":
			m.open("airplay", 0)
			return m.initCmd
		case "l":
			m.open("lastfm", 0)
			return m.initCmd
		case "q":
			return tea.Quit
		}
		return nil
	}
	ctx := keymap.List
	if o.CapturesInput() {
		ctx = keymap.Input
	}
	r := m.keys.Feed(ctx, keymap.Token(msg))
	switch {
	case r.Action != "":
		return m.action(r.Action, r.Count)
	case r.Ambiguous:
		return m.keys.TimeoutCmd()
	case !r.Consumed:
		return o.Key(msg, m.ctx())
	}
	return nil
}

func (m *model) action(a keymap.Action, n int) tea.Cmd {
	if o := m.top(); o != nil {
		if ok, cmd := o.Action(a, n, m.ctx()); ok {
			return cmd
		}
	}
	if a == keymap.Quit {
		return tea.Quit
	}
	m.status = "unhandled " + string(a)
	return nil
}

func (m *model) mouse(msg tea.MouseMsg) tea.Cmd {
	o := m.top()
	if o == nil || len(m.rects) < len(m.overlays) {
		return nil
	}
	ms := msg.Mouse()
	r := m.rects[len(m.overlays)-1]
	ev := ui.MouseEvent{Button: ms.Button, X: ms.X - r.Min.X, Y: ms.Y - r.Min.Y, Mod: ms.Mod, ScreenX: ms.X, ScreenY: ms.Y}
	switch msg.(type) {
	case tea.MouseClickMsg:
		ev.Kind = ui.MousePress
		now := time.Now()
		ev.Double = now.Sub(m.lastPress) < 350*time.Millisecond && ms.X == m.lastX && ms.Y == m.lastY && ms.Button == m.lastButton
		m.lastPress, m.lastX, m.lastY, m.lastButton = now, ms.X, ms.Y, ms.Button
		if !image.Pt(ms.X, ms.Y).In(r) {
			return m.host.CloseOverlay()
		}
	case tea.MouseReleaseMsg:
		ev.Kind = ui.MouseRelease
	case tea.MouseMotionMsg:
		ev.Kind = ui.MouseMotion
	case tea.MouseWheelMsg:
		ev.Kind = ui.MouseWheel
		if !image.Pt(ms.X, ms.Y).In(r) {
			return nil
		}
	}
	return o.Mouse(ev, m.ctx())
}

func (m *model) View() tea.View {
	c := m.ctx()
	layers := []*lipgloss.Layer{lipgloss.NewLayer(m.background())}
	m.rects = m.rects[:0]
	for i, o := range m.overlays {
		r := o.Rect(m.w, m.h)
		m.rects = append(m.rects, r)
		layers = append(layers, lipgloss.NewLayer(o.Render(c)).X(r.Min.X).Y(r.Min.Y).Z(i+1))
	}
	v := tea.NewView(lipgloss.NewCompositor(layers...).Render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

// background is a quiet stand-in for the app: a songs table in dim text.
func (m *model) background() string {
	t := m.theme
	lines := make([]string, 0, m.h)
	lines = append(lines, ui.Fit(t.Dim.Render("   ◂◂  ▶  ▸▸        Daylight Drive · Redmatic"), m.w))
	lines = append(lines, ui.HRule(t.Faint, "─", m.w))
	lines = append(lines, ui.Fit("  "+t.Bold.Render("Songs")+"  "+t.Dim.Render(fmt.Sprintf("%d songs", len(m.lib.Songs))), m.w))
	lines = append(lines, "")
	for i := 0; len(lines) < m.h-1 && i < len(m.lib.Songs); i++ {
		s := m.lib.Songs[i]
		row := ui.Row(2,
			ui.Cell{Text: s.Title, W: max(10, m.w*4/10)},
			ui.Cell{Text: s.Artist, W: max(8, m.w/4)},
			ui.Cell{Text: s.AlbumName, W: max(8, m.w/4)},
		)
		lines = append(lines, "  "+t.Faint.Render(row))
	}
	status := m.status
	if status == "" {
		status = m.host.last
	}
	if len(m.overlays) == 0 {
		status = strings.TrimSpace(status + "   s search · p palette · h help · m menu · a AirPlay · l last.fm · q quit")
	}
	for len(lines) < m.h-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:m.h-1], t.Accent.Render(status))
	return ui.Lines(lines, m.w, m.h)
}

// host records what overlays ask the app to do.
type host struct {
	m    *model
	last string
}

func (h *host) record(format string, args ...any) tea.Cmd {
	h.last = fmt.Sprintf(format, args...)
	h.m.status = ""
	return nil
}

func titles(ts []*library.Track) string {
	if len(ts) == 1 {
		return ts[0].Title
	}
	return fmt.Sprintf("%d tracks", len(ts))
}

func (h *host) Play(src player.Source, ts []*library.Track, i int) tea.Cmd {
	return h.record("Play(%s %q, %d tracks, #%d %q)", src.Kind, src.Title, len(ts), i, ts[i].Title)
}
func (h *host) PlayNext(ts []*library.Track) tea.Cmd   { return h.record("PlayNext(%s)", titles(ts)) }
func (h *host) AddToQueue(ts []*library.Track) tea.Cmd { return h.record("AddToQueue(%s)", titles(ts)) }
func (h *host) OpenInMusic(kind, id, _ string) tea.Cmd {
	return h.record("OpenInMusic(%s %s)", kind, id)
}
func (h *host) ToggleFavorite(ts []*library.Track) tea.Cmd {
	return h.record("ToggleFavorite(%s)", titles(ts))
}
func (h *host) Open(v ui.View) tea.Cmd { return h.record("Open(%s)", v.Key()) }
func (h *host) OpenAlbum(a *library.Album) tea.Cmd {
	return h.record("OpenAlbum(%q by %s)", a.Title, a.Artist)
}
func (h *host) OpenArtist(a *library.Artist) tea.Cmd           { return h.record("OpenArtist(%q)", a.Name) }
func (h *host) OpenPlaylist(p *library.Playlist) tea.Cmd       { return h.record("OpenPlaylist(%q)", p.Name) }
func (h *host) OpenIn(act keymap.Action, _ ui.Subject) tea.Cmd { return h.record("OpenIn(%s)", act) }
func (h *host) PushOverlay(o ui.Overlay) tea.Cmd {
	h.m.overlays = append(h.m.overlays, o)
	return nil
}
func (h *host) CloseOverlay() tea.Cmd {
	if n := len(h.m.overlays); n > 0 {
		if o, ok := h.m.overlays[n-1].(interface{ Close() }); ok {
			o.Close()
		}
		h.m.overlays = h.m.overlays[:n-1]
	}
	return nil
}
func (h *host) Menu(x, y int, ts []*library.Track) tea.Cmd {
	return h.PushOverlay(ui.NewMenu(x, y, ts))
}
func (h *host) Status(msg string) tea.Cmd { return h.record("Status(%q)", msg) }
func (h *host) Do(a keymap.Action) tea.Cmd {
	switch a {
	case keymap.Help:
		h.PushOverlay(ui.NewHelp())
	case keymap.SearchAll:
		h.PushOverlay(ui.NewSearch(search.All))
	}
	return h.record("Do(%s)", a)
}
func (h *host) Art(key, pid string, w, ht int, mode ui.ArtMode) string { return ui.Blank(w, ht) }

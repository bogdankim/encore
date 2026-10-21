// Command preview-albums hosts one album or artist view full-screen against
// the demo library, with a stub host that records calls in a status line. It
// is a throwaway harness for tools/snap screenshots.
//
//	go run ./cmd/preview-albums -view albums|recent|artists|album|artist
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/theme"
	"github.com/bogdankim/encore/internal/ui"
)

// Chrome the real app draws around the content area, reserved so views get
// realistic sizes.
const (
	barRows     = 4  // player bar + rule
	sidebarCols = 23 // sidebar + rule
	minSidebarW = 90
)

func main() {
	which := flag.String("view", "albums", "albums, recent, artists, album or artist")
	chrome := flag.Bool("chrome", true, "reserve space for the player bar and sidebar")
	flag.Parse()

	fake := bridge.NewFake(1)
	snap, err := fake.Snapshot(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lib := library.Build(snap)
	np, _ := fake.State(context.Background())

	m := &model{
		fake:   fake,
		lib:    lib,
		idx:    search.NewIndex(lib),
		theme:  theme.New(theme.Lookup("encore", true), theme.GlyphsFor("auto", "WezTerm"), false),
		keys:   keymap.New(),
		chrome: *chrome,
		art:    map[string]string{},
		snap:   player.Snapshot{NowPlaying: np},
	}
	if np.Track != nil {
		m.snap.Track = lib.Track(np.Track.PID)
	}
	m.host = &host{m: m}
	m.view = m.initial(*which)
	if m.view == nil {
		fmt.Fprintln(os.Stderr, "unknown view", *which)
		os.Exit(2)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type model struct {
	fake   *bridge.Fake
	lib    *library.Library
	idx    *search.Index
	theme  *theme.Theme
	keys   *keymap.Engine
	host   *host
	chrome bool

	view    ui.View
	history []ui.View
	art     map[string]string
	snap    player.Snapshot
	status  string
	w, h    int

	lastPress     time.Time
	lastX, lastY  int
	pendingAction string
}

func (m *model) initial(which string) ui.View {
	switch which {
	case "albums":
		return ui.NewAlbumsView(m.lib)
	case "recent":
		return ui.NewRecentView(m.lib)
	case "artists":
		return ui.NewArtistsView(m.lib)
	case "album":
		if t := m.snap.Track; t != nil {
			return ui.NewAlbumView(t.Album)
		}
		return ui.NewAlbumView(m.lib.RecentlyAdded[0])
	case "artist":
		if t := m.snap.Track; t != nil {
			return ui.NewArtistView(t.Group)
		}
		return ui.NewArtistView(m.lib.Artists[0])
	}
	return nil
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) ctx() *ui.Ctx {
	return &ui.Ctx{
		Theme: m.theme, Lib: m.lib, Index: m.idx, Keys: m.keys,
		Player: m.snap, Focused: true, Now: time.Now(), Host: m.host,
		Origin: m.content().Min,
	}
}

// content is the view's rectangle: the app's content body, inset from the
// sidebar rule and the player bar like the real layout.
func (m *model) content() image.Rectangle {
	r := image.Rect(0, 0, m.w, m.h-1) // status line at the bottom
	if m.chrome {
		r.Min.Y = barRows
		if m.w >= minSidebarW {
			r.Min.X = sidebarCols
		}
	}
	r.Min.X += 2
	r.Min.Y++
	r.Max.X--
	return r
}

type inputView interface {
	CapturesInput() bool
	InputKey(msg tea.KeyPressMsg, c *ui.Ctx) tea.Cmd
}

func (m *model) capturing() (inputView, bool) {
	iv, ok := m.view.(inputView)
	return iv, ok && iv.CapturesInput()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case tea.MouseMsg:
		return m, m.mouse(msg)
	}
	return m, nil
}

func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	c := m.ctx()
	ctx := keymap.List
	iv, capturing := m.capturing()
	if capturing {
		ctx = keymap.Input
	}
	res := m.keys.Feed(ctx, keymap.Token(msg))
	if res.Action == "" {
		if capturing && !res.Consumed {
			return iv.InputKey(msg, c)
		}
		return nil
	}
	if res.Action == keymap.Quit {
		return tea.Quit
	}
	handled, cmd := m.view.Action(res.Action, res.Count, c)
	if handled {
		return cmd
	}
	switch res.Action {
	case keymap.Back, keymap.HistoryBack:
		if n := len(m.history); n > 0 {
			m.view, m.history = m.history[n-1], m.history[:n-1]
			m.status = "back"
		}
	default:
		m.status = "unhandled: " + string(res.Action)
	}
	return nil
}

func (m *model) mouse(msg tea.MouseMsg) tea.Cmd {
	mm := msg.Mouse()
	r := m.content()
	ev := ui.MouseEvent{
		Button: mm.Button, X: mm.X - r.Min.X, Y: mm.Y - r.Min.Y, Mod: mm.Mod,
		ScreenX: mm.X, ScreenY: mm.Y,
	}
	switch msg.(type) {
	case tea.MouseClickMsg:
		ev.Kind = ui.MousePress
		now := time.Now()
		ev.Double = now.Sub(m.lastPress) < 350*time.Millisecond && mm.X == m.lastX && mm.Y == m.lastY
		m.lastPress, m.lastX, m.lastY = now, mm.X, mm.Y
		if ev.Double {
			m.lastPress = time.Time{}
		}
	case tea.MouseReleaseMsg:
		ev.Kind = ui.MouseRelease
	case tea.MouseWheelMsg:
		ev.Kind = ui.MouseWheel
	case tea.MouseMotionMsg:
		ev.Kind = ui.MouseMotion
	}
	if !(image.Point{mm.X, mm.Y}).In(r) {
		if ev.Kind != ui.MouseMotion {
			return nil
		}
		ev.X, ev.Y = -1, -1
	}
	return m.view.Mouse(ev, m.ctx())
}

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

func (m *model) render() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	t := m.theme
	r := m.content()
	sidebar := m.chrome && m.w >= minSidebarW
	lines := make([]string, m.h)
	for y := range lines {
		left := ""
		if sidebar && y >= barRows && y < m.h-1 {
			left = strings.Repeat(" ", sidebarCols-1) + t.Faint.Render(t.G.RuleV)
		}
		lines[y] = ui.Fit(left, m.w)
	}
	if m.chrome {
		lines[barRows-1] = t.Faint.Render(strings.Repeat(t.G.RuleH, m.w))
	}
	if r.Dx() > 0 && r.Dy() > 0 {
		body := strings.Split(m.view.Render(r.Dx(), r.Dy(), m.ctx()), "\n")
		for i, l := range body {
			y := r.Min.Y + i
			lines[y] = ansi.Truncate(lines[y], r.Min.X, "") + l + strings.Repeat(" ", m.w-r.Max.X)
		}
	}
	lines[m.h-1] = ui.Fit(t.Dim.Render(" "+m.status), m.w)
	return strings.Join(lines, "\n")
}

// host is a stub ui.Host: navigation works, everything else is recorded.
type host struct{ m *model }

func (h *host) note(format string, args ...any) tea.Cmd {
	h.m.status = fmt.Sprintf(format, args...)
	return nil
}

func titles(ts []*library.Track) string {
	if len(ts) == 0 {
		return "nothing"
	}
	if len(ts) == 1 {
		return ts[0].Title
	}
	return fmt.Sprintf("%d tracks", len(ts))
}

func (h *host) Play(src player.Source, ts []*library.Track, i int) tea.Cmd {
	if i >= 0 && i < len(ts) {
		h.m.snap.Track = ts[i]
		np := h.m.snap.NowPlaying
		np.Track = &bridge.CurrentTrack{PID: ts[i].PID, Name: ts[i].Title}
		h.m.snap.NowPlaying = np
		return h.note("Play %q from %s %q (%d of %d)", ts[i].Title, src.Kind, src.Title, i+1, len(ts))
	}
	return h.note("Play: bad index %d", i)
}

func (h *host) PlayNext(ts []*library.Track) tea.Cmd   { return h.note("Play next: %s", titles(ts)) }
func (h *host) AddToQueue(ts []*library.Track) tea.Cmd { return h.note("Add to queue: %s", titles(ts)) }
func (h *host) ToggleFavorite(ts []*library.Track) tea.Cmd {
	return h.note("Favorite: %s", titles(ts))
}
func (h *host) OpenInMusic(_, _, name string) tea.Cmd { return h.note("Open in Music.app: %s", name) }

func (h *host) Open(v ui.View) tea.Cmd {
	h.m.history = append(h.m.history, h.m.view)
	h.m.view = v
	return h.note("open %s", v.Key())
}

func (h *host) OpenAlbum(a *library.Album) tea.Cmd       { return h.Open(ui.NewAlbumView(a)) }
func (h *host) OpenArtist(a *library.Artist) tea.Cmd     { return h.Open(ui.NewArtistView(a)) }
func (h *host) OpenIn(keymap.Action, ui.Subject) tea.Cmd { return nil }
func (h *host) OpenPlaylist(p *library.Playlist) tea.Cmd {
	return h.note("open playlist %s", p.Name)
}
func (h *host) PushOverlay(o ui.Overlay) tea.Cmd { return h.note("overlay") }
func (h *host) CloseOverlay() tea.Cmd            { return nil }
func (h *host) Menu(x, y int, ts []*library.Track) tea.Cmd {
	return h.note("Menu at %d,%d: %s", x, y, titles(ts))
}
func (h *host) Status(msg string) tea.Cmd { return h.note("%s", msg) }

func (h *host) Do(a keymap.Action) tea.Cmd {
	if a == keymap.PlayerShuffle {
		h.m.snap.NowPlaying.Shuffle = !h.m.snap.NowPlaying.Shuffle
	}
	return h.note("Do %s", a)
}

// Art renders the fake cover as half blocks, cached per key and size.
func (h *host) Art(key, pid string, w, ht int, mode ui.ArtMode) string {
	ck := fmt.Sprintf("%s/%dx%d", key, w, ht)
	if s, ok := h.m.art[ck]; ok {
		return s
	}
	s := placeholder(h.m.theme, w, ht)
	if img := h.load(pid, max(w, 2*ht)); img != nil {
		s = art.Blocks(img, w, ht)
	}
	h.m.art[ck] = s
	return s
}

func (h *host) load(pid string, px int) image.Image {
	a, err := h.m.fake.Artwork(context.Background(), pid, max(px, 32))
	if err != nil || a.Path == "" {
		return nil
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil
	}
	return img
}

func placeholder(t *theme.Theme, w, h int) string {
	row := t.Faint.Render(strings.Repeat("░", w))
	return strings.TrimSuffix(strings.Repeat(row+"\n", h), "\n")
}

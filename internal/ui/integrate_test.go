package ui

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/lastfm"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

func newArtApp(t *testing.T, p art.Protocol, w, h int) *App {
	t.Helper()
	lib := shellLibrary(t)
	f := bridge.NewFakeWithClock(1, time.Now)
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	a := New(Options{
		Client: f, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th },
		Art:   art.NewRenderer(art.Options{Protocol: p}),
	})
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

func albumWithArt(t *testing.T, lib *library.Library) *library.Album {
	t.Helper()
	for _, al := range lib.Albums {
		if al.ArtPID != "" {
			return al
		}
	}
	t.Fatal("no album with art")
	return nil
}

func TestArtTileLoadsAsHalfBlocks(t *testing.T) {
	for _, p := range []art.Protocol{art.ProtoBlocks, art.ProtoIIP} {
		a := newArtApp(t, p, 140, 42)
		al := albumWithArt(t, a.lib)
		first := a.Art(al.Key, al.ArtPID, 12, 6, ArtTile)
		if first != artPlaceholder(a.theme, 12, 6) {
			t.Fatalf("%v: first request should be the placeholder", p)
		}
		var got string
		waitFor(t, func() bool {
			got = a.Art(al.Key, al.ArtPID, 12, 6, ArtTile)
			return got != first
		})
		checkBlock(t, "tile", got, 12, 6)
		if !strings.Contains(got, "▀") {
			t.Fatalf("%v: tiles are half blocks whatever the protocol", p)
		}
	}
}

func TestArtOffDrawsPlaceholders(t *testing.T) {
	a := newArtApp(t, art.ProtoOff, 140, 42)
	al := albumWithArt(t, a.lib)
	a.Art(al.Key, al.ArtPID, 12, 6, ArtTile)
	if len(a.art.queue) != 0 || a.art.started {
		t.Fatal("art off should not load anything")
	}
}

func TestArtLoadsAreDeduped(t *testing.T) {
	s := newArtStore(nil, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	s.started = true // no workers: requests stay queued
	for range 3 {
		s.block("k", "pid", 10, 5, ArtTile)
	}
	s.block("k", "pid", 12, 6, ArtTile)
	if len(s.queue) != 2 {
		t.Fatalf("queue %d, want one request per size", len(s.queue))
	}
	s.frame()
	for i := range artQueueMax + 10 {
		s.block("k"+itoa(i), "pid"+itoa(i), 10, 5, ArtTile)
	}
	if len(s.queue) != artQueueMax || len(s.busy) != artQueueMax {
		t.Fatalf("queue %d busy %d, want %d", len(s.queue), len(s.busy), artQueueMax)
	}
	for _, j := range s.queue {
		if j.req.key == "k" {
			t.Fatal("requests from an older frame should be dropped first")
		}
	}
	if first := s.queue[s.firstLocked()].req; first.key != "k0" {
		t.Fatalf("this frame's first request should be served first, got %s", first.key)
	}
}

// Each frame's requests go first, in the order the frame asked for them: the
// player bar's art is not left behind a grid's tiles, and tiles a frame asks
// for again move ahead of ones that scrolled away.
func TestArtLoadOrder(t *testing.T) {
	s := newArtStore(nil, art.NewRenderer(art.Options{Protocol: art.ProtoBlocks}))
	s.started = true // no workers: requests stay queued
	s.frame()
	s.block("old", "pid-old", 10, 5, ArtTile)
	s.block("tile1", "pid-tile1", 10, 5, ArtTile)
	s.frame()
	s.block("bar", "pid-bar", 6, 3, ArtHero)
	for _, k := range []string{"tile1", "tile2", "tile3"} {
		s.block(k, "pid-"+k, 10, 5, ArtTile)
	}
	var got []string
	for len(s.queue) > 0 {
		i := s.firstLocked()
		got = append(got, s.queue[i].req.key)
		s.queue = slices.Delete(s.queue, i, i+1)
	}
	if want := []string{"bar", "tile1", "tile2", "tile3", "old"}; !slices.Equal(got, want) {
		t.Fatalf("load order %v, want %v", got, want)
	}
}

func TestHeroArtIsPlaced(t *testing.T) {
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.nav.open(a.section(KeySongs)) // no album grid: only the bar's art
	a.player.PlayContext(player.Source{Kind: player.KindSongs}, a.lib.Songs[:5], 0)
	a.refreshSnap()
	var b barLayout
	waitFor(t, func() bool {
		a.View()
		b = a.barLayout
		return len(a.frameArt) == 1
	})
	want := image.Rect(b.lcdX, 0, b.lcdX+artW, artH)
	if got := a.frameArt[0].box; got != want {
		t.Fatalf("player bar art at %v, want %v", got, want)
	}
	press(a, ":")
	a.View()
	if len(a.frameCovers) != 1 || a.frameCovers[0].layer != 1 {
		t.Fatalf("the palette should cover the frame: %+v", a.frameCovers)
	}
}

func TestArtOcclusion(t *testing.T) {
	base := artPlace{key: "bar", box: image.Rect(10, 0, 16, 3)}
	over := artPlace{key: "lyrics", box: image.Rect(4, 5, 30, 18), layer: 1}
	under := artPlace{key: "header", box: image.Rect(2, 6, 22, 16)}
	covers := []artCover{
		{image.Rect(0, 4, 100, 40), 1, true},   // full-screen overlay with its own art
		{image.Rect(90, 30, 100, 40), 2, true}, // which-key on top
	}
	keep, rects := artOcclusion([]artPlace{base, over, under}, covers)
	if len(keep) != 2 || keep[0].key != "bar" || keep[1].key != "lyrics" {
		t.Fatalf("keep %+v", keep)
	}
	for _, r := range rects {
		if r.Overlaps(over.box) {
			t.Fatalf("cover %v hides the overlay's own art", r)
		}
		if r.Overlaps(base.box) {
			t.Fatalf("cover %v hides uncovered art", r)
		}
	}
	if !slices.Contains(rects, image.Rect(90, 30, 100, 40)) {
		t.Fatalf("which-key cover missing: %v", rects)
	}
}

func TestAirPlayLabel(t *testing.T) {
	mac := bridge.AirPlayDevice{Name: "MacBook Pro", Kind: "computer", Selected: true}
	lr := bridge.AirPlayDevice{Name: "Living Room", Kind: "homepod", Selected: true}
	off := bridge.AirPlayDevice{Name: "Kitchen", Kind: "speaker"}
	for _, tc := range []struct {
		devs   []bridge.AirPlayDevice
		want   string
		remote bool
	}{
		{nil, "", false},
		{[]bridge.AirPlayDevice{mac, off}, "MacBook Pro", false},
		{[]bridge.AirPlayDevice{off, lr}, "Living Room", true},
		{[]bridge.AirPlayDevice{mac, lr}, "Living Room + 1", true},
	} {
		l, remote := airPlayLabel(tc.devs)
		if got := l.fit(airNameMax); got != tc.want || remote != tc.remote {
			t.Errorf("%v: %q %v, want %q %v", tc.devs, got, remote, tc.want, tc.remote)
		}
	}
	l := airLabel{name: "Living Room Speaker", more: 2}
	if got := l.fit(14); got != "Living Ro… + 2" {
		t.Errorf("fit keeps the count: %q", got)
	}
}

func TestPlayerBarAirPlayNameNarrows(t *testing.T) {
	g := theme.NerdFont
	air := airLabel{name: "Living Room", more: 1}
	wide := layoutBar(160, 3, &g, air, false, 3*time.Minute)
	if wide.airName != "Living Room + 1" {
		t.Fatalf("wide: %q", wide.airName)
	}
	seen := map[string]bool{}
	for w := 160; w >= 60; w-- {
		b := layoutBar(w, 3, &g, air, true, 3*time.Minute)
		seen[b.airName] = true
		if b.airName != "" && Width(b.airName) < airNameMin {
			t.Fatalf("%d: name %q too short to show", w, b.airName)
		}
		if b.lcdX < 0 || b.lcdX+b.lcdW > w || b.dotX < 0 {
			t.Fatalf("%d: lcd %d+%d dot %d", w, b.lcdX, b.lcdW, b.dotX)
		}
	}
	if !seen[""] || len(seen) < 3 {
		t.Fatalf("the name should shorten, then hide: %v", seen)
	}
}

func TestLastfmIssue(t *testing.T) {
	for _, tc := range []struct {
		st   lastfm.Status
		want bool
	}{
		{lastfm.Status{State: lastfm.StateOK, Queued: 3}, false},
		{lastfm.Status{State: lastfm.StateOffline}, false},
		{lastfm.Status{State: lastfm.StateOffline, Queued: 1}, true},
		{lastfm.Status{State: lastfm.StateReauth}, true},
		{lastfm.Status{State: lastfm.StateConfig}, true},
	} {
		if got := lastfmIssue(tc.st); got != tc.want {
			t.Errorf("%+v: %v", tc.st, got)
		}
	}
}

func TestLastfmDotOpensSetup(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.bar.lastfmIssue = true
	a.render()
	b := a.barLayout
	if b.dotX < 0 {
		t.Fatal("dot hidden")
	}
	click(a, b.dotX, 1)
	if a.topOverlay() == nil {
		t.Fatalf("the dot should open last.fm setup; status %q", a.status.text)
	}
}

func TestShuffleThenPlayInOneSequence(t *testing.T) {
	a := newShellApp(t, 140, 42)
	ts := a.lib.Songs[:40]
	// as AlbumView.shuffle builds it: both calls run before the sequence does
	_ = tea.Sequence(a.Do(keymap.PlayerShuffle), a.Play(player.Source{Kind: player.KindSongs}, ts, 0))
	st := a.player.State()
	if !st.NowPlaying.Shuffle {
		t.Fatal("shuffle should be on before Play runs")
	}
	up := a.player.Upcoming()
	inOrder := true
	for i, tr := range up {
		if i+1 < len(ts) && tr != ts[i+1] {
			inOrder = false
		}
	}
	if inOrder {
		t.Fatal("the context should play shuffled")
	}
	// a second toggle in the same frame reads the player, not a stale snapshot
	a.Do(keymap.PlayerShuffle)
	a.Do(keymap.PlayerShuffle)
	if !a.player.State().NowPlaying.Shuffle {
		t.Fatal("two toggles should cancel out")
	}
}

func TestNewLineNegativeWidth(t *testing.T) {
	p := paintsFor(shellTheme())
	if s := newLine(p, surfSel, -3).add(toneText, "x").pad(2).String(); s != "" {
		t.Fatalf("got %q", s)
	}
}

func TestTrackTableKeyboardSort(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.render()
	tt := tracksOf(a)
	var labels []string
	for range len(sortCycle) {
		a.status.text = ""
		press(a, "o")
		labels = append(labels, a.status.text)
	}
	// the header arrow shows the sort; only columns off screen at this
	// width (Plays, Added) get a status line
	want := []string{"", "", "", "Sorted by plays, most first", "Sorted by added, newest first", ""}
	if !slices.Equal(labels, want) {
		t.Fatalf("cycle %q", labels)
	}
	a.status.text = ""
	press(a, "O")
	if !tt.desc || tt.sortCol != ColTitle || a.status.text != "" {
		t.Fatalf("reverse: %v %q", tt.desc, a.status.text)
	}

	var pl *library.Playlist
	for _, p := range a.lib.Playlists {
		if len(p.Tracks) > 3 && p.Kind != "folder" {
			pl = p
			break
		}
	}
	a.OpenPlaylist(pl)
	tt = tracksOf(a)
	for range len(sortCycle) + 1 {
		press(a, "o")
	}
	if tt.sortSet {
		t.Fatal("playlists cycle back to their own order")
	}
	a.status.text = ""
	press(a, "O")
	if a.status.text != "Playlist order, reversed" {
		t.Fatalf("a reversed playlist has no arrow to show it: %q", a.status.text)
	}
	if rows := tt.Rows(); rows[0] != pl.Tracks[len(pl.Tracks)-1] {
		t.Fatal("reversed playlist order")
	}
	if tt.source().ID != "" {
		t.Fatal("a reversed playlist can't play natively")
	}
}

func TestAlbumsKeyboardSort(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.goSection(KeyAlbums, "Albums")
	v := a.nav.cur.(*AlbumsView)
	a.View()
	press(a, "o")
	if v.sort != 1 || a.status.text != "" {
		t.Fatalf("sort %d %q: the title line shows the sort, no status", v.sort, a.status.text)
	}
	v.zones = nil // too narrow for the controls: the status says it
	press(a, "o")
	if v.sort != 2 || a.status.text != "Sorted by year, newest first" {
		t.Fatalf("sort %d %q", v.sort, a.status.text)
	}
	desc := v.desc
	press(a, "O")
	if v.desc == desc {
		t.Fatal("reverse")
	}
}

// probeOverlay records what the app sends it.
type probeOverlay struct {
	captures bool
	calls    []string
	closed   int
}

func (o *probeOverlay) Rect(w, h int) image.Rectangle { return image.Rect(10, 10, 40, 20) }
func (o *probeOverlay) Render(c *Ctx) string          { return Blank(30, 10) }
func (o *probeOverlay) CapturesInput() bool           { return o.captures }
func (o *probeOverlay) Init(c *Ctx) tea.Cmd           { o.calls = append(o.calls, "init"); return nil }
func (o *probeOverlay) Close()                        { o.closed++ }
func (o *probeOverlay) Update(msg tea.Msg, c *Ctx) tea.Cmd {
	if ev, ok := msg.(bridge.AirPlayEvent); ok {
		o.calls = append(o.calls, "airplay "+itoa(len(ev.Devices)))
	}
	return nil
}
func (o *probeOverlay) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	o.calls = append(o.calls, "action "+string(a))
	return a != keymap.Back && a != keymap.InputCancel, nil
}
func (o *probeOverlay) Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	o.calls = append(o.calls, "key "+msg.String())
	return nil
}
func (o *probeOverlay) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	if ev.Kind == MouseMotion {
		o.calls = append(o.calls, "motion "+itoa(ev.X)+","+itoa(ev.Y))
	}
	return nil
}

func TestOverlayRouting(t *testing.T) {
	a := newShellApp(t, 140, 42)
	o := &probeOverlay{}
	a.PushOverlay(o)
	press(a, "j", "z")
	a.Update(tea.MouseMotionMsg{X: 50, Y: 12})
	a.Update(BridgeMsg{Event: bridge.AirPlayEvent{Devices: make([]bridge.AirPlayDevice, 2)}})
	want := []string{"init", "action nav.down", "key z", "motion 40,2", "airplay 2"}
	if !slices.Equal(o.calls, want) {
		t.Fatalf("calls %q, want %q", o.calls, want)
	}

	o.captures = true
	o.calls = nil
	press(a, "j")
	if !slices.Equal(o.calls, []string{"key j"}) {
		t.Fatalf("a text field gets letters: %q", o.calls)
	}

	press(a, "esc")
	if len(a.overlays) != 0 || o.closed != 1 {
		t.Fatalf("esc closes and calls Close: %d open, closed %d", len(a.overlays), o.closed)
	}
}

func TestClickThatClosesOverlaySwallowsDoubleClick(t *testing.T) {
	a := newShellApp(t, 140, 42)
	tt := tracksOf(a)
	body := a.lay.ContentBody
	x, y := body.Min.X+5, body.Min.Y+tableTop+1+15
	a.render()
	o := &probeOverlay{}
	a.PushOverlay(o)
	click(a, x, y)
	if len(a.overlays) != 0 || o.closed != 1 {
		t.Fatal("outside click closes")
	}
	click(a, x, y)
	if a.player.State().Loading || a.player.State().NowPlaying.Track != nil {
		t.Fatal("the second press of the double-click must not play the row")
	}
	click(a, x, y)
	if tt.cursor != 15 {
		t.Fatalf("a later click selects as usual: cursor %d", tt.cursor)
	}
}

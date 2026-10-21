package ui

import (
	"fmt"
	"image"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
)

var ovScreens = [][2]int{{140, 42}, {100, 30}, {80, 24}, {60, 16}, {30, 10}, {12, 5}, {3, 2}, {0, 0}}

func TestOverlaysRenderExactSize(t *testing.T) {
	c, _ := ovTestCtx(t)
	tracks := c.Lib.RecentlyAdded[0].Tracks
	cases := map[string]func() Overlay{
		"search":       func() Overlay { return NewSearch(search.All) },
		"search songs": func() Overlay { return NewSearch(search.Songs) },
		"palette":      NewPalette,
		"help":         NewHelp,
		"menu":         func() Overlay { return NewMenu(130, 40, tracks[:1]) },
		"album menu":   func() Overlay { return NewMenu(5, 5, tracks) },
	}
	for name, mk := range cases {
		for _, q := range []string{"", "amber", "zzqqxxjj", "a"} {
			for _, s := range ovScreens {
				o := mk()
				if q != "" && o.CapturesInput() {
					ovSettle(o, c, ovType(o, c, q))
				}
				ovCheckSize(t, name+" "+q, o, c, s[0], s[1])
			}
		}
	}
}

func TestSearchDropsStaleResults(t *testing.T) {
	c, _ := ovTestCtx(t)
	o := NewSearch(search.All).(*SearchOverlay)
	first := ovType(o, c, "a")
	second := ovType(o, c, "m")
	ovSettle(o, c, second)
	ovSettle(o, c, first)
	if o.res.Query != "am" {
		t.Fatalf("results for %q, want the latest query", o.res.Query)
	}
	other := NewSearch(search.All).(*SearchOverlay)
	o.Update(searchResultsMsg{owner: other, gen: o.gen, res: search.Results{Query: "x"}}, c)
	if o.res.Query != "am" {
		t.Fatal("took results meant for another overlay")
	}
}

func TestSearchEmptyQueryClearsResults(t *testing.T) {
	c, _ := ovTestCtx(t)
	o := NewSearch(search.Songs).(*SearchOverlay)
	ovSettle(o, c, ovType(o, c, "amber"))
	if len(o.rows) == 0 {
		t.Fatal("no rows for amber")
	}
	for range 5 {
		o.Key(tea.KeyPressMsg{Code: tea.KeyBackspace}, c)
	}
	if o.query() != "" || o.res.Query != "" {
		t.Fatalf("query %q results %q after clearing", o.query(), o.res.Query)
	}
	if ok, _ := o.Action(keymap.InputSubmit, 1, c); !ok {
		t.Fatal("submit not handled")
	}
}

func TestSearchEnterPlaysSongInContext(t *testing.T) {
	c, h := ovTestCtx(t)
	o := NewSearch(search.Songs).(*SearchOverlay)
	ovSettle(o, c, ovType(o, c, "amber"))
	o.Action(keymap.InputNext, 2, c)
	_, cmd := o.Action(keymap.InputSubmit, 1, c)
	if cmd != nil {
		cmd()
	}
	if !slices.Equal(h.calls, []string{"CloseOverlay", "Play"}) {
		t.Fatalf("calls %v", h.calls)
	}
	if h.play.src.Kind != player.KindSearch || h.play.src.Title != "amber" {
		t.Fatalf("source %+v", h.play.src)
	}
	if h.play.i != 2 || len(h.play.tracks) != len(o.res.Songs) || h.play.tracks[2] != o.res.Songs[2].Track {
		t.Fatalf("played #%d of %d", h.play.i, len(h.play.tracks))
	}
	if searchHistory[0] != "amber" {
		t.Fatalf("history %v", searchHistory)
	}
}

func TestSearchAllGroupsAndOpens(t *testing.T) {
	c, h := ovTestCtx(t)
	o := NewSearch(search.All).(*SearchOverlay)
	ovSettle(o, c, ovType(o, c, "amber"))
	if len(o.rows) == 0 || o.rows[0].kind != srHeading || o.rows[0].text != "Top results" {
		t.Fatalf("first row %+v", o.rows[0])
	}
	seen := map[searchRowKind]bool{}
	for _, r := range o.rows {
		if r.kind >= srSong {
			seen[r.kind] = true
		}
	}
	for _, k := range []searchRowKind{srSong, srAlbum, srArtist} {
		if !seen[k] {
			t.Fatalf("no rows of kind %d", k)
		}
	}
	for i, r := range o.rows {
		if r.kind == srAlbum {
			o.cur.Sel = i
			break
		}
	}
	o.Action(keymap.InputSubmit, 1, c)
	if !strings.HasPrefix(h.last(), "OpenAlbum ") || h.calls[0] != "CloseOverlay" {
		t.Fatalf("calls %v", h.calls)
	}
}

func TestSearchScopes(t *testing.T) {
	c, _ := ovTestCtx(t)
	o := NewSearch(search.All).(*SearchOverlay)
	ovSettle(o, c, ovType(o, c, "amber"))
	_, cmd := o.Action(keymap.InputNextScope, 1, c)
	ovSettle(o, c, cmd)
	if o.scope != search.Songs || o.res.Scope != search.Songs {
		t.Fatalf("scope %v results %v", o.scope, o.res.Scope)
	}
	o.Action(keymap.InputPrevScope, 1, c)
	_, cmd = o.Action(keymap.InputPrevScope, 1, c)
	ovSettle(o, c, cmd)
	if o.scope != search.Playlists {
		t.Fatalf("scope %v after wrapping back", o.scope)
	}

	o.Rect(140, 42)
	o.Render(c)
	tab := o.tabs[2] // Albums
	cmd = o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: tab.Min.X, Y: tab.Min.Y}, c)
	ovSettle(o, c, cmd)
	if o.scope != search.Albums || o.res.Scope != search.Albums {
		t.Fatalf("clicking the Albums tab gave scope %v", o.scope)
	}
}

func TestSearchMouse(t *testing.T) {
	c, h := ovTestCtx(t)
	o := NewSearch(search.Artists).(*SearchOverlay)
	ovSettle(o, c, ovType(o, c, "a"))
	o.Rect(140, 42)
	o.Render(c)
	o.Mouse(MouseEvent{Kind: MouseMotion, X: 10, Y: o.listY + 1}, c)
	if o.cur.Hover != 1 {
		t.Fatalf("hover %d", o.cur.Hover)
	}
	o.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: 10, Y: o.listY}, c)
	if o.cur.Off != 3 {
		t.Fatalf("wheel offset %d", o.cur.Off)
	}
	o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 10, Y: o.listY}, c)
	want := "OpenArtist " + o.res.Artists[3].Artist.Name
	if !slices.Equal(h.calls, []string{"CloseOverlay", want}) {
		t.Fatalf("calls %v, want %s", h.calls, want)
	}
}

func TestPaletteRunsAfterClosing(t *testing.T) {
	c, h := ovTestCtx(t)
	o := NewPalette().(*PaletteOverlay)
	o.Key(tea.KeyPressMsg{Code: 'x', Text: "x"}, c)
	o.Key(tea.KeyPressMsg{Code: tea.KeyBackspace}, c)
	ovType(o, c, "go to songs")
	if got := o.items[o.rows[o.cur.Sel].item].action; got != keymap.GoSongs {
		t.Fatalf("top hit %s", got)
	}
	o.Action(keymap.InputSubmit, 1, c)
	if !slices.Equal(h.calls, []string{"CloseOverlay", "Do go.songs"}) {
		t.Fatalf("calls %v", h.calls)
	}
	for _, r := range o.rows {
		if r.item >= 0 && paletteHidden(o.items[r.item].action) {
			t.Fatalf("hidden action %s listed", o.items[r.item].action)
		}
	}
}

func TestPaletteFindsByActionName(t *testing.T) {
	c, _ := ovTestCtx(t)
	o := NewPalette().(*PaletteOverlay)
	ovType(o, c, "lastfm")
	if len(o.rows) == 0 || o.items[o.rows[0].item].action != keymap.SettingsLastfm {
		t.Fatalf("rows %v", o.rows)
	}
	ovType(o, c, "zzz")
	if len(o.rows) != 0 {
		t.Fatalf("%d rows for nonsense", len(o.rows))
	}
	ovCheckSize(t, "palette empty", o, c, 100, 30)
}

func TestHelpFilterAndClose(t *testing.T) {
	c, h := ovTestCtx(t)
	o := NewHelp().(*HelpOverlay)
	o.Rect(100, 30)
	o.Render(c)
	if !o.CapturesInput() {
		t.Fatal("help's filter doesn't have the keyboard")
	}
	o.Action(keymap.InputNext, 3, c)
	if o.off != 3 {
		t.Fatalf("offset %d after 3 down", o.off)
	}
	ovType(o, c, "lyr")
	if o.off != 0 || o.in.Value() != "lyr" {
		t.Fatalf("filter %q offset %d", o.in.Value(), o.off)
	}
	secs := o.filtered()
	if len(secs) == 0 {
		t.Fatal("nothing matches lyr")
	}
	for _, s := range secs {
		for _, e := range s.entries {
			if !strings.Contains(strings.ToLower(e.desc+e.keys+s.name), "lyr") {
				t.Fatalf("%q matched lyr", e.desc)
			}
		}
	}
	o.Action(keymap.InputCancel, 1, c)
	if o.in.Value() != "" || len(h.calls) != 0 {
		t.Fatalf("esc with a filter: %q, calls %v", o.in.Value(), h.calls)
	}
	o.Action(keymap.InputCancel, 1, c)
	if h.last() != "CloseOverlay" {
		t.Fatalf("calls %v", h.calls)
	}
}

// Keys typed into help go to its filter, never to the app behind it: r
// used to toggle repeat under the modal.
func TestHelpKeysStayInHelp(t *testing.T) {
	a := newShellApp(t, 140, 42)
	repeat := a.player.State().NowPlaying.Repeat
	press(a, "?", "l", "y", "r", "s", "p")
	o, ok := a.topOverlay().(*HelpOverlay)
	if !ok {
		t.Fatalf("top overlay %T", a.topOverlay())
	}
	if got := o.in.Value(); got != "lyrsp" {
		t.Fatalf("filter %q", got)
	}
	if got := a.player.State().NowPlaying.Repeat; got != repeat {
		t.Fatalf("repeat changed behind help: %v → %v", repeat, got)
	}
	if a.pending.Pending {
		t.Fatal("which-key opened over help")
	}
}

// A modal popup (a menu) keeps playback keys from the app; a mode drawn
// over the frame (full-screen lyrics) lets them through.
func TestModalOverlaysSwallowGlobalKeys(t *testing.T) {
	a := newShellApp(t, 140, 42)
	repeat := a.player.State().NowPlaying.Repeat
	press(a, "m")
	if _, ok := a.topOverlay().(*MenuOverlay); !ok {
		t.Fatalf("m opened %T", a.topOverlay())
	}
	press(a, "r", "space")
	if got := a.player.State().NowPlaying.Repeat; got != repeat {
		t.Fatal("r reached the app under a menu")
	}
	if a.pending.Pending {
		t.Fatal("which-key opened over a menu")
	}
	if !passesKeys(newLyricsFull(Services{})) {
		t.Fatal("full-screen lyrics should pass playback keys")
	}
}

func TestHelpListsEveryContextAndMouse(t *testing.T) {
	c, _ := ovTestCtx(t)
	var names []string
	descs := map[string]bool{}
	for _, s := range helpSections(c.Keys) {
		names = append(names, s.name)
		for _, e := range s.entries {
			descs[e.desc] = true
		}
	}
	for _, d := range []string{"Filter", "Move Down", "Offset +100 ms", "Next Result", "Play / Pause"} {
		if !descs[d] {
			t.Errorf("help lacks %q", d)
		}
	}
	if names[len(names)-1] != "Mouse" {
		t.Errorf("sections %v", names)
	}
}

func TestMenuItemsAndActions(t *testing.T) {
	c, h := ovTestCtx(t)
	album := c.Lib.RecentlyAdded[0]
	one := album.Tracks[:1]
	m := NewMenu(10, 10, one).(*MenuOverlay)
	labels := func(m *MenuOverlay) []string {
		var out []string
		for _, it := range m.items {
			if it.Label != "" {
				out = append(out, it.Label)
			}
		}
		return out
	}
	if got := labels(m); !slices.Equal(got, []string{"Play", "Play Next", "Add to Up Next", "Go to Album", "Go to Artist", "Open Album in Apple Music", "Open Artist in Apple Music", "Favorite"}) {
		t.Fatalf("labels %v", got)
	}
	favItem := m.items[len(m.items)-1]
	want := "Favorite"
	if one[0].Favorited {
		want = "Unfavorite"
	}
	if got := menuLabel(favItem, c); got != want {
		t.Fatalf("favorite label %q, want %q", got, want)
	}
	c.fav = map[string]bool{one[0].PID: !one[0].Favorited}
	if got := menuLabel(favItem, c); got == want {
		t.Fatal("label ignores the session's favorite change")
	}
	c.fav = nil
	m.Action(keymap.NavDown, 1, c)
	m.Action(keymap.Open, 1, c)
	if !slices.Equal(h.calls, []string{"CloseOverlay", "PlayNext 1"}) {
		t.Fatalf("calls %v", h.calls)
	}

	am := NewMenu(10, 10, album.Tracks).(*MenuOverlay)
	if am.items[0].Label != "Play" || slices.Contains(labels(am), "Go to Album") {
		t.Fatalf("album labels %v", labels(am))
	}
	h.calls = nil
	am.Rect(140, 42)
	am.Mouse(MouseEvent{Kind: MouseMotion, X: 3, Y: 1}, c)
	am.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 3, Y: 1}, c)
	if !slices.Equal(h.calls, []string{"CloseOverlay", "Play"}) || h.play.src.Kind != player.KindAlbum || len(h.play.tracks) != len(album.Tracks) {
		t.Fatalf("calls %v source %+v", h.calls, h.play.src)
	}
	h.calls = nil
	am.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 3, Y: 4}, c) // separator
	if len(h.calls) != 0 {
		t.Fatalf("separator click ran %v", h.calls)
	}

	// The favorite item does what its label says: a mix favorites the rest,
	// only all favorites unfavorites.
	mix := album.Tracks[:2]
	c.fav = map[string]bool{mix[0].PID: true, mix[1].PID: false}
	if got := favoriteTargets(mix, c); len(got) != 1 || got[0] != mix[1] {
		t.Fatalf("mixed targets %d", len(got))
	}
	c.fav = map[string]bool{mix[0].PID: true, mix[1].PID: true}
	if got := favoriteTargets(mix, c); len(got) != 2 {
		t.Fatalf("all-favorite targets %d", len(got))
	}
	c.fav = nil

	// The menu opens on the row below the anchor, or above it at the
	// bottom of the screen.
	if r := NewMenu(10, 10, one).Rect(140, 42); r.Min.Y != 11 {
		t.Fatalf("menu at %v, want the row below 10", r)
	}
	if r := NewMenu(10, 40, one).Rect(140, 42); r.Max.Y != 40 {
		t.Fatalf("menu at %v, want it to end above row 40", r)
	}
}

func TestMenuStaysOnScreen(t *testing.T) {
	c, _ := ovTestCtx(t)
	m := NewMenu(139, 41, c.Lib.Songs[:1])
	r := m.Rect(140, 42)
	if r.Max.X > 140 || r.Max.Y > 42 || r.Min.X < 0 || r.Min.Y < 0 {
		t.Fatalf("rect %v", r)
	}
	if r.Max.X != 140 || r.Max.Y != 41 {
		t.Fatalf("menu should open above the pointer's row and left of it, got %v", r)
	}
}

func TestOvFuzzy(t *testing.T) {
	if _, pos, ok := ovFuzzy("go to Songs", "song"); !ok || !slices.Equal(pos, []int{6, 7, 8, 9}) {
		t.Fatalf("substring: %v %v", pos, ok)
	}
	if _, pos, ok := ovFuzzy("full-screen lyrics", "fsl"); !ok || !slices.Equal(pos, []int{0, 5, 12}) {
		t.Fatalf("subsequence: %v %v", pos, ok)
	}
	a, _, _ := ovFuzzy("search songs", "so")
	b, _, _ := ovFuzzy("toggle sidebar", "so")
	if a <= b {
		t.Fatalf("word-start substring %d should beat a scattered match %d", a, b)
	}
	if _, _, ok := ovFuzzy("quit", "x"); ok {
		t.Fatal("matched a missing letter")
	}
}

func TestSearchRerunsAfterLibrarySwap(t *testing.T) {
	c, _ := ovTestCtx(t)
	o := NewSearch(search.Songs).(*SearchOverlay)
	ovSettle(o, c, ovType(o, c, "amber"))
	if cmd := o.Update(struct{}{}, c); cmd != nil {
		t.Fatal("re-ran without a new index")
	}
	swapped := *c
	swapped.Index = search.NewIndex(c.Lib)
	cmd := o.Update(struct{}{}, &swapped)
	if cmd == nil {
		t.Fatal("no new search after the index changed")
	}
	ovSettle(o, &swapped, cmd)
	if o.idx != swapped.Index || len(o.rows) == 0 {
		t.Fatal("results not refreshed")
	}
}

// ovMouseLog is an overlay that records the pointer events it gets.
type ovMouseLog struct {
	r   image.Rectangle
	evs []MouseEvent
}

func (o *ovMouseLog) Rect(int, int) image.Rectangle                   { return o.r }
func (o *ovMouseLog) Render(c *Ctx) string                            { return ovFrame(c.Theme, o.r.Dx(), o.r.Dy(), nil) }
func (o *ovMouseLog) CapturesInput() bool                             { return false }
func (o *ovMouseLog) Action(keymap.Action, int, *Ctx) (bool, tea.Cmd) { return false, nil }
func (o *ovMouseLog) Key(tea.KeyPressMsg, *Ctx) tea.Cmd               { return nil }
func (o *ovMouseLog) Update(tea.Msg, *Ctx) tea.Cmd                    { return nil }
func (o *ovMouseLog) Mouse(ev MouseEvent, _ *Ctx) tea.Cmd {
	o.evs = append(o.evs, ev)
	return nil
}

func TestOverlayGetsDragsThatLeaveIt(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.drag = regionBar // the press that opened the overlay; its release never came
	o := &ovMouseLog{r: image.Rect(10, 10, 30, 20)}
	a.PushOverlay(o)
	a.Update(tea.MouseClickMsg{X: 15, Y: 15, Button: tea.MouseLeft})
	a.Update(tea.MouseMotionMsg{X: 50, Y: 15, Button: tea.MouseLeft})
	a.Update(tea.MouseReleaseMsg{X: 50, Y: 15, Button: tea.MouseLeft})
	if n := len(o.evs); n != 3 || o.evs[2].Kind != MouseRelease {
		t.Fatalf("overlay got %d events: %+v", n, o.evs)
	}
}

func TestPasteGoesToTheTopOverlay(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.PushOverlay(NewSearch(search.All))
	a.Update(tea.PasteMsg{Content: " amber\nbloom "})
	if got := a.topOverlay().(*SearchOverlay).in.Value(); got != "amber bloom" {
		t.Fatalf("search field %q", got)
	}
	a.PushOverlay(NewPalette())
	a.Update(tea.PasteMsg{Content: "fav"})
	if got := a.topOverlay().(*PaletteOverlay).in.Value(); got != "fav" {
		t.Fatalf("palette field %q", got)
	}
	if got := a.overlays[0].(*SearchOverlay).in.Value(); got != "amber bloom" {
		t.Fatalf("paste reached the search under the palette: %q", got)
	}
}

func TestQuitKeyClosesAnOverlay(t *testing.T) {
	a := newShellApp(t, 140, 42)
	press(a, "m")
	if len(a.overlays) != 1 {
		t.Fatalf("menu didn't open: %d overlays", len(a.overlays))
	}
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if len(a.overlays) != 0 {
		t.Fatal("q left the menu open")
	}
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q quit the app under the menu")
		}
	}
	// in help, q is a letter of the filter
	press(a, "?", "q")
	if o, ok := a.topOverlay().(*HelpOverlay); !ok || o.in.Value() != "q" {
		t.Fatalf("q in help: %T", a.topOverlay())
	}
}

// A playlist's or folder's menu plays its songs and offers nothing that
// would change every song in it.
func TestPlaylistMenu(t *testing.T) {
	c, h := ovTestCtx(t)
	var folder *library.Playlist
	for _, p := range c.Lib.Playlists {
		if p.Kind == "folder" && folder == nil && len(playlistTracks(p)) > 0 {
			folder = p
		}
	}
	if folder == nil {
		t.Skip("no folder in the demo library")
	}
	m := NewPlaylistMenu(5, 5, folder).(*MenuOverlay)
	var labels []string
	for _, it := range m.items {
		labels = append(labels, it.Label)
	}
	if !slices.Equal(labels, []string{"Play", "Play Next", "Add to Up Next"}) {
		t.Fatalf("folder menu %v", labels)
	}
	m.Action(keymap.QueueAdd, 1, c) // the shortcut shown at the right runs its item
	if !slices.Equal(h.calls, []string{"CloseOverlay", fmt.Sprintf("AddToQueue %d", len(playlistTracks(folder)))}) {
		t.Fatalf("calls %v", h.calls)
	}
	empty := &library.Playlist{Name: "Empty", Kind: "user"}
	if m := NewPlaylistMenu(5, 5, empty).(*MenuOverlay); len(m.items) != 1 || m.items[0].Label != "No Songs" {
		t.Fatalf("empty playlist menu %+v", m.items)
	}
}

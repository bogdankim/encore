package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/library/libtest"
)

var emptyLib = library.Build(&bridge.Snapshot{})

// typeText feeds text to an input view one key at a time.
func typeText(v interface {
	InputKey(tea.KeyPressMsg, *Ctx) tea.Cmd
}, c *Ctx, s string) {
	for _, r := range s {
		v.InputKey(tea.KeyPressMsg{Code: r, Text: string(r)}, c)
	}
}

func TestAlbumViewsRenderSizes(t *testing.T) {
	for _, lib := range []*library.Library{albumTestLib, emptyLib} {
		c, _ := albumTestCtx(lib)
		views := []View{
			NewAlbumsView(lib), NewRecentView(lib), NewArtistsView(lib),
		}
		if len(lib.Albums) > 0 {
			views = append(views, NewAlbumView(lib.Albums[0]), NewArtistView(lib.Artists[0]))
		}
		for _, v := range views {
			for _, focused := range []bool{true, false} {
				c.Focused = focused
				for _, sz := range renderSizes {
					checkSize(t, v.Key(), v.Render(sz[0], sz[1], c), sz[0], sz[1])
				}
			}
		}
	}
}

func TestAlbumsViewTitle(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	v := NewAlbumsView(albumTestLib)
	title, meta := v.Title(c)
	if title != "Albums" || meta != count(len(albumTestLib.Albums)) {
		t.Fatalf("title %q %q", title, meta)
	}
	if k := NewRecentView(albumTestLib).Key(); k != "recent" {
		t.Fatalf("recent key %q", k)
	}
	if _, meta := NewAlbumsView(emptyLib).Title(c); meta != "" {
		t.Fatalf("empty meta %q", meta)
	}
}

func TestAlbumsViewFilter(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	v := NewAlbumsView(albumTestLib)
	v.Render(117, 37, c)

	if ok, _ := v.Action(keymap.Filter, 1, c); !ok || !v.CapturesInput() {
		t.Fatal("/ should start the filter")
	}
	typeText(v, c, "abbey")
	if v.grid.Len() != 1 || v.grid.Selected().Title != "Abbey Road" {
		t.Fatalf("filtered to %d albums", v.grid.Len())
	}
	if _, meta := v.Title(c); meta != "1 of "+count(len(albumTestLib.Albums)) {
		t.Fatalf("meta %q", meta)
	}
	if out := ansi.Strip(v.Render(117, 37, c)); !strings.Contains(out, c.Theme.G.Search+" abbey") {
		t.Fatalf("filter line missing:\n%s", out)
	}
	v.Action(keymap.InputSubmit, 1, c)
	if v.CapturesInput() || v.grid.Len() != 1 {
		t.Fatal("enter keeps the filter and returns the keyboard")
	}
	if ok, _ := v.Action(keymap.Back, 1, c); !ok || v.filter.Active() {
		t.Fatal("esc clears an applied filter")
	}
	if v.grid.Len() != len(albumTestLib.Albums) {
		t.Fatalf("cleared filter shows %d albums", v.grid.Len())
	}
	if v.grid.Selected().Title != "Abbey Road" {
		t.Fatalf("clearing should keep the filtered pick, got %q", v.grid.Selected().Title)
	}
	if ok, _ := v.Action(keymap.Back, 1, c); ok {
		t.Fatal("esc without a filter goes back")
	}

	v.Action(keymap.Filter, 1, c)
	typeText(v, c, "zzzzqqq")
	if out := ansi.Strip(v.Render(117, 37, c)); !strings.Contains(out, "Nothing matches “zzzzqqq”") {
		t.Fatalf("no empty state:\n%s", out)
	}
	v.InputKey(tea.KeyPressMsg{Code: tea.KeyEscape}, c)
	if v.filter.Active() || v.grid.Len() != len(albumTestLib.Albums) {
		t.Fatal("raw esc cancels the filter")
	}
}

func TestAlbumsViewSortControl(t *testing.T) {
	c, h := albumTestCtx(albumTestLib)
	v := NewAlbumsView(albumTestLib)
	v.Render(117, 37, c)
	if len(v.zones) != 3 {
		t.Fatalf("%d controls", len(v.zones))
	}
	click := func(i int) {
		z := v.zones[i]
		v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: z.x0, Y: z.y}, c)
		v.Render(117, 37, c)
	}
	out := ansi.Strip(v.Render(117, 37, c))
	if !strings.Contains(strings.Split(out, "\n")[0], "Title ↑") {
		t.Fatalf("sort label missing: %q", strings.Split(out, "\n")[0])
	}
	// The sort control opens a menu: the fields, the active one checked,
	// then Reverse.
	pick := func(label string) {
		t.Helper()
		h.overlay = nil
		click(0)
		ra, ok := h.overlay.(rightAligned)
		if !ok {
			t.Fatalf("sort control opened %T", h.overlay)
		}
		m := ra.MenuOverlay
		for _, it := range m.items {
			if strings.TrimSpace(strings.TrimPrefix(it.Label, "✓")) == label {
				it.Run(h)
				v.Render(117, 37, c)
				return
			}
		}
		t.Fatalf("no %q in the sort menu", label)
	}
	click(0)
	m := h.overlay.(rightAligned).MenuOverlay
	if r := h.overlay.Rect(200, 50); r.Max.X != v.zones[0].x1 || r.Min.Y != v.zones[0].y+1 {
		t.Fatalf("sort menu at %v, want its right edge at %d under the control", r, v.zones[0].x1)
	}
	if len(m.items) != 6 || m.items[0].Label != "✓ Title" || m.items[1].Label != "  Artist" || m.items[5].Label != "  Reverse" {
		t.Fatalf("sort menu %+v", m.items)
	}
	pick("Artist")
	as := v.grid.Items()
	for i := 1; i < len(as); i++ {
		if library.CompareKeys(as[i-1].SortArtist, as[i].SortArtist) > 0 {
			t.Fatalf("not sorted by artist at %d: %q > %q", i, as[i-1].SortArtist, as[i].SortArtist)
		}
	}
	pick("Reverse")
	if !v.desc {
		t.Fatal("Reverse should reverse")
	}
	pick("Year")
	as = v.grid.Items()
	if v.sort != 2 || !v.desc || as[0].Year < as[len(as)-1].Year {
		t.Fatalf("year sort: sort=%d desc=%v", v.sort, v.desc)
	}
	v.Action(keymap.ViewSort, 1, c)
	if v.sort != 3 {
		t.Fatalf("o still cycles: sort=%d", v.sort)
	}
	click(2)
	if !v.ListLayout() {
		t.Fatal("List control should switch layout")
	}
	click(1)
	if v.ListLayout() {
		t.Fatal("Grid control should switch back")
	}

	r := NewRecentView(albumTestLib)
	r.Render(117, 37, c)
	if len(r.zones) != 2 {
		t.Fatalf("recent has %d controls, want the layout toggle only", len(r.zones))
	}
}

func TestAlbumsViewReindex(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	v := NewAlbumsView(albumTestLib)
	v.Render(117, 37, c)
	v.Action(keymap.NavBottom, 1, c)
	key := v.grid.Selected().Key

	lib := library.Build(libtest.Snapshot(1500))
	if v.Reindex(lib) != v {
		t.Fatal("reindex returns the view")
	}
	if got := v.grid.Selected(); got == nil || got.Key != key || got != lib.Album(key) {
		t.Fatalf("selection lost: %v", got)
	}
	if v.Reindex(emptyLib) == nil || v.grid.Len() != 0 {
		t.Fatal("reindex to an empty library keeps an empty view")
	}
}

func TestAlbumsViewSelectedAndPlaying(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	v := NewAlbumsView(albumTestLib)
	al := albumTestLib.Albums[0]
	if got := v.Selected(c); len(got) != len(al.Tracks) {
		t.Fatalf("selected %d tracks, want %d", len(got), len(al.Tracks))
	}
	c.Player.Track = al.Tracks[0]
	out := ansi.Strip(v.Render(117, 37, c))
	if !strings.Contains(out, c.Theme.G.Playing+" "+al.Title[:3]) {
		t.Fatalf("playing marker missing:\n%s", out)
	}
	if NewAlbumsView(emptyLib).Selected(c) != nil {
		t.Fatal("empty view selects nothing")
	}
}

package ui

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

// tilePlaces are the grid tiles placed as images in the last frame.
func tilePlaces(a *App) []artPlace {
	var ps []artPlace
	for _, p := range a.frameArt {
		if strings.HasPrefix(p.key, "tile:") {
			ps = append(ps, p)
		}
	}
	return ps
}

func TestGridTilesAreImagesOnIIP(t *testing.T) {
	t.Setenv("TMUX", "")
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.nav.open(a.section(KeyRecent))
	waitFor(t, func() bool {
		a.View()
		return len(tilePlaces(a)) >= 10
	})
	body := a.lay.ContentBody
	var cropped int
	for _, p := range tilePlaces(a) {
		vis := p.box
		if !p.crop.Empty() {
			vis = p.crop
		}
		if !vis.In(body) {
			t.Fatalf("tile %v shows outside the content %v", vis, body)
		}
		if vis != p.box {
			cropped++
			for _, r := range subtractRect(p.box, vis) {
				if !slices.Contains(a.frameCovers, artCover{rect: r, layer: 1}) {
					t.Fatalf("no cover over the cut-off part %v of %v", r, p.box)
				}
			}
		}
	}
	if cropped == 0 {
		t.Fatal("at 140×42 the last tile row is cut off by the footer")
	}
	if len(tilePlaces(a)) > maxTileImages {
		t.Fatalf("%d tile images, cap %d", len(tilePlaces(a)), maxTileImages)
	}

	// A tile drawn as an image is blank cells in the frame.
	p := tilePlaces(a)[0]
	lines := strings.Split(ansi.Strip(a.render()), "\n")
	row := []rune(lines[p.box.Min.Y])
	if s := string(row[p.box.Min.X:p.box.Max.X]); strings.TrimSpace(s) != "" {
		t.Fatalf("image tile cells are not blank: %q", s)
	}
}

func TestGridTilesAreHalfBlocksElsewhere(t *testing.T) {
	a := newArtApp(t, art.ProtoBlocks, 140, 42)
	a.nav.open(a.section(KeyRecent))
	waitFor(t, func() bool {
		return strings.Contains(a.render(), "▀")
	})
	if n := len(tilePlaces(a)); n != 0 {
		t.Fatalf("%d tile images with half blocks", n)
	}
}

func TestGridFastScrollDefersImages(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	g := NewAlbumGrid(gridTestPad)
	g.Reset(testAlbums(300))
	g.Render(117, 37, c, "")
	t0 := time.Unix(1000, 0)
	wheel := func(at time.Time) tea.Cmd {
		c.Now = at
		return g.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: 5, Y: 5}, c)
	}
	if cmd := wheel(t0); cmd != nil || !g.settled(t0) {
		t.Fatal("a single scroll step keeps images")
	}
	t1 := t0.Add(40 * time.Millisecond)
	if cmd := wheel(t1); cmd != nil || !g.settled(t1) {
		t.Fatal("a quick second step (a double tap) keeps images")
	}
	t1 = t1.Add(40 * time.Millisecond)
	if cmd := wheel(t1); cmd == nil || g.settled(t1) {
		t.Fatal("a third quick step switches to half blocks and asks for a redraw")
	}
	if g.settled(t1.Add(scrollSettle - time.Millisecond)) {
		t.Fatal("still settling")
	}
	if !g.settled(t1.Add(scrollSettle)) {
		t.Fatal("images come back once scrolling stops")
	}
	t2 := t1.Add(time.Second)
	if cmd := wheel(t2); cmd != nil || !g.settled(t2) {
		t.Fatal("a lone step later keeps images")
	}
	// Moving within the visible rows is not a scroll.
	c.Now = t2.Add(10 * time.Millisecond)
	g.cursor = g.top * g.perRow()
	if _, cmd := g.Action(keymap.FocusRight, 1, c); cmd != nil {
		t.Fatal("moving the cursor without scrolling is not a scroll")
	}
}

func TestGridTileCaptions(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	var places []artPlace
	c.art = artSink{place: func(p artPlace) { places = append(places, p) }, clip: image.Rect(0, 0, 500, 500)}
	g := NewAlbumGrid(gridTestPad)
	g.Reset(testAlbums(30))
	g.Render(117, 37, c, "")
	geo := g.geo
	under := func() []string {
		lines := strings.Split(ansi.Strip(g.Render(117, 37, c, "")), "\n")
		return []string{lines[geo.artH+2], lines[geo.pitch()+geo.artH+2]}
	}
	if u := under(); !strings.HasPrefix(strings.TrimSpace(u[0]), strings.Repeat(underlineGlyph, geo.tileW)) {
		t.Fatalf("the selected tile has no underline: %q", u[0])
	}
	g.Passive = true
	if u := under(); strings.Contains(u[0], underlineGlyph) {
		t.Fatalf("a passive grid shows no selection: %q", u[0])
	}
	g.Passive = false

	// Hover: badge on the art and a cover so images are cut around it.
	places = nil
	g.Mouse(MouseEvent{Kind: MouseMotion, X: geo.pad + geo.stride() + 2, Y: 1}, c)
	out := strings.Split(ansi.Strip(g.Render(117, 37, c, "")), "\n")
	badge := []rune(out[geo.artH-1])[geo.pad+geo.stride():]
	if string(badge[:playBadgeW]) != " "+c.Theme.G.Playing+" " {
		t.Fatalf("no play badge on the hovered tile: %q", string(badge[:playBadgeW]))
	}
	want := image.Rect(geo.pad+geo.stride(), geo.artH-1, geo.pad+geo.stride()+playBadgeW, geo.artH)
	if !slices.ContainsFunc(places, func(p artPlace) bool { return p.cover && p.box == want }) {
		t.Fatalf("no cover for the badge at %v: %+v", want, places)
	}
	if u := under(); strings.Contains(string([]rune(u[0])[geo.pad+geo.stride():]), underlineGlyph) {
		t.Fatal("hover is not selection")
	}
}

func TestArtistPageHeader(t *testing.T) {
	c, h := albumTestCtx(albumTestLib)
	var ar *library.Artist
	for _, a := range albumTestLib.Artists {
		if len(a.Albums) >= 2 {
			ar = a
			break
		}
	}
	v := NewArtistView(ar)
	out := ansi.Strip(v.Render(117, 37, c))
	for _, want := range []string{ar.Name, artistMeta(ar), "Play", "Shuffle"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if len(v.page.zones) != 2 {
		t.Fatalf("%d header zones", len(v.page.zones))
	}
	press := func(i int) {
		z := v.page.zones[i]
		v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: z.x0, Y: z.y}, c)
	}
	press(0)
	var all []*library.Track
	for _, al := range ar.Albums {
		all = append(all, al.Tracks...)
	}
	if h.played.i != 0 || !slices.Equal(h.played.tracks, all) {
		t.Fatalf("Play plays every song in album order: %d tracks from %d", len(h.played.tracks), h.played.i)
	}
	h.calls = nil
	press(1)
	if len(h.calls) != 2 || h.calls[0] != "do player.shuffle" || h.calls[1] != "play artist "+itoa(h.played.i) {
		t.Fatalf("Shuffle turns shuffle on and plays: %v", h.calls)
	}
}

func TestArtistTopSongsHideOneAlbum(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	for _, ar := range albumTestLib.Artists {
		v := NewArtistView(ar)
		out := ansi.Strip(v.Render(117, 37, c))
		songs := v.page.songs[:v.page.songsN]
		if len(songs) < 2 {
			continue
		}
		name := songs[0].AlbumName
		lines := strings.Split(out, "\n")[v.page.songsY : v.page.songsY+len(songs)]
		shown := strings.Contains(lines[0], name) && !strings.Contains(songs[0].Title, name)
		if oneAlbum(songs) && shown {
			t.Fatalf("%s: the album column repeats %q", ar.Name, name)
		}
	}
}

// TestGridRowsInTmux: with sixel inside tmux, which keeps nine images, each
// row of tiles is one image and the frame stays within the limit.
func TestGridRowsInTmux(t *testing.T) {
	lib := shellLibrary(t)
	f := bridge.NewFakeWithClock(1, time.Now)
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	a := New(Options{
		Client: f, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th },
		Art:   art.NewRenderer(art.Options{Protocol: art.ProtoSixel, Tmux: true, Cell: art.CellSize{W: 8, H: 16}}),
	})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 42})
	a.nav.open(a.section(KeyRecent))
	rows := func() []artPlace {
		var ps []artPlace
		for _, p := range a.frameArt {
			if strings.HasPrefix(p.key, "row:") {
				ps = append(ps, p)
			}
		}
		return ps
	}
	waitFor(t, func() bool {
		a.View()
		return len(rows()) >= 3
	})
	if n := len(tilePlaces(a)); n != 0 {
		t.Fatalf("%d single tile images; tmux keeps too few for that", n)
	}
	if n := len(a.frameArt); n > art.TmuxImageLimit {
		t.Fatalf("%d images in one frame, tmux keeps %d", n, art.TmuxImageLimit)
	}
	for _, p := range rows() {
		if p.box.Dx() <= 2*p.box.Dy() { // a tile is twice as wide as tall
			t.Fatalf("row image %v is no wider than one tile", p.box)
		}
	}
	// the row's cells are blank in the frame; the image goes over them
	p := rows()[0]
	lines := strings.Split(ansi.Strip(a.render()), "\n")
	if s := string([]rune(lines[p.box.Min.Y])[p.box.Min.X:p.box.Max.X]); strings.TrimSpace(s) != "" {
		t.Fatalf("row image cells are not blank: %q", s)
	}
}

// TestGridTilesInTmuxPassthrough: on WezTerm inside tmux, sixel stays for
// the player bar and page headers while each grid tile is its own iTerm2
// image through passthrough, so tmux's nine-image limit no longer applies
// and a tile shows as soon as it has loaded.
func TestGridTilesInTmuxPassthrough(t *testing.T) {
	lib := shellLibrary(t)
	f := bridge.NewFakeWithClock(1, time.Now)
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	r := art.NewRenderer(art.Options{
		Protocol: art.ProtoSixel, Passthrough: true, Tmux: true,
		TilePrefix: TileKeyPrefix, Cell: art.CellSize{W: 8, H: 16},
	})
	a := New(Options{
		Client: f, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th },
		Art:   r,
	})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 42})
	a.nav.open(a.section(KeyRecent))
	waitFor(t, func() bool {
		a.View()
		return len(tilePlaces(a)) >= 10
	})
	for _, p := range a.frameArt {
		if strings.HasPrefix(p.key, "row:") {
			t.Fatalf("row image %q; tiles go one by one through passthrough", p.key)
		}
	}
	if a.art.rowImages {
		t.Fatal("rows as one image under passthrough")
	}
	if !r.Fragile() || r.ImageLimit() != 0 {
		t.Fatalf("Fragile %v ImageLimit %d", r.Fragile(), r.ImageLimit())
	}
}

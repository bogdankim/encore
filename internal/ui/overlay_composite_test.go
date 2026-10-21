package ui

import (
	"image"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/art"
)

// The compositor cuts the base frame around an overlay by cells. Glyphs
// whose width tables disagree ("…" is East Asian ambiguous, Nerd Font
// icons are private use, CJK is wide) must keep every cell left and right
// of the overlay where it was, measured both ways Bubble Tea may measure
// (grapheme widths with mode 2027, wcwidth without).
func TestCompositeKeepsCellsAroundOverlays(t *testing.T) {
	const w = 60
	faint := lipgloss.NewStyle().Foreground(lipgloss.Color("#4A4A52"))
	left := []string{
		"   Northbound: Indie… " + faint.Render("│"),
		"   夏のプレイリスト   " + faint.Render("│"),
		" \U000F0150 Recently Added   " + faint.Render("│"),
		"   Songs for the Long…" + faint.Render("│"),
	}
	var base []string
	for _, l := range left {
		base = append(base, Fit(l+"  Live at the Tangier…  The Hidden Machines", w))
	}
	over := Box(strings.Repeat(faint.Render("│")+" Search Albums   space s a l "+faint.Render("│")+"\n", 4), 31, 4)
	out := lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(base, "\n")),
		lipgloss.NewLayer(over).X(26).Y(0).Z(1),
	).Render()
	lines := strings.Split(Box(out, w, len(base)), "\n")
	for i, l := range lines {
		for _, m := range []ansi.Method{ansi.GraphemeWidth, ansi.WcWidth} {
			if got := m.StringWidth(l); got != w {
				t.Errorf("line %d is %d cells wide by %v: %q", i, got, m, ansi.Strip(l))
			}
		}
		if got, want := ansi.Strip(ansi.Cut(l, 0, 26)), ansi.Strip(ansi.Cut(base[i], 0, 26)); got != want {
			t.Errorf("line %d left of the overlay: %q, want %q", i, got, want)
		}
		if got, want := ansi.Strip(ansi.Cut(l, 26, 57)), ansi.Strip(strings.Split(over, "\n")[i]); got != want {
			t.Errorf("line %d overlay: %q, want %q", i, got, want)
		}
		if got, want := ansi.Strip(ansi.Cut(l, 57, w)), ansi.Strip(ansi.Cut(base[i], 57, w)); got != want {
			t.Errorf("line %d right of the overlay: %q, want %q", i, got, want)
		}
	}
}

// A grid tile an overlay leaves only a sliver of is blank, not an image
// cropped to a strip beside the overlay.
func TestOverlaysHideTheTileImagesTheyTouch(t *testing.T) {
	t.Setenv("TMUX", "")
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	a.nav.open(a.section(KeyRecent))
	waitFor(t, func() bool {
		a.View()
		return len(tilePlaces(a)) >= 10
	})
	before := len(tilePlaces(a))
	press(a, ":")
	a.View()
	r := a.topOverlay().Rect(a.w, a.h)
	for _, p := range tilePlaces(a) {
		if shown := a.artShown(p); p.box.Overlaps(r) && shown < artMinShown {
			t.Fatalf("tile image %v cut to %.0f%% by the palette %v", p.box, shown*100, r)
		}
	}
	if n := len(tilePlaces(a)); n == 0 || n >= before {
		t.Fatalf("%d tile images with the palette open, %d without", n, before)
	}
}

// A drawer against the left edge keeps a blank column between its rule and
// the frame beside it.
func TestOverlayGap(t *testing.T) {
	for _, tc := range []struct {
		r, want image.Rectangle
	}{
		{image.Rect(0, 4, 23, 40), image.Rect(23, 4, 24, 40)},
		{image.Rect(90, 4, 140, 40), image.Rect(89, 4, 90, 40)},
		{image.Rect(30, 5, 100, 30), image.Rectangle{}},
		{image.Rect(0, 0, 140, 42), image.Rectangle{}},
	} {
		if got := overlayGap(tc.r, 140); got != tc.want {
			t.Errorf("gap of %v: %v, want %v", tc.r, got, tc.want)
		}
	}
}

// Hero art follows the same rule as tiles: blank when overlays leave less
// than half of it, placed (and cropped around them) otherwise. Covers on its
// own layer are its own badges, not overlays over it.
func TestOverlaysHideTheHeroArtTheyMostlyCover(t *testing.T) {
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	for _, tc := range []struct {
		name   string
		covers []artCover
		placed bool
	}{
		{"uncovered", nil, true},
		{"top row left", []artCover{{image.Rect(0, 1, 140, 42), 1, true}}, false},
		{"bottom rows covered", []artCover{{image.Rect(0, 3, 140, 42), 1, true}}, true},
		{"drawer and gap", []artCover{
			{image.Rect(4, 0, 140, 42), 1, true},
			{image.Rect(3, 0, 4, 42), 1, true},
		}, false},
		{"own layer", []artCover{{image.Rect(0, 1, 140, 42), 0, true}}, true},
		{"not an overlay", []artCover{{image.Rect(0, 1, 140, 42), 1, false}}, true},
	} {
		a.frameCovers = tc.covers
		var got []artPlace
		c := a.baseCtx()
		c.art.place = func(p artPlace) { got = append(got, p) }
		s := c.HeroArt("k", "pid", 0, 0, 10, 5)
		if placed := len(got) == 1; placed != tc.placed {
			t.Errorf("%s: placed %v, want %v", tc.name, placed, tc.placed)
		}
		if !tc.placed && s != Blank(10, 5) {
			t.Errorf("%s: hidden hero art should be blank", tc.name)
		}
	}
}

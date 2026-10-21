package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
)

// barRow renders the player bar of a and returns its middle row without
// styling, with the layout the render used.
func barRow(t *testing.T, a *App, w int) (string, barLayout) {
	t.Helper()
	rows := a.renderBar(w, 3, a.baseCtx())
	if len(rows) != 3 {
		t.Fatalf("w%d: %d bar rows", w, len(rows))
	}
	row := ansi.Strip(rows[1])
	if got := ansi.StringWidth(row); got != w {
		t.Fatalf("w%d: bar row is %d cells wide: %q", w, got, row)
	}
	return row, a.barLayout
}

// TestIdleBarKeepsRightSide: with nothing playing the bar's right group has
// the layout of the playing bar: the same positions, the volume meter, the
// whole AirPlay name and a gap before each toggle. "Not Playing" once ran as
// wide as the LCD from its centred start, which pushed the right group out.
func TestIdleBarKeepsRightSide(t *testing.T) {
	for _, w := range []int{80, 100, 120, 140, 160, 200} {
		name := "w" + itoa(w)
		idle := newShellApp(t, w, 40)
		play := playingApp(t, w, 40)
		for _, a := range []*App{idle, play} {
			a.setAirPlay([]bridge.AirPlayDevice{
				{Name: "MacBook Pro", Kind: "computer"},
				{Name: "Living Room", Kind: "homepod", Selected: true},
			})
		}
		if idle.snap.NowPlaying.Track != nil || play.snap.NowPlaying.Track == nil {
			t.Fatalf("%s: idle plays %v, playing plays %v", name, idle.snap.NowPlaying.Track, play.snap.NowPlaying.Track)
		}
		irow, ib := barRow(t, idle, w)
		prow, pb := barRow(t, play, w)

		// the layout is the same up to the width of the clock
		if ib.level != pb.level || ib.rightX != pb.rightX || ib.volX != pb.volX || ib.airX != pb.airX ||
			ib.lyricsX != pb.lyricsX || ib.queueX != pb.queueX || ib.airName != pb.airName ||
			ib.lcdX != pb.lcdX || ib.lcdW != pb.lcdW {
			t.Fatalf("%s: idle layout %+v, playing %+v", name, ib, pb)
		}

		g := &idle.baseCtx().Theme.G
		cut := func(row string, x, n int) string { return ansi.Cut(row, x, x+n) }
		for label, row := range map[string]string{"idle": irow, "playing": prow} {
			b := ib
			// everything from the right group on is where the layout says
			if ib.meter > 0 && strings.TrimSpace(cut(row, b.volX, b.meter)) == "" {
				t.Errorf("%s %s: no volume meter at %d: %q", name, label, b.volX, row)
			}
			if got := cut(row, b.airX, slotW(g.AirPlay)); strings.TrimSpace(got) != g.AirPlay {
				t.Errorf("%s %s: AirPlay glyph at %d is %q: %q", name, label, b.airX, got, row)
			}
			if b.airName != "" {
				nx := b.airX + slotW(g.AirPlay) + 1
				if got := cut(row, nx, ansi.StringWidth(b.airName)); got != b.airName {
					t.Errorf("%s %s: AirPlay name %q at %d is %q: %q", name, label, b.airName, nx, got, row)
				}
				if ib.airName == "" || strings.Contains(ib.airName, "…") {
					t.Errorf("%s %s: AirPlay name cut to %q at %d columns", name, label, ib.airName, w)
				}
			}
			if b.lyricsX >= 0 {
				if got := strings.TrimSpace(cut(row, b.lyricsX, slotW(g.Lyrics))); got != g.Lyrics {
					t.Errorf("%s %s: lyrics at %d is %q: %q", name, label, b.lyricsX, got, row)
				}
				if got := strings.TrimSpace(cut(row, b.queueX, slotW(g.Queue))); got != g.Queue {
					t.Errorf("%s %s: Up Next at %d is %q: %q", name, label, b.queueX, got, row)
				}
				// space between the groups
				if gap := cut(row, b.lyricsX-btnGap, btnGap); strings.TrimSpace(gap) != "" {
					t.Errorf("%s %s: no gap before lyrics: %q", name, label, row)
				}
				if gap := cut(row, b.queueX-btnGap, btnGap); strings.TrimSpace(gap) != "" {
					t.Errorf("%s %s: no gap before Up Next: %q", name, label, row)
				}
			}
			if gap := cut(row, b.airX-2, 2); strings.TrimSpace(gap) != "" {
				t.Errorf("%s %s: no gap before AirPlay: %q", name, label, row)
			}
		}

		// the right group reads the same, apart from the volume number (the
		// player reports 0 while nothing plays)
		right := func(row string, b barLayout) string {
			s := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return -1
				}
				return r
			}, ansi.Cut(row, b.rightX, b.w))
			return strings.Join(strings.Fields(s), " ")
		}
		if ri, rp := right(irow, ib), right(prow, pb); ri != rp {
			t.Errorf("%s: idle right %q, playing right %q", name, ri, rp)
		}

		// "Not Playing" is centred in the LCD and stays inside it
		msg := "Not Playing"
		i := strings.Index(irow, msg)
		if i < 0 {
			t.Fatalf("%s: no %q: %q", name, msg, irow)
		}
		x := ansi.StringWidth(irow[:i])
		if x < ib.lcdX || x+len(msg) > ib.lcdX+ib.lcdW {
			t.Errorf("%s: %q at %d..%d outside the LCD %d..%d", name, msg, x, x+len(msg), ib.lcdX, ib.lcdX+ib.lcdW)
		}
		if want := ib.lcdX + (ib.lcdW-len(msg))/2; x != want {
			t.Errorf("%s: %q at %d, centre is %d", name, msg, x, want)
		}
	}
}

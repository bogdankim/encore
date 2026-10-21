package ui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/lyrics"
	"github.com/bogdankim/encore/internal/theme"
)

// atWord puts the snapshot at d, playing, with the clock at the moment it
// was taken.
func (fx *lyricsFixture) atWord(d time.Duration) {
	fx.at(d)
	fx.c.Player.NowPlaying.State = bridge.Playing
	fx.c.Now = time.UnixMilli(fx.c.Player.NowPlaying.At)
}

// edgeSGR is the style of a fill edge cell lv of the way to the accent.
func edgeSGR(c *Ctx, lv float64) string {
	return sgr(lipgloss.NewStyle().Foreground(mixLinear(textColor(c), c.Theme.P.Accent, lv)).Bold(true), lipgloss.NewStyle())
}

func TestWordTimes(t *testing.T) {
	s := time.Second
	ms := time.Millisecond
	ls := []lyrics.Line{
		{Start: 1 * s, Words: []lyrics.Word{
			{Start: 1 * s, End: 1200 * ms, Text: "own "}, // its own end
			{Start: 1500 * ms, Text: "next "},            // the next word's start
			{Start: 2 * s, Text: "long "},                // capped
			{Start: 5 * s, Text: "last"},                 // the next line's start
		}},
		{Start: 5500 * ms, Words: []lyrics.Word{{Start: 5500 * ms, Text: "end"}}}, // no next line: capped
	}
	for _, c := range []struct {
		i, j       int
		start, end time.Duration
	}{
		{0, 0, 1 * s, 1200 * ms},
		{0, 1, 1500 * ms, 2 * s},
		{0, 2, 2 * s, 2*s + wordFillMax},
		{0, 3, 5 * s, 5500 * ms},
		{1, 0, 5500 * ms, 5500*ms + wordFillMax},
	} {
		if st, end := wordTimes(ls, c.i, c.j); st != c.start || end != c.end {
			t.Errorf("word %d.%d: %v–%v, want %v–%v", c.i, c.j, st, end, c.start, c.end)
		}
	}
}

func TestFillLevels(t *testing.T) {
	text := "Paper lanterns"
	spans := wordSpans(text, []lyrics.Word{{Text: "Paper "}, {Text: "lanterns"}})
	if len(spans) != 2 || spans[1] != (wordSpan{6, 14}) {
		t.Fatalf("spans %v", spans)
	}
	// Paper sung, lanterns 0.55 of the way: 4.4 of its 8 cells
	lv := fillLevels(text, spans, []float64{1, 0.55})
	want := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0.4, 0, 0, 0}
	for i := range want {
		if d := lv[i] - want[i]; d > 1e-9 || d < -1e-9 {
			t.Fatalf("level of %q (byte %d) = %v, want %v; all %v", text[i], i, lv[i], want[i], lv)
		}
	}
	// a space waits for the word before it to be sung through
	if lv := fillLevels(text, spans, []float64{0.99, 0}); lv[5] != 0 || lv[4] < 0.9 {
		t.Errorf("space before the word is sung through: %v", lv)
	}
	// a wide character is one edge cell across its two columns
	cjk := "夜明け"
	lv = fillLevels(cjk, wordSpans(cjk, []lyrics.Word{{Text: cjk}}), []float64{0.5})
	if lv[0] != 1 || lv[3] != 0.5 || lv[6] != 0 {
		t.Errorf("wide: %v", lv)
	}
}

func TestMixLinear(t *testing.T) {
	black, white := color.RGBA{0, 0, 0, 255}, color.RGBA{255, 255, 255, 255}
	if c := mixLinear(black, white, 0); c != black {
		t.Errorf("0: %v", c)
	}
	if c := mixLinear(black, white, 1); c != white {
		t.Errorf("1: %v", c)
	}
	// half the light is brighter than half the sRGB value
	if c := mixLinear(black, white, 0.5).(color.RGBA); c.R != 188 {
		t.Errorf("0.5: %v, want 188 grey", c)
	}
}

func TestLyricsWordsFill(t *testing.T) {
	fx := newLyricsFixture(t, wordsLRC)
	p := newLyricsPane(fx.svc).(*lyricsPane)
	fx.load(p.v)
	fx.c.TrueColor = true
	pp := paintsFor(fx.c.Theme)
	ms := time.Millisecond

	// Paper sung, lanterns (1.5–2.0 s) half way: four cells of it
	fx.atWord(1750 * ms)
	out := p.Render(40, 12, fx.c)
	want := pp.paint(toneAccentBold, surfNone, "Paper lant") + pp.paint(toneBold, surfNone, "erns and rain")
	if !strings.Contains(out, want) {
		t.Errorf("at 1.75 s:\n%q\nwant %q", out, want)
	}

	// 4.2 cells: the fifth is a fifth of the way to the accent
	fx.atWord(1762500 * time.Microsecond)
	out = p.Render(40, 12, fx.c)
	want = pp.paint(toneAccentBold, surfNone, "Paper lant") + edgeSGR(fx.c, 6.0/32) + "e" + ansi.ResetStyle + pp.paint(toneBold, surfNone, "rns and rain")
	if !strings.Contains(out, want) {
		t.Errorf("at 1.7625 s:\n%q\nwant %q", out, want)
	}

	// The line comes LyricsLead early; its words wait for their time, in
	// bold so the line keeps one weight.
	fx.atWord(900 * ms)
	if out := p.Render(40, 12, fx.c); !strings.Contains(out, pp.paint(toneBold, surfNone, "Paper lanterns and rain")) {
		t.Errorf("at 0.9 s no word should be sung:\n%q", out)
	}

	// A wrapped line: "and" (2.0–2.4 s) half sung on the second row.
	fx.atWord(2200 * ms)
	out = p.Render(18, 12, fx.c)
	for _, w := range []string{
		pp.paint(toneAccentBold, surfNone, "Paper lanterns"),
		pp.paint(toneAccentBold, surfNone, "a") + edgeSGR(fx.c, 0.5) + "n" + ansi.ResetStyle + pp.paint(toneBold, surfNone, "d rain"),
	} {
		if !strings.Contains(out, w) {
			t.Errorf("wrapped at 2.2 s: missing %q in\n%q", w, out)
		}
	}

	// The last word has no end: it fills until the next line, at most
	// wordFillMax (2.4–3.9 s).
	fx.atWord(3150 * ms)
	if out := p.Render(40, 12, fx.c); !strings.Contains(out, pp.paint(toneAccentBold, surfNone, "Paper lanterns and ra")+pp.paint(toneBold, surfNone, "in")) {
		t.Errorf("last word at 3.15 s:\n%q", out)
	}
}

func TestLyricsWordLead(t *testing.T) {
	fx := newLyricsFixture(t, wordsLRC)
	fx.svc.WordLead = 100 * time.Millisecond
	p := newLyricsPane(fx.svc).(*lyricsPane)
	fx.load(p.v)
	fx.c.TrueColor = true
	pp := paintsFor(fx.c.Theme)
	fx.atWord(1650 * time.Millisecond) // 1.75 s with the lead
	if out := p.Render(40, 12, fx.c); !strings.Contains(out, pp.paint(toneAccentBold, surfNone, "Paper lant")+pp.paint(toneBold, surfNone, "erns and rain")) {
		t.Errorf("word lead:\n%q", out)
	}
}

func TestLyricsWordsSteps(t *testing.T) {
	fx := newLyricsFixture(t, wordsLRC)
	p := newLyricsPane(fx.svc).(*lyricsPane)
	fx.load(p.v)
	pp := paintsFor(fx.c.Theme)

	// Without 24-bit colour a word turns accent as it starts, in one step.
	fx.c.TrueColor = false
	for _, at := range []time.Duration{1500 * time.Millisecond, 1762500 * time.Microsecond, 1999 * time.Millisecond} {
		fx.atWord(at)
		out := p.Render(40, 12, fx.c)
		if want := pp.paint(toneAccentBold, surfNone, "Paper lanterns ") + pp.paint(toneBold, surfNone, "and rain"); !strings.Contains(out, want) {
			t.Errorf("steps at %v:\n%q\nwant %q", at, out, want)
		}
	}

	// NO_COLOR: bold marks sung words, the rest is plain.
	fx.c.TrueColor = true
	fx.c.Theme = theme.New(theme.Lookup("encore", true), theme.Unicode, true)
	pp = paintsFor(fx.c.Theme)
	fx.atWord(1762500 * time.Microsecond)
	out := p.Render(40, 12, fx.c)
	if want := pp.paint(toneAccentBold, surfNone, "Paper lanterns ") + "and rain"; !strings.Contains(out, want) {
		t.Errorf("NO_COLOR:\n%q\nwant %q", out, want)
	}
}

func TestLyricsWordsFillFullScreen(t *testing.T) {
	fx := newLyricsFixture(t, wordsLRC)
	o := newLyricsFull(fx.svc).(*lyricsFull)
	fx.load(o.v)
	fx.c.TrueColor = true
	pp := paintsFor(fx.c.Theme)
	fx.atWord(1762500 * time.Microsecond)
	o.Rect(140, 42)
	out := o.Render(fx.c)
	want := pp.paint(toneAccentBold, surfNone, "Paper lant") + edgeSGR(fx.c, 6.0/32) + "e" + ansi.ResetStyle + pp.paint(toneBold, surfNone, "rns and rain")
	if !strings.Contains(out, want) {
		t.Errorf("full screen:\n%q", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(row); w != 140 {
			t.Fatalf("row %d cells wide: %q", w, row)
		}
	}
}

func TestLyricsWantsFrames(t *testing.T) {
	fx := newLyricsFixture(t, wordsLRC)
	p := newLyricsPane(fx.svc).(*lyricsPane)
	fx.load(p.v)
	s := time.Second
	ms := time.Millisecond
	for _, c := range []struct {
		at   time.Duration
		want bool
	}{
		{500 * ms, false},  // before the first line
		{900 * ms, true},   // the line is current, its first word about to start
		{2500 * ms, true},  // filling
		{3850 * ms, true},  // the next line is current (lead), filling
		{5600 * ms, false}, // its word is sung through
		{3 * s, true},
	} {
		fx.atWord(c.at)
		p.Render(40, 12, fx.c)
		if got := p.wantsFrames(fx.c); got != c.want {
			t.Errorf("at %v: wantsFrames %v", c.at, got)
		}
	}
	fx.atWord(2500 * ms)
	fx.c.Player.NowPlaying.State = bridge.Paused
	if p.wantsFrames(fx.c) {
		t.Error("paused wants frames")
	}

	// line-level lyrics never need them
	fx2 := newLyricsFixture(t, testLRC)
	p2 := newLyricsPane(fx2.svc).(*lyricsPane)
	fx2.load(p2.v)
	fx2.atWord(2 * s)
	p2.Render(40, 12, fx2.c)
	if p2.wantsFrames(fx2.c) {
		t.Error("line-level lyrics want frames")
	}
}

type frameProbe struct {
	probeOverlay
	want bool
}

func (o *frameProbe) wantsFrames(*Ctx) bool { return o.want }

func TestTickRate(t *testing.T) {
	a := newShellApp(t, 140, 42)
	if a.tickRate() != tickEvery {
		t.Fatal("nothing filling: the player tick")
	}
	o := &frameProbe{want: true}
	a.overlays = append(a.overlays, o)
	if a.tickRate() != frameEvery {
		t.Fatal("lyrics filling: frames")
	}
	o.want = false
	if a.tickRate() != tickEvery {
		t.Fatal("done filling: back to the player tick")
	}
}

func TestTickWhileBlurredWithLyrics(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.snap.NowPlaying.State = bridge.Playing
	a.blurred = true
	if a.wantTick() {
		t.Fatal("in the background without lyrics: no tick")
	}
	a.right = rightLyrics
	a.relayout()
	if !a.paneShown(rightLyrics) || !a.wantTick() {
		t.Fatal("lyrics on screen keep moving in the background")
	}
}

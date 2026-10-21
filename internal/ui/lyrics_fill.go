package ui

import (
	"image/color"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/lyrics"
)

// Word-by-word lyrics fill like karaoke: on the current line, sung words
// are accent, the word being sung fills left to right across its time, and
// words to come keep the text colour. The cell at the fill edge takes a
// colour between the two, so the fill moves smoothly rather than a cell at
// a time. Without 24-bit colour each word turns accent in one step as it
// starts.

// wordFillMax caps how long a word without an end time takes to fill.
const wordFillMax = 1500 * time.Millisecond

// wordSpan is where a timed word's text is in its line's trimmed text, in
// bytes.
type wordSpan struct{ from, to int }

// wordSpans finds each word in text, the line's trimmed text; nil when the
// words don't spell it out.
func wordSpans(text string, words []lyrics.Word) []wordSpan {
	out := make([]wordSpan, len(words))
	at := 0
	for i, w := range words {
		t := strings.TrimSpace(w.Text)
		k := strings.Index(text[at:], t)
		if k < 0 {
			return nil
		}
		out[i] = wordSpan{at + k, at + k + len(t)}
		at += k + len(t)
	}
	return out
}

// wordTimes is when word j of line i starts and when it has been sung
// through: its own end, else the next word's start (the next line's for the
// last word), at most wordFillMax after it starts.
func wordTimes(ls []lyrics.Line, i, j int) (start, end time.Duration) {
	ws := ls[i].Words
	start, end = ws[j].Start, ws[j].End
	if end <= start {
		end = 0
		switch {
		case j+1 < len(ws):
			end = ws[j+1].Start
		case i+1 < len(ls):
			end = ls[i+1].Start
		}
		if end <= start {
			end = start + wordFillMax
		}
	}
	return start, min(end, start+wordFillMax)
}

// hasWords reports whether line i has timed words found in its text.
func (lay *lyricLayout) hasWords(i int) bool {
	return i >= 0 && i < len(lay.words) && lay.words[i] != nil
}

// wordsDone is when every word of line i has been sung through.
func (lay *lyricLayout) wordsDone(i int) time.Duration {
	var done time.Duration
	for j := range lay.lyr.Lines[i].Words {
		_, end := wordTimes(lay.lyr.Lines, i, j)
		done = max(done, end)
	}
	return done
}

// wordLevels is how far each word of line i has been sung at pos: 0 before
// it starts, 1 once it has been sung through, the fraction of its time in
// between. With steps a word is 1 from its start.
func (lay *lyricLayout) wordLevels(i int, pos time.Duration, steps bool) []float64 {
	ls := lay.lyr.Lines
	out := make([]float64, len(ls[i].Words))
	for j := range out {
		start, end := wordTimes(ls, i, j)
		switch {
		case pos < start:
		case steps || pos >= end:
			out[j] = 1
		default:
			out[j] = float64(pos-start) / float64(end-start)
		}
	}
	return out
}

// lineFill is the current line's fill at one moment: a level per byte of
// its text, 0 for the text colour, 1 for accent, between for the edge.
type lineFill struct {
	level  []float64
	unsung tone        // text to come: bold, so the line keeps one weight
	from   color.Color // the text colour and the accent, for the edge cell
	to     color.Color
}

// fillLevels spreads the word levels over the bytes of text. Within a word
// the fill covers level×width cells: cells before it are 1, the cell it
// ends in the part of it covered. Text between words is 1 once the word
// before is sung through.
func fillLevels(text string, spans []wordSpan, levels []float64) []float64 {
	out := make([]float64, len(text))
	prev := 0.0
	at := 0
	for j, sp := range spans {
		for k := at; k < sp.from; k++ {
			out[k] = boolLevel(prev >= 1)
		}
		f := levels[j] * float64(Width(text[sp.from:sp.to]))
		x, last := 0.0, 0.0
		for k := sp.from; k < sp.to; {
			r, n := utf8.DecodeRuneInString(text[k:])
			w := float64(ansi.StringWidth(string(r)))
			lv := last // a zero-width rune goes with the one before
			if w > 0 {
				lv = min(max((f-x)/w, 0), 1)
			}
			for b := k; b < k+n; b++ {
				out[b] = lv
			}
			x, last, k = x+w, lv, k+n
		}
		prev, at = levels[j], max(at, sp.to)
	}
	for k := at; k < len(text); k++ {
		out[k] = boolLevel(prev >= 1)
	}
	return out
}

func boolLevel(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// paint draws s, the part of the line from byte off, run by run: accent
// bold, the unsung tone, and an edge cell in a mixed colour.
func (f *lineFill) paint(p *paints, sf surface, s string, off int) string {
	var b strings.Builder
	lvAt := func(k int) float64 {
		if i := off + k; i >= 0 && i < len(f.level) {
			return quantLevel(f.level[i])
		}
		return 0
	}
	for k := 0; k < len(s); {
		lv := lvAt(k)
		_, n := utf8.DecodeRuneInString(s[k:])
		next := k + n
		if lv > 0 && lv < 1 {
			b.WriteString(f.edge(p, sf, lv, s[k:next]))
			k = next
			continue
		}
		for next < len(s) && lvAt(next) == lv {
			_, n := utf8.DecodeRuneInString(s[next:])
			next += n
		}
		t := f.unsung
		if lv == 1 {
			t = toneAccentBold
		}
		b.WriteString(p.paint(t, sf, s[k:next]))
		k = next
	}
	return b.String()
}

// quantLevel rounds a level to 1/32, so levels too close to 0 or 1 to tell
// apart draw as either.
func quantLevel(lv float64) float64 { return math.Round(lv*32) / 32 }

// edge is the cell the fill ends in: bold, its colour lv of the way from
// the text colour to the accent.
func (f *lineFill) edge(p *paints, sf surface, lv float64, s string) string {
	c := mixLinear(f.from, f.to, lv)
	fg := lipgloss.NewStyle().Foreground(c).Bold(true)
	return sgr(fg, surfaceStyle(p, sf)) + s + ansi.ResetStyle
}

func surfaceStyle(p *paints, sf surface) lipgloss.Style {
	t := p.theme
	switch sf {
	case surfSel:
		return t.Selected
	case surfHover:
		return t.Hovered
	case surfPanel:
		return t.Surface
	}
	return lipgloss.NewStyle()
}

// mixLinear is a + (b-a)*f, mixed in linear light so the middle of the
// fade is as bright as the eye expects.
func mixLinear(a, b color.Color, f float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		lx, ly := srgbToLinear(float64(x)/0xffff), srgbToLinear(float64(y)/0xffff)
		return uint8(math.Round(linearToSRGB(lx+(ly-lx)*f) * 255))
	}
	return color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}
}

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSRGB(c float64) float64 {
	c = min(max(c, 0), 1)
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

// textColor is the colour plain text has on screen: the theme's, else the
// terminal's own foreground, else a guess from the background.
func textColor(c *Ctx) color.Color {
	if fg := c.Theme.P.FG; isColor(fg) {
		return fg
	}
	if isColor(c.TermFG) {
		return c.TermFG
	}
	if c.Theme.P.Dark {
		return color.RGBA{0xE8, 0xE8, 0xEA, 0xff}
	}
	return color.RGBA{0x1C, 0x1C, 0x1E, 0xff}
}

// wordPos is the position words are timed against: the snapshot moved on
// to this moment while playing, the word lead and the track's offset.
func (v *lyricsView) wordPos(c *Ctx) time.Duration {
	pos := c.Player.Position
	if np := c.Player.NowPlaying; np.State == bridge.Playing && np.At > 0 {
		if d := time.Duration(c.Now.UnixMilli()-np.At) * time.Millisecond; d > 0 {
			pos += min(d, 250*time.Millisecond)
		}
	}
	return pos + v.svc.WordLead + v.offset()
}

// fill is line cur's fill at this moment, nil when it has no timed words.
func (v *lyricsView) fill(c *Ctx, cur int) *lineFill {
	lay := v.layout()
	if !lay.hasWords(cur) {
		return nil
	}
	accent := c.Theme.P.Accent
	colour := isColor(c.Theme.Accent.GetForeground())
	steps := !c.TrueColor || !colour
	f := &lineFill{unsung: toneBold, from: textColor(c), to: accent}
	if !colour {
		f.unsung = toneText // NO_COLOR: bold is all that marks sung words
	}
	levels := lay.wordLevels(cur, v.wordPos(c), steps)
	f.level = fillLevels(lay.texts[cur], lay.words[cur], levels)
	return f
}

// wantsFrames reports whether the current line is filling word by word, or
// about to: frames then come faster than the player tick.
func (v *lyricsView) wantsFrames(c *Ctx) bool {
	if v.w <= 0 || v.h <= 0 || c.Player.NowPlaying.State != bridge.Playing {
		return false
	}
	cur := v.current(c)
	lay := v.layout()
	if !lay.hasWords(cur) {
		return false
	}
	return v.wordPos(c) < lay.wordsDone(cur)
}

func (p *lyricsPane) wantsFrames(c *Ctx) bool { return p.v.wantsFrames(c) }
func (o *lyricsFull) wantsFrames(c *Ctx) bool { return o.v.wantsFrames(c) }

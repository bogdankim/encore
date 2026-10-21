package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/theme"
)

// rightGutter is the blank strip every view keeps at its right edge: the
// track table's scrollbar and the cell before it. Lists, grids, tables and
// title-line controls all end at contentRight, so nothing jumps between
// views or when a view switches layout.
const rightGutter = 2

// contentRight is where a view's content ends (exclusive) at width w.
func contentRight(w int) int { return max(w-rightGutter, 0) }

// detailMaxW caps the track lists of album and artist pages, so a short
// album on a wide window keeps its times next to its titles.
const detailMaxW = 108

// headerHang is the column a detail page's header starts at. The cells
// before it hold the back mark on the first row, the way a list's gutter
// holds its selection marker, so the art lines up with the titles below.
const headerHang = tableGutter

// headerGap is the space between the art and the text.
const headerGap = 3

// heroSize is the header art size for a w×h view; 0×0 folds the header
// into one line.
func heroSize(w, h int) (int, int) {
	switch {
	case w >= 120 && h >= 40:
		return 26, 13
	case w >= 90 && h >= 24:
		return 20, 10
	case w >= 60 && h >= 18:
		return 14, 7
	}
	return 0, 0
}

// pageHeader is the top of every detail page (album, playlist, folder,
// artist) and of the Artists page's right half: the back mark, art, the
// name in bold, an optional second line, dim meta lines, and Play /
// Shuffle at the foot of the art. Without room for art it is one line with
// the buttons at the right.
type pageHeader struct {
	art, artPID string // art key and the pid to fetch it with
	title       string
	sub         headerLine // second line: the album's artist, a playlist's kind; text "" for none
	meta        []string   // dim lines under it
	// tail is one more line under the meta, its parts joined by " · ",
	// each one a link or not: where an Apple Music page comes from and
	// the way to the same album in the library. Left out of the one-line
	// header.
	tail    []headerLine
	buttons []headerButton
}

// headerLine is one line of header text. A line with link is clickable and
// underlined under the pointer.
type headerLine struct {
	text string
	tone tone
	link func() tea.Cmd
	hint string // the footer's name for the link
}

// headerButton is a button in the header. The primary one is in the
// accent.
type headerButton struct {
	label   string
	primary bool
	hint    string
	do      func() tea.Cmd
}

// pageButtons are a page's Play and Shuffle, named for what the page is
// ("Play Album  P").
func pageButtons(c *Ctx, noun string, play, shuffle func() tea.Cmd) []headerButton {
	return []headerButton{
		{c.Theme.G.Play + " Play", true, c.HintFor("Play "+noun, keymap.PagePlay), play},
		{c.Theme.G.Shuffle + " Shuffle", false, c.HintFor("Shuffle "+noun, keymap.PageShuffle), shuffle},
	}
}

// openInTail is a library page's last header line: a dim link to the same
// album or artist on Apple Music (act), when there is a catalog.
func openInTail(c *Ctx, act keymap.Action, s Subject) []headerLine {
	if c.Catalog == nil {
		return nil
	}
	return []headerLine{{text: "Open in Apple Music", tone: toneDim, hint: c.HintFor(keymap.Name(act), act),
		link: func() tea.Cmd { return c.Host.OpenIn(act, s) }}}
}

// pageHints are the footer hints for a page's Play and Shuffle.
var pageHints = []FooterHint{
	{Action: keymap.PagePlay, Label: "play all"},
	{Action: keymap.PageShuffle, Label: "shuffle"},
}

// render draws the header at width w for a view h rows tall and appends
// its zones (the link, then the buttons); hover is the zone index under the
// pointer.
func (hd pageHeader) render(w, h int, c *Ctx, hover int, zones *[]albumZone) []string {
	aw, ah := heroSize(w, h)
	tx := headerHang + aw + headerGap
	if aw == 0 || contentRight(w)-tx < 20 {
		return []string{hd.compact(w, c, hover, zones)}
	}
	p := paintsFor(c.Theme)
	tw := contentRight(w) - tx

	text := []string{newLine(p, surfNone, tw).add(toneBold, hd.title).String()}
	if hd.sub.text != "" {
		text = append(text, hd.linkLine(p, tw, tx, len(text), c, hover, zones))
	}
	for _, m := range hd.meta {
		if m != "" {
			text = append(text, newLine(p, surfNone, tw).add(toneDim, m).String())
		}
	}
	if len(hd.tail) > 0 {
		text = append(text, hd.tailLine(p, tw, tx, len(text), c, hover, zones))
	}

	by := max(ah-1, len(text)+1)
	// The chips start one cell left of the text, so their labels line up
	// with the lines above.
	bx := tx - 1
	bl := newLine(p, surfNone, contentRight(w)-bx)
	hd.drawButtons(bl, bx, by, c, hover, zones)

	n := max(ah, by+1)
	art := strings.Split(c.HeroArt(hd.art, hd.artPID, headerHang, 0, aw, ah), "\n")
	lines := make([]string, n)
	for y := range n {
		var b strings.Builder
		lead := newLine(p, surfNone, headerHang)
		if y == 0 {
			c.BackMark(lead, 0, 0)
		}
		b.WriteString(lead.String())
		switch {
		case y < len(art) && y < ah:
			b.WriteString(Fit(art[y], aw))
		default:
			b.WriteString(strings.Repeat(" ", aw))
		}
		col := headerHang + aw
		switch {
		case y < len(text):
			b.WriteString(strings.Repeat(" ", headerGap))
			b.WriteString(text[y])
			col = contentRight(w)
		case y == by && len(hd.buttons) > 0:
			b.WriteString(strings.Repeat(" ", headerGap-1))
			b.WriteString(bl.String())
			col = contentRight(w)
		}
		b.WriteString(strings.Repeat(" ", w-col))
		lines[y] = b.String()
	}
	return lines
}

// linkLine draws the second line at view column x, row y: underlined
// under the pointer when it is a link.
func (hd pageHeader) linkLine(p *paints, tw, x, y int, c *Ctx, hover int, zones *[]albumZone) string {
	hl := hd.sub
	l := newLine(p, surfNone, tw)
	if hl.link == nil {
		return l.add(hl.tone, hl.text).String()
	}
	s := truncate(hl.text, tw)
	sw := Width(s)
	if hover == len(*zones) {
		l.raw(underlined(c.Theme, s), sw)
	} else {
		l.add(hl.tone, s)
	}
	*zones = append(*zones, albumZone{x0: x, x1: x + sw, y: y, do: hl.link, hint: hl.hint})
	return l.String()
}

// tailLine draws the tail parts at view column x, row y, links
// underlined under the pointer.
func (hd pageHeader) tailLine(p *paints, tw, x, y int, c *Ctx, hover int, zones *[]albumZone) string {
	l := newLine(p, surfNone, tw)
	sep := " " + c.Theme.G.Dot + " "
	for i, part := range hd.tail {
		if i > 0 {
			if l.col()+Width(sep)+Width(part.text) > tw {
				break
			}
			l.add(toneDim, sep)
		}
		s := truncate(part.text, tw-l.col())
		sw := Width(s)
		switch {
		case part.link != nil && hover == len(*zones):
			l.raw(underlined(c.Theme, s), sw)
		default:
			l.add(part.tone, s)
		}
		if part.link != nil {
			*zones = append(*zones, albumZone{x0: x + l.col() - sw, x1: x + l.col(), y: y, do: part.link, hint: part.hint})
		}
	}
	return l.String()
}

// buttonsWidth is the width of the button row: chips with one blank cell
// each side, two cells apart.
func (hd pageHeader) buttonsWidth() int {
	n := 0
	for i, b := range hd.buttons {
		if i > 0 {
			n += 2
		}
		n += Width(b.label) + 2
	}
	return n
}

// drawButtons writes the chips on l, which starts at view column x0, on
// view row y. A hovered chip takes the hover surface and turns bold.
func (hd pageHeader) drawButtons(l *line, x0, y int, c *Ctx, hover int, zones *[]albumZone) {
	p := l.p
	for i, b := range hd.buttons {
		if i > 0 {
			l.pad(2)
		}
		tn, sf := toneText, surfNone
		if b.primary {
			tn = toneAccent
		}
		if hover == len(*zones) {
			sf = surfHover
			tn = toneBold
			if b.primary {
				tn = toneAccentBold
			}
		}
		start := x0 + l.col()
		bw := Width(b.label) + 2
		l.raw(p.paint(tn, sf, " "+b.label+" "), bw)
		*zones = append(*zones, albumZone{x0: start, x1: start + bw, y: y, do: b.do, hint: b.hint})
	}
}

// compact is the header in one line: the back mark, the name, the second
// line (or the last meta line) in dim, and the buttons at the right edge.
// The meta goes first when space runs short, then the buttons.
func (hd pageHeader) compact(w int, c *Ctx, hover int, zones *[]albumZone) string {
	p := paintsFor(c.Theme)
	right := contentRight(w)
	l := newLine(p, surfNone, w)
	c.BackMark(l, 0, 0)
	l.to(headerHang)
	if !c.Back {
		l = newLine(p, surfNone, w) // no back mark: the name starts the line, like a title line
	}
	bw := hd.buttonsWidth()
	bx := right - bw + 1 // the last chip's trailing blank may sit in the gutter
	tw := Width(hd.title)
	if len(hd.buttons) == 0 || bx < l.col()+min(tw, 12)+3 {
		bx = right
		hd.buttons = nil
	}
	l.fit(toneBold, hd.title, min(tw, bx-3-l.col()))
	sub := hd.sub
	if sub.text == "" && len(hd.meta) > 0 {
		sub = headerLine{text: hd.meta[len(hd.meta)-1], tone: toneDim}
	}
	room := bx - l.col() - 3 - 2
	if sub.link == nil {
		sub.text = fitMeta(sub.text, room)
	}
	if room >= 6 && sub.text != "" {
		l.pad(3)
		x := l.col()
		s := truncate(sub.text, room)
		switch {
		case sub.link != nil && hover == len(*zones):
			l.raw(underlined(c.Theme, s), Width(s))
		case sub.link != nil:
			l.add(toneDim, s)
		default:
			l.add(sub.tone, s)
		}
		if sub.link != nil {
			*zones = append(*zones, albumZone{x0: x, x1: x + Width(s), y: 0, do: sub.link, hint: sub.hint})
		}
	}
	if len(hd.buttons) > 0 {
		l.to(bx)
		hd.drawButtons(l, 0, 0, c, hover, zones)
	}
	return l.String()
}

// underlined is s in the text colour, underlined: a link under the pointer.
func underlined(t *theme.Theme, s string) string {
	st := ansi.Style{}.Underline(true)
	if isColor(t.P.FG) {
		st = st.ForegroundColor(t.P.FG)
	}
	return st.String() + s + ansi.ResetStyle
}

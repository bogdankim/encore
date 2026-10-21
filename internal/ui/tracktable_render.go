package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/theme"
)

// Render draws the header and the visible rows into exactly w×h cells. The
// last column holds the scrollbar.
func (t *TrackTable) Render(w, h int, c *Ctx) string {
	if w < tableGutter+8 || h <= 0 {
		return Blank(w, h)
	}
	t.width, t.bodyH = w, h-1
	t.layout(w)
	t.clamp()
	p := paintsFor(c.Theme)
	t.prepareCache(c)

	lines := make([]string, 0, h)
	lines = append(lines, t.header(p, w))
	bh := h - 1
	if len(t.rows) == 0 {
		msg := t.opts.Empty
		if t.query != "" {
			msg = "Nothing matches “" + t.query + "”"
		}
		for i := 0; i < bh; i++ {
			if i == (bh-1)/3 {
				lines = append(lines, Center(p.paint(toneDim, surfNone, msg), w))
			} else {
				lines = append(lines, strings.Repeat(" ", w))
			}
		}
		return strings.Join(lines, "\n")
	}

	playing := c.PlayingTrackPID()
	thumbY, thumbH := scrollThumb(len(t.rows), bh, t.top)
	g := &c.Theme.G
	for i := 0; i < bh; i++ {
		bar := " "
		if thumbH > 0 {
			if i >= thumbY && i < thumbY+thumbH {
				bar = p.paint(toneDim, surfNone, g.ScrollThumb)
			} else {
				bar = p.paint(toneFaint, surfNone, g.ScrollTrack)
			}
		}
		idx := t.top + i
		if idx >= len(t.rows) {
			lines = append(lines, strings.Repeat(" ", w-1)+bar)
			continue
		}
		s := t.rowString(t.rows[idx], idx, t.cursor, t.hover, c.Focused, playing, p, c, w-1)
		lines = append(lines, s+bar)
	}
	return strings.Join(lines, "\n")
}

// prepareCache drops cached rows drawn with another theme, or too many.
func (t *TrackTable) prepareCache(c *Ctx) {
	if t.cacheTheme != c.Theme || len(t.cache) > maxRowCache {
		t.cache, t.cacheTheme = nil, c.Theme
	}
	if t.cache == nil {
		t.cache = make(map[rowKey]string, 128)
	}
}

// rowString is row idx (track tr) w cells wide, from the cache when it can.
func (t *TrackTable) rowString(tr *library.Track, idx, cursor, hover int, focused bool, playing string, p *paints, c *Ctx, w int) string {
	var st uint8
	if idx == cursor {
		st |= rowSel
		if focused {
			st |= rowFocus
		}
	}
	if idx == hover {
		st |= rowHover
	}
	if tr.PID == playing {
		st |= rowPlaying
	}
	if c.IsFavorite(tr) {
		st |= rowFav
	}
	k := rowKey{tr, w, st}
	s, ok := t.cache[k]
	if !ok {
		s = t.row(tr, st, p, &c.Theme.G, w)
		t.cache[k] = s
	}
	return s
}

// listRows draws every track as a row w cells wide, with no header and no
// scrollbar (its column stays blank, so the rows end where a table's do),
// for a short list that keeps its own cursor and hover: an artist's top
// songs. cursor and hover are row indexes, -1 for none.
func (t *TrackTable) listRows(w, cursor, hover int, focused bool, c *Ctx) []string {
	lines := make([]string, len(t.rows))
	if w < tableGutter+8 {
		for i := range lines {
			lines[i] = Blank(w, 1)
		}
		return lines
	}
	t.width = w
	t.layout(w)
	p := paintsFor(c.Theme)
	t.prepareCache(c)
	playing := c.PlayingTrackPID()
	for i, tr := range t.rows {
		lines[i] = t.rowString(tr, i, cursor, hover, focused, playing, p, c, w-1) + " "
	}
	return lines
}

// scrollThumb returns the thumb position and size for n rows in h, or a zero
// size when everything fits.
func scrollThumb(n, h, top int) (y, size int) {
	if n <= h || h <= 0 {
		return 0, 0
	}
	size = max(1, h*h/n)
	if n > h {
		y = top * (h - size) / (n - h)
	}
	return min(y, h-size), size
}

func (t *TrackTable) layout(w int) {
	if w == t.layW {
		return
	}
	t.layW = w
	t.cols = t.columns(w)
	t.cache = nil
}

// columns computes column positions for a table w cells wide: fixed widths
// for numbers, times and the heart, the rest shared by Title, Artist and
// Album, which drop out in that order (Album first) when space runs short.
func (t *TrackTable) columns(w int) []colSpec {
	cw := w - 1 - tableGutter - 1 // scrollbar and its margin
	var cols []Column
	for _, c := range t.opts.Columns {
		if c == ColTime {
			if cw >= wideFrom {
				cols = append(cols, t.opts.Wide...)
			}
			if !t.opts.NoHeart {
				cols = append(cols, ColHeart)
			}
		}
		cols = append(cols, c)
	}
	flexAvail := func(cs []Column) int {
		n := cw
		for i, c := range cs {
			n -= t.fixedWidth(c)
			if i < len(cs)-1 {
				n -= gapAfter(c)
			}
		}
		return n
	}
	for _, drop := range []struct {
		c    Column
		need int
	}{{ColAlbum, 56}, {ColArtist, 30}} {
		if flexAvail(cols) < drop.need {
			cols = without(cols, drop.c)
		}
	}

	avail := flexAvail(cols)
	weight := map[Column]int{ColTitle: 10, ColArtist: 7, ColAlbum: 7}
	sum := 0
	for _, c := range cols {
		sum += weight[c]
	}
	specs := make([]colSpec, 0, len(cols))
	x, given := tableGutter, 0
	flexLeft := sum
	for _, c := range cols {
		cs := colSpec{c: c, x: x, w: t.fixedWidth(c), right: c == ColTime || c == ColPlays || c == ColNumber}
		if wt := weight[c]; wt > 0 {
			flexLeft -= wt
			if flexLeft == 0 {
				cs.w = avail - given
			} else {
				cs.w = avail * wt / sum
			}
			given += cs.w
		}
		cs.w = max(cs.w, 1)
		specs = append(specs, cs)
		x += cs.w + gapAfter(c)
	}
	return specs
}

// gapAfter is the space after a column. The heart column carries its own
// trailing cell, so Time follows it closely: "♥  3:26".
func gapAfter(c Column) int {
	if c == ColHeart {
		return 1
	}
	return columnGap
}

func (t *TrackTable) fixedWidth(c Column) int {
	switch c {
	case ColTime:
		return t.timeW
	case ColPlays:
		return 5
	case ColAdded:
		return 12
	case ColNumber:
		return 2
	case ColHeart:
		return heartW
	}
	return 0
}

func without(cs []Column, drop Column) []Column {
	out := cs[:0:0]
	for _, c := range cs {
		if c != drop {
			out = append(out, c)
		}
	}
	return out
}

func (t *TrackTable) header(p *paints, w int) string {
	l := newLine(p, surfNone, w)
	for _, cs := range t.cols {
		if cs.c == ColHeart {
			continue
		}
		name := columnNames[cs.c]
		tn := toneDim
		if int(cs.c) == t.hoverCol {
			tn = toneText
		}
		arrow := ""
		if t.sortSet && t.sortCol == cs.c {
			arrow = "↑"
			if t.desc {
				arrow = "↓"
			}
		}
		if cs.right {
			if arrow != "" {
				l.to(cs.x - 2)
				l.add(toneDim, arrow)
			}
			l.to(cs.x)
			l.right(tn, name, cs.w)
			continue
		}
		l.to(cs.x)
		if arrow != "" && ansi.StringWidth(name)+2 <= cs.w {
			l.add(tn, name)
			l.add(toneDim, " "+arrow)
			l.to(cs.x + cs.w)
		} else {
			l.fit(tn, name, cs.w)
		}
	}
	return l.String()
}

// row renders one track row, w cells wide (the scrollbar is added later).
func (t *TrackTable) row(tr *library.Track, st uint8, p *paints, g *theme.Glyphs, w int) string {
	sf := surfNone
	switch {
	case st&rowSel != 0 && st&rowFocus != 0:
		sf = surfSel
	case st&rowHover != 0:
		sf = surfHover
	}
	l := newLine(p, sf, w)
	switch {
	case st&rowSel != 0 && st&rowFocus != 0:
		l.add(toneAccent, g.Marker)
	case st&rowSel != 0:
		l.add(toneDim, g.Marker)
	default:
		l.pad(1)
	}
	playing := st&rowPlaying != 0
	dead := !tr.Available
	switch {
	case playing:
		l.add(toneAccent, g.Playing)
	case dead:
		l.add(toneFaint, g.Blocked)
	default:
		l.pad(1)
	}
	l.pad(1)

	main, sub := toneText, toneDim
	if dead {
		main = toneDim
	} else if playing {
		main = toneAccent
	}
	for _, cs := range t.cols {
		l.to(cs.x)
		switch cs.c {
		case ColTitle:
			titleCell(l, tr, false, main, sub, g, cs.w)
		case ColHeart:
			heartCell(l, g, st&rowFav != 0, playing, st&rowHover != 0 && !dead)
		case ColArtist:
			l.fit(sub, tr.Artist, cs.w)
		case ColAlbum:
			l.fit(sub, tr.AlbumName, cs.w)
		case ColPlays:
			if tr.Plays > 0 {
				l.right(sub, count(tr.Plays), cs.w)
			}
		case ColAdded:
			l.fit(sub, shortDate(tr.Added), cs.w)
		case ColTime:
			l.right(sub, clock(tr.Duration), cs.w)
		case ColNumber:
			if tr.TrackNo > 0 {
				l.right(sub, itoa(tr.TrackNo), cs.w)
			}
		}
	}
	return l.String()
}

// heartW is the favorite column: the glyph and the cell after it.
const heartW = 2

// titleCell writes the title with its explicit tag one space after it; the
// title gives way first when space is short. fav adds a heart after the
// title too, for lists without a heart column (prefer heartCell).
//
// Tags are painted together with the cell after them: Nerd Font icons are
// drawn wider than one cell and only overflow into a following blank of the
// same style, so a row background stays unbroken under them.
func titleCell(l *line, tr *library.Track, fav bool, main, sub tone, g *theme.Glyphs, w int) {
	end := l.col() + w
	tags := ""
	if tr.Explicit {
		tags = g.Explicit + " "
	}
	if fav {
		tags += g.Heart + " "
	}
	tagsW := 0
	if tags != "" {
		tagsW = 1 + ansi.StringWidth(tags)
	}
	if w-tagsW < 6 {
		tags, tagsW = "", 0
	}
	l.fit(main, tr.Title, min(ansi.StringWidth(tr.Title), w-tagsW))
	if tags != "" {
		l.pad(1).add(sub, tags)
	}
	l.to(end)
}

// heartCell fills the two-cell favorite column: the heart in dim for a
// favorite (accent on the playing row), a faint outline on the hovered row
// to show it can be clicked, otherwise blank.
func heartCell(l *line, g *theme.Glyphs, fav, playing, hover bool) {
	switch {
	case fav && playing:
		l.add(toneAccent, g.Heart+" ")
	case fav:
		l.add(toneDim, g.Heart+" ")
	case hover:
		l.add(toneFaint, g.HeartOutline+" ")
	default:
		l.pad(heartW)
	}
}

package ui

import (
	"image"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

func init() { overlayCtors.LyricsFull = newLyricsFull }

const (
	fullWideFrom = 100 // cols: the art column shows
	fullMeasure  = 72  // widest lyric line, in cells
	fullClose    = "×"
)

// lyricsFull is full-screen lyrics: the art, title, progress and transport
// on the left third, the lyrics centred in the rest.
type lyricsFull struct {
	v      *lyricsView
	sw, sh int

	// from the last Render, in screen cells
	area       image.Rectangle // lyrics
	art        image.Rectangle
	bar        image.Rectangle // progress line
	btns       [3]image.Rectangle
	close      image.Rectangle
	hoverClose bool
	hoverBtn   int  // -1 when none
	seeking    bool // dragging on the progress line
}

// fullBtnActs are the transport buttons under the art, left to right.
var fullBtnActs = [3]keymap.Action{keymap.PlayerPrev, keymap.PlayerPlayPause, keymap.PlayerNext}

const fullBtnGap = 4

func newLyricsFull(s Services) Overlay {
	return &lyricsFull{v: newLyricsView(s, fullLyricStyle), hoverBtn: -1}
}

func (o *lyricsFull) Rect(sw, sh int) image.Rectangle {
	o.sw, o.sh = sw, sh
	return image.Rect(0, 0, sw, sh)
}

func (o *lyricsFull) CapturesInput() bool                { return false }
func (o *lyricsFull) KeyContext() keymap.Context         { return keymap.Lyrics }
func (o *lyricsFull) Key(tea.KeyPressMsg, *Ctx) tea.Cmd  { return nil }
func (o *lyricsFull) Update(msg tea.Msg, c *Ctx) tea.Cmd { return o.v.update(msg, c) }

func (o *lyricsFull) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.LyricsFullscreen:
		return true, c.Host.CloseOverlay()
	case keymap.Back:
		// esc first ends a free scroll, then closes
		if ok, cmd := o.v.action(a, n, c); ok {
			return true, cmd
		}
		return true, c.Host.CloseOverlay()
	}
	return o.v.action(a, n, c)
}

func (o *lyricsFull) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	pt := image.Pt(ev.X, ev.Y)
	switch ev.Kind {
	case MouseMotion:
		if o.seeking && ev.Button == tea.MouseLeft {
			o.seek(ev.X, c)
			return nil
		}
		o.hoverClose = pt.In(o.close)
		o.hoverBtn = -1
		for i, r := range o.btns {
			if pt.In(r) {
				o.hoverBtn = i
			}
		}
	case MouseRelease:
		if o.seeking {
			o.seeking = false
			return nil
		}
	case MousePress:
		if ev.Button != tea.MouseLeft {
			break
		}
		switch {
		case pt.In(o.close), ev.Double && pt.In(o.art):
			return c.Host.CloseOverlay()
		case pt.In(o.bar):
			o.seeking = true
			o.seek(ev.X, c)
			return nil
		}
		for i, r := range o.btns {
			if pt.In(r) {
				return c.Host.Do(fullBtnActs[i])
			}
		}
	}
	return o.v.mouse(ev.local(o.area.Min.X, o.area.Min.Y), c)
}

// seek moves playback to the point of the progress line at screen column x.
func (o *lyricsFull) seek(x int, c *Ctx) {
	d := c.Player.Duration
	if d <= 0 || o.v.svc.Player == nil || o.bar.Empty() {
		return
	}
	f := float64(x-o.bar.Min.X) / float64(max(o.bar.Dx()-1, 1))
	f = min(max(f, 0), 1)
	o.v.svc.Player.Seek(time.Duration(f * float64(d)))
}

func (o *lyricsFull) Render(c *Ctx) string {
	w, h := o.sw, o.sh
	if w <= 0 || h <= 0 {
		return ""
	}
	p := paintsFor(c.Theme)
	o.bar, o.art, o.btns = image.Rectangle{}, image.Rectangle{}, [3]image.Rectangle{}
	lw := 0 // the art column
	var header string
	if w >= fullWideFrom {
		lw = w / 3
		o.area = image.Rect(lw+2, 2, w-4, h-2)
	} else {
		header = o.header(c, p, w-10)
		o.area = image.Rect(4, 4, w-4, h-2)
	}
	if mw := o.area.Dx(); mw > fullMeasure {
		o.area.Min.X += (mw - fullMeasure) / 2
		o.area.Max.X = o.area.Min.X + fullMeasure
	}
	side := o.side(lw, h, c, p)
	var body []string
	if !o.area.Empty() {
		body = o.v.render(o.area.Dx(), o.area.Dy(), c)
	}
	// × sits two cells from the right edge; a cell either side of it counts
	o.close = image.Rect(w-4, 1, w-1, 2)
	closeBtn := p.spaces(surfNone, 1) + p.paint(toneDim, surfNone, fullClose) + p.spaces(surfNone, 1)
	if o.hoverClose {
		closeBtn = p.spaces(surfHover, 1) + p.paint(toneText, surfHover, fullClose) + p.spaces(surfHover, 1)
	}

	rows := make([]string, h)
	for y := range rows {
		var b strings.Builder
		b.WriteString(side[y])
		rw := w - lw
		switch {
		case y == 1:
			x := 0
			if header != "" {
				x = (w - Width(header)) / 2
				b.WriteString(strings.Repeat(" ", x))
				b.WriteString(header)
				x += Width(header)
			}
			b.WriteString(strings.Repeat(" ", max(rw-4-x, 0)))
			b.WriteString(closeBtn)
			b.WriteString(" ")
		case y >= o.area.Min.Y && y < o.area.Max.Y && body != nil:
			ax := o.area.Min.X - lw
			b.WriteString(strings.Repeat(" ", ax))
			b.WriteString(body[y-o.area.Min.Y])
			b.WriteString(strings.Repeat(" ", rw-ax-o.area.Dx()))
		default:
			b.WriteString(strings.Repeat(" ", rw))
		}
		rows[y] = b.String()
	}
	return strings.Join(rows, "\n")
}

// side draws the art column, lw cells wide: art, title, artist, a slim
// progress line and the transport buttons, centred.
func (o *lyricsFull) side(lw, h int, c *Ctx, p *paints) []string {
	out := make([]string, h)
	blank := strings.Repeat(" ", lw)
	for y := range out {
		out[y] = blank
	}
	ct := c.Player.NowPlaying.Track
	if lw == 0 || ct == nil {
		return out
	}
	title, artist := ct.Name, ct.Artist
	if t := c.Player.Track; t != nil {
		title, artist = t.Title, t.Artist
	}
	aw := min(max(lw-10, 16), 36, 2*(h-12))
	aw -= aw % 2
	ah := aw / 2
	if aw < 12 {
		aw, ah = min(lw-8, 36), 0
	}
	if aw <= 0 {
		return out
	}
	block := 7
	if ah > 0 {
		block += ah + 1
	}
	y := max((h-block)/2-1, 1)
	x := max((lw-aw)/2+1, 2)
	put := func(s string) {
		if y >= 0 && y < h {
			out[y] = strings.Repeat(" ", x) + s + strings.Repeat(" ", max(lw-x-aw, 0))
		}
		y++
	}
	if ah > 0 {
		o.art = image.Rect(x, y, x+aw, y+ah)
		art := strings.Split(Box(c.NowPlayingArt(x, y, aw, ah), aw, ah), "\n")
		for _, s := range art {
			put(s)
		}
		y++
	}
	put(newLine(p, surfNone, aw).add(toneBold, title).String())
	put(newLine(p, surfNone, aw).add(toneDim, artist).String())
	y++
	if d := c.Player.Duration; d > 0 {
		pos := min(c.Player.Position, d)
		o.bar = image.Rect(x, y, x+aw, y+1)
		put(progressBar(p, &c.Theme.G, float64(pos)/float64(d), aw))
		right := "-" + clock(d-pos)
		put(newLine(p, surfNone, aw).add(toneDim, clock(pos)).to(aw-Width(right)).add(toneDim, right).String())
	}
	y++
	if y < h {
		o.btns = fullBtnRects(&c.Theme.G, x, y, aw)
		put(o.transport(c, p, x, aw))
	}
	return out
}

// fullBtnGlyphs are the transport glyphs: previous, play or pause, next.
func fullBtnGlyphs(g *theme.Glyphs, playing bool) [3]string {
	play := g.Play
	if playing {
		play = g.Pause
	}
	return [3]string{g.Prev, play, g.Next}
}

// fullBtnRects are the click targets of the transport row at screen row y,
// centred in aw cells from x: each glyph's slot and a cell either side.
func fullBtnRects(g *theme.Glyphs, x, y, aw int) [3]image.Rectangle {
	var ws [3]int
	total := 2 * fullBtnGap
	for i, gl := range [3]string{g.Prev, g.Pause, g.Next} {
		ws[i] = max(slotW(gl), slotW(g.Play))
		total += ws[i]
	}
	bx := x + max((aw-total)/2, 0)
	var out [3]image.Rectangle
	for i := range out {
		out[i] = image.Rect(bx-1, y, bx+ws[i]+1, y+1)
		bx += ws[i] + fullBtnGap
	}
	return out
}

// transport draws the button row, aw cells wide; a hovered button gets the
// hover surface.
func (o *lyricsFull) transport(c *Ctx, p *paints, x, aw int) string {
	np := c.Player.NowPlaying
	gl := fullBtnGlyphs(&c.Theme.G, np.State == bridge.Playing || c.Player.Loading)
	l := newLine(p, surfNone, aw)
	for i, r := range o.btns {
		sf, tn := surfNone, toneDim
		if i == 1 {
			tn = toneText
		}
		if i == o.hoverBtn {
			sf, tn = surfHover, toneText
		}
		l.to(r.Min.X - x)
		inner := r.Dx() - 2
		sw := Width(gl[i])
		l.raw(p.spaces(sf, 1+(inner-sw)/2), 1+(inner-sw)/2)
		l.raw(p.paint(tn, sf, gl[i]), sw)
		rest := r.Dx() - 1 - (inner-sw)/2 - sw
		l.raw(p.spaces(sf, rest), rest)
	}
	return l.String()
}

// header is the narrow layout's title line, at most w cells.
func (o *lyricsFull) header(c *Ctx, p *paints, w int) string {
	ct := c.Player.NowPlaying.Track
	if ct == nil || w <= 0 {
		return ""
	}
	title, artist := ct.Name, ct.Artist
	if t := c.Player.Track; t != nil {
		title, artist = t.Title, t.Artist
	}
	tw := min(Width(title), w)
	l := newLine(p, surfNone, w).fit(toneBold, title, tw)
	if artist != "" && w-tw > 6 {
		l.pad(2).add(toneDim, artist)
	}
	n := l.col()
	return ansi.Truncate(l.String(), n, "")
}

// nowPlayingArt is the art key and pid of the playing track. A catalog
// song's art comes by URL, under its album's catalog key.
func nowPlayingArt(c *Ctx) (key, pid string) {
	if it := c.Player.Catalog; it != nil {
		if it.ArtURL == "" {
			return "", ""
		}
		key = player.CatalogAlbumPrefix + it.AlbumID
		if it.AlbumID == "" {
			key = player.StorePID(it.StoreID)
		}
		return key, it.ArtURL
	}
	if t := c.Player.Track; t != nil {
		key, pid = t.PID, t.PID
		if a := t.Album; a != nil {
			key = a.Key
			if a.ArtPID != "" {
				pid = a.ArtPID
			}
		}
		return key, pid
	}
	if ct := c.Player.NowPlaying.Track; ct != nil {
		return ct.PID, ct.PID
	}
	return "", ""
}

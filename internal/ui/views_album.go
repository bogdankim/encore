package ui

import (
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// AlbumView is an album's page: a header with large art, title, artist,
// genre and year and Play / Shuffle buttons, then the track list with disc
// headings. The header stands in for the title line.
type AlbumView struct {
	album  *library.Album
	rows   []albumRow
	rowOf  []int // track index → row index
	byArt  bool  // some track's artist differs from the album artist
	timeW  int   // the Time column's width
	cursor int   // track index
	top    int   // first visible row
	hover  int   // track index under the pointer, -1 when none

	w, h   int
	listY  int // first line of the track list, from the last Render
	listH  int
	zones  []albumZone
	hoverZ int
	press  pressGuard
	placed bool // the first render has put the cursor on the playing track
	// want is the track the first render puts the cursor on instead, when
	// the page was opened to show it (Open in).
	want trackWant

	// Set for an Apple Music album (CatalogAlbumView): no favorite
	// column, and it plays as a catalog context.
	noHeart bool
	kind    player.Kind
}

// trackWant names a track a page should select once its tracks are there:
// by pid, or failing that by title.
type trackWant struct{ pid, title string }

// find returns the index of the wanted track in ts, or -1.
func (w trackWant) find(ts []*library.Track) int {
	if w.pid != "" {
		for i, t := range ts {
			if t.PID == w.pid {
				return i
			}
		}
	}
	if key := songMatchKey(w.title); key != "" {
		for i, t := range ts {
			if songMatchKey(t.Title) == key {
				return i
			}
		}
	}
	return -1
}

// wanted and setWant hand a wanted track over to the open page when Open
// is asked for the page already shown.
func (v *AlbumView) wanted() trackWant { return v.want }

func (v *AlbumView) setWant(w trackWant) {
	v.want, v.placed = w, false
}

// albumRow is a track (track ≥ 0), a disc heading (disc > 0) or a blank line.
type albumRow struct{ track, disc int }

// NewAlbumView opens album a.
func NewAlbumView(a *library.Album) *AlbumView {
	v := &AlbumView{hover: -1, hoverZ: -1}
	v.bind(a)
	return v
}

func (v *AlbumView) bind(a *library.Album) {
	v.album = a
	v.rows = v.rows[:0]
	v.rowOf = make([]int, len(a.Tracks))
	v.byArt = false
	multi := false
	var longest time.Duration
	for _, t := range a.Tracks {
		longest = max(longest, t.Duration)
		if t.Disc != a.Tracks[0].Disc {
			multi = true
		}
		if t.Artist != "" && t.Artist != a.Artist {
			v.byArt = true
		}
	}
	v.timeW = clockWidth(longest)
	disc := -1
	for i, t := range a.Tracks {
		if multi && t.Disc != disc {
			if disc != -1 {
				v.rows = append(v.rows, albumRow{track: -1})
			}
			v.rows = append(v.rows, albumRow{track: -1, disc: max(t.Disc, 1)})
			disc = t.Disc
		}
		v.rowOf[i] = len(v.rows)
		v.rows = append(v.rows, albumRow{track: i})
	}
	v.cursor = min(v.cursor, max(len(a.Tracks)-1, 0))
}

func (v *AlbumView) Key() string { return "album:" + v.album.Key }

// Album is the album shown.
func (v *AlbumView) Album() *library.Album { return v.album }

func (v *AlbumView) Title(c *Ctx) (string, string) {
	return v.album.Title, v.album.Artist
}

func (v *AlbumView) Reindex(lib *library.Library) View {
	a := lib.Album(v.album.Key)
	if a == nil {
		return nil
	}
	pid := ""
	if t := v.selectedTrack(); t != nil {
		pid = t.PID
	}
	v.bind(a)
	for i, t := range a.Tracks {
		if t.PID == pid {
			v.cursor = i
		}
	}
	v.hover = -1
	v.ensureVisible()
	return v
}

func (v *AlbumView) selectedTrack() *library.Track {
	if v.cursor < 0 || v.cursor >= len(v.album.Tracks) {
		return nil
	}
	return v.album.Tracks[v.cursor]
}

func (v *AlbumView) Selected(c *Ctx) []*library.Track {
	if t := v.selectedTrack(); t != nil {
		return []*library.Track{t}
	}
	return nil
}

func (v *AlbumView) source() player.Source {
	kind := player.KindAlbum
	if v.kind != "" {
		kind = v.kind
	}
	return player.Source{Kind: kind, ID: v.album.Key, Title: v.album.Title}
}

func (v *AlbumView) play(i int, c *Ctx) tea.Cmd {
	if len(v.album.Tracks) == 0 {
		return nil
	}
	return c.Host.Play(v.source(), v.album.Tracks, i)
}

// shuffle plays the album shuffled, from a random track.
func (v *AlbumView) shuffle(c *Ctx) tea.Cmd {
	n := len(v.album.Tracks)
	if n == 0 {
		return nil
	}
	return shufflePlay(c, v.source(), v.album.Tracks, rand.IntN(n))
}

// shufflePlay turns encore's shuffle on (when it is off) and plays ts from
// ts[start]; the player orders the rest at random. Every Shuffle button and
// PageShuffle comes here.
func shufflePlay(c *Ctx, src player.Source, ts []*library.Track, start int) tea.Cmd {
	if len(ts) == 0 {
		return nil
	}
	if c.Player.NowPlaying.Shuffle {
		return c.Host.Play(src, ts, start)
	}
	on := c.Host.Do(keymap.PlayerShuffle) // before Play: the host may act at once
	return tea.Sequence(on, c.Host.Play(src, ts, start))
}

func (v *AlbumView) artistOf() *library.Artist {
	if len(v.album.Tracks) == 0 {
		return nil
	}
	return v.album.Tracks[0].Group
}

func (v *AlbumView) ensureVisible() {
	if len(v.rowOf) == 0 || v.listH <= 0 {
		v.top = 0
		return
	}
	r := v.rowOf[v.cursor]
	if r < v.top {
		v.top = r
		// Keep the heading above the first track of a disc in view.
		for v.top > 0 && v.rows[v.top-1].track < 0 && r-v.top < 2 {
			v.top--
		}
	}
	if r >= v.top+v.listH {
		v.top = r - v.listH + 1
	}
	v.clampTop()
}

func (v *AlbumView) clampTop() {
	v.top = max(min(v.top, len(v.rows)-v.listH), 0)
}

func (v *AlbumView) moveTo(i int) {
	if len(v.album.Tracks) == 0 {
		return
	}
	v.cursor = min(max(i, 0), len(v.album.Tracks)-1)
	v.ensureVisible()
}

func (v *AlbumView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	n = max(n, 1)
	page := max(v.listH, 1)
	switch a {
	case keymap.NavDown:
		v.moveTo(v.cursor + n)
	case keymap.NavUp:
		v.moveTo(v.cursor - n)
	case keymap.NavTop:
		v.moveTo(0)
	case keymap.NavBottom:
		v.moveTo(len(v.album.Tracks) - 1)
	case keymap.NavHalfDown:
		v.moveTo(v.cursor + n*max(page/2, 1))
	case keymap.NavHalfUp:
		v.moveTo(v.cursor - n*max(page/2, 1))
	case keymap.NavPageDown:
		v.moveTo(v.cursor + n*page)
	case keymap.NavPageUp:
		v.moveTo(v.cursor - n*page)
	case keymap.Open:
		return true, v.play(v.cursor, c)
	case keymap.PagePlay:
		return true, v.play(0, c)
	case keymap.PageShuffle:
		return true, v.shuffle(c)
	case keymap.TrackFavorite, keymap.QueueAdd, keymap.QueuePlayNext, keymap.Menu:
		sel := v.Selected(c)
		if sel == nil {
			return true, nil
		}
		switch a {
		case keymap.TrackFavorite:
			return true, c.Host.ToggleFavorite(sel)
		case keymap.QueueAdd:
			return true, c.Host.AddToQueue(sel)
		case keymap.QueuePlayNext:
			return true, c.Host.PlayNext(sel)
		}
		y := v.listY + v.rowOf[v.cursor] - v.top
		return true, c.Host.Menu(c.Origin.X+tableGutter, c.Origin.Y+y, sel)
	default:
		return false, nil
	}
	return true, nil
}

// trackAt maps a local cell to a track index, or -1.
func (v *AlbumView) trackAt(x, y int) int {
	if y < v.listY || y >= v.listY+v.listH || x < 0 || x >= v.listW() {
		return -1
	}
	r := v.top + y - v.listY
	if r >= len(v.rows) {
		return -1
	}
	return v.rows[r].track
}

func (v *AlbumView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	v.hoverZ = zoneAt(v.zones, ev.X, ev.Y)
	switch ev.Kind {
	case MouseWheel:
		v.top += wheelRows * ev.wheelDelta()
		v.clampTop()
		v.hover = v.trackAt(ev.X, ev.Y)
	case MouseMotion:
		v.hover = v.trackAt(ev.X, ev.Y)
		v.hint(ev.X, c)
	case MousePress:
		double := v.press.double(ev)
		if v.hoverZ >= 0 && ev.Button == tea.MouseLeft {
			return v.zones[v.hoverZ].do()
		}
		i := v.trackAt(ev.X, ev.Y)
		if i < 0 {
			return nil
		}
		v.cursor = i
		switch {
		case ev.Button == tea.MouseRight:
			return c.Host.Menu(ev.ScreenX, ev.ScreenY, v.Selected(c))
		case ev.Button == tea.MouseLeft && v.onHeart(ev.X) && v.album.Tracks[i].Available:
			return c.Host.ToggleFavorite(v.Selected(c))
		case ev.Button == tea.MouseLeft && double:
			return v.play(i, c)
		}
	}
	return nil
}

// onHeart reports whether local column x is on the favorite column.
func (v *AlbumView) onHeart(x int) bool {
	if v.noHeart {
		return false
	}
	hx := v.cols(v.listW()).heartX
	return x >= hx && x < hx+heartW
}

// hint names what the pointer rests on: a header control, the favorite
// toggle, or a track Music can't play.
func (v *AlbumView) hint(x int, c *Ctx) {
	if v.hoverZ >= 0 {
		c.Hint(v.zones[v.hoverZ].hint)
		return
	}
	if v.hover < 0 {
		return
	}
	if s := trackTip(v.album.Tracks[v.hover], v.onHeart(x), c); s != "" {
		c.Hint(s)
	}
}

func (v *AlbumView) Render(w, h int, c *Ctx) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	v.w, v.h = w, h
	lw := v.listW()
	lines := v.header(lw, h, c)
	if len(lines) > 0 {
		lines = append(lines, "") // blank line under the header
	}
	v.listY = len(lines)
	v.listH = max(h-v.listY, 0)
	if !v.placed {
		v.placed = true
		v.showPlaying(c)
	}
	v.clampTop()
	lines = append(lines, v.trackLines(lw, c)...)
	return Lines(lines, w, h)
}

// listW is the track list's width: the view's, up to detailMaxW.
func (v *AlbumView) listW() int { return min(v.w, detailMaxW) }

// showPlaying puts the cursor on the wanted track, else on the playing
// track when it is on this album, in the middle of the list: the page was
// opened to see it.
func (v *AlbumView) showPlaying(c *Ctx) {
	if w := v.want; w != (trackWant{}) {
		v.want = trackWant{}
		if i := w.find(v.album.Tracks); i >= 0 {
			v.cursor = i
			v.top = v.rowOf[i] - v.listH/2
			v.clampTop()
			return
		}
	}
	pid := c.PlayingTrackPID()
	for i, t := range v.album.Tracks {
		if pid != "" && t.PID == pid {
			v.cursor = i
			v.top = v.rowOf[i] - v.listH/2
			v.clampTop()
			return
		}
	}
}

// header returns the header lines and records the clickable zones: the
// artist, Play and Shuffle.
func (v *AlbumView) header(w, h int, c *Ctx) []string {
	a := v.album
	v.zones = v.zones[:0]
	hd := pageHeader{
		art: a.Key, artPID: a.ArtPID,
		title: a.Title,
		sub: headerLine{text: a.Artist, tone: toneText, hint: "Go to Artist", link: func() tea.Cmd {
			if ar := v.artistOf(); ar != nil {
				return c.Host.OpenArtist(ar)
			}
			return nil
		}},
		meta:    []string{joinMeta(a.Genre, yearText(a.Year)), songsMeta(len(a.Tracks), a.Duration)},
		tail:    openInTail(c, keymap.OpenAlbumInCatalog, Subject{album: a}),
		buttons: pageButtons(c, "Album", func() tea.Cmd { return v.play(0, c) }, func() tea.Cmd { return v.shuffle(c) }),
	}
	return hd.render(w, h, c, v.hoverZ, &v.zones)
}

// albumCols are the track list's column widths, and where the heart
// column starts.
type albumCols struct{ num, title, artist, time, heartX int }

// cols lays the list out in w cells, ending at contentRight(w) like every
// list: marker, number, title (and artist on compilations), heart, time.
func (v *AlbumView) cols(w int) albumCols {
	const num = 3
	tm := v.timeW
	right := contentRight(w)
	cols := albumCols{num: num, time: tm, heartX: right - tm - 1 - heartW}
	if v.noHeart {
		cols.heartX = right - tm - 1
	}
	rest := cols.heartX - columnGap - (1 + num + columnGap)
	cols.title = rest
	if v.byArt && rest > 30 {
		cols.artist = rest * 3 / 10
		cols.title = rest - cols.artist - columnGap
	}
	return cols
}

func (v *AlbumView) trackLines(w int, c *Ctx) []string {
	t := c.Theme
	p := paintsFor(t)
	cols := v.cols(w)
	playing := c.PlayingTrackPID()
	lines := make([]string, 0, v.listH)
	for r := v.top; r < len(v.rows) && len(lines) < v.listH; r++ {
		row := v.rows[r]
		if row.track < 0 {
			l := newLine(p, surfNone, w)
			if row.disc > 0 {
				l.pad(1+cols.num+columnGap).add(toneDim, "Disc "+itoa(row.disc))
			}
			lines = append(lines, l.String())
			continue
		}
		tr := v.album.Tracks[row.track]
		st := itemStateOf(row.track, v.cursor, v.hover, c.Focused)
		l := newLine(p, st.surface(), w)
		st.marker(l, t.G.Marker)
		isPlaying, dead := tr.PID == playing, !tr.Available
		main, sub := toneText, toneDim
		switch {
		case isPlaying:
			l.right(toneAccent, t.G.Playing, cols.num)
			main = toneAccent
		case dead:
			l.right(toneFaint, t.G.Blocked, cols.num)
			main = toneDim
		default:
			l.right(toneDim, trackNumber(tr, row.track), cols.num)
		}
		l.pad(columnGap)
		titleCell(l, tr, false, main, sub, &t.G, cols.title)
		if cols.artist > 0 {
			artist := ""
			if tr.Artist != v.album.Artist {
				artist = tr.Artist
			}
			l.pad(columnGap).fit(sub, artist, cols.artist)
		}
		l.to(cols.heartX)
		if !v.noHeart {
			heartCell(l, &t.G, c.IsFavorite(tr), isPlaying, row.track == v.hover && !dead)
		}
		l.pad(1).right(sub, clock(tr.Duration), cols.time)
		lines = append(lines, l.String())
	}
	return lines
}

func trackNumber(tr *library.Track, i int) string {
	if tr.TrackNo > 0 {
		return itoa(tr.TrackNo)
	}
	return itoa(i + 1)
}

// FooterHints are the track list's keys and the header's.
func (v *AlbumView) FooterHints(c *Ctx) []FooterHint {
	return append([]FooterHint{
		{Keys: keysHint(c, keymap.NavDown, keymap.NavUp), Label: "move"},
		{Action: keymap.Open, Label: "play"},
	}, pageHints...)
}

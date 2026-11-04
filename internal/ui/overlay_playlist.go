package ui

import (
	"fmt"
	"image"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
)

// playlistRow is one row of the picker: a playlist, or the row that creates
// one named after the filter (pl nil).
type playlistRow struct {
	pl     *library.Playlist
	folder string // the folders it sits in, "" at the top level
	has    bool   // already holds every song being added
	pos    []int
}

// PlaylistPicker adds songs to a playlist the user picks. The field filters
// the playlists; text that names none offers a new playlist by that name.
type PlaylistPicker struct {
	in     ovInput
	tracks []*library.Track
	recent string // the playlist added to last goes first
	all    []playlistRow
	rows   []playlistRow
	cur    ovCursor
	built  bool

	w, h         int
	listY, listH int
}

// NewPlaylistPicker opens the picker for tracks, which must all be in the
// library. recent is the pid of the playlist to list first, or "".
func NewPlaylistPicker(tracks []*library.Track, recent string) Overlay {
	return &PlaylistPicker{in: newOvInput("Find or name a playlist"), tracks: tracks, recent: recent, cur: ovCursor{Hover: -1}}
}

// playlistEditable reports whether songs can be added to p. Music refuses
// folders, smart and Genius playlists, its own Library and Music playlists,
// and Apple Music playlists the user follows.
func playlistEditable(p *library.Playlist) bool {
	return p.Kind == "user" && p.Special == ""
}

// playlistHasAll reports whether p already holds every track.
func playlistHasAll(p *library.Playlist, tracks []*library.Track) bool {
	return len(playlistMissing(p, tracks)) == 0
}

// playlistMissing is tracks without the ones p already holds or that repeat
// an earlier one.
func playlistMissing(p *library.Playlist, tracks []*library.Track) []*library.Track {
	seen := make(map[string]bool, len(p.Tracks)+len(tracks))
	for _, t := range p.Tracks {
		seen[t.PID] = true
	}
	var out []*library.Track
	for _, t := range tracks {
		if !seen[t.PID] {
			seen[t.PID] = true
			out = append(out, t)
		}
	}
	return out
}

func playlistFolder(p *library.Playlist) string {
	var parts []string
	for f := p.Parent; f != nil; f = f.Parent {
		parts = append(parts, f.Name)
	}
	slices.Reverse(parts)
	return strings.Join(parts, " / ")
}

func (p *PlaylistPicker) build(c *Ctx) {
	if p.built {
		return
	}
	p.built = true
	if c.Lib != nil {
		for _, pl := range c.Lib.PlaylistsFlat {
			if !playlistEditable(pl) {
				continue
			}
			r := playlistRow{pl: pl, folder: playlistFolder(pl), has: playlistHasAll(pl, p.tracks)}
			if pl.PID == p.recent {
				p.all = slices.Insert(p.all, 0, r)
			} else {
				p.all = append(p.all, r)
			}
		}
	}
	p.filter()
}

func (p *PlaylistPicker) filter() {
	p.rows = p.rows[:0]
	q := strings.TrimSpace(p.in.Value())
	if q == "" {
		p.rows = append(p.rows, p.all...)
		p.cur.First(len(p.rows), p.ok)
		return
	}
	type scored struct {
		row   playlistRow
		score int
	}
	var hits []scored
	exact := false
	for _, r := range p.all {
		exact = exact || strings.EqualFold(r.pl.Name, q)
		if sc, pos, ok := ovFuzzy(r.pl.Name, q); ok {
			r.pos = pos
			hits = append(hits, scored{r, sc})
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int { return b.score - a.score })
	for _, h := range hits {
		p.rows = append(p.rows, h.row)
	}
	if !exact {
		p.rows = append(p.rows, playlistRow{})
	}
	p.cur.First(len(p.rows), p.ok)
}

func (p *PlaylistPicker) ok(int) bool { return true }

func (p *PlaylistPicker) move(d int) {
	p.cur.Step(d, len(p.rows), p.ok)
	p.cur.Follow(p.listH, len(p.rows), p.ok)
}

func (p *PlaylistPicker) run(i int, c *Ctx) tea.Cmd {
	if i < 0 || i >= len(p.rows) {
		return nil
	}
	if pl := p.rows[i].pl; pl != nil {
		return ovClose(c, func(h Host) tea.Cmd { return h.AddToPlaylist(pl, p.tracks) })
	}
	name := strings.TrimSpace(p.in.Value())
	return ovClose(c, func(h Host) tea.Cmd { return h.AddToNewPlaylist(name, p.tracks) })
}

// Rect shrinks to the playlists listed, keeping its top where it was so the
// field doesn't move.
func (p *PlaylistPicker) Rect(screenW, screenH int) image.Rectangle {
	r := ovPlace(screenW, screenH, ovSize(screenW, 50, 50, 72), ovSize(screenH, 60, 12, 26))
	if p.built {
		// border, title, field and rule, rows, hints
		r.Max.Y = r.Min.Y + min(r.Dy(), 2+3+max(len(p.rows), paletteMinRows)+2)
	}
	p.w, p.h = r.Dx(), r.Dy()
	return r
}

func (p *PlaylistPicker) CapturesInput() bool { return true }

func (p *PlaylistPicker) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	p.build(c)
	switch a {
	case keymap.InputNext, keymap.NavDown:
		p.move(n)
	case keymap.InputPrev, keymap.NavUp:
		p.move(-n)
	case keymap.InputSubmit, keymap.Open:
		return true, p.run(p.cur.Sel, c)
	case keymap.InputCancel, keymap.Back:
		return true, c.Host.CloseOverlay()
	case keymap.InputNextScope, keymap.InputPrevScope:
	default:
		return false, nil
	}
	return true, nil
}

func (p *PlaylistPicker) Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	p.build(c)
	if p.in.Key(msg) {
		p.filter()
	}
	return nil
}

// Paste puts pasted text in the field.
func (p *PlaylistPicker) Paste(text string, c *Ctx) tea.Cmd {
	p.build(c)
	if p.in.Paste(text) {
		p.filter()
	}
	return nil
}

func (p *PlaylistPicker) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	row := -1
	if y := ev.Y - p.listY; y >= 0 && y < p.listH && p.cur.Off+y < len(p.rows) {
		row = p.cur.Off + y
	}
	switch ev.Kind {
	case MouseMotion:
		p.cur.Hover = row
	case MouseWheel:
		p.cur.Scroll(ovWheel(ev), p.listH, len(p.rows))
	case MousePress:
		if row >= 0 && ev.Button == tea.MouseLeft {
			return p.run(row, c)
		}
	}
	return nil
}

func (p *PlaylistPicker) Update(tea.Msg, *Ctx) tea.Cmd { return nil }

// title names what is being added.
func (p *PlaylistPicker) title() string {
	if len(p.tracks) == 1 {
		return "Add " + p.tracks[0].Title + " to"
	}
	return "Add " + plural(len(p.tracks), "song", "songs") + " to"
}

func (p *PlaylistPicker) Render(c *Ctx) string {
	p.build(c)
	t := c.Theme
	w, h := p.w, p.h
	if w == 0 {
		w, h = 60, 20
	}
	inner, ih := w-2, h-2
	if inner < 4 || ih < 1 {
		return ovFrame(t, w, h, nil)
	}
	var body []string
	if ih >= 5 {
		body = append(body, ovPad(t.Dim.Render(p.title()), inner))
	}
	body = append(body, p.in.View(t, "+", inner))
	if ih >= 6 {
		body = append(body, ovRule(t, inner))
	}
	header := len(body)
	hints := ih >= header+1+2
	p.listY = 1 + header
	p.listH = max(0, ih-header)
	if hints {
		p.listH -= 2
	}
	p.cur.Clamp(p.listH, len(p.rows))

	cw := inner - 3
	for y := 0; y < p.listH && p.cur.Off+y < len(p.rows); y++ {
		i := p.cur.Off + y
		bar := ovBar(t, y, p.listH, p.cur.Off, len(p.rows))
		body = append(body, ovRow(t, p.rowLine(p.rows[i], c, cw), inner-1, i == p.cur.Sel, i == p.cur.Hover)+bar)
	}
	if len(p.rows) == 0 && p.listH > 0 {
		body = append(body, ovCenterLine(t, "Type a name for a new playlist", inner))
	}
	if hints {
		for len(body) < ih-1 {
			body = append(body, "")
		}
		body = append(body, ovHints(t, inner, ovHint{"↑↓", "select"}, ovHint{"enter", "add"}, ovHint{"esc", "close"}))
	}
	return ovFrame(t, w, h, body)
}

// rowLine is a playlist's name with its folder and a mark when it already
// holds the songs, or the row that creates a playlist.
func (p *PlaylistPicker) rowLine(r playlistRow, c *Ctx, w int) string {
	t := c.Theme
	if r.pl == nil {
		return Fit(t.Accent.Render(fmt.Sprintf("New Playlist “%s”", strings.TrimSpace(p.in.Value()))), w)
	}
	side := r.folder
	if r.has {
		side = strings.TrimSpace(t.G.InLib + " " + side)
	}
	sw := min(Width(side), w/2)
	return Row(2, Cell{Text: ovMark(r.pl.Name, r.pos, t.Text, t.Bold), W: w - sw - 2}, Cell{Text: t.Dim.Render(side), W: sw, Right: true})
}

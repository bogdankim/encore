package ui

import (
	"math/rand/v2"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// inputView is implemented by views that can hold a text field (the inline
// filter). While CapturesInput is true keys go through keymap.Input and the
// rest reach InputKey.
type inputView interface {
	CapturesInput() bool
	InputKey(msg tea.KeyPressMsg, c *Ctx) tea.Cmd
	Paste(s string)
}

// tableTop is how many rows of Songs and Favorites sit above the table: the
// title and the filter line (blank when no filter is shown). Playlists put a
// taller header there (see tracksView.header).
const tableTop = 2

// tracksView is Songs, Favorites and playlists: a title line (a header with
// art and Play / Shuffle for playlists) and a track table.
type tracksView struct {
	key   string
	title string
	pid   string            // playlists
	pl    *library.Playlist // playlists and folders, nil for Songs and Favorites
	empty string
	table *TrackTable

	top    int         // rows above the table at the last render
	zones  []albumZone // header buttons, from the last render
	hoverZ int
	placed bool // the first render has put the cursor on the playing track
}

func newSongsView(lib *library.Library) View {
	return &tracksView{
		key: KeySongs, title: "Songs", empty: "No songs in your library", hoverZ: -1, top: tableTop,
		table: NewTrackTable(lib.Songs, TableOptions{
			Source:   player.Source{Kind: player.KindSongs, Title: "Songs"},
			SortedBy: library.ByTitle,
			Wide:     []Column{ColPlays, ColAdded},
		}),
	}
}

func newFavoritesView(lib *library.Library) View {
	return &tracksView{
		key: KeyFavorites, title: "Favorites", empty: "No favorites yet", hoverZ: -1, top: tableTop,
		table: NewTrackTable(lib.Favorites, TableOptions{
			Source:   player.Source{Kind: player.KindFavorites, Title: "Favorites"},
			SortedBy: library.ByTitle,
			Wide:     []Column{ColPlays, ColAdded},
			NoHeart:  true,
		}),
	}
}

func newPlaylistView(p *library.Playlist) View {
	empty := "This playlist is empty"
	if p.Kind == "folder" {
		empty = "No songs in this folder"
	}
	return &tracksView{
		key: "playlist:" + p.PID, title: p.Name, pid: p.PID, pl: p, empty: empty, hoverZ: -1, top: tableTop,
		table: NewTrackTable(playlistTracks(p), TableOptions{
			Source:  player.Source{Kind: player.KindPlaylist, ID: p.PID, Title: p.Name},
			Natural: true,
			Wide:    []Column{ColPlays, ColAdded},
		}),
	}
}

// playlistTracks is a playlist's tracks, or for a folder every track in the
// playlists under it, first appearance first.
func playlistTracks(p *library.Playlist) []*library.Track {
	if p.Kind != "folder" {
		return p.Tracks
	}
	seen := map[string]bool{}
	var out []*library.Track
	var walk func(*library.Playlist)
	walk = func(p *library.Playlist) {
		for _, t := range p.Tracks {
			if !seen[t.PID] {
				seen[t.PID] = true
				out = append(out, t)
			}
		}
		for _, ch := range p.Children {
			walk(ch)
		}
	}
	walk(p)
	return out
}

// playlistKind names a playlist's kind for its header, or "" for a plain
// playlist of the user's, where the page already says it.
func playlistKind(p *library.Playlist) string {
	switch p.Kind {
	case "folder":
		return joinMeta("Folder", plural(len(p.Children), "playlist", "playlists"))
	case "smart":
		return "Smart Playlist"
	case "subscription":
		return "Apple Music Playlist"
	case "genius":
		return "Genius Playlist"
	}
	return ""
}

func (v *tracksView) Key() string { return v.key }

func (v *tracksView) Title(c *Ctx) (string, string) {
	return v.title, v.stats()
}

// stats is "17 songs · 1 hr 2 min", or "12 of 340 songs" while filtered.
func (v *tracksView) stats() string {
	t := v.table
	switch {
	case t.Filtered():
		return count(t.Len()) + " of " + plural(t.Total(), "song", "songs")
	case t.Total() == 0:
		return ""
	}
	return songsMeta(t.Total(), t.Duration())
}

func (v *tracksView) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.PagePlay:
		return true, v.play(c)
	case keymap.PageShuffle:
		return true, v.shuffle(c)
	}
	return v.table.Action(a, n, c.at(0, v.top))
}

// FooterHints are the table's keys; a playlist's page adds its header's
// Play and Shuffle, Songs and Favorites the filter.
func (v *tracksView) FooterHints(c *Ctx) []FooterHint {
	hs := []FooterHint{
		{Keys: keysHint(c, keymap.NavDown, keymap.NavUp), Label: "move"},
		{Action: keymap.Open, Label: "play"},
	}
	if v.pl != nil && !v.filtering() {
		return append(hs, pageHints...)
	}
	hs = append(hs, FooterHint{Action: keymap.PageShuffle, Label: "shuffle"})
	if v.filtering() {
		return append(hs, FooterHint{Keys: "esc", Label: "clear filter"})
	}
	return append(hs, FooterHint{Action: keymap.Filter, Label: "filter"})
}

func (v *tracksView) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	t := v.table
	tc := c.at(0, v.top)
	// A scrollbar drag goes on, and ends, wherever the pointer is.
	if ev.Kind == MouseRelease || (t.scrollDrag && ev.Kind == MouseMotion) {
		v.hoverZ = -1
		return t.Mouse(ev.local(0, v.top), tc)
	}
	if ev.Y >= v.top {
		v.hoverZ = -1
		return t.Mouse(ev.local(0, v.top), tc)
	}
	t.Mouse(leave, tc)
	v.hoverZ = zoneAt(v.zones, ev.X, ev.Y)
	if v.hoverZ >= 0 && ev.Kind == MouseMotion {
		c.Hint(v.zones[v.hoverZ].hint)
	}
	if ev.Kind != MousePress || ev.Button != tea.MouseLeft {
		return nil
	}
	if v.hoverZ >= 0 {
		return v.zones[v.hoverZ].do()
	}
	if ev.Y == v.top-1 && t.FilterShown() {
		t.typing = true
	}
	return nil
}

func (v *tracksView) Render(w, h int, c *Ctx) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	if c.LibraryLoading && v.table.Total() == 0 {
		v.table.opts.Empty = "Loading your library…"
	} else {
		v.table.opts.Empty = v.empty
	}
	var lines []string
	if v.pl != nil {
		lines = v.header(w, h, c)
	} else {
		v.zones = v.zones[:0]
		lines = []string{titleLine(c, w, v.title, v.stats())}
	}
	if len(lines) < h {
		if v.table.FilterShown() {
			lines = append(lines, v.table.filterLine(w, c))
		} else {
			lines = append(lines, strings.Repeat(" ", w))
		}
	}
	lines = lines[:min(len(lines), h)]
	v.top = len(lines)
	if !v.placed && v.pl != nil && h > v.top {
		v.placed = true
		v.table.Center(c.PlayingTrackPID(), h-v.top-1)
	}
	if h > v.top {
		lines = append(lines, v.table.Render(w, h-v.top, c))
	}
	return strings.Join(lines, "\n")
}

// header is a playlist or folder page's header (see pageHeader): art, the
// name, its kind when that says more than "playlist", its length, and Play
// / Shuffle.
func (v *tracksView) header(w, h int, c *Ctx) []string {
	v.zones = v.zones[:0]
	key, pid := c.PlaylistArt(v.pl)
	noun := "Playlist"
	if v.pl.Kind == "folder" {
		noun = "Folder"
	}
	hd := pageHeader{
		art: key, artPID: pid,
		title: v.title,
		sub:   headerLine{text: playlistKind(v.pl), tone: toneDim},
		meta:  []string{v.stats()},
	}
	if v.table.Len() > 0 {
		hd.buttons = pageButtons(c, noun, func() tea.Cmd { return v.play(c) }, func() tea.Cmd { return v.shuffle(c) })
	}
	return hd.render(w, h, c, v.hoverZ, &v.zones)
}

// firstPlayable is the first row Music can play at or after i, wrapping, or
// -1 when none can be.
func (v *tracksView) firstPlayable(i int) int {
	rows := v.table.Rows()
	for k := range rows {
		j := (i + k) % len(rows)
		if rows[j].Available {
			return j
		}
	}
	return -1
}

// play starts the list as shown from its first playable row.
func (v *tracksView) play(c *Ctx) tea.Cmd {
	i := v.firstPlayable(0)
	if i < 0 || c.Host == nil {
		return nil
	}
	return c.Host.Play(v.table.source(), v.table.Rows(), i)
}

// shuffle turns shuffle on (when it is off) and starts from a random
// playable row; the player orders the rest at random.
func (v *tracksView) shuffle(c *Ctx) tea.Cmd {
	n := v.table.Len()
	if n == 0 || c.Host == nil {
		return nil
	}
	i := v.firstPlayable(rand.IntN(n))
	if i < 0 {
		return nil
	}
	return shufflePlay(c, v.table.source(), v.table.Rows(), i)
}

func (v *tracksView) Selected(c *Ctx) []*library.Track { return v.table.Selected() }

func (v *tracksView) Reindex(lib *library.Library) View {
	switch v.key {
	case KeySongs:
		v.table.SetTracks(lib.Songs)
	case KeyFavorites:
		v.table.SetTracks(lib.Favorites)
	default:
		p := lib.Playlist(v.pid)
		if p == nil {
			return nil
		}
		v.title, v.pl = p.Name, p
		v.table.opts.Source.Title = p.Name
		v.table.SetTracks(playlistTracks(p))
	}
	return v
}

func (v *tracksView) CapturesInput() bool { return v.table.Typing() }

func (v *tracksView) InputKey(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	v.table.Key(msg)
	return nil
}

func (v *tracksView) Paste(s string) { v.table.Paste(s) }

func (v *tracksView) filtering() bool { return v.table.Filtered() }

// titleLine is the one-line view title: the name in bold and a dim meta to
// its right.
func titleLine(c *Ctx, w int, title, meta string) string {
	p := paintsFor(c.Theme)
	l := newLine(p, surfNone, w)
	c.BackMark(l, 0, 0) // "‹ " on detail pages; titleLine is the view's row 0
	l.add(toneBold, title)
	if meta = fitMeta(meta, w-l.col()-3); meta != "" {
		l.pad(3).add(toneDim, meta)
	}
	return l.String()
}

// fitMeta shortens a dim meta ("3,158 songs · 9 days") to room cells by
// dropping parts from the end, or to "" when not even the first part fits:
// a count cut to "…" says nothing.
func fitMeta(meta string, room int) string {
	for meta != "" && Width(meta) > room {
		i := strings.LastIndex(meta, " · ")
		if i < 0 {
			return ""
		}
		meta = meta[:i]
	}
	return meta
}

// filterLine is the inline filter: the search glyph, the query and, while
// typing, a cursor.
func (t *TrackTable) filterLine(w int, c *Ctx) string {
	p := paintsFor(c.Theme)
	l := newLine(p, surfNone, w)
	l.add(toneDim, c.Theme.G.Search).pad(1)
	if t.typing {
		l.add(toneText, t.query).add(toneAccent, "▏")
	} else {
		l.add(toneDim, t.query)
	}
	return l.String()
}

// at returns a copy of c with Origin moved by (dx, dy).
func (c *Ctx) at(dx, dy int) *Ctx {
	cc := *c
	cc.Origin.X += dx
	cc.Origin.Y += dy
	return &cc
}

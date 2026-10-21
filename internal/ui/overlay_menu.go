package ui

import (
	"image"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// MenuItem is one entry of a context menu. An item with an empty Label is a
// separator.
type MenuItem struct {
	Label string
	// Key is an action whose first binding is shown as the shortcut.
	Key keymap.Action
	Run func(h Host) tea.Cmd

	favOf  []*library.Track // set on the favorite toggle: its label follows Ctx.IsFavorite
	openIn *Subject         // set on Open in items: Init drops them where they can't apply
}

// MenuSeparator is a rule between groups of items.
var MenuSeparator = MenuItem{}

const menuKeyW = 5

// MenuOverlay is a small context menu at the pointer.
type MenuOverlay struct {
	x, y  int
	items []MenuItem
	cur   ovCursor
	w, h  int
	keyW  int // the shortcut column, menuKeyW until Init measures it
}

// NewMenu opens the context menu for tracks at a screen position. extra items
// are appended after a separator. A pane with actions of its own adds them
// this way and opens the menu itself, for example the Up Next pane:
//
//	c.Host.PushOverlay(NewMenu(x, y, tracks, MenuItem{
//		Label: "Remove from Up Next", Key: keymap.QueueRemove,
//		Run:   func(h Host) tea.Cmd { return remove(tracks) },
//	}))
//
// The menu opens on the row below (x, y), so the row it is for stays in
// view, and above it when there's no room below. Keyboard callers pass the
// selected row and the column where the menu should start.
func NewMenu(screenX, screenY int, tracks []*library.Track, extra ...MenuItem) Overlay {
	return newMenuFor(screenX, screenY, tracks, subjectOf(tracks), extra...)
}

// newMenuFor is NewMenu with Open in acting on s, for a selection whose
// tracks don't say what it is: every song of an artist with one album.
func newMenuFor(screenX, screenY int, tracks []*library.Track, s Subject, extra ...MenuItem) Overlay {
	items := trackMenuItemsFor(tracks, s)
	if len(extra) > 0 {
		if len(items) > 0 {
			items = append(items, MenuSeparator)
		}
		items = append(items, extra...)
	}
	m := &MenuOverlay{x: screenX, y: screenY, items: items, cur: ovCursor{Hover: -1}}
	m.cur.First(len(items), m.ok)
	return m
}

// trackMenuItems are the standard actions for one or more tracks. Several
// tracks of one album read as an album: no Go to Album.
func trackMenuItems(ts []*library.Track) []MenuItem {
	return trackMenuItemsFor(ts, subjectOf(ts))
}

// trackMenuItemsFor is trackMenuItems with Open in acting on s.
func trackMenuItemsFor(ts []*library.Track, s Subject) []MenuItem {
	if len(ts) == 0 {
		return nil
	}
	if allCatalog(ts) {
		return catalogMenuItems(ts)
	}
	first := ts[0]
	album, artist := first.Album, first.Group
	for _, t := range ts {
		if t.Album != album {
			album = nil
		}
		if t.Group != artist {
			artist = nil
		}
	}
	isAlbum := len(ts) > 1 && album != nil && len(ts) == len(album.Tracks)

	src := player.Source{Kind: player.KindSongs, Title: first.Title}
	switch {
	case isAlbum:
		src = player.Source{Kind: player.KindAlbum, ID: album.Key, Title: album.Title}
	case len(ts) > 1:
		src = player.Source{Kind: player.KindSongs, Title: plural(len(ts), "Song", "Songs")}
	}
	items := playMenuItems(src, ts)
	var nav []MenuItem
	if album != nil && !isAlbum {
		nav = append(nav, MenuItem{Label: "Go to Album", Run: func(h Host) tea.Cmd { return h.OpenAlbum(album) }})
	}
	if artist != nil {
		nav = append(nav, MenuItem{Label: "Go to Artist", Run: func(h Host) tea.Cmd { return h.OpenArtist(artist) }})
	}
	items = menuGroup(menuGroup(items, nav...), openInItems(false, s)...)
	return append(items, MenuSeparator,
		MenuItem{Label: keymap.Name(keymap.TrackFavorite), Key: keymap.TrackFavorite, favOf: ts})
}

// menuGroup appends a group of items to a menu after a separator, or
// nothing for an empty group.
func menuGroup(items []MenuItem, group ...MenuItem) []MenuItem {
	if len(group) == 0 {
		return items
	}
	if len(items) > 0 {
		items = append(items, MenuSeparator)
	}
	return append(items, group...)
}

// playMenuItems are Play, Play Next and Add to Up Next for ts, played as src.
// Play is just "Play" whatever the target: the menu sits on it.
func playMenuItems(src player.Source, ts []*library.Track) []MenuItem {
	return []MenuItem{
		{Label: "Play", Key: keymap.Open, Run: func(h Host) tea.Cmd { return h.Play(src, ts, 0) }},
		{Label: keymap.Name(keymap.QueuePlayNext), Key: keymap.QueuePlayNext, Run: func(h Host) tea.Cmd { return h.PlayNext(ts) }},
		{Label: keymap.Name(keymap.QueueAdd), Key: keymap.QueueAdd, Run: func(h Host) tea.Cmd { return h.AddToQueue(ts) }},
	}
}

// NewPlaylistMenu is the context menu for a playlist or a folder of them:
// the play items for its songs and nothing else. Favorite and Go to would
// act on every song in it. A playlist without songs gets a menu that says
// so.
func NewPlaylistMenu(screenX, screenY int, pl *library.Playlist) Overlay {
	tracks := playlistTracks(pl)
	items := []MenuItem{{Label: "No Songs"}}
	if len(tracks) > 0 {
		src := player.Source{Kind: player.KindPlaylist, ID: pl.PID, Title: pl.Name}
		if pl.Kind == "folder" {
			src = player.Source{Kind: player.KindSongs, Title: pl.Name}
		}
		items = playMenuItems(src, tracks)
	}
	m := &MenuOverlay{x: screenX, y: screenY, items: items, cur: ovCursor{Hover: -1}}
	m.cur.First(len(items), m.ok)
	return m
}

// PlaylistMenu opens NewPlaylistMenu for pl at a screen cell.
func (a *App) PlaylistMenu(screenX, screenY int, pl *library.Playlist) tea.Cmd {
	if pl == nil {
		return nil
	}
	return a.PushOverlay(NewPlaylistMenu(screenX, screenY, pl))
}

// favoriteTargets are the tracks the favorite toggle changes, so that it
// does what its label says: the ones that aren't favorites yet, or all of
// them when every one is (Host.ToggleFavorite flips them as a group).
func favoriteTargets(ts []*library.Track, c *Ctx) []*library.Track {
	var out []*library.Track
	for _, t := range ts {
		if !c.IsFavorite(t) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return ts
	}
	return out
}

// menuLabel is the item's label; the favorite toggle reads Unfavorite only
// when every track already is one.
func menuLabel(it MenuItem, c *Ctx) string {
	if it.favOf == nil {
		return it.Label
	}
	for _, t := range it.favOf {
		if !c.IsFavorite(t) {
			return "Favorite"
		}
	}
	return "Unfavorite"
}

func (m *MenuOverlay) ok(i int) bool { return m.items[i].Label != "" }

// Init drops the Open in items that can't apply here (no catalog, not in
// the library) and the separators left doubled, and sizes the shortcut
// column to the keys shown.
func (m *MenuOverlay) Init(c *Ctx) tea.Cmd {
	var items []MenuItem
	for _, it := range m.items {
		if it.openIn != nil && !openInApplies(it.Key, *it.openIn, c) {
			continue
		}
		if it.Label == "" && (len(items) == 0 || items[len(items)-1].Label == "") {
			continue
		}
		items = append(items, it)
	}
	for len(items) > 0 && items[len(items)-1].Label == "" {
		items = items[:len(items)-1]
	}
	m.items = items
	m.keyW = menuKeyW
	for _, it := range items {
		m.keyW = max(m.keyW, Width(m.key(it, c)))
	}
	m.cur = ovCursor{Hover: -1}
	m.cur.First(len(m.items), m.ok)
	return nil
}

// key is the shortcut shown at the right of it: its action's first key.
func (m *MenuOverlay) key(it MenuItem, c *Ctx) string {
	if it.Key == "" || c.Keys == nil {
		return ""
	}
	if ks := c.Keys.KeysFor(it.Key); len(ks) > 0 {
		return ovKeys(ks[0])
	}
	return ""
}

func (m *MenuOverlay) keyWidth() int { return max(m.keyW, menuKeyW) }

func (m *MenuOverlay) Rect(screenW, screenH int) image.Rectangle {
	labelW := 0
	for _, it := range m.items {
		labelW = max(labelW, Width(it.Label))
	}
	w := min(screenW, labelW+m.keyWidth()+3+4)
	h := min(screenH, len(m.items)+2)
	x, y := m.x, m.y+1
	if x+w > screenW {
		x = m.x - w + 1
	}
	if y+h > screenH {
		y = m.y - h
	}
	x = max(0, min(x, screenW-w))
	y = max(0, min(y, screenH-h))
	m.w, m.h = w, h
	return image.Rect(x, y, x+w, y+h)
}

func (m *MenuOverlay) CapturesInput() bool { return false }

func (m *MenuOverlay) run(i int, c *Ctx) tea.Cmd {
	if i < 0 || i >= len(m.items) || !m.ok(i) {
		return nil
	}
	it := m.items[i]
	if it.favOf != nil {
		ts := favoriteTargets(it.favOf, c)
		return ovClose(c, func(h Host) tea.Cmd { return h.ToggleFavorite(ts) })
	}
	if it.Run == nil {
		return nil
	}
	return ovClose(c, it.Run)
}

func (m *MenuOverlay) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.NavDown, keymap.InputNext:
		m.cur.Step(n, len(m.items), m.ok)
	case keymap.NavUp, keymap.InputPrev:
		m.cur.Step(-n, len(m.items), m.ok)
	case keymap.NavTop:
		m.cur.First(len(m.items), m.ok)
	case keymap.NavBottom:
		m.cur.Step(len(m.items), len(m.items), m.ok)
	case keymap.Open, keymap.InputSubmit:
		return true, m.run(m.cur.Sel, c)
	case keymap.Back, keymap.InputCancel, keymap.Menu, keymap.Quit:
		return true, c.Host.CloseOverlay()
	default:
		// an item's shortcut, shown at its right, runs it
		for i, it := range m.items {
			if it.Key == a && m.ok(i) {
				return true, m.run(i, c)
			}
		}
		// anything else stays in the menu: it is modal
		return true, nil
	}
	return true, nil
}

func (m *MenuOverlay) Key(tea.KeyPressMsg, *Ctx) tea.Cmd { return nil }

func (m *MenuOverlay) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	i := ev.Y - 1
	if i < 0 || i >= len(m.items) || ev.X <= 0 || ev.X >= m.w-1 || !m.ok(i) {
		i = -1
	}
	switch ev.Kind {
	case MouseMotion:
		if i >= 0 {
			m.cur.Sel = i
		}
	case MousePress, MouseRelease:
		if ev.Button == tea.MouseLeft && i >= 0 && ev.Kind == MousePress {
			return m.run(i, c)
		}
	}
	return nil
}

func (m *MenuOverlay) Update(tea.Msg, *Ctx) tea.Cmd { return nil }

func (m *MenuOverlay) Render(c *Ctx) string {
	t := c.Theme
	w, h := m.w, m.h
	if w == 0 {
		w, h = 30, len(m.items)+2
	}
	inner := w - 2
	if inner < 4 {
		return ovFrame(t, w, h, nil)
	}
	cw := inner - 2
	body := make([]string, 0, len(m.items))
	for i, it := range m.items {
		if !m.ok(i) {
			body = append(body, ovRule(t, inner))
			continue
		}
		key := m.key(it, c)
		label := t.Text.Render(menuLabel(it, c))
		if it.Run == nil && it.favOf == nil {
			label = t.Dim.Render(it.Label) // a note, such as "No Songs"
		}
		kw := m.keyWidth()
		line := Row(2, Cell{Text: label, W: cw - kw - 2}, Cell{Text: t.Dim.Render(key), W: kw, Right: true})
		body = append(body, ovRow(t, line, inner, i == m.cur.Sel, false))
	}
	return ovFrame(t, w, h, body)
}

package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/library"
)

func TestSongsMeta(t *testing.T) {
	for _, tc := range []struct {
		n    int
		d    time.Duration
		want string
	}{
		{17, 62*time.Minute + 10*time.Second, "17 songs · 1 hr 2 min"},
		{1, 3 * time.Minute, "1 song · 3 min"},
		{40, 47*time.Hour + 59*time.Minute, "40 songs · 47 hr 59 min"},
		{5820, 15 * 24 * time.Hour, "5,820 songs · 15 days"},
		{0, 0, "0 songs"},
	} {
		if got := songsMeta(tc.n, tc.d); got != tc.want {
			t.Errorf("songsMeta(%d, %v) = %q, want %q", tc.n, tc.d, got, tc.want)
		}
	}
}

func tableCtx(t *testing.T) *Ctx {
	return &Ctx{Theme: shellTheme(), Lib: shellLibrary(t), Focused: true}
}

// TestTrackTableColumnWidths checks the layout from 40 to 220 cells: columns
// never overlap, Time ends before the scrollbar margin, the heart sits right
// before Time, and Artist holds on down to 60.
func TestTrackTableColumnWidths(t *testing.T) {
	lib := shellLibrary(t)
	tt := NewTrackTable(lib.Songs, TableOptions{Wide: []Column{ColPlays, ColAdded}})
	for w := 40; w <= 220; w++ {
		cols := tt.columns(w)
		end := tableGutter
		has := map[Column]bool{}
		for i, cs := range cols {
			has[cs.c] = true
			if cs.x < end || cs.w < 1 {
				t.Fatalf("w=%d: column %d overlaps or is empty: %+v", w, i, cols)
			}
			end = cs.x + cs.w
		}
		last := cols[len(cols)-1]
		if last.c != ColTime || end != w-2 {
			t.Fatalf("w=%d: Time should end at %d: %+v", w, w-2, cols)
		}
		if h := cols[len(cols)-2]; h.c != ColHeart || h.x+h.w+1 != last.x {
			t.Fatalf("w=%d: heart should sit right before Time: %+v", w, cols)
		}
		if w >= 60 && !has[ColArtist] {
			t.Fatalf("w=%d: Artist dropped", w)
		}
		if w >= 77 && !has[ColAlbum] {
			t.Fatalf("w=%d: Album dropped", w)
		}
	}
	fav := NewTrackTable(lib.Favorites, TableOptions{NoHeart: true})
	for _, cs := range fav.columns(120) {
		if cs.c == ColHeart {
			t.Fatal("Favorites shows no heart column")
		}
	}
}

// TestTrackTableRows renders every row at several widths: exact widths, the
// heart in its column, the unavailable mark in the gutter.
func TestTrackTableRows(t *testing.T) {
	c := tableCtx(t)
	lib := c.Lib
	tt := NewTrackTable(lib.Songs, TableOptions{Wide: []Column{ColPlays, ColAdded}})
	g := &c.Theme.G
	for _, w := range []int{60, 80, 100, 140, 220} {
		s := tt.Render(w, 40, c)
		checkBlock(t, "table", s, w, 40)
		var heartX int
		for _, cs := range tt.cols {
			if cs.c == ColHeart {
				heartX = cs.x
			}
		}
		for i, l := range strings.Split(s, "\n")[1:] {
			tr := tt.rows[tt.top+i]
			cells := []rune(ansi.Strip(l))
			if got := string(cells[heartX]) == g.Heart; got != c.IsFavorite(tr) {
				t.Fatalf("w=%d row %d (%s): heart %v", w, i, tr.Title, got)
			}
			if got := string(cells[1]) == g.Blocked; got != !tr.Available {
				t.Fatalf("w=%d row %d (%s): unavailable mark %v", w, i, tr.Title, got)
			}
		}
	}
}

func TestTrackTableRefreshKeepsPlace(t *testing.T) {
	c := tableCtx(t)
	songs := c.Lib.Songs[:200]
	tt := NewTrackTable(songs, TableOptions{})
	tt.Render(100, 21, c)
	tt.moveTo(120)
	cur, top := tt.cursor, tt.top
	gone := tt.Current()

	// the selected track survives: the cursor follows it
	next := append([]*library.Track{}, songs[:10]...)
	next = append(next, songs[20:]...)
	tt.SetTracks(next)
	if tt.Current() != gone || tt.cursor != cur-10 {
		t.Fatalf("cursor %d on %v", tt.cursor, tt.Current())
	}

	// the selected track is gone: index and scroll stay
	tt.moveTo(cur)
	cur, top = tt.cursor, tt.top
	pid := tt.Current().PID
	var without []*library.Track
	for _, tr := range next {
		if tr.PID != pid {
			without = append(without, tr)
		}
	}
	tt.SetTracks(without)
	if tt.cursor != cur || tt.top != top {
		t.Fatalf("removed: cursor %d top %d, want %d %d", tt.cursor, tt.top, cur, top)
	}

	// fewer rows than the old index: clamped to the end
	tt.SetTracks(without[:50])
	if tt.cursor != 49 || tt.top != 50-20 {
		t.Fatalf("shrunk: cursor %d top %d", tt.cursor, tt.top)
	}
}

func TestTrackTableScrollDragRelease(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.render()
	tt := tracksOf(a)
	v := a.nav.cur.(*tracksView)
	body := a.lay.ContentBody
	barX := body.Min.X + tt.width - 1
	y0 := body.Min.Y + v.top + 1
	a.Update(tea.MouseClickMsg{X: barX, Y: y0 + 10, Button: tea.MouseLeft})
	if !tt.scrollDrag || tt.top == 0 {
		t.Fatalf("press on the scrollbar should start a drag: top %d", tt.top)
	}
	// drag up past the table, over the title line, and let go there
	a.Update(tea.MouseMotionMsg{X: barX, Y: body.Min.Y, Button: tea.MouseLeft})
	if tt.top != 0 {
		t.Fatalf("dragging above the table should scroll to the top: %d", tt.top)
	}
	a.Update(tea.MouseReleaseMsg{X: barX, Y: body.Min.Y, Button: tea.MouseLeft})
	if tt.scrollDrag {
		t.Fatal("release above the table should end the drag")
	}
	top := tt.top
	a.Update(tea.MouseMotionMsg{X: barX, Y: y0 + 20})
	if tt.top != top {
		t.Fatal("plain motion after the release should not scroll")
	}
}

func TestTrackTableHeartClickAndTip(t *testing.T) {
	a := newShellApp(t, 140, 42)
	a.render()
	tt := tracksOf(a)
	v := a.nav.cur.(*tracksView)
	body := a.lay.ContentBody
	var heartX int
	for _, cs := range tt.cols {
		if cs.c == ColHeart {
			heartX = body.Min.X + cs.x
		}
	}
	i := -1
	for k, tr := range tt.rows[:30] {
		if tr.Available {
			i = k
			break
		}
	}
	tr := tt.rows[i]
	was := a.viewCtx().IsFavorite(tr)
	a.Update(tea.MouseClickMsg{X: heartX, Y: body.Min.Y + v.top + 1 + i, Button: tea.MouseLeft})
	a.Update(tea.MouseReleaseMsg{X: heartX, Y: body.Min.Y + v.top + 1 + i, Button: tea.MouseLeft})
	if a.viewCtx().IsFavorite(tr) == was {
		t.Fatal("clicking the heart should toggle the favorite")
	}
	a.Update(tea.MouseMotionMsg{X: heartX, Y: body.Min.Y + v.top + 1 + i})
	if !strings.HasPrefix(a.hint, "Favorite") && !strings.HasPrefix(a.hint, "Unfavorite") {
		t.Fatalf("heart hint %q", a.hint)
	}

	for k, tr := range tt.rows[:30] {
		if !tr.Available {
			a.Update(tea.MouseMotionMsg{X: body.Min.X + 10, Y: body.Min.Y + v.top + 1 + k})
			if a.hint != "Not available" {
				t.Fatalf("hint %q", a.hint)
			}
			return
		}
	}
	t.Skip("no unavailable row on the first page")
}

func TestPlaylistHeader(t *testing.T) {
	a := newShellApp(t, 140, 42)
	var pl, folder *library.Playlist
	for _, p := range a.lib.PlaylistsFlat {
		switch {
		case p.Kind == "folder" && folder == nil:
			folder = p
		case p.Kind != "folder" && len(p.Tracks) > 30 && pl == nil:
			pl = p
		}
	}
	for _, p := range []*library.Playlist{pl, folder} {
		a.OpenPlaylist(p)
		v := a.nav.cur.(*tracksView)
		c := a.viewCtx()
		for _, sz := range [][2]int{{140, 40}, {100, 25}, {77, 20}, {60, 12}, {40, 6}, {30, 2}, {20, 1}} {
			s := v.Render(sz[0], sz[1], c)
			checkBlock(t, "playlist", s, sz[0], sz[1])
		}
		s := ansi.Strip(v.Render(117, 37, c))
		for _, want := range []string{p.Name, playlistKind(p), " songs · ", "Play", "Shuffle"} {
			if !strings.Contains(s, want) {
				t.Fatalf("%s header lacks %q:\n%s", p.Name, want, s)
			}
		}
		if v.top < 6 {
			t.Fatalf("header with art: table at %d", v.top)
		}
		v.Render(117, 14, c)
		if v.top != tableTop {
			t.Fatalf("short window: one-line header, table at %d", v.top)
		}
	}

	// Play and Shuffle are clickable
	a.OpenPlaylist(pl)
	a.render()
	v := a.nav.cur.(*tracksView)
	z := v.zones[0]
	body := a.lay.ContentBody
	click(a, body.Min.X+z.x0+1, body.Min.Y+z.y)
	if got := a.player.State().Track; got == nil || got.PID != v.table.rows[v.firstPlayable(0)].PID {
		t.Fatalf("Play should start the first track, playing %v", got)
	}
	a.render()
	z = v.zones[1]
	click(a, body.Min.X+z.x0+1, body.Min.Y+z.y)
	if !a.player.State().NowPlaying.Shuffle {
		t.Fatal("Shuffle should turn shuffle on")
	}
}

package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
)

func TestQueueRows(t *testing.T) {
	r := makeQueueRows(true, 10, 3, 1)
	want := []qKind{qNow, qNone, qQueuedLabel, qTrack, qTrack, qTrack, qNone, qNextLabel, qTrack}
	for y, k := range want {
		if got, _, _ := r.at(y); got != k {
			t.Fatalf("row %d: kind %d, want %d", y, got, k)
		}
	}
	if r.total != 15 {
		t.Fatalf("total %d", r.total)
	}
	for _, th := range []int{1, 2} {
		r := makeQueueRows(true, 10, 3, th)
		for i := range 10 {
			for sub := range th {
				if k, j, s := r.at(r.rowOf(i) + sub); k != qTrack || j != i || s != sub {
					t.Fatalf("th %d: rowOf(%d)+%d round trip: %d %d %d", th, i, sub, k, j, s)
				}
			}
		}
		if got := r.indexAt(r.nextLabel); got != 3 {
			t.Fatalf("th %d: drop on the next label lands at %d", th, got)
		}
		if got := r.indexAt(r.total + 4); got != 9 {
			t.Fatalf("th %d: drop below the list lands at %d", th, got)
		}
	}
	if r := makeQueueRows(false, 4, 0, 1); r.nextLabel != 0 || r.qLabel != -1 || r.total != 5 {
		t.Fatalf("no queue: %+v", r)
	}
}

// queueTitles returns the titles of tracks in Upcoming()[:n].
func queueTitles(fx *lyricsFixture, n int) []string {
	up := fx.pl.Upcoming()
	var out []string
	for _, t := range up[:min(n, len(up))] {
		out = append(out, t.Title)
	}
	return out
}

func newQueueFixture(t *testing.T) (*lyricsFixture, *queuePane, []*library.Track) {
	t.Helper()
	fx := newLyricsFixture(t, "")
	extra := fx.c.Lib.Songs[100:104]
	fx.pl.AddToQueue(extra...)
	fx.sync()
	q := newQueuePane(fx.svc).(*queuePane)
	fx.c.Focused = true
	q.Render(40, 20, fx.c)
	return fx, q, extra
}

func TestQueuePaneRender(t *testing.T) {
	fx, q, extra := newQueueFixture(t)
	for _, sz := range [][2]int{{40, 20}, {28, 6}, {60, 3}, {8, 1}} {
		s := q.Render(sz[0], sz[1], fx.c)
		lines := strings.Split(s, "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines", sz, len(lines))
		}
		for _, l := range lines {
			if Width(l) != sz[0] {
				t.Fatalf("%v: width %d %q", sz, Width(l), ansi.Strip(l))
			}
		}
	}
	lines := plain(strings.Split(q.Render(40, 20, fx.c), "\n"))
	if want := "Up Next   " + plural(len(fx.pl.Upcoming()), "song", "songs") + " · "; !strings.HasPrefix(lines[0], want) {
		t.Fatalf("title %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], "▶ "+fx.tr.Title[:5]) {
		t.Fatalf("now playing %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "  "+fx.tr.Artist[:5]) {
		t.Fatalf("now playing artist %q", lines[3])
	}
	if !strings.HasPrefix(lines[5], "  Queued ") || !strings.Contains(lines[6], extra[0].Title) || !strings.Contains(lines[7], extra[0].Artist) {
		t.Fatalf("queued section:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[15], "  Next from: Songs ") {
		t.Fatalf("next label %q", lines[15])
	}

	empty := newQueuePane(Services{}).(*queuePane)
	c, _ := ovTestCtx(t)
	if s := ansi.Strip(empty.Render(40, 12, c)); !strings.Contains(s, "Nothing up next") {
		t.Fatalf("empty:\n%s", s)
	}
}

func TestQueuePaneKeys(t *testing.T) {
	fx, q, extra := newQueueFixture(t)
	q.Action(keymap.NavDown, 1, fx.c)
	q.Action(keymap.QueueMoveDown, 2, fx.c)
	if got := queueTitles(fx, 4); got[3] != extra[1].Title || q.cursor != 3 {
		t.Fatalf("J: %q cursor %d", got, q.cursor)
	}
	q.Action(keymap.QueueMoveUp, 1, fx.c)
	if got := queueTitles(fx, 4); got[2] != extra[1].Title || q.cursor != 2 {
		t.Fatalf("K: %q cursor %d", got, q.cursor)
	}
	q.Action(keymap.QueueRemove, 1, fx.c)
	if fx.pl.State().Queued != 3 || q.queued != 3 {
		t.Fatalf("x: queued %d / %d", fx.pl.State().Queued, q.queued)
	}

	// A context row moves too, and becomes queued along with the row it passed.
	q.Action(keymap.NavTop, 1, fx.c)
	q.Action(keymap.NavDown, 4, fx.c)
	moved := fx.pl.UpcomingAt(4)
	q.Action(keymap.QueueMoveDown, 1, fx.c)
	if got := fx.pl.UpcomingAt(5); got != moved || q.cursor != 5 || q.queued != 6 {
		t.Fatalf("J on a context row: %v at 5, cursor %d, queued %d", got.Title, q.cursor, q.queued)
	}
	q.Action(keymap.QueueMoveUp, 5, fx.c)
	if got := fx.pl.UpcomingAt(0); got != moved || q.cursor != 0 {
		t.Fatalf("5K: %v at 0, cursor %d", got.Title, q.cursor)
	}

	q.Action(keymap.TrackFavorite, 1, fx.c)
	if fx.h.last() != "ToggleFavorite 1" {
		t.Fatalf("f: %v", fx.h.calls)
	}
	q.Action(keymap.Menu, 1, fx.c)
	if fx.h.last() != "PushOverlay" {
		t.Fatalf("m: %v", fx.h.calls)
	}
	m := fx.h.pushed[len(fx.h.pushed)-1].(*MenuOverlay)
	var labels []string
	for _, it := range m.items {
		labels = append(labels, menuLabel(it, fx.c))
	}
	if got := strings.Join(labels, "|"); got != "Play Now|Move to Top|Remove from Up Next||Go to Album|Go to Artist||Open Album in Apple Music|Open Artist in Apple Music||Favorite" &&
		got != "Play Now|Move to Top|Remove from Up Next||Go to Album|Go to Artist||Open Album in Apple Music|Open Artist in Apple Music||Unfavorite" {
		t.Fatalf("menu %q", got)
	}
	// Remove from Up Next, run from the menu
	before := fx.pl.State().Upcoming
	m.items[2].Run(fx.h)
	if got := fx.pl.State().Upcoming; got != before-1 || fx.pl.UpcomingAt(0) == moved {
		t.Fatalf("menu remove: %d upcoming, was %d", got, before)
	}
	// Move to Top
	third := fx.pl.UpcomingAt(3)
	q.Action(keymap.NavDown, 3, fx.c)
	q.Action(keymap.Menu, 1, fx.c)
	fx.h.pushed[len(fx.h.pushed)-1].(*MenuOverlay).items[1].Run(fx.h)
	if fx.pl.UpcomingAt(0) != third {
		t.Fatal("Move to Top didn't")
	}
	q.Action(keymap.NavTop, 1, fx.c)
	first := fx.pl.UpcomingAt(0)
	q.Action(keymap.Open, 1, fx.c)
	if got := fx.pl.State().Track; got == nil || got.PID != first.PID {
		t.Fatalf("enter played %v", got)
	}
}

func TestQueuePaneMouse(t *testing.T) {
	fx, q, extra := newQueueFixture(t)
	r := q.rows(fx.c)
	row := func(i int) int { return queueBodyTop + r.rowOf(i) - q.top }

	q.Mouse(MouseEvent{Kind: MouseMotion, Button: tea.MouseNone, X: 5, Y: row(2)}, fx.c)
	if q.hover != 2 {
		t.Fatalf("hover %d", q.hover)
	}

	// drag the first queued track below the third
	q.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 5, Y: row(0)}, fx.c)
	q.Mouse(MouseEvent{Kind: MouseMotion, Button: tea.MouseLeft, X: 5, Y: row(2)}, fx.c)
	lines := plain(strings.Split(q.Render(40, 20, fx.c), "\n"))
	if !strings.Contains(lines[row(2)+r.th], "────") || !strings.Contains(q.Render(40, 20, fx.c), sgrBG(fx.c, extra[0].Title)) {
		t.Fatalf("no insertion line after row %d:\n%s", row(2), strings.Join(lines, "\n"))
	}
	q.Mouse(MouseEvent{Kind: MouseRelease, Button: tea.MouseLeft, X: 5, Y: row(2)}, fx.c)
	if got := queueTitles(fx, 3); got[2] != extra[0].Title || q.cursor != 2 {
		t.Fatalf("drag: %q cursor %d", got, q.cursor)
	}
	if strings.Contains(strings.Join(plain(strings.Split(q.Render(40, 20, fx.c), "\n")), "\n"), "────") {
		t.Fatal("insertion line left after the drop")
	}

	q.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseRight, X: 5, Y: row(5), ScreenX: 70, ScreenY: 12}, fx.c)
	if fx.h.last() != "PushOverlay" || q.cursor != 5 {
		t.Fatalf("right click: %v cursor %d", fx.h.calls, q.cursor)
	}

	q.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: 5, Y: 5}, fx.c)
	if q.top != wheelRows {
		t.Fatalf("wheel top %d", q.top)
	}

	target := q.track(6)
	r = q.rows(fx.c)
	before := fx.pl.State().Track
	q.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 5, Y: row(6), Double: true}, fx.c)
	if got := fx.pl.State().Track; got != before {
		t.Fatalf("a double-click that began elsewhere played %v", got)
	}
	q.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 5, Y: row(6)}, fx.c)
	q.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 5, Y: row(6), Double: true}, fx.c)
	if got := fx.pl.State().Track; got == nil || got.PID != target.PID {
		t.Fatalf("double click played %v, want %s", got, target.Title)
	}
}

// sgrBG is the selected-row start of a cell holding text, for checking a row
// is drawn on the sel surface.
func sgrBG(c *Ctx, text string) string {
	return paintsFor(c.Theme).paint(toneText, surfSel, text)
}

func TestQueuePaneDragContextRow(t *testing.T) {
	fx, q, _ := newQueueFixture(t)
	r := q.rows(fx.c)
	row := func(i int) int { return queueBodyTop + r.rowOf(i) - q.top }
	moved := fx.pl.UpcomingAt(5) // a context row
	q.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 5, Y: row(5)}, fx.c)
	q.Mouse(MouseEvent{Kind: MouseMotion, Button: tea.MouseLeft, X: 5, Y: row(1)}, fx.c)
	lines := plain(strings.Split(q.Render(40, 20, fx.c), "\n"))
	if !strings.Contains(lines[row(1)], "────") {
		t.Fatalf("no insertion line above row %d:\n%s", row(1), strings.Join(lines, "\n"))
	}
	q.Mouse(MouseEvent{Kind: MouseRelease, Button: tea.MouseLeft, X: 5, Y: row(1)}, fx.c)
	if fx.pl.UpcomingAt(1) != moved || q.cursor != 1 || q.queued != 5 {
		t.Fatalf("drag: cursor %d queued %d", q.cursor, q.queued)
	}
}

func TestQueuePaneColumnsAlign(t *testing.T) {
	fx, q, _ := newQueueFixture(t)
	for _, w := range []int{40, 48, 64, 90} {
		lines := plain(strings.Split(q.Render(w, 24, fx.c), "\n"))
		r := q.rows(fx.c)
		if want := 1 + boolInt(w < queueOneLineW); r.th != want {
			t.Fatalf("w %d: %d rows per track, want %d", w, r.th, want)
		}
		col := -1
		for y := queueBodyTop; y < len(lines); y++ {
			k, i, sub := r.at(q.top + y - queueBodyTop)
			if k != qTrack || sub != r.th-1 {
				continue
			}
			tr := q.track(i)
			at := strings.Index(lines[y], tr.Artist[:3])
			if at < 0 {
				t.Fatalf("w %d: no artist on row %d:\n%s", w, y, strings.Join(lines, "\n"))
			}
			x := Width(lines[y][:at])
			if col < 0 {
				col = x
			}
			if x != col {
				t.Fatalf("w %d: artist of row %d at %d, want %d:\n%s", w, i, x, col, strings.Join(lines, "\n"))
			}
		}
		// times end at the same column on every track
		for y := queueBodyTop; y < len(lines); y++ {
			if k, _, sub := r.at(q.top + y - queueBodyTop); k == qTrack && sub == 0 && Width(strings.TrimRight(lines[y], " ┃│")) != w-2 {
				t.Fatalf("w %d: time of row %d ends at %d:\n%s", w, y, Width(lines[y]), strings.Join(lines, "\n"))
			}
		}
	}
}

package ui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/lyrics"
	"github.com/bogdankim/encore/internal/player"
)

const testLRC = `[ti:Test]
[00:01.00]First line of the song
[00:05.00]Second line that is long enough to wrap in a narrow pane
[00:09.00]
[00:20.00]Third line after the break
[00:24.00]Fourth line
[00:28.00]
`

// longLRC is n lines four seconds apart from 0:04.
func longLRC(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "[%02d:%02d.00]Line number %d\n", i*4/60, i*4%60, i)
	}
	return b.String()
}

// collect runs cmd's leaves concurrently and returns the messages that
// arrive within d, or up to a final lyrics result (later ticks are left
// behind).
func collect(cmd tea.Cmd, d time.Duration) []tea.Msg {
	ch := make(chan tea.Msg, 16)
	var run func(tea.Cmd) int
	run = func(c tea.Cmd) int {
		if c == nil {
			return 0
		}
		go func() { ch <- c() }()
		return 1
	}
	n := run(cmd)
	var out []tea.Msg
	deadline := time.After(d)
	for n > 0 {
		select {
		case m := <-ch:
			n--
			if b, ok := m.(tea.BatchMsg); ok {
				for _, c := range b {
					n += run(c)
				}
				continue
			}
			if m != nil {
				out = append(out, m)
			}
			if r, ok := m.(lyricsResultMsg); ok && !r.interim {
				return out
			}
		case <-deadline:
			return out
		}
	}
	return out
}

// lyricsFixture is a player on the fake bridge playing the first library
// track, with demo lyrics for every track.
type lyricsFixture struct {
	t   *testing.T
	c   *Ctx
	h   *ovRecorder
	pl  *player.Player
	svc Services
	tr  *library.Track
}

func newLyricsFixture(t *testing.T, lrc string) *lyricsFixture {
	t.Helper()
	c, h := ovTestCtx(t)
	f := bridge.NewFakeWithClock(1, time.Now)
	pl := player.New(f, c.Lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	ls := lyrics.NewService(lyrics.Options{Demo: func(string) string { return lrc }})
	t.Cleanup(ls.Close)
	offs := lyrics.LoadOffsets(t.TempDir() + "/offsets.json")
	songs := c.Lib.Songs
	pl.PlayContext(player.Source{Kind: player.KindSongs, Title: "Songs"}, songs[:20], 0)
	fx := &lyricsFixture{t: t, c: c, h: h, pl: pl, tr: songs[0],
		svc: Services{Player: pl, Lyrics: ls, Offsets: offs, LyricsLead: 200 * time.Millisecond}}
	fx.sync()
	return fx
}

// sync refreshes the snapshot in the Ctx.
func (fx *lyricsFixture) sync() { fx.c.Player = fx.pl.State() }

// at sets the snapshot position.
func (fx *lyricsFixture) at(d time.Duration) {
	fx.sync()
	fx.c.Player.Position = d
}

// load runs an update and delivers the lookup result.
func (fx *lyricsFixture) load(v *lyricsView) {
	fx.t.Helper()
	for _, m := range collect(v.update(paneShownMsg{}, fx.c), time.Second) {
		if r, ok := m.(lyricsResultMsg); ok {
			v.update(r, fx.c)
		}
	}
	if v.state != lyricsReady && v.state != lyricsMissing {
		fx.t.Fatalf("state %d after load", v.state)
	}
}

func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return out
}

func TestLyricLayout(t *testing.T) {
	l := lyrics.FromText(testLRC, lyrics.SourceDemo)
	lay := buildLyricLayout(l, 30, paneLyricStyle)
	var got []string
	for _, r := range lay.rows {
		pre := ""
		if r.cont {
			pre = "> "
		}
		got = append(got, pre+r.text)
	}
	want := []string{
		"First line of the song",
		"Second line that is long",
		"> enough to wrap in a",
		"> narrow pane",
		"",
		"Third line after the break",
		"Fourth line",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows\n got %q\nwant %q", got, want)
	}
	if lay.first[2] != 4 || lay.rows[4].line != 2 || lay.first[3] != 5 || lay.first[5] != 7 {
		t.Fatalf("first = %v", lay.first)
	}

	full := buildLyricLayout(l, 80, fullLyricStyle)
	if n := len(full.rows); n != 9 { // 4 lines, 2 gaps around the break, the break
		t.Fatalf("full-screen rows = %d", n)
	}
}

func TestWrapLyricBreaksLongWords(t *testing.T) {
	rows := wrapLyric("Supercalifragilisticexpialidocious yes", 12, 2)
	for _, r := range rows {
		if Width(r) > 12 {
			t.Fatalf("row %q wider than 12", r)
		}
	}
	if strings.ReplaceAll(strings.Join(rows, ""), " ", "") != "Supercalifragilisticexpialidociousyes" {
		t.Fatalf("rows %q lost text", rows)
	}
}

func TestLyricsPaneStates(t *testing.T) {
	c, _ := ovTestCtx(t)
	p := newLyricsPane(Services{}).(*lyricsPane)
	p.Update(paneShownMsg{}, c)
	if got := plain(strings.Split(p.Render(40, 20, c), "\n")); !strings.Contains(strings.Join(got, "\n"), "Nothing playing") {
		t.Fatalf("no track: %q", got)
	}

	fx := newLyricsFixture(t, "")
	p = newLyricsPane(Services{Player: fx.pl}).(*lyricsPane)
	p.Update(paneShownMsg{}, fx.c)
	if s := p.Render(40, 20, fx.c); !strings.Contains(ansi.Strip(s), "Lyrics are turned off") {
		t.Fatalf("no service:\n%s", ansi.Strip(s))
	}

	p = newLyricsPane(fx.svc).(*lyricsPane)
	cmd := p.Update(paneShownMsg{}, fx.c)
	if s := ansi.Strip(p.Render(40, 20, fx.c)); strings.Contains(s, "Looking") {
		t.Fatal("loading notice before 300 ms")
	}
	fx.c.Now = fx.c.Now.Add(lyricsQuiet)
	if s := ansi.Strip(p.Render(40, 20, fx.c)); !strings.Contains(s, "Looking for lyrics…") {
		t.Fatalf("no loading notice:\n%s", s)
	}
	for _, m := range collect(cmd, time.Second) {
		p.Update(m, fx.c)
	}
	if s := ansi.Strip(p.Render(40, 20, fx.c)); !strings.Contains(s, "No lyrics for this song") {
		t.Fatalf("not found:\n%s", s)
	}
}

func TestLyricsPaneFollowsCurrentLine(t *testing.T) {
	fx := newLyricsFixture(t, testLRC)
	p := newLyricsPane(fx.svc).(*lyricsPane)
	v := p.v
	fx.load(v)

	for _, sz := range [][2]int{{40, 20}, {28, 8}, {60, 3}, {5, 1}} {
		s := p.Render(sz[0], sz[1], fx.c)
		if lines := strings.Split(s, "\n"); len(lines) != sz[1] {
			t.Fatalf("%v: %d lines", sz, len(lines))
		}
		for _, l := range strings.Split(s, "\n") {
			if Width(l) != sz[0] {
				t.Fatalf("%v: line width %d: %q", sz, Width(l), ansi.Strip(l))
			}
		}
	}

	if got := plain(strings.Split(p.Render(40, 22, fx.c), "\n"))[0]; got != "Lyrics" {
		t.Fatalf("title %q: the source isn't shown, nor meta with nothing to say", got)
	}

	long := newLyricsFixture(t, longLRC(40))
	lp := newLyricsPane(long.svc).(*lyricsPane)
	long.load(lp.v)
	long.at(60*time.Second + 500*time.Millisecond)
	lines := plain(strings.Split(lp.Render(40, 22, long.c), "\n"))
	anchor := lyricsBodyTop + 20*2/5
	if lines[anchor] != "  Line number 15" || lines[anchor-1] != "  Line number 14" {
		t.Fatalf("current line not at the anchor (row %d):\n%s", anchor, strings.Join(lines, "\n"))
	}

	// the highlight leads by lyrics.lead_ms (200 ms here)
	fx.at(24*time.Second - 150*time.Millisecond)
	if cur := v.current(fx.c); cur != 4 {
		t.Fatalf("current = %d, want 4 (lead)", cur)
	}
	v.svc.LyricsLead = 0
	if cur := v.current(fx.c); cur != 3 {
		t.Fatalf("current = %d with no lead, want 3", cur)
	}
	v.svc.LyricsLead = 200 * time.Millisecond
	// a break long enough shows dots
	fx.at(10 * time.Second)
	fx.c.Now = fx.c.Now.Add(2 * time.Second)
	if s := ansi.Strip(p.Render(40, 22, fx.c)); !strings.Contains(s, lyricsDots) {
		t.Fatalf("no break dots:\n%s", s)
	}
}

func TestLyricsScrollAnimates(t *testing.T) {
	fx := newLyricsFixture(t, longLRC(40))
	v := newLyricsView(fx.svc, paneLyricStyle)
	fx.load(v)
	fx.at(40 * time.Second)
	v.render(30, 10, fx.c)
	start := v.top
	fx.at(60 * time.Second)
	cmd := v.update(tickMsg{}, fx.c)
	if cmd == nil || !v.frame {
		t.Fatal("no animation frame scheduled")
	}
	v.render(30, 10, fx.c)
	if v.top != start {
		t.Fatalf("jumped to %d at t=0", v.top)
	}
	fx.c.Now = fx.c.Now.Add(lyricsScrollDur / 5)
	v.render(30, 10, fx.c)
	mid := v.top
	fx.c.Now = fx.c.Now.Add(lyricsScrollDur)
	v.render(30, 10, fx.c)
	if !(mid > start && mid < v.top) || v.top-start != 5 {
		t.Fatalf("scroll %d → %d → %d is not gradual", start, mid, v.top)
	}
}

func TestLyricsDropsLateResult(t *testing.T) {
	fx := newLyricsFixture(t, testLRC)
	v := newLyricsView(fx.svc, paneLyricStyle)
	v.update(paneShownMsg{}, fx.c)
	old := v.pid
	fx.pl.Next()
	fx.sync()
	v.update(playerMsg{}, fx.c)
	if v.pid == old || v.state != lyricsLoading {
		t.Fatalf("track change not picked up: %q %d", v.pid, v.state)
	}
	v.update(lyricsResultMsg{from: v, gen: v.gen - 1, pid: old, lyr: lyrics.FromText(testLRC, "demo")}, fx.c)
	if v.state != lyricsLoading {
		t.Fatal("a result for the previous track was taken")
	}
	// another view's result for this track is welcome
	other := &lyricsView{}
	v.update(lyricsResultMsg{from: other, gen: 1, pid: v.pid, err: lyrics.ErrNotFound}, fx.c)
	if v.state != lyricsMissing {
		t.Fatalf("state %d", v.state)
	}
}

func TestLyricsMouseAndKeys(t *testing.T) {
	fx := newLyricsFixture(t, testLRC)
	p := newLyricsPane(fx.svc).(*lyricsPane)
	v := p.v
	fx.load(v)
	fx.at(0)
	p.Render(40, 22, fx.c)

	// row 3 of the body is the second row of line 1 (wrapped)
	y := lyricsBodyTop + v.layout().first[3]
	p.Mouse(MouseEvent{Kind: MouseMotion, Button: tea.MouseNone, X: 5, Y: y}, fx.c)
	if v.hoverLine() != 3 {
		t.Fatalf("hover line %d", v.hoverLine())
	}
	p.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 5, Y: y}, fx.c)
	if pos := fx.pl.Position(); pos < 20*time.Second || pos > 21*time.Second {
		t.Fatalf("seeked to %v, want 0:20", pos)
	}

	// wheel scrolls freely; the snap back is scheduled
	fx.at(20 * time.Second)
	fx.c.Now = fx.c.Now.Add(time.Second)
	p.Render(40, 22, fx.c)
	before := v.top
	cmd := p.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelUp, X: 5, Y: 5}, fx.c)
	if cmd == nil || !v.manual || v.top >= before && before > 0 {
		t.Fatalf("wheel: manual=%v top %d → %d", v.manual, before, v.top)
	}
	v.update(lyricsWakeMsg{v: v, kind: wakeSnap, gen: v.manualGen}, fx.c)
	if v.manual {
		t.Fatal("no snap back")
	}

	// offsets
	p.Action(keymap.LyricsOffsetUp, 1, fx.c)
	p.Action(keymap.LyricsOffsetUp, 1, fx.c)
	p.Action(keymap.LyricsOffsetDown, 1, fx.c)
	if got := fx.svc.Offsets.Get(v.pid); got != 100*time.Millisecond {
		t.Fatalf("offset %v", got)
	}
	if line := ansi.Strip(strings.Split(p.Render(40, 22, fx.c), "\n")[0]); !strings.Contains(line, "+0.1 s") {
		t.Fatalf("title %q", line)
	}
	if formatOffset(-300*time.Millisecond) != "−0.3 s" {
		t.Fatal(formatOffset(-300 * time.Millisecond))
	}

	// keys scroll and enter seeks to the line at the anchor
	ok, _ := p.Action(keymap.NavTop, 1, fx.c)
	if !ok || !v.manual || !v.kbd {
		t.Fatal("gg didn't scroll")
	}
	p.Render(40, 22, fx.c)
	line := v.hoverLine()
	if line < 0 {
		t.Fatal("no keyboard cursor line")
	}
	p.Action(keymap.Open, 1, fx.c)
	want := v.lyr.Lines[line].Start - 100*time.Millisecond
	if pos := fx.pl.Position(); pos < want || pos > want+time.Second {
		t.Fatalf("enter seeked to %v, want %v", pos, want)
	}
}

func TestLyricsUnsynced(t *testing.T) {
	text := strings.Repeat("a plain line\n", 40)
	fx := newLyricsFixture(t, text)
	p := newLyricsPane(fx.svc).(*lyricsPane)
	fx.load(p.v)
	fx.at(0)
	first := plain(strings.Split(p.Render(40, 12, fx.c), "\n"))
	if !strings.Contains(first[0], "unsynced") {
		t.Fatalf("title %q", first[0])
	}
	fx.at(fx.c.Player.Duration)
	fx.c.Now = fx.c.Now.Add(time.Second)
	p.Render(40, 12, fx.c)
	if p.v.top != 40-10 {
		t.Fatalf("top at the end = %d", p.v.top)
	}
}

func TestLyricsFullOverlay(t *testing.T) {
	fx := newLyricsFixture(t, testLRC)
	o := newLyricsFull(fx.svc).(*lyricsFull)
	fx.load(o.v)
	fx.at(5 * time.Second)
	for _, sz := range [][2]int{{140, 42}, {200, 55}, {80, 24}, {100, 30}, {40, 10}} {
		ovCheckSize(t, "lyrics full", o, fx.c, sz[0], sz[1])
	}

	o.Rect(140, 42)
	s := ansi.Strip(o.Render(fx.c))
	if !strings.Contains(s, fx.tr.Title) || !strings.Contains(s, "Second line that is long enough to wrap") {
		t.Fatalf("full screen:\n%s", s)
	}

	o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: o.close.Min.X + 2, Y: 1}, fx.c)
	if fx.h.last() != "CloseOverlay" {
		t.Fatalf("close click: %v", fx.h.calls)
	}
	if ok, _ := o.Action(keymap.LyricsFullscreen, 1, fx.c); !ok || fx.h.last() != "CloseOverlay" {
		t.Fatal("toggle doesn't close")
	}

	// clicking the progress line seeks
	bar := o.bar
	o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: bar.Min.X + bar.Dx()/2, Y: bar.Min.Y}, fx.c)
	half := fx.c.Player.Duration / 2
	if pos := fx.pl.Position(); pos < half-10*time.Second || pos > half+10*time.Second {
		t.Fatalf("seeked to %v, want about %v", pos, half)
	}
	// and dragging along it keeps seeking
	o.Mouse(MouseEvent{Kind: MouseMotion, Button: tea.MouseLeft, X: bar.Max.X - 1, Y: bar.Min.Y + 3}, fx.c)
	o.Mouse(MouseEvent{Kind: MouseRelease, Button: tea.MouseLeft, X: bar.Max.X - 1, Y: bar.Min.Y + 3}, fx.c)
	if pos := fx.pl.Position(); pos < fx.c.Player.Duration-2*time.Second {
		t.Fatalf("drag seeked to %v, want the end", pos)
	}

	// the close target is the × and a cell either side
	if o.close.Dx() != 3 || o.close.Dy() != 1 {
		t.Fatalf("close target %v", o.close)
	}

	// transport under the art: hover lights a button, a click runs it
	fx.sync()
	o.Render(fx.c)
	play := o.btns[1]
	if play.Empty() || play.Min.Y <= bar.Min.Y {
		t.Fatalf("transport %v under bar %v", o.btns, bar)
	}
	o.Mouse(MouseEvent{Kind: MouseMotion, Button: tea.MouseNone, X: play.Min.X + 1, Y: play.Min.Y}, fx.c)
	if o.hoverBtn != 1 {
		t.Fatalf("hover %d", o.hoverBtn)
	}
	row := strings.Split(o.Render(fx.c), "\n")[play.Min.Y]
	pp := paintsFor(fx.c.Theme)
	if !strings.Contains(row, pp.paint(toneText, surfHover, fx.c.Theme.G.Pause)) {
		t.Fatalf("hovered play button not lit: %q", row)
	}
	for i, want := range []string{"Do player.prev", "Do player.playPause", "Do player.next"} {
		r := o.btns[i]
		o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: r.Min.X, Y: r.Min.Y}, fx.c)
		if !strings.HasPrefix(fx.h.last(), "Do ") || !strings.EqualFold(fx.h.last(), want) {
			t.Fatalf("button %d: %v, want %s", i, fx.h.last(), want)
		}
	}

	// a double click on the art closes
	art := o.art
	n := len(fx.h.calls)
	o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: art.Min.X + 2, Y: art.Min.Y + 2}, fx.c)
	if len(fx.h.calls) != n {
		t.Fatalf("a single click on the art did %v", fx.h.calls[n:])
	}
	o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: art.Min.X + 2, Y: art.Min.Y + 2, Double: true}, fx.c)
	if fx.h.last() != "CloseOverlay" {
		t.Fatalf("double click on the art: %v", fx.h.calls[n:])
	}
}

func TestLyricsLineTones(t *testing.T) {
	fx := newLyricsFixture(t, longLRC(40))
	p := newLyricsPane(fx.svc).(*lyricsPane)
	fx.load(p.v)
	fx.at(60*time.Second + 500*time.Millisecond) // line 15 is current
	out := p.Render(40, 22, fx.c)
	pp := paintsFor(fx.c.Theme)
	for _, tc := range []struct {
		text, want, what string
	}{
		{"Line number 15", pp.paint(toneAccentBold, surfNone, "Line number 15"), "current in accent bold"},
		{"Line number 16", pp.paint(toneDim, surfNone, "Line number 16"), "upcoming in dim"},
		{"Line number 14", pastPrefix(pp, surfNone) + "Line number 14", "past a step quieter"},
	} {
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: %q not painted as %q", tc.what, tc.text, tc.want)
		}
	}
	if pastPrefix(pp, surfNone) == pp.pre[toneDim][surfNone] || pastPrefix(pp, surfNone) == pp.pre[toneFaint][surfNone] {
		t.Error("past lines should sit between faint and dim")
	}
	if c, ok := mixColor(color.RGBA{0, 0, 0, 255}, color.RGBA{90, 90, 90, 255}, 1.0/3); !ok || c != (color.RGBA{30, 30, 30, 255}) {
		t.Errorf("mix %v", c)
	}
}

// cursorRow is the body row the keyboard cursor's line starts on, or -1
// when it is off screen.
func cursorRow(v *lyricsView) int {
	y := v.layout().first[v.cursor] - v.top
	if y < 0 || y >= v.h {
		return -1
	}
	return y
}

func TestLyricsCursorReachesEnds(t *testing.T) {
	fx := newLyricsFixture(t, longLRC(40))
	for _, h := range []int{3, 8, 12, 22, 46, 90} {
		p := newLyricsPane(fx.svc).(*lyricsPane)
		v := p.v
		fx.load(v)
		fx.at(60*time.Second + 500*time.Millisecond) // line 15 (index 14) is current
		p.Render(40, h, fx.c)

		// the first k starts from the current line
		p.Action(keymap.NavUp, 1, fx.c)
		if v.cursor != 13 {
			t.Fatalf("h %d: first k: cursor %d, want 13", h, v.cursor)
		}
		for range 60 {
			p.Action(keymap.NavUp, 1, fx.c)
			p.Render(40, h, fx.c)
			if cursorRow(v) < 0 {
				t.Fatalf("h %d: cursor %d off screen (top %d)", h, v.cursor, v.top)
			}
		}
		lines := plain(strings.Split(p.Render(40, h, fx.c), "\n"))
		if v.cursor != 0 || v.top != 0 || lines[lyricsBodyTop] != "  Line number 1" {
			t.Fatalf("h %d: k to the top: cursor %d top %d\n%s", h, v.cursor, v.top, strings.Join(lines, "\n"))
		}
		pp := paintsFor(fx.c.Theme)
		if row := strings.Split(p.Render(40, h, fx.c), "\n")[lyricsBodyTop]; !strings.HasPrefix(row, pp.spaces(surfHover, 2)) {
			t.Fatalf("h %d: first line not lit: %q", h, row)
		}

		for range 60 {
			p.Action(keymap.NavDown, 1, fx.c)
			p.Render(40, h, fx.c)
			if cursorRow(v) < 0 {
				t.Fatalf("h %d: cursor %d off screen (top %d)", h, v.cursor, v.top)
			}
		}
		if v.cursor != 39 {
			t.Fatalf("h %d: j to the bottom: cursor %d", h, v.cursor)
		}
		lines = plain(strings.Split(p.Render(40, h, fx.c), "\n"))
		if y := cursorRow(v); lines[lyricsBodyTop+y] != "  Line number 40" {
			t.Fatalf("h %d: last line not at row %d\n%s", h, y, strings.Join(lines, "\n"))
		}

		// gg and G jump; in the middle the cursor sits at the anchor
		p.Action(keymap.NavTop, 1, fx.c)
		if v.cursor != 0 || v.top != 0 {
			t.Fatalf("h %d: gg: cursor %d top %d", h, v.cursor, v.top)
		}
		p.Action(keymap.NavBottom, 1, fx.c)
		if p.Render(40, h, fx.c); v.cursor != 39 || cursorRow(v) < 0 {
			t.Fatalf("h %d: G: cursor %d top %d", h, v.cursor, v.top)
		}
		p.Action(keymap.NavHalfUp, 1, fx.c)
		if p.Render(40, h, fx.c); v.cursor >= 39 || cursorRow(v) < 0 {
			t.Fatalf("h %d: ctrl+u: cursor %d top %d", h, v.cursor, v.top)
		}
		if 20 >= v.anchor() {
			p.Action(keymap.NavTop, 1, fx.c)
			p.Action(keymap.NavDown, 20, fx.c)
			if p.Render(40, h, fx.c); cursorRow(v) != v.anchor() {
				t.Fatalf("h %d: mid-song cursor at row %d, want the anchor %d", h, cursorRow(v), v.anchor())
			}
		}
	}
}

func TestLyricsCursorInFullScreen(t *testing.T) {
	fx := newLyricsFixture(t, longLRC(40))
	for _, sz := range [][2]int{{140, 42}, {80, 24}, {200, 60}} {
		o := newLyricsFull(fx.svc).(*lyricsFull)
		v := o.v
		fx.load(v)
		fx.at(60*time.Second + 500*time.Millisecond)
		o.Rect(sz[0], sz[1])
		o.Render(fx.c)
		for range 50 {
			o.Action(keymap.NavUp, 1, fx.c)
		}
		s := plain(strings.Split(o.Render(fx.c), "\n"))
		if v.cursor != 0 || v.top != 0 || !strings.Contains(s[o.area.Min.Y], "Line number 1") {
			t.Fatalf("%v: cursor %d top %d\n%s", sz, v.cursor, v.top, strings.Join(s, "\n"))
		}
		for range 50 {
			o.Action(keymap.NavDown, 1, fx.c)
		}
		o.Render(fx.c)
		if v.cursor != 39 || cursorRow(v) < 0 {
			t.Fatalf("%v: cursor %d at row %d", sz, v.cursor, cursorRow(v))
		}
		// esc ends the free scroll first, then closes
		n := len(fx.h.calls)
		if ok, _ := o.Action(keymap.Back, 1, fx.c); !ok || v.manual || len(fx.h.calls) != n {
			t.Fatalf("%v: esc: manual %v, calls %v", sz, v.manual, fx.h.calls[n:])
		}
		if o.Action(keymap.Back, 1, fx.c); fx.h.last() != "CloseOverlay" {
			t.Fatalf("%v: second esc didn't close: %v", sz, fx.h.calls[n:])
		}
	}
}

func TestLyricsCursorEnterAndSnapBack(t *testing.T) {
	fx := newLyricsFixture(t, longLRC(40))
	p := newLyricsPane(fx.svc).(*lyricsPane)
	v := p.v
	fx.load(v)
	fx.at(60*time.Second + 500*time.Millisecond)
	p.Render(40, 46, fx.c)

	// enter seeks to the first line and follows again
	p.Action(keymap.NavTop, 1, fx.c)
	if v.hoverLine() != 0 {
		t.Fatalf("cursor line %d", v.hoverLine())
	}
	p.Action(keymap.Open, 1, fx.c)
	if pos := fx.pl.Position(); pos < 4*time.Second || pos > 5*time.Second {
		t.Fatalf("enter seeked to %v, want 0:04", pos)
	}
	if v.manual {
		t.Fatal("still free after enter")
	}

	// each key restarts the 3 s; an older wake doesn't end it
	p.Action(keymap.NavDown, 1, fx.c)
	old := v.manualGen
	p.Action(keymap.NavDown, 1, fx.c)
	v.update(lyricsWakeMsg{v: v, kind: wakeSnap, gen: old}, fx.c)
	if !v.manual || !v.kbd {
		t.Fatal("snapped back while moving")
	}
	v.update(lyricsWakeMsg{v: v, kind: wakeSnap, gen: v.manualGen}, fx.c)
	if v.manual {
		t.Fatal("no snap back after the last key")
	}

	// esc returns to following
	p.Action(keymap.NavUp, 1, fx.c)
	if ok, _ := p.Action(keymap.Back, 1, fx.c); !ok || v.manual {
		t.Fatalf("esc: handled %v manual %v", ok, v.manual)
	}
	if ok, _ := p.Action(keymap.Back, 1, fx.c); ok {
		t.Fatal("esc while following should leave the pane to the app")
	}
}

func TestLyricsWheelReachesEnds(t *testing.T) {
	fx := newLyricsFixture(t, longLRC(40))
	for _, h := range []int{8, 22, 46} {
		p := newLyricsPane(fx.svc).(*lyricsPane)
		v := p.v
		fx.load(v)
		fx.at(60*time.Second + 500*time.Millisecond)
		p.Render(40, h, fx.c)
		wy := lyricsBodyTop + (h-lyricsBodyTop)/2
		for range 40 {
			p.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelUp, X: 5, Y: wy}, fx.c)
		}
		lines := plain(strings.Split(p.Render(40, h, fx.c), "\n"))
		if v.top != 0 || lines[lyricsBodyTop] != "  Line number 1" {
			t.Fatalf("h %d: wheel to the top: top %d\n%s", h, v.top, strings.Join(lines, "\n"))
		}
		for range 40 {
			p.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: 5, Y: wy}, fx.c)
		}
		s := ansi.Strip(p.Render(40, h, fx.c))
		if !strings.Contains(s, "Line number 40") {
			t.Fatalf("h %d: wheel to the bottom:\n%s", h, s)
		}
		// keys after the wheel start from the hovered line
		hovered := v.hoverLine()
		p.Action(keymap.NavDown, 1, fx.c)
		if want := min(hovered+1, 39); hovered >= 0 && v.cursor != want {
			t.Fatalf("h %d: j after the wheel: cursor %d, want %d", h, v.cursor, want)
		}
	}
}

func TestLyricsUnsyncedCursor(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "plain line %d\n", i)
	}
	fx := newLyricsFixture(t, b.String())
	p := newLyricsPane(fx.svc).(*lyricsPane)
	v := p.v
	fx.load(v)
	fx.at(fx.c.Player.Duration / 2)
	p.Render(40, 30, fx.c)
	for range 60 {
		p.Action(keymap.NavUp, 1, fx.c)
	}
	lines := plain(strings.Split(p.Render(40, 30, fx.c), "\n"))
	if v.cursor != 0 || v.top != 0 || lines[lyricsBodyTop] != "  plain line 1" || v.hoverLine() != 0 {
		t.Fatalf("cursor %d top %d\n%s", v.cursor, v.top, strings.Join(lines, "\n"))
	}
	pp := paintsFor(fx.c.Theme)
	if row := strings.Split(p.Render(40, 30, fx.c), "\n")[lyricsBodyTop]; !strings.HasPrefix(row, pp.spaces(surfHover, 2)) {
		t.Fatalf("unsynced cursor not lit: %q", row)
	}
	p.Action(keymap.NavBottom, 1, fx.c)
	if p.Render(40, 30, fx.c); v.cursor != 39 || cursorRow(v) < 0 {
		t.Fatalf("G: cursor %d row %d", v.cursor, cursorRow(v))
	}
	before := fx.pl.Position()
	p.Action(keymap.Open, 1, fx.c)
	if d := fx.pl.Position() - before; d < -time.Second || d > time.Second {
		t.Fatalf("enter moved unsynced playback by %v", d)
	}
}

func TestLyricsUpgradeAndGiveUp(t *testing.T) {
	fx := newLyricsFixture(t, testLRC)
	v := newLyricsView(fx.svc, paneLyricStyle)
	v.update(paneShownMsg{}, fx.c)
	gen := v.gen

	// Past lyricsGiveUp the view stops looking busy; the lookup goes on.
	v.update(lyricsWakeMsg{v: v, kind: wakeGiveUp, gen: gen}, fx.c)
	if v.state != lyricsMissing {
		t.Fatalf("state %d after giving up", v.state)
	}

	// Plain lyrics arrive late, then synced ones replace them.
	plainLyr := lyrics.FromText("just words\nmore words", lyrics.SourceMusic)
	if cmd := v.update(lyricsResultMsg{from: v, gen: gen, pid: v.pid, lyr: plainLyr, interim: true}, fx.c); cmd == nil {
		t.Fatal("an interim result should wait for the next one")
	}
	if v.state != lyricsReady || v.lyr != plainLyr || !strings.Contains(v.meta(), "unsynced") {
		t.Fatalf("plain not shown: state %d", v.state)
	}
	syncedLyr := lyrics.FromText(testLRC, lyrics.SourceLRCLIB)
	v.update(lyricsResultMsg{from: v, gen: gen, pid: v.pid, lyr: syncedLyr}, fx.c)
	if v.lyr != syncedLyr || !v.synced() {
		t.Fatal("synced lyrics didn't replace plain")
	}

	// Nothing replaces lyrics on screen with less.
	v.update(lyricsResultMsg{from: v, gen: gen, pid: v.pid, err: lyrics.ErrNotFound}, fx.c)
	v.update(lyricsResultMsg{from: v, gen: gen, pid: v.pid, lyr: plainLyr}, fx.c)
	if v.state != lyricsReady || v.lyr != syncedLyr {
		t.Fatalf("downgraded: state %d", v.state)
	}
	// A stale give-up is ignored.
	v.update(lyricsWakeMsg{v: v, kind: wakeGiveUp, gen: gen - 1}, fx.c)
	if v.state != lyricsReady {
		t.Fatal("old give-up took effect")
	}
}

func TestLyricsAppleReplacesLRCLIB(t *testing.T) {
	fx := newLyricsFixture(t, testLRC)
	v := newLyricsView(fx.svc, paneLyricStyle)
	v.update(paneShownMsg{}, fx.c)
	gen := v.gen

	lrclib := lyrics.FromText(testLRC, lyrics.SourceLRCLIB)
	v.update(lyricsResultMsg{from: v, gen: gen, pid: v.pid, lyr: lrclib}, fx.c)
	if v.checkApple() == nil {
		t.Fatal("with LRCLIB's lyrics on screen the view should look for Apple's")
	}
	apple := lyrics.FromText(testLRC, lyrics.SourceApple)
	v.update(lyricsResultMsg{from: v, gen: gen, pid: v.pid, lyr: apple}, fx.c)
	if v.lyr != apple {
		t.Fatal("Apple's lyrics didn't replace LRCLIB's")
	}
	if v.checkApple() != nil {
		t.Error("Apple's lyrics on screen: no need to look again")
	}
	v.update(lyricsResultMsg{from: v, gen: gen, pid: v.pid, lyr: lrclib}, fx.c)
	if v.lyr != apple || v.meta() != "" {
		t.Errorf("LRCLIB replaced Apple, or the title says %q", v.meta())
	}
}

const wordsLRC = `[00:01.00]<00:01.00>Paper <00:01.50>lanterns <00:02.00>and <00:02.40>rain
[00:04.00]<00:04.00>Next
`

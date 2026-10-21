package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// twoDiscLib has one album with two discs (3 + 2 tracks), one guest artist
// and a favorited, explicit track.
func twoDiscLib() *library.Library {
	s := &bridge.Snapshot{}
	add := func(i, disc, no int, title, artist string) {
		s.Tracks = append(s.Tracks, bridge.SnapshotTrack{
			PID: fmt.Sprintf("%016X", i+1), Title: title, Artist: artist, AlbumArtist: "Night Owls",
			Album: "Double Feature", AlbumPID: "ALBUM00000000001", Genre: "Rock", Year: 2019,
			Track: no, Disc: disc, DiscCount: 2, Duration: 200_000 + int64(i)*1000,
			InLibrary: true, Available: true, Art: true, Plays: 10 - i,
			Favorited: i == 1, Explicit: i == 1,
		})
	}
	add(0, 1, 1, "Opening", "Night Owls")
	add(1, 1, 2, "Second Song", "Night Owls")
	add(2, 1, 3, "Third", "Night Owls feat. Guest")
	add(3, 2, 1, "Reprise", "Night Owls")
	add(4, 2, 2, "Closing", "Night Owls")
	return library.Build(s)
}

func TestAlbumViewRows(t *testing.T) {
	lib := twoDiscLib()
	al := lib.Albums[0]
	c, _ := albumTestCtx(lib)
	v := NewAlbumView(al)
	if v.Key() != "album:"+al.Key {
		t.Fatalf("key %q", v.Key())
	}
	out := ansi.Strip(v.Render(117, 37, c))
	for _, want := range []string{
		"Double Feature", "Night Owls", "Rock · 2019", "5 songs · 17 min",
		"Play", "Shuffle", "Disc 1", "Disc 2", "Second Song E", "Night Owls feat. Guest", "3:22",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.Contains(l, "Second Song") && !strings.Contains(l, "♥  ") {
			t.Errorf("favorite heart missing before the time: %q", l)
		}
	}
	disc2 := -1
	for i, l := range lines {
		if strings.Contains(l, "Disc 2") {
			disc2 = i
		}
	}
	if disc2 < 1 || strings.TrimSpace(lines[disc2-1]) != "" {
		t.Errorf("Disc 2 needs a blank line above it")
	}
	if strings.Count(out, "Night Owls feat. Guest") != 1 {
		t.Error("the artist column shows only artists that differ from the album's")
	}
}

func TestAlbumViewSingleDiscHasNoHeadings(t *testing.T) {
	al := albumTestLib.Album(albumTestLib.Albums[0].Key)
	c, _ := albumTestCtx(albumTestLib)
	out := ansi.Strip(NewAlbumView(al).Render(117, 37, c))
	if strings.Contains(out, "Disc ") {
		t.Fatalf("single-disc album has a disc heading:\n%s", out)
	}
}

func TestAlbumViewPlay(t *testing.T) {
	lib := twoDiscLib()
	al := lib.Albums[0]
	c, h := albumTestCtx(lib)
	v := NewAlbumView(al)
	v.Render(117, 37, c)
	v.Action(keymap.NavDown, 3, c)
	v.Action(keymap.Open, 1, c)
	want := player.Source{Kind: player.KindAlbum, ID: al.Key, Title: al.Title}
	if h.played.src != want || h.played.i != 3 || len(h.played.tracks) != 5 {
		t.Fatalf("played %+v from %d of %d", h.played.src, h.played.i, len(h.played.tracks))
	}
	if got := v.Selected(c); len(got) != 1 || got[0].Title != "Reprise" {
		t.Fatalf("selected %v", got)
	}
	v.Action(keymap.NavBottom, 1, c)
	v.Action(keymap.NavDown, 5, c)
	if v.cursor != 4 {
		t.Fatalf("G then j: %d", v.cursor)
	}
	v.Action(keymap.NavTop, 1, c)
	if v.cursor != 0 {
		t.Fatalf("gg: %d", v.cursor)
	}
	if ok, _ := v.Action(keymap.FocusLeft, 1, c); ok {
		t.Fatal("h falls through")
	}
	for a, want := range map[keymap.Action]string{keymap.QueueAdd: "queue 1", keymap.QueuePlayNext: "next 1", keymap.Menu: "menu 1"} {
		v.Action(a, 1, c)
		if h.last() != want {
			t.Errorf("%s: %q", a, h.last())
		}
	}
}

func TestAlbumViewHeaderButtons(t *testing.T) {
	lib := twoDiscLib()
	al := lib.Albums[0]
	c, h := albumTestCtx(lib)
	v := NewAlbumView(al)
	v.Render(117, 37, c)
	if len(v.zones) != 3 {
		t.Fatalf("%d header zones", len(v.zones))
	}
	press := func(i int) {
		z := v.zones[i]
		v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: z.x0, Y: z.y}, c)
	}
	press(0)
	if h.last() != "artist Night Owls" {
		t.Fatalf("artist link: %v", h.calls)
	}
	press(1)
	if h.last() != "play album 0" {
		t.Fatalf("Play: %v", h.calls)
	}
	h.calls = nil
	press(2)
	if len(h.calls) != 2 || h.calls[0] != "do player.shuffle" || !strings.HasPrefix(h.calls[1], "play album") {
		t.Fatalf("Shuffle with shuffle off turns it on first: %v", h.calls)
	}
	h.calls = nil
	c.Player.NowPlaying.Shuffle = true
	press(2)
	if len(h.calls) != 1 || !strings.HasPrefix(h.calls[0], "play album") {
		t.Fatalf("Shuffle with shuffle on just plays: %v", h.calls)
	}
}

func TestAlbumViewMouse(t *testing.T) {
	lib := twoDiscLib()
	c, h := albumTestCtx(lib)
	v := NewAlbumView(lib.Albums[0])
	v.Render(117, 37, c)
	// Rows: Disc 1, three tracks, blank, Disc 2, two tracks.
	y := v.listY + 6
	v.Mouse(MouseEvent{Kind: MouseMotion, X: 10, Y: y}, c)
	if v.hover != 3 {
		t.Fatalf("hover %d", v.hover)
	}
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 10, Y: v.listY}, c)
	if v.cursor != 0 || len(h.calls) != 0 {
		t.Fatal("clicking a disc heading does nothing")
	}
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 10, Y: y, Double: true}, c)
	if len(h.calls) != 0 {
		t.Fatalf("a double-click that began on another view plays nothing: %v", h.calls)
	}
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 10, Y: y}, c)
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 10, Y: y, Double: true}, c)
	if v.cursor != 3 || h.last() != "play album 3" {
		t.Fatalf("double-click: cursor %d, %v", v.cursor, h.calls)
	}
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseRight, X: 10, Y: v.listY + 1}, c)
	if v.cursor != 0 || h.last() != "menu 1" {
		t.Fatalf("right-click: %v", h.calls)
	}
}

func TestAlbumViewScrolls(t *testing.T) {
	al := albumTestLib.Albums[0]
	for _, a := range albumTestLib.Albums {
		if len(a.Tracks) > len(al.Tracks) {
			al = a
		}
	}
	c, _ := albumTestCtx(albumTestLib)
	v := NewAlbumView(al)
	v.Render(80, 16, c)
	for range len(al.Tracks) {
		v.Action(keymap.NavDown, 1, c)
		r := v.rowOf[v.cursor]
		if r < v.top || r >= v.top+v.listH {
			t.Fatalf("cursor row %d outside %d+%d", r, v.top, v.listH)
		}
	}
}

func TestAlbumViewReindex(t *testing.T) {
	lib := twoDiscLib()
	c, _ := albumTestCtx(lib)
	v := NewAlbumView(lib.Albums[0])
	v.Render(117, 37, c)
	v.Action(keymap.NavDown, 2, c)
	lib2 := twoDiscLib()
	if v.Reindex(lib2) == nil || v.Selected(c)[0] != lib2.Track(v.Selected(c)[0].PID) || v.cursor != 2 {
		t.Fatal("reindex keeps the track")
	}
	if v.Reindex(emptyLib) != nil {
		t.Fatal("a removed album reindexes to nil")
	}
}

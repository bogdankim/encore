package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

func TestArtistsViewFocus(t *testing.T) {
	c, h := albumTestCtx(albumTestLib)
	v := NewArtistsView(albumTestLib)
	v.Render(117, 37, c)
	if ok, _ := v.Action(keymap.FocusLeft, 1, c); ok {
		t.Fatal("h on the list falls through to the sidebar")
	}
	v.Action(keymap.NavDown, 2, c)
	ar := albumTestLib.Artists[2]
	if v.page.artist != ar {
		t.Fatalf("page shows %q, want %q", v.page.artist.Name, ar.Name)
	}
	if ok, _ := v.Action(keymap.Open, 1, c); !ok || !v.right {
		t.Fatal("enter moves focus to the page")
	}
	v.Render(117, 37, c)
	v.Action(keymap.Open, 1, c)
	if h.played.src != (player.Source{Kind: player.KindArtist, ID: ar.Name, Title: ar.Name}) || h.played.i != 0 {
		t.Fatalf("played %+v at %d", h.played.src, h.played.i)
	}
	if h.played.tracks[0].Plays < h.played.tracks[len(h.played.tracks)-1].Plays {
		t.Fatal("top songs are most played first")
	}
	if ok, _ := v.Action(keymap.FocusLeft, 1, c); !ok || v.right {
		t.Fatal("h on the page returns to the list")
	}
	v.Action(keymap.FocusRight, 1, c)
	v.Action(keymap.Back, 1, c)
	if v.right {
		t.Fatal("esc on the page returns to the list")
	}
}

func TestArtistPageMovesFromSongsIntoGrid(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	var ar *library.Artist
	for _, a := range albumTestLib.Artists {
		if len(a.Albums) >= 2 && a.Tracks > topSongs {
			ar = a
			break
		}
	}
	v := NewArtistView(ar)
	if v.Key() != "artist:"+ar.Name {
		t.Fatalf("key %q", v.Key())
	}
	out := ansi.Strip(v.Render(117, 37, c))
	for _, want := range []string{ar.Name, artistMeta(ar), "Top Songs", "Albums"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if v.page.songsN != topSongs {
		t.Fatalf("%d songs shown", v.page.songsN)
	}
	v.Action(keymap.NavDown, topSongs-1, c)
	if v.page.inGrid {
		t.Fatal("still in the songs")
	}
	v.Action(keymap.NavDown, 1, c)
	if !v.page.inGrid {
		t.Fatal("j past the last song moves into the grid")
	}
	if got := v.Selected(c); len(got) != len(ar.Albums[0].Tracks) {
		t.Fatalf("grid selection is the album's tracks, got %d", len(got))
	}
	v.Action(keymap.NavUp, 1, c)
	if v.page.inGrid || v.page.songCur != topSongs-1 {
		t.Fatal("k from the first grid row returns to the last song")
	}
	v.Action(keymap.NavBottom, 1, c)
	v.Action(keymap.NavTop, 1, c)
	if v.page.inGrid || v.page.songCur != 0 {
		t.Fatal("gg goes to the first song")
	}
}

func TestArtistPageHidesSongsWhenShort(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	v := NewArtistView(albumTestLib.Artists[0])
	v.Render(117, 14, c)
	if v.page.songsN != 0 || !v.page.inGrid {
		t.Fatalf("songs shown at height 14: %d", v.page.songsN)
	}
	if ok, _ := v.Action(keymap.Open, 1, c); !ok {
		t.Fatal("enter opens the album")
	}
}

func TestArtistsViewMouse(t *testing.T) {
	c, h := albumTestCtx(albumTestLib)
	v := NewArtistsView(albumTestLib)
	v.Render(117, 37, c)
	v.Mouse(MouseEvent{Kind: MouseMotion, X: 3, Y: listTop + 4}, c)
	if v.hover != 4 {
		t.Fatalf("hover %d", v.hover)
	}
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 3, Y: listTop + 4}, c)
	if v.cursor != 4 || v.right {
		t.Fatalf("click selects: cursor %d right %v", v.cursor, v.right)
	}
	v.Render(117, 37, c)
	px := v.pageX() + 4
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: px, Y: listTop + v.page.songsY + 1}, c)
	v.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: px, Y: listTop + v.page.songsY + 1, Double: true}, c)
	if !v.right || h.last() != "play artist 1" {
		t.Fatalf("double-click on a song: right %v, %v", v.right, h.calls)
	}
	v.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: 5, Y: listTop + 5}, c)
	if v.top != 3 {
		t.Fatalf("wheel scrolls the list: top %d", v.top)
	}
}

func TestArtistsViewFilterAndReindex(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	v := NewArtistsView(albumTestLib)
	v.Render(117, 37, c)
	v.Action(keymap.Filter, 1, c)
	typeText(v, c, "beatles")
	if len(v.items) != 1 || v.page.artist.Name != "The Beatles" {
		t.Fatalf("filter: %d artists", len(v.items))
	}
	if _, meta := v.Title(c); !strings.HasPrefix(meta, "1 of ") {
		t.Fatalf("meta %q", meta)
	}
	v.Action(keymap.InputCancel, 1, c)
	if len(v.items) != len(albumTestLib.Artists) || v.selected().Name != "The Beatles" {
		t.Fatal("cancel restores the list and keeps the artist")
	}
	v.Reindex(albumTestLib)
	if v.selected().Name != "The Beatles" {
		t.Fatal("reindex keeps the artist")
	}
	if v.Reindex(emptyLib) == nil || v.page.artist != nil {
		t.Fatal("empty library: no artist")
	}
	if NewArtistView(albumTestLib.Artists[0]).Reindex(emptyLib) != nil {
		t.Fatal("a removed artist reindexes to nil")
	}
}

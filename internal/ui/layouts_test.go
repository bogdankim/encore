package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

func TestLayoutsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "layouts.json")
	l := LoadLayouts(path)
	if l.List(KeyRecent) {
		t.Fatal("a page with no choice is a grid")
	}
	l.Set(KeyRecent, true)
	l.Set(KeyAlbums, true)
	l.Set(KeyAlbums, false)
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	again := LoadLayouts(path)
	if !again.List(KeyRecent) || again.List(KeyAlbums) || again.List(KeyArtists) {
		t.Fatalf("after reload: %v", again.pages)
	}
}

func TestLayoutsBadFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layouts.json")
	for _, body := range []string{"not json", "null", `{"recent":"sideways"}`} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		l := LoadLayouts(path)
		if l.List(KeyRecent) {
			t.Errorf("%q should read as a grid", body)
		}
		l.Set(KeyAlbums, true) // and stay usable
	}
}

func TestLayoutsWithoutPathOrStore(t *testing.T) {
	l := LoadLayouts("")
	l.Set(KeyRecent, true)
	if !l.List(KeyRecent) {
		t.Fatal("kept in memory")
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	var none *Layouts
	none.Set(KeyRecent, true)
	if none.List(KeyRecent) || none.Flush() != nil {
		t.Fatal("a nil store remembers nothing")
	}
}

// The choice follows each page: v and the Grid and List controls save it, a
// new view restores it, and other pages keep their own.
func TestAlbumsViewRemembersLayout(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	l := LoadLayouts("")

	recent := NewRecentView(albumTestLib)
	recent.keepLayouts(l)
	recent.Render(117, 37, c)
	recent.Action(keymap.ViewToggleGrid, 1, c)
	if !l.List(KeyRecent) {
		t.Fatal("v should save the list choice")
	}

	albums := NewAlbumsView(albumTestLib)
	albums.keepLayouts(l)
	if albums.ListLayout() {
		t.Fatal("Albums keeps its own choice")
	}

	fresh := NewRecentView(albumTestLib)
	fresh.keepLayouts(l)
	if !fresh.ListLayout() {
		t.Fatal("a new Recently Added view should open in the list")
	}
	fresh.Render(117, 37, c)
	for _, it := range fresh.controls(c) {
		if it.text == " Grid " {
			it.do()
		}
	}
	if l.List(KeyRecent) {
		t.Fatal("the Grid control should save too")
	}
}

// Artist pages share one choice, and a page further back in history picks up
// a change made on another when it comes back.
func TestArtistPagesShareLayout(t *testing.T) {
	var ar *library.Artist
	for _, a := range albumTestLib.Artists {
		if len(a.Albums) > 0 {
			ar = a
			break
		}
	}
	if ar == nil {
		t.Fatal("no artist with albums")
	}
	l := LoadLayouts("")
	first, second := NewArtistView(ar), NewArtistView(ar)
	first.keepLayouts(l)
	second.keepLayouts(l)
	second.page.grid.SetList(true)
	if !l.List(layoutArtist) || l.List(layoutArtist+layoutEssential) || l.List(KeyArtists) {
		t.Fatalf("saved %v", l.pages)
	}
	first.keepLayouts(l)
	if !first.page.grid.IsList() {
		t.Fatal("the older page should follow the saved choice")
	}
}

// The app applies the saved choice whenever a page opens, including at start
// and on the way back through history.
func TestAppRestoresLayoutOnOpen(t *testing.T) {
	f := bridge.NewFakeWithClock(1, time.Now)
	pl := player.New(f, albumTestLib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	path := filepath.Join(t.TempDir(), "layouts.json")
	start := func() *App {
		return New(Options{
			Client: f, Player: pl, Library: albumTestLib, Keys: keymap.New(), Start: KeyRecent,
			Theme:    func(bool) *theme.Theme { return th },
			Services: Services{Layouts: LoadLayouts(path)},
		})
	}

	a := start()
	a.Init()
	a.goSection(KeyAlbums, "Albums")
	v := a.nav.cur.(*AlbumsView)
	if v.ListLayout() {
		t.Fatal("Albums starts as a grid")
	}
	v.grid.SetList(true)
	if err := a.svc.Layouts.Flush(); err != nil {
		t.Fatal(err)
	}

	a = start() // a restart
	a.Init()
	if a.nav.cur.(*AlbumsView).ListLayout() {
		t.Fatal("Recently Added keeps its own choice")
	}
	a.goSection(KeyAlbums, "Albums")
	if !a.nav.cur.(*AlbumsView).ListLayout() {
		t.Fatal("Albums should reopen in the list after a restart")
	}
}

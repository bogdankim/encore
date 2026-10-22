package ui

import (
	"context"
	"runtime"
	"testing"
	"time"
	"weak"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/search"
)

// A library swap must let go of the old library. The filters on Songs, Albums
// and the search overlay keep corpora for the slices they filtered last, and
// those hold pointers into the library they came from.
func TestLibrarySwapFreesTheOldLibrary(t *testing.T) {
	typeFilter := func(a *App, q string) {
		a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
		for _, r := range q {
			a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	}
	cases := []struct {
		name string
		use  func(a *App)
	}{
		{"songs filter", func(a *App) { a.goSection(KeySongs, "Songs"); typeFilter(a, "love") }},
		{"albums filter", func(a *App) { a.goSection(KeyAlbums, "Albums"); typeFilter(a, "love") }},
		{"artists filter", func(a *App) { a.goSection(KeyArtists, "Artists"); typeFilter(a, "love") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newShellApp(t, 140, 42)
			old := rebuiltLibrary(t)
			a.Update(LibraryMsg{Lib: old, Index: search.NewIndex(old)})
			c.use(a)
			a.render()
			ref := weak.Make(old.All[0])
			old = nil

			fresh := rebuiltLibrary(t)
			a.Update(LibraryMsg{Lib: fresh, Index: search.NewIndex(fresh)})
			c.use(a)
			a.render()
			for range 3 {
				runtime.GC()
			}
			if ref.Value() != nil {
				t.Fatal("the library from before the swap is still reachable")
			}
			runtime.KeepAlive(a)
		})
	}
}

// rebuiltLibrary is a new library with the same content as the shared one.
func rebuiltLibrary(t testing.TB) *library.Library {
	t.Helper()
	f := bridge.NewFakeWithClock(1, func() time.Time { return bridge.FakeEpoch })
	defer f.Close()
	snap, err := f.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return library.Build(snap)
}

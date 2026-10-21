package ui

import (
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

func TestStartPage(t *testing.T) {
	lib := shellLibrary(t)
	th := shellTheme()
	for _, tc := range []struct {
		start string
		page  *home.Page
		want  string
	}{
		{"", homeTestPage(), KeyRecent},
		{KeyRecent, homeTestPage(), KeyRecent},
		{KeyHome, homeTestPage(), KeyHome},
		{KeyHome, nil, KeyRecent},
		{KeyHome, &home.Page{}, KeyRecent},
		{KeyAlbums, nil, KeyAlbums},
		{KeySongs, homeTestPage(), KeySongs},
	} {
		f := bridge.NewFakeWithClock(1, time.Now)
		pl := player.New(f, lib.Track)
		a := New(Options{
			Client: f, Player: pl, Library: lib, Keys: keymap.New(),
			Theme:    func(bool) *theme.Theme { return th },
			HomePage: tc.page, Start: tc.start,
		})
		if got := a.nav.cur; got != a.sections[tc.want] || got == nil {
			t.Errorf("Start %q with page %v: opened %v, want %s", tc.start, tc.page != nil, got, tc.want)
		}
		pl.Close()
		f.Close()
	}
}

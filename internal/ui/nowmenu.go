package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
)

// nowMenuHost opens the playing song's menu (App.NowPlayingMenu).
type nowMenuHost interface {
	NowPlayingMenu(x, y int) tea.Cmd
}

// NowPlayingMenu opens the playing song's menu at a screen cell (the
// player bar's title or art, Up Next's playing row): its album and
// artist, then Favorite for a library song, and Open in Music.app for an
// Apple Music one.
func (a *App) NowPlayingMenu(x, y int) tea.Cmd {
	if a.snap.NowPlaying.Track == nil {
		return nil
	}
	items := []MenuItem{
		{Label: "Go to Album", Run: func(h Host) tea.Cmd { return h.Do(keymap.GoNowPlaying) }},
		{Label: "Go to Artist", Run: func(Host) tea.Cmd { return a.openPlayingArtist() }},
	}
	if t := a.snap.Track; t != nil && t.InLibrary {
		items = menuGroup(items, MenuItem{Label: "Favorite", Key: keymap.TrackFavoritePlaying, favOf: []*library.Track{t}})
	}
	if ci := a.snap.Catalog; ci != nil {
		if p, ok := songPage(ci.StoreID, ci.AlbumID, ci.Title); ok {
			items = menuGroup(items, musicMenuItem(p))
		}
	}
	return a.PushOverlay(NewMenu(x, y, nil, items...))
}

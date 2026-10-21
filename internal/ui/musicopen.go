package ui

import (
	"context"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Open in Music.app: an Apple Music song, album or playlist, shown in
// Music through Services.OpenMusic (a music:// link), whether or not the
// library has it. Music's own + adds it to the library from there;
// neither Apple Events nor MediaRemote can add from outside Music (see
// docs/research, "Adding to the library"). Menus, the catalog album
// page's header, Home's footer, `M` and `space o m` all offer it for what
// has a link: a song, an album or a playlist from Apple Music. Library
// items, artists and stations have none here.

const (
	openingInMusicM = "Opening in Music.app"
	musicOnlyM      = "Only Apple Music songs, albums and playlists open in Music.app"
)

// musicPage is an Apple Music item Music.app can show, as
// Services.OpenMusic takes it: kind home.TypeAlbum, TypePlaylist or
// TypeSong, and the item's id.
type musicPage struct {
	kind, id string
	name     string // for the footer
}

// albumPage is the page of catalog album id.
func albumPage(id, name string) (musicPage, bool) {
	return musicPage{home.TypeAlbum, id, name}, digits(id)
}

// songPage is the page of catalog song id: its album's page with the
// song picked ("<album id>?i=<song id>") when the album is known, else
// the song's own.
func songPage(id, albumID, name string) (musicPage, bool) {
	if digits(albumID) {
		return musicPage{home.TypeAlbum, albumID + "?i=" + id, name}, digits(id)
	}
	return musicPage{home.TypeSong, id, name}, digits(id)
}

// musicPageOf is the page of s when it is an Apple Music song or album.
func musicPageOf(s Subject) (musicPage, bool) {
	switch {
	case player.IsCatalog(s.track):
		ci, _ := catalogItemOf(s.track)
		return songPage(ci.StoreID, ci.AlbumID, ci.Title)
	case s.album != nil && isCatalogAlbum(s.album):
		id, ok := catalogAlbumID(s.album.Key)
		p, valid := albumPage(strconv.FormatInt(id, 10), s.album.Title)
		return p, ok && valid
	}
	return musicPage{}, false
}

// homeMusicPage is the page of a Home tile: an album, a song or a
// playlist. Stations and artists have none.
func homeMusicPage(it home.Item) (musicPage, bool) {
	switch it.Type {
	case home.TypeAlbum:
		return albumPage(it.ID, it.Title)
	case home.TypeSong:
		return songPage(it.ID, "", it.Title)
	case home.TypePlaylist:
		return musicPage{home.TypePlaylist, it.ID, it.Title}, it.ID != ""
	}
	return musicPage{}, false
}

// musicMenuItem is a menu's Open in Music.app for p.
func musicMenuItem(p musicPage) MenuItem {
	return MenuItem{
		Label: keymap.Name(keymap.MusicOpen),
		Key:   keymap.MusicOpen,
		Run:   func(h Host) tea.Cmd { return h.OpenInMusic(p.kind, p.id, p.name) },
	}
}

// musicMenuItems are the Open in Music.app group for a menu of ts: one
// song, or every song of one catalog album as the album.
func musicMenuItems(ts []*library.Track) []MenuItem {
	if p, ok := musicPageOf(subjectOf(ts)); ok {
		return []MenuItem{musicMenuItem(p)}
	}
	return nil
}

// musicTail is a catalog album page's header link to Music.app.
func musicTail(c *Ctx, p musicPage) []headerLine {
	name := keymap.Name(keymap.MusicOpen)
	return []headerLine{{text: name, tone: toneDim, hint: c.HintFor(name, keymap.MusicOpen),
		link: func() tea.Cmd { return c.Host.OpenInMusic(p.kind, p.id, p.name) }}}
}

// openSelectionInMusic opens the focused selection in Music.app (`M`) for
// the views and panes that leave it to the app; Home opens its tiles
// itself.
func (a *App) openSelectionInMusic() tea.Cmd {
	if p, ok := musicPageOf(a.focusedSubject()); ok {
		return a.OpenInMusic(p.kind, p.id, p.name)
	}
	return a.setStatus(musicOnlyM, false)
}

// OpenInMusic asks Music.app to show an Apple Music item (kind "album",
// "playlist" or "song" and its id), and says so in the footer.
func (a *App) OpenInMusic(kind, id, name string) tea.Cmd {
	open := a.svc.OpenMusic
	if open == nil {
		return a.setStatus("Music.app can’t open "+name+" from here", false)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := open(ctx, kind, id); err != nil {
			return StatusMsg{Text: "Couldn’t open " + name + " in Music.app", Err: true}
		}
		return StatusMsg{Text: openingInMusicM}
	}
}

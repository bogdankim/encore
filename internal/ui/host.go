package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// Play starts tracks[i] within the context of tracks.
// Catalog songs (stand-ins, see player.StandIn) play through
// PlayCatalog; a context mixing them with library tracks keeps the kind
// of tracks[i].
func (a *App) Play(src player.Source, tracks []*library.Track, i int) tea.Cmd {
	if len(tracks) == 0 {
		return nil
	}
	i = min(max(i, 0), len(tracks)-1)
	if player.IsCatalog(tracks[i]) {
		var cat []*library.Track
		at := 0
		for k, t := range tracks {
			if player.IsCatalog(t) {
				if k < i {
					at++
				}
				cat = append(cat, t)
			}
		}
		items, start := catalogItemsOf(cat, at)
		if start < 0 {
			return a.setStatus("This song isn’t available to stream", false)
		}
		if src.Kind == "" {
			src.Kind = player.KindCatalog
		}
		a.player.PlayCatalog(src, items, start)
		return nil
	}
	if !allLibrary(tracks) {
		var lib []*library.Track
		at := 0
		for k, t := range tracks {
			if !player.IsCatalog(t) {
				if k < i {
					at++
				}
				lib = append(lib, t)
			}
		}
		tracks, i = lib, at
	}
	a.player.PlayContext(src, tracks, i)
	return nil
}

func allLibrary(ts []*library.Track) bool {
	for _, t := range ts {
		if player.IsCatalog(t) {
			return false
		}
	}
	return true
}

func (a *App) PlayNext(tracks []*library.Track) tea.Cmd {
	if len(tracks) == 0 {
		return nil
	}
	runs := splitRuns(tracks)
	for i := len(runs) - 1; i >= 0; i-- { // each run goes in front of the last
		if r := runs[i]; player.IsCatalog(r[0]) {
			items, _ := catalogItemsOf(r, 0)
			a.player.PlayNextCatalog(items...)
		} else {
			a.player.PlayNext(r...)
		}
	}
	return a.setStatus(queueNote("Playing next", tracks), false)
}

func (a *App) AddToQueue(tracks []*library.Track) tea.Cmd {
	if len(tracks) == 0 {
		return nil
	}
	for _, r := range splitRuns(tracks) {
		if player.IsCatalog(r[0]) {
			items, _ := catalogItemsOf(r, 0)
			a.player.AddCatalogToQueue(items...)
		} else {
			a.player.AddToQueue(r...)
		}
	}
	return a.setStatus(queueNote("Added to Up Next", tracks), false)
}

func queueNote(what string, ts []*library.Track) string {
	if len(ts) == 1 {
		return what + ": " + ts[0].Title
	}
	return what + ": " + plural(len(ts), "song", "songs")
}

// ToggleFavorite favorites every track, or unfavorites them all when every
// one already is (as the context menu does). The change shows at once and
// reverts if Music refuses it.
func (a *App) ToggleFavorite(tracks []*library.Track) tea.Cmd {
	if len(tracks) == 0 {
		return nil
	}
	if !allLibrary(tracks) {
		var lib []*library.Track
		for _, t := range tracks {
			if !player.IsCatalog(t) {
				lib = append(lib, t)
			}
		}
		if tracks = lib; len(tracks) == 0 {
			return a.setStatus(catalogFavM, false)
		}
	}
	c := a.baseCtx()
	all := true
	pids := make([]string, 0, len(tracks))
	for _, t := range tracks {
		pids = append(pids, t.PID)
		all = all && c.IsFavorite(t)
	}
	return a.setFavorite(pids, all)
}

func (a *App) setFavorite(pids []string, was bool) tea.Cmd {
	for _, pid := range pids {
		a.fav[pid] = !was
	}
	client := a.client
	return func() tea.Msg {
		for _, pid := range pids {
			ctx, cancel := context.WithTimeout(context.Background(), bridge.DefaultTimeout)
			err := client.SetFavorite(ctx, pid, !was)
			cancel()
			if err != nil {
				return favoriteMsg{pids: pids, was: was, err: err}
			}
		}
		return favoriteMsg{pids: pids, was: was}
	}
}

func (a *App) onFavorite(msg favoriteMsg) tea.Cmd {
	if msg.err != nil {
		for _, pid := range msg.pids {
			a.fav[pid] = msg.was
		}
		return a.setStatus(humanize(msg.err), true)
	}
	if a.o.RefreshLibrary != nil {
		a.o.RefreshLibrary()
	}
	return nil
}

// Open shows v in the content area and focuses it. A view with an
// Init(*Ctx) tea.Cmd method (one that loads what it shows) gets it called
// here, and again whenever history brings it back.
func (a *App) Open(v View) tea.Cmd {
	if v == nil {
		return nil
	}
	if cur := a.nav.cur; cur != nil && cur.Key() == v.Key() {
		// the page is open already: it shows the track v was opened for
		from, ok1 := v.(interface{ wanted() trackWant })
		to, ok2 := cur.(interface{ setWant(trackWant) })
		if ok1 && ok2 && from.wanted() != (trackWant{}) {
			to.setWant(from.wanted())
		}
	}
	a.nav.open(v)
	a.setFocus(focusContent)
	a.sidebar.syncTo(a.nav.cur.Key())
	return a.initView()
}

// initView calls the open view's Init, if it has one.
func (a *App) initView() tea.Cmd {
	if in, ok := a.nav.cur.(interface{ Init(*Ctx) tea.Cmd }); ok {
		return in.Init(a.viewCtx())
	}
	return nil
}

// OpenAlbum opens an album page; a catalog album (key "catalog:<id>")
// opens its Apple Music page.
func (a *App) OpenAlbum(al *library.Album) tea.Cmd {
	if al == nil {
		return nil
	}
	if isCatalogAlbum(al) {
		id, ok := catalogAlbumID(al.Key)
		if !ok {
			return a.setStatus("No album page for this song", false)
		}
		seed, ok := catalogAlbums[id]
		if !ok {
			seed = catalog.Album{ID: id, Name: al.Title, Artist: al.Artist, Artwork: al.ArtPID}
		}
		return a.Open(newCatalogAlbumView(seed))
	}
	if viewCtors.Album == nil {
		return a.notYet("Album pages")
	}
	return a.Open(viewCtors.Album(al))
}

func (a *App) OpenArtist(ar *library.Artist) tea.Cmd {
	if ar == nil {
		return nil
	}
	if viewCtors.Artist == nil {
		return a.notYet("Artist pages")
	}
	return a.Open(viewCtors.Artist(ar))
}

func (a *App) OpenPlaylist(p *library.Playlist) tea.Cmd {
	if p == nil {
		return nil
	}
	return a.Open(newPlaylistView(p))
}

// PushOverlay opens o on top. An overlay with an Init(*Ctx) tea.Cmd method
// gets it called here.
func (a *App) PushOverlay(o Overlay) tea.Cmd {
	if o == nil {
		return nil
	}
	a.overlays = append(a.overlays, o)
	a.keys.Reset()
	a.pending = keymap.Result{}
	if in, ok := o.(interface{ Init(*Ctx) tea.Cmd }); ok {
		return in.Init(a.overlayCtx(o))
	}
	return nil
}

// CloseOverlay closes the top overlay, calling its Close method if it has
// one, however it was closed.
func (a *App) CloseOverlay() tea.Cmd {
	if len(a.overlays) == 0 {
		return nil
	}
	o := a.overlays[len(a.overlays)-1]
	a.overlays = a.overlays[:len(a.overlays)-1]
	a.keys.Reset()
	a.overlayDrag = false
	if cl, ok := o.(interface{ Close() }); ok {
		cl.Close()
	}
	return nil
}

func (a *App) Menu(screenX, screenY int, tracks []*library.Track) tea.Cmd {
	if len(tracks) == 0 {
		return nil
	}
	if overlayCtors.Menu == nil {
		return a.notYet("The context menu")
	}
	return a.PushOverlay(overlayCtors.Menu(a.services(), screenX, screenY, tracks))
}

func (a *App) Status(msg string) tea.Cmd { return a.setStatus(msg, false) }

// Do runs an action as if its key was pressed with no overlay open: the
// focused pane gets it first, then the app.
func (a *App) Do(act keymap.Action) tea.Cmd { return a.run(act, 1) }

func (a *App) notYet(what string) tea.Cmd {
	verb := " isn't"
	if len(what) > 0 && what[len(what)-1] == 's' {
		verb = " aren't"
	}
	return a.setStatus(what+verb+" available yet", false)
}

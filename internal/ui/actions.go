package ui

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/search"
)

const (
	seekShort = 10 * time.Second
	seekLong  = 30 * time.Second
	volStep   = 5
	wheelSeek = 5 * time.Second
)

// global handles actions no pane took.
func (a *App) global(act keymap.Action, n int) tea.Cmd {
	pl := a.player
	switch act {
	case keymap.FocusLeft:
		a.moveFocus(-1, false)
	case keymap.FocusRight:
		a.moveFocus(1, false)
	case keymap.FocusNext:
		a.moveFocus(1, true)
	case keymap.FocusPrev:
		a.moveFocus(-1, true)
	case keymap.Back:
		switch {
		case a.fullLyrics:
			a.fullLyrics = false
			a.relayout()
		case a.focus == focusRight:
			a.setFocus(focusContent)
		case a.nav.cur != nil && isDetail(a.nav.cur.Key()):
			// esc leaves a detail page; it never moves between library
			// sections (history does that: ctrl+o, mouse button 4, ‹)
			a.nav.goBack()
			a.syncSidebar()
			return a.initView()
		}
	case keymap.HistoryBack:
		a.nav.goBack()
		a.syncSidebar()
		return a.initView()
	case keymap.HistoryForward:
		a.nav.goForward()
		a.syncSidebar()
		return a.initView()

	case keymap.PlayerPlayPause:
		pl.Toggle()
	case keymap.PlayerNext:
		pl.Next()
	case keymap.PlayerPrev:
		pl.Previous()
	case keymap.PlayerSeekFwd:
		pl.SeekRelative(seekShort * time.Duration(n))
	case keymap.PlayerSeekBack:
		pl.SeekRelative(-seekShort * time.Duration(n))
	case keymap.PlayerSeekFwdLong:
		pl.SeekRelative(seekLong * time.Duration(n))
	case keymap.PlayerSeekBackLong:
		pl.SeekRelative(-seekLong * time.Duration(n))
	case keymap.PlayerVolUp:
		pl.AdjustVolume(volStep * n)
	case keymap.PlayerVolDown:
		pl.AdjustVolume(-volStep * n)
	case keymap.PlayerShuffle:
		// read the player, not a.snap: the snapshot may predate a toggle
		// made earlier in this frame
		pl.SetShuffle(!pl.State().NowPlaying.Shuffle)
		a.refreshSnap()
	case keymap.PlayerRepeat:
		pl.CycleRepeat()
	case keymap.TrackFavoritePlaying:
		ct := a.snap.NowPlaying.Track
		if ct == nil {
			return a.setStatus("Nothing is playing", false)
		}
		if a.snap.Catalog != nil {
			return a.setStatus(catalogFavM, false)
		}
		return a.setFavorite([]string{ct.PID}, a.baseCtx().IsFavoritePID(ct.PID, ct.Favorited))

	case keymap.SearchAll:
		return a.openSearch(search.All)
	case keymap.SearchSongs:
		return a.openSearch(search.Songs)
	case keymap.SearchAlbums:
		return a.openSearch(search.Albums)
	case keymap.SearchArtists:
		return a.openSearch(search.Artists)
	case keymap.SearchPlaylists:
		return a.openSearch(search.Playlists)
	case keymap.SearchCatalog:
		return a.openSearch(scopeCatalog)
	case keymap.Help:
		return a.openOverlay("Help", overlayCtors.Help)
	case keymap.Palette:
		return a.openOverlay("The command palette", overlayCtors.Palette)
	case keymap.AirPlay:
		return a.openOverlay("AirPlay", overlayCtors.AirPlay)
	case keymap.SettingsLastfm:
		return a.openOverlay("last.fm setup", overlayCtors.Lastfm)

	case keymap.PaneLyrics:
		return a.toggleRight(rightLyrics)
	case keymap.PaneQueue:
		return a.toggleRight(rightQueue)
	case keymap.LyricsFullscreen:
		return a.openFullLyrics()
	case keymap.SidebarToggle:
		if a.lay.RailOn && a.sidebarOn {
			// folded by the window size: show it over the content
			return a.openDrawer(false)
		}
		a.sidebarOn = !a.sidebarOn
		a.relayout()
		if a.sidebarOn && !a.lay.SidebarOn {
			return a.openDrawer(false)
		}

	case keymap.GoHome:
		return a.goSection(KeyHome, "Home")
	case keymap.GoRecent:
		return a.goSection(KeyRecent, "Recently Added")
	case keymap.GoArtists:
		return a.goSection(KeyArtists, "Artists")
	case keymap.GoAlbums:
		return a.goSection(KeyAlbums, "Albums")
	case keymap.GoSongs:
		return a.goSection(KeySongs, "Songs")
	case keymap.GoFavorites:
		return a.goSection(KeyFavorites, "Favorites")
	case keymap.GoPlaylists:
		return a.goPlaylists()
	case keymap.GoNowPlaying:
		switch t := a.snap.Track; {
		case a.snap.NowPlaying.Track == nil:
			return a.setStatus("Nothing is playing", false)
		case a.snap.Catalog != nil:
			return a.openPlayingCatalog(false)
		case t == nil:
			return a.openPlayingOutside(false)
		case t.Album == nil:
			return a.setStatus("No album page for this song", false)
		default:
			return a.OpenAlbum(t.Album)
		}

	case keymap.OpenAlbumInCatalog, keymap.OpenArtistInCatalog, keymap.OpenAlbumInLibrary, keymap.OpenArtistInLibrary:
		return a.OpenIn(act, a.focusedSubject())
	case keymap.MusicOpen:
		return a.openSelectionInMusic()

	case keymap.Quit:
		return tea.Quit
	case keymap.Redraw:
		if a.artR != nil {
			a.artR.Reload()
		}
		return tea.ClearScreen
	}
	return nil
}

func (a *App) syncSidebar() {
	if a.nav.cur != nil {
		a.sidebar.syncTo(a.nav.cur.Key())
	}
}

// moveFocus steps between the visible panes; cycle wraps around.
func (a *App) moveFocus(d int, cycle bool) {
	var order []focusArea
	if a.lay.SidebarOn {
		order = append(order, focusSidebar)
	}
	if a.lay.ContentOn {
		order = append(order, focusContent)
	}
	if a.lay.RightOn {
		order = append(order, focusRight)
	}
	if len(order) == 0 {
		return
	}
	i := 0
	for j, f := range order {
		if f == a.focus {
			i = j
		}
	}
	i += d
	switch {
	case cycle:
		i = (i + len(order)) % len(order)
	case i < 0 || i >= len(order):
		return
	}
	a.setFocus(order[i])
}

func (a *App) goSection(key, name string) tea.Cmd {
	if a.nav.cur != nil && a.nav.cur.Key() == key {
		a.setFocus(focusContent)
		return nil
	}
	v := a.section(key)
	if v == nil {
		return a.notYet(name)
	}
	cmd := a.Open(v)
	a.sidebar.syncTo(key)
	return cmd
}

func (a *App) goPlaylists() tea.Cmd {
	if !a.lay.SidebarOn {
		return a.openDrawer(true)
	}
	a.focus = focusSidebar
	for i, e := range a.sidebar.entries {
		if e.pl != nil {
			a.sidebar.cursor = i
			a.sidebar.scrollToCursor()
			break
		}
	}
	return nil
}

// openFullLyrics opens full-screen lyrics, or closes them when they are
// on top (space l under the pane's width toggles them).
func (a *App) openFullLyrics() tea.Cmd {
	if _, ok := a.topOverlay().(*lyricsFull); ok {
		return a.CloseOverlay()
	}
	if overlayCtors.LyricsFull != nil {
		o := overlayCtors.LyricsFull(a.services())
		a.PushOverlay(o)
		return o.Update(paneShownMsg{}, a.overlayCtx(o))
	}
	a.fullLyrics = !a.fullLyrics
	if a.fullLyrics {
		a.right = rightLyrics
		a.focus = focusRight
	}
	a.relayout()
	return nil
}

// paneFits reports whether a right pane can show at the window's size.
func (a *App) paneFits() bool {
	return computeLayout(a.w, a.h, layoutOpts{sidebar: a.sidebarOn, sidebarW: a.sidebarW, right: true, rightW: a.rightW}).RightOn
}

// paneShown reports whether the right pane k is on screen: in the layout,
// or as the Up Next drawer.
func (a *App) paneShown(k rightKind) bool {
	if a.right == k && a.lay.RightOn {
		return true
	}
	if k == rightQueue {
		for _, o := range a.overlays {
			if _, ok := o.(*queueDrawer); ok {
				return true
			}
		}
	}
	return false
}

// toggleRight shows or hides the right pane k. When the window is too narrow
// for a pane, lyrics open full screen and Up Next opens as a drawer over the
// content.
func (a *App) toggleRight(k rightKind) tea.Cmd {
	if !a.paneFits() {
		switch k {
		case rightLyrics:
			return a.openFullLyrics()
		case rightQueue:
			return a.toggleQueueDrawer()
		}
		return nil
	}
	if a.right == k {
		a.right = rightNone
		a.fullLyrics = false
		a.relayout()
		return nil
	}
	a.right = k
	a.fullLyrics = false
	a.relayout()
	a.pane() // build it so it hears the broadcast
	return a.broadcast(paneShownMsg{})
}

func (a *App) openSearch(scope search.Scope) tea.Cmd {
	if overlayCtors.Search == nil {
		return a.notYet("Search")
	}
	return a.PushOverlay(overlayCtors.Search(a.services(), scope))
}

func (a *App) openOverlay(name string, ctor func(Services) Overlay) tea.Cmd {
	if ctor == nil {
		return a.notYet(name)
	}
	return a.PushOverlay(ctor(a.services()))
}

// barMouse handles the player bar: buttons, seeking, volume, toggles.
func (a *App) barMouse(ev MouseEvent) tea.Cmd {
	b := a.barLayout
	z := zoneNone
	if ev.X >= 0 && ev.Y >= 0 {
		z = b.hit(ev.X, ev.Y)
	}
	pl := a.player
	switch ev.Kind {
	case MouseMotion:
		switch {
		case a.bar.scrubbing:
			a.bar.scrub = b.progressFrac(ev.X)
		case a.bar.volDragging:
			if v := b.volumeAt(ev.X); v >= 0 {
				pl.SetVolume(v)
			}
		default:
			a.bar.hover = z
		}
		a.setHint(a.barHint(z))
	case MouseRelease:
		if a.bar.scrubbing {
			a.bar.scrubbing = false
			if dur := a.snap.Duration; dur > 0 {
				pl.Seek(time.Duration(a.bar.scrub * float64(dur)))
			}
		}
		a.bar.volDragging = false
	case MouseWheel:
		switch z {
		case zoneVolume:
			pl.AdjustVolume(-ev.wheelDelta() * volStep)
		case zoneProgress:
			pl.SeekRelative(time.Duration(-ev.wheelDelta()) * wheelSeek)
		}
	case MousePress:
		if ev.Button == tea.MouseRight {
			switch z {
			case zoneTitle, zoneArt, zoneArtist:
				return a.NowPlayingMenu(ev.ScreenX, ev.ScreenY)
			}
		}
		if ev.Button != tea.MouseLeft {
			return nil
		}
		switch z {
		case zonePrev:
			pl.Previous()
		case zonePlay:
			pl.Toggle()
		case zoneNext:
			pl.Next()
		case zoneShuffle:
			return a.global(keymap.PlayerShuffle, 1)
		case zoneRepeat:
			pl.CycleRepeat()
		case zoneTitle, zoneArt:
			return a.global(keymap.GoNowPlaying, 1)
		case zoneArtist:
			return a.openPlayingArtist()
		case zoneHeart:
			return a.global(keymap.TrackFavoritePlaying, 1)
		case zoneProgress:
			if a.snap.Duration > 0 && !a.snap.Loading {
				a.bar.scrubbing = true
				a.bar.scrub = b.progressFrac(ev.X)
			}
		case zoneVolume:
			if v := b.volumeAt(ev.X); v >= 0 && ev.X < b.volX+b.meter {
				a.bar.volDragging = true
				pl.SetVolume(v)
			}
		case zoneAirPlay:
			return a.global(keymap.AirPlay, 1)
		case zoneLastfm:
			return a.global(keymap.SettingsLastfm, 1)
		case zoneLyrics:
			return a.global(keymap.PaneLyrics, 1)
		case zoneQueue:
			return a.global(keymap.PaneQueue, 1)
		}
	}
	return nil
}

// openPlayingArtist opens the page of the playing track's artist, or of the
// artist it is listed under; for a station's song, the library's or the
// catalog's (see playing_outside.go).
func (a *App) openPlayingArtist() tea.Cmd {
	ct, t := a.snap.NowPlaying.Track, a.snap.Track
	switch {
	case ct == nil:
		return a.setStatus("Nothing is playing", false)
	case a.snap.Catalog != nil:
		return a.openPlayingCatalog(true)
	case t == nil:
		return a.openPlayingOutside(true)
	}
	if ar := a.lib.Artist(t.Artist); ar != nil {
		return a.OpenArtist(ar)
	}
	if t.Group != nil {
		return a.OpenArtist(t.Group)
	}
	return a.setStatus("No artist page for this song", false)
}

// openPlayingCatalog opens the Apple Music album, or artist, of the
// playing catalog song.
func (a *App) openPlayingCatalog(artist bool) tea.Cmd {
	it := a.snap.Catalog
	if artist {
		id, err := strconv.ParseInt(it.ArtistID, 10, 64)
		if err != nil || id <= 0 {
			return a.setStatus("No artist page for this song", false)
		}
		return a.Open(newCatalogArtistView(catalog.Artist{ID: id, Name: it.Artist}))
	}
	id, err := strconv.ParseInt(it.AlbumID, 10, 64)
	if err != nil || id <= 0 {
		return a.setStatus("No album page for this song", false)
	}
	seed, ok := catalogAlbums[id]
	if !ok {
		seed = catalog.Album{ID: id, Name: it.Album, Artist: it.Artist, Artwork: it.ArtURL}
	}
	return a.Open(newCatalogAlbumView(seed))
}

// barHint names a player bar control and its key for the footer.
func (a *App) barHint(z barZone) string {
	np := a.snap.NowPlaying
	switch z {
	case zonePrev:
		return a.actHint(keymap.PlayerPrev)
	case zonePlay:
		// what a click does now, not the action's "Play / Pause"
		if np.State == bridge.Playing || a.snap.Loading {
			return a.hintText("Pause", keymap.PlayerPlayPause)
		}
		return a.hintText("Play", keymap.PlayerPlayPause)
	case zoneNext:
		return a.actHint(keymap.PlayerNext)
	case zoneShuffle:
		state := "Off"
		if np.Shuffle {
			state = "On"
		}
		return a.hintText(actionName(keymap.PlayerShuffle)+": "+state, keymap.PlayerShuffle)
	case zoneRepeat:
		state := "Off"
		switch np.Repeat {
		case bridge.RepeatAll:
			state = "All"
		case bridge.RepeatOne:
			state = "One"
		}
		return a.hintText("Repeat: "+state, keymap.PlayerRepeat)
	case zoneTitle, zoneArt:
		if np.Track != nil {
			return a.actHint(keymap.GoNowPlaying)
		}
	case zoneArtist:
		return "Go to Artist"
	case zoneHeart:
		if np.Track != nil && a.baseCtx().IsFavoritePID(np.Track.PID, np.Track.Favorited) {
			return a.hintText("Unfavorite", keymap.TrackFavoritePlaying)
		}
		return a.hintText("Favorite", keymap.TrackFavoritePlaying)
	case zoneProgress:
		if a.snap.Duration > 0 {
			return a.keysHint("Seek", keymap.PlayerSeekBack, keymap.PlayerSeekFwd)
		}
	case zoneVolume:
		// the bar sets Music's own volume; a lone AirPlay speaker has its
		// own level too (in the picker), so name the speaker this one feeds
		name := "Volume " + itoa(np.Volume)
		if a.bar.airRemote && a.bar.air.more == 0 {
			name = "Music Volume " + itoa(np.Volume) + " · " + a.bar.air.fit(airNameMax)
		}
		return a.keysHint(name, keymap.PlayerVolDown, keymap.PlayerVolUp)
	case zoneAirPlay:
		name := actionName(keymap.AirPlay)
		if a.barLayout.airName == "" && a.bar.air.name != "" {
			name += " · " + a.bar.air.fit(airNameMax)
		}
		return a.hintText(name, keymap.AirPlay)
	case zoneLyrics:
		if !a.paneFits() {
			// the pane can't show at this width: the button opens full screen
			return a.hintText(actionName(keymap.LyricsFullscreen), keymap.PaneLyrics)
		}
		return a.actHint(keymap.PaneLyrics)
	case zoneQueue:
		return a.actHint(keymap.PaneQueue)
	case zoneLastfm:
		return a.hintText("last.fm needs attention", keymap.SettingsLastfm)
	}
	return ""
}

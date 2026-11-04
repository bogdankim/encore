package ui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

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

// PickPlaylist opens the playlist picker for tracks. Apple Music songs count
// when the library has them too: Music can only copy a track it holds, so the
// rest stay out and the footer says so afterwards.
func (a *App) PickPlaylist(tracks []*library.Track) tea.Cmd {
	lib := libraryTracksOf(a.lib, tracks)
	if len(lib) == 0 {
		if len(tracks) == 0 {
			return nil
		}
		return a.setStatus(catalogListM, false)
	}
	return a.PushOverlay(NewPlaylistPicker(lib, a.plRecent))
}

// libraryTracksOf is tracks with each Apple Music song swapped for its library
// copy, and left out when the library has none.
func libraryTracksOf(lib *library.Library, tracks []*library.Track) []*library.Track {
	out := make([]*library.Track, 0, len(tracks))
	for _, t := range tracks {
		if player.IsCatalog(t) {
			id, _ := strconv.ParseInt(strings.TrimPrefix(t.PID, "store:"), 10, 64)
			if t = lib.MatchSong(id, t.Title, t.Artist); t == nil {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// playlistBatch is how many songs go to Music in one call, so that a whole
// discography doesn't hold up playback commands queued behind it.
const playlistBatch = 50

// playlistAddTTL is how long songs just added stay on a playlist while
// snapshots don't show them yet.
const playlistAddTTL = 5 * time.Minute

// playlistAdd is one song Music added that a snapshot hasn't shown yet.
type playlistAdd struct {
	pl, pid string
	at      time.Time
}

// playlistAddedMsg reports AddToPlaylist or AddToNewPlaylist. name is the
// playlist's, pid its id (empty when creating it failed), and pids the songs
// that went in. skipped counts the ones it already held.
type playlistAddedMsg struct {
	name, pid string
	pids      []string
	skipped   int
	failed    int
	err       error
}

func (a *App) AddToPlaylist(pl *library.Playlist, tracks []*library.Track) tea.Cmd {
	add := playlistMissing(pl, tracks)
	if len(add) == 0 {
		return a.setStatus(alreadyInM(tracks, pl.Name), false)
	}
	name, pid, skipped := pl.Name, pl.PID, len(tracks)-len(add)
	a.plRecent = pid
	client := a.client
	return func() tea.Msg {
		msg := playlistAddedMsg{name: name, pid: pid, skipped: skipped}
		sendToPlaylist(client, &msg, add)
		return msg
	}
}

func (a *App) AddToNewPlaylist(name string, tracks []*library.Track) tea.Cmd {
	client := a.client
	add := playlistMissing(&library.Playlist{}, tracks)
	return func() tea.Msg {
		msg := playlistAddedMsg{name: name}
		ctx, cancel := context.WithTimeout(context.Background(), bridge.PlaylistTimeout)
		pid, err := client.CreatePlaylist(ctx, name)
		cancel()
		if err != nil {
			msg.err = err
			return msg
		}
		msg.pid = pid
		sendToPlaylist(client, &msg, add)
		return msg
	}
}

// sendToPlaylist adds tracks to msg.pid in batches and notes the outcome in
// msg. It stops at the first batch Music refuses.
func sendToPlaylist(client bridge.Client, msg *playlistAddedMsg, tracks []*library.Track) {
	for batch := range slices.Chunk(tracks, playlistBatch) {
		pids := make([]string, len(batch))
		for i, t := range batch {
			pids[i] = t.PID
		}
		ctx, cancel := context.WithTimeout(context.Background(), bridge.PlaylistTimeout)
		added, missing, err := client.AddToPlaylist(ctx, msg.pid, pids)
		cancel()
		if err != nil {
			msg.err = err
			return
		}
		msg.failed += len(missing)
		for _, p := range pids {
			if added > 0 && !slices.Contains(missing, p) {
				msg.pids = append(msg.pids, p)
				added--
			}
		}
	}
}

func (a *App) onPlaylistAdded(msg playlistAddedMsg) tea.Cmd {
	var cmds []tea.Cmd
	if len(msg.pids) > 0 {
		now := time.Now()
		for _, pid := range msg.pids {
			a.plAdded = append(a.plAdded, playlistAdd{msg.pid, pid, now})
		}
		a.plRecent = msg.pid
		// the open page shows them now; the snapshot catches up behind it
		cmds = append(cmds, a.setLibrary(a.lib, a.idx))
	}
	if msg.pid != "" && a.o.RefreshLibraryNow != nil {
		a.o.RefreshLibraryNow() // also shows a new playlist that stayed empty
	}
	return tea.Batch(append(cmds, a.setStatus(playlistAddedText(a.lib, msg), msg.err != nil))...)
}

// reapplyPlaylistAdds puts songs just added back on the playlists of lib,
// where a snapshot taken before Music saved them leaves them out, and forgets
// the ones lib already has.
func (a *App) reapplyPlaylistAdds(lib *library.Library) {
	now := time.Now()
	a.plAdded = slices.DeleteFunc(a.plAdded, func(ad playlistAdd) bool {
		t := lib.Track(ad.pid)
		if t == nil || now.Sub(ad.at) > playlistAddTTL {
			return true
		}
		pl := lib.Playlist(ad.pl)
		if pl == nil {
			return false // a new playlist, until a snapshot has it
		}
		if slices.ContainsFunc(pl.Tracks, func(x *library.Track) bool { return x.PID == ad.pid }) {
			return true
		}
		pl.Append(t)
		return false
	})
}

func alreadyInM(tracks []*library.Track, name string) string {
	if len(tracks) == 1 {
		return "Already in " + name
	}
	return "All " + plural(len(tracks), "song", "songs") + " are already in " + name
}

// playlistAddedText is the footer line for msg.
func playlistAddedText(lib *library.Library, msg playlistAddedMsg) string {
	if msg.err != nil && len(msg.pids) == 0 {
		return "Couldn’t add to " + msg.name + ": " + humanize(msg.err)
	}
	what := plural(len(msg.pids), "song", "songs")
	if len(msg.pids) == 1 {
		if t := lib.Track(msg.pids[0]); t != nil {
			what = t.Title
		}
	}
	s := "Added " + what + " to " + msg.name
	if msg.skipped > 0 {
		s += fmt.Sprintf(", %d already there", msg.skipped)
	}
	if n := msg.failed; n > 0 || msg.err != nil {
		if msg.err != nil {
			return s + ", then Music stopped: " + humanize(msg.err)
		}
		s += fmt.Sprintf(", %d couldn’t be added", n)
	}
	return s
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

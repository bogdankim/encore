package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
)

// What Home's tiles do. A click (or enter) plays a station or a song and
// opens an album's or an artist's page; the play badge plays any of them
// (an artist's is its station). A playlist plays when Music's cache had
// its songs; otherwise Music.app shows it, since Apple playlists can't be
// started from outside Music.

// homeHost is what Home needs from the app beyond Host.
type homeHost interface {
	// PlayStation plays an Apple Music radio station.
	PlayStation(st player.Station) tea.Cmd
}

// songLooker is a catalog that looks songs up by id (*catalog.Client).
type songLooker interface {
	LookupSongs(ctx context.Context, ids []int64) ([]catalog.Song, error)
}

// homeActivate does what a click on item i of shelf s does, or with play
// what its play badge does.
func homeActivate(c *Ctx, s home.Shelf, i int, play bool) tea.Cmd {
	it := s.Items[i]
	switch it.Type {
	case home.TypeStation:
		return homePlayStation(c, stationOf(it))
	case home.TypeSong:
		return homePlaySongs(c, s, i)
	case home.TypeAlbum:
		al := homeAlbum(it)
		if play {
			return catalogAlbumAction(keymap.PlayerPlayPause, al, c, 0, 0)
		}
		return c.Host.OpenAlbum(al)
	case home.TypeArtist:
		id, err := strconv.ParseInt(it.ID, 10, 64)
		if err != nil || id <= 0 {
			return c.Host.Status("No artist page for " + it.Title)
		}
		if play {
			return homePlayStation(c, player.Station{ID: player.ArtistStationID(id), Name: it.Title + " Station", Kind: player.StationArtist, ArtURL: it.ArtURL(catalogArtPx)})
		}
		return c.Host.Open(newCatalogArtistView(catalog.Artist{ID: id, Name: it.Title}))
	case home.TypePlaylist:
		if len(it.Tracks) > 0 {
			if _, ok := c.Catalog.(songLooker); ok {
				return homePlayPlaylist(c, it)
			}
		}
		return homeOpenInMusic(c, it)
	}
	return nil
}

// homeItemCommand runs act (Menu, PageShuffle, QueueAdd, QueuePlayNext,
// MusicOpen) on item i of shelf s. Albums and songs take them all;
// playlists have a menu and Open in Music.app too; other items only
// shuffle-play as they play. A menu opens at the screen cell (x, y).
func homeItemCommand(c *Ctx, act keymap.Action, s home.Shelf, i, x, y int) tea.Cmd {
	it := s.Items[i]
	if act == keymap.MusicOpen {
		if p, ok := homeMusicPage(it); ok {
			return c.Host.OpenInMusic(p.kind, p.id, p.name)
		}
		return c.Host.Status(musicOnlyM)
	}
	switch it.Type {
	case home.TypePlaylist:
		if act == keymap.Menu {
			return c.Host.PushOverlay(homePlaylistMenu(it, x, y))
		}
	case home.TypeAlbum:
		return catalogAlbumAction(act, homeAlbum(it), c, x, y)
	case home.TypeSong:
		ts, k := homeSongTracks(s, i)
		if k < 0 {
			return nil
		}
		switch act {
		case keymap.Menu:
			return c.Host.Menu(x, y, ts[k:k+1])
		case keymap.QueueAdd:
			return c.Host.AddToQueue(ts[k : k+1])
		case keymap.QueuePlayNext:
			return c.Host.PlayNext(ts[k : k+1])
		case keymap.PageShuffle:
			return homePlaySongs(c, s, i)
		}
	default:
		if act == keymap.PageShuffle {
			return homeActivate(c, s, i, true)
		}
	}
	return nil
}

// homePlaylistMenu is a Home playlist's menu: Play when its songs are
// known, and Open in Music.app, which a click does for the rest. Play
// acts on the selected tile, which the menu was opened for.
func homePlaylistMenu(it home.Item, x, y int) Overlay {
	var items []MenuItem
	if !homeOpensInMusic(it) {
		items = append(items, MenuItem{Label: "Play", Key: keymap.Open, Run: func(h Host) tea.Cmd { return h.Do(keymap.Open) }})
	}
	if p, ok := homeMusicPage(it); ok {
		items = menuGroup(items, musicMenuItem(p))
	}
	return NewMenu(x, y, nil, items...)
}

// homeCanPlay reports whether the item has a play badge.
func homeCanPlay(it home.Item) bool {
	switch it.Type {
	case home.TypePlaylist:
		return len(it.Tracks) > 0
	case home.TypeArtist:
		id, err := strconv.ParseInt(it.ID, 10, 64)
		return err == nil && id > 0
	}
	return true
}

// homeOpensInMusic reports whether a click on the item opens it in
// Music.app.
func homeOpensInMusic(it home.Item) bool { return it.Type == home.TypePlaylist && len(it.Tracks) == 0 }

// homePlayName names the play badge, for the hover hint.
func homePlayName(it home.Item) string {
	switch it.Type {
	case home.TypeStation:
		return "Play Station"
	case home.TypeAlbum:
		return "Play Album"
	case home.TypeSong:
		return "Play Song"
	case home.TypePlaylist:
		return "Play Playlist"
	case home.TypeArtist:
		return "Play Artist Station"
	}
	return "Play"
}

// homeOpenVerb is what enter does to the item, for the footer.
func homeOpenVerb(it home.Item) string {
	switch {
	case it.Type == home.TypeStation, it.Type == home.TypeSong:
		return "play"
	case homeOpensInMusic(it):
		return "open in Music.app"
	case it.Type == home.TypePlaylist:
		return "play"
	}
	return "open"
}

// homePlaying reports whether the item is what plays.
func homePlaying(c *Ctx, it home.Item) bool {
	switch it.Type {
	case home.TypeStation:
		return c.Player.Station != nil && c.Player.Station.ID == it.ID
	case home.TypeArtist:
		id, err := strconv.ParseInt(it.ID, 10, 64)
		return err == nil && c.Player.Station != nil && c.Player.Station.ID == player.ArtistStationID(id)
	case home.TypeAlbum:
		return playingAlbumKey(c) == player.CatalogAlbumPrefix+it.ID
	case home.TypeSong:
		return c.Player.Catalog != nil && c.Player.Catalog.StoreID == it.ID
	case home.TypePlaylist:
		return c.Player.Catalog != nil && c.Player.Source.Kind == player.KindCatalog && c.Player.Source.ID == homePlaylistSource+it.ID
	}
	return false
}

// homePlaylistSource prefixes a Home playlist's id in the play source.
const homePlaylistSource = "home-playlist:"

// homeArtKey is the art store's key for an item's artwork; an album shares
// its catalog album's.
func homeArtKey(it home.Item) string {
	if it.Type == home.TypeAlbum {
		return player.CatalogAlbumPrefix + it.ID
	}
	return "home:" + it.Type + ":" + it.ID
}

// homeAlbum is the catalog album of an album item, as grids show it.
func homeAlbum(it home.Item) *library.Album {
	id, _ := strconv.ParseInt(it.ID, 10, 64)
	return catalogAlbum(catalog.Album{ID: id, Name: it.Title, Artist: it.Subtitle, Artwork: it.ArtURL(100), Explicit: it.Explicit})
}

// stationOf is the station a station item plays.
func stationOf(it home.Item) player.Station {
	st := player.Station{ID: it.ID, Name: it.Title, ArtURL: it.ArtURL(catalogArtPx)}
	switch {
	case it.IsLive || it.StationKind == "streaming":
		st.Kind = player.StationLive
	case it.StationKind == "user" || strings.HasPrefix(it.ID, "ra.u-"):
		st.Kind = player.StationPersonal
	case it.StationKind == "artistSeeded" || it.StationKind == "artistOnly":
		st.Kind = player.StationArtist
	case it.StationKind == "discovery":
		st.Kind = player.StationDiscovery
	default:
		st.Kind = player.StationMood
	}
	return st
}

func homePlayStation(c *Ctx, st player.Station) tea.Cmd {
	h, ok := c.Host.(homeHost)
	if !ok {
		return nil
	}
	return h.PlayStation(st)
}

func homeOpenInMusic(c *Ctx, it home.Item) tea.Cmd {
	return c.Host.OpenInMusic(it.Type, it.ID, it.Title)
}

// homeSongTracks are stand-ins for the songs on shelf s, and the place of
// item i among them (-1 when it isn't a song).
func homeSongTracks(s home.Shelf, i int) ([]*library.Track, int) {
	var ts []*library.Track
	at := -1
	for k, it := range s.Items {
		if it.Type != home.TypeSong {
			continue
		}
		id, err := strconv.ParseInt(it.ID, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if k == i {
			at = len(ts)
		}
		ts = append(ts, catalogTrack(catalog.Song{
			ID: id, Name: it.Title, Artist: it.Subtitle, Artwork: it.ArtURL(100),
			Explicit: it.Explicit, Streamable: true,
		}))
	}
	return ts, at
}

// homePlaySongs plays song i of shelf s with the shelf's songs as the
// context. When the catalog can look them up, the songs play with their
// albums and lengths; without, or when that fails, as the shelf has them.
func homePlaySongs(c *Ctx, s home.Shelf, i int) tea.Cmd {
	ts, at := homeSongTracks(s, i)
	if at < 0 {
		return nil
	}
	src := player.Source{Kind: player.KindCatalog, ID: "home-shelf:" + s.ID, Title: s.Title}
	look, ok := c.Catalog.(songLooker)
	if !ok {
		return c.Host.Play(src, ts, at)
	}
	ids := make([]int64, len(ts))
	for k, t := range ts {
		ids[k], _ = strconv.ParseInt(strings.TrimPrefix(t.PID, "store:"), 10, 64)
	}
	want := ids[at]
	return catalogCmd(c.Catalog, func(ctx context.Context, _ Catalog) func(*Ctx) tea.Cmd {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		songs, err := look.LookupSongs(ctx, ids)
		return func(c *Ctx) tea.Cmd {
			if err != nil || len(songs) == 0 {
				return c.Host.Play(src, ts, at)
			}
			full := catalogTracks(songs)
			start := 0
			for k, s := range songs {
				if s.ID == want {
					start = k
				}
			}
			return c.Host.Play(src, full, start)
		}
	})
}

// homePlayPlaylist plays a playlist whose songs Music's cache had: they
// are looked up for their names, which Music's reports are matched by.
func homePlayPlaylist(c *Ctx, it home.Item) tea.Cmd {
	look := c.Catalog.(songLooker)
	ids := make([]int64, 0, len(it.Tracks))
	for _, t := range it.Tracks {
		if id, err := strconv.ParseInt(t, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	src := player.Source{Kind: player.KindCatalog, ID: homePlaylistSource + it.ID, Title: it.Title}
	return catalogCmd(c.Catalog, func(ctx context.Context, _ Catalog) func(*Ctx) tea.Cmd {
		songs, err := look.LookupSongs(ctx, ids)
		return func(c *Ctx) tea.Cmd {
			if len(songs) == 0 {
				if err != nil {
					return c.Host.Status(catalogErrText(err))
				}
				return homeOpenInMusic(c, it)
			}
			return c.Host.Play(src, catalogTracks(songs), 0)
		}
	})
}

// PlayStation plays st and makes it the context.
func (a *App) PlayStation(st player.Station) tea.Cmd {
	a.player.PlayStation(player.Source{Kind: player.KindStation, ID: st.ID, Title: st.Name}, st)
	return nil
}

package keymap

import "strings"

// Action names a UI command. The string value is the name used in the
// [keys] table of config.toml.
type Action string

const (
	NavDown     Action = "nav.down"
	NavUp       Action = "nav.up"
	NavTop      Action = "nav.top"
	NavBottom   Action = "nav.bottom"
	NavHalfDown Action = "nav.halfDown"
	NavHalfUp   Action = "nav.halfUp"
	NavPageDown Action = "nav.pageDown"
	NavPageUp   Action = "nav.pageUp"

	FocusLeft  Action = "focus.left"
	FocusRight Action = "focus.right"
	FocusNext  Action = "focus.next"
	FocusPrev  Action = "focus.prev"

	Open           Action = "open"
	Back           Action = "back"
	HistoryBack    Action = "history.back"
	HistoryForward Action = "history.forward"
	Filter         Action = "filter"
	ViewToggleGrid Action = "view.toggleGrid"
	ViewSort       Action = "view.sort"
	ViewSortRev    Action = "view.sortReverse"

	PlayerPlayPause    Action = "player.playPause"
	PlayerNext         Action = "player.next"
	PlayerPrev         Action = "player.prev"
	PlayerSeekFwd      Action = "player.seekFwd"
	PlayerSeekBack     Action = "player.seekBack"
	PlayerSeekFwdLong  Action = "player.seekFwdLong"
	PlayerSeekBackLong Action = "player.seekBackLong"
	PlayerVolUp        Action = "player.volUp"
	PlayerVolDown      Action = "player.volDown"
	PlayerShuffle      Action = "player.shuffle"
	PlayerRepeat       Action = "player.repeat"

	// PagePlay and PageShuffle are the page header's Play and Shuffle:
	// the album, playlist or artist shown, every song on Songs and
	// Favorites, the selected album in a grid.
	PagePlay    Action = "page.playAll"
	PageShuffle Action = "page.shuffleAll"

	TrackFavorite        Action = "track.favorite"
	TrackFavoritePlaying Action = "track.favoritePlaying"
	QueueAdd             Action = "queue.add"
	QueuePlayNext        Action = "queue.playNext"
	Menu                 Action = "menu"

	SearchAll       Action = "search.all"
	SearchSongs     Action = "search.songs"
	SearchAlbums    Action = "search.albums"
	SearchArtists   Action = "search.artists"
	SearchPlaylists Action = "search.playlists"
	SearchCatalog   Action = "search.appleMusic"

	PaneLyrics       Action = "pane.lyrics"
	LyricsFullscreen Action = "lyrics.fullscreen"
	PaneQueue        Action = "pane.queue"
	AirPlay          Action = "airplay"
	SidebarToggle    Action = "sidebar.toggle"

	GoHome       Action = "go.home"
	GoRecent     Action = "go.recent"
	GoArtists    Action = "go.artists"
	GoAlbums     Action = "go.albums"
	GoSongs      Action = "go.songs"
	GoFavorites  Action = "go.favorites"
	GoPlaylists  Action = "go.playlists"
	GoNowPlaying Action = "go.nowPlaying"

	// OpenAlbumInCatalog and OpenArtistInCatalog open the album or the
	// artist of the selected song, album or artist on Apple Music (a song
	// opens its album with the song selected); OpenAlbumInLibrary and
	// OpenArtistInLibrary open them in the library.
	OpenAlbumInCatalog  Action = "open.albumInAppleMusic"
	OpenArtistInCatalog Action = "open.artistInAppleMusic"
	OpenAlbumInLibrary  Action = "open.albumInLibrary"
	OpenArtistInLibrary Action = "open.artistInLibrary"

	// MusicOpen shows the selected Apple Music song, album or playlist in
	// Music.app.
	MusicOpen Action = "music.open"

	Help           Action = "Help"
	Palette        Action = "palette"
	Quit           Action = "Quit"
	Redraw         Action = "redraw"
	SettingsLastfm Action = "settings.lastfm"

	QueueMoveDown Action = "queue.moveDown"
	QueueMoveUp   Action = "queue.moveUp"
	QueueRemove   Action = "queue.remove"

	LyricsOffsetUp   Action = "lyrics.offsetUp"
	LyricsOffsetDown Action = "lyrics.offsetDown"

	InputSubmit    Action = "input.submit"
	InputCancel    Action = "input.cancel"
	InputNext      Action = "input.next"
	InputPrev      Action = "input.prev"
	InputNextScope Action = "input.nextScope"
	InputPrevScope Action = "input.prevScope"
)

// Help screen groups, in display order.
const (
	GroupNavigation = "Navigation"
	GroupPlayback   = "Playback"
	GroupSearch     = "Search"
	GroupPanes      = "Panes"
	GroupGoTo       = "Go to"
	GroupQueue      = "Queue"
	GroupLyrics     = "Lyrics"
	GroupGeneral    = "General"
)

// Groups lists the help screen groups in display order.
var Groups = []string{
	GroupNavigation, GroupPlayback, GroupSearch, GroupPanes,
	GroupGoTo, GroupQueue, GroupLyrics, GroupGeneral,
}

// ActionInfo describes an action for the help screen, which-key popup,
// command palette, context menus and hover hints. Name is the one name the
// action goes by everywhere, in Title Case with short words such as "to"
// and "from" in lower case ("Go to Playing Album", "Add to Up Next").
type ActionInfo struct {
	Action Action
	Name   string
	Group  string
	Short  string // Name under a prefix that already says it ("Albums" under "Search…"); Name when empty
}

// Label returns the which-key label: Short when set, otherwise Name.
func (i ActionInfo) Label() string {
	if i.Short != "" {
		return i.Short
	}
	return i.Name
}

// actions is the catalogue of every action, in help screen order within each
// group.
var actions = []ActionInfo{
	{NavDown, "Down", GroupNavigation, ""},
	{NavUp, "Up", GroupNavigation, ""},
	{NavTop, "Top", GroupNavigation, ""},
	{NavBottom, "Bottom", GroupNavigation, ""},
	{NavHalfDown, "Half Page Down", GroupNavigation, ""},
	{NavHalfUp, "Half Page Up", GroupNavigation, ""},
	{NavPageDown, "Page Down", GroupNavigation, ""},
	{NavPageUp, "Page Up", GroupNavigation, ""},
	{FocusLeft, "Focus Pane Left", GroupNavigation, ""},
	{FocusRight, "Focus Pane Right", GroupNavigation, ""},
	{FocusNext, "Next Pane", GroupNavigation, ""},
	{FocusPrev, "Previous Pane", GroupNavigation, ""},
	{Open, "Play or Open", GroupNavigation, ""},
	{Back, "Close or Leave Page", GroupNavigation, ""},
	{HistoryBack, "Back", GroupNavigation, ""},
	{HistoryForward, "Forward", GroupNavigation, ""},
	{Filter, "Filter", GroupNavigation, ""},
	{ViewToggleGrid, "Grid / List", GroupNavigation, ""},
	{ViewSort, "Sort", GroupNavigation, ""},
	{ViewSortRev, "Reverse Sort", GroupNavigation, ""},

	{PlayerPlayPause, "Play / Pause", GroupPlayback, ""},
	{PlayerNext, "Next Track", GroupPlayback, ""},
	{PlayerPrev, "Previous Track", GroupPlayback, ""},
	{PlayerSeekFwd, "Seek +10 s", GroupPlayback, ""},
	{PlayerSeekBack, "Seek −10 s", GroupPlayback, ""},
	{PlayerSeekFwdLong, "Seek +30 s", GroupPlayback, ""},
	{PlayerSeekBackLong, "Seek −30 s", GroupPlayback, ""},
	{PlayerVolUp, "Volume Up", GroupPlayback, ""},
	{PlayerVolDown, "Volume Down", GroupPlayback, ""},
	{PlayerShuffle, "Shuffle", GroupPlayback, ""},
	{PlayerRepeat, "Repeat", GroupPlayback, ""},
	{PagePlay, "Play Page", GroupPlayback, ""},
	{PageShuffle, "Shuffle Page", GroupPlayback, ""},
	{TrackFavorite, "Favorite", GroupPlayback, ""},
	{TrackFavoritePlaying, "Favorite Playing Song", GroupPlayback, ""},

	{SearchSongs, "Search Songs", GroupSearch, "Songs"},
	{SearchAlbums, "Search Albums", GroupSearch, "Albums"},
	{SearchArtists, "Search Artists", GroupSearch, "Artists"},
	{SearchPlaylists, "Search Playlists", GroupSearch, "Playlists"},
	{SearchCatalog, "Search Apple Music", GroupSearch, "Apple Music"},
	{SearchAll, "Search Everything", GroupSearch, ""},

	{PaneLyrics, "Lyrics", GroupPanes, ""},
	{LyricsFullscreen, "Full-Screen Lyrics", GroupPanes, ""},
	{PaneQueue, "Up Next", GroupPanes, ""},
	{AirPlay, "AirPlay", GroupPanes, ""},
	{SidebarToggle, "Sidebar", GroupPanes, ""},

	{GoHome, "Go to Home", GroupGoTo, "Home"},
	{GoRecent, "Go to Recently Added", GroupGoTo, "Recently Added"},
	{GoArtists, "Go to Artists", GroupGoTo, "Artists"},
	{GoAlbums, "Go to Albums", GroupGoTo, "Albums"},
	{GoSongs, "Go to Songs", GroupGoTo, "Songs"},
	{GoFavorites, "Go to Favorites", GroupGoTo, "Favorites"},
	{GoPlaylists, "Go to Playlists", GroupGoTo, "Playlists"},
	{GoNowPlaying, "Go to Playing Album", GroupGoTo, "Playing Album"},
	{OpenAlbumInCatalog, "Open Album in Apple Music", GroupGoTo, "Album"},
	{OpenArtistInCatalog, "Open Artist in Apple Music", GroupGoTo, "Artist"},
	{OpenAlbumInLibrary, "Open Album in Library", GroupGoTo, "Album"},
	{OpenArtistInLibrary, "Open Artist in Library", GroupGoTo, "Artist"},
	{MusicOpen, "Open in Music.app", GroupGoTo, "Music.app"},

	{QueueAdd, "Add to Up Next", GroupQueue, ""},
	{QueuePlayNext, "Play Next", GroupQueue, ""},
	{QueueMoveDown, "Move Down", GroupQueue, ""},
	{QueueMoveUp, "Move Up", GroupQueue, ""},
	{QueueRemove, "Remove from Up Next", GroupQueue, ""},

	{LyricsOffsetUp, "Offset +100 ms", GroupLyrics, ""},
	{LyricsOffsetDown, "Offset −100 ms", GroupLyrics, ""},

	{Menu, "Context Menu", GroupGeneral, ""},
	{Redraw, "Redraw Screen", GroupGeneral, ""},
	{Help, "Help", GroupGeneral, ""},
	{Palette, "Command Palette", GroupGeneral, ""},
	{SettingsLastfm, "Set Up last.fm", GroupGeneral, ""},
	{Quit, "Quit", GroupGeneral, ""},
	{InputSubmit, "Submit", GroupGeneral, ""},
	{InputCancel, "Cancel", GroupGeneral, ""},
	{InputNext, "Next Result", GroupGeneral, ""},
	{InputPrev, "Previous Result", GroupGeneral, ""},
	{InputNextScope, "Next Scope", GroupGeneral, ""},
	{InputPrevScope, "Previous Scope", GroupGeneral, ""},
}

var actionIndex = func() map[Action]int {
	m := make(map[Action]int, len(actions))
	for i, a := range actions {
		m[a.Action] = i
	}
	return m
}()

// Actions returns every known action in help screen order.
func Actions() []ActionInfo {
	return append([]ActionInfo(nil), actions...)
}

// Name is a's name, or the action string for an unknown action.
func Name(a Action) string {
	if i, ok := actionIndex[a]; ok {
		return actions[i].Name
	}
	return string(a)
}

// Info returns the name and group of a. ok is false for unknown
// actions.
func Info(a Action) (info ActionInfo, ok bool) {
	i, ok := actionIndex[a]
	if !ok {
		return ActionInfo{Action: a}, false
	}
	return actions[i], true
}

// IsNav reports whether a is a motion that takes a count prefix.
func (a Action) IsNav() bool {
	return strings.HasPrefix(string(a), "nav.")
}

// LookupAction resolves a config name to an action. Exact names match first,
// then names compared without case and punctuation ("Player.PlayPause",
// "player-play-pause"), then a unique match on the part after the dot
// ("play-pause" for player.playPause).
func LookupAction(name string) (Action, bool) {
	if _, ok := actionIndex[Action(name)]; ok {
		return Action(name), true
	}
	key := squash(name)
	if key == "" {
		return "", false
	}
	for _, a := range actions {
		if squash(string(a.Action)) == key {
			return a.Action, true
		}
	}
	var found Action
	for _, a := range actions {
		s := string(a.Action)
		if i := strings.LastIndexByte(s, '.'); i >= 0 && squash(s[i+1:]) == key {
			if found != "" {
				return "", false
			}
			found = a.Action
		}
	}
	return found, found != ""
}

func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r != '-' && r != '_' && r != '.' && r != ' ' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

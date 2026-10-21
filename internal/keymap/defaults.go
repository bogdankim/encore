package keymap

// DefaultLeader is the leader key token.
const DefaultLeader = "space"

type defaultBinding struct {
	ctx    Context
	action Action
	seqs   []string // config notation
}

// defaults is the vim keymap from docs/design.md.
var defaults = []defaultBinding{
	{Global, NavDown, []string{"j", "down"}},
	{Global, NavUp, []string{"k", "up"}},
	{Global, NavTop, []string{"gg", "home"}},
	{Global, NavBottom, []string{"G", "end"}},
	{Global, NavHalfDown, []string{"<C-d>"}},
	{Global, NavHalfUp, []string{"<C-u>"}},
	{Global, NavPageDown, []string{"pgdown"}},
	{Global, NavPageUp, []string{"pgup"}},
	{Global, FocusLeft, []string{"h", "left"}},
	{Global, FocusRight, []string{"l", "right"}},
	{Global, FocusNext, []string{"tab"}},
	{Global, FocusPrev, []string{"shift+tab"}},
	{Global, Open, []string{"enter"}},
	{Global, Back, []string{"esc", "backspace"}},
	{Global, HistoryBack, []string{"<C-o>"}},
	{Global, HistoryForward, []string{"<C-i>"}},

	{Global, PlayerPlayPause, []string{"p", "<leader><leader>"}},
	{Global, PlayerNext, []string{"]"}},
	{Global, PlayerPrev, []string{"["}},
	{Global, PlayerSeekFwd, []string{"."}},
	{Global, PlayerSeekBack, []string{","}},
	{Global, PlayerSeekFwdLong, []string{">"}},
	{Global, PlayerSeekBackLong, []string{"<"}},
	{Global, PlayerVolUp, []string{"="}},
	{Global, PlayerVolDown, []string{"-"}},
	{Global, PlayerShuffle, []string{"s"}},
	{Global, PlayerRepeat, []string{"r"}},
	{Global, TrackFavoritePlaying, []string{"F"}},

	{Global, SearchSongs, []string{"<leader>ss"}},
	{Global, SearchAlbums, []string{"<leader>sal"}},
	{Global, SearchArtists, []string{"<leader>sar"}},
	{Global, SearchPlaylists, []string{"<leader>sp"}},
	{Global, SearchCatalog, []string{"<leader>sm"}},
	{Global, SearchAll, []string{"<leader>/"}},

	{Global, PaneLyrics, []string{"<leader>l"}},
	{Global, LyricsFullscreen, []string{"<leader>L"}},
	{Global, PaneQueue, []string{"<leader>q"}},
	{Global, AirPlay, []string{"<leader>a"}},
	{Global, SidebarToggle, []string{"<leader>b"}},

	{Global, GoHome, []string{"<leader>gh"}},
	{Global, GoRecent, []string{"<leader>gr"}},
	{Global, GoArtists, []string{"<leader>gar"}},
	{Global, GoAlbums, []string{"<leader>gal"}},
	{Global, GoSongs, []string{"<leader>gs"}},
	{Global, GoFavorites, []string{"<leader>gf"}},
	{Global, GoPlaylists, []string{"<leader>gp"}},
	{Global, GoNowPlaying, []string{"<leader>gn"}},
	{Global, OpenAlbumInCatalog, []string{"<leader>oal"}},
	{Global, OpenArtistInCatalog, []string{"<leader>oar"}},
	{Global, OpenAlbumInLibrary, []string{"<leader>oll"}},
	{Global, OpenArtistInLibrary, []string{"<leader>olr"}},
	{Global, MusicOpen, []string{"M", "<leader>om"}},

	{Global, Help, []string{"?", "<leader>?"}},
	{Global, Palette, []string{":", "<C-k>"}},
	{Global, SettingsLastfm, []string{"<leader>,"}},
	{Global, Quit, []string{"q"}},
	{Global, Redraw, []string{"<C-l>"}},

	{List, Filter, []string{"/"}},
	{List, ViewToggleGrid, []string{"v"}},
	{List, ViewSort, []string{"o"}},
	{List, ViewSortRev, []string{"O"}},
	{List, TrackFavorite, []string{"f"}},
	{List, QueueAdd, []string{"a"}},
	{List, QueuePlayNext, []string{"A"}},
	{List, PagePlay, []string{"P"}},
	{List, PageShuffle, []string{"S"}},
	{List, Menu, []string{"m"}},
	{Sidebar, Menu, []string{"m"}},

	{Queue, QueueMoveDown, []string{"J"}},
	{Queue, QueueMoveUp, []string{"K"}},
	{Queue, QueueRemove, []string{"x"}},

	{Lyrics, LyricsOffsetUp, []string{"}"}},
	{Lyrics, LyricsOffsetDown, []string{"{"}},

	{Input, InputCancel, []string{"esc"}},
	{Input, InputSubmit, []string{"enter"}},
	{Input, InputNextScope, []string{"tab"}},
	{Input, InputPrevScope, []string{"shift+tab"}},
	{Input, InputNext, []string{"down", "<C-j>", "<C-n>"}},
	{Input, InputPrev, []string{"up", "<C-k>", "<C-p>"}},
}

// prefixNames label the default prefixes in the which-key popup, keyed by
// the prefix in config notation. Prefixes without a name are labelled after
// the group of their first action.
var prefixNames = map[string]string{
	"<leader>":   "Leader",
	"<leader>s":  "Search",
	"<leader>sa": "Albums / Artists",
	"<leader>g":  "Go to",
	"<leader>ga": "Artists / Albums",
	"<leader>o":  "Open in",
	"<leader>oa": "Apple Music",
	"<leader>ol": "Library",
}

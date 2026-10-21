# encore UI architecture

How `internal/ui` is put together. Read `docs/design.md` first: it has the look,
the layout, the views and the keymap. This file is about code structure, so that
several people can build views and overlays independently and they still fit.

## Principles

- `Update` and `View` never block and never do I/O. Bridge calls, disk, network
  and image decoding run in `tea.Cmd`s and come back as messages.
- `View` cost scales with the screen, not the library. Lists render only visible
  rows and cache row strings keyed by (item, width, state).
- Every region renders to exactly its rectangle (width × height). No component
  relies on lipgloss to pad or clip for it; use the helpers in `ui/draw.go`.
- Mouse and keyboard reach the same actions. A component exposes `Action(...)`
  for keymap actions and `Mouse(...)` for pointer events in local coordinates,
  and both call the same methods.

## Pieces

```
ui/
  app.go         root model: deps, layout, focus, navigation, overlays, message routing
  layout.go      rectangles from the window size; hit testing by rectangle
  draw.go        fit/pad/truncate helpers, rules, scrollbar, column layout
  ctx.go         Ctx passed to views: theme, library, player snapshot, helpers
  playerbar.go   top bar: transport, LCD (art, title, artist · album, progress), volume, AirPlay, toggles
  sidebar.go     Library sections + playlist tree
  footer.go      contextual key hints
  tracktable.go  virtualized track table (columns, sort, filter, selection, hover)
  albumgrid.go   virtualized album grid with art tiles
  views_*.go     Home, Songs, Favorites, Albums, Artists, Recently Added, Album, Playlist, Artist
  lyrics.go      lyrics pane and full-screen lyrics
  queue.go       Up Next pane
  inlibrary.go   the check for Apple Music items the library has: memoized lookups on Ctx
  overlay_*.go   search, palette, help, context menu, AirPlay picker, last.fm setup, which-key
```

## Root model

`App` owns:

- `deps` (bridge client, player, lyrics service, scrobbler, config, art renderer,
  keymap engine, theme);
- `lib *library.Library` and `idx *search.Index`, swapped atomically when a
  snapshot arrives (`libraryMsg`); views get `Reindex(lib)` to re-find their items
  by PID/key and keep the cursor;
- `layout`: rectangles recomputed on `tea.WindowSizeMsg` and when panes toggle;
- `focus`: sidebar, content or right pane;
- `nav`: a stack of views with back/forward history (`ctrl+o`/`ctrl+i`); `esc`
  leaves a detail page but never moves between library sections;
- `right`: none, lyrics or queue;
- `overlays`: a stack; the top one gets keys and mouse first;
- `hover`: the region and row under the pointer;
- `status`: one transient line, shown in the footer for 3 s or until the next
  key or click.

Message routing in `Update`:

1. `tea.KeyPressMsg` → if the top overlay captures input, it gets the key first.
   Otherwise `keymap.Feed(context, token)`; a resolved action goes to the overlay,
   then the focused component, then the app (global actions). Pending sequences
   start a 1 s `keyTimeoutMsg` and show the which-key popup. Popups (help,
   palette, search, menus, pickers) are modal: an action they don't handle stops
   there, and no which-key opens over them. Overlays with a key context of their
   own (the drawers, full-screen lyrics) are panes or modes and pass what they
   leave to the app.
2. Mouse messages → the top overlay if the point is inside it (outside click
   closes it); otherwise the region under the point by `layout.Hit(x, y)`, with
   coordinates translated to the region. Wheel goes to the region under the
   pointer, not the focused one. Clicking a region focuses it. When a press
   changes the view or opens or closes an overlay, the app drops the second
   press of that double-click. Views that play on a double-click also keep a
   `pressGuard` (albumgrid.go), so a double-click only counts when the view
   got both presses on the same local cell.
3. `playerMsg` (from `player.Changes()`), `tickMsg` (8 Hz while playing, for the
   progress bar and lyrics; 30 Hz while a word-timed lyric line fills, and it
   keeps going while the terminal is in the background if lyrics are on
   screen), `lyricsMsg`, `artMsg`, `searchMsg`, `snapshotMsg`,
   `airplayMsg`, `lastfmMsg`.

## View interface

```go
type View interface {
	Key() string                          // identity for history ("album:<key>")
	Title() (title, meta string)          // "Songs", "3,158 songs · 9 days"
	Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd)
	Mouse(ev MouseEvent, c *Ctx) tea.Cmd  // local coordinates
	Render(w, h int, c *Ctx) string       // exactly w×h
	Selected() []*library.Track           // targets for play/queue/menu actions
	Reindex(lib *library.Library) View
}
```

`MouseEvent` normalises Bubble Tea's messages: `Kind` (press, release, motion,
wheel), `Button`, `X`, `Y` (local), `Double` (two presses within 350 ms on the
same cell), `Mod`.

`Ctx` exposes what views need without reaching into the app: `Theme`, `Lib`,
`Player` snapshot, `Focused bool`, `Playing *library.Track`, and helpers that
return `tea.Cmd`s: `PlayTracks(src, tracks, i)`, `Open(View)`, `OpenAlbum(a)`,
`OpenArtist(a)`, `OpenPlaylist(p)`, `Menu(x, y, tracks)`, `Status(msg)`.

## Layout

- Player bar: rows 0–2, rule on row 3 (the rule is not clickable). Under 20
  rows or 60 cols the bar collapses to one line. When the LCD would get fewer
  than 40 cols the bar drops groups in this order: AirPlay name, volume number,
  shuffle and repeat, volume meter (one volume glyph stays), lyrics and Up Next
  toggles, remaining time. Under 60 cols it is `⏯  Title · Artist  1:08`, under
  40 the play button and the title. The title never drops; an artist or album
  that would be cut under 8 cells drops instead. The AirPlay name shows only
  when something other than this computer plays. The LCD is centred
  between the left and right groups, at most 90 cols wide.
- Footer: last row; hidden under 24 rows. Left: the hovered control's name and
  key (`Ctx.Hint`), else the status. The hint clears on the next motion
  event and whenever the view changes or an overlay opens or closes. Right:
  up to five hints; a view or a right pane can replace the default ones with
  `FooterHints(*Ctx) []FooterHint`. No hints while an overlay is open, unless
  the overlay has `FooterHints` too (the Up Next drawer). Under 24 rows the
  left slot moves to the right end of the rule under the bar.
- Middle: sidebar (22 cols, then a `│` rule) | content | optional rule + right
  pane (40 cols; 48 when the window is ≥ 160 cols). The sidebar folds to a
  3-col rail of icons (Search, the Library sections, Playlists) under 90 cols
  or with `space b`; Playlists on the rail opens the full sidebar as a drawer
  over the content. With a right pane open the content keeps 70 cols: the
  sidebar folds to the rail first, then the pane narrows to 36. The sidebar
  never narrows otherwise. The right pane hides under 100 cols; there the
  lyrics toggle opens full-screen lyrics and the Up Next toggle opens the
  pane as a drawer over the right of the content (`queueDrawer`). It becomes
  the pane again when the window widens.
- Detail pages (album, playlist, folder, artist) share one header,
  `pageHeader` in `pageheader.go`: art sized by `heroSize`, the text column
  beside it, Play / Shuffle at the foot of the art, one line without art. It
  starts at column 3, so the art lines up with the titles of the list under
  it; `Ctx.BackMark` sits at column 0 of its first row when there is history
  to go back to (the cell either side counts too); clicking it goes back.
  Mouse buttons 4 and 5 go back and forward.
- Every view's content ends at `contentRight(w)` (two cells in from the
  right: the track table's scrollbar and its margin), title-line controls
  included. Album track lists stop at 108 cells (`detailMaxW`).
- Overlays are composited with `lipgloss.NewCompositor` layers over the base frame.

## Apple Music pages

`catalog.go` and `views_catalog.go`. `Services.Catalog` (`Ctx.Catalog`) is the
catalog: `*catalog.Client` behind a lazy wrapper in real mode, an offline
fixture in `--demo`, nil to leave the feature out. Catalog songs travel through
the UI as stand-in tracks (`player.StandIn`, pid `store:<id>`); the host's
Play, PlayNext and AddToQueue send them to the player's catalog calls, menus
for them drop Favorite, and `catalogItemOf` maps a stand-in back to its item.
`CatalogAlbumView` (key `catalog-album:<id>`) embeds `AlbumView` without the
heart column; `CatalogArtistView` (`catalog-artist:<id>`) wraps `artistPage`
with Apple's top songs. A view with `Init(*Ctx) tea.Cmd` is initialised by
`Open` and when history brings it back; it loads in a Cmd whose result comes
back as `catalogResultMsg` and runs on the UI goroutine, and says "Loading…"
only after `catalogQuiet` (300 ms). Album grids treat keys `catalog:<id>`
(no tracks yet) by loading the songs first. The search overlay's Apple Music
scope debounces 350 ms, cancels the request in flight when the text changes,
and never queries from the All scope.

Items the library already has (`inlibrary.go`, rules in
`library/match.go`; `docs/design.md`, "Apple Music items in the library"):
`Ctx.CatalogSongInLibrary`, `CatalogAlbumInLibrary`, `CatalogPlaylistInLibrary`
and the wrappers `StandInInLibrary` (a stand-in track; a library track is never
marked), `CatalogAlbumTileInLibrary` (a `catalog:<id>` album) and
`HomeItemInLibrary` (Home album, song and playlist; stations and artists
never). Rows draw every frame, so the answers are memoized per `*library.Library`
in `marks` (UI goroutine only; a new library resets it). The glyph is
`Glyphs.InLib`, drawn in dim, in these places: `TableOptions.LibMark` (the
artist page's top songs) and `AlbumView.libMark` (`CatalogAlbumView`) use the
favorite column's slot, `libCell` (those tables have no heart column), with
`rowLib` in the row cache key; `tileTitle` and `titleMark` put it in the last
cell of an album tile's title (grid, and the title column in list mode) and
of a Home tile's, taking two cells from the title and nothing under 8 cells
of room; `searchMark` puts it at the left edge of the Apple Music search
row's time column, and a playing row shows `▶` instead. Hovering the check
of a song says "In Your Library". None of this changes what enter, the menus
or the buttons do.

Open in (`openin.go`): `space o a l` / `space o a r` (album / artist in
Apple Music) and `space o l l` / `space o l r` (in the library) act on the
focused selection (`subjecter`, else `Selected`); `space o a` and
`space o l` are pure prefixes. Menus carry the same items (`openInItems`,
after the Go to group) and drop them in `MenuOverlay.Init` when they can't
apply. A library item's catalog page is found with one `SearchKinds`
request of one kind, picked by folded title, lead artist, year, track count
or length, and kept in `catalogMatches` (misses too, errors not). The
artist of a song or album is its lead artist (`leadArtist` drops "feat."
guests); a song or album already matched gives its artist id without a
request (`knownCatalogArtist`), else the artist is searched by name under
the same `artist:<name>` key a library artist uses. A catalog item's
library page is found in memory (`libraryTarget`). A song opens its album
page with the song selected (`AlbumView.want`).

Open in Music.app (`musicopen.go`): `M` and `space o m` (`keymap.MusicOpen`)
show the focused selection in Music.app through `Host.OpenInMusic`
(`App.OpenInMusic`, which calls `Services.OpenMusic`) and say "Opening in
Music.app". Neither Apple Events nor MediaRemote can add an item to the
library from outside Music, so it is Music's own + that does it; encore
offers the link whether or not the library has the item. A `musicPage`
(kind, id, name) is built for what has a music:// link: a catalog song
(`musicPageOf`; on its album's page, "<album id>?i=<song id>", when the
song knows its album), a catalog album, and a Home album, song or
playlist (`homeMusicPage`). Library items, artists and stations have
none. Menus carry the item (`musicMenuItem`, `musicMenuItems`): catalog
songs and albums wherever they have a menu, a Home playlist's menu
(`homePlaylistMenu`, where a playlist that only opens in Music has this
as its one item), and the playing song's menu (`App.NowPlayingMenu`,
opened by right-clicking the player bar or Up Next's playing row). A
catalog album page's header ends with it (`musicTail`), and Home's footer
hints it where enter does something else. `M` reaches
`App.openSelectionInMusic` through the focused view's subject (Home
handles it per tile).

## Home

`views_home.go` and `home_play.go`. `Services.Home` (`Ctx.Home`) is a
`HomeSource`: `*home.Reader` in real mode, a fixture built from the demo
catalog in `--demo` (`cmd/encore/demohome.go`). `main` starts a `Load`
before the bridge and waits up to 300 ms for it before `ui.New`;
`Options.Start` (config `start`) picks the start view, Recently Added by default; Home needs `Options.HomePage` with shelves, else Recently Added. `HomeView` keeps
each shelf's selected item and scroll (`homeShelf.cur`, `.off`) and the
page's first block (`top`: a shelf or the "New" heading). j/k keep the
column on screen, h/l at a row's end return false so focus moves on, as
in the album grid. The wheel scrolls one shelf per notch; shift+wheel and
the horizontal wheel scroll the row under the pointer. A double-click is
one click (`pressGuard`), since a single click already plays or opens.

`Init` loads the page when it has none and starts a 10 s poll. `Render`
sets `shown`; a poll tick that finds it unset stops, and `Init` (on
`Open` and history) starts it again. Each tick calls `Changed` and `Load`
off the UI goroutine, and loads anyway every sixth tick so live shows stay
current. A reload keeps the selection and `top` by shelf id and item id.

A tile's title line carries the check when the library has the item
(`HomeItemInLibrary`); a Home playlist is in the library when a library
playlist has its `pl.…` id as `GlobalID`.

Activation (`homeActivate`): a station plays through `homeHost.PlayStation`
(the app's `PlayStation`, `player.PlayStation`), a song plays with its
shelf's songs as a catalog context (looked up with `LookupSongs` when the
catalog has it, for albums and lengths), an album opens its catalog page
(`catalogAlbum`, key `catalog:<id>`, so its art and page are the catalog's)
and its badge plays it through `catalogAlbumAction`, an artist opens its
catalog page and its badge plays `player.ArtistStationID`. A playlist with
cached song ids plays them; any other opens in Music through
`Host.OpenInMusic` and `Services.OpenMusic` (`/usr/bin/open
music://music.apple.com/<cc>/playlist/<id>` in real mode). Those tiles
have no play badge; hovering one says "Opens in Music.app".

## Album art

`art.Renderer` decides the protocol. Views ask for art through `Ctx` with a key
(album key or track pid) and a cell size; the app loads it through
`bridge.Client.Artwork` in a Cmd, caches the PNG bytes, and re-renders on `artMsg`.
A pid with `://` is a URL (Apple Music artwork): `art_url.go` fetches it
over HTTP at the size needed (`Options.ArtFetch` replaces that in demo mode)
and keeps `catalog-<id>-<px>.jpg` in `Options.ArtCacheDir`; the queue, LRU
and frame order are the same.
The player bar and page headers use the best available protocol (`Ctx.HeroArt`);
the player bar and full-screen lyrics go through `Ctx.NowPlayingArt`, which keeps
the last track's art in the block while the new track's art loads, for at most
`nowArtHold` (4 s) after the track changed, then shows the placeholder. Music
reports no artwork for a station or catalog song until it has fetched it, which
takes seconds, so hero art asks again 1 s after a "none" answer, doubling up to
30 s, for as long as it is on screen; a failed load is asked again after 3 s.
Each such answer schedules a redraw for when the retry is due, so it happens
even when nothing else redraws. Tiles take the first answer.
`ENCORE_DEBUG_ART=1` logs every load (queued, pruned, started, done, none,
failed) and what the now-playing art shows and why to
`~/Library/Logs/encore/art.log`.
Grid tiles (`Ctx.TileArt`) are images too on iTerm2-protocol terminals, fetched
at 256 px and at most 64 per frame (the renderer keeps 96 decoded images). Inside
tmux on WezTerm and iTerm2 every image goes through passthrough as an iTerm2
image; with `art = "sixel"` each tile row is one sixel image (`Ctx.TileRow`). Elsewhere, while a tile loads, and while the grid is scrolling fast (a
scroll step within 150 ms of the last one) they are half blocks; images come back
150 ms after scrolling stops. A tile cut off at the bottom of its grid is cropped
with covers just above its layer, and the hover play badge covers its cells with
`Ctx.CoverArt`. Overlay rectangles are recorded before the frame under them is
drawn: a tile an overlay leaves less than half of in view is blank rather than a
sliver of art, and an overlay against one side of the screen (a drawer) keeps a
blank column between itself and the frame. See `internal/art` docs for how IIP images are placed after a
frame.

## Names

`keymap.Name(action)` is the one name an action goes by in help, the palette,
the which-key popup, menus and hover hints: Title Case with short words in
lower case ("Go to Playing Album", "Add to Up Next"). Under a prefix that already
says it, which-key shows the action's `Short` ("Albums" under "Search…"). Footer
hints use the same words in lower case ("a add to Up Next"). Header buttons and
badges name their target: "Play Album", "Shuffle Artist", "Play Playlist".

## Testing

- `go test ./internal/ui/...`: components render to the exact size, never panic
  on empty data, and handle actions/mouse correctly (drive them directly).
- `make demo` + `tools/snap` scripts in `tools/snap/scripts/` for screenshots.
  `ENCORE_DEMO_IDLE=1 encore --demo` starts with nothing queued, so the bar
  reads "Not Playing".

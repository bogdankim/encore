# Research notes

Findings that shaped the design. Measured on macOS 27.0, Music 1.7, arm64, against
a 4,830-item library (3,158 in `library playlist 1`, the rest only inside
subscription playlists). Verified code lives in `code/`.

## Framework

- Go + Bubble Tea v2 (`charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`,
  `charm.land/bubbles/v2`, installed as a matched set, v2.1.x at time of writing).
- `View()` returns `tea.View`; alt screen, mouse mode (`tea.MouseModeAllMotion` for
  hover), focus reporting and window title are fields on it.
- Mouse: `tea.MouseClickMsg`, `MouseReleaseMsg`, `MouseWheelMsg`, `MouseMotionMsg`;
  `msg.Mouse()` gives `X, Y, Button, Mod`. Zero-based coords.
- Keys: `tea.KeyPressMsg`, match on `msg.String()`. No sequence support built in.
- WezTerm only sends kitty-keyboard disambiguation if the user enables it, so default
  bindings must not depend on ctrl+h vs backspace etc.
- Don't use `bubbles/list` or `bubbles/table` for big lists. Render only visible rows,
  cache row strings, do I/O in `tea.Cmd`s only.

## Design references

- Claude Code: one warm accent, normal fg for content, dim grey for everything
  secondary, rules instead of boxes, one status line, contextual dim hints.
- Hermes TUI: modal overlays for pickers, slash palette with descriptions, status
  rule that never jitters, light-terminal detection (OSC 11).
- herdr (Rust/Ratatui): mouse first-class (click focus, drag resize, right-click
  menus), sidebar with coloured state dots, "adds semantics instead of chrome".
- spotify-player / rmpc: multi-key sequences, `?` searchable help, action popup on
  the selected item, synced lyrics page, album art with half-block fallback.

### Slop checklist (do / don't)

Do: one accent with meaning (playing, focus), two text tones plus one muted
selection surface, whitespace and single rules between regions, strict column
alignment (right-aligned durations and numbers, `…` truncation by display width),
a status line that never changes width, contextual hints only, one-sentence empty
states and errors, light and dark backgrounds, `NO_COLOR`.

Don't: boxes around every pane, nested boxes, outer border, emoji as icons,
gradients, neon, redundant headers ("Now Playing" above the now playing bar),
labels that repeat the column header, welcome banners, toasts for trivial actions,
pulsing dots, marquees, mixed border styles, more than ~5 colours on screen.

## Album art

- WezTerm: kitty graphics support is partial (no unicode placeholders). Use the
  iTerm2 inline image protocol (IIP) with `doNotMoveCursor=1`, drawn over a
  reserved blank rectangle via `tea.Raw` with save/restore cursor. Needs a spike to
  confirm the Bubble Tea v2 renderer does not wipe the image cells.
- Kitty/Ghostty: kitty unicode placeholders (image becomes text, diff-safe).
- Fallback: half blocks (`▀`, fg = top pixel, bg = bottom pixel, truecolor).
- WezTerm bundles Nerd Font symbols in its fallback chain; other terminals often don't.

## Music.app control

- Every Apple Event costs ~8.3 ms; cost scales with the number of AEs, not the API.
  Raw `NSAppleEventDescriptor.sendEvent` from one serial queue. `osascript` is 200 ms.
- `properties of current track` returns 62 keys in one AE. Never ask for
  `properties` of the application (510 ms).
- Persistent IDs: ITLibrary `persistentID` (UInt64) ↔ AppleScript hex
  `String(format: "%016llX", v)`. The playerInfo notification sends it as a signed
  Int64 (`UInt64(bitPattern:)`).
- `whose persistent ID is X` is one AE (8 ms). Tracks that only exist inside a
  subscription playlist are not in `library playlist 1`; find them inside their
  playlist.
- `play track N of playlist X` plays one track only and then stops. `play playlist X`
  gives a real context (next/previous work, media keys follow it).
- Music snapshots a playlist at `play` time; later edits to it don't affect playback.
  So one reusable queue playlist works: clear it (8 ms), duplicate tracks with
  no-reply events plus one barrier (12 tracks ≈ 30 ms, 200 ≈ 0.9 s), `play` it.
  Audio starts ≈ 0.9 s after `play`.
- Shuffle: keep Music's shuffle off for queue playback and shuffle on our side.
- Reads right after a set can return the old value for up to ~1 s; trust our write.
- Dead cloud tracks (`cloud status` "no longer available"; ITLibrary private KVC
  `cloudType == 9`) accept `play` and silently stop.
- Some playback sessions start with Music's scripting layer believing it is paused
  while it plays (log: `Applying debounced playing state: false` right after
  `StartPlaying`). In such a session `pause` is dropped without a trace,
  `playpause` becomes `play`, and resending doesn't help, while `player state`
  still reads `playing`. Seen with a HomePod selected but unavailable: 2 of 4
  starts at first, then every start for the rest of the day; the trigger is
  unknown. `stop` and `play` still work, and so does pause through
  MediaRemote: `MRMediaRemoteSendCommandToApp(1, nil, nil, "com.apple.Music", 0, q, cb)`
  reaches the playback engine directly. The bridge sends pause and play as Apple
  Events, reads the state back at 400 ms and 1.2 s, and resends through MediaRemote
  when Music hasn't moved. Watch it with
  `log show --predicate 'process == "Music" AND category == "ampplay"'`.
- `com.apple.Music.playerInfo` distributed notification still fires (play, pause,
  next, prev, stop, shuffle/repeat changes) ~250–450 ms after the change. Payload has
  no position. Not sent on seek, volume, mute, favorite.
- Position: float with ms precision, `missing value` when stopped. Seek with
  `set player position`.
- Favorite is `favorited` (`pLov`), dislike `disliked` (`pHat`). Always send literal
  values in sets.
- AirPlay: `AirPlay devices` (id, name, kind, active, available, selected, sound
  volume). Select with `set current AirPlay devices to {AirPlay device id N, ...}` or
  `set selected of AirPlay device id N to true`. First AirPlay query in a process is
  ~570 ms, later ones 8 ms; warm it up at start.
- Artwork: ITLibrary `item.artwork?.imageData` is 0.3–0.5 ms per item (600 px JPEG),
  present for cloud items too. Now playing via AE `raw data of artwork 1 of current
  track` 5–11 ms.
- Music's `lyrics` property is empty for subscription tracks. Synced Apple lyrics
  are not scriptable.
- Music's scripting dictionary: `sdef /System/Applications/Music.app`.
- Music not running: raw AE fails with -600. Launch with `NSWorkspace` (no
  activation) and retry.

## Catalog playback

- MediaRemote's system app playback queue makes Music stream catalog songs by
  store ID (the iTunes Search API `trackId`), in the library or not:
  `MRSystemAppPlaybackQueueCreate(kCFAllocatorDefault, 3)` (3 is
  "iTunesStoreTracklist"), `MRSystemAppPlaybackQueueSetTracklistStoreIDs(q,
  ids)`, `MRSystemAppPlaybackQueueSetIsRequestingImmediatePlayback(q, true)`,
  `MRMediaRemoteSetSystemAppPlaybackQueue(q, queue, done)`, then
  `MRSystemAppPlaybackQueueDestroy(q)` in `done`. Code in
  `bridge/Sources/EncoreBridge/StoreQueue.swift`.
- `done` gets a `UInt32` status (0 on success; else a send-command error, named
  by `MRMediaRemoteCopySendCommandErrorDescription`: 1 ApplicationNotFound,
  3 CommandIgnored, 5 TimedOut, …). Its second argument is not a CFArray:
  declaring it as one crashed the bridge when Swift retained it.
- For type 3 the queue goes to the system media application (Music), not to
  the now-playing app (`MRMediaRemoteSetSystemAppPlaybackQueueWithContext`
  passes `kMRMediaRemoteSystemMediaApplicationDisplayIdentifier`). An
  unentitled process can't ask who is now playing:
  `MRMediaRemoteGetNowPlayingApplicationDisplayID` returns nil and
  `MRMediaRemoteGetNowPlayingInfo` nil.
- There is no start index for a tracklist (the `FirstItem` setters are for
  generic track identifiers), so the bridge sends `ids[start:]`.
- Measured: the call returns after about 1.6 s, audio starts at about the same
  time, from paused and from stopped. Music's `next track` and `previous track`
  walk the tracklist.
- Music reports the song through Apple Events like any other: class
  `URL track`, name, artist, album, duration (456.0 against the API's 456.853),
  composer, genre; album artist empty, track number 0, favorited false. Its
  persistent ID is not in `library playlist 1`. The same
  song played again kept its persistent ID within the session, but nothing
  says it is stable, and `properties` has no store ID.
- Not measured yet: what Music does at the end of a store tracklist (stop or
  Autoplay), behaviour with another app now playing, and an account without
  an Apple Music subscription.

## Web pages

Checked 2026-10-28 with curl, storefront ca, no cookies or token.

- Every music.apple.com page embeds its data as JSON in
  `<script type="application/json" id="serialized-server-data">`:
  `data[0].data.sections[]`, each with `id`, `itemKind` and `items`. A plain
  browser User-Agent gets it. Code in `internal/catalog/web.go`.
- Artist page (`/<sf>/artist/<id>`, 0.4–1 MB): section ids carry a marker that
  isn't localized, `artists_<id>_default.marker.<Name>-…`: `TopSongs` (rows of
  its own, or under a featured playlist in
  `shelfgroup.FeaturedContent.TopSongs`, 24 rows), `Essentials` (Essential
  Albums; each item wraps a `squareLockup`), `Albums`, `ArtistSingles`,
  `LiveAlbums`, `CompilationAlbums`, `AppearsOn`, `ArtistPlaylists` (the first
  is "<Artist> Essentials" when Apple has one), `MusicVideos`,
  `SimilarArtists`, `MoreToHear`, `AllUpcomingConcerts`. Shelves stop at 12
  items; "see all" goes to `/v1/editorial/…`, which needs a token, so the full
  album list stays with the Search API. Album tiles: `contentDescriptor`
  (kind, storeAdamID, url), `titleLinks[0].title`, `subtitleLinks[0].title`
  (the year on their own shelves, a blurb on Essential Albums, the album's
  artist on Appears On), artwork templates like `{w}x{h}bb.{f}`. Song rows:
  `contentDescriptor` (the song id; its URL has the album id), `title`,
  `duration` (ms), `showExplicitBadge`, subtitle "Album · 2017".
- Low Roar's page has no Compilations or Appears On shelf, though they have 18
  songs on Death Stranding (Songs from the Video Game). The Search API's song
  lookup (`entity=song&limit=200`) has them with `collectionArtistName:
  "Various Artists"`, which the album lookup leaves out; own albums have no
  `collectionArtistName`. The album lookup does list others' albums the
  artist is credited on (Thom Yorke's "Creep (Very 2021 Rmx) [feat.
  Radiohead]" under Radiohead, `artistId` Thom Yorke's).
- Playlist page (`/<sf>/playlist/<pl.id>`): a `trackLockup` section with every
  song of an Essentials playlist (35 of 35 for Radiohead), artist and album
  links included.
- Search page (`/<sf>/search?term=`): sections `top-search-section - top`,
  `… - artist`, `… - album`, `… - song`, `… - playlist`, `… - station`,
  `… - radio_show`, `… - radio_episode`, 21 items at most. "nts" lists NTS
  Radio 1 (`ra.1461123917`), NTS Radio 2 (`ra.1461608125`), Memory Lane, Slow
  Focus, The Tube, Poolside and more. A station page says
  `radioStation: {isLiveRadio: true, isBroadcastRadio: true}` for these.
- NTS Radio 1 plays through the station queue below (live test 2026-10-28,
  speakers muted): status 0 after 4.7 s through the bridge, then playing.
  Music reports a `URL track` named "NTS Radio 1" with an empty artist,
  duration 0 and `current stream title` missing value, so the show on air
  isn't known.

## Adding to the library

- Works (verified live 2026-10-21, on the playing station song): `duplicate
  <track> to source 1`, the library source, adds a track Music has to the
  library. The library count went up by one. The command returns no
  result; the new track shows up in `library playlist 1`, so the bridge
  confirms by looking for a library track with the same name and artist
  (every 250 ms, up to 5 s). Code in
  `bridge/Sources/EncoreBridge/LibraryAddHandlers.swift` (`library.addTrack`).
- `duplicate <track> to library playlist 1` fails with -10006, "Can only
  duplicate subscription tracks to library source".
- Only tracks Music has can be duplicated: the current track (a catalog or
  station song, class `URL track`, under its temporary persistent ID) and
  tracks in playlists. An album, a playlist or a catalog song that isn't
  playing has no track object, so encore opens it in Music
  (`music://music.apple.com/<cc>/album/<id>`, `…/album/<id>?i=<song id>` for a
  song, `…/playlist/<id>`) and the user presses + there. Driving that button
  through Accessibility is a possible later step.
- Not measured yet: duplicating a playlist-only song that isn't playing (found
  through its playlist, as `track.setFavorite` finds it), what Music does
  with Sync Library off, and how long Music takes to write the new track to
  its database, which is when ITLibrary sees it (the bridge emits `library`
  at once and checks again after 10 s).
- Not tried yet: whether `music://` links of the forms `album/<id>?i=<song
  id>` and `song/<id>` open the page in Music.app (playlist links do).
- Doesn't work: MediaRemote command 128 (AddItemToLibrary). Music's own
  disassembly has a handler for it (options `ModifyLibraryCommand` =
  `AddItems`, `OpaqueProductID` = catalog id, optional `PlaylistPersistentID`;
  status 4106 without Sync Library; then the
  `com.apple.Music.addedMediaPlayerItems` notification with `CommandID` and
  `AddedItems`), but mediaremoted refuses to deliver it from another process:
  "Undeliverable command [never supported] AddItemToLibrary", reply `[404]`,
  for both the `OpaqueProductID` form and `kMRMediaRemoteOptionTrackID`.
  `-[MPMediaLibrary addItemWithProductID:completionHandler:]` fails with
  MPErrorDomain 5. Both live tests 2026-10-21.
- Doesn't work, and can't from encore: the one API that adds a whole album
  or subscribes to a catalog playlist is AMPLibraryAgent's
  `-synchronousAddToCloudMusicLibrary:withReply:` (request ivars `command`,
  `pids`, `itemIdentifiers`, `cloudPlaylistID`, `curatorAdamID`, …) on the
  mach service `com.apple.amp.library`. That connection requires the
  restricted entitlement `com.apple.amp.library.client` (held by Music.app);
  an ad-hoc signed helper that declares it is killed by AMFI at launch (exit
  137). The service encore's TCC grant reaches, `com.apple.amp.library.framework`
  (ITLibrary's `AMPLFrameworkClient`), is read-only; iTunesLibrary's
  `ITLibraryModifyLibrary*` keys are only used by the agent's clients.
  Checked 2026-10-21.
- Doesn't work: inserting catalog songs at the end of Up Next and duplicating
  them from there. `MRMediaRemoteInsertSystemAppPlaybackQueue(q, 1 /* tail */,
  nil, dq, done)` with a type 3 queue (immediate playback off) returns 0 in
  0.27 s, but Music's Apple Events have no Up Next: no new source, playlist
  or track object appears (sources and playlists diffed before and after), and
  `current playlist` is unreadable while a station plays. Only the current
  track can be duplicated, so the songs would have to be played. Music
  advertised only commands 0, 122, 132 and 133 to MediaRemote at the time, so
  the queue can't be read or edited through MediaRemote either. Live test
  2026-10-21 with a catalog EP; library unchanged.

## Station playback

- The same system app playback queue plays Apple Music radio stations, with
  a queue of type 2 ("RadioStation"):
  `MRSystemAppPlaybackQueueCreate(kCFAllocatorDefault, 2)`,
  `MRSystemAppPlaybackQueueSetRadioStationIDType(q, 2)` (2 is a string
  identifier, 1 an integer PID; the setters trap when the type doesn't match),
  `MRSystemAppPlaybackQueueSetRadioStationStringIdentifier(q, "ra.…")`, immediate
  playback, `MRMediaRemoteSetSystemAppPlaybackQueue`. Code in
  `bridge/Sources/EncoreBridge/StationQueue.swift`.
- Verified 2026-10-20 (speakers muted): a personal station `ra.u-…` (status 0
  after about 2 s, then playing), an artist station `ra.<artistId>` (the
  artist and similar artists, about 1 s; 3.2 s through the bridge another
  time, playing as the call returned), and live Apple Music 1 `ra.978194965`.
- Any catalog artist id makes a station: `ra.<artistId>` is the artist and
  similar artists, `ra.a-<artistId>` the artist only.
- Station songs report like catalog songs: class `URL track`, name, artist,
  duration (208 as an integer for one song), temporary persistent IDs not in
  the library, a new one after each `next track`. `next track` moves to
  another song of the station.
- Live radio (Apple Music 1) plays with the position staying 0.0. The current
  track's name and artist are reported, its class is `URL track`, and
  `current stream title` is missing value.
- Playlists don't play this way: a playlist id (`pl.pm-…`, "Your Essentials")
  in a type 3 store tracklist fails with ApplicationNotFound, and a type 5
  generic tracklist with its `music.apple.com` URL returns status 0 and plays
  nothing.
- Not measured yet: whether `previous track` can go back within a station,
  and what Music reports between the songs of a live show.

## Lyrics

- LRCLIB: `GET /api/get` (track_name, artist_name, album_name, duration) and
  `/api/search`. `/api/get-cached` is gone. Always send duration (±2 s window).
  User-Agent required. 429/503 come with `Retry-After`; honour it with jitter.
- Chain: get(raw) → get(clean title, primary artist, no album) → search + score
  (synced +4, title match +2, album match +1, ±3 s) → best plain → Music `lyrics`.
- Timing (Oct 2026, about 160 live lookups): a hit LRCLIB has ready answers in
  ~150 ms; 404s, slow hits and 503 ServerOverloaded all take ~1.15 s, and a 503
  asks for `Retry-After: 1`. A signature that got a 503 often gets one again on
  retry. With the sequential chain and 3 attempts per request, p50 was
  150 ms–2.5 s depending on load, p90 3.8–6.8 s, the worst 11 s.
- So the lookups now overlap (`internal/lyrics/provider.go`, `Find`): the cleaned
  get goes out 250 ms after the raw one (or as soon as it misses; not while
  LRCLIB has been overloaded in the last 30 s), search only after both gets
  miss, Music's `lyrics` after 300 ms (it runs on the bridge's Music queue).
  Plain lyrics show while LRCLIB keeps looking; a 429/503 holds every request
  until its Retry-After.
- LRC parser spec and code in `code/lyrics-lastfm/go/lrc`. LRCLIB's API docs
  are at <https://lrclib.net/docs>.

### Apple Music lyrics

Measured 2026-10-20 on macOS 27, reading only the lyrics rows. encore reads
Music's cache for this and never touches its sign-in data.

- Music caches its amp-api responses in `~/Library/Caches/com.apple.Music/Cache.db`
  (NSURLCache SQLite) and `fsCachedData/`. The same database stores request and
  response objects with the authorization headers and the Music user token
  (`cfurl_cache_blob_data`); encore never reads that table or any headers.
- Lyrics come with `GET /v1/catalog/<sf>/songs?filter[equivalents]=<id>&…&extend[syllable-lyrics]=ttmlLocalizations&include=syllable-lyrics`
  (sometimes `/songs/<id>?…`). JSON:API: `data[0].attributes` has name,
  artistName, albumName, durationInMillis, playParams.id, hasLyrics,
  hasTimeSyncedLyrics; `relationships.syllable-lyrics.data[0].attributes.ttmlLocalizations`
  is the TTML (empty `data` when the song has none). No separate `include=lyrics`
  requests showed up. Bodies up to ~4 KB sit in `cfurl_cache_receiver_data`,
  larger ones in `fsCachedData/<uuid>` (`isDataOnFS = 1`, the row holds the name).
- TTML: `itunes:timing` is `Word` (each `<p>` has `<span begin end>` per syllable,
  spaces between spans as text) or `Line` (text in `<p>`). `<div>` per stanza
  with `itunes:songPart`; times like `23.891` or `1:02.345`; head metadata has
  songwriters and `<translations/>`.
- When: Music fetches a song's lyrics within about a second of the song starting
  (watched over 4 track changes; the row's `time_stamp` matches the start), for
  songs without lyrics too, so it looks like the check behind the lyrics button
  rather than the lyrics view. Whether Music's lyrics view was open wasn't
  checked; encore doesn't touch Music's UI.
- The newest rows are in the WAL: open read-only without `immutable=1`, which
  shows the last checkpoint (stale rows, none of the new ones).
- Rows don't last: 33 lyrics rows from 10:15–11:08 (local time, in the last
  checkpoint) were all gone by 12:00, while rows from 11:26 on were still there
  at 12:39. Not a fixed age, so probably NSURLCache trimming for size. encore
  copies Apple's lyrics into its own cache.
- Reading: two stats per lookup when nothing changed (~0.1 ms), ~7 ms to index 10
  responses after a change. System libsqlite3 through cgo (the bridge already
  needs cgo and Xcode), no new Go module.
- Cache in `~/Library/Caches/encore/lyrics/`; synced kept forever, plain retried
  after 7 days, 404 after 24 h then 7 days, transient errors only in memory.

### Apple Music Home page

Measured 2026-10-20 on macOS 27. encore reads Music's cache for the Home page
too, with the same limits. Code in `internal/home`, the shared reader in
`internal/musiccache`.

- Music fetches two responses when it shows Home, both with
  `format[resources]=map` (`data` holds `{id,type}` references, `resources`
  holds every object by type and id):
  `GET amp-api.music.apple.com/v1/me/recommendations?name=listen-now&…` (about
  700 KB, in `fsCachedData`) and
  `GET amp-api-edge.music.apple.com/v1/editorial/<sf>/groupings?name=music&tabs=subscriber&…`
  (the New tab). The listen-now URL carries a location geohash, so encore never
  logs or stores request URLs.
- listen-now: `data` lists `personal-recommendation` shelves in page order.
  Each has `attributes.kind` (`music-recommendations`, `recently-played`,
  `your-favorites`, `concerts-for-you`, `recommended-friends`),
  `display.kind` (`MusicCoverShelf`, `MusicNotesHeroShelf`, an untitled
  `MusicSuperHeroShelf`, `MusicSocialCardShelf`), `title.stringForDisplay`,
  `resourceTypes`, `meta.metrics.moduleType`, and `relationships.contents`.
  "Friends Are Listening To" is a plain `music-recommendations` shelf with
  module type 11 (its id starts `11-`), so encore drops it by module type.
- Items: albums (`playParams.id`, artistName, `contentRating: explicit`),
  playlists (`playlistType` editorial, personal-mix, replay or user-shared;
  curatorName), stations (`kind` artistSeeded, artistOnly, query, discovery,
  user; `playParams.kind: radioStation`, `format: tracks`). Recently Played also
  holds `library-albums`/`library-playlists` with only an id and
  `meta.catalogIds`, and Replay has an `editorial-items` card. encore skips
  those.
- Artwork `url` is a template: `{w}x{h}bb.{f}`, `{w}x{h}{c}.{f}`,
  `{w}x{h}SC.DN02.{f}?l=en-CA`, `{w}x{h}AM.RSMA04.{f}?imgLeft=…`. encore fills
  `{c}` with `bb` and `{f}` with `jpg`.
- groupings: `groupings/700` → `tabs` → editorial element 382 → `children`.
  Element kinds seen: 316 hero (children of kind 317/320, one item each with a
  `designBadge`), 326 shelf, 327 song list, 322 and 391 links. Names are
  localized, so encore picks by kind and contents: the first 327 (Best New
  Songs), the first all-album 326 (New This Week), the first all-playlist 326
  (Updated Playlists), and the 326 of live stations (Listen to Live Radio Now).
- Live stations have `isLive: true`, `kind: streaming`, `playParams.format:
  stream` and an `events` relationship to `station-events` with title,
  startTime and endTime (12 or more per station, one to two hours each; encore keeps 12). The
  show on air is the event with start ≤ now < end. Episode stations (New Radio
  Episodes) have `isLive: false` and an `airTime`.
- What the cache held: 19 listen-now shelves kept (Friends, Concerts and
  Your Friends Are Following dropped), plus Live Radio with 6 stations, and 3
  New shelves of 20 items. Loading takes about 24 ms after a change and 20 µs
  when nothing changed.
- encore keeps its copy in `~/Library/Caches/encore/home/` (`listen-now.json`,
  `new.json`, `playlists.json`, mode 0600). Each holds the parsed shelves and
  the response's `time_stamp`. A newer response replaces the copy, and the
  copy serves the page once NSURLCache evicts Music's. The two files were 62 KB
  and 29 KB.
- Playlist tracks: no `/v1/catalog/<sf>/playlists…` responses were in the cache
  (0 of the 51 playlists on the page). `internal/home` reads them if Music ever
  caches one (`/playlists/<id>?include=tracks`, `?ids=…`, the first page of
  `/playlists/<id>/tracks`), so playing a Home playlist needs another route for
  now.

## last.fm

- Desktop auth: `auth.getToken` → open `https://www.last.fm/api/auth/?api_key=K&token=T`
  → poll `auth.getSession` every 2–3 s (error 14 = not yet authorized, 15/4 =
  restart). Session keys don't expire.
- An app needs an API key and secret. We can't register one on the user's behalf,
  so setup asks for them once (https://www.last.fm/api/account/create, callback URL
  left blank), then runs the desktop flow. Env/config override.
- `track.updateNowPlaying` at track start (never retried); `track.scrobble` (batches
  of up to 50) once the track is > 30 s and has played half its length or 4 minutes.
  Count actual playing time. Write-ahead queue on disk, retry with backoff.
- Code: `code/lyrics-lastfm/go/lastfm`.

# encore design

encore is a terminal client for Apple Music on macOS. Music.app stays the playback
engine; encore replaces its UI with something fast and keyboard-first that is also
fully usable with a mouse. Built for a large library: every list opens instantly,
search answers per keystroke, and nothing in the UI waits on Music.app.

This document is the contract between components. Research behind the decisions is
in `docs/research/README.md`.

## Goals

- Instant: cold start shows the library from cache in < 150 ms; search over 50k
  tracks returns in < 10 ms; scrolling never stutters; no Music.app call on the UI
  goroutine, ever.
- Familiar layout, close to the Apple Music desktop app: sidebar (Library sections,
  Playlists), main content, player bar at the top with transport, track info and
  progress, volume, AirPlay, lyrics and queue toggles.
- Vim bindings by default, with leader sequences and a which-key popup.
- Everything reachable with the mouse alone: click, double-click, right-click menus,
  wheel, drag to seek and set volume, clickable tabs, buttons and lyrics lines.
- Synced lyrics in a toggleable side pane and a full-screen mode.
- AirPlay device picker (multi-select, per-device volume).
- last.fm scrobbling with a guided one-time setup.
- Looks designed, not generated. See "Visual language".

## Non-goals

- Replacing Music.app's audio engine, downloading, or editing tags.
- Catalog features that need a MusicKit token: adding to the library, editorial
  playlists, personal recommendations. Catalog search uses the public iTunes
  Search API (`internal/catalog`) and catalog playback goes through
  MediaRemote (see "Catalog playback").
- Windows/Linux.

## Architecture

```
┌────────────── encore (Go, Bubble Tea v2) ──────────────┐      JSON lines       ┌── encore-bridge (Swift) ──┐
│ ui  ←→  app state  ←→  bridge client ───────────────────┼── stdin/stdout ─────→│ router                    │
│          │   │   │                                      │                       │ ITLibrary snapshot        │
│          │   │   └→ library index + search (in memory)  │                       │ raw Apple Events → Music  │
│          │   └────→ lyrics (Apple, LRCLIB, cache)        │                       │ playerInfo notifications  │
│          └────────→ last.fm scrobbler (+ disk queue)     │                       │ artwork (ITLibrary/AE)    │
└─────────────────────────────────────────────────────────┘                       └───────────────────────────┘
```

- `encore-bridge` is a long-lived Swift process started by encore. It owns all
  macOS APIs: iTunesLibrary.framework, Apple Events to Music.app, MediaRemote
  (private, `dlopen`ed), distributed notifications, image decoding/resizing. It's embedded in the Go binary
  (`go:embed`) and extracted to `~/Library/Caches/encore/bin/encore-bridge`,
  so encore ships as one file. The path never changes. A launch compares the
  file's sha256 with the embedded binary's and replaces the file only when
  they differ: it writes a temp file in the same folder, syncs it, makes it
  0755 and renames it over the old one. `bin/.encore-bridge.sum` records the
  hash with the file's size and mtime, so a launch that finds both unchanged
  skips hashing the 650 KB file. The rename gives the new file a new inode. A bridge
  still running from the old one keeps running, and the kernel never sees a
  signed binary change in place. A restart after a bridge crash locates the
  file again, so it starts the bridge that matches this encore. Two encore
  versions used in turn replace the file back and forth, and each swap counts
  as an update for TCC (below). Files named `encore-bridge-<sha8>`, from
  builds before the stable path, are removed after a successful extraction.
  A dev override `ENCORE_BRIDGE=/path` skips extraction.
- encore spawns the bridge with `posix_spawn` and the private
  `responsibility_spawnattrs_setdisclaim`, so macOS treats the bridge as its own
  responsible process for privacy (TCC) checks instead of the terminal. Its
  identity is `bridge/Info.plist`, linked into `__TEXT,__info_plist`, and an
  ad-hoc signature with the same identifier (`io.github.bogdankim.encore.bridge`).
  The plist carries the Automation and Media & Apple Music usage strings, which
  the prompts show.
- TCC records a client without an app bundle by its absolute path
  (`client_type` 1 in `TCC.db`) and stores the client's code requirement
  (`csreq`) with the answer. The bridge used to live at
  `bin/encore-bridge-<sha8>`, so every build was a new path, and bridge.log
  showed both prompts again after each update even with the requirement
  below. The stable path fixes the path half.
- The signature carries an explicit designated requirement,
  `identifier "io.github.bogdankim.encore.bridge"` (Makefile, `bridge`). An
  ad-hoc signature's implicit requirement is `cdhash H"…"`, which changes with
  every build. If TCC stores the explicit requirement as `csreq`, a later
  build at the same path satisfies it and keeps the answer. That part is
  unverified. Apple DTS says macOS tracks code identity by the designated
  requirement and that ad-hoc code has no stable one, and recommends a real
  signing identity. Projects that sign ad hoc report that macOS pins their
  grants to the cdhash, but none of them tested an explicit identifier-only
  requirement. A requirement that names only an identifier is also easy to
  forge, since anyone can ad-hoc sign a binary with that identifier, which
  is a plausible reason for TCC to ignore it. So two outcomes are possible
  after an update that changes the bridge. Either TCC keeps the answer, or it
  asks once per changed bridge and replaces the row for that path. An
  encore update that leaves the bridge unchanged doesn't touch the file and
  shouldn't ask in either case. Testing this means answering a prompt, so it
  isn't covered here. After an update, bridge.log says whether it asked:
  `asking for permission to control Music` and `asking for Media & Apple
  Music access` appear only when the bridge prompts. `tccutil reset AppleEvents
  io.github.bogdankim.encore.bridge` resets by bundle identifier and may miss
  a path-keyed row. System Settings → Privacy & Security lists the helper
  either way. Check the requirement with `codesign -d -r-
  internal/bridge/embed/encore-bridge`. A Developer ID signature is the fix
  Apple supports.
- At launch the bridge asks for Automation (if Music runs), then Media & Apple
  Music, one prompt after the other, each at most once per bridge process.
  `Permissions.start` runs before any handler registers. It asks TCC without
  prompting first and holds every Apple Event until TCC answers: up to 0.5 s,
  after which the bridge sends `permissions` with automation `pending`. That
  call (`AEDeterminePermissionToAutomateTarget` with `askUserIfNeeded` false)
  blocks for as long as a prompt is up, minutes on macOS 27. While Automation
  is being checked or asked for, every Apple Event on every queue (music,
  AirPlay, artwork, store, snapshots) fails at once with -1744, hello reports
  `pending`, and the bridge sends `permissions` again once the user answers,
  then `library`. While Music is launching and TCC can't answer for it yet,
  Apple Events fail with -600, which the launch waits retry. All other Apple
  Events go out with `kAEDoNotPromptForUserConsent`, so only `Permissions`
  ever prompts. Before prompting it writes
  `~/Library/Caches/encore/prompts/automation` (or `media-library`) with its
  pid and the time. A bridge that finds another live process's marker, or a
  dead one's less than a minute old, waits for that answer instead of asking.
  `ENCORE_INHERIT_TCC=1` spawns the bridge normally, with the terminal's
  permissions. `ENCORE_NO_PROMPT=1` never prompts, for testing against a real
  Music.
- Without Media & Apple Music access (or with `ENCORE_NO_ITLIBRARY=1`, for
  testing) the bridge reads the library and artwork from Music through Apple
  Events: about 1.5 s for 3,158 library tracks and 1,672 playlist-only tracks,
  against 0.5 s with ITLibrary. That snapshot has no album persistent IDs (encore
  keys albums by album artist and title), no explicit flags, and `art` is always
  true.
- Only user commands launch Music: play, transport, settings and favorites.
  `player.state`, `track.lyrics` and background work never do. Before a launch
  the bridge waits for a quitting Music to go away, then until 3 s have passed
  since it quit, which is `MusicAE.relaunchGrace`. On 2026-10-20 Music showed its
  "Choose Music Library" file panel three times, and each of those Music
  processes had started 0.1 to 0.6 s after the previous one quit. Music said the
  library couldn't be found. The panel is Music's own. Neither encore nor the
  bridge ever shows one.
- The Go side owns all state and policy: the library index, search, the play
  context and queue model, shuffle order, lyrics, scrobbling, config, UI.
- `encore --demo` runs against an in-process fake bridge with a synthetic library
  (deterministic, ~6k tracks, generated artwork). Used for development,
  screenshots and tests. The fake implements the same Go interface as the real
  client.

### Repository layout

```
cmd/encore/            main: flags, config load, wiring
internal/bridge/       Go client for encore-bridge (process, RPC, events), embed, fake
internal/library/      types, index (albums, artists, playlists, sorted views), cache
internal/search/       query parsing and scoring
internal/player/       play context + queue model, shuffle, position interpolation
internal/lyrics/       Apple lyrics from Music's URL cache (TTML), LRC parser, LRCLIB client, provider chain, cache
internal/lastfm/       API client, auth, scrobble tracker, disk queue
internal/keymap/       key sequence engine (trie, counts, timeout), default bindings
internal/config/       paths, TOML config, secrets (Keychain via `security`)
internal/theme/        palettes, styles, glyph sets
internal/art/          image → terminal (iTerm2 IIP, kitty, half blocks)
internal/ui/           Bubble Tea models and components
bridge/                Swift package, product `encore-bridge`
tools/snap/            screenshot harness (pty + xterm.js + headless Chrome)
```

## Bridge protocol

One JSON object per line, UTF-8, `\n` terminated, in both directions. stderr is
for logs only.

Request (Go → bridge): `{"id": 7, "method": "player.seek", "params": {"seconds": 42.5}, "deadline": 1760908803123}`
Response: `{"id": 7, "result": {...}}` or `{"id": 7, "error": {"code": -1728, "message": "…"}}`
Event (bridge → Go, no id): `{"event": "player", "data": {...}}`

`deadline` (optional, unix ms) is when the Go client stops waiting for the
reply. A request still queued past it is skipped when its queue reaches it, and
answered with error `4`, so presses made while Music.app was stuck don't all fire
once it answers again. Without `deadline` the request always runs.

Error codes: Apple Event errors pass through (`-600` Music not running, `-1728`
not found, `-1743` automation permission denied, `-1744` waiting for the
Automation answer). Bridge errors: `1` bad request,
`2` unknown method, `3` internal, `4` expired (deadline passed in the queue), `5`
MediaRemote refused or didn't answer a catalog or station request (the message
names MediaRemote's reason, such as `CommandIgnored`).

After 3 timeouts in a row on Music-queue methods (every method except
`library.snapshot`, `artwork.get` and `playlist.artwork`) with no reply in between, the Go client
kills the bridge and the supervisor starts a new one. It doesn't while the
bridge says automation is `pending` (hello or `permissions`), nor within 10 s
of a -1744 reply: the prompt is what holds Music up, and a new bridge would
only wait for it again.

The bridge processes Music.app calls on one serial queue (concurrency makes Apple
Events slower). Library snapshots run on a separate queue so a snapshot never
delays a play command, and artwork has a third queue with its own ITLibrary
index so art never waits behind a snapshot.

### Methods

| Method | Params | Result |
|---|---|---|
| `hello` | `{}` | `{"version": "1", "musicRunning": bool, "automation": "granted"\|"denied"\|"undetermined"\|"pending", "library": "granted"\|"denied"\|"unknown"}`. `library` is Media & Apple Music access. `pending` while the bridge checks or waits for the Automation prompt. Never prompts. |
| `library.snapshot` | `{"path": "/abs/file.json"}` | writes a `Snapshot` (below) to `path`, returns `{"tracks": n, "playlists": n, "ms": n}`. The bridge encodes 2,000 tracks at a time and appends them (`SnapshotWriter`), so a 100,000-track library costs +17 MB while it is written instead of +481 MB; the Go side reads the arrays one element at a time (`decodeSnapshot`). |
| `player.state` | `{}` | `NowPlaying` |
| `player.playpause` / `player.pause` / `player.play` | `{}` | `{}` (`player.play` resumes; `playpause` reads the state and sends pause or play; pause and play are checked at 400 ms and 1.2 s and resent through MediaRemote if Music ignored them) |
| `player.next` / `player.previous` | `{}` | `{}` |
| `player.seek` | `{"seconds": f}` | `{}` |
| `player.volume` | `{"volume": 0..100}` | `{}` |
| `player.repeat` | `{"mode": "off"\|"one"\|"all"}` | `{}` |
| `player.shuffle` | `{"enabled": bool}` | `{}` (Music's native shuffle; encore keeps it off for queue playback) |
| `player.playTracks` | `{"pids": ["HEX", …], "containers": {"HEX": "PLAYLIST_HEX"}}` | `{}`: rebuilds the queue playlist with these tracks in order, plays from the first. `containers` maps tracks that are not in the library to a playlist that contains them. |
| `player.playPlaylist` | `{"pid": "HEX"}` | `{}`: native `play playlist` |
| `player.playStore` | `{"ids": ["1440942209", …], "start": 0}` | `{}`: plays Apple Music catalog songs by store ID, `ids[start:]` in order, through MediaRemote's store queue. Launches Music if needed, turns its shuffle off, waits up to 5 s for MediaRemote. Returns after about 1.6 s; audio starts about then. |
| `player.playStation` | `{"id": "ra.725086732"}` | `{}`: plays an Apple Music radio station by station ID (`ra.` and then letters, digits, `-`, `_`, `.`) through MediaRemote's radio station queue. Launches Music if needed, leaves its shuffle alone, waits up to 5 s for MediaRemote. Returns after 1–3 s; audio starts about then. |
| `track.setFavorite` | `{"pid": "HEX", "value": bool}` | `{}` |
| `track.setDislike` | `{"pid": "HEX", "value": bool}` | `{}` |
| `track.lyrics` | `{"pid": "HEX"}` | `{"text": "…"}` (Music's own plain lyrics, often empty). Never launches Music; fails with -600 when it isn't running. |
| `airplay.list` | `{}` | `{"devices": [AirPlayDevice]}` |
| `airplay.select` | `{"ids": [int]}` | `{}` (selected set becomes exactly these) |
| `airplay.volume` | `{"id": int, "volume": 0..100}` | `{}` |
| `artwork.get` | `{"pid": "HEX", "size": px, "path": "/abs/out.jpg"}` | `{"path": "…", "w": n, "h": n}` or `{"path": ""}` when there is none. Square, resized with ImageIO, JPEG at quality 0.9 (a 256 px cover is about 10 KB; as PNG it was 97 KB). `pid` may be a track or an album persistent ID. Through Music (when ITLibrary can't answer) an album pid is mapped to a track by the last snapshot; before the first one a pid Music doesn't know is an error, not `{"path": ""}`, because it can't be told from an album yet. |
| `playlist.artwork` | `{"pid": "HEX", "size": px, "path": "/abs/out.jpg"}` | Same reply as `artwork.get`, for the cover Music shows for a playlist (`raw data of artwork 1 of playlist`; `count of artworks` reads 0 even when there is one). Always an Apple Event: ITLibrary has no playlist artwork. `{"path": ""}` for Library, Music Videos and some subscription playlists. |

### Events

| Event | Data | When |
|---|---|---|
| `player` | `NowPlaying` | on every playerInfo notification (re-read via one `properties of current track` AE), after our own commands, and once per second while playing |
| `airplay` | `{"devices": [AirPlayDevice]}` | when the selection or volumes change (poll every 5 s while the AirPlay picker is open, requested by `airplay.list`) |
| `airplay.repaired` | `{"devices": ["Living Room"], "ok": bool}` | after the bridge reconnected a stale AirPlay route (see "AirPlay route repair"). `devices` are the names it selected again; `ok` is false when Music kept playing on the Mac or refused the change |
| `music` | `{"running": bool}` | Music.app launched or quit (NSWorkspace notifications) |
| `library` | `{}` | the library changed, checked every 60 s and on `music.running`; also after a permission prompt. With the library in its default place the check is the size and mtime of `Library.musicdb`; for a library stored elsewhere it is an ITLibrary reload (item count, newest modification), which is skipped while Music isn't running |
| `permissions` | `{"automation": …, "library": …}` | the bridge started waiting for the Automation answer (`automation` is `pending`), or the user answered a permission prompt; fields as in `hello` |

### Types

```jsonc
// NowPlaying
{
  "state": "playing" | "paused" | "stopped",
  "position": 61.4,          // seconds, 0 when stopped
  "at": 1760908800123,       // unix ms when position was read
  "volume": 40, "shuffle": false, "repeat": "off",
  "track": null | {
    "pid": "00000000000000A0", "name": "…", "artist": "…", "album": "…",
    "albumArtist": "…", "duration": 211.8, "favorited": true, "disliked": false
  }
}
// AirPlayDevice
{ "id": 29274, "name": "Living Room", "kind": "homepod" | "computer" | "speaker" | "tv" | "unknown",
  "selected": true, "active": true, "available": true, "volume": 35 }
// Snapshot
{
  "generatedAt": 1760908800,
  "tracks": [Track], "playlists": [Playlist]
}
// Track
{
  "pid": "HEX", "title": "…", "artist": "…", "albumArtist": "…", "album": "…",
  "albumPid": "HEX", "genre": "…", "composer": "…", "year": 2014,
  "track": 3, "trackCount": 12, "disc": 1, "discCount": 1,
  "duration": 211800,                    // ms
  "added": 1700000000, "lastPlayed": 0,  // unix seconds, 0 = never
  "plays": 12, "rating": 0,              // 0..100
  "favorited": true, "disliked": false,
  "explicit": false, "compilation": false,
  "inLibrary": true,                     // in `library playlist 1`
  "available": true,                     // false for "no longer available" cloud items
  "kind": "subscription" | "uploaded" | "purchased" | "local",
  "art": true,
  "sortTitle": "…", "sortArtist": "…", "sortAlbum": "…", "sortAlbumArtist": "…",
  "storeId": 1561682197                  // Apple Music catalog song id; omitted for local files and uploads
}
// Playlist
{
  "pid": "HEX", "parentPid": "HEX" | "", "name": "…",
  "kind": "user" | "smart" | "folder" | "subscription" | "genius",
  "special": "" | "library" | "music" | "purchased" | "favorites" | "…",
  "items": ["HEX", …],
  "globalId": "pl.bed492442a53481f98e98c6c4da9e01d"   // cloud id the catalog and Home use; omitted without one
}
```

`storeId` is iTunesLibrary's private `storeItemID` (the same number the iTunes
Search API calls `trackId`) and `globalId` its private `globalID` for playlists
(`pl.…`). Both are read the way `cloudType` is, behind a selector check, and are
left out when the key is missing or 0. The Apple Events snapshot has neither, so
matching falls back to names there (see "Apple Music items in the library").
Albums have no id of this kind (`ITLibAlbum`'s `adamID` and `cloudAlbumID` read 0).

`favorited` comes from one AE (`persistent ID of every track of library playlist 1
whose favorited is true`) merged into the ITLibrary data. `inLibrary` likewise
from `persistent ID of every track of library playlist 1`. The queue playlist
(named `encore`) is excluded from snapshots.

### AirPlay route repair

After a long idle or a lock and unlock, macOS can leave Music with an AirPlay
device selected while it plays through the Mac's own output. Measured on macOS
27 and Music 1.7: Music's `AirPlay devices` read the same in that state as in a
healthy one (the HomePod `selected` true, `active` false; `active` is false
even while it really plays), so Apple Events can't tell them apart. CoreAudio
can. While Music plays to AirPlay only, its process object (found with
`kAudioHardwarePropertyTranslatePIDToProcessObject`) has
`kAudioProcessPropertyIsRunningOutput` 0 and no output-scope
`kAudioProcessPropertyDevices`, because Music streams AirPlay itself. In the
broken state the sound comes out of the speakers and the process runs output
on the built-in speakers (`runningOutput 1`, `MacBook Pro Speakers [bltn]`).
Confirmed on 2026-10-22 after an unlock: the bridge saw that reading with
Living Room selected, reconnected, and local output stopped (`runningOutput
0`, no outputs); the HomePod played again.

- `AirPlayRepairer` (`AirPlayRepair.swift`, on the AirPlay queue) reads
  CoreAudio on CoreAudio listeners (Music's process object:
  `IsRunningOutput`, output `Devices`; the system object's process list, for
  Music launching), on a change of player state or track (`PlayerMonitor`),
  on an `airplay` event, 2, 8 and 20 s after `NSWorkspace` wake and
  session-active notifications and `com.apple.screenIsUnlocked`, and every
  30 s while playing. A reading costs no Apple Event unless Music runs output
  on a device whose transport isn't `airp`; then it reads the player state
  and the AirPlay list.
- `StaleRouteDetector` (`StaleRoute.swift`, a value type with unit tests in
  `bridge/Tests`) decides. Stale means: playing, at least one device
  selected, no `computer` among them, local output, all held for 3 s. Any
  reading without local output restarts the 3 s, and so does a new selection
  or track. It allows one reconnect per 60 s and gives up after 2 failures
  until the selection or the track changes.
- The reconnect selects the `computer` device, waits 400 ms, re-reads the
  list and selects the old devices again by persistent ID, then reads
  CoreAudio every 500 ms. Two readings in a row without local output within
  5 s count as success. If a set fails, it puts the old selection back. It
  emits `airplay` with the new selection and then `airplay.repaired`.
- Every stale-looking reading, every decision and the readings around a
  reconnect go to stderr (bridge.log) as `airplay route: …`, with Music's
  pid and process object, `runningOutput`, each output device's name,
  transport type, id and `DeviceIsRunningSomewhere`, and the default output.
  Healthy readings are logged only when they change, plus the first one
  after a wake.
- `--no-airplay-reconnect` (config `[airplay] auto_reconnect = false`, passed
  by the Go client in `Options.NoAirPlayReconnect`) keeps the detection and
  the log ("would reconnect") but never changes the route.
- `ENCORE_AUDIO_PROBE=1 swift test --filter probe` in `bridge/` prints one
  reading the same way, without touching Music.
- The fake's `DropAirPlayRoute(ok)` emits the same two events, so UI tests
  cover the footer status ("Reconnected to Living Room", or "Couldn't
  reconnect to Living Room" as a warning).

## Playback model (Go, `internal/player`)

- A play context is an ordered list of track pids plus a source (album, playlist,
  songs view, search results, artist) and the index that started it.
- Playing item N of a context sends `player.playTracks` with `context[N:]` (capped
  at 300 tracks; when playback reaches the end of that slice, the next slice is
  issued).
- Playing a playlist from its first track with shuffle off uses
  `player.playPlaylist` (native context, works with media keys).
- Shuffle is ours: `[N] + shuffled(rest)`. Toggling shuffle while playing rebuilds
  the remaining order and re-issues at the next track boundary.
- `previous` at queue index 0 re-issues `context[N-1:]` when N > 0, otherwise
  restarts the track.
- Up Next: "Play next" and "Add to queue" edit our model. Music only reads the
  queue playlist at `play` time, so the bridge re-issues the queue at the next
  track boundary when it's dirty (the notification for the new track arrives, we
  play our intended next track instead, the overlap is under a second).
- Position is interpolated locally from `NowPlaying.position` + `at` with a
  monotonic clock, corrected on each event.

### Catalog playback

- `PlayCatalog(src, items, start)` plays Apple Music catalog songs that need not
  be in the library (`CatalogItem`: store ID, title, artist, album, duration,
  art URL). `PlayNextCatalog` and `AddCatalogToQueue` queue them.
- In the model a catalog song is a stand-in `*library.Track` with pid
  `store:<id>` (`IsCatalog`, `Player.CatalogItem`), so the context, queue,
  shuffle and Up Next don't care which kind an entry is. `Snapshot.Track` is
  nil for a catalog song and `Snapshot.Catalog` carries it.
- Issuing sends one kind per call: the slice from the current entry runs until
  the kind changes, through `player.playStore` for catalog songs and
  `player.playTracks` for library tracks. Music walks a store tracklist itself,
  so next and previous within it are Music's own, and the next call goes out
  when Music stops (or wanders off into Autoplay) at the end of the slice.
- Music plays catalog songs under temporary persistent IDs, so a report that
  isn't a pid we issued is matched by name, artist and duration against the
  catalog songs we issued, nearest to where Music should be. That match drives
  boundary detection and the Up Next re-issue as for library tracks.
- Catalog songs take longer to start: a play stays pending up to 10 s, and a
  song that leaves Music stopped for 6 s counts as dead and is skipped.

### Station playback

- `PlayStation(src, station)` plays an Apple Music radio station through
  `player.playStation` and makes it the context (`Source.Kind` is
  `KindStation`). `Station` has the ID (`ra.…`), name, kind (`personal`,
  `artist`, `mood`, `live`, `discovery`) and an optional art URL.
  `ArtistStationID(id)` is `ra.<artistId>` (the artist and similar artists),
  `ArtistOnlyStationID(id)` `ra.a-<artistId>`.
- Music picks every song, so there is no context to walk: `Snapshot.Station`
  names the station, `Track` and `Catalog` are nil for its songs, and
  `NowPlaying.Track` carries Music's report. Up Next lists only the user queue
  and says "Station: <name>" when it is empty. Music's own song changes are
  followed, not re-issued.
- Next is Music's next song in the station. Previous is sent to Music as
  it is; a station may only restart the song.
- The start stays pending up to 10 s. Any song reported after the call
  returned, other than the one Music had, confirms it. A station that leaves
  Music stopped for 6 s, or never replaces the old song, failed: the station
  is dropped with "The station didn't start".
- Items queued during a station play at the next song boundary, issued like
  any context. When Music reaches the end of them (or Next is pressed on the
  last one) the station starts again, with a fresh song.
- Live stations (kind `live`, or a song Music reports with duration 0) have
  no timeline: `Snapshot.Live` is set, position and duration are 0, seeking
  does nothing, and the bar shows "Live" in place of the elapsed time.
- While the station's own songs play, the bar's second line is "artist ·
  Station Name": the station takes the album's place and isn't a link.
- Playing anything else, or Music moving off the station's queued items to
  something encore didn't issue, ends the station. Music's own move from the
  station to something else (from Music.app) can't be told apart from the
  station's next song, so encore keeps showing the station until then.
- Toggle on a station Music stopped (or after Music quit) starts it again.

## Library index (`internal/library`)

- Loaded from the disk cache (`~/Library/Caches/encore/library.cache`) at start, then
  refreshed from `library.snapshot` in the background; the UI swaps the index
  atomically and keeps the cursor on the same item. A refresh waits 20 times as
  long as the last successful one took (at most 5 minutes) after it, on top of the
  5 s debounce, so a big library isn't re-read because Music rewrote its database.
  `setLibrary` resets the search filters' corpora, which point into the old library.
- Views: Recently Added (albums by newest track `added`), Artists (by album artist,
  falling back to artist; "Various Artists" for compilations), Albums (by album
  pid; fallback key `albumArtist|album`), Songs (in-library tracks), Favorites
  (Apple's favorited songs), Playlists (tree with folders).
- Default sorting follows Apple Music: sort fields first, case-insensitive,
  leading "The " ignored, diacritics folded.
- The cache file is version 2: version 1 added nothing but still loads, without
  store ids and global ids, until the next snapshot replaces it.
- Search keys are precomputed per entity: folded lowercase strings for title,
  artist, album.

### Apple Music items in the library

Wherever the UI lists catalog items it marks the ones the library has (see
"Visual language"). `internal/library/match.go` is the one matcher, indexed in
`Build` (about 12 ms on 50k songs) so a lookup is a map access:

- Song: by catalog id (`Track.StoreID`) among the songs in the library
  (`InLibrary`; a song that only sits in a playlist is not in it). Without an
  id on both sides, by `SongKey` of the title (a featured artist or a remaster
  note dropped, "(Live)" and "(Acoustic)" kept) and `SameArtist` (the lead
  artist: "Daft Punk" matches "Daft Punk & Pharrell Williams"). A library
  song with an id of its own that differs from the catalog's is another
  release, so it does not match by name.
- Album: by `AlbumKey` of the title (edition notes such as "(Deluxe
  Edition)", "[Remastered]" and "(Explicit)", and a trailing " - Single" or
  " - EP" dropped) and the artist (folded, "The" optional), among the albums
  the library lists. Albums carry no ids, so there is no id match. The album
  page also counts the album as in the library when one of its songs matches by
  id, for an album the library titles differently.
- Playlist: by cloud id (`Playlist.GlobalID`) only. Folders never match.
- Artists are matched for the "In Your Library" link on an artist page but
  never marked in lists: one song is enough to have an artist.

"Open in Library" uses the same keys, with a looser song match (any library
song of that title and artist, id or not).

## Search (`internal/search`)

- Scopes: All, Songs, Albums, Artists, Playlists, and Apple Music, which
  searches the catalog (iTunes Search API) 350 ms after typing stops and lists
  artists, albums and songs. All ends with a row that switches to it.
- Query: whitespace-separated terms, all must match (AND). Each term matches as a
  substring of any field; scoring favours whole-word and word-prefix matches,
  title over artist over album, shorter fields, and favorited/recently played as a
  tie-breaker. Fuzzy (subsequence) matching is a fallback when the strict pass
  returns nothing.
- Runs on a goroutine with the query generation number; stale results are
  dropped. Budget: < 10 ms for 50k tracks.

## Visual language

The look is restrained: one accent, two text tones, whitespace and thin rules
instead of boxes. The terminal background shows through (the user runs WezTerm
with a translucent, blurred background), so encore never paints a full-screen
background.

### Palette (default "encore", dark)

| Token | Value | Use |
|---|---|---|
| `fg` | terminal default | primary text |
| `dim` | `#8B8B93` | secondary text: artist, album, durations, counts, hints |
| `faint` | `#4A4A52` | rules, inactive glyphs, scrollbar track |
| `accent` | `#FA586A` | now playing marker, focused selection marker, progress fill, active tab, current lyric line |
| `sel` | `#26262B` | selected row background (unfocused pane: no background, dim marker) |
| `hover` | `#1E1E22` | hovered row background |
| `warn` | `#E5B567` | errors and warnings, rare |

Light variant chosen automatically from the terminal background
(`tea.RequestBackgroundColor`). `NO_COLOR` turns colours off (bold/underline only).
Built-in alternatives: `mono` (accent = fg), and a few accent presets. Users can
override any token in config.

### Rules

- No outer border, no box around panes. The sidebar is separated by one vertical
  rule (`│` in `faint`), the player bar by one horizontal rule (`─`). Overlays
  (search, palette, menus, pickers) are the only bordered things: one rounded
  border in `faint`, nothing nested.
- Focus shows as the accent marker `▎` at the left of the selected row and the
  `sel` background; the unfocused pane keeps a dim marker only.
- Columns line up exactly: fixed widths computed per layout, numbers and durations
  right-aligned, truncation with `…` by display width (`lipgloss.Width` /
  `ansi.Truncate`), never wrapping.
- Headers are plain dim words (Title, Artist, Album, Time), no underline, no
  separators between columns, two spaces of gutter.
- The view title is one line at the top of the content: name in `fg` bold, a
  dim count to its right ("3,158 songs · 9 days"). No "Welcome", no subtitle.
- Empty states are one dim sentence centred in the pane ("No favorites yet").
- Hints: the footer shows at most five dim key hints for the focused view or
  pane, and disappears when the window is short. Its left side names the
  control under the pointer and its key; otherwise it shows the last status
  message for 3 s or until the next key or click. Without the footer, that
  name or status sits at the right end of the rule under the player bar. A which-key popup appears
  after the leader.
- Glyphs: `▶` playing, `♥` favorited, `✓` (dim) an Apple Music item the
  library already has, `E` boxed explicit tag in dim, `·`
  separators, `━` progress fill with `╸` head and `─` rest, `▁▂▃▄▅▆▇█` volume.
  Media button glyphs come from the active glyph set (Nerd Font symbols on WezTerm,
  which bundles them; plain Unicode elsewhere). No emoji anywhere.
- Motion: only the progress bar and the lyrics highlight move. A spinner appears
  only while something is actually loading and longer than 300 ms.

### Layout

```
 ⏮  ⏸  ⏭   ⇄  ↻     ▒▒▒  Daylight Drive ♥                               ▂▄▆ 40   ⌁ Living Room   ❞  ☰
                    ▒▒▒  Redmatic · Daylight Drive - Single
                    ▒▒▒  1:02 ━━━━━━━━━━━━━━━━━━━━━━━━━╸──────────────────────────── -1:10
─────────────────────────────────────────────────────────────────────────────────────────────────────────
 Search ⌕           │  Songs   3,158 songs · 9 days
                    │
 Library            │     Title                          Artist               Album                   Time
   Home             │  ▎▶ Daylight Drive                 Redmatic             Daylight Drive - Single 2:12
   Recently Added   │     Song two                       …
   Artists          │
   Albums           │
   Songs            │
   Favorites        │
                    │
 Playlists          │
   Chill            │
   Gym              │
                                             j/k move · enter play · / filter · space commands · ? help
```

- Player bar (top, 3 rows + rule), like Apple Music's toolbar: transport and
  shuffle/repeat on the left, the "LCD" in the middle (art 6×3 cells, title with
  ♥, artist · album, elapsed / progress / remaining), volume, AirPlay, lyrics and
  queue toggles on the right. Every element is clickable.
- Sidebar: 22 columns by default (drag its rule to resize). Under 90 columns,
  with `space b`, or when a right pane needs the room, it folds to a 3-column
  rail of icons (Search, the Library sections, Playlists). Playlists on the
  rail opens the full sidebar as a drawer over the content. Section headers
  in dim, items in fg, the active item in accent.
- Content: the current view. Album detail and artist pages have a header with
  large art (IIP image or half blocks).
- Right pane (optional): lyrics or Up Next, 36–48 columns, toggled independently;
  only one is shown at a time.
- Overlays: search (centred, 70% width), command palette, help, context menu
  (at the mouse or the selected row), AirPlay picker, last.fm setup.

### Views

- Home: Apple Music's Home page read from Music's cache (`internal/home`).
  The title line says "Home" and how old the page is ("updated 2 h ago").
  Shelves stack vertically: a bold title at column 2 over one row of tiles
  (art, title, subtitle, selection underline), a blank line between
  shelves, and the New tab's shelves under a dim "New" heading. Tiles are
  13 to 19 cells wide (`layoutTiles`). A row scrolls by whole tiles; a
  faint `‹` at column 0 and `›` at the last column, level with the middle
  of the art, say there is more. Station tiles say "Station" under the
  name, live stations "Live" and the show on air. With `start = "home"`
  encore opens on it when it has shelves; without a cached page it says "Open Home in Music
  once and it will show up here."
- Recently Added: album grid (art tiles with title and artist under each).
- Artists: two columns: artist list, and the selected artist's albums (grid) with
  their top songs.
- Apple Music items the library has carry a dim `✓`, always in a fixed place:
  in the favorite column's slot (two cells before Time) of an Apple Music
  album's track list and an artist's top songs; at the right edge of the title
  line of an album tile or a Home tile, the title giving up its last two cells;
  at the left edge of the time column of an Apple Music search row. Library
  views never show it. Up Next doesn't either: its rows are tight and what
  plays next isn't a library question.
- Albums: grid by default, list with `v` (or the toggle in the title line).
  `o` cycles the sort and `O` reverses it.
- Songs: table (Title, Artist, Album, a heart column, Time; Plays and Added
  when there is room). The heart column shows `♥` on favorites and an outline
  on the hovered row; clicking it toggles the favorite. Sort by clicking a
  header, `o` for the next column and `O` to reverse.
- Favorites: same as Songs.
- Playlist: table with the playlist's own order.
- Album detail: art + title + artist + genre · year + N songs · duration, then the
  track list (disc headings when > 1 disc). Album, playlist, folder and artist
  pages share this header; opening the playing album or playlist puts the
  cursor on the playing song.
- Queue (Up Next): the current context from the playing track on, editable
  (reorder any row with `J/K` or drag with the mouse, remove with `x`, `m` or
  right-click for the row menu). As in Apple Music, a context row moved by hand
  becomes queued, along with the context rows it now follows. Tracks take two
  rows (title over artist) below 60 columns, one row with fixed columns above.

## Interaction

### Keymap (defaults)

Leader is `space`. While a sequence is pending the which-key popup lists the
continuations and stays open until a key or `esc`; only a sequence that is both an
action and a prefix fires on a 1 s timeout. Counts work for motions (`5j`).

Navigation

| Keys | Action |
|---|---|
| `j` `k` / `↓` `↑` | down / up |
| `gg` `G` / `Home` `End` | top / bottom |
| `ctrl+d` `ctrl+u` | half page |
| `PgDn` `PgUp` | page |
| `h` `l` / `←` `→` | focus pane left / right |
| `tab` `shift+tab` | cycle focus |
| `enter` | play the selected song, or open the selected album/artist/playlist |
| `esc` / `backspace` | close the overlay or filter, or leave a detail page (never switches library sections) |
| `ctrl+o` `ctrl+i` | back / forward in view history |
| `/` | filter the current list (live), `esc` clears |
| `v` | toggle grid/list where it applies |
| `o` `O` | next sort field / reverse the sort |

Playback

| Keys | Action |
|---|---|
| `p` | play / pause |
| `]` `[` | next / previous track |
| `.` `,` | seek +10 s / −10 s (`>` `<` for ±30 s) |
| `=` `-` | volume +5 / −5 |
| `s` | shuffle |
| `P` | play the page: the album, playlist or artist shown, every song on Songs and Favorites, the selected album in a grid (the header's Play) |
| `S` | the same, shuffled (the header's Shuffle) |
| `r` | repeat: off → all → one |
| `f` | favorite the selected song (`F` for the playing song) |
| `a` | add selected to Up Next (end) |
| `A` | play selected next |
| `m` | context menu for the selection |

Up Next and lyrics panes (while focused)

| Keys | Action |
|---|---|
| `J` `K` | move the selected Up Next track down / up |
| `x` | remove the selected track from Up Next |
| `{` `}` | lyrics offset −100 ms / +100 ms |
| `enter` | play the selected track (Up Next), seek to the line the scroll rests on (lyrics) |

Leader

| Keys | Action |
|---|---|
| `space s s` | search songs |
| `space s a l` | search albums |
| `space s a r` | search artists |
| `space s p` | search playlists |
| `space s m` | search Apple Music |
| `space /` | search everything |
| `space l` | toggle lyrics pane |
| `space L` | full-screen lyrics |
| `space q` | toggle Up Next pane |
| `space a` | AirPlay picker |
| `space g h` `space g r` `space g a r` `space g a l` `space g s` `space g f` | go to Home / Recently Added / Artists / Albums / Songs / Favorites |
| `space g p` | go to playlists (focus the sidebar list) |
| `space g n` | go to the playing album |
| `space o a l` | open the album of the selected song or album in Apple Music (a song's album opens with the song selected) |
| `space o a r` | open the artist of the selected song, album or artist in Apple Music (a song's lead artist, without "feat." guests) |
| `space o l l` | open the album of the selected Apple Music song or album in the library |
| `space o l r` | open the artist of the selected Apple Music song, album or artist in the library |
| `M` `space o m` | open the selected Apple Music song, album or playlist in Music.app (a song on its album's page), in the library or not; Music's own + adds it |
| `space b` | toggle sidebar |
| `space space` | play / pause |
| `space ?` / `?` | help (searchable list of every binding) |
| `:` / `ctrl+k` | command palette |
| `q` | quit (Music keeps playing) |

Text input (search, filter, palette) captures all printable keys; only `esc`,
`enter`, `tab`, arrows, `ctrl+j/k/n/p`, `ctrl+w`, `ctrl+u` are interpreted.

All bindings are overridable in `~/.config/encore/config.toml`:

```toml
[keys]
leader = "space"
"play-pause" = ["p", "<leader><leader>"]
"search.songs" = ["<leader>ss"]
```

### Mouse

- Click a sidebar item or rail icon to open it; the content pane takes focus.
  Click an album tile to open it. Click a row to select it,
  double-click to play or open. Right-click for the context menu (Play, Play
  Next, Add to Up Next, Go to Album, Go to Artist, then Open Album in Apple
  Music and Open Artist in Apple Music, then Favorite; an album's menu has
  both Open items, an artist's only Open Artist. Apple Music items get Open
  Album in Library and Open Artist in Library instead, each when the
  library has that album or artist, and no Favorite). Apple Music songs and
  albums end with Open in Music.app, whether or not the library has them.
  Home albums and songs have the same menus; a Home playlist has its own
  (Play when encore can play it, then Open in Music.app; a playlist that
  only opens in Music has just that item). Search results have the menu too.
- Right-click the player bar's title, artist or art, or Up Next's playing
  row, for the playing song's menu: Go to Album, Go to Artist, then
  Favorite for a library song or Open in Music.app for an Apple Music one,
  a station's song among them (found in the library, else in the catalog).
- When the first press of a double-click opens a view or an overlay, encore
  drops the second press, so it never plays the track that ends up under the
  pointer.
- Mouse buttons 4 and 5 go back and forward in view history. Detail pages
  show `‹` at the top left of the page when there is history; clicking it
  goes back.
- Wheel scrolls the pane under the pointer (3 rows per notch); hover highlights the
  row under the pointer and names hovered controls in the footer. The name
  clears when the pointer moves off or the view changes.
- Click a column header to sort, again to reverse. Click the heart column to
  toggle a favorite.
- Player bar: click buttons; click or drag the progress bar to seek; click, drag or
  wheel on the volume meter; click the AirPlay name for the picker; click ❞ and ☰
  for the lyrics and Up Next panes; click the title or the art to go to the album.
  Under 100 columns ❞ opens full-screen lyrics and ☰ opens Up Next as a drawer
  over the right of the content. Each toggle is lit only while its pane is on
  screen.
- Lyrics: click a line to seek to it; wheel scrolls freely and the view snaps back
  to the current line 3 s later.
- Overlays: click outside closes; items are clickable; scope tabs in search are
  clickable.
- Drag the vertical rule to resize the sidebar or right pane.

## Lyrics

Provider chain from the research notes: Apple Music's synced lyrics from
Music's URL cache, then LRCLIB, then Music's own `lyrics`. Apple's are checked
first on every lookup and a few times in the first 10 s of a track, and replace
LRCLIB's on screen and in the disk cache. The pane shows the current line in
accent bold (with word timing, the line is bold and fills like karaoke: sung
words in accent, the word being sung filling left to right over its time with
the edge cell mixed between text colour and accent, words to come in text
colour; without 24-bit colour each word turns accent as it starts), upcoming
lines in dim and sung lines
a step quieter (a third of the way from faint to dim, since faint is too low to
read as text), with the current line kept at about 40% height. Plain lyrics
scroll with the track position proportionally. The pane title is just "Lyrics",
plus "unsynced" or the offset when they apply. A per-track offset (`{`/`}` in the
pane, 100 ms steps; `-`/`=` stay volume) is saved to disk. Lookups prefetch the
next track in the context. Full-screen lyrics put the art, title, progress (click
or drag to seek) and prev / play-pause / next buttons on the left; `×`, `esc` or
a double-click on the art closes it.

## last.fm

`:lastfm` (or `space ,`) opens a three-step setup overlay: open the API account
page, paste key and secret, approve in the browser (we poll). Secrets go to the
Keychain (`security`, service "encore", account "lastfm"). Status shows as a dim
dot in the player bar's right side only when there's a problem (queue stuck,
re-auth needed). Scrobbles queue on disk and retry with backoff.

## Config and files

- Config: `~/.config/encore/config.toml` (theme, accent, icons, keys, sidebar
  width, art protocol, lyrics provider toggles, last.fm on/off, AirPlay
  auto-reconnect).
- Cache: `~/Library/Caches/encore/` (library.cache, art/, lyrics/, bin/). See
  "Caches and memory" for what is bounded.
- State: `~/Library/Application Support/encore/` (scrobble queue, lyric offsets,
  last view, UI state).
- Logs: `~/Library/Logs/encore/bridge.log` (the bridge's stderr and the Go
  client's own diagnostics, rotated at 5 MB).

## Caches and memory

Everything encore keeps is bounded. The limits, so a number you see in `top` has an explanation:

| What | Where | Limit |
|---|---|---|
| Covers (library, Apple Music, playlist mosaics) | `art/*.jpg`, JPEG | files unused for 90 days go at start, then the least recently used until the folder is under 256 MB. A read bumps the file's mtime at most once a day. |
| Covers from before JPEG | `art/*.png` | deleted at start |
| Lyrics | `lyrics/*.json` | 120 days unused, 64 MB |
| Catalog and Home copies | `catalog/`, `home/` | 7 days (catalog) |
| Snapshot temp files | `snapshot-*.json*` | deleted at start when over 10 minutes old (a crash left them) |
| Decoded cover images | `art.Renderer.images` | 96 images and 48 MB |
| Scaled boxes (what an iTerm2 image is cut from) | `art.Renderer.boxes` | 96 boxes and 32 MB |
| Encoded payloads (the bytes sent to the terminal) | `art.Renderer.payloads` | 8 MB, about 700 grid tiles. A tile counts as ready while its payload is kept, even when its decoded image is gone. |
| Half-block tiles | `artStore.tiles` | 512 tiles |
| "No artwork" answers | `artStore.none`, `bridge.Process.noArt` | tiles ask again after 10 s and 1 minute, then it stands 10 minutes; the client's copy stands 1 s; expired entries are deleted when met |
| Failed loads | `artStore.failed` | a tile retries after 2 s, doubling to 30 s; kept until it loads or 10 minutes pass |
| Bridge `ArtIndex` (an `ITLibMediaItem` per track) | bridge | released after 120 s without an artwork request, reloaded in about 0.5 s. A failed or empty reload is tried again after 5 s; until then covers come from Music. |

The art folder, the lyrics folder and the snapshot leftovers are cleaned by a
goroutine at start (`internal/cachegc`), never on the UI goroutine.

Go's heap is the rest. Go lets it grow to twice the live data before it
collects, and a scroll through the album grid makes a lot of short-lived
garbage, so without a limit the resident size sat at 131 MB on the demo library
(140 to 150 MB on a real one). encore sets a soft memory limit of 100 MB at
start (`cmd/encore/memory.go`), which only makes the collector work harder as
the process nears it. A live heap that needs more raises the limit (live x 1.25
+ 20 MB, checked every 2 s), so a 100,000-track library doesn't leave the
collector running all the time. `GOMEMLIMIT` in the environment replaces the
whole mechanism. Once the UI has had no key, mouse or resize for 5 s after
64 MB of allocation (a scroll, a library refresh), `debug.FreeOSMemory` runs
once. On the demo library, scrolling the grid three times left 133 MB resident
at the end of each pass; with the limit it is 96 MB, and 78 MB four seconds
after the last scroll.

## Performance budgets

| Operation | Budget |
|---|---|
| Start to first frame with cached library | 150 ms |
| Library snapshot refresh (5k tracks) | 1.5 s, background |
| Search keystroke → results (50k tracks) | 10 ms |
| View() with 200×60 terminal | 2 ms |
| Play command → bridge send | same frame |
| Holding `j` in the album grid (demo library, 169×52, pty harness) | 22 to 24% of a core once the covers are cached, 41% the first time through; wheel 18%; page down 13 to 18%. Before: 74%, 48%, 76% |
| A tap on `j` that scrolls the album grid a row (real library, 140×42, pty harness, cold covers, directly in a terminal that scrolls images: WezTerm, iTerm2) | 10 images and 54 KB, last image byte 17 ms. A tap on `k`: 10 images, 85 KB. Before the images scrolled in the terminal: 15 images, 125 KB, 23 ms (and before the payload work: 12 of 15 covers, 155 KB, 35 ms). Inside tmux, and with `ENCORE_IMAGE_SCROLL=off`, it is still 15 images. A tap that only moves the selection sends 0 to 200 bytes. |
| Resident memory after scrolling the whole grid (demo library) | 96 MB, 78 MB once idle (it was 133 MB) |

## Testing

- Unit tests per package (`go test ./...`). The bridge has a `--selftest` mode,
  and `swift test` in `bridge/` covers its pure parts (the stale AirPlay route
  detector). `make test` runs both.
- `encore --demo` with the fake bridge for UI work.
- `tools/snap`: runs encore in a pty rendered by xterm.js inside headless Chrome,
  sends keys and mouse events from a script, saves PNG screenshots. Used to review
  every UI change.

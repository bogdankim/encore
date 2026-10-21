# Using encore

The full reference: permissions, every default key, each feature, the config file and how the parts fit together. The [README](../README.md) has the short version.

## First run and permissions

encore is one file. On first run it extracts its Swift helper, `encore-bridge`, to `~/Library/Caches/encore/bin/encore-bridge`, and later runs replace that file only when the helper changed.

On first launch macOS asks two things about encore itself, whichever terminal you run it in. First, whether encore may control Music. Click Allow. Second, whether encore may access Apple Music and your media library. Click Allow there too. If you clicked Don't Allow, the footer says so. Turn encore back on in System Settings → Privacy & Security → Automation (Music under encore) or → Media & Apple Music, and restart encore. Without media library access encore still works: it reads the library and artwork from Music, which is slower.

While a prompt is open, the footer says "Waiting for permission to control Music…" and encore leaves Music alone until you answer. encore asks once per launch. If encore restarts its helper while a prompt is still open, the new helper waits for that answer and doesn't ask again.

The helper has an ad-hoc signature and no app bundle, so macOS remembers your answers by the helper's path and checks them against its signature. Older builds put each helper at a new path, so every update asked again. The path is now fixed. An update that doesn't change the helper keeps your answers. An update that does may ask once more, because Apple doesn't document whether privacy checks accept the helper's identifier-only signing requirement (`io.github.bogdankim.encore.bridge`) for ad-hoc code. If it asks, click Allow. Switching back and forth between two encore versions swaps the helper each time and may ask each time. The first run after updating from an older build asks once, since earlier answers belong to the old paths. Set `ENCORE_INHERIT_TCC=1` to have the helper use your terminal's permissions instead.

## The screen

The screen has four parts:

- The player bar on top: previous, play/pause, next, shuffle, repeat; the now playing art, title, artist and album with a progress bar; volume, the AirPlay output, and the lyrics and Up Next toggles.
- The sidebar on the left: Search, the Library sections and your playlists. It folds to a 3-column icon rail under 90 columns or with `space b`.
- The content in the middle: Home, Recently Added, Artists, Albums, Songs, Favorites, a playlist, or an album or artist page.
- An optional right pane with lyrics or Up Next. It hides under 100 columns, where the lyrics button opens full-screen lyrics and the Up Next button opens Up Next as a drawer over the content.

Every control works with the mouse. Click to select, double-click to play or open, right-click for a menu with Play, Play Next, Add to Up Next, Go to Album, Go to Artist, Open Album in Apple Music, Open Artist in Apple Music and Favorite. The wheel scrolls the pane under the pointer. Click or drag the progress bar to seek, and click, drag or scroll the volume meter. Click a column header to sort and click it again to reverse. Drag a vertical rule to resize the sidebar or the right pane. Mouse buttons 4 and 5 go back and forward. Right-click the song name or the cover in the player bar, or the playing song at the top of Up Next, for a menu with Go to Album, Go to Artist and Favorite, plus Open in Music.app for an Apple Music song.

`?` opens a searchable list of every binding. `:` or `ctrl+k` opens the command palette, which runs any action by name. `space` is the leader: press it and a popup lists what can follow. The popup stays open until you press a key or `esc`.

A number before a motion repeats it: `5j` moves down five rows. Counts apply only to the navigation keys.

### Keys

These are the defaults from `internal/keymap/defaults.go`, grouped as in the help screen.

Navigation

| Keys | Action |
|---|---|
| `j` `down` / `k` `up` | down / up |
| `gg` `home` / `G` `end` | top / bottom |
| `ctrl+d` / `ctrl+u` | half page down / up |
| `pgdown` / `pgup` | page down / up |
| `h` `left` / `l` `right` | focus the pane to the left / right |
| `tab` / `shift+tab` | next / previous pane |
| `enter` | play the song, or open the album, artist or playlist |
| `esc` `backspace` | close the overlay or filter, or leave a detail page |
| `ctrl+o` / `ctrl+i` | back / forward in view history |
| `/` | filter the list as you type; `esc` clears it |
| `v` | switch between grid and list |
| `o` / `O` | sort by the next field / reverse the sort |

Playback

| Keys | Action |
|---|---|
| `p` `space space` | play / pause |
| `]` / `[` | next / previous track |
| `.` / `,` | seek +10 s / -10 s |
| `>` / `<` | seek +30 s / -30 s |
| `=` / `-` | volume +5 / -5 |
| `s` | shuffle |
| `P` | play the page: the album, playlist or artist shown, all songs on Songs and Favorites, the selected album in a grid |
| `S` | the same, shuffled |
| `r` | repeat: off, all, one |
| `f` | favorite the selected song |
| `F` | favorite the playing song |

Search

| Keys | Action |
|---|---|
| `space /` | search everything |
| `space s s` | search songs |
| `space s a l` | search albums |
| `space s a r` | search artists |
| `space s p` | search playlists |
| `space s m` | search Apple Music |

Panes

| Keys | Action |
|---|---|
| `space l` | lyrics pane |
| `space L` | full-screen lyrics |
| `space q` | Up Next pane |
| `space a` | AirPlay devices |
| `space b` | sidebar |

Go to

| Keys | Action |
|---|---|
| `space g h` | Home |
| `space g r` | Recently Added |
| `space g a r` | Artists |
| `space g a l` | Albums |
| `space g s` | Songs |
| `space g f` | Favorites |
| `space g p` | playlists in the sidebar |
| `space g n` | the playing album |
| `space o a l` | the album of the selected song or album in Apple Music |
| `space o a r` | the artist of the selected song, album or artist in Apple Music |
| `space o l l` | the album of the selected Apple Music song or album in your library |
| `space o l r` | the artist of the selected Apple Music song, album or artist in your library |
| `M` `space o m` | the selected Apple Music song, album or playlist in Music.app |

Queue

| Keys | Action |
|---|---|
| `a` | add the selection to the end of Up Next |
| `A` | play the selection next |
| `J` / `K` | move the row down / up, in the Up Next pane |
| `x` | remove the row from Up Next |

Lyrics, in the lyrics pane

| Keys | Action |
|---|---|
| `}` / `{` | offset +100 ms / -100 ms |

General

| Keys | Action |
|---|---|
| `m` | context menu for the selection |
| `?` `space ?` | help |
| `:` `ctrl+k` | command palette |
| `space ,` | last.fm setup |
| `ctrl+l` | redraw the screen and every image |
| `q` | quit; Music keeps playing |

In search, filter and the palette, typed characters go into the field. `enter` opens, `esc` closes, `tab` and `shift+tab` change the scope, and `down` `ctrl+j` `ctrl+n` and `up` `ctrl+k` `ctrl+p` move through results.

### Apple Music

The last search tab, Apple Music, searches the Apple Music catalog instead of your library, so you can find an artist you don't have and stream them. `space s m` opens search on that tab. In All, the last row offers the same search for what you typed. Results come in three groups: artists, albums and songs. `enter` on a song plays it, with the other songs in the list after it. An album or an artist opens its Apple Music page, laid out like the library's, with Play and Shuffle, the track list or the artist's top songs and albums. When the album or artist is also in your library, the page links to it ("In Your Library"). Songs from Apple Music can go in Up Next like any other, and the menu has Play, Play Next, Add to Up Next, Go to Album and Go to Artist. Favorite isn't there, because Music only favorites songs in the library.

You can go between the two. `space o a l` (Open Album in Apple Music) opens the album of the selected song or album from your library on Apple Music, with the song selected. `space o a r` (Open Artist in Apple Music) opens the artist's page; for a song that is its lead artist, so "Dua Lipa feat. DaBaby" opens Dua Lipa. Both are in the menu, and album and artist pages link to Apple Music too. encore finds the page with one search the first time and remembers the answer, or says "Not found in Apple Music". Once a song or album has been found, its artist needs no search. `space o l l` and `space o l r` (Open Album in Library, Open Artist in Library) go the other way, and the menu of an Apple Music item has them when your library has the album or the artist.

`M` (or `space o m`) opens the selected Apple Music song, album or playlist in Music.app. It is in the menu as Open in Music.app, in the header of an Apple Music album page, and in the footer on Home. A song opens on its album's page. It works whether or not your library has the item. encore can't add anything to your library: Music doesn't let another app do it, so the + button in Music.app does the adding. Library items, artists and stations have no Apple Music link to open. The footer says "Opening in Music.app".

The catalog comes from the public iTunes Search API, which needs no account but allows about 20 requests a minute, and one search takes three. encore waits until you stop typing for 350 ms before it asks, caches answers, and says "Apple Music search is busy, try again in a moment" when Apple refuses. The storefront follows your Music account's country. Artwork is cached in `~/Library/Caches/encore/art/`.

### Home

Home is Apple Music's Home page: Top Picks, Recently Played, Stations for You, the mixes Apple makes for you, Live Radio, and under "New" the New tab's best new songs, new albums and updated playlists. Each shelf is one row of tiles. `j` and `k` move between shelves and keep your column, `h` and `l` move along a shelf and scroll it, and each shelf remembers where you left it. A faint `‹` or `›` at a row's edge means there is more that way. The wheel scrolls shelves; shift+wheel or a sideways swipe scrolls the row under the pointer. Click `›` to page a row.

`enter` or a click plays a station or a song, and opens an album or an artist on its Apple Music page. The play badge on a hovered tile plays it instead, and an artist's badge plays the artist's station. `P` does what the badge does. A song plays with the rest of its shelf after it. Live stations say "Live" and the show on air.

encore can't start Apple's playlists, the personal mixes included. Music only plays them from its own window. So `enter` on a mix or an editorial playlist opens it in Music.app, which comes to the front on that playlist, and the footer says "Opening in Music.app". Hovering such a tile says "Opens in Music.app". `M` opens any album, song or playlist tile in Music.app the same way, including the playlists encore can play. When Music's cache happens to hold a playlist's songs, encore plays them itself.

encore doesn't talk to Apple Music's servers for this. It reads the Home page from Music's own cache (`~/Library/Caches/com.apple.Music`, read-only) and keeps a copy in `~/Library/Caches/encore/home/`, so Home is as fresh as the last time Music showed its Home page. The title line says how old it is ("updated 2 h ago"). If Music has never shown Home since its cache was cleared, the page asks you to open Home in Music once. While Home is on screen encore checks the cache every 10 seconds and reloads when it changed, keeping your place. When there is a page, encore starts on Home; otherwise on Recently Added.

While a station plays, the player bar shows the station's name where the album would be ("artist · Station Name"), and live radio shows "Live" instead of the elapsed time.

## Lyrics

`space l` opens the lyrics pane. When a song starts, Music downloads Apple Music's synced lyrics for it and keeps them in its cache for a while. encore reads them from there (`~/Library/Caches/com.apple.Music`, read-only, only the lyrics responses), so Apple Music songs usually get Apple's lyrics, with each word lighting up as it is sung when Apple has word timing. encore keeps a copy, since Music clears its cache. For everything else encore looks the track up on [LRCLIB](https://lrclib.net) by title, artist, album and duration, then by a cleaned-up title, then by search, and falls back to Music's own plain lyrics. If LRCLIB answers before Music has fetched Apple's lyrics, Apple's replace them within a few seconds. The lookup for the next track in the queue starts while the current one plays.

The current line is in the accent colour and stays at about 40% of the pane height. Click a line to seek to it. The wheel scrolls freely and the view returns to the current line 3 seconds later. Unsynced lyrics scroll with the track position.

If lines come early or late, press `}` or `{` in the pane to shift them by 100 ms. The offset is saved per track in `~/Library/Application Support/encore/lyrics-offsets.json`.

Results are cached in `~/Library/Caches/encore/lyrics/`. Synced lyrics stay forever. A plain-only result is looked up again after 7 days, in case synced lyrics appeared. A miss is retried after 24 hours, then every 7 days.

`space L` shows lyrics full screen with the art, progress and transport buttons. `esc` closes it.

## AirPlay

`space a`, or a click on the output name in the player bar, opens the picker. Rows work like checkboxes: `space` or a click adds a device to the outputs or removes it, and the last output can't be removed. `enter` or a right-click plays on that one device only. Each playing device has its own volume meter: click, drag or scroll it, or use `h` `l` and the arrow keys in steps of 5.

Music renumbers AirPlay devices, so a device ID from one listing can point at a different device a minute later. encore tracks devices by their persistent ID and looks the current numeric ID up right before each call. If Music still reports "not found", encore lists the devices again and retries once.

## last.fm

Scrobbling needs your own last.fm API key; last.fm doesn't let an app register one for you. Open the setup with `space ,`, or type `last.fm` in the palette. It has three steps:

1. Create an API account at <https://www.last.fm/api/account/create>. Any application name works. Leave the callback URL empty.
2. Paste the API key and the shared secret into encore.
3. encore opens last.fm in your browser. Allow access there. encore checks every 2.5 seconds and finishes on its own once you approve, or gives up after 10 minutes.

The key, secret and session key go to the macOS Keychain as a generic password with service `encore` and account `lastfm`. If the Keychain can't be used, they go to `~/Library/Application Support/encore/secrets.json` with mode 0600. `ENCORE_LASTFM_KEY` and `ENCORE_LASTFM_SECRET` override the stored key and secret.

encore sends "now playing" when a track starts and scrobbles it once you have heard half of it or 4 minutes, whichever comes first. Tracks of 30 seconds or less are skipped. Scrobbles are written to `~/Library/Application Support/encore/scrobbles.jsonl` before they are sent, so nothing is lost offline or on quit. Failed sends retry with a backoff from 30 seconds up to 30 minutes, in batches of up to 50. last.fm rejects plays older than 14 days, so encore drops those. The player bar shows a dot only when something is wrong.

Set `enabled = false` under `[lastfm]` to turn scrobbling off.

## Configuration

encore reads `~/.config/encore/config.toml`, or `$XDG_CONFIG_HOME/encore/config.toml` when that variable is set. The file is optional and encore doesn't create it. Unknown keys and bad values show as a warning in the footer and fall back to the default; the rest of the file still applies.

This is the full file with every default:

```toml
# Palette: "encore" (follows the terminal's light/dark background), "light",
# "mono", "auto", or another built-in name.
theme = "encore"

# Accent override as "#RRGGBB". Empty keeps the palette's accent.
accent = ""

# Media and UI glyphs: "auto", "nerd" (Nerd Font symbols) or "unicode".
icons = "auto"

# Album art: "auto", "iip" (iTerm2/WezTerm), "kitty", "sixel", "blocks" (half
# blocks) or "off". Inside tmux, auto asks tmux which terminal it runs in.
art = "auto"

sidebar_width = 22
mouse = true

[lyrics]
enabled = true
apple = true    # Apple Music's synced lyrics, from Music's own cache
lrclib = true   # look lyrics up on lrclib.net; off = Music's own lyrics only
lead_ms = 200   # highlight each line slightly before its timestamp
word_lead_ms = 100  # start filling each timed word this much early

[lastfm]
enabled = true  # run ":lastfm" in encore to connect an account

# Override palette tokens: fg, dim, faint, accent, sel, hover, warn.
[colors]
# accent = "#FA586A"
# sel = "#26262B"

# Key bindings. Each action takes a list of key sequences; <leader> expands
# to the leader key. Press ? in encore to see every action name.
[keys]
leader = "space"
# "play-pause" = ["p", "<leader><leader>"]
# "search.songs" = ["<leader>ss"]
```

Notes on the keys:

- `theme`: `encore` and `auto` pick the dark or light palette from the terminal background. `dark` and `light` force one. `mono` uses white as the accent. `NO_COLOR=1` turns colour off.
- `accent` and `[colors]` take `#RRGGBB` or `#RGB`. The tokens are `fg`, `dim`, `faint`, `accent`, `sel`, `hover`, `warn` and `shadow`, which is the overlay background. `fg` defaults to the terminal's own foreground, and encore never paints the full-screen background, so a translucent terminal stays translucent.
- `icons = "auto"` uses Nerd Font symbols on WezTerm, which bundles them, and plain Unicode elsewhere. Set `nerd` if your font has them.
- `art = "auto"` picks iTerm2 images on WezTerm and iTerm2, kitty graphics on kitty and Ghostty, and half blocks everywhere else. Inside tmux it asks tmux which terminal the session is attached to; see [tmux](#tmux). `iip`, `kitty`, `sixel` and `blocks` skip the detection.
- `sidebar_width` is in columns, from 14 to 40. Outside that range encore warns and uses 22.
- `mouse = false` turns mouse reporting off.
- `lyrics.lead_ms` highlights each synced lyric line this many milliseconds before its timestamp, in the pane and in full-screen lyrics. The default is 200. It takes -5000 to 5000.
- `lyrics.word_lead_ms` starts filling each word of word-timed lyrics (Apple's, and LRC with `<mm:ss.xx>` word tags) this many milliseconds before its timestamp, to make up for the time a frame takes to reach the screen. The default is 100. It takes -5000 to 5000.

### tmux

Inside tmux, `art = "auto"` asks tmux for the attached terminal (`#{client_termtype}`, then `#{client_termname}`, then `TERM_PROGRAM` from the tmux environment) and picks:

- WezTerm and iTerm2: iTerm2 images through tmux passthrough, for the player bar, page headers, full-screen lyrics and grid tiles alike. If tmux refuses passthrough and draws sixel on that terminal (`sixel` is in `#{client_termfeatures}`), every image is sixel, as with `art = "sixel"`.
- kitty and Ghostty: kitty graphics through tmux passthrough.
- Anything else: half blocks.

Passthrough needs `allow-passthrough`. If it is off, encore turns it on for its own pane only (`set-option -p`, tmux 3.3 or later) and puts the pane back as it was when it quits. To allow it everywhere instead, add this to `~/.tmux.conf`:

```
set -g allow-passthrough on
```

tmux doesn't keep passthrough images, so every time it redraws the pane they are gone. encore installs a few tmux hooks on its own session, window and pane (`session-window-changed`, `client-attached`, `client-session-changed`, `client-resized`, `window-layout-changed`, `pane-mode-changed`). Each one sends encore `SIGUSR1`, and encore draws the images again 100 ms later, once tmux has finished. It removes exactly those hooks when it quits, and the next run removes any that a crashed run left behind. `focus-events` doesn't need to be on. Closing a `display-popup` or menu has no hook, so art under it stays blank until something redraws it, or until you press `ctrl+l`.

encore only sends passthrough images while its pane is on screen. It asks tmux (`#{window_active}`, `#{session_attached}`, zoom and copy mode) after every hook and every 2 seconds. With `allow-passthrough all` tmux would otherwise pass images from a hidden window onto the one you are looking at.

Earlier builds drew the player bar and page headers as sixel inside tmux. tmux drops a sixel image whenever a line under it is erased or scrolled, then redraws the whole pane and sends every image again. Each track change redrew every tile on the page, and each lyric line in full-screen lyrics sent the art again. tmux never stores passthrough images, so none of that happens to them.

`art = "sixel"` keeps every image sixel. tmux keeps at most nine sixel images across the whole server and drops the oldest when a tenth arrives, so in that mode each row of the album grid is one image. A row shares one 256-colour palette across its tiles.

`ENCORE_OUTER_TERM=WezTerm` (or `iTerm2`, `kitty`, `ghostty`) overrides the terminal tmux reports. It is for testing in terminals tmux can't name, such as `tools/snap`.

### Key remapping

`[keys]` maps an action name to a list of sequences, and the list replaces the defaults for that action. An empty list unbinds it. `?` in encore shows the action names; they are also in `internal/keymap/action.go`. A unique suffix works too, so `"play-pause"` means `player.playPause`.

```toml
[keys]
leader = "space"
"player.next" = ["n"]
"player.prev" = ["N"]
"search.all" = ["<C-f>", "<leader>/"]
"player.shuffle" = []
```

A sequence can mix these forms:

- plain characters, where `gg` is g then g
- key names separated by spaces, like `space s s`
- vim brackets: `<leader>`, `<C-d>`, `<S-Tab>`, `<CR>`
- Bubble Tea names: `ctrl+d`, `shift+tab`, `pgdown`

The leader must be a single key. A sequence that is also the start of a longer one fires after 1 second without another key. Conflicts, unknown actions and bad sequences become warnings; the first one shows in the footer at startup.

## How it works

encore is a Go program built on Bubble Tea v2. Everything that needs a macOS API lives in `encore-bridge`, a Swift process that encore starts and talks to over stdin and stdout with one JSON object per line. The bridge sends raw Apple Events to Music from one serial queue. Each one takes about 8 ms; running `osascript` for the same command takes about 200 ms. Music's `playerInfo` notifications reach encore as events, and the bridge polls once per second while a track plays to correct the position. If three calls in a row time out, encore restarts the bridge.

The library comes from iTunesLibrary.framework as one snapshot of tracks and playlists, plus one Apple Event for favorites. encore keeps it in memory, saves it to `~/Library/Caches/encore/library.cache`, and draws the next start from that cache before the fresh snapshot arrives. The bridge checks for library changes every 60 seconds. Search runs in memory over precomputed keys: on a generated 62,000-track library on an M3 Pro, a query takes between 0.4 and 5.2 ms. All terms must match, title matches rank above artist and album, and a subsequence match is the fallback when nothing matches exactly.

Music has no scriptable Up Next. `play track` plays one song and stops, and `play playlist` gives a real context that next, previous and the media keys follow. So encore keeps one ordinary playlist named `encore`, fills it with the tracks you asked for, and plays it. Music copies a playlist when it starts playing, so encore can rewrite it at any time without breaking what plays. Filling 200 tracks takes about 0.9 s, and audio starts about 0.9 s after `play`. encore holds the real queue, shuffle order and Up Next itself and refills the playlist at the next track change when you edit them. It sends up to 300 tracks at a time and sends the next slice when playback reaches the end. Playing a playlist from its first track with shuffle off uses Music's own `play playlist` instead.

Album art goes out in one of four ways. On WezTerm and iTerm2 encore draws iTerm2 inline images over blank cells and redraws them after each frame where the renderer touched those cells. Inside tmux they go through passthrough, each one carrying its own cursor move to the pane's position on the terminal. When Bubble Tea scrolls rows that hold an image (a lyric line advancing next to the art), encore drops the scroll and repaints those rows' text around the image, so the image is never sent again for it. With `art = "sixel"` encore's own encoder reduces each image to 256 colours with median cut and light dithering, and tmux stores the result and redraws it. On kitty and Ghostty it uses kitty graphics with Unicode placeholders, so the image is text and the renderer diffs it like any other cell. Everywhere else it draws half blocks in truecolor. Grid tiles show as half blocks while you scroll fast. When the track changes, the player bar keeps the last cover for up to 4 seconds while the new one loads. Music has no cover for a station song until it has fetched it, so encore asks again a second later, then less often, until it comes. To see what happens with covers on your Mac, run `ENCORE_DEBUG_ART=1 encore` and read `~/Library/Logs/encore/art.log`.

Files:

| Path | Contents |
|---|---|
| `~/.config/encore/config.toml` | config |
| `~/Library/Caches/encore/` | library cache, lyrics, the Home page copy, the extracted bridge |
| `~/Library/Application Support/encore/` | scrobble queue, lyric offsets, secrets fallback |

## Known limitations

- The `encore` playlist is a normal playlist. It shows up in Music's sidebar and syncs to your other devices through iCloud Music Library. Its contents change every time you play something from encore.
- Apple Music streams to one device per account at a time. If the account is streaming on another device, Music on this Mac won't play subscription tracks, and encore can't change that.
- Some Music sessions ignore pause sent as an Apple Event while still reporting `playing`. The bridge reads the state back after 400 ms and 1.2 s and resends pause or play through the private MediaRemote framework if nothing changed. That path can break with a macOS update.
- The kitty graphics path is written to the protocol but hasn't been tested in a real kitty window, in or out of tmux. Set `art = "blocks"` if art looks wrong there.
- With `art = "sixel"` in tmux, art costs more than iTerm2 images: 3 to 6 times the bytes for the same picture, and tmux sends every stored image again whenever it redraws the pane. Another program showing sixel in the same tmux server can push the player bar or header image out of tmux's nine slots; `ctrl+l` brings it back.
- The Apple Music tab uses the iTunes Search API, not MusicKit, which needs a developer token. So it has no artist photos, no playlists or radio, and no "add to library". Songs Apple doesn't stream in your country show dimmed and don't play.
- encore can't add to your library: Music doesn't let another app do it. Apple Music songs, albums and playlists open in Music.app (`M`), and you add them there with its + button.
- Home is a copy of what Music last showed. Shelves don't change until you open Home in Music again, though live stations update the show on air.
- Apple's playlists and mixes open in Music.app instead of playing from encore, and Music comes to the front when they do.
- macOS only, arm64 only.

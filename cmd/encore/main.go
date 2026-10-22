// Command encore is a fast terminal client for Apple Music on macOS.
//
//	encore            control Music.app
//	encore --demo     run against a built-in demo library
//	encore --version
package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/config"
	"github.com/bogdankim/encore/internal/home"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/lastfm"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/lyrics"
	"github.com/bogdankim/encore/internal/musiccache"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
	"github.com/bogdankim/encore/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	demo := flag.Bool("demo", false, "use a built-in demo library instead of Music.app")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("encore", version)
		return
	}
	if err := run(*demo); err != nil {
		fmt.Fprintln(os.Stderr, "encore:", err)
		os.Exit(1)
	}
}

func run(demo bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mem := newMemTuner() // sets the soft memory limit before the library loads
	go mem.run(ctx)

	cfg, warns, err := config.Load()
	if err != nil {
		warns = append(warns, err.Error())
	}
	keys := keymap.New()
	warns = append(warns, keys.Apply(cfg.Keys.Bindings, cfg.Keys.Leader)...)

	var (
		client    bridge.Client
		lib       *library.Library
		loading   bool
		cachePath string
		svc       ui.Services
		artFetch  ui.ArtFetch
		artDir    string
		homePage  = func() *home.Page { return nil }
	)
	if demo {
		fake := bridge.NewFake(1)
		fake.SetAutoReconnect(cfg.AirPlay.AutoReconnect)
		if os.Getenv("ENCORE_DEMO_ART_FAULTS") == "1" {
			// artwork as Music gives it at its worst: slow, sometimes
			// failing, and none for a station song's first seconds
			fake.SetArtFaults(bridge.ArtFaults{Delay: 300 * time.Millisecond, StationLate: 3 * time.Second, FailEvery: 5})
		}
		if os.Getenv("ENCORE_DEMO_IDLE") == "1" {
			// nothing queued, as when Music has just started
			fake.SetIdle()
		}
		snap, err := fake.Snapshot(ctx)
		if err != nil {
			return err
		}
		client, lib = fake, library.Build(snap)
		svc.FakeLRC = fake.FakeLRC
		dc := newDemoCatalog(lib, fake)
		if snap, err = fake.Snapshot(ctx); err != nil { // now with catalog ids
			return err
		}
		lib = library.Build(snap)
		svc.Catalog = dc
		svc.Home = newDemoHome(dc)
		homePage = preloadHome(ctx, svc.Home)
		svc.OpenMusic = func(context.Context, string, string) error { return nil }
		artFetch = demoArt
	} else {
		svc.Home = home.New(musiccache.DefaultDir(), home.DefaultStoreDir())
		homePage = preloadHome(ctx, svc.Home)
		proc, err := bridge.Start(ctx, bridge.Options{CacheDir: config.CacheDir(), NoAirPlayReconnect: !cfg.AirPlay.AutoReconnect})
		if err != nil {
			return err
		}
		client = proc
		cachePath = filepath.Join(config.CacheDir(), "library.cache")
		lib, loading = loadCachedLibrary(cachePath)
		cat := newLazyCatalog()
		svc.Catalog = cat
		svc.OpenMusic = openInMusic(func() string { return cat.get().Country })
		artDir = filepath.Join(config.CacheDir(), "art")
	}
	defer client.Close()

	if cfg.Lyrics.Enabled {
		opts := lyrics.Options{CacheDir: lyrics.DefaultCacheDir(), MusicLyrics: client.Lyrics, Demo: svc.FakeLRC}
		if cfg.Lyrics.LRCLIB && !demo {
			opts.LRCLIB = &lyrics.Client{}
		}
		if cfg.Lyrics.Apple && !demo {
			opts.Apple = lyrics.NewAppleCache(lyrics.DefaultAppleCacheDir())
		}
		svc.Lyrics = lyrics.NewService(opts)
		defer svc.Lyrics.Close()
	}
	svc.LyricsLead = time.Duration(cfg.Lyrics.LeadMS) * time.Millisecond
	svc.WordLead = time.Duration(cfg.Lyrics.WordLeadMS) * time.Millisecond
	svc.Offsets = lyrics.LoadOffsets(lyrics.DefaultOffsetsPath())
	defer svc.Offsets.Flush()
	if !demo {
		// Credentials come from the Keychain, which can take seconds (or
		// wait on a prompt), so they load after the first frame; see below.
		svc.Scrobbler = lastfm.NewScrobbler(lastfm.Credentials{}, lastfm.DefaultQueuePath(), nil,
			lastfm.WithEnabled(cfg.LastFM.Enabled))
		defer svc.Scrobbler.Close()
	}

	var current atomic.Pointer[library.Library]
	current.Store(lib)
	pl := player.New(client, func(pid string) *library.Track { return current.Load().Track(pid) })
	defer pl.Close()

	artChoice, restore, err := art.Setup(cfg.Art, os.Getenv, nil)
	if err != nil {
		warns = append(warns, err.Error())
	}
	artR := art.NewRenderer(art.Options{
		Protocol:    artChoice.Protocol,
		Passthrough: artChoice.Passthrough,
		Tmux:        artChoice.Tmux,
		TilePrefix:  ui.TileKeyPrefix,
	})
	stopWatch := func() {}
	if artChoice.Tmux && artR.Fragile() {
		stopWatch = art.WatchTmux(artR, nil, os.Getenv("TMUX_PANE"))
	}
	restoreTmux := sync.OnceFunc(func() {
		stopWatch()
		restore()
	})
	defer restoreTmux()
	out := art.NewOutput(os.Stdout, artR)
	defer out.Close()

	ref := newRefresher(client, cachePath, &current)
	status := ""
	if len(warns) > 0 {
		status = "config: " + warns[0]
	}
	app := ui.New(ui.Options{
		Client:         client,
		Player:         pl,
		Library:        lib,
		Loading:        loading,
		Keys:           keys,
		Theme:          themeFor(cfg, theme.GlyphsFor(cfg.Icons, termProgram(artChoice)), os.Getenv("NO_COLOR") != ""),
		SidebarWidth:   cfg.SidebarWidth,
		NoMouse:        !cfg.Mouse,
		Status:         status,
		RefreshLibrary: ref.trigger,
		Services:       svc,
		Art:            artR,
		ArtFetch:       artFetch,
		ArtCacheDir:    artDir,
		HomePage:       homePage(),
		Start:          cfg.Start,
		FrameCap:       time.Second / 30, // while a key or the wheel repeats
		OnInput:        mem.Touch,
	})
	prog := tea.NewProgram(app, tea.WithOutput(out))
	ref.send = prog.Send
	stopHangup := onHangup(restoreTmux, func() {
		prog.Quit()
		// The terminal is gone; don't let a stuck shutdown keep the process.
		time.AfterFunc(5*time.Second, func() { os.Exit(1) })
	})
	defer stopHangup()

	if svc.Scrobbler != nil {
		go loadCredentials(svc.Scrobbler, lastfm.LoadCredentials, prog.Send)
	}
	go forwardEvents(client, pl, svc.Scrobbler, prog.Send, ref)
	go ref.run(ctx, !demo)

	_, err = prog.Run()
	return err
}

// termProgram is TERM_PROGRAM, or inside tmux (where it is "tmux") the
// terminal tmux runs in, when art detection found it.
func termProgram(ch art.Choice) string {
	if ch.Outer == art.OuterWezTerm {
		return "WezTerm"
	}
	return os.Getenv("TERM_PROGRAM")
}

// loadCredentials reads the stored last.fm credentials off the UI's path and
// hands them to the scrobbler. A failure shows in the footer.
func loadCredentials(sc *lastfm.Scrobbler, load func() (lastfm.Credentials, error), send func(tea.Msg)) {
	creds, err := load()
	sc.InitCredentials(creds)
	if err != nil {
		send(ui.StatusMsg{Text: "last.fm: " + err.Error(), Err: true})
	}
}

// forwardEvents feeds bridge events to the player directly (so playback
// state never waits on the UI) and passes the ones the UI shows along.
func forwardEvents(client bridge.Client, pl *player.Player, sc *lastfm.Scrobbler, send func(tea.Msg), ref *refresher) {
	for ev := range client.Events() {
		pl.Observe(ev)
		if pe, ok := ev.(bridge.PlayerEvent); ok && sc != nil {
			sc.Observe(pe.NowPlaying, trackMeta(pe.NowPlaying.Track))
		}
		switch ev.(type) {
		case bridge.LibraryEvent:
			ref.trigger()
		case bridge.AirPlayEvent, bridge.AirPlayRepairedEvent, bridge.MusicEvent, bridge.ExitEvent, bridge.PermissionsEvent:
			send(ui.BridgeMsg{Event: ev})
		}
	}
}

// trackMeta is what last.fm needs to know about the playing track.
func trackMeta(t *bridge.CurrentTrack) lastfm.TrackMeta {
	if t == nil {
		return lastfm.TrackMeta{}
	}
	return lastfm.TrackMeta{
		Artist: t.Artist, Title: t.Name, Album: t.Album, AlbumArtist: t.AlbumArtist,
		Duration: time.Duration(t.Duration * float64(time.Second)),
	}
}

// loadCachedLibrary returns the library from the last run for an instant
// first frame, or an empty one (loading) when there is no usable cache.
func loadCachedLibrary(path string) (*library.Library, bool) {
	snap, err := library.LoadCache(path)
	if err != nil {
		return library.Build(&bridge.Snapshot{}), true
	}
	return library.Build(snap), false
}

// themeFor resolves the configured palette for a dark or light terminal.
// "encore" and "auto" follow the terminal background.
func themeFor(cfg config.Config, g theme.Glyphs, noColor bool) func(dark bool) *theme.Theme {
	built := map[bool]*theme.Theme{}
	return func(dark bool) *theme.Theme {
		if t, ok := built[dark]; ok {
			return t
		}
		name := cfg.Theme
		switch name {
		case "", "encore", "auto":
			name = "auto"
		case "light":
			name = "encore-light"
		case "dark":
			name = "encore"
		}
		overrides := maps.Clone(cfg.Colors)
		if overrides == nil {
			overrides = map[string]string{}
		}
		if cfg.Accent != "" {
			overrides["accent"] = cfg.Accent
		}
		p, _ := theme.Lookup(name, dark).WithOverrides(overrides)
		t := theme.New(p, g, noColor)
		built[dark] = t
		return t
	}
}

package bridge

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Fake is an in-process Client with a synthetic library (about 6,000 tracks,
// deterministic from the seed) and a simulated Music.app player. It backs
// `encore --demo`, UI development, screenshot tests and unit tests.
//
// The player starts paused one minute into the most recently added album.
// Position advances with the clock while playing, tracks end and advance on
// their own, and a PlayerEvent is pushed after every change and once a second
// while playing, like the real bridge.
type Fake struct {
	now    func() time.Time
	events *eventQueue
	artDir string
	stop   chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	lib     *fakeLibrary
	byPID   map[string]int // track pid → index in lib.tracks
	plByPID map[string]int // playlist pid → index in lib.playlists
	rng     *rand.Rand     // shuffle order

	state    PlayerState
	queue    []string
	qi       int
	pos      float64   // seconds into the current track at posAt
	posAt    time.Time // when pos was taken
	volume   int
	shuffle  bool
	repeat   RepeatMode
	devices  []AirPlayDevice
	lastEmit time.Time
	// noReconnect is auto-reconnect off (see DropAirPlayRoute).
	noReconnect bool

	// Catalog songs played by PlayStore live outside the library, under
	// temporary pids, as in Music.app.
	catalog map[string]FakeCatalogSong // store id → metadata from RegisterCatalog
	extra   map[string]*SnapshotTrack  // temporary pid → the catalog song playing under it
	tempSeq uint64

	// The station PlayStation started, "" when none is playing (see fakestation.go).
	station  string
	live     bool
	stationN uint64
	songAt   time.Time // when the station's current song started

	faults   ArtFaults
	artCalls int
}

// SetIdle puts the Fake in the state Music.app has when nothing is queued:
// stopped, no current track. Play starts nothing until something is queued.
func (f *Fake) SetIdle() {
	f.mu.Lock()
	f.queue, f.state, f.qi, f.pos = nil, Stopped, 0, 0
	f.mu.Unlock()
}

// ArtFaults makes the Fake's artwork answers behave like Music's at its
// worst, to reproduce missing covers (see SetArtFaults).
type ArtFaults struct {
	// Delay is how long every artwork answer takes.
	Delay time.Duration
	// StationLate is how long after a station song starts Music still
	// reports it has no artwork, while it fetches it.
	StationLate time.Duration
	// FailEvery fails every n-th artwork call, as a timeout would; 0 never.
	FailEvery int
}

// SetArtFaults sets how artwork answers misbehave; the zero value is none.
func (f *Fake) SetArtFaults(af ArtFaults) {
	f.mu.Lock()
	f.faults = af
	f.mu.Unlock()
}

// FakeCatalogSong is what the Fake reports for a catalog song played by store
// ID. Register songs with RegisterCatalog; other ids play as "Catalog song
// <id>".
type FakeCatalogSong struct {
	StoreID     string
	Name        string
	Artist      string
	Album       string
	AlbumArtist string  // "" means Artist
	Duration    float64 // seconds; 0 picks 2:30–5:00 from the id
}

// NewFake returns a Fake on the wall clock. A background ticker advances
// playback and emits the per-second PlayerEvent until Close.
func NewFake(seed int64) *Fake {
	f := newFake(seed, time.Now)
	f.wg.Add(1)
	go f.run()
	return f
}

// NewFakeWithClock returns a Fake that reads time from now and has no
// background ticker: call Tick to emit the periodic and track-change events.
// Reads (State, Snapshot, …) always reflect the clock.
func NewFakeWithClock(seed int64, now func() time.Time) *Fake {
	return newFake(seed, now)
}

func newFake(seed int64, now func() time.Time) *Fake {
	lib := generateLibrary(seed)
	f := &Fake{
		now:     now,
		events:  newEventQueue(64),
		artDir:  filepath.Join(os.TempDir(), "encore-demo-art"),
		stop:    make(chan struct{}),
		lib:     lib,
		byPID:   make(map[string]int, len(lib.tracks)),
		plByPID: make(map[string]int, len(lib.playlists)),
		rng:     rand.New(rand.NewPCG(uint64(seed), 1)),
		volume:  40,
		repeat:  RepeatOff,
		state:   Stopped,
		devices: []AirPlayDevice{
			{ID: 41, PID: "6F2C1A0B9D3E4F10", Name: "MacBook Pro", Kind: "computer", Selected: true, Active: true, Available: true, Volume: 100},
			{ID: 29274, PID: "A4D1E2F3B5C60718", Name: "Living Room", Kind: "homepod", Available: true, Volume: 35},
			{ID: 29301, PID: "C0FFEE1234ABCD56", Name: "Kitchen", Kind: "speaker", Available: true, Volume: 50},
			{ID: 30117, PID: "7B3E9A2D4C1F8E60", Name: "Bedroom TV", Kind: "tv", Volume: 20},
		},
	}
	for i, t := range lib.tracks {
		f.byPID[t.PID] = i
	}
	for i, p := range lib.playlists {
		f.plByPID[p.PID] = i
	}
	f.posAt = now()
	if len(lib.start) > 0 {
		f.queue = slices.Clone(lib.start)
		f.state, f.pos = Paused, 62.0
	}
	return f
}

func (f *Fake) run() {
	defer f.wg.Done()
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			f.Tick()
		case <-f.stop:
			return
		}
	}
}

// Tick advances playback to the clock and emits a PlayerEvent when the track
// changed or a second has passed since the last one while playing.
func (f *Fake) Tick() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	now := f.now()
	if f.advance(now) || (f.state == Playing && now.Sub(f.lastEmit) >= time.Second) {
		f.emit(now)
	}
}

// do runs fn with the player advanced to now, under the lock. A PlayerEvent
// follows when advancing changed the track, or when notify is set and fn
// succeeded.
func (f *Fake) do(ctx context.Context, notify bool, fn func(now time.Time) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	now := f.now()
	changed := f.advance(now)
	err := fn(now)
	if changed || (notify && err == nil) {
		f.emit(now)
	}
	return err
}

func (f *Fake) emit(now time.Time) {
	f.events.push(PlayerEvent{NowPlaying: f.nowPlaying(now)})
	f.lastEmit = now
}

func (f *Fake) current() *SnapshotTrack {
	if f.qi < 0 || f.qi >= len(f.queue) {
		return nil
	}
	pid := f.queue[f.qi]
	if t, ok := f.extra[pid]; ok {
		return t
	}
	return &f.lib.tracks[f.byPID[pid]]
}

func (f *Fake) position(now time.Time) float64 {
	if f.state == Playing {
		return f.pos + now.Sub(f.posAt).Seconds()
	}
	return f.pos
}

func (f *Fake) nowPlaying(now time.Time) NowPlaying {
	np := NowPlaying{State: f.state, At: now.UnixMilli(), Volume: f.volume, Shuffle: f.shuffle, Repeat: f.repeat}
	if t := f.current(); t != nil && f.state != Stopped {
		np.Position = math.Round(f.position(now)*1000) / 1000
		np.Track = &CurrentTrack{
			PID: t.PID, Name: t.Title, Artist: t.Artist, Album: t.Album, AlbumArtist: t.AlbumArtist,
			Duration: float64(t.Duration) / 1000, Favorited: t.Favorited, Disliked: t.Disliked,
		}
		if f.live {
			// Live radio has no timeline: Music reports 0 and no duration.
			np.Position, np.Track.Duration = 0, 0
		}
	}
	return np
}

// advance moves playback to now, finishing tracks along the way. It reports
// whether the track or state changed.
func (f *Fake) advance(now time.Time) bool {
	if f.state != Playing {
		return false
	}
	cur := f.position(now)
	changed := false
	for {
		t := f.current()
		if t == nil {
			f.halt()
			return true
		}
		dur := float64(t.Duration) / 1000
		if cur < dur {
			break
		}
		changed = true
		if f.repeat == RepeatOne && f.station == "" {
			n := math.Floor(cur / dur)
			t.Plays += int(n)
			t.LastPlayed = now.Unix()
			cur -= n * dur
			continue
		}
		cur -= dur
		t.Plays++
		t.LastPlayed = now.Add(-time.Duration(cur * float64(time.Second))).Unix()
		if !f.step() {
			f.halt()
			return true
		}
	}
	f.pos, f.posAt = cur, now
	return changed
}

// step moves to the next playable track, wrapping with repeat all. It returns
// false at the end of the queue.
func (f *Fake) step() bool {
	if f.station != "" {
		f.nextStationSong()
		return true
	}
	for range len(f.queue) {
		f.qi++
		if f.qi >= len(f.queue) {
			if f.repeat != RepeatAll {
				return false
			}
			f.qi = 0
		}
		if f.current().Available {
			return true
		}
	}
	return false
}

// halt stops like Music.app at the end of a queue: no current track; Play
// starts the queue over.
func (f *Fake) halt() {
	f.state, f.qi, f.pos = Stopped, 0, 0
}

// begin starts the track at qi from 0. Unavailable tracks "play" and stop at
// once, as they do in Music.app.
func (f *Fake) begin(now time.Time) {
	f.pos, f.posAt = 0, now
	if t := f.current(); t == nil || !t.Available {
		f.state = Stopped
		return
	}
	f.state = Playing
}

func notFound(what, pid string) error {
	return &Error{Code: ErrNotFound, Message: fmt.Sprintf("Can’t get %s id %s.", what, pid)}
}

func (f *Fake) track(pid string) (*SnapshotTrack, error) {
	if t, ok := f.extra[pid]; ok {
		return t, nil
	}
	i, ok := f.byPID[pid]
	if !ok {
		return nil, notFound("track", pid)
	}
	return &f.lib.tracks[i], nil
}

// Hello reports a running, authorized Music.app.
func (f *Fake) Hello(ctx context.Context) (Hello, error) {
	err := f.do(ctx, false, func(time.Time) error { return nil })
	return Hello{Version: "1", MusicRunning: true, Automation: "granted", Library: "granted"}, err
}

// Snapshot returns a copy of the library with current favorites and play
// counts.
func (f *Fake) Snapshot(ctx context.Context) (*Snapshot, error) {
	var s *Snapshot
	err := f.do(ctx, false, func(now time.Time) error {
		s = &Snapshot{GeneratedAt: now.Unix(), Tracks: slices.Clone(f.lib.tracks), Playlists: slices.Clone(f.lib.playlists)}
		for i := range s.Playlists {
			s.Playlists[i].Items = slices.Clone(s.Playlists[i].Items)
		}
		favs := []string{}
		for _, t := range f.lib.tracks {
			if t.InLibrary && t.Favorited {
				favs = append(favs, t.PID)
			}
		}
		s.Playlists[f.lib.favorites].Items = favs
		return nil
	})
	return s, err
}

func (f *Fake) State(ctx context.Context) (NowPlaying, error) {
	var np NowPlaying
	err := f.do(ctx, false, func(now time.Time) error {
		np = f.nowPlaying(now)
		return nil
	})
	return np, err
}

func (f *Fake) PlayPause(ctx context.Context) error {
	return f.do(ctx, true, func(now time.Time) error {
		if f.state == Playing {
			f.pause(now)
		} else {
			f.play(now)
		}
		return nil
	})
}

func (f *Fake) Play(ctx context.Context) error {
	return f.do(ctx, true, func(now time.Time) error {
		f.play(now)
		return nil
	})
}

func (f *Fake) Pause(ctx context.Context) error {
	return f.do(ctx, true, func(now time.Time) error {
		f.pause(now)
		return nil
	})
}

func (f *Fake) play(now time.Time) {
	switch f.state {
	case Paused:
		f.state, f.posAt = Playing, now
	case Stopped:
		f.begin(now)
	}
}

func (f *Fake) pause(now time.Time) {
	if f.state == Playing {
		f.pos, f.posAt, f.state = f.position(now), now, Paused
	}
}

func (f *Fake) Next(ctx context.Context) error {
	return f.do(ctx, true, func(now time.Time) error {
		if f.state == Stopped || f.current() == nil {
			return nil
		}
		if !f.step() {
			f.halt()
			return nil
		}
		f.pos, f.posAt = 0, now
		return nil
	})
}

// Previous restarts the track after 3 s, otherwise goes back one.
func (f *Fake) Previous(ctx context.Context) error {
	return f.do(ctx, true, func(now time.Time) error {
		if f.state == Stopped || f.current() == nil {
			return nil
		}
		if f.position(now) <= 3 && f.qi > 0 && f.station == "" {
			f.qi--
		}
		f.pos, f.posAt = 0, now
		return nil
	})
}

func (f *Fake) Seek(ctx context.Context, seconds float64) error {
	return f.do(ctx, true, func(now time.Time) error {
		t := f.current()
		if t == nil || f.state == Stopped || f.live {
			return nil
		}
		f.pos, f.posAt = min(max(seconds, 0), float64(t.Duration)/1000), now
		f.advance(now)
		return nil
	})
}

func (f *Fake) SetVolume(ctx context.Context, volume int) error {
	return f.do(ctx, true, func(time.Time) error {
		f.volume = min(max(volume, 0), 100)
		return nil
	})
}

func (f *Fake) SetRepeat(ctx context.Context, mode RepeatMode) error {
	return f.do(ctx, true, func(time.Time) error {
		switch mode {
		case RepeatOff, RepeatOne, RepeatAll:
			f.repeat = mode
			return nil
		}
		return &Error{Code: 1, Message: fmt.Sprintf("bad repeat mode %q", mode)}
	})
}

func (f *Fake) SetShuffle(ctx context.Context, enabled bool) error {
	return f.do(ctx, true, func(time.Time) error {
		f.shuffle = enabled
		return nil
	})
}

// PlayTracks plays pids in order. Like the real bridge, a pid that isn't in the
// library must be mapped by containers to a playlist that holds it.
func (f *Fake) PlayTracks(ctx context.Context, pids []string, containers map[string]string) error {
	return f.do(ctx, true, func(now time.Time) error {
		if len(pids) == 0 {
			return &Error{Code: 1, Message: "pids is empty"}
		}
		for _, pid := range pids {
			t, err := f.track(pid)
			if err != nil {
				return err
			}
			if t.InLibrary {
				continue
			}
			pl, ok := f.plByPID[containers[pid]]
			if !ok || !slices.Contains(f.lib.playlists[pl].Items, pid) {
				return notFound("track", pid)
			}
		}
		f.leaveStation()
		f.queue, f.qi, f.extra = slices.Clone(pids), 0, nil
		f.begin(now)
		return nil
	})
}

// SetStoreIDs gives the library's songs Apple Music catalog ids, as iTunesLibrary
// does for songs from the catalog: id returns the id of a song, 0 for none.
// Later snapshots carry them.
func (f *Fake) SetStoreIDs(id func(SnapshotTrack) int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.lib.tracks {
		f.lib.tracks[i].StoreID = id(f.lib.tracks[i])
	}
}

// RegisterCatalog gives PlayStore names and durations for these store ids.
func (f *Fake) RegisterCatalog(songs ...FakeCatalogSong) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.catalog == nil {
		f.catalog = make(map[string]FakeCatalogSong, len(songs))
	}
	for _, s := range songs {
		f.catalog[s.StoreID] = s
	}
}

// PlayStore plays catalog songs ids[start:] in order. Each plays under a fresh
// temporary pid that is not in the library, as Music.app reports them, with
// the metadata from RegisterCatalog or a made-up "Catalog song <id>".
func (f *Fake) PlayStore(ctx context.Context, ids []string, start int) error {
	return f.do(ctx, true, func(now time.Time) error {
		if len(ids) == 0 {
			return &Error{Code: 1, Message: "ids must be a non-empty list of catalog song ids"}
		}
		if start < 0 || start >= len(ids) {
			return &Error{Code: 1, Message: fmt.Sprintf("start must index ids (0..<%d)", len(ids))}
		}
		for _, id := range ids {
			if !storeID(id) {
				return &Error{Code: 1, Message: "bad catalog song id " + id}
			}
		}
		f.leaveStation()
		f.extra = make(map[string]*SnapshotTrack, len(ids)-start)
		f.queue, f.qi = make([]string, 0, len(ids)-start), 0
		for _, id := range ids[start:] {
			t := f.catalogTrack(id)
			f.extra[t.PID] = t
			f.queue = append(f.queue, t.PID)
		}
		f.begin(now)
		return nil
	})
}

func (f *Fake) catalogTrack(id string) *SnapshotTrack {
	f.tempSeq++
	h := fnv32(id)
	s, ok := f.catalog[id]
	if !ok {
		s = FakeCatalogSong{Name: "Catalog song " + id, Artist: "Catalog Artist", Album: "Catalog Album"}
	}
	if s.AlbumArtist == "" {
		s.AlbumArtist = s.Artist
	}
	dur := s.Duration
	if dur <= 0 {
		dur = float64(150 + h%150)
	}
	return &SnapshotTrack{
		PID:   fmt.Sprintf("010A%04X%08X", f.tempSeq&0xFFFF, h),
		Title: s.Name, Artist: s.Artist, Album: s.Album, AlbumArtist: s.AlbumArtist,
		Duration: int64(dur * 1000), Kind: "subscription", Available: true,
	}
}

func storeID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func fnv32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// PlayPlaylist plays a playlist (a folder plays its children in order),
// shuffled when shuffle is on. An empty playlist does nothing.
func (f *Fake) PlayPlaylist(ctx context.Context, pid string) error {
	return f.do(ctx, true, func(now time.Time) error {
		i, ok := f.plByPID[pid]
		if !ok {
			return notFound("playlist", pid)
		}
		items := f.playlistItems(f.lib.playlists[i])
		if len(items) == 0 {
			return nil
		}
		if f.shuffle {
			f.rng.Shuffle(len(items), func(a, b int) { items[a], items[b] = items[b], items[a] })
		}
		f.leaveStation()
		f.queue, f.qi, f.extra = items, 0, nil
		f.begin(now)
		return nil
	})
}

func (f *Fake) playlistItems(p SnapshotPlaylist) []string {
	if p.Kind != "folder" {
		if p.Special == "favorites" {
			var favs []string
			for _, t := range f.lib.tracks {
				if t.InLibrary && t.Favorited {
					favs = append(favs, t.PID)
				}
			}
			return favs
		}
		return slices.Clone(p.Items)
	}
	var items []string
	for _, c := range f.lib.playlists {
		if c.ParentPID == p.PID {
			items = append(items, f.playlistItems(c)...)
		}
	}
	return items
}

// SetFavorite sets favorited (clearing disliked). It shows in State, events and
// later Snapshots.
func (f *Fake) SetFavorite(ctx context.Context, pid string, value bool) error {
	return f.setFlag(ctx, pid, func(t *SnapshotTrack) {
		t.Favorited = value
		if value {
			t.Disliked = false
		}
	})
}

// SetDislike sets disliked (clearing favorited).
func (f *Fake) SetDislike(ctx context.Context, pid string, value bool) error {
	return f.setFlag(ctx, pid, func(t *SnapshotTrack) {
		t.Disliked = value
		if value {
			t.Favorited = false
		}
	})
}

func (f *Fake) setFlag(ctx context.Context, pid string, set func(*SnapshotTrack)) error {
	return f.do(ctx, false, func(now time.Time) error {
		t, err := f.track(pid)
		if err != nil {
			return err
		}
		set(t)
		if c := f.current(); c != nil && c.PID == pid && f.state != Stopped {
			f.emit(now)
		}
		return nil
	})
}

// Lyrics returns Music's own plain lyrics, which only a few non-subscription
// tracks have. Synced demo lyrics come from FakeLRC.
func (f *Fake) Lyrics(ctx context.Context, pid string) (string, error) {
	var text string
	err := f.do(ctx, false, func(time.Time) error {
		t, err := f.track(pid)
		if err != nil {
			return err
		}
		text = plainLyrics(t)
		return nil
	})
	return text, err
}

// FakeLRC returns synthetic synced lyrics (LRC) for a demo track, timed across
// its duration, or "" for instrumentals and tracks without lyrics (about a
// third). It stands in for LRCLIB in demo mode.
func (f *Fake) FakeLRC(pid string) string {
	f.mu.Lock()
	t, err := f.track(pid)
	var tc SnapshotTrack
	if err == nil {
		tc = *t
	}
	f.mu.Unlock()
	if err != nil {
		return ""
	}
	return syncedLyrics(&tc)
}

func (f *Fake) AirPlayDevices(ctx context.Context) ([]AirPlayDevice, error) {
	var out []AirPlayDevice
	err := f.do(ctx, false, func(time.Time) error {
		out = slices.Clone(f.devices)
		return nil
	})
	return out, err
}

// SelectAirPlay makes ids the selected set. Unknown or unavailable devices fail.
func (f *Fake) SelectAirPlay(ctx context.Context, ids []int) error {
	return f.do(ctx, false, func(time.Time) error {
		if len(ids) == 0 {
			return &Error{Code: 1, Message: "ids is empty"}
		}
		for _, id := range ids {
			i := f.device(id)
			if i < 0 {
				return notFound("AirPlay device", fmt.Sprint(id))
			}
			if !f.devices[i].Available {
				return &Error{Code: -10000, Message: f.devices[i].Name + " is not available."}
			}
		}
		for i := range f.devices {
			d := &f.devices[i]
			d.Selected = slices.Contains(ids, d.ID)
			d.Active = d.Selected
		}
		f.events.push(AirPlayEvent{Devices: slices.Clone(f.devices)})
		return nil
	})
}

func (f *Fake) SetAirPlayVolume(ctx context.Context, id int, volume int) error {
	return f.do(ctx, false, func(time.Time) error {
		i := f.device(id)
		if i < 0 {
			return notFound("AirPlay device", fmt.Sprint(id))
		}
		f.devices[i].Volume = min(max(volume, 0), 100)
		f.events.push(AirPlayEvent{Devices: slices.Clone(f.devices)})
		return nil
	})
}

// SetAutoReconnect turns the simulated AirPlay reconnect on or off, as
// [airplay] auto_reconnect does for the real bridge. It starts on.
func (f *Fake) SetAutoReconnect(on bool) {
	f.mu.Lock()
	f.noReconnect = !on
	f.mu.Unlock()
}

// DropAirPlayRoute simulates the macOS bug where Music keeps an AirPlay
// device selected but plays through the Mac's speakers. As the real bridge
// does, the Fake then reconnects if it is playing, this computer isn't
// selected and auto-reconnect is on: it pushes an AirPlayEvent with the same
// selection and an AirPlayRepairedEvent whose OK is ok. It reports whether it
// reconnected.
func (f *Fake) DropAirPlayRoute(ok bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.noReconnect {
		return false
	}
	f.advance(f.now())
	if f.state != Playing {
		return false
	}
	var names []string
	for _, d := range f.devices {
		if !d.Selected {
			continue
		}
		if d.Kind == "computer" {
			return false
		}
		names = append(names, d.Name)
	}
	if len(names) == 0 {
		return false
	}
	f.events.push(AirPlayEvent{Devices: slices.Clone(f.devices)})
	f.events.push(AirPlayRepairedEvent{Devices: names, OK: ok})
	return true
}

func (f *Fake) device(id int) int {
	return slices.IndexFunc(f.devices, func(d AirPlayDevice) bool { return d.ID == id })
}

// Artwork renders the album's generated cover at size px (default 600) into a
// temp-dir cache. Tracks without art and unknown pids get an empty Path.
func (f *Fake) Artwork(ctx context.Context, pid string, size int) (Artwork, error) {
	var key string
	var af ArtFaults
	err := f.do(ctx, false, func(now time.Time) error {
		af = f.faults
		f.artCalls++
		if af.FailEvery > 0 && f.artCalls%af.FailEvery == 0 {
			return &Error{Code: -1712, Message: "fake artwork: Music timed out"}
		}
		if f.lib.albums[pid] {
			key = pid
		} else if i, ok := f.byPID[pid]; ok && f.lib.tracks[i].Art {
			key = f.lib.tracks[i].AlbumPID
		} else if t, ok := f.extra[pid]; ok && f.station != "" && now.Sub(f.songAt) >= af.StationLate {
			key = "station-" + t.Artist + "-" + t.Album // a station's song has its album's art
		}
		return nil
	})
	if af.Delay > 0 && err == nil {
		select {
		case <-time.After(af.Delay):
		case <-ctx.Done():
			return Artwork{}, ctx.Err()
		}
	}
	if err != nil || key == "" {
		return Artwork{}, err
	}
	if size <= 0 {
		size = 600
	}
	size = min(size, 2048)
	path, err := cachedCover(f.artDir, key, size)
	if err != nil {
		return Artwork{}, err
	}
	return Artwork{Path: path, W: size, H: size}, nil
}

// PlaylistArtwork reports that no demo playlist has a cover of its own, so
// encore draws them from their albums' art.
func (f *Fake) PlaylistArtwork(ctx context.Context, pid string, size int) (Artwork, error) {
	return Artwork{}, f.do(ctx, false, func(time.Time) error { return nil })
}

// Events delivers PlayerEvent, AirPlayEvent and AirPlayRepairedEvent. Closed by Close.
func (f *Fake) Events() <-chan Event { return f.events.out }

// Close stops the clock and closes Events. Later calls return ErrClosed.
func (f *Fake) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.mu.Unlock()
	close(f.stop)
	f.wg.Wait()
	f.events.close()
	return nil
}

var _ Client = (*Fake)(nil)

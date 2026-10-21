// Package player is encore's playback brain. Music.app has no scriptable Up
// Next, so encore owns the play context, the order (shuffle happens here, Music's
// own shuffle stays off) and the user queue, and tells Music what to play through
// a queue playlist the bridge rebuilds on PlayTracks. See docs/design.md,
// "Playback model".
//
// Every method is safe to call from Bubble Tea's Update: state changes locally
// and at once, and bridge calls run in order on one worker goroutine.
package player

import (
	"context"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

// SliceSize is the most tracks sent in one PlayTracks call.
const SliceSize = 300

const (
	// holdFor is how long our own volume, repeat, play state and position win
	// over what Music reports.
	holdFor = 1500 * time.Millisecond
	// restartAfter is how far into a track Previous restarts it instead.
	restartAfter = 3 * time.Second
	// deadGrace is how long after a play call returns Music may sit stopped with
	// no track before we decide the track is dead and skip it.
	deadGrace = 2500 * time.Millisecond
	// pendingTimeout is how long we wait for Music to report the track we asked
	// for before we accept whatever it is playing.
	pendingTimeout = 6 * time.Second
	// The same for catalog songs, which take a few seconds to start streaming.
	storeDeadGrace      = 6 * time.Second
	storePendingTimeout = 10 * time.Second
	// finishWindow is how close to its end a track must have been for Music
	// moving off what we issued to count as running past the end of it.
	finishWindow = 5 * time.Second
	playTimeout  = 20 * time.Second
)

// Kind says what a play context was started from.
type Kind string

const (
	KindAlbum     Kind = "album"
	KindPlaylist  Kind = "playlist"
	KindSongs     Kind = "songs"
	KindFavorites Kind = "favorites"
	KindArtist    Kind = "artist"
	KindSearch    Kind = "search"
	KindQueue     Kind = "queue"
)

// Source describes where the current context came from. ID is the album key,
// playlist pid or artist name, depending on Kind.
type Source struct {
	Kind  Kind
	ID    string
	Title string
}

// Snapshot is what the UI renders.
type Snapshot struct {
	// NowPlaying is Music's state with our recent settings layered on top, the
	// interpolated position and, while a play command is in flight, the track we
	// asked for.
	NowPlaying bridge.NowPlaying
	Track      *library.Track // the playing track in the library, or nil
	// Catalog is the playing catalog song when it is one encore started
	// (PlayCatalog and the catalog queue calls); Track is nil then.
	// NowPlaying.Track still carries Music's report, with Music's temporary
	// persistent ID once it has started.
	Catalog *CatalogItem
	// Station is the radio station PlayStation started, while it is the
	// context: Music picks what plays, so Upcoming holds only the user queue,
	// and the station starts again after it. Track and Catalog are nil for a
	// station's own songs; NowPlaying.Track has Music's report.
	Station *Station
	// Live is set while a live station (no timeline) plays: Position and
	// Duration are 0.
	Live      bool
	Source    Source
	Position  time.Duration
	Duration  time.Duration
	Upcoming  int // len(Upcoming())
	Queued    int // how many of Upcoming() come from the user queue (they come first)
	Loading   bool
	LastError string
	ErrorAt   time.Time
}

// Option configures a Player.
type Option func(*Player)

// WithClock replaces the wall clock.
func WithClock(c Clock) Option { return func(p *Player) { p.clock = c } }

// WithRand sets the random source used for shuffling.
func WithRand(r *rand.Rand) Option { return func(p *Player) { p.rng = r } }

type pendingKind int

const (
	pendingPlay    pendingKind = iota // PlayTracks or PlayPlaylist
	pendingStep                       // Client.Next or Client.Previous
	pendingStation                    // Client.PlayStation; pid is ""
)

// pending is a transport command whose result Music hasn't reported yet.
type pending struct {
	seq     int
	pid     string // the track we asked for
	from    string // the track Music reported when we asked
	fromRaw string // its persistent ID as Music reported it (catalog songs differ)
	kind    pendingKind
	sent    bool
	sentAt  time.Time
}

// Player drives Music.app. Create it with New and feed it bridge events with
// Observe.
type Player struct {
	client  bridge.Client
	lookup  func(pid string) *library.Track
	clock   Clock
	rng     *rand.Rand
	w       *worker
	changes chan struct{}

	mu sync.Mutex

	src       Source
	orig      []*library.Track
	removed   []bool // orig entries taken out of the context
	order     []int
	pos       int
	ver       uint64    // bumped by touch on every change to the play order
	sum       upSummary // cached for ver
	now       *library.Track
	fromQueue bool
	queue     []*library.Track
	shuffle   bool
	dead      map[string]bool

	issued    []string // what Music is playing through, in order
	issuedIdx int      // index in issued of the track Music is on
	pending   *pending
	seq       int

	// Catalog songs the model refers to, by stand-in pid, and the last report
	// mapped to one (see canon). issuedStore is set once a catalog run was issued.
	items       map[string]*catalogEntry
	issuedStore bool
	canonRaw    string
	canonPID    string

	// The station that is the context (see station.go), or nil.
	station *Station

	np      bridge.NowPlaying // last report from the bridge
	lastPID string
	base    float64   // position in seconds at baseAt
	baseAt  time.Time // monotonic
	posHold time.Time // event positions are ignored until then
	volume  held[int]
	repeat  held[bridge.RepeatMode]
	state   held[bridge.PlayerState]
	// musicShuffleFix throttles our requests to turn Music's shuffle off.
	musicShuffleFix time.Time

	lastErr string
	errAt   time.Time
}

// New returns a Player that controls Music through c. lookup maps a pid to a
// library track; it may return nil.
func New(c bridge.Client, lookup func(pid string) *library.Track, opts ...Option) *Player {
	p := &Player{
		client:  c,
		lookup:  lookup,
		clock:   realClock{},
		changes: make(chan struct{}, 1),
		pos:     -1,
		dead:    map[string]bool{},
	}
	for _, o := range opts {
		o(p)
	}
	if p.rng == nil {
		p.rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	p.baseAt = p.clock.Now()
	p.w = newWorker()
	return p
}

// Close stops the worker. Calls still queued are dropped.
func (p *Player) Close() { p.w.close() }

// Changes receives a value whenever the snapshot changed. Sends never block and
// coalesce, so one receive may stand for many changes.
func (p *Player) Changes() <-chan struct{} { return p.changes }

func (p *Player) notify() {
	select {
	case p.changes <- struct{}{}:
	default:
	}
}

// lock takes p.mu and returns the unlock, which also notifies: defer p.lock()().
func (p *Player) lock() func() {
	p.mu.Lock()
	return func() {
		p.mu.Unlock()
		p.notify()
	}
}

// State returns a snapshot for rendering.
func (p *Player) State() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock.Now()

	np := p.np
	np.Volume = p.volume.or(now, np.Volume)
	np.Repeat = p.repeatLocked(now)
	np.Shuffle = p.shuffle
	np.State = p.stateLocked(now)

	s := Snapshot{Source: p.src, LastError: p.lastErr, ErrorAt: p.errAt}
	if p.station != nil {
		st := *p.station
		s.Station = &st
		s.Live = p.liveLocked()
	}
	if p.pending != nil && p.pending.kind == pendingStation {
		// Starting a station: nothing to show until Music reports its first song.
		s.Loading = true
		np.State = p.state.or(now, bridge.Playing)
		np.Track = nil
	} else if p.pending != nil && p.now != nil {
		s.Loading = true
		if s.Catalog = p.catalogItem(p.now.PID); s.Catalog == nil {
			s.Track = p.now
		}
		np.State = p.state.or(now, bridge.Playing)
		np.Track = &bridge.CurrentTrack{
			PID:         p.now.PID,
			Name:        p.now.Title,
			Artist:      p.now.Artist,
			Album:       p.now.AlbumName,
			AlbumArtist: p.now.AlbumArtist,
			Duration:    p.now.Duration.Seconds(),
			Favorited:   p.now.Favorited,
			Disliked:    p.now.Disliked,
		}
	} else if np.Track != nil {
		ct := *np.Track
		np.Track = &ct
		if s.Catalog = p.catalogItem(p.lastPID); s.Catalog == nil {
			s.Track = p.lookupTrack(ct.PID)
		}
	}
	s.Duration = p.durationLocked()
	s.Position = p.positionLocked(now)
	np.Position = s.Position.Seconds()
	np.At = now.UnixMilli()
	s.NowPlaying = np
	s.Upcoming, s.Queued = p.counts(now)
	return s
}

// Position is the interpolated playback position, cheap enough to call on every
// frame.
func (p *Player) Position() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.positionLocked(p.clock.Now())
}

// Upcoming returns what plays next, in order: the user queue, then the rest of
// the context (in shuffled order when shuffle is on) and, with repeat all, the
// context again up to the current track.
//
// It copies the whole sequence, which for a Songs context can be tens of
// thousands of tracks. Prefer UpcomingN, UpcomingAt and UpcomingDuration.
func (p *Player) Upcoming() []*library.Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upcoming(-1)
}

// UpcomingN returns the first n tracks of Upcoming(), or fewer when fewer are
// upcoming. It costs O(n) when no track has been found dead.
func (p *Player) UpcomingN(n int) []*library.Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upcoming(max(n, 0))
}

// UpcomingAt returns Upcoming()[i], or nil when i is out of range. It costs
// O(1) when no track has been found dead, O(i) otherwise.
func (p *Player) UpcomingAt(i int) *library.Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.locate(i)
	if !ok {
		return nil
	}
	return l.t
}

// UpcomingDuration is the total duration of Upcoming(). It walks the sequence
// once after each change to the queue, context, order or repeat mode and is
// cached in between.
func (p *Player) UpcomingDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upcomingDuration(p.clock.Now())
}

// ClearError drops LastError.
func (p *Player) ClearError() {
	defer p.lock()()
	p.lastErr, p.errAt = "", time.Time{}
}

// PlayContext plays tracks[start] and makes tracks the context. Unavailable
// tracks are left out; if tracks[start] is one, the next playable track starts.
// The user queue is kept and plays after the first track.
func (p *Player) PlayContext(src Source, tracks []*library.Track, start int) {
	defer p.lock()()
	start = max(start, 0)
	ok := make([]*library.Track, 0, len(tracks))
	first := -1 // index into ok of the track to start with
	for i, t := range tracks {
		if t == nil || !t.Available {
			continue
		}
		if first < 0 && i >= start {
			first = len(ok)
		}
		ok = append(ok, t)
	}
	if first < 0 {
		p.setErr(MsgNothingPlayable)
		return
	}
	p.setContext(src, ok, first)
	if src.Kind == KindPlaylist && src.ID != "" && start == 0 && !p.shuffle && len(ok) == len(tracks) {
		p.issueNative(src.ID)
		return
	}
	p.issue()
}

// PlayNext puts tracks at the front of the user queue.
func (p *Player) PlayNext(tracks ...*library.Track) {
	defer p.lock()()
	p.touch()
	p.queue = append(playable(tracks), p.queue...)
}

// AddToQueue appends tracks to the user queue.
func (p *Player) AddToQueue(tracks ...*library.Track) {
	defer p.lock()()
	p.touch()
	p.queue = append(p.queue, playable(tracks)...)
}

// RemoveFromQueue removes Upcoming()[i], whether it came from the user queue or
// the context.
func (p *Player) RemoveFromQueue(i int) {
	defer p.lock()()
	p.removeUpcoming(i)
}

// MoveInQueue moves Upcoming()[from] to position to. Both must be in the user
// queue part (below Snapshot.Queued); anything else is ignored.
func (p *Player) MoveInQueue(from, to int) {
	defer p.lock()()
	p.moveQueued(from, to)
}

// ClearQueue empties the user queue. The context is untouched.
func (p *Player) ClearQueue() {
	defer p.lock()()
	p.touch()
	p.queue = nil
}

// JumpTo plays Upcoming()[i], skipping everything before it.
func (p *Player) JumpTo(i int) {
	defer p.lock()()
	p.skipTo(i)
}

// Next plays the next track. During a station with nothing queued that is
// Music's next song in the station.
func (p *Player) Next() {
	defer p.lock()()
	if p.station != nil && p.peek() == nil {
		p.stationNext()
		return
	}
	p.skipTo(0)
}

func (p *Player) skipTo(i int) {
	l, ok := p.locate(i)
	if !ok {
		return
	}
	p.moveTo(l)
	if i == 0 && (p.pending == nil || p.pending.kind == pendingStep) {
		if k := p.musicNext(); k >= 0 && p.issued[k] == p.now.PID {
			p.issuedIdx = k
			p.step(func(ctx context.Context) error { return p.client.Next(ctx) })
			return
		}
	}
	p.issue()
}

// Previous restarts the track when it has played for more than 3 s, otherwise
// goes back one track in the context.
func (p *Player) Previous() {
	defer p.lock()()
	if p.onStation() {
		p.stationPrevious()
		return
	}
	now := p.clock.Now()
	if p.now == nil || p.positionLocked(now) > restartAfter {
		p.seekLocked(0)
		return
	}
	target := p.pos - 1
	if p.fromQueue {
		target = p.pos // the context track that played before the queue took over
	}
	for target >= 0 && p.dead[p.orig[p.order[target]].PID] {
		target--
	}
	if target < 0 || len(p.order) == 0 {
		p.seekLocked(0)
		return
	}
	p.touch()
	if p.fromQueue {
		p.queue = append([]*library.Track{p.now}, p.queue...)
	}
	p.pos = target
	p.now = p.orig[p.order[target]]
	p.fromQueue = false
	if p.pending == nil || p.pending.kind == pendingStep {
		if k := p.issuedIdx - 1; k >= 0 && k < len(p.issued) && p.issued[k] == p.now.PID {
			p.issuedIdx = k
			p.step(func(ctx context.Context) error { return p.client.Previous(ctx) })
			return
		}
	}
	p.issue()
}

// Toggle plays or pauses. When Music has stopped it replays the current track.
func (p *Player) Toggle() {
	defer p.lock()()
	now := p.clock.Now()
	st := p.stateLocked(now)
	if p.pending == nil && p.now != nil && (st == bridge.Stopped || p.np.Track == nil) {
		p.issue()
		return
	}
	if p.pending == nil && p.onStation() && (st == bridge.Stopped || p.np.Track == nil) {
		p.issueStation()
		return
	}
	if p.pending != nil {
		st = p.state.or(now, bridge.Playing) // what State shows while a track loads
	}
	p.base = p.positionLocked(now).Seconds()
	p.baseAt = now
	p.posHold = now.Add(holdFor)
	// Send the command we mean, not Music's playpause: Music can drop a playpause
	// or apply it the wrong way round, and then it plays while we show paused.
	next, run := bridge.Playing, p.client.Play
	if st == bridge.Playing {
		next, run = bridge.Paused, p.client.Pause
	}
	p.state = held[bridge.PlayerState]{next, now.Add(holdFor)}
	p.push(job{
		key:     keyPlayPause,
		timeout: bridge.DefaultTimeout,
		run:     run,
	})
}

// Seek moves to d into the current track.
func (p *Player) Seek(d time.Duration) {
	defer p.lock()()
	p.seekLocked(d.Seconds())
}

// SeekRelative moves by d from the current position.
func (p *Player) SeekRelative(d time.Duration) {
	defer p.lock()()
	p.seekLocked((p.positionLocked(p.clock.Now()) + d).Seconds())
}

func (p *Player) seekLocked(sec float64) {
	if p.liveLocked() {
		return // live radio has no timeline
	}
	now := p.clock.Now()
	sec = max(sec, 0)
	if dur := p.durationLocked().Seconds(); dur > 0 {
		sec = min(sec, dur)
	}
	p.base, p.baseAt = sec, now
	p.posHold = now.Add(holdFor)
	p.push(job{
		key:     keySeek,
		timeout: bridge.DefaultTimeout,
		run:     func(ctx context.Context) error { return p.client.Seek(ctx, sec) },
	})
}

// SetVolume sets Music's volume, 0..100.
func (p *Player) SetVolume(v int) {
	defer p.lock()()
	p.setVolumeLocked(v)
}

// AdjustVolume changes the volume by delta.
func (p *Player) AdjustVolume(delta int) {
	defer p.lock()()
	p.setVolumeLocked(p.volume.or(p.clock.Now(), p.np.Volume) + delta)
}

func (p *Player) setVolumeLocked(v int) {
	v = min(max(v, 0), 100)
	p.volume = held[int]{v, p.clock.Now().Add(holdFor)}
	p.push(job{
		key:     keyVolume,
		timeout: bridge.DefaultTimeout,
		run:     func(ctx context.Context) error { return p.client.SetVolume(ctx, v) },
	})
}

// SetShuffle turns encore's shuffle on or off. On, the rest of the context
// becomes [current] + shuffled(others); off, the original order resumes after
// the current track. Music picks up the new order at the next track boundary.
func (p *Player) SetShuffle(on bool) {
	defer p.lock()()
	p.setShuffleLocked(on)
}

func (p *Player) setShuffleLocked(on bool) {
	if p.shuffle == on {
		return
	}
	p.shuffle = on
	p.reorder()
}

// CycleRepeat goes off → all → one → off.
func (p *Player) CycleRepeat() {
	defer p.lock()()
	now := p.clock.Now()
	var next bridge.RepeatMode
	switch p.repeatLocked(now) {
	case bridge.RepeatOff:
		next = bridge.RepeatAll
	case bridge.RepeatAll:
		next = bridge.RepeatOne
	default:
		next = bridge.RepeatOff
	}
	p.repeat = held[bridge.RepeatMode]{next, now.Add(holdFor)}
	p.push(job{
		key:     keyRepeat,
		timeout: bridge.DefaultTimeout,
		run:     func(ctx context.Context) error { return p.client.SetRepeat(ctx, next) },
	})
}

func (p *Player) repeatLocked(now time.Time) bridge.RepeatMode {
	r := p.repeat.or(now, p.np.Repeat)
	if r == "" {
		return bridge.RepeatOff
	}
	return r
}

func (p *Player) stateLocked(now time.Time) bridge.PlayerState {
	s := p.state.or(now, p.np.State)
	if s == "" {
		return bridge.Stopped
	}
	return s
}

func (p *Player) positionLocked(now time.Time) time.Duration {
	if p.liveLocked() {
		return 0
	}
	sec := p.base
	if p.pending == nil && p.stateLocked(now) == bridge.Playing {
		sec += now.Sub(p.baseAt).Seconds()
	}
	sec = max(sec, 0)
	if dur := p.durationLocked().Seconds(); dur > 0 {
		sec = min(sec, dur)
	}
	return time.Duration(math.Round(sec*1000)) * time.Millisecond
}

func (p *Player) durationLocked() time.Duration {
	if p.pending != nil && p.now != nil {
		return p.now.Duration
	}
	if (p.pending != nil && p.pending.kind == pendingStation) || p.liveLocked() {
		return 0
	}
	if p.np.Track != nil {
		if p.np.Track.Duration > 0 {
			return time.Duration(p.np.Track.Duration * float64(time.Second))
		}
		if t := p.lookupTrack(p.np.Track.PID); t != nil {
			return t.Duration
		}
	}
	return 0
}

func (p *Player) lookupTrack(pid string) *library.Track {
	if p.lookup != nil {
		if t := p.lookup(pid); t != nil {
			return t
		}
	}
	if p.now != nil && p.now.PID == pid {
		return p.now
	}
	if e := p.items[pid]; e != nil {
		return e.t
	}
	return nil
}

func (p *Player) setErr(msg string) {
	if msg == "" {
		return
	}
	p.lastErr, p.errAt = msg, p.clock.Now()
}

// push queues a bridge call. Errors from jobs without their own done handler
// land in LastError.
func (p *Player) push(j job) {
	if j.done == nil {
		j.done = func(err error) {
			if err == nil {
				return
			}
			defer p.lock()()
			p.setErr(humanize(err))
		}
	}
	p.w.push(j)
}

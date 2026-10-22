package lastfm

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

// Scrobbler states.
const (
	StateOK      = "ok"
	StateReauth  = "reauth"  // session key rejected: run setup again
	StateConfig  = "config"  // API key or secret rejected
	StateOffline = "offline" // last send failed; retrying with backoff
)

// Status is what the player bar shows.
type Status struct {
	Enabled      bool // config allows it and credentials are complete
	User         string
	Queued       int
	State        string
	LastError    string
	LastScrobble time.Time
}

// Clock is the time source; tests inject a fake.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// SystemClock is the real clock.
var SystemClock Clock = systemClock{}

const (
	backoffMin = 30 * time.Second
	backoffMax = 30 * time.Minute
)

// Option customises a Scrobbler.
type Option func(*Scrobbler)

// WithHTTPClient sets the HTTP client for API calls.
func WithHTTPClient(c *http.Client) Option { return func(s *Scrobbler) { s.http = c } }

// WithAPIRoot points the Scrobbler at another API root (tests).
func WithAPIRoot(root string) Option { return func(s *Scrobbler) { s.root = root } }

// WithEnabled sets whether tracking and sending start on (default true).
// Passing the config here, rather than calling SetEnabled after
// NewScrobbler, keeps a leftover queue from flushing while last.fm is off.
func WithEnabled(on bool) Option { return func(s *Scrobbler) { s.enabled = on } }

// WithTick sets how often the last snapshot is re-observed between player
// updates (default 1 s; 0 disables). This keeps play time accruing when the
// UI only reports changes.
func WithTick(d time.Duration) Option { return func(s *Scrobbler) { s.tick = d } }

// Scrobbler implements Last.fm's scrobbling rules on top of player updates.
// Observe is cheap and never blocks; network and disk work run on the
// Scrobbler's own goroutines.
type Scrobbler struct {
	clock     Clock
	queuePath string
	http      *http.Client
	root      string
	tick      time.Duration

	mu         sync.Mutex
	creds      Credentials
	credsSet   bool // SetCredentials ran; InitCredentials must not override it
	enabled    bool
	tracker    tracker
	last       *bridge.NowPlaying
	lastMeta   TrackMeta
	queue      []Track
	queued     map[string]bool
	nowPlaying *Track
	state      string
	lastErr    string
	lastOK     time.Time
	blocked    bool // reauth/config: no network until SetCredentials
	backoff    time.Duration
	nextTry    time.Time
	singles    int // after a request-level error, send this many plays one by one

	kick    chan struct{}
	wake    chan struct{} // the tick loop: enabled or credentials changed
	ticks   atomic.Int64  // ticks that fired (tests)
	persist chan struct{}
	updates chan Status
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewScrobbler loads the queue at queuePath (normally DefaultQueuePath())
// and starts sending. clock may be nil for the system clock. It starts
// enabled unless WithEnabled(false) is passed.
func NewScrobbler(creds Credentials, queuePath string, clock Clock, opts ...Option) *Scrobbler {
	if clock == nil {
		clock = SystemClock
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scrobbler{
		clock:     clock,
		queuePath: queuePath,
		tick:      time.Second,
		creds:     creds,
		enabled:   true,
		state:     StateOK,
		queued:    map[string]bool{},
		kick:      make(chan struct{}, 1),
		wake:      make(chan struct{}, 1),
		persist:   make(chan struct{}, 1),
		updates:   make(chan Status, 1),
		ctx:       ctx,
		cancel:    cancel,
	}
	for _, o := range opts {
		o(s)
	}
	for _, t := range readQueue(queuePath, clock.Now()) {
		s.queue = append(s.queue, t)
		s.queued[dedupeKey(t)] = true
	}
	s.wg.Add(2)
	go s.sendLoop()
	go s.persistLoop()
	if s.tick > 0 {
		s.wg.Add(1)
		go s.tickLoop()
	}
	s.signal(s.kick)
	return s
}

// Close stops the goroutines and writes the queue one last time.
func (s *Scrobbler) Close() error {
	s.cancel()
	s.wg.Wait()
	s.mu.Lock()
	q := append([]Track(nil), s.queue...)
	s.mu.Unlock()
	return writeQueue(s.queuePath, q)
}

// Updates delivers the latest Status after each change. Only the newest is
// kept if the reader falls behind.
func (s *Scrobbler) Updates() <-chan Status { return s.updates }

// Status returns the current status.
func (s *Scrobbler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Scrobbler) statusLocked() Status {
	return Status{
		Enabled:      s.enabled && s.creds.Ready(),
		User:         s.creds.Username,
		Queued:       len(s.queue),
		State:        s.state,
		LastError:    s.lastErr,
		LastScrobble: s.lastOK,
	}
}

// SetCredentials replaces the credentials (after setup or disconnect) and
// retries the queue right away.
func (s *Scrobbler) SetCredentials(c Credentials) {
	s.mu.Lock()
	s.credsSet = true
	s.setCredsLocked(c)
	s.mu.Unlock()
	s.signal(s.kick)
	s.signal(s.wake)
	s.emit()
}

// InitCredentials applies credentials loaded in the background at startup.
// It does nothing if SetCredentials already ran (setup or disconnect
// finished first), so a slow Keychain read can't undo the user's change.
func (s *Scrobbler) InitCredentials(c Credentials) {
	s.mu.Lock()
	if s.credsSet {
		s.mu.Unlock()
		return
	}
	s.setCredsLocked(c)
	s.mu.Unlock()
	s.signal(s.kick)
	s.signal(s.wake)
	s.emit()
}

func (s *Scrobbler) setCredsLocked(c Credentials) {
	s.creds = c
	s.blocked, s.state, s.lastErr = false, StateOK, ""
	s.backoff, s.nextTry, s.singles = 0, time.Time{}, 0
}

// SetEnabled turns tracking and sending on or off. The queue is kept.
func (s *Scrobbler) SetEnabled(on bool) {
	s.mu.Lock()
	s.enabled = on
	if !on {
		s.tracker = tracker{}
		s.last = nil
		s.nowPlaying = nil
	}
	s.mu.Unlock()
	s.signal(s.kick)
	s.signal(s.wake)
	s.emit()
}

// Observe feeds a player snapshot. Call it on every player update; meta adds
// library details (pass the zero value if there are none).
func (s *Scrobbler) Observe(np bridge.NowPlaying, meta TrackMeta) {
	s.mu.Lock()
	if !s.enabled || !s.creds.Ready() {
		s.mu.Unlock()
		return
	}
	snap := np
	if np.Track != nil {
		t := *np.Track
		snap.Track = &t
	}
	s.last, s.lastMeta = &snap, meta
	changed := s.observeLocked(snap, meta)
	s.mu.Unlock()
	if changed {
		s.emit()
	}
}

func (s *Scrobbler) tickLoop() {
	defer s.wg.Done()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		// While tracking is off or last.fm isn't set up there is nothing to
		// accrue: wait to be woken instead of waking every second.
		s.mu.Lock()
		active := s.enabled && s.creds.Ready()
		s.mu.Unlock()
		if !active {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			}
			continue
		}
		timer.Reset(s.tick)
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}
		s.ticks.Add(1)
		s.mu.Lock()
		changed := false
		if s.last != nil && s.enabled && s.creds.Ready() {
			changed = s.observeLocked(*s.last, s.lastMeta)
		}
		s.mu.Unlock()
		if changed {
			s.emit()
		}
	}
}

// observeLocked runs the tracker and queues its output; it reports whether
// the queue grew.
func (s *Scrobbler) observeLocked(np bridge.NowPlaying, meta TrackMeta) bool {
	ev := s.tracker.observe(np, meta, s.clock.Now())
	if ev.nowPlaying != nil && ev.nowPlaying.Artist != "" && ev.nowPlaying.Track != "" {
		s.nowPlaying = ev.nowPlaying
		s.signal(s.kick)
	}
	grew := false
	for _, t := range ev.scrobbles {
		k := dedupeKey(t)
		if t.Artist == "" || t.Track == "" || s.queued[k] {
			continue
		}
		s.queued[k] = true
		s.queue = append(s.queue, t)
		grew = true
	}
	if grew {
		s.signal(s.persist)
		s.signal(s.kick)
	}
	return grew
}

func (s *Scrobbler) signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Scrobbler) emit() {
	st := s.Status()
	for {
		select {
		case s.updates <- st:
			return
		default:
		}
		select {
		case <-s.updates:
		default:
		}
	}
}

func (s *Scrobbler) persistLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.persist:
		}
		s.mu.Lock()
		q := append([]Track(nil), s.queue...)
		s.mu.Unlock()
		if err := writeQueue(s.queuePath, q); err != nil {
			s.mu.Lock()
			s.lastErr = "queue: " + err.Error()
			s.mu.Unlock()
			s.emit()
		}
	}
}

func (s *Scrobbler) sendLoop() {
	defer s.wg.Done()
	for {
		s.sendNowPlaying()
		wait := s.flush()
		var timer <-chan time.Time
		if wait > 0 {
			timer = s.clock.After(wait)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.kick:
		case <-timer:
		}
	}
}

func (s *Scrobbler) client() *Client {
	return &Client{Key: s.creds.APIKey, Secret: s.creds.Secret, SessionKey: s.creds.SessionKey, HTTP: s.http, Root: s.root}
}

// sendNowPlaying sends the pending now-playing update once; it's never retried.
func (s *Scrobbler) sendNowPlaying() {
	s.mu.Lock()
	t := s.nowPlaying
	s.nowPlaying = nil
	if t == nil || s.blocked || !s.enabled || !s.creds.Ready() {
		s.mu.Unlock()
		return
	}
	c := s.client()
	s.mu.Unlock()
	err := c.UpdateNowPlaying(s.ctx, *t)
	var ae *APIError
	if errors.As(err, &ae) && (ae.NeedsReauth() || ae.BadConfig()) {
		s.mu.Lock()
		s.block(ae)
		s.mu.Unlock()
		s.emit()
	}
}

// flush sends the queue oldest first. It returns how long to wait before the
// next attempt, or 0 when there's nothing to do until something changes.
func (s *Scrobbler) flush() time.Duration {
	for s.ctx.Err() == nil {
		s.mu.Lock()
		if s.blocked || !s.enabled || !s.creds.Ready() || len(s.queue) == 0 {
			s.mu.Unlock()
			return 0
		}
		now := s.clock.Now()
		if now.Before(s.nextTry) {
			s.mu.Unlock()
			return s.nextTry.Sub(now)
		}
		if s.dropExpiredLocked(now) {
			s.signal(s.persist)
			if len(s.queue) == 0 {
				s.mu.Unlock()
				s.emit()
				return 0
			}
		}
		n := min(len(s.queue), MaxBatch)
		if s.singles > 0 {
			n = 1
		}
		batch := append([]Track(nil), s.queue[:n]...)
		c := s.client()
		s.mu.Unlock()

		res, err := c.Scrobble(s.ctx, batch)
		if s.ctx.Err() != nil {
			return 0
		}

		s.mu.Lock()
		if s.singles > 0 {
			s.singles--
		}
		var ae *APIError
		switch {
		case err == nil:
			s.removeLocked(n)
			if res.Scrobbles.Attr.Accepted > 0 {
				s.lastOK = s.clock.Now()
			}
			s.state, s.lastErr, s.backoff, s.nextTry = StateOK, "", 0, time.Time{}
			if items, _ := res.Items(); res.Scrobbles.Attr.Ignored > 0 {
				for _, it := range items {
					if it.IgnoredMessage.Code != 0 {
						s.lastErr = "ignored: " + it.IgnoredMessage.Text
						break
					}
				}
			}
		case errors.As(err, &ae) && (ae.NeedsReauth() || ae.BadConfig()):
			s.block(ae)
		case errors.As(err, &ae) && ae.BadPayload() && !ae.Retryable():
			// The request's content was refused. With one play, that play
			// is the problem: drop it. With more, retry them one by one.
			// Anything else (8 "try again", 5xx, unknown codes) keeps the
			// plays and backs off below.
			if n == 1 {
				s.removeLocked(1)
				s.lastErr = "dropped: " + ae.Message
			} else {
				s.singles = n
			}
		default:
			s.backoff = min(max(backoffMin, s.backoff*2), backoffMax)
			s.nextTry = s.clock.Now().Add(s.backoff)
			s.state, s.lastErr = StateOffline, err.Error()
		}
		s.mu.Unlock()
		s.emit()
	}
	return 0
}

func (s *Scrobbler) block(ae *APIError) {
	s.blocked, s.lastErr = true, ae.Error()
	if ae.NeedsReauth() {
		s.state = StateReauth
	} else {
		s.state = StateConfig
	}
}

func (s *Scrobbler) removeLocked(n int) {
	for _, t := range s.queue[:n] {
		delete(s.queued, dedupeKey(t))
	}
	s.queue = append(s.queue[:0:0], s.queue[n:]...)
	s.signal(s.persist)
}

func (s *Scrobbler) dropExpiredLocked(now time.Time) bool {
	kept := s.queue[:0:0]
	for _, t := range s.queue {
		if tooOld(t, now) {
			delete(s.queued, dedupeKey(t))
			continue
		}
		kept = append(kept, t)
	}
	dropped := len(kept) != len(s.queue)
	s.queue = kept
	return dropped
}

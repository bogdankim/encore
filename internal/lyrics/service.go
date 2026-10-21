package lyrics

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/config"
)

// Query identifies a track to find lyrics for.
type Query struct {
	PID      string
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
	// StoreID is the Apple Music catalog song id, when known (a catalog
	// song encore plays); Apple lyrics match on it before the metadata.
	StoreID string
}

// Cache policy, from docs/research/README.md ("Lyrics").
const (
	plainTTL     = 7 * 24 * time.Hour // plain-only results: look again for synced
	firstMissTTL = 24 * time.Hour     // first not-found
	missTTL      = 7 * 24 * time.Hour // repeated not-found
	transientTTL = 5 * time.Minute    // outages, in memory only

	prefetchDelay = 500 * time.Millisecond
	cacheVersion  = 1
)

// Options configures a Service. Every field is optional.
type Options struct {
	// CacheDir holds one JSON file per track; "" disables the disk cache.
	// Normally DefaultCacheDir().
	CacheDir string
	// Providers are the sources to ask, most preferred first. When nil
	// it is Apple, LRCLIB, then MusicLyrics, for those that are set.
	Providers []Provider
	// Apple reads Apple Music's lyrics from Music.app's URL cache; nil
	// turns it off. Every lookup asks it first, before the disk cache, so
	// Apple's lyrics replace what LRCLIB had.
	Apple *AppleCache
	// LRCLIB is the lrclib.net client; nil turns LRCLIB off.
	LRCLIB *Client
	// MusicLyrics returns Music.app's own lyrics for a track (bridge.Client.Lyrics).
	MusicLyrics func(ctx context.Context, pid string) (string, error)
	// Demo, when set, is asked first and returns LRC (or plain) text for a
	// pid, or "" to fall through to the normal chain. Its results aren't cached.
	Demo func(pid string) string
	// Now is the clock; default time.Now.
	Now func() time.Time
	// FetchTimeout bounds one lookup through the whole chain; default 30 s.
	FetchTimeout time.Duration
}

// DefaultCacheDir is ~/Library/Caches/encore/lyrics.
func DefaultCacheDir() string { return filepath.Join(config.CacheDir(), "lyrics") }

// Service finds lyrics: demo, then Apple's in Music's cache, then the disk
// cache, then every provider at once (see Provider). It is safe for
// concurrent use. Returned *Lyrics are shared; don't modify them.
type Service struct {
	opts      Options
	providers []Provider
	lrclib    bool // a provider is LRCLIB: cached misses from before it was on are retried
	ctx       context.Context
	cancel    context.CancelFunc

	mu       sync.Mutex
	inflight map[string]*call
	memory   map[string]memEntry
	active   int           // foreground lookups in flight
	idle     chan struct{} // closed when active drops to 0
	queue    []Query       // prefetches not started yet

	prefetch   chan struct{} // a nudge: queue changed
	workerOnce sync.Once
	wg         sync.WaitGroup
}

type call struct {
	done chan struct{}
	lyr  *Lyrics // final, set before done closes
	err  error

	interim *Lyrics       // best answer so far; s.mu
	changed chan struct{} // closed when interim improves; s.mu
}

// Result is one answer from Next.
type Result struct {
	Lyrics *Lyrics
	Err    error
	// Final is false for an interim answer (plain lyrics, or a stale cache
	// entry) while the lookup goes on for something better.
	Final bool
}

type memEntry struct {
	until time.Time
	lyr   *Lyrics
	err   error
}

func NewService(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.FetchTimeout <= 0 {
		opts.FetchTimeout = 30 * time.Second
	}
	ps := opts.Providers
	if ps == nil {
		if opts.Apple != nil {
			ps = append(ps, opts.Apple)
		}
		if opts.LRCLIB != nil {
			ps = append(ps, LRCLIBProvider(opts.LRCLIB))
		}
		if opts.MusicLyrics != nil {
			ps = append(ps, MusicProvider(opts.MusicLyrics))
		}
	}
	usesLRCLIB := false
	for _, p := range ps {
		usesLRCLIB = usesLRCLIB || p.Name() == SourceLRCLIB
	}
	ctx, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &Service{
		opts:      opts,
		providers: ps,
		lrclib:    usesLRCLIB,
		ctx:       ctx,
		cancel:    cancel,
		inflight:  map[string]*call{},
		memory:    map[string]memEntry{},
		idle:      idle,
		prefetch:  make(chan struct{}, 1),
	}
}

// Close stops prefetching and aborts lookups in flight.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// Get returns lyrics for q, or ErrNotFound when no provider has any. Other
// errors mean a provider was unreachable; the miss is remembered for a few
// minutes. Concurrent Gets for the same track share one lookup, and a lookup
// keeps running (and fills the cache) when ctx is cancelled.
func (s *Service) Get(ctx context.Context, q Query) (*Lyrics, error) {
	r := s.next(ctx, q, nil, false, false)
	return r.Lyrics, r.Err
}

// Next is Get for a view that shows lyrics as they come: it returns the
// first answer better than prev (see Better), interim or final, or the
// final answer. Call it again with the interim Lyrics until Final is set.
// When ctx ends it returns ctx.Err() as a final answer.
func (s *Service) Next(ctx context.Context, q Query, prev *Lyrics) Result {
	return s.next(ctx, q, prev, true, false)
}

// Prefetch looks qs up in the background, in order, so they're cached by
// the time they play. Prefetches run one at a time, after foreground
// lookups; a newer Prefetch replaces the ones that haven't started.
func (s *Service) Prefetch(qs ...Query) {
	s.workerOnce.Do(func() {
		s.wg.Add(1)
		go s.prefetchLoop()
	})
	s.mu.Lock()
	s.queue = append(s.queue[:0], qs...)
	s.mu.Unlock()
	select {
	case s.prefetch <- struct{}{}:
	default:
	}
}

func (s *Service) prefetchLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.prefetch:
		}
		for {
			t := time.NewTimer(prefetchDelay) // LRCLIB asks for a gap between requests
			select {
			case <-s.ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			if !s.waitIdle() {
				return
			}
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.mu.Unlock()
				break
			}
			q := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			s.next(s.ctx, q, nil, false, true)
		}
	}
}

func (s *Service) waitIdle() bool {
	for {
		s.mu.Lock()
		if s.active == 0 {
			s.mu.Unlock()
			return true
		}
		ch := s.idle
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return false
		case <-ch:
		}
	}
}

func (s *Service) next(ctx context.Context, q Query, prev *Lyrics, interim, background bool) Result {
	final := func(l *Lyrics, err error) Result { return Result{Lyrics: l, Err: err, Final: true} }
	if s.opts.Demo != nil {
		if text := s.opts.Demo(q.PID); text != "" {
			if l := FromText(text, SourceDemo); l != nil {
				return final(l, nil)
			}
		}
	}
	if q.Title == "" && q.PID == "" {
		return final(nil, ErrNotFound)
	}
	key := q.key()
	now := s.opts.Now()
	cached, cacheOK := s.load(key)
	if l := s.apple(key, q, cached, cacheOK); l != nil {
		return final(l, nil)
	}
	if cacheOK && s.fresh(cached, now) {
		return final(cached.result())
	}

	s.mu.Lock()
	if m, ok := s.memory[key]; ok && now.Before(m.until) {
		s.mu.Unlock()
		if cacheOK && cached.found() {
			return final(cached.result())
		}
		return final(m.lyr, m.err)
	}
	c := s.inflight[key]
	if c == nil {
		c = &call{done: make(chan struct{}), changed: make(chan struct{})}
		if cacheOK && cached.found() {
			c.interim, _ = cached.result() // stale, but better than nothing
		}
		s.inflight[key] = c
		if !background {
			s.begin()
		}
		go s.run(key, q, c, cached, cacheOK, background)
	}
	for {
		select {
		case <-c.done:
			s.mu.Unlock()
			return final(c.lyr, c.err)
		default:
		}
		if interim && Better(c.interim, prev) {
			l := c.interim
			s.mu.Unlock()
			return Result{Lyrics: l}
		}
		changed := c.changed
		s.mu.Unlock()
		select {
		case <-c.done:
		case <-changed:
		case <-ctx.Done():
			return final(nil, ctx.Err())
		}
		s.mu.Lock()
	}
}

// Apple returns Apple Music's lyrics for q from Music's cache, or nil, and
// keeps them in the disk cache in place of anything else. It is cheap
// enough to call a few times while a track starts, when Music may not have
// fetched them yet.
func (s *Service) Apple(q Query) *Lyrics {
	if s.opts.Apple == nil || (q.Title == "" && q.PID == "") {
		return nil
	}
	key := q.key()
	cached, ok := s.load(key)
	return s.apple(key, q, cached, ok)
}

func (s *Service) apple(key string, q Query, cached entry, cacheOK bool) *Lyrics {
	l := s.opts.Apple.Find(q)
	if l == nil || (cacheOK && Better(mustResult(cached), l)) {
		return nil // none, or plain where the cache has synced
	}
	if !cacheOK || cached.Source != SourceApple || cached.TTML != l.ttml {
		s.store(key, newEntry(l, s.opts.Now(), cacheOK && cached.UsedLRCLIB))
	}
	return l
}

// begin and end track foreground lookups; s.mu must be held.
func (s *Service) begin() {
	if s.active == 0 {
		s.idle = make(chan struct{})
	}
	s.active++
}

func (s *Service) end() {
	if s.active--; s.active == 0 {
		close(s.idle)
	}
}

func (s *Service) run(key string, q Query, c *call, old entry, hadOld bool, background bool) {
	ctx, cancel := context.WithTimeout(s.ctx, s.opts.FetchTimeout)
	lyr, lookupErr := lookup(ctx, s.providers, q, background, func(l *Lyrics) {
		s.mu.Lock()
		if Better(l, c.interim) {
			c.interim = l
			close(c.changed)
			c.changed = make(chan struct{})
		}
		s.mu.Unlock()
	})
	cancel()
	now := s.opts.Now()

	switch {
	case lookupErr == nil:
		// Definitive answer: found or not found.
		e := newEntry(lyr, now, s.lrclib)
		if cur, ok := s.load(key); ok && cur.Source == SourceApple && !Better(lyr, mustResult(cur)) {
			// Apple's turned up in the meantime (Service.Apple).
			hadOld, old, e = true, cur, cur
		}
		if lyr == nil && hadOld {
			if old.found() {
				// Keep what we had rather than forgetting it on a later miss.
				old.Fetched = now
				e = old
			} else {
				e.Misses = old.Misses + 1
			}
		}
		s.store(key, e)
		c.lyr, c.err = e.result()
	case hadOld && old.found():
		c.lyr, c.err = old.result()
	case lyr != nil:
		c.lyr = lyr
	default:
		c.err = fmt.Errorf("lyrics: %w", lookupErr)
	}

	s.mu.Lock()
	if lookupErr != nil && s.ctx.Err() == nil {
		s.memory[key] = memEntry{until: now.Add(transientTTL), lyr: c.lyr, err: c.err}
	} else {
		delete(s.memory, key)
	}
	delete(s.inflight, key)
	if !background {
		s.end()
	}
	s.mu.Unlock()
	close(c.done)
}

func fromRecord(r *Record) *Lyrics {
	if r.Instrumental {
		return &Lyrics{Instrumental: true, Source: SourceLRCLIB, LRCLIBID: int(r.ID)}
	}
	var plain string
	if r.PlainLyrics != nil {
		plain = *r.PlainLyrics
	}
	if r.Synced() {
		if lrc := ParseLRC(*r.SyncedLyrics); hasText(lrc.Lines) {
			return &Lyrics{Synced: true, Lines: lrc.Lines, Plain: plain, Source: SourceLRCLIB, LRCLIBID: int(r.ID), lrc: *r.SyncedLyrics}
		}
	}
	if plain == "" {
		return nil
	}
	return &Lyrics{Plain: plain, Source: SourceLRCLIB, LRCLIBID: int(r.ID)}
}

// key identifies a track by its metadata, since that's what LRCLIB matches
// on; the pid is the fallback for untitled tracks.
func (q Query) key() string {
	if q.Title == "" {
		return "pid\x1f" + q.PID
	}
	secs := int(math.Round(q.Duration.Seconds()))
	return Key(q.Artist) + "\x1f" + Key(q.Title) + "\x1f" + Key(q.Album) + "\x1f" + strconv.Itoa(secs)
}

// --- disk cache ---------------------------------------------------------------

// entry is one cache file. LRC holds the raw synced text; lines are parsed
// again on load.
type entry struct {
	V            int       `json:"v"`
	Fetched      time.Time `json:"fetched"`
	Misses       int       `json:"misses,omitempty"` // consecutive definitive misses
	UsedLRCLIB   bool      `json:"usedLrclib"`
	Source       string    `json:"source,omitempty"` // "" = not found
	Instrumental bool      `json:"instrumental,omitempty"`
	LRC          string    `json:"lrc,omitempty"`
	Plain        string    `json:"plain,omitempty"`
	LRCLIBID     int       `json:"lrclibId,omitempty"`
	TTML         string    `json:"ttml,omitempty"` // Apple's lyrics as they came
}

func newEntry(l *Lyrics, now time.Time, usedLRCLIB bool) entry {
	e := entry{V: cacheVersion, Fetched: now, UsedLRCLIB: usedLRCLIB}
	if l == nil {
		e.Misses = 1
		return e
	}
	e.Source, e.Instrumental, e.LRCLIBID = l.Source, l.Instrumental, l.LRCLIBID
	switch {
	case l.ttml != "":
		e.TTML = l.ttml
	case l.Synced:
		e.LRC, e.Plain = l.lrc, l.Plain
	default:
		e.Plain = l.Plain
	}
	return e
}

func (e entry) found() bool { return e.Source != "" }

func (e entry) result() (*Lyrics, error) {
	if !e.found() {
		return nil, ErrNotFound
	}
	if e.TTML != "" {
		l, err := ParseTTML(e.TTML)
		if err != nil {
			return nil, ErrNotFound
		}
		l.Source = e.Source
		return l, nil
	}
	l := &Lyrics{Instrumental: e.Instrumental, Plain: e.Plain, Source: e.Source, LRCLIBID: e.LRCLIBID}
	if e.LRC != "" {
		l.Synced, l.Lines, l.lrc = true, ParseLRC(e.LRC).Lines, e.LRC
	}
	return l, nil
}

func mustResult(e entry) *Lyrics {
	l, _ := e.result()
	return l
}

func (s *Service) fresh(e entry, now time.Time) bool {
	age := now.Sub(e.Fetched)
	retryLRCLIB := !e.UsedLRCLIB && s.lrclib
	switch {
	case e.LRC != "" || e.Instrumental:
		return true
	case e.TTML != "":
		switch l := mustResult(e); {
		case l == nil:
			return false
		case l.Synced:
			return true
		}
		return age < plainTTL && !retryLRCLIB
	case e.found():
		return age < plainTTL && !retryLRCLIB
	case e.Misses <= 1:
		return age < firstMissTTL && !retryLRCLIB
	default:
		return age < missTTL && !retryLRCLIB
	}
}

func (s *Service) path(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(s.opts.CacheDir, hex.EncodeToString(sum[:])+".json")
}

func (s *Service) load(key string) (entry, bool) {
	if s.opts.CacheDir == "" {
		return entry{}, false
	}
	data, err := os.ReadFile(s.path(key))
	if err != nil {
		return entry{}, false
	}
	var e entry
	if json.Unmarshal(data, &e) != nil || e.V != cacheVersion {
		return entry{}, false
	}
	return e, true
}

func (s *Service) store(key string, e entry) {
	if s.opts.CacheDir == "" {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	// A failed cache write only costs a refetch later.
	_ = config.WriteFileAtomic(s.path(key), data, 0o644)
}

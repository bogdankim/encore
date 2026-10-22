package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bogdankim/encore/internal/cachegc"
)

// Timeouts for calls whose ctx has no deadline, besides DefaultTimeout.
const (
	SnapshotTimeout = 30 * time.Second
	// PlayTimeout covers PlayTracks, PlayPlaylist, PlayStore and
	// PlayStation, which may have to launch Music.app and fill the queue
	// playlist first (PlayStore and PlayStation wait up to 5 s for
	// MediaRemote).
	PlayTimeout = 10 * time.Second
	// ArtworkTimeout allows for artwork requests queued behind a snapshot.
	ArtworkTimeout = 10 * time.Second
)

var (
	// ErrExited is returned by calls in flight when the bridge process dies.
	ErrExited = errors.New("bridge: process exited")
	// ErrClosed is returned by calls after Close.
	ErrClosed = errors.New("bridge: client closed")
)

// Options configure Start. The zero value is ready to use.
type Options struct {
	// Path is the bridge binary. Empty means Locate(CacheDir).
	Path string
	// Args are passed to the bridge.
	Args []string
	// Env replaces the bridge's environment when non-nil.
	Env []string
	// CacheDir holds the extracted binary, snapshot files and artwork.
	// Default ~/Library/Caches/encore.
	CacheDir string
	// LogPath receives the bridge's stderr and the client's own diagnostics,
	// rotated at 5 MB. Default ~/Library/Logs/encore/bridge.log.
	LogPath string
	// EventBuffer is the capacity of the Events channel. Default 64.
	EventBuffer int
	// NoAirPlayReconnect starts the bridge with --no-airplay-reconnect: it
	// still notices when Music falls back to the Mac's speakers with an
	// AirPlay device selected, and logs it, but leaves the route alone.
	NoAirPlayReconnect bool

	backoff    func(attempt int) time.Duration // restart delay; tests shorten it
	afterSpawn func(*conn)                     // test hook between spawn and install
}

// restartDelay is 0.5 s, 1 s, 2 s, … capped at 10 s.
func restartDelay(attempt int) time.Duration {
	d := 500 * time.Millisecond
	for range attempt {
		d *= 2
		if d >= 10*time.Second {
			return 10 * time.Second
		}
	}
	return d
}

// stableRun is how long a bridge must stay up for the restart backoff to reset.
const stableRun = 30 * time.Second

// maxTimeouts is how many watched calls in a row may time out before the
// bridge is killed and restarted. Its Music queue is then wedged, and
// whatever is queued behind the stuck call is stale.
const maxTimeouts = 3

// unwatched methods run on the bridge's library and artwork queues, where
// long waits are normal (artwork queued behind a snapshot), so their timeouts
// don't count toward maxTimeouts.
var unwatched = map[string]bool{"library.snapshot": true, "artwork.get": true, "playlist.artwork": true}

// AutomationPending is the automation state the bridge reports (in Hello and
// PermissionsEvent) while it waits for the user to answer the prompt to
// control Music, or for Music to launch so it can ask.
const AutomationPending = "pending"

// Process is the Client backed by a running encore-bridge. It restarts the
// bridge when it dies; calls made meanwhile wait for the new process (or their
// ctx).
type Process struct {
	opts   Options
	path   string
	locate bool // path came from Locate; restarts locate it again
	events *eventQueue
	logw   *rotatingFile
	log    *log.Logger
	nextID atomic.Int64

	mu     sync.Mutex
	cur    *conn         // nil while (re)starting
	last   *conn         // most recently spawned, stopped by Close
	ready  chan struct{} // closed when cur is set
	closed bool
	done   chan struct{} // closed by Close
	sup    sync.WaitGroup

	artMu   sync.Mutex
	noArt   map[string]time.Time // name-size → when the bridge said it has no artwork
	closing sync.Once
}

// conn is one bridge process.
type conn struct {
	child   *child
	stdin   io.WriteCloser
	started time.Time

	wmu sync.Mutex // serializes request lines

	mu       sync.Mutex
	pending  map[int64]pendingCall
	dead     bool
	timeouts int  // watched calls timed out in a row
	ended    bool // readLoop is done with it; guarded by Process.mu
	// permPending is set while the bridge says it waits for the Automation
	// prompt (a "pending" permissions event or hello), and consentAt is the
	// last -1744 reply. Timeouts then don't count toward a restart: a new
	// bridge would only ask again.
	permPending bool
	consentAt   time.Time

	exited  chan struct{} // closed after the process has been reaped
	waitErr error
}

type pendingCall struct {
	method string
	ch     chan reply
}

type reply struct {
	result json.RawMessage
	err    error
}

type request struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
	// Deadline is when the client stops waiting (unix ms). The bridge
	// skips a request still queued past it and replies ErrExpired.
	Deadline int64 `json:"deadline,omitempty"`
}

// inbound is any line from the bridge: a response (id set) or an event.
type inbound struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
}

var empty = struct{}{}

// Start locates (unless opts.Path is set) and launches the bridge. The ctx only
// bounds startup; the process lives until Close.
func Start(ctx context.Context, opts Options) (*Process, error) {
	if opts.CacheDir == "" {
		opts.CacheDir = defaultCacheDir()
	}
	if opts.LogPath == "" {
		opts.LogPath = defaultLogPath()
	}
	if opts.backoff == nil {
		opts.backoff = restartDelay
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := opts.Path
	if path == "" {
		var err error
		if path, err = Locate(opts.CacheDir); err != nil {
			return nil, err
		}
	}
	p := &Process{
		opts:   opts,
		path:   path,
		locate: opts.Path == "",
		events: newEventQueue(opts.EventBuffer),
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
		noArt:  map[string]time.Time{},
	}
	var logw io.Writer = io.Discard
	if f, err := openRotatingFile(opts.LogPath, maxLogSize); err == nil {
		p.logw = f
		logw = f
	}
	p.log = log.New(logw, "encore: ", log.LstdFlags|log.Lmicroseconds)

	c, err := p.spawn()
	if err != nil {
		p.events.close()
		if p.logw != nil {
			p.logw.Close()
		}
		return nil, err
	}
	if opts.afterSpawn != nil {
		opts.afterSpawn(c)
	}
	p.install(c)
	p.sup.Add(1)
	go p.supervise(c)
	go p.trimCaches()
	return p, nil
}

// inheritTCC is set by ENCORE_INHERIT_TCC=1: the bridge then uses the
// permissions of the terminal that runs encore instead of its own. Handy for
// development, where every rebuild changes the bridge's ad-hoc signature and
// macOS asks again.
var inheritTCC = os.Getenv("ENCORE_INHERIT_TCC") == "1"

func (p *Process) spawn() (*conn, error) {
	var stderr io.Writer
	if p.logw != nil {
		stderr = p.logw
	}
	args := p.opts.Args
	if p.opts.NoAirPlayReconnect {
		args = append(slices.Clone(args), "--no-airplay-reconnect")
	}
	ch, err := startChild(p.path, args, p.opts.Env, stderr, !inheritTCC)
	if err != nil {
		return nil, fmt.Errorf("bridge: start %s: %w", p.path, err)
	}
	c := &conn{
		child:   ch,
		stdin:   ch.stdin,
		started: time.Now(),
		pending: map[int64]pendingCall{},
		exited:  make(chan struct{}),
	}
	p.mu.Lock()
	p.last = c
	p.mu.Unlock()
	pid := ch.proc.Pid
	tcc := "the terminal's"
	if ch.disclaimed && responsiblePID(pid) == pid {
		tcc = "its own"
	}
	p.log.Printf("bridge started: %s (pid %d, %s permissions)", p.path, pid, tcc)
	go p.readLoop(c, ch.stdout)
	return c, nil
}

// relocate runs Locate again before a restart, so the bridge that starts
// matches this encore even if another encore version replaced the extracted
// file meanwhile. On failure it keeps the old path. Only supervise calls it.
func (p *Process) relocate() {
	if !p.locate {
		return
	}
	path, err := Locate(p.opts.CacheDir)
	if err != nil {
		p.log.Printf("%v; restarting %s", err, p.path)
		return
	}
	p.path = path
}

// installLocked makes c the live conn and wakes callers waiting for one. A
// conn whose readLoop already finished (the child died right after spawning)
// is skipped: supervise sees it exit and spawns the next one, and ready stays
// open so callers keep waiting. ready is closed only while still open. The
// caller holds p.mu.
func (p *Process) installLocked(c *conn) {
	if c.ended {
		return
	}
	p.cur = c
	select {
	case <-p.ready:
	default:
		close(p.ready)
	}
}

func (p *Process) install(c *conn) {
	p.mu.Lock()
	p.installLocked(c)
	p.mu.Unlock()
}

// supervise restarts the bridge whenever it dies, until Close.
func (p *Process) supervise(c *conn) {
	defer p.sup.Done()
	attempt := 0
	for {
		<-c.exited
		if p.isClosed() {
			return
		}
		err := fmt.Errorf("%w: %v", ErrExited, c.waitErr)
		if c.waitErr == nil {
			err = fmt.Errorf("%w with status 0", ErrExited)
		}
		p.events.push(ExitEvent{Err: err})
		if time.Since(c.started) > stableRun {
			attempt = 0
		}
		for {
			d := p.opts.backoff(attempt)
			attempt++
			p.log.Printf("%v; restarting in %v", err, d)
			select {
			case <-time.After(d):
			case <-p.done:
				return
			}
			p.relocate()
			next, serr := p.spawn()
			if serr != nil {
				err = serr
				p.events.push(ExitEvent{Err: serr})
				continue
			}
			if p.opts.afterSpawn != nil {
				p.opts.afterSpawn(next)
			}
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				next.stop()
				return
			}
			p.installLocked(next)
			p.mu.Unlock()
			c = next
			break
		}
	}
}

func (p *Process) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *Process) readLoop(c *conn, stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			p.dispatch(c, line)
		}
		if err != nil {
			break
		}
	}

	// Stop handing out this conn before failing its calls, so retries wait for
	// the replacement instead of finding the dead one again.
	p.mu.Lock()
	c.ended = true
	if p.cur == c {
		p.cur = nil
		p.ready = make(chan struct{})
	}
	failure := ErrExited
	if p.closed {
		failure = ErrClosed
	}
	p.mu.Unlock()

	c.mu.Lock()
	c.dead = true
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, pc := range pending {
		pc.ch <- reply{err: fmt.Errorf("bridge %s: %w", pc.method, failure)}
	}

	c.waitErr = c.child.wait()
	if !p.isClosed() {
		p.log.Printf("bridge exited: %v", c.waitErr)
	}
	close(c.exited)
}

func (p *Process) dispatch(c *conn, line []byte) {
	var in inbound
	if err := json.Unmarshal(line, &in); err != nil {
		p.log.Printf("bad line from bridge: %v: %.200q", err, line)
		return
	}
	if in.ID != nil {
		c.mu.Lock()
		pc, ok := c.pending[*in.ID]
		delete(c.pending, *in.ID)
		c.mu.Unlock()
		if ok || in.Error != nil {
			c.notePermission(pc.method, in.Result, in.Error)
		}
		if !ok {
			return // the caller gave up
		}
		if in.Error != nil {
			pc.ch <- reply{err: in.Error}
		} else {
			pc.ch <- reply{result: in.Result}
		}
		return
	}
	ev, err := decodeEvent(in.Event, in.Data)
	if err != nil {
		p.log.Printf("bad %q event: %v", in.Event, err)
		return
	}
	if ev == nil {
		return
	}
	if pe, ok := ev.(PermissionsEvent); ok {
		c.setPermPending(pe.Automation == AutomationPending)
	}
	if _, ok := ev.(LibraryEvent); ok {
		p.artMu.Lock()
		clear(p.noArt)
		p.artMu.Unlock()
	}
	p.events.push(ev)
}

// decodeEvent turns an event line into an Event. Unknown events return nil.
func decodeEvent(name string, data json.RawMessage) (Event, error) {
	switch name {
	case "player":
		var np NowPlaying
		if err := json.Unmarshal(data, &np); err != nil {
			return nil, err
		}
		return PlayerEvent{NowPlaying: np}, nil
	case "airplay":
		var d struct {
			Devices []AirPlayDevice `json:"devices"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		return AirPlayEvent{Devices: d.Devices}, nil
	case "airplay.repaired":
		var d struct {
			Devices []string `json:"devices"`
			OK      bool     `json:"ok"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		return AirPlayRepairedEvent{Devices: d.Devices, OK: d.OK}, nil
	case "music":
		var d struct {
			Running bool `json:"running"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		return MusicEvent{Running: d.Running}, nil
	case "library":
		return LibraryEvent{}, nil
	case "permissions":
		var d struct {
			Automation string `json:"automation"`
			Library    string `json:"library"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		return PermissionsEvent{Automation: d.Automation, Library: d.Library}, nil
	}
	return nil, nil
}

// live returns the running conn, waiting for a restart if needed.
func (p *Process) live(ctx context.Context) (*conn, error) {
	for {
		p.mu.Lock()
		c, ready, closed := p.cur, p.ready, p.closed
		p.mu.Unlock()
		if closed {
			return nil, ErrClosed
		}
		if c != nil {
			return c, nil
		}
		select {
		case <-ready:
		case <-p.done:
			return nil, ErrClosed
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// call sends one request and decodes the result into out (if non-nil). A ctx
// without a deadline gets timeout.
func (p *Process) call(ctx context.Context, timeout time.Duration, method string, params, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if params == nil {
		params = empty
	}
	ch := make(chan reply, 1)
	for {
		c, err := p.live(ctx)
		if err != nil {
			return fmt.Errorf("bridge %s: %w", method, err)
		}
		id := p.nextID.Add(1)
		c.mu.Lock()
		if c.dead {
			c.mu.Unlock()
			continue // died before we sent anything: wait for the next one
		}
		c.pending[id] = pendingCall{method: method, ch: ch}
		c.mu.Unlock()

		dl, _ := ctx.Deadline()
		line, err := json.Marshal(request{ID: id, Method: method, Params: params, Deadline: dl.UnixMilli()})
		if err != nil {
			c.forget(id)
			return fmt.Errorf("bridge %s: %w", method, err)
		}
		line = append(line, '\n')
		c.wmu.Lock()
		_, err = c.stdin.Write(line)
		c.wmu.Unlock()
		if err != nil {
			c.forget(id)
			return fmt.Errorf("bridge %s: %w: %v", method, ErrExited, err)
		}

		select {
		case r := <-ch:
			c.answered(method)
			if r.err != nil {
				return r.err
			}
			if out != nil && len(r.result) > 0 {
				if err := json.Unmarshal(r.result, out); err != nil {
					return fmt.Errorf("bridge %s: decode result: %w", method, err)
				}
			}
			return nil
		case <-ctx.Done():
			c.forget(id)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && c.timedOut(method) {
				p.log.Printf("bridge %s: %d calls in a row timed out; restarting it", method, maxTimeouts)
				c.child.proc.Kill()
			}
			return fmt.Errorf("bridge %s: %w", method, ctx.Err())
		}
	}
}

func (c *conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// answered resets the timeout streak after a reply to a watched call.
func (c *conn) answered(method string) {
	if unwatched[method] {
		return
	}
	c.mu.Lock()
	c.timeouts = 0
	c.mu.Unlock()
}

// consentGrace is how long after a -1744 reply timeouts don't count. While
// the prompt is up every Music call gets one, which keeps renewing it.
const consentGrace = 10 * time.Second

// notePermission records what a reply says about the Automation prompt: a
// -1744 (consent needed) starts consentGrace, and hello's automation field
// sets permPending. method is "" for a reply whose caller already gave up.
func (c *conn) notePermission(method string, result json.RawMessage, rerr *Error) {
	switch {
	case rerr != nil && rerr.Code == ErrAutomationConsent:
		c.mu.Lock()
		c.consentAt = time.Now()
		c.timeouts = 0
		c.mu.Unlock()
	case rerr == nil && method == "hello":
		var h struct {
			Automation string `json:"automation"`
		}
		if json.Unmarshal(result, &h) == nil {
			c.setPermPending(h.Automation == AutomationPending)
		}
	}
}

func (c *conn) setPermPending(on bool) {
	c.mu.Lock()
	c.permPending = on
	if on {
		c.timeouts = 0
	}
	c.mu.Unlock()
}

// timedOut records a watched call that got no reply in time and reports
// whether the streak just reached maxTimeouts. Nothing counts while the bridge
// waits for permission: the prompt is what holds Music up.
func (c *conn) timedOut(method string) bool {
	if unwatched[method] {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead || c.permPending || time.Since(c.consentAt) < consentGrace {
		return false
	}
	c.timeouts++
	return c.timeouts == maxTimeouts
}

// stop closes stdin (the bridge exits at EOF), then kills it after a second.
func (c *conn) stop() {
	c.stdin.Close()
	select {
	case <-c.exited:
		return
	case <-time.After(time.Second):
	}
	c.child.proc.Kill()
	<-c.exited
}

// Close stops the bridge and closes the Events channel. Calls in flight fail
// with ErrClosed.
func (p *Process) Close() error {
	p.closing.Do(func() {
		p.mu.Lock()
		p.closed = true
		c := p.last
		p.mu.Unlock()
		close(p.done)
		if c != nil {
			c.stop()
		}
		p.sup.Wait()
		p.events.close()
		if p.logw != nil {
			p.logw.Close()
		}
	})
	return nil
}

// Events delivers pushed events, including ExitEvent when the bridge dies.
func (p *Process) Events() <-chan Event { return p.events.out }

// Hello performs the handshake.
func (p *Process) Hello(ctx context.Context) (Hello, error) {
	var h Hello
	err := p.call(ctx, DefaultTimeout, "hello", nil, &h)
	return h, err
}

// Snapshot asks the bridge to dump the library to a temp file in CacheDir and
// decodes it.
func (p *Process) Snapshot(ctx context.Context) (*Snapshot, error) {
	if err := os.MkdirAll(p.opts.CacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("bridge library.snapshot: %w", err)
	}
	f, err := os.CreateTemp(p.opts.CacheDir, "snapshot-*.json")
	if err != nil {
		return nil, fmt.Errorf("bridge library.snapshot: %w", err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	var res struct {
		Tracks    int `json:"tracks"`
		Playlists int `json:"playlists"`
		MS        int `json:"ms"`
	}
	if err := p.call(ctx, SnapshotTimeout, "library.snapshot", map[string]any{"path": path}, &res); err != nil {
		return nil, err
	}
	f, err = os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bridge library.snapshot: %w", err)
	}
	defer f.Close()
	snap := new(Snapshot)
	if err := json.NewDecoder(bufio.NewReaderSize(f, 256<<10)).Decode(snap); err != nil {
		return nil, fmt.Errorf("bridge library.snapshot: decode: %w", err)
	}
	p.log.Printf("snapshot: %d tracks, %d playlists in %d ms", res.Tracks, res.Playlists, res.MS)
	return snap, nil
}

// State reads the player.
func (p *Process) State(ctx context.Context) (NowPlaying, error) {
	var np NowPlaying
	err := p.call(ctx, DefaultTimeout, "player.state", nil, &np)
	return np, err
}

func (p *Process) PlayPause(ctx context.Context) error {
	return p.call(ctx, DefaultTimeout, "player.playpause", nil, nil)
}

func (p *Process) Play(ctx context.Context) error {
	return p.call(ctx, DefaultTimeout, "player.play", nil, nil)
}

func (p *Process) Pause(ctx context.Context) error {
	return p.call(ctx, DefaultTimeout, "player.pause", nil, nil)
}

func (p *Process) Next(ctx context.Context) error {
	return p.call(ctx, DefaultTimeout, "player.next", nil, nil)
}

func (p *Process) Previous(ctx context.Context) error {
	return p.call(ctx, DefaultTimeout, "player.previous", nil, nil)
}

func (p *Process) Seek(ctx context.Context, seconds float64) error {
	return p.call(ctx, DefaultTimeout, "player.seek", map[string]any{"seconds": seconds}, nil)
}

func (p *Process) SetVolume(ctx context.Context, volume int) error {
	return p.call(ctx, DefaultTimeout, "player.volume", map[string]any{"volume": min(max(volume, 0), 100)}, nil)
}

func (p *Process) SetRepeat(ctx context.Context, mode RepeatMode) error {
	return p.call(ctx, DefaultTimeout, "player.repeat", map[string]any{"mode": mode}, nil)
}

func (p *Process) SetShuffle(ctx context.Context, enabled bool) error {
	return p.call(ctx, DefaultTimeout, "player.shuffle", map[string]any{"enabled": enabled}, nil)
}

func (p *Process) PlayTracks(ctx context.Context, pids []string, containers map[string]string) error {
	if containers == nil {
		containers = map[string]string{}
	}
	return p.call(ctx, PlayTimeout, "player.playTracks", map[string]any{"pids": pids, "containers": containers}, nil)
}

func (p *Process) PlayPlaylist(ctx context.Context, pid string) error {
	return p.call(ctx, PlayTimeout, "player.playPlaylist", map[string]any{"pid": pid}, nil)
}

func (p *Process) PlayStore(ctx context.Context, ids []string, start int) error {
	return p.call(ctx, PlayTimeout, "player.playStore", map[string]any{"ids": ids, "start": start}, nil)
}

func (p *Process) PlayStation(ctx context.Context, id string) error {
	return p.call(ctx, PlayTimeout, "player.playStation", map[string]any{"id": id}, nil)
}

func (p *Process) SetFavorite(ctx context.Context, pid string, value bool) error {
	return p.call(ctx, DefaultTimeout, "track.setFavorite", map[string]any{"pid": pid, "value": value}, nil)
}

func (p *Process) SetDislike(ctx context.Context, pid string, value bool) error {
	return p.call(ctx, DefaultTimeout, "track.setDislike", map[string]any{"pid": pid, "value": value}, nil)
}

// Lyrics returns Music's own plain lyrics for pid, often "".
func (p *Process) Lyrics(ctx context.Context, pid string) (string, error) {
	var res struct {
		Text string `json:"text"`
	}
	err := p.call(ctx, DefaultTimeout, "track.lyrics", map[string]any{"pid": pid}, &res)
	return res.Text, err
}

func (p *Process) AirPlayDevices(ctx context.Context) ([]AirPlayDevice, error) {
	var res struct {
		Devices []AirPlayDevice `json:"devices"`
	}
	err := p.call(ctx, DefaultTimeout, "airplay.list", nil, &res)
	return res.Devices, err
}

func (p *Process) SelectAirPlay(ctx context.Context, ids []int) error {
	if ids == nil {
		ids = []int{}
	}
	return p.call(ctx, DefaultTimeout, "airplay.select", map[string]any{"ids": ids}, nil)
}

func (p *Process) SetAirPlayVolume(ctx context.Context, id int, volume int) error {
	return p.call(ctx, DefaultTimeout, "airplay.volume", map[string]any{"id": id, "volume": min(max(volume, 0), 100)}, nil)
}

// The art folder (this client's covers, Apple Music covers, playlist
// mosaics) is trimmed at start: files unused for artCacheMaxAge go, then the
// least recently used until it is under artCacheMaxBytes.
const (
	artCacheMaxAge   = 90 * 24 * time.Hour
	artCacheMaxBytes = 256 << 20
)

// snapshotLeftoverAge is how old a snapshot temp file in the cache folder
// must be before start-up deletes it. A snapshot takes at most
// SnapshotTimeout, and another encore may be taking one right now.
const snapshotLeftoverAge = 10 * time.Minute

// trimCaches runs once at start, in the background: it deletes what a crash
// left behind (snapshot temp files, which the bridge's SnapshotWriter also
// leaves as .tmp) and trims the art folder.
func (p *Process) trimCaches() {
	dir := p.opts.CacheDir
	cachegc.RemoveGlob(filepath.Join(dir, "snapshot-*.json*"), snapshotLeftoverAge)
	cachegc.Prune(filepath.Join(dir, "art"), artCacheMaxAge, artCacheMaxBytes)
}

// NoArtworkTTL is how long Artwork and PlaylistArtwork remember that an item
// has no artwork, unless a LibraryEvent comes first. A track that has just
// started can have none until Music has fetched it, so the answer is not
// kept for good, and the player bar asks again a second later (see
// heroNoneRetry in internal/ui): no longer than that.
const NoArtworkTTL = time.Second

// Artwork returns <CacheDir>/art/<pid>-<size>.png (size 0 means 600), asking
// the bridge to write it on first use. Items without artwork are remembered
// for NoArtworkTTL.
func (p *Process) Artwork(ctx context.Context, pid string, size int) (Artwork, error) {
	return p.artworkFile(ctx, "artwork.get", pid, pid, size, 0)
}

// PlaylistArtTTL is how long a playlist's cover is used from disk before
// Music is asked again: Apple changes the covers of its playlists, and Music
// redraws the ones it makes when a playlist is renamed.
const PlaylistArtTTL = 24 * time.Hour

// PlaylistArtwork returns <CacheDir>/art/pl-<pid>-<size>.png (size 0 means
// 600), the cover Music shows for the playlist, asking the bridge when the
// file is missing or older than PlaylistArtTTL. A stale file is still used
// when the bridge can't answer. Playlists without a cover are remembered
// for NoArtworkTTL.
func (p *Process) PlaylistArtwork(ctx context.Context, pid string, size int) (Artwork, error) {
	return p.artworkFile(ctx, "playlist.artwork", pid, "pl-"+pid, size, PlaylistArtTTL)
}

// artworkFile serves <CacheDir>/art/<name>-<size>.png, asking the bridge's
// method to write it when it is missing or, with ttl > 0, older than ttl.
func (p *Process) artworkFile(ctx context.Context, method, pid, name string, size int, ttl time.Duration) (Artwork, error) {
	if size <= 0 {
		size = 600
	}
	key := name + "-" + strconv.Itoa(size)
	p.artMu.Lock()
	at, none := p.noArt[key]
	p.artMu.Unlock()
	if none && time.Since(at) < NoArtworkTTL {
		return Artwork{}, nil
	}
	dir := filepath.Join(p.opts.CacheDir, "art")
	path := filepath.Join(dir, key+".png")
	stale, haveStale := cachedArtwork(path)
	if haveStale && (ttl <= 0 || fileAge(path) < ttl) {
		cachegc.Touch(path) // keep what is read; see artCacheMaxBytes
		return stale, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Artwork{}, fmt.Errorf("bridge %s: %w", method, err)
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, p.nextID.Add(1))
	var a Artwork
	err := p.call(ctx, ArtworkTimeout, method, map[string]any{"pid": pid, "size": size, "path": tmp}, &a)
	if err != nil {
		os.Remove(tmp)
		if haveStale {
			return stale, nil
		}
		return Artwork{}, err
	}
	if a.Path == "" {
		os.Remove(tmp)
		if haveStale {
			os.Remove(path)
		}
		p.artMu.Lock()
		p.noArt[key] = time.Now()
		p.artMu.Unlock()
		return Artwork{}, nil
	}
	if err := os.Rename(a.Path, path); err != nil {
		os.Remove(a.Path)
		return Artwork{}, fmt.Errorf("bridge %s: %w", method, err)
	}
	a.Path = path
	return a, nil
}

// fileAge is how long ago path was written, or a very long time when it
// can't be read.
func fileAge(path string) time.Duration {
	st, err := os.Stat(path)
	if err != nil {
		return 1<<63 - 1
	}
	return time.Since(st.ModTime())
}

func cachedArtwork(path string) (Artwork, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Artwork{}, false
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return Artwork{}, false
	}
	return Artwork{Path: path, W: cfg.Width, H: cfg.Height}, true
}

var _ Client = (*Process)(nil)

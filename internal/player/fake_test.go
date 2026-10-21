package player

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

// call is one recorded bridge call.
type call struct {
	name       string
	pids       []string
	containers map[string]string
	pid        string
	f          float64
	n          int
	b          bool
	mode       bridge.RepeatMode
}

// fakeClient records calls. gate makes the next call block until release.
type fakeClient struct {
	mu      sync.Mutex
	calls   []call
	errs    map[string]error
	gated   bool
	entered chan struct{}
	release chan struct{}
}

func newFakeClient() *fakeClient {
	return &fakeClient{errs: map[string]error{}}
}

func (f *fakeClient) gate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gated = true
	f.entered = make(chan struct{})
	f.release = make(chan struct{})
}

func (f *fakeClient) record(c call) error {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	err := f.errs[c.name]
	gated, entered, release := f.gated, f.entered, f.release
	f.gated = false
	f.mu.Unlock()
	if gated {
		close(entered)
		<-release
	}
	return err
}

func (f *fakeClient) failWith(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[name] = err
}

func (f *fakeClient) take() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

func (f *fakeClient) Hello(context.Context) (bridge.Hello, error)        { return bridge.Hello{}, nil }
func (f *fakeClient) Snapshot(context.Context) (*bridge.Snapshot, error) { return nil, nil }
func (f *fakeClient) State(context.Context) (bridge.NowPlaying, error) {
	return bridge.NowPlaying{}, nil
}
func (f *fakeClient) PlayPause(context.Context) error { return f.record(call{name: "playpause"}) }
func (f *fakeClient) Play(context.Context) error      { return f.record(call{name: "play"}) }
func (f *fakeClient) Pause(context.Context) error     { return f.record(call{name: "pause"}) }
func (f *fakeClient) Next(context.Context) error      { return f.record(call{name: "next"}) }
func (f *fakeClient) Previous(context.Context) error  { return f.record(call{name: "previous"}) }
func (f *fakeClient) Seek(_ context.Context, s float64) error {
	return f.record(call{name: "seek", f: s})
}
func (f *fakeClient) SetVolume(_ context.Context, v int) error {
	return f.record(call{name: "volume", n: v})
}
func (f *fakeClient) SetRepeat(_ context.Context, m bridge.RepeatMode) error {
	return f.record(call{name: "repeat", mode: m})
}
func (f *fakeClient) SetShuffle(_ context.Context, on bool) error {
	return f.record(call{name: "shuffle", b: on})
}
func (f *fakeClient) PlayTracks(_ context.Context, pids []string, c map[string]string) error {
	return f.record(call{name: "playTracks", pids: pids, containers: c})
}
func (f *fakeClient) PlayPlaylist(_ context.Context, pid string) error {
	return f.record(call{name: "playPlaylist", pid: pid})
}
func (f *fakeClient) PlayStore(_ context.Context, ids []string, start int) error {
	return f.record(call{name: "playStore", pids: ids, n: start})
}
func (f *fakeClient) PlayStation(_ context.Context, id string) error {
	return f.record(call{name: "playStation", pid: id})
}
func (f *fakeClient) SetFavorite(context.Context, string, bool) error { return nil }
func (f *fakeClient) SetDislike(context.Context, string, bool) error  { return nil }
func (f *fakeClient) Lyrics(context.Context, string) (string, error)  { return "", nil }
func (f *fakeClient) AirPlayDevices(context.Context) ([]bridge.AirPlayDevice, error) {
	return nil, nil
}
func (f *fakeClient) SelectAirPlay(context.Context, []int) error       { return nil }
func (f *fakeClient) SetAirPlayVolume(context.Context, int, int) error { return nil }
func (f *fakeClient) Artwork(context.Context, string, int) (bridge.Artwork, error) {
	return bridge.Artwork{}, nil
}

func (f *fakeClient) PlaylistArtwork(context.Context, string, int) (bridge.Artwork, error) {
	return bridge.Artwork{}, nil
}
func (f *fakeClient) Events() <-chan bridge.Event { return nil }
func (f *fakeClient) Close() error                { return nil }

// fakeClock moves only when told to and fires timers synchronously.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
}

type fakeTimer struct {
	at time.Time
	f  func()
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timers = append(c.timers, fakeTimer{c.now.Add(d), f})
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due, rest []fakeTimer
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			due = append(due, t)
		} else {
			rest = append(rest, t)
		}
	}
	c.timers = rest
	c.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, t := range due {
		t.f()
	}
}

// rig is a player wired to fakes, plus helpers for driving it.
type rig struct {
	t      *testing.T
	p      *Player
	c      *fakeClient
	clock  *fakeClock
	byPID  map[string]*library.Track
	volume int
	repeat bridge.RepeatMode
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, c: newFakeClient(), clock: newFakeClock(), byPID: map[string]*library.Track{}, volume: 50, repeat: bridge.RepeatOff}
	r.p = New(r.c, func(pid string) *library.Track { return r.byPID[pid] },
		WithClock(r.clock), WithRand(rand.New(rand.NewPCG(1, 2))))
	t.Cleanup(r.p.Close)
	return r
}

// tracks makes n available library tracks with 200 s durations.
func (r *rig) tracks(n int) []*library.Track {
	out := make([]*library.Track, n)
	base := len(r.byPID)
	for i := range out {
		t := &library.Track{
			PID:       fmt.Sprintf("%016X", base+i+1),
			Title:     fmt.Sprintf("Track %d", base+i),
			Duration:  200 * time.Second,
			InLibrary: true,
			Available: true,
		}
		r.byPID[t.PID] = t
		out[i] = t
	}
	return out
}

// flush waits until the worker has run everything queued so far.
func (r *rig) flush() {
	r.t.Helper()
	done := make(chan struct{})
	r.p.w.push(job{timeout: time.Second, run: func(context.Context) error { close(done); return nil }})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		r.t.Fatal("worker stuck")
	}
}

// calls flushes and returns the calls made since the last time.
func (r *rig) calls() []call {
	r.flush()
	return r.c.take()
}

func (r *rig) nowPlaying(pid string, state bridge.PlayerState, pos float64) bridge.NowPlaying {
	np := bridge.NowPlaying{
		State: state, Position: pos, At: r.clock.Now().UnixMilli(),
		Volume: r.volume, Repeat: r.repeat,
	}
	if pid != "" {
		np.Track = &bridge.CurrentTrack{PID: pid, Duration: 200}
	}
	return np
}

// playing reports pid playing at pos.
func (r *rig) playing(pid string, pos float64) {
	r.p.Observe(bridge.PlayerEvent{NowPlaying: r.nowPlaying(pid, bridge.Playing, pos)})
}

// stoppedEv reports Music stopped with no current track.
func (r *rig) stoppedEv() {
	r.p.Observe(bridge.PlayerEvent{NowPlaying: r.nowPlaying("", bridge.Stopped, 0)})
}

// played flushes the worker and reports pid starting, confirming a pending play.
func (r *rig) played(pid string) {
	r.flush()
	r.playing(pid, 0)
}

func pidsOf(ts []*library.Track) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.PID
	}
	return out
}

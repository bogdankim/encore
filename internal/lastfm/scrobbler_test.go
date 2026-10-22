package lastfm

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

var testCreds = Credentials{APIKey: "k", Secret: testSecret, SessionKey: "sk", Username: "bogdan"}

const okScrobble = `{"scrobbles":{"scrobble":{"ignoredMessage":{"code":"0","#text":""}},"@attr":{"accepted":1,"ignored":0}}}`

func okBatch(p url.Values) (int, string) {
	n := 0
	for p.Has("artist[" + strconv.Itoa(n) + "]") {
		n++
	}
	items := strings.TrimSuffix(strings.Repeat(`{"ignoredMessage":{"code":"0","#text":""}},`, n), ",")
	return 200, fmt.Sprintf(`{"scrobbles":{"scrobble":[%s],"@attr":{"accepted":%d,"ignored":0}}}`, items, n)
}

func newTestScrobbler(t *testing.T, f *fakeAPI, clk *fakeClock, queue string) *Scrobbler {
	t.Helper()
	if queue == "" {
		queue = filepath.Join(t.TempDir(), "scrobbles.jsonl")
	}
	s := NewScrobbler(testCreds, queue, clk, WithAPIRoot(f.root()), WithTick(0))
	t.Cleanup(func() { s.Close() })
	return s
}

// listen plays one track for secs seconds, one snapshot per second.
func listen(s *Scrobbler, clk *fakeClock, pid, title string, secs int, dur float64) {
	tr := &bridge.CurrentTrack{PID: pid, Name: title, Artist: "Artist", Album: "Album", Duration: dur}
	s.Observe(bridge.NowPlaying{State: bridge.Playing, Track: tr}, TrackMeta{})
	for i := 1; i <= secs; i++ {
		clk.Advance(time.Second)
		s.Observe(bridge.NowPlaying{State: bridge.Playing, Position: float64(i), Track: tr}, TrackMeta{TrackNumber: 4})
	}
}

func writeQueueFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func queueLines(t *testing.T, path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

func TestScrobblerNowPlayingAndScrobble(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", okBatch)
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	s := newTestScrobbler(t, f, clk, queue)
	start := clk.Now()

	listen(s, clk, "P1", "Song", 100, 200)
	waitFor(t, "now playing", func() bool { return len(f.callsTo("track.updateNowPlaying")) == 1 })
	waitFor(t, "scrobble", func() bool { return len(f.callsTo("track.scrobble")) == 1 })
	np := f.callsTo("track.updateNowPlaying")[0]
	if np.Get("track") != "Song" || np.Get("duration") != "200" || np.Get("sk") != "sk" {
		t.Errorf("now playing params: %v", np)
	}
	sc := f.callsTo("track.scrobble")[0]
	if sc.Get("track[0]") != "Song" || sc.Get("timestamp[0]") != strconv.FormatInt(start.Unix(), 10) || sc.Get("trackNumber[0]") != "4" {
		t.Errorf("scrobble params: %v", sc)
	}
	waitFor(t, "status", func() bool { st := s.Status(); return st.Queued == 0 && !st.LastScrobble.IsZero() })
	st := s.Status()
	if !st.Enabled || st.User != "bogdan" || st.State != StateOK {
		t.Errorf("status: %+v", st)
	}
	waitFor(t, "queue file emptied", func() bool { return queueLines(t, queue) == 0 })

	// Playing on doesn't scrobble the same play again.
	listen(s, clk, "P1", "Song", 0, 200)
	time.Sleep(20 * time.Millisecond)
	if n := len(f.callsTo("track.scrobble")); n != 1 {
		t.Fatalf("scrobbled %d times", n)
	}
}

func TestScrobblerObserveNeverBlocks(t *testing.T) {
	f := newFakeAPI(t)
	release := make(chan struct{})
	f.handle("track.updateNowPlaying", func(url.Values) (int, string) { <-release; return 200, `{}` })
	defer close(release)
	clk := newFakeClock()
	s := newTestScrobbler(t, f, clk, "")
	start := time.Now()
	for i := range 3 {
		listen(s, clk, "P"+strconv.Itoa(i), "Song", 50, 60) // each plays past half: now playing + scrobble
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Observe blocked behind the network: %v", d)
	}
}

func TestScrobblerBatchesOldestFirst(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", okBatch)
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	base := clk.Now().Add(-48 * time.Hour).Unix()
	var lines []string
	for i := range 120 {
		lines = append(lines, fmt.Sprintf(`{"artist":"A","track":"T%d","timestamp":%d}`, i, base+int64(i)*300))
	}
	lines = append(lines,
		lines[5], // duplicate
		fmt.Sprintf(`{"artist":"A","track":"Old","timestamp":%d}`, clk.Now().Add(-15*24*time.Hour).Unix()),
		`not json`,
	)
	writeQueueFile(t, queue, lines...)

	s := newTestScrobbler(t, f, clk, queue)
	waitFor(t, "three batches", func() bool { return len(f.callsTo("track.scrobble")) == 3 })
	calls := f.callsTo("track.scrobble")
	next := 0
	for i, want := range []int{50, 50, 20} {
		p := calls[i]
		for j := range want {
			if got := p.Get("track[" + strconv.Itoa(j) + "]"); got != "T"+strconv.Itoa(next) {
				t.Fatalf("batch %d item %d = %q, want T%d", i, j, got, next)
			}
			next++
		}
		if p.Has("track[" + strconv.Itoa(want) + "]") {
			t.Fatalf("batch %d larger than %d", i, want)
		}
	}
	waitFor(t, "empty queue", func() bool { return s.Status().Queued == 0 && queueLines(t, queue) == 0 })
}

func TestScrobblerIgnoredItemsDropped(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", func(url.Values) (int, string) {
		return 200, `{"scrobbles":{"scrobble":[{"ignoredMessage":{"code":"0","#text":""}},{"ignoredMessage":{"code":"1","#text":"Artist was ignored"}}],"@attr":{"accepted":1,"ignored":1}}}`
	})
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	ts := clk.Now().Add(-time.Hour).Unix()
	writeQueueFile(t, queue,
		fmt.Sprintf(`{"artist":"A","track":"Good","timestamp":%d}`, ts),
		fmt.Sprintf(`{"artist":"Spam","track":"Bad","timestamp":%d}`, ts+300))
	s := newTestScrobbler(t, f, clk, queue)
	waitFor(t, "queue drained", func() bool { return s.Status().Queued == 0 })
	if n := len(f.callsTo("track.scrobble")); n != 1 {
		t.Fatalf("ignored item retried: %d calls", n)
	}
	if st := s.Status(); st.State != StateOK || !strings.Contains(st.LastError, "Artist was ignored") {
		t.Errorf("status: %+v", st)
	}
}

func TestScrobblerRetryBackoff(t *testing.T) {
	f := newFakeAPI(t)
	var fail atomic.Bool
	fail.Store(true)
	f.handle("track.scrobble", func(p url.Values) (int, string) {
		if fail.Load() {
			return 200, `{"error":11,"message":"Service Offline"}`
		}
		return okBatch(p)
	})
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	writeQueueFile(t, queue, fmt.Sprintf(`{"artist":"A","track":"T","timestamp":%d}`, clk.Now().Add(-time.Hour).Unix()))
	s := newTestScrobbler(t, f, clk, queue)

	waitFor(t, "first attempt", func() bool { return len(f.callsTo("track.scrobble")) == 1 })
	waitFor(t, "30 s backoff", func() bool { return clk.waiting(30 * time.Second) })
	if st := s.Status(); st.State != StateOffline || st.Queued != 1 || st.LastError == "" {
		t.Fatalf("status after failure: %+v", st)
	}
	clk.Advance(29 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if n := len(f.callsTo("track.scrobble")); n != 1 {
		t.Fatalf("retried early: %d", n)
	}
	clk.Advance(time.Second)
	waitFor(t, "second attempt", func() bool { return len(f.callsTo("track.scrobble")) == 2 })
	waitFor(t, "60 s backoff", func() bool { return clk.waiting(60 * time.Second) })

	fail.Store(false)
	clk.Advance(60 * time.Second)
	waitFor(t, "success", func() bool { st := s.Status(); return st.Queued == 0 && st.State == StateOK })
}

func TestScrobblerBackoffCaps(t *testing.T) {
	d := time.Duration(0)
	for range 20 {
		d = min(max(backoffMin, d*2), backoffMax)
	}
	if d != 30*time.Minute {
		t.Fatalf("backoff cap = %v", d)
	}
}

func TestScrobblerReauthKeepsQueue(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", func(p url.Values) (int, string) {
		if p.Get("sk") != "fresh" {
			return 200, `{"error":9,"message":"Invalid session key - Please re-authenticate"}`
		}
		return okBatch(p)
	})
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	writeQueueFile(t, queue, fmt.Sprintf(`{"artist":"A","track":"T","timestamp":%d}`, clk.Now().Add(-time.Hour).Unix()))
	s := newTestScrobbler(t, f, clk, queue)
	waitFor(t, "reauth", func() bool { return s.Status().State == StateReauth })
	clk.Advance(time.Hour)
	listen(s, clk, "P1", "Song", 100, 200) // new plays still queue up
	time.Sleep(20 * time.Millisecond)
	if n := len(f.callsTo("track.scrobble")); n != 1 {
		t.Fatalf("kept sending with a dead session: %d calls", n)
	}
	if n := len(f.callsTo("track.updateNowPlaying")); n != 0 {
		t.Fatalf("now playing sent with a dead session: %d", n)
	}
	if st := s.Status(); st.Queued != 2 {
		t.Fatalf("queue: %+v", st)
	}
	c := testCreds
	c.SessionKey = "fresh"
	s.SetCredentials(c)
	waitFor(t, "sent after reauth", func() bool { st := s.Status(); return st.Queued == 0 && st.State == StateOK })
}

func TestScrobblerConfigError(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", func(url.Values) (int, string) {
		return 403, `{"error":26,"message":"Suspended API key"}`
	})
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	writeQueueFile(t, queue, fmt.Sprintf(`{"artist":"A","track":"T","timestamp":%d}`, clk.Now().Add(-time.Hour).Unix()))
	s := newTestScrobbler(t, f, clk, queue)
	waitFor(t, "config state", func() bool { return s.Status().State == StateConfig })
	if s.Status().Queued != 1 {
		t.Fatal("config error dropped the queue")
	}
}

func TestScrobblerRequestErrorDropsOnlyTheBadPlay(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", func(p url.Values) (int, string) {
		for i := 0; p.Has("artist[" + strconv.Itoa(i) + "]"); i++ {
			if p.Get("track["+strconv.Itoa(i)+"]") == "Bad" {
				return 200, `{"error":6,"message":"Invalid parameters"}`
			}
		}
		return okBatch(p)
	})
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	ts := clk.Now().Add(-time.Hour).Unix()
	writeQueueFile(t, queue,
		fmt.Sprintf(`{"artist":"A","track":"One","timestamp":%d}`, ts),
		fmt.Sprintf(`{"artist":"A","track":"Bad","timestamp":%d}`, ts+1),
		fmt.Sprintf(`{"artist":"A","track":"Three","timestamp":%d}`, ts+2))
	s := newTestScrobbler(t, f, clk, queue)
	waitFor(t, "queue drained", func() bool { return s.Status().Queued == 0 })
	var sent []string
	for _, c := range f.callsTo("track.scrobble") {
		if !c.Has("artist[1]") {
			sent = append(sent, c.Get("track[0]"))
		}
	}
	if strings.Join(sent, ",") != "One,Bad,Three" {
		t.Fatalf("one-by-one retries: %v", sent)
	}
}

func TestScrobblerQueueSurvivesRestart(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", func(url.Values) (int, string) { return 503, `{"error":16,"message":"Temporarily unavailable"}` })
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	s := NewScrobbler(testCreds, queue, clk, WithAPIRoot(f.root()), WithTick(0))
	listen(s, clk, "P1", "Song", 100, 200)
	waitFor(t, "write-ahead", func() bool { return queueLines(t, queue) == 1 })
	waitFor(t, "failed attempt", func() bool { return s.Status().State == StateOffline })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	f.handle("track.scrobble", okBatch)
	s2 := newTestScrobbler(t, f, clk, queue)
	waitFor(t, "sent after restart", func() bool { return s2.Status().Queued == 0 && queueLines(t, queue) == 0 })
	calls := f.callsTo("track.scrobble")
	if last := calls[len(calls)-1]; last.Get("track[0]") != "Song" {
		t.Fatalf("resent: %v", last)
	}
}

func TestScrobblerDisabled(t *testing.T) {
	f := newFakeAPI(t)
	clk := newFakeClock()
	s := NewScrobbler(Credentials{APIKey: "k", Secret: testSecret}, "", clk, WithAPIRoot(f.root()), WithTick(0))
	defer s.Close()
	listen(s, clk, "P1", "Song", 100, 200)
	if st := s.Status(); st.Enabled || st.Queued != 0 {
		t.Fatalf("no session key: %+v", st)
	}
	s.SetCredentials(testCreds)
	s.SetEnabled(false)
	listen(s, clk, "P2", "Song", 100, 200)
	if st := s.Status(); st.Enabled || st.Queued != 0 {
		t.Fatalf("disabled: %+v", st)
	}
	time.Sleep(20 * time.Millisecond)
	if len(f.callsTo("track.updateNowPlaying")) != 0 {
		t.Fatal("disabled scrobbler made calls")
	}
}

func TestScrobblerUpdates(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", okBatch)
	clk := newFakeClock()
	s := newTestScrobbler(t, f, clk, "")
	listen(s, clk, "P1", "Song", 100, 200)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case st := <-s.Updates():
			if !st.LastScrobble.IsZero() {
				return
			}
		case <-deadline:
			t.Fatal("no update with LastScrobble")
		}
	}
}

// pending reports whether anything is waiting on the clock.
func (c *fakeClock) pending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters) > 0
}

func TestScrobblerTransientErrorsKeepEveryPlay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"error 8", 200, `{"error":8,"message":"Operation failed - Most likely the backend service failed. Please try again."}`},
		{"5xx with a payload code", 503, `{"error":6,"message":"Invalid parameters"}`},
		{"5xx plain text", 502, `Bad Gateway`},
		{"unknown code", 200, `{"error":3,"message":"Invalid Method"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t)
			var fails atomic.Int32
			f.handle("track.scrobble", func(p url.Values) (int, string) {
				if fails.Add(1) <= 3 {
					return tc.status, tc.body
				}
				return okBatch(p)
			})
			clk := newFakeClock()
			queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
			base := clk.Now().Add(-48 * time.Hour).Unix()
			var lines []string
			for i := range 200 {
				lines = append(lines, fmt.Sprintf(`{"artist":"A","track":"T%d","timestamp":%d}`, i, base+int64(i)*300))
			}
			writeQueueFile(t, queue, lines...)
			s := newTestScrobbler(t, f, clk, queue)

			waitFor(t, "first failure", func() bool { return s.Status().State == StateOffline })
			if st := s.Status(); st.Queued != 200 {
				t.Fatalf("a transient error dropped plays: %+v", st)
			}
			deadline := time.Now().Add(5 * time.Second)
			for s.Status().Queued > 0 {
				if time.Now().After(deadline) {
					t.Fatalf("queue never drained: %+v", s.Status())
				}
				if clk.pending() {
					clk.Advance(backoffMax)
				}
				time.Sleep(time.Millisecond)
			}
			sent := map[string]bool{}
			for _, c := range f.callsTo("track.scrobble") {
				for i := 0; c.Has("track[" + strconv.Itoa(i) + "]"); i++ {
					sent[c.Get("track["+strconv.Itoa(i)+"]")] = true
				}
			}
			for i := range 200 {
				if !sent["T"+strconv.Itoa(i)] {
					t.Fatalf("T%d never sent", i)
				}
			}
			for _, c := range f.callsTo("track.scrobble") {
				if !c.Has("artist[1]") {
					t.Fatalf("transient error fell back to one-by-one sends: %v", c.Get("track[0]"))
				}
			}
			if st := s.Status(); st.State != StateOK || strings.Contains(st.LastError, "dropped") {
				t.Fatalf("status: %+v", st)
			}
		})
	}
}

func TestScrobblerAuthFailedNeedsReauth(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", func(url.Values) (int, string) {
		return 200, `{"error":4,"message":"Authentication Failed - You do not have permissions to access the service"}`
	})
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	ts := clk.Now().Add(-time.Hour).Unix()
	writeQueueFile(t, queue,
		fmt.Sprintf(`{"artist":"A","track":"One","timestamp":%d}`, ts),
		fmt.Sprintf(`{"artist":"A","track":"Two","timestamp":%d}`, ts+1))
	s := newTestScrobbler(t, f, clk, queue)
	waitFor(t, "reauth", func() bool { return s.Status().State == StateReauth })
	time.Sleep(20 * time.Millisecond)
	if n := len(f.callsTo("track.scrobble")); n != 1 {
		t.Fatalf("kept sending after error 4: %d calls", n)
	}
	if st := s.Status(); st.Queued != 2 {
		t.Fatalf("error 4 dropped plays: %+v", st)
	}
}

func TestScrobblerStartsDisabled(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", okBatch)
	clk := newFakeClock()
	queue := filepath.Join(t.TempDir(), "scrobbles.jsonl")
	writeQueueFile(t, queue, fmt.Sprintf(`{"artist":"A","track":"T","timestamp":%d}`, clk.Now().Add(-time.Hour).Unix()))
	s := NewScrobbler(testCreds, queue, clk, WithAPIRoot(f.root()), WithTick(0), WithEnabled(false))
	t.Cleanup(func() { s.Close() })
	time.Sleep(50 * time.Millisecond)
	if n := len(f.callsTo("track.scrobble")); n != 0 {
		t.Fatalf("leftover queue flushed while disabled: %d calls", n)
	}
	if st := s.Status(); st.Enabled || st.Queued != 1 {
		t.Fatalf("status: %+v", st)
	}
	s.SetEnabled(true)
	waitFor(t, "sent once enabled", func() bool { return s.Status().Queued == 0 })
}

func TestScrobblerInitCredentials(t *testing.T) {
	f := newFakeAPI(t)
	f.handle("track.scrobble", okBatch)
	clk := newFakeClock()
	s := NewScrobbler(Credentials{}, "", clk, WithAPIRoot(f.root()), WithTick(0))
	t.Cleanup(func() { s.Close() })

	s.InitCredentials(testCreds)
	if st := s.Status(); !st.Enabled || st.User != "bogdan" {
		t.Fatalf("background load not applied: %+v", st)
	}

	// Setup or disconnect wins over a Keychain read that finishes late.
	s.SetCredentials(Credentials{})
	s.InitCredentials(testCreds)
	if st := s.Status(); st.Enabled || st.User != "" {
		t.Fatalf("late background load overrode SetCredentials: %+v", st)
	}
}

// With tracking off or last.fm not set up the tick loop sleeps until woken,
// instead of waking every second to find nothing to do.
func TestTickLoopSleepsWhileOffOrUnconfigured(t *testing.T) {
	clk := newFakeClock()
	f := newFakeAPI(t)
	q := filepath.Join(t.TempDir(), "scrobbles.jsonl")

	s := NewScrobbler(Credentials{}, q, clk, WithAPIRoot(f.root()), WithTick(5*time.Millisecond))
	t.Cleanup(func() { s.Close() })
	time.Sleep(60 * time.Millisecond)
	if n := s.ticks.Load(); n != 0 {
		t.Fatalf("%d ticks without credentials", n)
	}
	s.SetCredentials(testCreds)
	waitTicks(t, s, 3)

	s.SetEnabled(false)
	time.Sleep(20 * time.Millisecond) // a tick already in flight may land
	n := s.ticks.Load()
	time.Sleep(60 * time.Millisecond)
	if got := s.ticks.Load(); got != n {
		t.Fatalf("%d ticks while disabled", got-n)
	}
	s.SetEnabled(true)
	waitTicks(t, s, n+3)
}

func waitTicks(t *testing.T, s *Scrobbler, n int64) {
	t.Helper()
	for range 200 {
		if s.ticks.Load() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ticks stuck at %d, want %d", s.ticks.Load(), n)
}

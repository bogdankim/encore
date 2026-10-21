package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary doubles as a fake bridge: with ENCORE_TEST_BRIDGE=1 it
// serves the JSON-lines protocol on stdin/stdout instead of running tests.
func TestMain(m *testing.M) {
	if os.Getenv("ENCORE_TEST_BRIDGE") == "1" {
		runTestBridge()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runTestBridge() {
	// Die on arrival once, to exercise a child that exits before it's installed.
	if f := os.Getenv("ENCORE_TEST_BRIDGE_DIE_ONCE"); f != "" && os.Remove(f) == nil {
		os.Exit(1)
	}
	var wmu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	write := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
		wmu.Unlock()
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<24)
	for in.Scan() {
		var req struct {
			ID       int64          `json:"id"`
			Method   string         `json:"method"`
			Params   map[string]any `json:"params"`
			Deadline int64          `json:"deadline"`
		}
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			fmt.Fprintln(os.Stderr, "bad request:", err)
			continue
		}
		if req.Params == nil {
			req.Params = map[string]any{}
		}
		req.Params["_deadline"] = float64(req.Deadline)
		run := func() {
			// Like Server.swift: skip a request whose deadline passed in the queue.
			if req.Deadline > 0 && time.Now().UnixMilli() > req.Deadline {
				fmt.Fprintln(os.Stderr, req.Method, "expired in the queue; skipped")
				write(map[string]any{"id": req.ID, "error": &Error{Code: ErrExpired, Message: req.Method + " expired"}})
				return
			}
			result, rerr := testBridgeHandle(req.Method, req.Params, func(ev string, data any) {
				write(map[string]any{"event": ev, "data": data})
			})
			if rerr == errNoReply {
				return
			}
			if rerr != nil {
				write(map[string]any{"id": req.ID, "error": rerr})
				return
			}
			write(map[string]any{"id": req.ID, "result": result})
		}
		// test.serial.* share one queue, like the bridge's Music queue.
		if strings.HasPrefix(req.Method, "test.serial.") {
			serial <- run
		} else {
			go run()
		}
	}
}

// serial runs test.serial.* requests one at a time, in order.
var serial = func() chan func() {
	ch := make(chan func(), 64)
	go func() {
		for f := range ch {
			f()
		}
	}()
	return ch
}()

var serialRuns atomic.Int64

var errNoReply = &Error{Code: 99, Message: "no reply"}

func testBridgeHandle(method string, params map[string]any, emit func(string, any)) (any, *Error) {
	num := func(k string) float64 { f, _ := params[k].(float64); return f }
	str := func(k string) string { s, _ := params[k].(string); return s }
	switch method {
	case "hello":
		return Hello{Version: "1", MusicRunning: true, Automation: "granted", Library: "granted"}, nil
	case "test.echo":
		time.Sleep(time.Duration(num("delayMs")) * time.Millisecond)
		return map[string]any{"value": params["value"]}, nil
	case "test.deadline":
		return map[string]any{"deadline": params["_deadline"]}, nil
	case "test.serial.sleep":
		time.Sleep(time.Duration(num("delayMs")) * time.Millisecond)
		return struct{}{}, nil
	case "test.serial.inc":
		serialRuns.Add(1)
		return struct{}{}, nil
	case "test.serial.count":
		return map[string]any{"n": serialRuns.Load()}, nil
	case "test.pid":
		return map[string]any{"pid": os.Getpid()}, nil
	case "test.responsible":
		return map[string]any{"pid": os.Getpid(), "responsible": responsiblePID(os.Getpid())}, nil
	case "test.hang":
		return nil, errNoReply
	case "test.permissions":
		emit("permissions", map[string]any{"automation": str("automation"), "library": "granted"})
		return struct{}{}, nil
	case "test.consent":
		return nil, &Error{Code: ErrAutomationConsent, Message: "waiting for permission to control Music"}
	case "test.crash":
		fmt.Fprintln(os.Stderr, "crashing on purpose")
		os.Exit(3)
	case "test.stderr":
		fmt.Fprintln(os.Stderr, str("text"))
		return struct{}{}, nil
	case "test.emit":
		for i := range int(num("n")) {
			emit("player", NowPlaying{State: Playing, Position: float64(i), Volume: 40, Repeat: RepeatOff,
				Track: &CurrentTrack{PID: "AAAA000000000001", Name: "Song", Duration: 200}})
		}
		emit("airplay", map[string]any{"devices": []AirPlayDevice{{ID: 7, Name: "Kitchen", Kind: "speaker"}}})
		emit("music", map[string]any{"running": false})
		emit("library", map[string]any{})
		emit("permissions", map[string]any{"automation": "granted", "library": "denied"})
		emit("mystery", map[string]any{})
		return struct{}{}, nil
	case "player.seek":
		if num("seconds") < 0 {
			return nil, &Error{Code: 1, Message: "seconds must be >= 0"}
		}
		return struct{}{}, nil
	case "player.playTracks":
		if _, ok := params["containers"].(map[string]any); !ok {
			return nil, &Error{Code: 1, Message: "containers missing"}
		}
		return struct{}{}, nil
	case "player.playStore":
		ids, ok := params["ids"].([]any)
		start, isNum := params["start"].(float64)
		if !ok || len(ids) == 0 || !isNum {
			return nil, &Error{Code: 1, Message: "ids and start required"}
		}
		if _, ok := ids[0].(string); !ok {
			return nil, &Error{Code: 1, Message: "ids must be strings"}
		}
		if ids[0] == "0" {
			return nil, &Error{Code: ErrStore, Message: "Music didn't take the catalog songs (CommandIgnored)"}
		}
		return map[string]any{"ids": ids, "start": start}, nil
	case "player.playStation":
		id, ok := params["id"].(string)
		if !ok || !strings.HasPrefix(id, "ra.") {
			return nil, &Error{Code: 1, Message: "bad station id"}
		}
		if id == "ra.0" {
			return nil, &Error{Code: ErrStore, Message: "Music didn't take the station (CommandIgnored)"}
		}
		return map[string]any{"id": id}, nil
	case "track.lyrics":
		return nil, &Error{Code: ErrNotFound, Message: "Can't get track."}
	case "airplay.list":
		return map[string]any{"devices": []AirPlayDevice{{ID: 1, Name: "MacBook Pro", Kind: "computer", Selected: true}}}, nil
	case "library.snapshot":
		snap := Snapshot{GeneratedAt: 1, Tracks: []SnapshotTrack{{PID: "AAAA000000000001", Title: "Song"}},
			Playlists: []SnapshotPlaylist{{PID: "BBBB000000000001", Name: "Mix", Items: []string{"AAAA000000000001"}}}}
		b, _ := json.Marshal(snap)
		if err := os.WriteFile(str("path"), b, 0o644); err != nil {
			return nil, &Error{Code: 3, Message: err.Error()}
		}
		return map[string]any{"tracks": 1, "playlists": 1, "ms": 2}, nil
	case "artwork.get", "playlist.artwork":
		if str("pid") == "FAIL" {
			return nil, &Error{Code: -600, Message: "Music isn't running"}
		}
		if str("pid") == "NOART" {
			return map[string]any{"path": ""}, nil
		}
		n := int(num("size"))
		f, err := os.Create(str("path"))
		if err != nil {
			return nil, &Error{Code: 3, Message: err.Error()}
		}
		png.Encode(f, image.NewGray(image.Rect(0, 0, n, n)))
		f.Close()
		return map[string]any{"path": str("path"), "w": n, "h": n}, nil
	}
	return nil, &Error{Code: 2, Message: "unknown method " + method}
}

func startTest(t *testing.T, env ...string) *Process {
	return startTestOpts(t, Options{}, env...)
}

func startTestOpts(t *testing.T, o Options, env ...string) *Process {
	t.Helper()
	dir := t.TempDir()
	o.Path = os.Args[0]
	o.Env = append(append(os.Environ(), "ENCORE_TEST_BRIDGE=1", "GORACE=atexit_sleep_ms=0"), env...)
	o.CacheDir = dir
	o.LogPath = filepath.Join(dir, "logs", "bridge.log")
	o.backoff = func(int) time.Duration { return 20 * time.Millisecond }
	p, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestProcessHello(t *testing.T) {
	p := startTest(t)
	h, err := p.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != "1" || !h.MusicRunning || h.Automation != "granted" {
		t.Fatalf("hello = %+v", h)
	}
}

// The bridge is spawned as its own TCC responsible process, so macOS asks
// about encore's permissions instead of the terminal's; ENCORE_INHERIT_TCC=1
// turns that off.
func TestProcessDisclaimsResponsibility(t *testing.T) {
	if responsiblePID(os.Getpid()) < 0 {
		t.Skip("no responsibility API on this system")
	}
	for _, inherit := range []bool{false, true} {
		inheritTCC = inherit
		p := startTest(t)
		var res struct{ Pid, Responsible int }
		if err := p.call(context.Background(), DefaultTimeout, "test.responsible", nil, &res); err != nil {
			t.Fatal(err)
		}
		p.Close()
		if self := res.Responsible == res.Pid; self == inherit {
			t.Errorf("inherit %v: bridge pid %d is responsible for itself: %v (responsible %d)", inherit, res.Pid, self, res.Responsible)
		}
	}
	inheritTCC = false
}

func TestProcessConcurrentCalls(t *testing.T) {
	p := startTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := range 200 {
		wg.Go(func() {
			var res struct {
				Value string `json:"value"`
			}
			want := fmt.Sprintf("v%d", i)
			err := p.call(context.Background(), DefaultTimeout, "test.echo",
				map[string]any{"value": want, "delayMs": rand.IntN(20)}, &res)
			if err == nil && res.Value != want {
				err = fmt.Errorf("got %q, want %q", res.Value, want)
			}
			if err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestProcessErrors(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()

	err := p.Seek(ctx, -1)
	var be *Error
	if !errors.As(err, &be) || be.Code != 1 {
		t.Fatalf("seek -1: %v", err)
	}
	if _, err := p.Lyrics(ctx, "X"); !errors.As(err, &be) || be.Code != ErrNotFound || be.Message != "Can't get track." {
		t.Fatalf("lyrics: %v", err)
	}
	if err := p.call(ctx, DefaultTimeout, "nope", nil, nil); !errors.As(err, &be) || be.Code != 2 {
		t.Fatalf("unknown method: %v", err)
	}
	if err := p.Seek(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := p.PlayTracks(ctx, []string{"A"}, nil); err != nil {
		t.Fatalf("playTracks with nil containers: %v", err)
	}
}

func TestProcessPlayStore(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	if err := p.PlayStore(ctx, []string{"1440942209", "1440942220"}, 1); err != nil {
		t.Fatal(err)
	}
	var res struct {
		IDs   []string `json:"ids"`
		Start int      `json:"start"`
	}
	if err := p.call(ctx, DefaultTimeout, "player.playStore", map[string]any{"ids": []string{"1440942209", "1440942220"}, "start": 1}, &res); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.IDs, []string{"1440942209", "1440942220"}) || res.Start != 1 {
		t.Fatalf("bridge saw %+v", res)
	}
	var be *Error
	if err := p.PlayStore(ctx, []string{"0"}, 0); !errors.As(err, &be) || be.Code != ErrStore || !strings.Contains(be.Message, "CommandIgnored") {
		t.Fatalf("refused: %v", err)
	}
}

func TestProcessPlayStation(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	if err := p.PlayStation(ctx, "ra.978194965"); err != nil {
		t.Fatal(err)
	}
	var be *Error
	if err := p.PlayStation(ctx, "pl.pm-1234"); !errors.As(err, &be) || be.Code != 1 {
		t.Fatalf("bad id: %v", err)
	}
	if err := p.PlayStation(ctx, "ra.0"); !errors.As(err, &be) || be.Code != ErrStore || !strings.Contains(be.Message, "CommandIgnored") {
		t.Fatalf("refused: %v", err)
	}
}

func TestProcessTimeout(t *testing.T) {
	p := startTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := p.call(ctx, DefaultTimeout, "test.hang", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
	// Default timeout applies when ctx has none.
	start = time.Now()
	err = p.call(context.Background(), 80*time.Millisecond, "test.hang", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("default timeout: %v after %v", err, time.Since(start))
	}
	// The client is still fine afterwards.
	if _, err := p.Hello(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProcessEvents(t *testing.T) {
	p := startTest(t)
	if err := p.call(context.Background(), DefaultTimeout, "test.emit", map[string]any{"n": 3}, nil); err != nil {
		t.Fatal(err)
	}
	var got []Event
	timeout := time.After(2 * time.Second)
	for len(got) < 7 {
		select {
		case ev := <-p.Events():
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("got %d events: %#v", len(got), got)
		}
	}
	for i := range 3 {
		pe, ok := got[i].(PlayerEvent)
		if !ok || pe.NowPlaying.Position != float64(i) || pe.NowPlaying.Track == nil || pe.NowPlaying.Track.Duration != 200 {
			t.Fatalf("event %d = %#v", i, got[i])
		}
	}
	if ae, ok := got[3].(AirPlayEvent); !ok || len(ae.Devices) != 1 || ae.Devices[0].Name != "Kitchen" {
		t.Fatalf("airplay = %#v", got[3])
	}
	if me, ok := got[4].(MusicEvent); !ok || me.Running {
		t.Fatalf("music = %#v", got[4])
	}
	if _, ok := got[5].(LibraryEvent); !ok {
		t.Fatalf("library = %#v", got[5])
	}
	if pe, ok := got[6].(PermissionsEvent); !ok || pe != (PermissionsEvent{Automation: "granted", Library: "denied"}) {
		t.Fatalf("permissions = %#v", got[6])
	}
}

func TestProcessCrashRestart(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	pid := func() int {
		var res struct{ Pid int }
		if err := p.call(ctx, DefaultTimeout, "test.pid", nil, &res); err != nil {
			t.Fatal(err)
		}
		return res.Pid
	}
	first := pid()

	// A call in flight when the bridge dies fails with ErrExited.
	hung := make(chan error, 1)
	go func() { hung <- p.call(ctx, 5*time.Second, "test.hang", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	if err := p.call(ctx, DefaultTimeout, "test.crash", nil, nil); !errors.Is(err, ErrExited) {
		t.Fatalf("crash call: %v", err)
	}
	if err := <-hung; !errors.Is(err, ErrExited) {
		t.Fatalf("pending call: %v", err)
	}

	select {
	case ev := <-p.Events():
		ee, ok := ev.(ExitEvent)
		if !ok || !errors.Is(ee.Err, ErrExited) || !strings.Contains(ee.Err.Error(), "exit status 3") {
			t.Fatalf("event = %#v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ExitEvent")
	}

	// Calls made while restarting wait for the new process.
	if second := pid(); second == first || second == 0 {
		t.Fatalf("pid after restart = %d (was %d)", second, first)
	}
	if _, err := p.Hello(ctx); err != nil {
		t.Fatal(err)
	}

	log, err := os.ReadFile(p.opts.LogPath)
	if err != nil || !strings.Contains(string(log), "crashing on purpose") {
		t.Fatalf("log: %v\n%s", err, log)
	}
}

// A bridge found by Locate is located again on restart, so it follows a file
// another encore replaced meanwhile. ENCORE_BRIDGE stands in for extraction.
func TestProcessRestartRelocates(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, l := range []string{a, b} {
		if err := os.Symlink(os.Args[0], l); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ENCORE_BRIDGE", a)
	p, err := Start(context.Background(), Options{
		Env:      append(os.Environ(), "ENCORE_TEST_BRIDGE=1", "GORACE=atexit_sleep_ms=0"),
		CacheDir: dir,
		LogPath:  filepath.Join(dir, "bridge.log"),
		backoff:  func(int) time.Duration { return 20 * time.Millisecond },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	ctx := context.Background()
	if _, err := p.Hello(ctx); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ENCORE_BRIDGE", b)
	if err := p.call(ctx, DefaultTimeout, "test.crash", nil, nil); !errors.Is(err, ErrExited) {
		t.Fatalf("crash call: %v", err)
	}
	if _, err := p.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(p.opts.LogPath)
	if !strings.Contains(string(log), "bridge started: "+a+" ") || !strings.Contains(string(log), "bridge started: "+b+" ") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestProcessClose(t *testing.T) {
	p := startTest(t)
	hung := make(chan error, 1)
	go func() { hung <- p.call(context.Background(), 5*time.Second, "test.hang", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("Close took %v", d)
	}
	if err := <-hung; !errors.Is(err, ErrClosed) {
		t.Fatalf("pending call: %v", err)
	}
	for ev := range p.Events() {
		if _, ok := ev.(ExitEvent); ok {
			t.Fatalf("ExitEvent after Close: %#v", ev)
		}
	}
	if _, err := p.Hello(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("hello after close: %v", err)
	}
	p.Close()
}

func TestProcessSnapshotAndArtwork(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	snap, err := p.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tracks) != 1 || snap.Tracks[0].Title != "Song" || snap.Playlists[0].Items[0] != "AAAA000000000001" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if left, _ := filepath.Glob(filepath.Join(p.opts.CacheDir, "snapshot-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}

	a, err := p.Artwork(ctx, "ABCD", 64)
	if err != nil {
		t.Fatal(err)
	}
	if a.W != 64 || a.H != 64 || a.Path != filepath.Join(p.opts.CacheDir, "art", "ABCD-64.png") {
		t.Fatalf("artwork = %+v", a)
	}
	// Second call is served from disk.
	b, err := p.Artwork(ctx, "ABCD", 64)
	if err != nil || b != a {
		t.Fatalf("cached artwork = %+v, %v", b, err)
	}
	if a, err := p.Artwork(ctx, "NOART", 64); err != nil || a.Path != "" {
		t.Fatalf("no artwork = %+v, %v", a, err)
	}

	// "no artwork" is remembered for NoArtworkTTL, then asked again: a track
	// that has just started can have none until Music fetches it
	p.artMu.Lock()
	p.noArt["LATE-64"] = time.Now()
	p.artMu.Unlock()
	if a, err := p.Artwork(ctx, "LATE", 64); err != nil || a.Path != "" {
		t.Fatalf("remembered no artwork = %+v, %v", a, err)
	}
	p.artMu.Lock()
	p.noArt["LATE-64"] = time.Now().Add(-NoArtworkTTL - time.Second)
	p.artMu.Unlock()
	if a, err := p.Artwork(ctx, "LATE", 64); err != nil || a.Path == "" {
		t.Fatalf("after the TTL = %+v, %v; want the bridge asked again", a, err)
	}
}

func TestProcessPlaylistArtwork(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	dir := filepath.Join(p.opts.CacheDir, "art")

	a, err := p.PlaylistArtwork(ctx, "ABCD", 64)
	if err != nil || a.W != 64 || a.Path != filepath.Join(dir, "pl-ABCD-64.png") {
		t.Fatalf("playlist artwork = %+v, %v", a, err)
	}
	// A track or album with the same pid has its own file.
	if b, err := p.Artwork(ctx, "ABCD", 64); err != nil || b.Path == a.Path {
		t.Fatalf("track artwork = %+v, %v", b, err)
	}

	// A cover older than the TTL is asked for again.
	old := time.Now().Add(-PlaylistArtTTL - time.Hour)
	if err := os.Chtimes(a.Path, old, old); err != nil {
		t.Fatal(err)
	}
	if b, err := p.PlaylistArtwork(ctx, "ABCD", 64); err != nil || b != a || fileAge(a.Path) > time.Minute {
		t.Fatalf("refreshed = %+v, %v, age %v", b, err, fileAge(a.Path))
	}

	// Stale but the bridge can't answer: the old file is better than nothing.
	stale := filepath.Join(dir, "pl-FAIL-64.png")
	if err := os.Rename(a.Path, stale); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if b, err := p.PlaylistArtwork(ctx, "FAIL", 64); err != nil || b.Path != stale {
		t.Fatalf("stale = %+v, %v", b, err)
	}
	if _, err := p.PlaylistArtwork(ctx, "FAIL", 32); err == nil {
		t.Fatal("no file and no answer: want the error")
	}

	// Stale and the playlist no longer has a cover: the file goes.
	gone := filepath.Join(dir, "pl-NOART-64.png")
	if err := os.Rename(stale, gone); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(gone, old, old); err != nil {
		t.Fatal(err)
	}
	if b, err := p.PlaylistArtwork(ctx, "NOART", 64); err != nil || b.Path != "" {
		t.Fatalf("no cover = %+v, %v", b, err)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("stale cover kept: %v", err)
	}
}

func TestRestartDelay(t *testing.T) {
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, w := range want {
		if d := restartDelay(i); d != w {
			t.Errorf("restartDelay(%d) = %v, want %v", i, d, w)
		}
	}
}

func TestEventQueueDropsOldPlayerEvents(t *testing.T) {
	q := newEventQueue(8)
	for i := range 100 {
		q.push(PlayerEvent{NowPlaying: NowPlaying{Position: float64(i)}})
		if i%10 == 0 {
			q.push(MusicEvent{Running: true})
		}
	}
	var players []float64
	music := 0
	deadline := time.After(time.Second)
	for done := false; !done; {
		select {
		case ev := <-q.out:
			switch ev := ev.(type) {
			case PlayerEvent:
				players = append(players, ev.NowPlaying.Position)
			case MusicEvent:
				music++
			}
		case <-time.After(50 * time.Millisecond):
			done = true
		case <-deadline:
			done = true
		}
	}
	if music != 10 {
		t.Errorf("music events = %d, want 10", music)
	}
	if len(players) == 0 || players[len(players)-1] != 99 {
		t.Errorf("player events = %v, want the newest last", players)
	}
	if len(players) > 20 {
		t.Errorf("kept %d player events", len(players))
	}
	q.close()
	if _, ok := <-q.out; ok {
		t.Error("out not closed")
	}
	q.push(LibraryEvent{}) // after close: ignored
}

func TestRotatingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x", "bridge.log")
	f, err := openRotatingFile(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strings.Repeat("a", 39) + "\n")
	for range 5 {
		f.Write(line)
	}
	f.Close()
	cur, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".1")
	if len(cur) != 40 || len(old) != 80 {
		t.Fatalf("current %d bytes, old %d bytes", len(cur), len(old))
	}
}

// A child that dies before Start (or supervise) installs it must not be
// handed out, and the restart must not close ready a second time.
func TestProcessChildDiesBeforeInstall(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "die-once")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var spawns atomic.Int32
	p := startTestOpts(t, Options{afterSpawn: func(c *conn) {
		if spawns.Add(1) == 1 {
			<-c.exited // the first child exits on arrival; let readLoop finish first
		}
	}}, "ENCORE_TEST_BRIDGE_DIE_ONCE="+marker)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	if n := spawns.Load(); n != 2 {
		t.Fatalf("spawned %d times, want 2", n)
	}
	// One more restart: the ready channel is still sound.
	if err := p.call(ctx, DefaultTimeout, "test.crash", nil, nil); !errors.Is(err, ErrExited) {
		t.Fatalf("crash: %v", err)
	}
	if _, err := p.Hello(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestProcessSendsDeadline(t *testing.T) {
	p := startTest(t)
	var res struct{ Deadline int64 }
	before := time.Now()
	if err := p.call(context.Background(), 2*time.Second, "test.deadline", nil, &res); err != nil {
		t.Fatal(err)
	}
	lo, hi := before.Add(2*time.Second).UnixMilli(), time.Now().Add(2*time.Second).UnixMilli()
	if res.Deadline < lo || res.Deadline > hi {
		t.Fatalf("deadline %d, want within [%d, %d]", res.Deadline, lo, hi)
	}
}

// Requests the client gave up on while the queue was stuck never run.
func TestProcessSkipsExpiredRequests(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	slow := make(chan error, 1)
	go func() {
		slow <- p.call(ctx, 2*time.Second, "test.serial.sleep", map[string]any{"delayMs": 300}, nil)
	}()
	time.Sleep(30 * time.Millisecond) // the sleep now holds the queue
	for range 2 {
		if err := p.call(ctx, 50*time.Millisecond, "test.serial.inc", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued call: %v", err)
		}
	}
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
	var res struct{ N int }
	if err := p.call(ctx, DefaultTimeout, "test.serial.count", nil, &res); err != nil {
		t.Fatal(err)
	}
	if res.N != 0 {
		t.Fatalf("%d expired requests ran anyway", res.N)
	}
	if err := p.call(ctx, DefaultTimeout, "test.serial.inc", nil, nil); err != nil {
		t.Fatalf("fresh request: %v", err)
	}
	log, _ := os.ReadFile(p.opts.LogPath)
	if !strings.Contains(string(log), "test.serial.inc expired") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestProcessRestartsAfterTimeouts(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	pid := func() int {
		var res struct{ Pid int }
		if err := p.call(ctx, DefaultTimeout, "test.pid", nil, &res); err != nil {
			t.Fatal(err)
		}
		return res.Pid
	}
	first := pid()

	// A reply in between resets the streak.
	for range maxTimeouts - 1 {
		p.call(ctx, 30*time.Millisecond, "test.hang", nil, nil)
	}
	p.Hello(ctx)
	for range maxTimeouts - 1 {
		p.call(ctx, 30*time.Millisecond, "test.hang", nil, nil)
	}
	if pid() != first {
		t.Fatal("restarted before maxTimeouts in a row")
	}

	for range maxTimeouts {
		if err := p.call(ctx, 30*time.Millisecond, "test.hang", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("hang: %v", err)
		}
	}
	select {
	case ev := <-p.Events():
		if _, ok := ev.(ExitEvent); !ok {
			t.Fatalf("event = %#v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no restart after timeouts")
	}
	if second := pid(); second == first || second == 0 {
		t.Fatalf("pid after restart = %d (was %d)", second, first)
	}
}

// While the bridge waits for the Automation prompt, calls that time out don't
// restart it: a new bridge would only ask again. A "pending" permissions event
// or a -1744 reply both mean waiting.
func TestProcessWaitsOutPermissionPrompt(t *testing.T) {
	p := startTest(t)
	ctx := context.Background()
	pid := func() int {
		var res struct{ Pid int }
		if err := p.call(ctx, DefaultTimeout, "test.pid", nil, &res); err != nil {
			t.Fatal(err)
		}
		return res.Pid
	}
	hangs := func(n int) {
		t.Helper()
		for range n {
			if err := p.call(ctx, 30*time.Millisecond, "test.hang", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("hang: %v", err)
			}
		}
	}
	noRestart := func(step string) {
		t.Helper()
		quiet := time.After(150 * time.Millisecond)
		for {
			select {
			case ev := <-p.Events():
				if _, ok := ev.(ExitEvent); ok {
					t.Fatalf("%s: restarted while permission was pending", step)
				}
				continue
			case <-quiet:
			}
			return
		}
	}
	first := pid()

	if err := p.call(ctx, DefaultTimeout, "test.permissions", map[string]any{"automation": AutomationPending}, nil); err != nil {
		t.Fatal(err)
	}
	hangs(maxTimeouts + 1)
	noRestart("pending event")

	// A -1744 reply is a reply, and it marks the wait too, after a granted
	// event cleared it.
	p.call(ctx, DefaultTimeout, "test.permissions", map[string]any{"automation": "granted"}, nil)
	var be *Error
	if err := p.call(ctx, DefaultTimeout, "test.consent", nil, nil); !errors.As(err, &be) || be.Code != ErrAutomationConsent {
		t.Fatalf("consent: %v", err)
	}
	hangs(maxTimeouts + 1)
	noRestart("-1744 reply")
	if pid() != first {
		t.Fatal("bridge restarted while permission was pending")
	}

	// Once permission is settled and the -1744s stop, the watchdog is back.
	p.call(ctx, DefaultTimeout, "test.permissions", map[string]any{"automation": "granted"}, nil)
	c, _ := p.live(ctx)
	c.mu.Lock()
	c.consentAt = time.Time{}
	c.mu.Unlock()
	hangs(maxTimeouts)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-p.Events():
			if _, ok := ev.(ExitEvent); !ok {
				continue
			}
		case <-deadline:
			t.Fatal("no restart after timeouts once permission was settled")
		}
		break
	}
}

// child.wait closes the parent's end of stdin too, so a crash-restart loop
// doesn't leak a descriptor per bridge.
func TestChildWaitClosesPipes(t *testing.T) {
	env := append(os.Environ(), "ENCORE_TEST_BRIDGE=1", "GORACE=atexit_sleep_ms=0")
	c, err := startChild(os.Args[0], nil, env, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	c.proc.Kill()
	c.wait()
	if _, err := c.stdin.Write([]byte("{}\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("stdin write after wait: %v, want os.ErrClosed", err)
	}
	if _, err := c.stdout.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("stdout read after wait: %v, want os.ErrClosed", err)
	}
}

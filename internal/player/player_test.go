package player

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

func onlyCall(t *testing.T, calls []call, name string) call {
	t.Helper()
	var found []call
	for _, c := range calls {
		if c.name == name {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %s call, got %d in %v", name, len(found), names(calls))
	}
	return found[0]
}

func names(calls []call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.name
	}
	return out
}

func noCalls(t *testing.T, calls []call) {
	t.Helper()
	if len(calls) != 0 {
		t.Fatalf("want no calls, got %v", names(calls))
	}
}

func TestPlayContextSlicesAt300(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(700)
	ts[20].InLibrary = false
	ts[20].Container = "PLAYLIST"
	r.p.PlayContext(Source{Kind: KindSongs}, ts, 10)

	c := onlyCall(t, r.calls(), "playTracks")
	if want := pidsOf(ts[10:310]); !slices.Equal(c.pids, want) {
		t.Fatalf("issued %d pids starting %s, want ts[10:310]", len(c.pids), c.pids[0])
	}
	if c.containers[ts[20].PID] != "PLAYLIST" || len(c.containers) != 1 {
		t.Fatalf("containers = %v", c.containers)
	}
	s := r.p.State()
	if !s.Loading || s.Track != ts[10] || s.Upcoming != 689 {
		t.Fatalf("snapshot loading=%v track=%v upcoming=%d", s.Loading, s.Track, s.Upcoming)
	}
	r.played(ts[10].PID)
	if r.p.State().Loading {
		t.Fatal("still loading after Music reported the track")
	}
}

func TestNextSliceIssuedAtEndOfSlice(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(400)
	r.p.PlayContext(Source{Kind: KindSongs}, ts, 0)
	r.calls()
	r.played(ts[0].PID)
	for i := 1; i < 300; i++ {
		r.playing(ts[i].PID, 0)
	}
	noCalls(t, r.calls())
	r.stoppedEv()
	c := onlyCall(t, r.calls(), "playTracks")
	if !slices.Equal(c.pids, pidsOf(ts[300:])) {
		t.Fatalf("next slice starts %s with %d pids", c.pids[0], len(c.pids))
	}
	r.played(ts[300].PID)
	for i := 301; i < 400; i++ {
		r.playing(ts[i].PID, 0)
	}
	r.stoppedEv()
	noCalls(t, r.calls()) // the end, repeat is off
}

func TestRepeatAllWrapsSlicedContext(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(400)
	r.p.CycleRepeat()
	if c := onlyCall(t, r.calls(), "repeat"); c.mode != bridge.RepeatAll {
		t.Fatalf("repeat mode %s", c.mode)
	}
	r.repeat = bridge.RepeatAll
	r.p.PlayContext(Source{Kind: KindSongs}, ts, 100)
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf(ts[100:400])) {
		t.Fatalf("first slice starts %s with %d pids", c.pids[0], len(c.pids))
	}
	r.played(ts[100].PID)
	for i := 101; i < 400; i++ {
		r.playing(ts[i].PID, 0)
	}
	noCalls(t, r.calls())
	// Music's repeat all wraps the slice to its start; our context wraps to 0.
	r.playing(ts[100].PID, 0)
	c := onlyCall(t, r.calls(), "playTracks")
	if c.pids[0] != ts[0].PID || len(c.pids) != SliceSize {
		t.Fatalf("wrap issued from %s with %d pids", c.pids[0], len(c.pids))
	}
}

func TestRepeatAllShortContextIssuesFullCycle(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(10)
	r.repeat = bridge.RepeatAll
	r.playing("", 0) // Music reports repeat all before we play
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 5)
	c := onlyCall(t, r.calls(), "playTracks")
	want := append(pidsOf(ts[5:]), pidsOf(ts[:5])...)
	if !slices.Equal(c.pids, want) {
		t.Fatalf("issued %v, want one full cycle from 5", c.pids)
	}
	r.played(ts[5].PID)
	for i := 6; i < 15; i++ {
		r.playing(ts[i%10].PID, 0)
	}
	r.playing(ts[5].PID, 0) // Music wraps the queue playlist: exactly our order
	noCalls(t, r.calls())
}

func TestRepeatAllWrapsAfterStop(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.repeat = bridge.RepeatAll
	r.playing("", 0)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)
	r.playing(ts[1].PID, 0)
	r.playing(ts[2].PID, 0)
	r.stoppedEv() // Music didn't wrap on its own
	if c := onlyCall(t, r.calls(), "playTracks"); c.pids[0] != ts[0].PID {
		t.Fatalf("restarted from %s", c.pids[0])
	}
}

func TestPrevious(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(400)
	r.p.PlayContext(Source{Kind: KindSongs}, ts, 300)
	r.calls()
	r.played(ts[300].PID)

	// Before the issued slice start: re-issue from there.
	r.p.Previous()
	if c := onlyCall(t, r.calls(), "playTracks"); c.pids[0] != ts[299].PID || len(c.pids) != 101 {
		t.Fatalf("previous issued from %s with %d pids", c.pids[0], len(c.pids))
	}
	r.played(ts[299].PID)
	r.playing(ts[300].PID, 0)

	// Inside the slice: Music's own previous.
	r.p.Previous()
	onlyCall(t, r.calls(), "previous")
	if s := r.p.State(); s.Track != ts[299] {
		t.Fatalf("track %v, want ts[299]", s.Track.Title)
	}
	r.playing(ts[299].PID, 0)

	// More than 3 s in: restart.
	r.playing(ts[299].PID, 40)
	r.p.Previous()
	if c := onlyCall(t, r.calls(), "seek"); c.f != 0 {
		t.Fatalf("seek to %v", c.f)
	}
}

func TestPreviousAtContextStartRestarts(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)
	r.p.Previous()
	onlyCall(t, r.calls(), "seek")
}

func TestShuffleOrderAndUnshuffle(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(10)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 3)
	r.calls()
	r.played(ts[3].PID)

	r.p.SetShuffle(true)
	noCalls(t, r.calls()) // nothing restarts mid-track
	up := r.p.Upcoming()
	others := append(slices.Clone(ts[:3]), ts[4:]...)
	if len(up) != 9 || slices.Contains(up, ts[3]) {
		t.Fatalf("shuffled upcoming has %d tracks", len(up))
	}
	sorted := slices.SortedFunc(slices.Values(up), func(a, b *library.Track) int { return comparePID(a, b) })
	if !slices.Equal(sorted, others) {
		t.Fatal("shuffle isn't a permutation of the other tracks")
	}
	if slices.Equal(up, others) {
		t.Fatal("shuffle kept the original order")
	}
	if !r.p.State().NowPlaying.Shuffle {
		t.Fatal("snapshot doesn't show shuffle on")
	}

	// Same seed, same order.
	r2 := newRig(t)
	ts2 := r2.tracks(10)
	r2.p.PlayContext(Source{Kind: KindAlbum}, ts2, 3)
	r2.p.SetShuffle(true)
	if !slices.Equal(pidsOf(r2.p.Upcoming()), pidsOf(up)) {
		t.Fatal("shuffle isn't deterministic for a seeded source")
	}

	// Music's slice still has ts[4] next; at the boundary we play our pick.
	r.playing(ts[4].PID, 0)
	c := onlyCall(t, r.calls(), "playTracks")
	if c.pids[0] != up[0].PID || !slices.Equal(c.pids[1:], pidsOf(up[1:])) {
		t.Fatalf("re-issued %v, want shuffled order", c.pids)
	}
	r.played(up[0].PID)

	r.p.SetShuffle(false)
	cur := slices.Index(ts, up[0])
	if got := r.p.Upcoming(); !slices.Equal(got, ts[cur+1:]) {
		t.Fatalf("unshuffled upcoming %d tracks, want ts[%d:]", len(got), cur+1)
	}
}

func comparePID(a, b *library.Track) int {
	switch {
	case a.PID < b.PID:
		return -1
	case a.PID > b.PID:
		return 1
	}
	return 0
}

func TestPlayContextWithShuffleStartsWithChosenTrack(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(20)
	r.p.SetShuffle(true)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 7)
	c := onlyCall(t, r.calls(), "playTracks")
	if c.pids[0] != ts[7].PID || len(c.pids) != 20 {
		t.Fatalf("shuffled play issued %d pids starting %s", len(c.pids), c.pids[0])
	}
}

func TestUpNextReissuedAtBoundary(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(5)
	extra := r.tracks(2)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)

	r.p.AddToQueue(extra[1])
	r.p.PlayNext(extra[0])
	want := append([]*library.Track{extra[0], extra[1]}, ts[1:]...)
	if got := r.p.Upcoming(); !slices.Equal(got, want) {
		t.Fatalf("upcoming %v", pidsOf(got))
	}
	if s := r.p.State(); s.Queued != 2 || s.Upcoming != 6 {
		t.Fatalf("queued=%d upcoming=%d", s.Queued, s.Upcoming)
	}
	noCalls(t, r.calls()) // no restart mid-track

	// Music moves on to ts[1]; we play the queue instead.
	r.playing(ts[1].PID, 0)
	c := onlyCall(t, r.calls(), "playTracks")
	if !slices.Equal(c.pids, pidsOf(want)) {
		t.Fatalf("re-issued %v", c.pids)
	}
	r.played(extra[0].PID)
	r.playing(extra[1].PID, 0)
	r.playing(ts[1].PID, 0)
	noCalls(t, r.calls())
	if got := r.p.Upcoming(); !slices.Equal(got, ts[2:]) {
		t.Fatalf("upcoming after the queue %v", pidsOf(got))
	}
}

func TestNextUsesMusicWhenSliceAgrees(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(5)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)

	r.p.Next()
	onlyCall(t, r.calls(), "next")
	if s := r.p.State(); s.Track != ts[1] || !s.Loading {
		t.Fatal("next isn't shown optimistically")
	}
	r.played(ts[1].PID)

	r.p.PlayNext(ts[4])
	r.p.Next()
	if c := onlyCall(t, r.calls(), "playTracks"); c.pids[0] != ts[4].PID {
		t.Fatalf("dirty next issued from %s", c.pids[0])
	}
}

func TestRapidNextCoalescesPlays(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(10)
	r.c.gate()
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	<-r.c.entered
	for range 4 {
		r.p.Next() // the first play is still in flight: each re-issues
	}
	close(r.c.release)
	calls := r.calls()
	if len(calls) != 2 || calls[1].pids[0] != ts[4].PID {
		t.Fatalf("calls %v", names(calls))
	}
}

func TestQueueEditing(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(4)
	q := r.tracks(3)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.p.AddToQueue(q...)

	r.p.MoveInQueue(2, 0)
	if got := r.p.Upcoming(); !slices.Equal(got[:3], []*library.Track{q[2], q[0], q[1]}) {
		t.Fatalf("after move %v", pidsOf(got))
	}
	r.p.MoveInQueue(0, 5) // into the context part: ignored
	r.p.RemoveFromQueue(1)
	r.p.RemoveFromQueue(3) // a context track: ts[2]
	want := []*library.Track{q[2], q[1], ts[1], ts[3]}
	if got := r.p.Upcoming(); !slices.Equal(got, want) {
		t.Fatalf("after removes %v", pidsOf(got))
	}
	// A removed context track stays removed through shuffle on and off.
	r.p.SetShuffle(true)
	r.p.SetShuffle(false)
	r.p.ClearQueue()
	if got := r.p.Upcoming(); !slices.Equal(got, []*library.Track{ts[1], ts[3]}) {
		t.Fatalf("after clear %v", pidsOf(got))
	}

	r.p.AddToQueue(q[0])
	r.p.JumpTo(1) // skips the queued track
	if s := r.p.State(); s.Track != ts[1] || s.Queued != 0 {
		t.Fatalf("jumped to %v, queued %d", s.Track.Title, s.Queued)
	}
}

func TestOptimisticOverridesExpire(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(2)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.played(ts[0].PID)

	r.p.SetVolume(30)
	r.p.CycleRepeat()
	r.playing(ts[0].PID, 1) // stale report
	s := r.p.State()
	if s.NowPlaying.Volume != 30 || s.NowPlaying.Repeat != bridge.RepeatAll {
		t.Fatalf("volume %d repeat %s right after setting", s.NowPlaying.Volume, s.NowPlaying.Repeat)
	}
	r.clock.Advance(2 * time.Second)
	r.playing(ts[0].PID, 3) // Music never applied them
	s = r.p.State()
	if s.NowPlaying.Volume != 50 || s.NowPlaying.Repeat != bridge.RepeatOff {
		t.Fatalf("volume %d repeat %s after the hold", s.NowPlaying.Volume, s.NowPlaying.Repeat)
	}
}

func TestToggleIsOptimistic(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(2)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.played(ts[0].PID)
	r.calls()
	r.p.Toggle()
	onlyCall(t, r.calls(), "pause")
	if st := r.p.State().NowPlaying.State; st != bridge.Paused {
		t.Fatalf("state %s after toggle", st)
	}
}

// Music's own playpause can be dropped or read the wrong way round, which would
// leave it playing while we show paused. Toggle sends the command it means.
func TestToggleSendsExplicitCommands(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(1)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.played(ts[0].PID)
	r.calls()
	r.clock.Advance(holdFor)
	r.p.Observe(bridge.PlayerEvent{NowPlaying: r.nowPlaying(ts[0].PID, bridge.Paused, 5)})
	r.p.Toggle()
	onlyCall(t, r.calls(), "play")
	r.p.Toggle()
	onlyCall(t, r.calls(), "pause")
}

// Music reports paused for a moment while a new track loads. A toggle then is a
// pause: the track is about to play and the UI shows it playing.
func TestToggleWhileLoadingPauses(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(1)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.flush()
	r.clock.Advance(300 * time.Millisecond)
	r.p.Observe(bridge.PlayerEvent{NowPlaying: r.nowPlaying(ts[0].PID, bridge.Paused, 0)})
	r.calls()
	if st := r.p.State().NowPlaying.State; st != bridge.Playing {
		t.Fatalf("state %s while loading", st)
	}
	r.p.Toggle()
	onlyCall(t, r.calls(), "pause")
}

func TestPositionInterpolation(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(1)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.flush()
	if pos := r.p.Position(); pos != 0 {
		t.Fatalf("position while loading %v", pos)
	}
	r.playing(ts[0].PID, 0)
	r.playing(ts[0].PID, 10)
	r.clock.Advance(2500 * time.Millisecond)
	if pos := r.p.Position(); pos != 12500*time.Millisecond {
		t.Fatalf("playing position %v", pos)
	}

	// The report was read 200 ms before it arrived.
	np := r.nowPlaying(ts[0].PID, bridge.Playing, 20)
	np.At -= 200
	r.p.Observe(bridge.PlayerEvent{NowPlaying: np})
	if pos := r.p.Position(); pos != 20200*time.Millisecond {
		t.Fatalf("lag-corrected position %v", pos)
	}

	r.p.Observe(bridge.PlayerEvent{NowPlaying: r.nowPlaying(ts[0].PID, bridge.Paused, 30)})
	r.clock.Advance(5 * time.Second)
	if pos := r.p.Position(); pos != 30*time.Second {
		t.Fatalf("paused position %v", pos)
	}

	r.playing(ts[0].PID, 195)
	r.clock.Advance(20 * time.Second)
	if pos := r.p.Position(); pos != 200*time.Second {
		t.Fatalf("position not clamped to duration: %v", pos)
	}

	r.p.Seek(50 * time.Second)
	r.playing(ts[0].PID, 199) // stale
	if pos := r.p.Position(); pos != 50*time.Second {
		t.Fatalf("position after seek %v", pos)
	}
	r.clock.Advance(time.Second)
	if pos := r.p.Position(); pos != 51*time.Second {
		t.Fatalf("position a second after seek %v", pos)
	}
}

func TestVolumeCoalesces(t *testing.T) {
	r := newRig(t)
	r.c.gate()
	r.p.SetVolume(10)
	<-r.c.entered
	for v := 11; v <= 20; v++ {
		r.p.SetVolume(v)
	}
	close(r.c.release)
	calls := r.calls()
	if len(calls) != 2 || calls[0].n != 10 || calls[1].n != 20 {
		t.Fatalf("volume calls %v", calls)
	}

	r.p.SetVolume(50)
	r.p.AdjustVolume(5)
	r.p.AdjustVolume(5)
	r.p.AdjustVolume(100)
	if v := r.p.State().NowPlaying.Volume; v != 100 {
		t.Fatalf("volume %d", v)
	}
	r.flush()
}

func TestSeekCoalesces(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(1)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.played(ts[0].PID)
	r.calls()
	r.c.gate()
	r.p.Seek(10 * time.Second)
	<-r.c.entered
	r.p.SeekRelative(5 * time.Second)
	r.p.SeekRelative(5 * time.Second)
	close(r.c.release)
	calls := r.calls()
	if len(calls) != 2 || calls[1].f != 20 {
		t.Fatalf("seek calls %v", calls)
	}
}

func TestUnavailableTracksAreLeftOut(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(5)
	ts[0].Available = false
	ts[2].Available = false
	r.p.PlayContext(Source{Kind: KindPlaylist, ID: "PL"}, ts, 0)
	c := onlyCall(t, r.calls(), "playTracks") // not native: some tracks are dead
	if !slices.Equal(c.pids, pidsOf([]*library.Track{ts[1], ts[3], ts[4]})) {
		t.Fatalf("issued %v", c.pids)
	}

	none := r.tracks(2)
	none[0].Available, none[1].Available = false, false
	r.p.PlayContext(Source{Kind: KindAlbum}, none, 0)
	noCalls(t, r.calls())
	if r.p.State().LastError != MsgNothingPlayable {
		t.Fatalf("error %q", r.p.State().LastError)
	}
}

func TestNativePlaylist(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.p.PlayContext(Source{Kind: KindPlaylist, ID: "PL"}, ts, 0)
	if c := onlyCall(t, r.calls(), "playPlaylist"); c.pid != "PL" {
		t.Fatalf("played playlist %s", c.pid)
	}
	r.played(ts[0].PID)
	r.playing(ts[1].PID, 0)
	noCalls(t, r.calls())

	r.p.PlayContext(Source{Kind: KindPlaylist, ID: "PL"}, ts, 1)
	onlyCall(t, r.calls(), "playTracks")
}

func TestSilentlyFailingTrackIsSkipped(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.stoppedEv() // right after the call: too early to judge
	noCalls(t, r.calls())
	r.clock.Advance(3 * time.Second)
	c := onlyCall(t, r.calls(), "playTracks")
	if c.pids[0] != ts[1].PID {
		t.Fatalf("skipped to %s", c.pids[0])
	}
	if s := r.p.State(); s.LastError != MsgSkippedUnavailable {
		t.Fatalf("error %q", s.LastError)
	}
	// The dead track is left out from now on.
	r.played(ts[1].PID)
	r.p.SetShuffle(true)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 1)
	if got := r.p.Upcoming(); !slices.Equal(got, ts[2:]) {
		t.Fatalf("upcoming %v", pidsOf(got))
	}
}

func TestSilentFailureSeenInEvent(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.playing(ts[2].PID, 50) // something else was playing
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.clock.Advance(deadGrace) // Music hasn't reported anything new yet
	noCalls(t, r.calls())
	r.stoppedEv()
	if c := onlyCall(t, r.calls(), "playTracks"); c.pids[0] != ts[1].PID {
		t.Fatalf("skipped to %s", c.pids[0])
	}
	if s := r.p.State(); s.LastError != MsgSkippedUnavailable {
		t.Fatalf("error %q", s.LastError)
	}
}

func TestBridgeErrors(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(2)
	r.c.failWith("playTracks", &bridge.Error{Code: bridge.ErrAutomationDenied, Message: "denied"})
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 1)
	r.flush()
	s := r.p.State()
	if s.Loading || s.LastError == "" || s.LastError == "denied" {
		t.Fatalf("loading=%v error=%q", s.Loading, s.LastError)
	}
	r.c.failWith("volume", &bridge.Error{Code: bridge.ErrMusicNotRunning})
	r.p.SetVolume(3)
	r.flush()
	if s := r.p.State(); s.LastError != "Music isn't running" {
		t.Fatalf("error %q", s.LastError)
	}
	r.p.ClearError()
	if r.p.State().LastError != "" {
		t.Fatal("error not cleared")
	}
}

func TestExternalPlaybackDetaches(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	other := r.tracks(1)
	q := r.tracks(1)
	r.p.PlayContext(Source{Kind: KindAlbum, Title: "A"}, ts, 0)
	r.played(ts[0].PID)
	r.calls()
	r.p.AddToQueue(q[0])
	r.playing(other[0].PID, 0) // started from Music.app
	s := r.p.State()
	if s.Source != (Source{}) || s.Track != other[0] || s.Upcoming != 1 {
		t.Fatalf("source=%v track=%v upcoming=%d", s.Source, s.Track, s.Upcoming)
	}
	noCalls(t, r.calls())
}

func TestMusicShuffleIsAdoptedAndTurnedOff(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(5)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.played(ts[0].PID)
	r.calls()
	r.clock.Advance(2 * time.Second)
	np := r.nowPlaying(ts[0].PID, bridge.Playing, 2)
	np.Shuffle = true
	r.p.Observe(bridge.PlayerEvent{NowPlaying: np})
	if c := onlyCall(t, r.calls(), "shuffle"); c.b {
		t.Fatal("asked Music to shuffle")
	}
	if !r.p.State().NowPlaying.Shuffle {
		t.Fatal("encore shuffle not on")
	}
}

func TestChangesNotify(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(1)
	r.p.AddToQueue(ts[0])
	r.p.AddToQueue(ts[0])
	select {
	case <-r.p.Changes():
	default:
		t.Fatal("no change notification")
	}
	select {
	case <-r.p.Changes():
		t.Fatal("notifications didn't coalesce")
	default:
	}
}

func TestNewWithoutOptions(t *testing.T) {
	p := New(newFakeClient(), nil)
	defer p.Close()
	p.SetShuffle(true)
	p.Next()
	p.Previous()
	_ = p.State()
}

func TestMediaKeysWithinSliceAreFollowed(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(5)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.played(ts[0].PID)
	r.playing(ts[1].PID, 0)
	r.playing(ts[2].PID, 0)
	r.calls()
	r.playing(ts[1].PID, 0) // previous from the keyboard's media keys
	noCalls(t, r.calls())
	if s := r.p.State(); s.Track != ts[1] || s.Source.Kind != KindAlbum {
		t.Fatalf("track %v source %v", s.Track.Title, s.Source)
	}
	if got := r.p.Upcoming(); !slices.Equal(got, ts[2:]) {
		t.Fatalf("upcoming %v", pidsOf(got))
	}
}

func TestMusicQuitThenToggleReplays(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 1)
	r.played(ts[1].PID)
	r.calls()
	r.p.Observe(bridge.MusicEvent{Running: false})
	r.p.Toggle()
	if c := onlyCall(t, r.calls(), "playTracks"); c.pids[0] != ts[1].PID {
		t.Fatalf("toggle replayed from %s", c.pids[0])
	}
}

func TestPendingTimesOutAndFollowsMusic(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(2)
	other := r.tracks(1)
	r.playing(other[0].PID, 10)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.playing(other[0].PID, 11) // Music ignored us
	r.clock.Advance(pendingTimeout)
	s := r.p.State()
	if s.Loading || s.Track != other[0] || s.Source != (Source{}) {
		t.Fatalf("loading=%v track=%v source=%v", s.Loading, s.Track, s.Source)
	}
}

func TestNothingNextPausesMusic(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.repeat = bridge.RepeatAll
	r.playing("", 0)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 1)
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf([]*library.Track{ts[1], ts[2], ts[0]})) {
		t.Fatalf("issued %v", c.pids)
	}
	r.played(ts[1].PID)
	r.repeat = bridge.RepeatOff // switched off in Music.app
	r.playing(ts[1].PID, 1)
	r.playing(ts[2].PID, 0)
	r.playing(ts[0].PID, 0) // Music carries on into the wrapped part of the slice
	onlyCall(t, r.calls(), "pause")
}

func TestReplayingCurrentTrackWaitsForFreshReport(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(2)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.played(ts[0].PID)
	r.playing(ts[0].PID, 120)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.playing(ts[0].PID, 121) // read before the call went out
	r.flush()
	r.playing(ts[0].PID, 121) // still the old playback
	if !r.p.State().Loading {
		t.Fatal("a stale report confirmed the replay")
	}
	r.playing(ts[0].PID, 0.4)
	if s := r.p.State(); s.Loading || s.Position != 400*time.Millisecond {
		t.Fatalf("loading=%v position=%v", s.Loading, s.Position)
	}
}

func TestHumanize(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&bridge.Error{Code: bridge.ErrAutomationDenied}, "encore needs permission to control Music (System Settings → Privacy & Security → Automation)"},
		{&bridge.Error{Code: bridge.ErrAutomationConsent, Message: "waiting for permission to control Music"}, MsgWaitingPermission},
		{&bridge.Error{Code: bridge.ErrMusicNotRunning}, "Music isn't running"},
		{&bridge.Error{Code: 3, Message: "boom"}, "boom"},
		{context.DeadlineExceeded, "Music didn't respond in time"},
		{context.Canceled, ""},
		{fmt.Errorf("wrapped: %w", &bridge.Error{Code: bridge.ErrMusicNotRunning}), "Music isn't running"},
	} {
		if got := humanize(tc.err); got != tc.want {
			t.Errorf("humanize(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

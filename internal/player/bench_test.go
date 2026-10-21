package player

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

// benchPlayer plays a Songs-sized context of n tracks with a few queued ones,
// confirmed, so State and Upcoming see a settled model.
func benchPlayer(b *testing.B, n int) *Player {
	b.Helper()
	clock := newFakeClock()
	byPID := map[string]*library.Track{}
	mk := func(n, base int) []*library.Track {
		out := make([]*library.Track, n)
		for i := range out {
			t := &library.Track{
				PID:       fmt.Sprintf("%016X", base+i+1),
				Duration:  200 * time.Second,
				InLibrary: true,
				Available: true,
			}
			byPID[t.PID] = t
			out[i] = t
		}
		return out
	}
	songs := mk(n, 0)
	p := New(newFakeClient(), func(pid string) *library.Track { return byPID[pid] },
		WithClock(clock), WithRand(rand.New(rand.NewPCG(1, 2))))
	b.Cleanup(p.Close)
	p.PlayContext(Source{Kind: KindSongs}, songs, 0)
	p.AddToQueue(mk(5, n)...)
	np := bridge.NowPlaying{State: bridge.Playing, At: clock.Now().UnixMilli(), Volume: 50, Repeat: bridge.RepeatAll,
		Track: &bridge.CurrentTrack{PID: songs[0].PID, Duration: 200}}
	p.Observe(bridge.PlayerEvent{NowPlaying: np})
	return p
}

func BenchmarkState50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.State()
	}
}

func BenchmarkUpcoming50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Upcoming()
	}
}

// BenchmarkFirstUpcoming50k is the lyrics pane's "next track" lookup as it was
// done before UpcomingN existed.
func BenchmarkFirstUpcoming50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Upcoming()[0]
	}
}

func BenchmarkUpcomingN1_50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.UpcomingN(1)
	}
}

func BenchmarkUpcomingAt_50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.UpcomingAt(40_000)
	}
}

func BenchmarkUpcomingDuration50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.UpcomingDuration()
	}
}

// BenchmarkStateAfterChange50k is State right after a queue edit: the one
// walk a change costs.
func BenchmarkStateAfterChange50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		p.mu.Lock()
		p.touch()
		p.mu.Unlock()
		_ = p.State()
	}
}

// BenchmarkUpcomingDurationAfterChange50k is the walk UpcomingDuration costs
// once per change.
func BenchmarkUpcomingDurationAfterChange50k(b *testing.B) {
	p := benchPlayer(b, 50_000)
	b.ReportAllocs()
	for b.Loop() {
		p.mu.Lock()
		p.touch()
		p.mu.Unlock()
		_ = p.UpcomingDuration()
	}
}

package player

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/library"
)

// checkUpcoming compares the cached and direct views of the upcoming sequence
// with a fresh walk.
func checkUpcoming(t *testing.T, r *rig, step int, op string) {
	t.Helper()
	up := r.p.Upcoming()
	r.p.mu.Lock()
	queued := 0
	r.p.walk(func(l loc) bool {
		if l.qi >= 0 {
			queued++
		}
		return true
	})
	r.p.mu.Unlock()
	var dur time.Duration
	for _, tr := range up {
		dur += tr.Duration
	}
	s := r.p.State()
	if s.Upcoming != len(up) || s.Queued != queued {
		t.Fatalf("step %d %s: state %d/%d, want %d/%d", step, op, s.Upcoming, s.Queued, len(up), queued)
	}
	if got := r.p.UpcomingDuration(); got != dur {
		t.Fatalf("step %d %s: duration %v, want %v", step, op, got, dur)
	}
	for _, n := range []int{-1, 0, 1, 2, 7, len(up), len(up) + 3} {
		want := up[:min(max(n, 0), len(up))]
		if got := r.p.UpcomingN(n); !slices.Equal(got, want) {
			t.Fatalf("step %d %s: UpcomingN(%d) %v, want %v", step, op, n, pidsOf(got), pidsOf(want))
		}
	}
	for i := -1; i <= len(up); i++ {
		var want *library.Track
		if i >= 0 && i < len(up) {
			want = up[i]
		}
		if got := r.p.UpcomingAt(i); got != want {
			t.Fatalf("step %d %s: UpcomingAt(%d) wrong", step, op, i)
		}
	}
}

func TestUpcomingViewsMatchWalk(t *testing.T) {
	for seed := range uint64(20) {
		r := newRig(t)
		rng := rand.New(rand.NewPCG(seed, 7))
		killing := seed%2 == 1 // even seeds keep every track alive, for locateDirect
		ts := r.tracks(12)
		for i, tr := range ts {
			tr.Duration = time.Duration(100+i) * time.Second
		}
		extra := r.tracks(6)
		r.p.PlayContext(Source{Kind: KindAlbum}, ts, rng.IntN(len(ts)))
		r.played(r.p.State().Track.PID)
		checkUpcoming(t, r, 0, "start")
		for step := 1; step <= 120; step++ {
			n := r.p.State().Upcoming
			op := ""
			switch rng.IntN(14) {
			case 0:
				op = "playNext"
				r.p.PlayNext(extra[rng.IntN(len(extra))])
			case 1:
				op = "addToQueue"
				r.p.AddToQueue(extra[rng.IntN(len(extra))], extra[rng.IntN(len(extra))])
			case 2:
				op = "remove"
				r.p.RemoveFromQueue(rng.IntN(n + 1))
			case 3:
				op = "move"
				q := r.p.State().Queued
				r.p.MoveInQueue(rng.IntN(q+1), rng.IntN(q+1))
			case 4:
				op = "clearQueue"
				r.p.ClearQueue()
			case 5:
				op = "jump"
				r.p.JumpTo(rng.IntN(n + 1))
			case 6:
				op = "next"
				r.p.Next()
			case 7:
				op = "previous"
				r.p.Previous()
			case 8:
				op = "shuffle"
				r.p.SetShuffle(!r.p.State().NowPlaying.Shuffle)
			case 9:
				op = "repeat"
				r.p.CycleRepeat()
			case 10:
				op = "expire holds" // repeat falls back to what Music reports
				r.clock.Advance(2 * time.Second)
			case 11:
				op = "dead"
				if !killing || n == 0 || rng.IntN(3) > 0 {
					continue
				}
				r.p.JumpTo(rng.IntN(n))
				r.calls()
				r.stoppedEv()
				r.clock.Advance(3 * time.Second)
			case 13:
				op = "moveUpcoming"
				r.p.MoveUpcoming(rng.IntN(n+1), rng.IntN(n+2))
			case 12:
				op = "replay context"
				r.p.PlayContext(Source{Kind: KindAlbum}, ts, rng.IntN(len(ts)))
			}
			if st := r.p.State(); st.Loading && st.Track != nil && rng.IntN(2) == 0 {
				r.played(st.Track.PID)
			}
			checkUpcoming(t, r, step, op)
		}
	}
}

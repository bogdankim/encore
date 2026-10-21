package player

import (
	"slices"
	"testing"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

func seq(ts ...*library.Track) []*library.Track { return ts }

func TestMoveUpcoming(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(8)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)

	steps := []struct {
		name     string
		from, to int
		want     []*library.Track
		queued   int
	}{
		// Everything comes from the context: the moved track and the one it
		// passes become queued, in the order shown.
		{"context down", 0, 1, seq(ts[2], ts[1], ts[3], ts[4], ts[5], ts[6], ts[7]), 2},
		{"context to top", 4, 0, seq(ts[5], ts[2], ts[1], ts[3], ts[4], ts[6], ts[7]), 3},
		{"queued within queue", 0, 2, seq(ts[2], ts[1], ts[5], ts[3], ts[4], ts[6], ts[7]), 3},
		{"queued into context", 0, 4, seq(ts[1], ts[5], ts[3], ts[4], ts[2], ts[6], ts[7]), 5},
		{"context into queue", 6, 1, seq(ts[1], ts[7], ts[5], ts[3], ts[4], ts[2], ts[6]), 6},
		{"past the end clamps", 0, 99, seq(ts[7], ts[5], ts[3], ts[4], ts[2], ts[6], ts[1]), 7},
		{"same place", 3, 3, seq(ts[7], ts[5], ts[3], ts[4], ts[2], ts[6], ts[1]), 7},
		{"out of range", 9, 0, seq(ts[7], ts[5], ts[3], ts[4], ts[2], ts[6], ts[1]), 7},
	}
	for _, s := range steps {
		r.p.MoveUpcoming(s.from, s.to)
		if got := r.p.Upcoming(); !slices.Equal(got, s.want) {
			t.Fatalf("%s: upcoming %v, want %v", s.name, pidsOf(got), pidsOf(s.want))
		}
		if st := r.p.State(); st.Queued != s.queued {
			t.Fatalf("%s: queued %d, want %d", s.name, st.Queued, s.queued)
		}
		checkUpcoming(t, r, 0, s.name)
	}
	noCalls(t, r.calls()) // the playing track is never restarted

	// Every context track was queued along the way, so clearing the queue
	// leaves nothing.
	r.p.ClearQueue()
	if got := r.p.Upcoming(); len(got) != 0 {
		t.Fatalf("after clear %v", pidsOf(got))
	}
}

func TestMoveUpcomingSurvivesShuffle(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(10)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)
	r.p.MoveUpcoming(5, 0) // ts[6] to the top
	r.p.SetShuffle(true)
	up := r.p.Upcoming()
	if up[0] != ts[6] || slices.Index(up[1:], ts[6]) >= 0 {
		t.Fatalf("after shuffle on %v", pidsOf(up))
	}
	r.p.SetShuffle(false)
	want := append(seq(ts[6]), ts[1], ts[2], ts[3], ts[4], ts[5], ts[7], ts[8], ts[9])
	if got := r.p.Upcoming(); !slices.Equal(got, want) {
		t.Fatalf("after shuffle off %v", pidsOf(got))
	}
}

func TestMoveUpcomingRepeatAllTail(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(5)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 2)
	r.p.CycleRepeat() // all
	r.calls()
	r.played(ts[2].PID)
	// ts[3], ts[4], then the wrap ts[0], ts[1]
	r.p.MoveUpcoming(3, 0) // ts[1] from the wrapped tail to the top
	want := seq(ts[1], ts[3], ts[4], ts[0])
	if got := r.p.Upcoming(); !slices.Equal(got, want) {
		t.Fatalf("upcoming %v, want %v", pidsOf(got), pidsOf(want))
	}
	checkUpcoming(t, r, 0, "tail")
	r.p.MoveUpcoming(1, 3) // ts[3] to the end: ts[4] and ts[0] get queued before it
	want = seq(ts[1], ts[4], ts[0], ts[3])
	if got := r.p.Upcoming(); !slices.Equal(got, want) {
		t.Fatalf("upcoming %v, want %v", pidsOf(got), pidsOf(want))
	}
	checkUpcoming(t, r, 1, "tail end")
}

func TestMoveUpcomingReissuedAtBoundary(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(6)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)

	r.p.MoveUpcoming(3, 0) // ts[4] next
	want := seq(ts[4], ts[1], ts[2], ts[3], ts[5])
	noCalls(t, r.calls())

	// Music moves on through the slice it has; we play the new order instead.
	r.playing(ts[1].PID, 0)
	c := onlyCall(t, r.calls(), "playTracks")
	if !slices.Equal(c.pids, pidsOf(want)) {
		t.Fatalf("re-issued %v, want %v", c.pids, pidsOf(want))
	}
	r.played(ts[4].PID)
	for _, tr := range want[1:] {
		r.playing(tr.PID, 0)
	}
	noCalls(t, r.calls())
	if s := r.p.State(); s.Track != ts[5] || s.Upcoming != 0 || s.NowPlaying.State != bridge.Playing {
		t.Fatalf("ended on %v with %d upcoming", s.Track, s.Upcoming)
	}
}

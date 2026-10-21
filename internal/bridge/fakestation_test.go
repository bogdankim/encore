package bridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakePlayStation(t *testing.T) {
	f, clk := newTestFake(t)
	ctx := context.Background()
	snap, _ := f.Snapshot(ctx)

	if err := f.PlayStation(ctx, "ra.725086732"); err != nil {
		t.Fatal(err)
	}
	np, _ := f.State(ctx)
	if np.State != Playing || np.Track == nil || np.Track.Name == "" || np.Track.Duration < 150 || np.Track.Duration >= 300 {
		t.Fatalf("station start %+v", np.Track)
	}
	first := *np.Track
	if _, err := f.track(first.PID); err != nil {
		t.Fatalf("station song not known by its pid: %v", err)
	}

	// Songs end and the station goes on: it never stops.
	seen := map[string]bool{first.PID: true}
	for range 20 {
		np, _ = f.State(ctx)
		clk.add(time.Duration(np.Track.Duration+1) * time.Second)
		f.Tick()
		np, _ = f.State(ctx)
		if np.State != Playing || np.Track == nil || seen[np.Track.PID] {
			t.Fatalf("station song %+v (state %s)", np.Track, np.State)
		}
		seen[np.Track.PID] = true
	}
	if len(f.queue) != 1 || len(f.extra) != 1 {
		t.Fatalf("station kept %d queue entries, %d extras", len(f.queue), len(f.extra))
	}

	// Next moves on; Previous only restarts the song.
	clk.add(2 * time.Second)
	if err := f.Next(ctx); err != nil {
		t.Fatal(err)
	}
	np, _ = f.State(ctx)
	cur := np.Track.PID
	if seen[cur] || np.Position != 0 {
		t.Fatalf("next: %+v at %v", np.Track, np.Position)
	}
	clk.add(time.Second)
	if err := f.Previous(ctx); err != nil {
		t.Fatal(err)
	}
	if np, _ = f.State(ctx); np.Track.PID != cur || np.Position != 0 {
		t.Fatalf("previous: %+v at %v", np.Track, np.Position)
	}

	// The same station again starts the same run of songs.
	if err := f.PlayStation(ctx, "ra.725086732"); err != nil {
		t.Fatal(err)
	}
	if np, _ = f.State(ctx); np.Track.Name != first.Name || np.Track.PID == first.PID {
		t.Fatalf("restart %+v, first was %+v", np.Track, first)
	}

	// A library play leaves the station; at its end Music stops.
	pid := snap.Tracks[0].PID
	if err := f.PlayTracks(ctx, []string{pid}, nil); err != nil {
		t.Fatal(err)
	}
	np, _ = f.State(ctx)
	if np.Track == nil || np.Track.PID != pid {
		t.Fatalf("library play %+v", np.Track)
	}
	clk.add(time.Duration(np.Track.Duration+1) * time.Second)
	if np, _ = f.State(ctx); np.State != Stopped {
		t.Fatalf("after the library track: %+v", np)
	}

	var be *Error
	for _, id := range []string{"", "ra.", "pl.pm-1234", "978194965", "ra.bad id"} {
		if err := f.PlayStation(ctx, id); !errors.As(err, &be) || be.Code != 1 {
			t.Errorf("PlayStation(%q) = %v", id, err)
		}
	}
}

func TestFakeLiveStation(t *testing.T) {
	f, clk := newTestFake(t)
	ctx := context.Background()
	if !FakeLiveStation("ra.978194965") || FakeLiveStation("ra.725086732") {
		t.Fatal("FakeLiveStation")
	}
	if err := f.PlayStation(ctx, "ra.978194965"); err != nil {
		t.Fatal(err)
	}
	clk.add(40 * time.Second)
	np, _ := f.State(ctx)
	if np.State != Playing || np.Track == nil || np.Position != 0 || np.Track.Duration != 0 {
		t.Fatalf("live: %+v at %v", np.Track, np.Position)
	}
	pid := np.Track.PID
	if err := f.Seek(ctx, 30); err != nil {
		t.Fatal(err)
	}
	if np, _ = f.State(ctx); np.Track.PID != pid || np.Position != 0 {
		t.Fatalf("seek moved live radio: %+v at %v", np.Track, np.Position)
	}
	// The show moves on to another song by itself.
	clk.add(10 * time.Minute)
	f.Tick()
	if np, _ = f.State(ctx); np.Track == nil || np.Track.PID == pid || np.Position != 0 || np.Track.Duration != 0 {
		t.Fatalf("live after 10 min: %+v at %v", np.Track, np.Position)
	}
}

// With ArtFaults, a station song has no artwork for its first seconds, as
// in Music while it fetches it, and some calls fail.
func TestFakeArtFaults(t *testing.T) {
	f, clk := newTestFake(t)
	f.artDir = t.TempDir()
	ctx := context.Background()
	f.SetArtFaults(ArtFaults{StationLate: 2 * time.Second, FailEvery: 3})
	if err := f.PlayStation(ctx, "ra.725086732"); err != nil {
		t.Fatal(err)
	}
	np, _ := f.State(ctx)
	if a, err := f.Artwork(ctx, np.Track.PID, 64); err != nil || a.Path != "" {
		t.Fatalf("at the song's start: %+v, %v; want none", a, err)
	}
	clk.add(2 * time.Second)
	if a, err := f.Artwork(ctx, np.Track.PID, 64); err != nil || a.Path == "" {
		t.Fatalf("two seconds in: %+v, %v; want the art", a, err)
	}
	if _, err := f.Artwork(ctx, np.Track.PID, 64); err == nil {
		t.Fatal("the third call did not fail")
	}
}

package main

import (
	"testing"
	"time"
)

func TestMemLimit(t *testing.T) {
	for _, c := range []struct {
		live uint64
		want int64
	}{
		{0, memFloor},
		{40 << 20, memFloor},
		{70 << 20, 107<<20 + 1<<19}, // 70 * 5/4 + 20
		{200 << 20, 270 << 20},
	} {
		if got := memLimit(c.live); got != c.want {
			t.Errorf("memLimit(%d MB) = %.1f MB, want %.1f MB", c.live>>20, float64(got)/(1<<20), float64(c.want)/(1<<20))
		}
	}
}

func TestMemTunerReclaimsOnceAfterAnIdleBurst(t *testing.T) {
	now := time.Unix(1000, 0)
	var live, allocs uint64 = 50 << 20, 10 << 20
	var limits []int64
	freed := 0
	m := &memTuner{
		adapt: true,
		now:   func() time.Time { return now },
		read:  func() (uint64, uint64) { return live, allocs },
		limit: func(n int64) int64 { limits = append(limits, n); return 0 },
		free:  func() { freed++ },
	}
	m.Touch()
	m.lastLimit = memFloor

	if m.step() || freed != 0 {
		t.Fatal("reclaimed with no burst")
	}
	allocs += reclaimAfter + 1 // a burst
	now = now.Add(reclaimIdle - time.Second)
	if m.step() || freed != 0 {
		t.Fatal("reclaimed while the UI was still busy")
	}
	m.Touch() // input resets the wait
	now = now.Add(reclaimIdle - time.Second)
	if m.step() {
		t.Fatal("reclaimed before the UI was idle for long enough")
	}
	now = now.Add(2 * time.Second)
	if !m.step() || freed != 1 {
		t.Fatalf("no reclaim after an idle burst (freed %d)", freed)
	}
	now = now.Add(time.Minute)
	if m.step() || freed != 1 {
		t.Fatal("reclaimed again with nothing allocated since")
	}
	allocs += 1 << 20
	if m.step() {
		t.Fatal("reclaimed after a small amount of garbage")
	}

	// the limit follows a big library up and stays put for small changes
	live = 200 << 20
	m.step()
	if len(limits) != 1 || limits[0] != memLimit(200<<20) {
		t.Fatalf("limit calls %v after the heap grew to 200 MB", limits)
	}
	live = 195 << 20
	m.step()
	if len(limits) != 1 {
		t.Fatalf("limit rewritten for a 2%% change: %v", limits)
	}
	live = 40 << 20
	m.step()
	if len(limits) != 2 || limits[1] != memFloor {
		t.Fatalf("limit calls %v after the heap shrank", limits)
	}
}

func TestMemTunerLeavesGOMEMLIMITAlone(t *testing.T) {
	now := time.Unix(1000, 0)
	set := 0
	m := &memTuner{
		now:   func() time.Time { return now },
		read:  func() (uint64, uint64) { return 500 << 20, 0 },
		limit: func(int64) int64 { set++; return 0 },
		free:  func() {},
	}
	m.step()
	if set != 0 {
		t.Fatal("changed a limit the user set")
	}
}

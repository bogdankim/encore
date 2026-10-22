package main

import (
	"context"
	"os"
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
	"time"
)

// Go lets its heap grow to twice the live data before it collects, and a scroll
// through the album grid makes a lot of short-lived garbage, so the resident
// size went to about twice the live heap (140 to 150 MB for 70 MB live).
// encore sets a soft memory limit, which makes the collector work harder only
// as the process nears it, and gives memory back to the system after a burst
// of work once the UI has been quiet for a while.
const (
	// memFloor is the soft limit for a small heap. GOMEMLIMIT in the
	// environment replaces all of it.
	memFloor = 100 << 20

	// reclaimIdle is how long the UI must be quiet before a burst's garbage
	// is given back.
	reclaimIdle = 5 * time.Second
	// reclaimAfter is how much must have been allocated since the last
	// reclaim for a forced collection to be worth it.
	reclaimAfter = 64 << 20

	memEvery = 2 * time.Second
)

// memLimit is the soft limit for a heap of live bytes. A limit below what the
// program really needs makes the collector run all the time (it is capped at
// half the CPU, which is still a bad trade), so a big library raises it.
func memLimit(live uint64) int64 {
	return max(memFloor, int64(live)*5/4+20<<20)
}

// memTuner keeps the limit in step with the live heap and releases memory
// after bursts. Touch is called on user input.
type memTuner struct {
	adapt   bool // GOMEMLIMIT is not set
	touched atomic.Int64
	now     func() time.Time

	// seams for tests
	read  func() (live, allocs uint64)
	limit func(int64) int64 // debug.SetMemoryLimit
	free  func()            // debug.FreeOSMemory

	lastLimit int64
	allocsAt  uint64 // total allocated at the last reclaim
}

func newMemTuner() *memTuner {
	m := &memTuner{
		adapt: os.Getenv("GOMEMLIMIT") == "",
		now:   time.Now,
		read:  readHeap,
		limit: debug.SetMemoryLimit,
		free:  debug.FreeOSMemory,
	}
	m.touched.Store(m.now().UnixNano())
	if m.adapt {
		m.lastLimit = memFloor
		m.limit(memFloor)
	}
	return m
}

// Touch records user input.
func (m *memTuner) Touch() { m.touched.Store(m.now().UnixNano()) }

// step is one pass: it adjusts the limit, and reclaims when the UI has been
// idle since a burst. It reports whether it reclaimed.
func (m *memTuner) step() bool {
	live, allocs := m.read()
	if m.adapt {
		// move only when it matters, so the limit isn't rewritten every pass
		if want := memLimit(live); want > m.lastLimit || want < m.lastLimit*4/5 {
			m.lastLimit = want
			m.limit(want)
		}
	}
	idle := m.now().Sub(time.Unix(0, m.touched.Load()))
	if idle < reclaimIdle || allocs-m.allocsAt < reclaimAfter {
		return false
	}
	m.free()
	_, m.allocsAt = m.read()
	return true
}

// run steps until ctx ends.
func (m *memTuner) run(ctx context.Context) {
	t := time.NewTicker(memEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.step()
		}
	}
}

// readHeap returns the live heap after the last collection and the bytes
// allocated since the program started. Unlike runtime.ReadMemStats it does not
// stop the world.
func readHeap() (live, allocs uint64) {
	s := []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindUint64 {
		live = s[0].Value.Uint64()
	}
	if s[1].Value.Kind() == metrics.KindUint64 {
		allocs = s[1].Value.Uint64()
	}
	return live, allocs
}

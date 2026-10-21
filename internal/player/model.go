package player

import (
	"slices"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

// The play order lives here. All methods expect p.mu to be held.
//
// The context is p.orig (playable tracks in source order) walked through
// p.order (indices into orig, shuffled or not). p.pos is the order index of the
// last context track that started; the user queue plays before order[pos+1:].
// p.now is what is playing, from the context or popped off the queue.
//
// Every write to queue, orig, removed, order, pos, fromQueue or dead must call
// p.touch, which invalidates the cached summary of the upcoming sequence.

// loc is a place in the upcoming sequence: a queue entry (qi >= 0) or a context
// entry at order index oi.
type loc struct {
	t      *library.Track
	qi, oi int
}

// upSummary is what State and the queue pane need to know about the upcoming
// sequence as a whole. p.sum caches it for one model version and repeat mode;
// the counts and the duration fill in separately, on first use.
type upSummary struct {
	ver           uint64
	repeat        bridge.RepeatMode
	counted       bool
	total, queued int
	timed         bool
	dur           time.Duration
}

// touch records a change to the play order.
func (p *Player) touch() { p.ver++ }

func (p *Player) summary(rep bridge.RepeatMode) *upSummary {
	if s := &p.sum; s.repeat == rep && s.ver == p.ver {
		return s
	}
	p.sum = upSummary{ver: p.ver, repeat: rep} // repeat is never "", so the zero value never matches
	return &p.sum
}

// counts returns how many tracks are upcoming and how many of them come from
// the user queue. With nothing dead it is arithmetic; otherwise it walks once
// per change.
func (p *Player) counts(now time.Time) (total, queued int) {
	rep := p.repeatLocked(now)
	s := p.summary(rep)
	if s.counted {
		return s.total, s.queued
	}
	if len(p.dead) == 0 {
		s.queued = len(p.queue)
		s.total = s.queued
		if len(p.order) > 0 {
			s.total += max(len(p.order)-p.pos-1, 0)
			if rep == bridge.RepeatAll {
				s.total += max(p.wrapEnd(), 0)
			}
		}
	} else {
		p.walkRepeat(rep, func(l loc) bool {
			s.total++
			if l.qi >= 0 {
				s.queued++
			}
			return true
		})
	}
	s.counted = true
	return s.total, s.queued
}

// upcomingDuration sums the upcoming tracks, walking once per change.
func (p *Player) upcomingDuration(now time.Time) time.Duration {
	rep := p.repeatLocked(now)
	s := p.summary(rep)
	if !s.timed {
		p.walkRepeat(rep, func(l loc) bool {
			s.dur += l.t.Duration
			return true
		})
		s.timed = true
	}
	return s.dur
}

// walk visits what plays after p.now, in order: the user queue, the rest of the
// context and, with repeat all, the context again up to the current track.
// Tracks known to be dead are skipped.
func (p *Player) walk(fn func(l loc) bool) {
	p.walkRepeat(p.repeatLocked(p.clock.Now()), fn)
}

// wrapEnd is the order index the repeat-all wrap stops before.
func (p *Player) wrapEnd() int {
	if p.fromQueue {
		return p.pos + 1 // order[pos] already finished; it comes round again
	}
	return p.pos
}

func (p *Player) walkRepeat(rep bridge.RepeatMode, fn func(l loc) bool) {
	anyDead := len(p.dead) > 0
	for qi, t := range p.queue {
		if anyDead && p.dead[t.PID] {
			continue
		}
		if !fn(loc{t: t, qi: qi, oi: -1}) {
			return
		}
	}
	if len(p.order) == 0 {
		return
	}
	visit := func(from, to int) bool {
		for oi := from; oi < to; oi++ {
			t := p.orig[p.order[oi]]
			if anyDead && p.dead[t.PID] {
				continue
			}
			if !fn(loc{t: t, qi: -1, oi: oi}) {
				return false
			}
		}
		return true
	}
	if !visit(p.pos+1, len(p.order)) {
		return
	}
	if rep == bridge.RepeatAll {
		visit(0, p.wrapEnd())
	}
}

// locate returns the i-th upcoming entry. With no dead tracks to skip it is
// O(1).
func (p *Player) locate(i int) (loc, bool) {
	if i < 0 {
		return loc{}, false
	}
	if len(p.dead) == 0 {
		return p.locateDirect(i)
	}
	var found loc
	ok := false
	n := 0
	p.walk(func(l loc) bool {
		if n == i {
			found, ok = l, true
			return false
		}
		n++
		return true
	})
	return found, ok
}

// locateDirect is locate by arithmetic, for when nothing is dead.
func (p *Player) locateDirect(i int) (loc, bool) {
	if i < len(p.queue) {
		return loc{t: p.queue[i], qi: i, oi: -1}, true
	}
	i -= len(p.queue)
	if len(p.order) == 0 {
		return loc{}, false
	}
	from := p.pos + 1
	rest := max(len(p.order)-from, 0)
	if i < rest {
		oi := from + i
		return loc{t: p.orig[p.order[oi]], qi: -1, oi: oi}, true
	}
	i -= rest
	if i < p.wrapEnd() && p.repeatLocked(p.clock.Now()) == bridge.RepeatAll {
		return loc{t: p.orig[p.order[i]], qi: -1, oi: i}, true
	}
	return loc{}, false
}

func (p *Player) peek() *library.Track {
	l, ok := p.locate(0)
	if !ok {
		return nil
	}
	return l.t
}

// moveTo makes l the current track, dropping everything that came before it.
func (p *Player) moveTo(l loc) {
	p.touch()
	p.now = l.t
	if l.qi >= 0 {
		p.queue = slices.Delete(p.queue, 0, l.qi+1)
		p.fromQueue = true
		return
	}
	p.queue = nil
	p.pos = l.oi
	p.fromQueue = false
}

// advance moves to the next track. It reports false at the end of everything.
func (p *Player) advance() bool {
	l, ok := p.locate(0)
	if ok {
		p.moveTo(l)
	}
	return ok
}

// plan returns p.now followed by what comes after it, at most limit tracks.
func (p *Player) plan(limit int) []*library.Track {
	out := []*library.Track{p.now}
	p.walk(func(l loc) bool {
		if len(out) >= limit {
			return false
		}
		out = append(out, l.t)
		return true
	})
	return out
}

// upcoming returns at most n upcoming tracks; n < 0 means all of them.
func (p *Player) upcoming(n int) []*library.Track {
	if n == 0 {
		return nil
	}
	// The wrap never revisits more than the context holds.
	bound := len(p.queue) + len(p.order)
	if n > 0 {
		bound = min(bound, n)
	}
	if bound == 0 {
		return nil
	}
	out := make([]*library.Track, 0, bound)
	p.walk(func(l loc) bool {
		out = append(out, l.t)
		return len(out) != n
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// setContext replaces the context; start indexes tracks.
func (p *Player) setContext(src Source, tracks []*library.Track, start int) {
	p.touch()
	p.src = src
	p.orig = tracks
	p.removed = make([]bool, len(tracks))
	if p.shuffle {
		p.order = p.shuffledOrder(start)
		p.pos = 0
	} else {
		p.order = p.straightOrder()
		p.pos = start
	}
	p.now = tracks[start]
	p.fromQueue = false
	p.station = nil
	p.pruneCatalog()
}

func (p *Player) clearContext() {
	p.touch()
	p.src = Source{}
	p.station = nil
	p.orig, p.order, p.removed = nil, nil, nil
	p.pos = -1
	p.fromQueue = false
}

// straightOrder is the context in source order, minus removed entries.
func (p *Player) straightOrder() []int {
	order := make([]int, 0, len(p.orig))
	for i := range p.orig {
		if !p.removed[i] {
			order = append(order, i)
		}
	}
	return order
}

// shuffledOrder is anchor followed by every other entry in random order.
func (p *Player) shuffledOrder(anchor int) []int {
	order := []int{anchor}
	for i := range p.orig {
		if i != anchor && !p.removed[i] {
			order = append(order, i)
		}
	}
	rest := order[1:]
	p.rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	return order
}

// reorder applies p.shuffle to the context, keeping the current entry current.
func (p *Player) reorder() {
	if len(p.order) == 0 || p.pos < 0 {
		return
	}
	p.touch()
	anchor := p.order[p.pos]
	if p.shuffle {
		p.order = p.shuffledOrder(anchor)
		p.pos = 0
		return
	}
	p.order = p.straightOrder()
	p.pos = max(slices.Index(p.order, anchor), 0)
}

// removeUpcoming drops the i-th upcoming entry.
func (p *Player) removeUpcoming(i int) {
	l, ok := p.locate(i)
	if !ok {
		return
	}
	p.touch()
	if l.qi >= 0 {
		p.queue = slices.Delete(p.queue, l.qi, l.qi+1)
		return
	}
	p.removed[p.order[l.oi]] = true
	p.order = slices.Delete(p.order, l.oi, l.oi+1)
	if l.oi <= p.pos { // == only in the repeat-all tail while a queue track plays
		p.pos--
	}
}

// moveQueued moves an entry within the user queue. Both indices are Upcoming
// indices and must point into its queue part.
func (p *Player) moveQueued(from, to int) {
	lf, ok := p.locate(from)
	if !ok || lf.qi < 0 {
		return
	}
	lt, ok := p.locate(to)
	if !ok || lt.qi < 0 || lt.qi == lf.qi {
		return
	}
	p.touch()
	t := p.queue[lf.qi]
	p.queue = slices.Delete(p.queue, lf.qi, lf.qi+1)
	p.queue = slices.Insert(p.queue, lt.qi, t)
}

// syncTo follows Music to pid after it moved somewhere we didn't send it (media
// keys, Music.app's own window): prefer the context entry nearest to pos, then
// the user queue.
func (p *Player) syncTo(pid string) {
	p.touch()
	best := -1
	for oi, ix := range p.order {
		if p.orig[ix].PID == pid && (best < 0 || abs(oi-p.pos) < abs(best-p.pos)) {
			best = oi
		}
	}
	if best >= 0 {
		p.pos = best
		p.now = p.orig[p.order[best]]
		p.fromQueue = false
		return
	}
	if qi := slices.IndexFunc(p.queue, func(t *library.Track) bool { return t.PID == pid }); qi >= 0 {
		p.now = p.queue[qi]
		p.queue = slices.Delete(p.queue, qi, qi+1)
		p.fromQueue = true
		return
	}
	p.now = p.lookupTrack(pid)
	p.fromQueue = true
}

func playable(tracks []*library.Track) []*library.Track {
	out := make([]*library.Track, 0, len(tracks))
	for _, t := range tracks {
		if t != nil && t.Available {
			out = append(out, t)
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

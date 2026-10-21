package player

import (
	"slices"

	"github.com/bogdankim/encore/internal/library"
)

// MoveUpcoming moves Upcoming()[from] so that it becomes Upcoming()[to], for
// any row of Up Next. A track moved out of the context becomes a queued
// track, as in Apple Music, so the move survives shuffle and repeat changes.
// When it lands past the queued part, the context tracks that now play
// before it are queued too, keeping what plays in the order shown. Music
// picks up the new order at the next track boundary.
func (p *Player) MoveUpcoming(from, to int) {
	defer p.lock()()
	p.moveUpcoming(from, to)
}

// UpcomingVersion changes whenever the queue, the context, its order or the
// current track change, so a copy of part of Upcoming() can tell it is stale.
// The repeat mode is not part of it.
func (p *Player) UpcomingVersion() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ver
}

func (p *Player) moveUpcoming(from, to int) {
	if from == to || to < 0 {
		return
	}
	lf, ok := p.locate(from)
	if !ok {
		return
	}
	total, _ := p.counts(p.clock.Now())
	if to = min(to, total-1); to == from {
		return
	}
	if lf.qi >= 0 {
		if lt, ok := p.locate(to); ok && lt.qi >= 0 {
			p.moveQueued(from, to)
			return
		}
	}

	p.touch()
	t := lf.t
	if lf.qi >= 0 {
		p.queue = slices.Delete(p.queue, lf.qi, lf.qi+1)
	} else {
		p.dropContext([]int{lf.oi})
	}

	// t goes in front of what is now at index to, which may be a queued track,
	// a context track or the end.
	if lt, ok := p.locate(to); ok && lt.qi >= 0 {
		p.queue = slices.Insert(p.queue, lt.qi, t)
		return
	}
	var pulled []*library.Track
	var ois []int
	i := 0
	p.walk(func(l loc) bool {
		if i >= to {
			return false
		}
		if l.qi < 0 {
			pulled = append(pulled, l.t)
			ois = append(ois, l.oi)
		}
		i++
		return true
	})
	p.dropContext(ois)
	p.queue = append(append(p.queue, pulled...), t)
}

// dropContext takes the entries at order indices ois out of the context for
// good, as RemoveFromQueue does.
func (p *Player) dropContext(ois []int) {
	slices.Sort(ois)
	for k := len(ois) - 1; k >= 0; k-- {
		oi := ois[k]
		p.removed[p.order[oi]] = true
		p.order = slices.Delete(p.order, oi, oi+1)
		if oi <= p.pos { // == only in the repeat-all tail while a queue track plays
			p.pos--
		}
	}
}

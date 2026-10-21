package bridge

import "sync"

// eventQueue decouples event producers (the stdout reader, the fake's clock)
// from a slow consumer. push never blocks. When more than limit events are
// waiting, the oldest queued PlayerEvent is dropped: a newer one supersedes it.
// Other events are never dropped.
type eventQueue struct {
	out   chan Event
	limit int

	mu     sync.Mutex
	q      []Event
	closed bool
	wake   chan struct{} // cap 1: something was queued
	done   chan struct{} // closed by close
	exited chan struct{} // closed when the pump returns
}

func newEventQueue(buffer int) *eventQueue {
	if buffer <= 0 {
		buffer = 64
	}
	e := &eventQueue{
		out:    make(chan Event, buffer),
		limit:  buffer,
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	go e.pump()
	return e
}

func (e *eventQueue) push(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	if len(e.q) >= e.limit {
		for i, old := range e.q {
			if _, ok := old.(PlayerEvent); ok {
				e.q = append(e.q[:i], e.q[i+1:]...)
				break
			}
		}
	}
	e.q = append(e.q, ev)
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *eventQueue) pump() {
	defer close(e.exited)
	for {
		e.mu.Lock()
		var ev Event
		if len(e.q) > 0 {
			ev = e.q[0]
			e.q[0] = nil
			e.q = e.q[1:]
		}
		e.mu.Unlock()
		if ev == nil {
			select {
			case <-e.wake:
				continue
			case <-e.done:
				return
			}
		}
		select {
		case e.out <- ev:
		case <-e.done:
			return
		}
	}
}

// close stops delivery and closes out. Events still queued are discarded.
func (e *eventQueue) close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.mu.Unlock()
	close(e.done)
	<-e.exited
	close(e.out)
}

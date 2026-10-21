package player

import "time"

// Clock is the player's time source. Tests swap in a fake one.
type Clock interface {
	Now() time.Time
	// AfterFunc runs f in its own goroutine once d has elapsed.
	AfterFunc(d time.Duration, f func())
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, f func()) { time.AfterFunc(d, f) }

// held is a value we set ourselves that wins over what Music reports until it
// expires, because Music applies settings asynchronously and reads right after a
// write can return the old value.
type held[T any] struct {
	v     T
	until time.Time
}

func (h held[T]) or(now time.Time, fallback T) T {
	if now.Before(h.until) {
		return h.v
	}
	return fallback
}

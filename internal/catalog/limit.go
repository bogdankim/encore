package catalog

import (
	"context"
	"sync"
	"time"
)

// clock is time, swappable in tests.
type clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

const (
	backoffBase = 5 * time.Second
	backoffMax  = 60 * time.Second
)

// limiter is a token bucket plus the backoff window after a refusal. Callers
// reserve a token and sleep until it's theirs, so waiters are served in
// order; a cancelled wait gives its token back.
type limiter struct {
	clk     clock
	rate    float64 // tokens per second
	burst   float64
	maxWait time.Duration // longer waits fail fast with a RateLimitError

	mu      sync.Mutex
	tokens  float64 // negative while waiters hold reservations
	last    time.Time
	until   time.Time // no requests before this
	began   time.Time // when the current backoff window started
	strikes int       // refusals in a row
}

func newLimiter(clk clock, perMinute float64, burst int, maxWait time.Duration) *limiter {
	return &limiter{clk: clk, rate: perMinute / 60, burst: float64(burst), tokens: float64(burst), maxWait: maxWait}
}

func (l *limiter) refill(now time.Time) {
	if !l.last.IsZero() && now.After(l.last) {
		l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	}
	l.last = now
}

// wait blocks until a request may go out. It returns a *RateLimitError
// without waiting while backing off, or when the wait would exceed maxWait,
// and ctx.Err() when ctx ends first.
func (l *limiter) wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	now := l.clk.Now()
	if now.Before(l.until) {
		d := l.until.Sub(now)
		l.mu.Unlock()
		return &RateLimitError{RetryAfter: d}
	}
	l.refill(now)
	var d time.Duration
	if l.tokens < 1 {
		d = time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
	}
	if l.maxWait > 0 && d > l.maxWait {
		l.mu.Unlock()
		return &RateLimitError{RetryAfter: d}
	}
	l.tokens--
	l.mu.Unlock()
	if d == 0 {
		return nil
	}
	if err := l.clk.Sleep(ctx, d); err != nil {
		l.mu.Lock()
		l.tokens = min(l.burst, l.tokens+1)
		l.mu.Unlock()
		return err
	}
	// Apple may have refused someone else while we slept.
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := l.clk.Now(); now.Before(l.until) {
		return &RateLimitError{RetryAfter: l.until.Sub(now)}
	}
	return nil
}

// refused records a 403/429 for a request sent at sent and returns the
// backoff: 5 s, doubling per refusal in a row, capped at 60 s. It also
// empties the bucket, so the first requests after the window trickle out
// instead of bursting. A refusal of a request sent before the current window
// began (a parallel request that was already out) adds no strike and returns
// what is left of the window.
func (l *limiter) refused(sent time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	if l.stale(sent) {
		return max(0, l.until.Sub(now))
	}
	l.strikes++
	d := backoffMax
	if l.strikes <= 4 {
		d = min(backoffMax, backoffBase<<(l.strikes-1))
	}
	l.began = now
	l.until = now.Add(d)
	l.tokens = 0
	l.last = l.until // nothing refills during the window
	return d
}

// ok records a successful response to a request sent at sent, which ends the
// run of refusals unless the request went out before the current window.
func (l *limiter) ok(sent time.Time) {
	l.mu.Lock()
	if !l.stale(sent) {
		l.strikes = 0
	}
	l.mu.Unlock()
}

// stale reports whether a request sent at sent went out before the current
// backoff window began. The caller holds l.mu.
func (l *limiter) stale(sent time.Time) bool {
	return !l.began.IsZero() && !sent.After(l.began)
}

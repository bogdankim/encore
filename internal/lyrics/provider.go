package lyrics

import (
	"context"
	"errors"
	"time"
)

// Provider is one source of lyrics. Service asks its providers at the same
// time and prefers them in the order given in Options.Providers: synced
// beats plain, and between two synced (or two plain) results the earlier
// provider wins.
//
// Lookup returns ErrNotFound (or nil, nil) when the provider has nothing
// for the track. Any other error means it couldn't tell; it may come with a
// plain result found on the way.
type Provider interface {
	Name() string
	Lookup(ctx context.Context, q Query) (*Lyrics, error)
}

// A Deferred provider is asked only after Delay, or as soon as every
// provider before it has answered without synced lyrics; in a prefetch,
// only then. For sources that cost something and rarely win, like Music's
// own lyrics, which run on the bridge's Music queue in front of transport
// commands.
type Deferred interface {
	Delay() time.Duration
}

const (
	// syncedWindow is how long a synced result waits for providers ahead of
	// it in the order before it is taken.
	syncedWindow = 300 * time.Millisecond
	// musicDelay is how long LRCLIB gets before Music is asked too. LRCLIB
	// usually answers in about 150 ms.
	musicDelay = 300 * time.Millisecond
)

// LRCLIBProvider looks tracks up on lrclib.net.
func LRCLIBProvider(c *Client) Provider { return lrclibProvider{c} }

type lrclibProvider struct{ c *Client }

func (lrclibProvider) Name() string { return SourceLRCLIB }

func (p lrclibProvider) Lookup(ctx context.Context, q Query) (*Lyrics, error) {
	if q.Title == "" || q.Artist == "" {
		return nil, ErrNotFound
	}
	rec, err := p.c.Find(ctx, q.Title, q.Artist, q.Album, q.Duration.Seconds())
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	var lyr *Lyrics
	if rec != nil {
		lyr = fromRecord(rec)
	}
	switch {
	case lyr != nil && (lyr.Synced || lyr.Instrumental):
		return lyr, nil
	case lyr == nil && err == nil:
		return nil, ErrNotFound
	}
	return lyr, err // plain, but a better record may have been missed
}

// MusicProvider asks Music.app for the lyrics stored with the track
// (bridge.Client.Lyrics). It is Deferred.
func MusicProvider(get func(ctx context.Context, pid string) (string, error)) Provider {
	return musicProvider(get)
}

type musicProvider func(ctx context.Context, pid string) (string, error)

func (musicProvider) Name() string         { return SourceMusic }
func (musicProvider) Delay() time.Duration { return musicDelay }

func (m musicProvider) Lookup(ctx context.Context, q Query) (*Lyrics, error) {
	if q.PID == "" {
		return nil, ErrNotFound
	}
	text, err := m(ctx, q.PID)
	if err != nil {
		return nil, err
	}
	if lyr := FromText(text, SourceMusic); lyr != nil {
		return lyr, nil
	}
	return nil, ErrNotFound
}

// rank orders results: 2 synced or instrumental, 1 plain, 0 nothing.
func rank(l *Lyrics) int {
	switch {
	case l == nil:
		return 0
	case l.Synced || l.Instrumental:
		return 2
	case l.Plain != "":
		return 1
	}
	return 0
}

// Better reports whether a is an upgrade on b: synced or instrumental over
// plain, plain over nothing, and Apple's over anyone else's of the same
// kind.
func Better(a, b *Lyrics) bool {
	ra, rb := rank(a), rank(b)
	return ra > rb || (ra == rb && ra > 0 && a.Source == SourceApple && b.Source != SourceApple)
}

// lookup asks every provider and merges what they say; background is for
// a prefetch, which nobody waits on. publish gets a plain result while
// better ones may still come. A nil error means the answer is definitive
// (lyr == nil: nobody has lyrics); a non-nil error means some provider
// failed, and lyr may still hold a usable plain result.
//
// It returns as soon as the answer can't improve: a synced result once
// every provider ahead of it has answered (or syncedWindow has passed), or
// else once every provider has answered. The rest are cancelled.
func lookup(ctx context.Context, ps []Provider, q Query, background bool, publish func(*Lyrics)) (*Lyrics, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type answer struct {
		i   int
		lyr *Lyrics
		err error
	}
	answers := make(chan answer, len(ps))
	wakeups := make(chan int, len(ps))
	started := make([]bool, len(ps))
	done := make([]bool, len(ps))
	got := make([]answer, len(ps))
	start := func(i int) {
		if started[i] {
			return
		}
		started[i] = true
		go func() {
			l, err := ps[i].Lookup(ctx, q)
			answers <- answer{i, l, err}
		}()
	}
	for i, p := range ps {
		if d, ok := p.(Deferred); ok && d.Delay() > 0 {
			if background {
				continue
			}
			t := time.AfterFunc(d.Delay(), func() { wakeups <- i })
			defer t.Stop()
			continue
		}
		start(i)
	}

	var window <-chan time.Time
	windowUp := false
	published := 0
	for {
		// Start deferred providers once nobody ahead of them can still win.
		ahead := true
		for i := range ps {
			if ahead && !started[i] {
				start(i)
			}
			if !done[i] || rank(got[i].lyr) == 2 {
				ahead = false
			}
		}

		// The best answer so far: highest rank, then earliest provider.
		best, allDone, aheadDone := -1, true, true
		for i := range ps {
			allDone = allDone && done[i]
			if done[i] && rank(got[i].lyr) > 0 && (best < 0 || rank(got[i].lyr) > rank(got[best].lyr)) {
				best = i
			}
		}
		for i := 0; i < best; i++ {
			aheadDone = aheadDone && done[i]
		}
		var lyr *Lyrics
		if best >= 0 {
			lyr = got[best].lyr
		}
		if rank(lyr) == 2 {
			if aheadDone || windowUp {
				return lyr, nil
			}
			if window == nil {
				t := time.NewTimer(syncedWindow)
				defer t.Stop()
				window = t.C
			}
		}
		if allDone {
			var errs []error
			for _, a := range got {
				if a.err != nil && !errors.Is(a.err, ErrNotFound) {
					errs = append(errs, a.err)
				}
			}
			return lyr, errors.Join(errs...)
		}
		if r := rank(lyr); r == 1 && r > published && publish != nil {
			published = r
			publish(lyr)
		}

		select {
		case <-ctx.Done():
			return lyr, ctx.Err()
		case <-window:
			windowUp = true
		case i := <-wakeups:
			start(i)
		case a := <-answers:
			done[a.i], got[a.i] = true, a
		}
	}
}

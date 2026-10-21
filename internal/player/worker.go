package player

import (
	"context"
	"sync"
	"time"
)

// Coalescing keys. A queued job is replaced by a newer one with the same key.
const (
	keyPlay      = "play"
	keyPlayPause = "playpause" // Toggle's play or pause: only the latest intent matters
	keySeek      = "seek"
	keyVolume    = "volume"
	keyRepeat    = "repeat"
	keyShuffle   = "shuffle"
)

type job struct {
	key string // "" never coalesces
	// transport jobs (play, next, previous) are dropped when a newer play job is
	// queued behind them: the play replaces whatever they would have done.
	transport bool
	timeout   time.Duration
	run       func(ctx context.Context) error
	done      func(err error)
}

// worker runs bridge calls one at a time, in order, off the UI goroutine.
type worker struct {
	mu   sync.Mutex
	jobs []job
	wake chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newWorker() *worker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &worker{wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	w.wg.Add(1)
	go w.loop()
	return w
}

func (w *worker) push(j job) {
	w.mu.Lock()
	kept := w.jobs[:0]
	for _, q := range w.jobs {
		if (j.key != "" && q.key == j.key) || (j.key == keyPlay && q.transport) {
			continue
		}
		kept = append(kept, q)
	}
	w.jobs = append(kept, j)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *worker) pop() (job, bool) {
	for {
		w.mu.Lock()
		if len(w.jobs) > 0 {
			j := w.jobs[0]
			w.jobs[0] = job{}
			w.jobs = w.jobs[1:]
			w.mu.Unlock()
			return j, true
		}
		w.mu.Unlock()
		select {
		case <-w.wake:
		case <-w.ctx.Done():
			return job{}, false
		}
	}
}

func (w *worker) loop() {
	defer w.wg.Done()
	for {
		j, ok := w.pop()
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(w.ctx, j.timeout)
		err := j.run(ctx)
		cancel()
		if j.done != nil {
			j.done(err)
		}
	}
}

func (w *worker) close() {
	w.cancel()
	w.wg.Wait()
}

package lyrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/config"
)

// DefaultOffsetsPath is ~/Library/Application Support/encore/lyrics-offsets.json.
func DefaultOffsetsPath() string { return filepath.Join(config.StateDir(), "lyrics-offsets.json") }

const offsetsSaveDelay = 500 * time.Millisecond

// Offsets stores per-track lyric timing offsets keyed by pid. As with LRC's
// [offset:] tag, a positive offset shows lines sooner: the pane highlights
// LineAt(position + lead + offset). Set only touches memory and schedules a
// save, so it's cheap to call from a key handler; call Flush before exiting.
type Offsets struct {
	path string

	mu    sync.Mutex
	ms    map[string]int64 // pid -> milliseconds
	timer *time.Timer
	dirty bool

	saveMu sync.Mutex // serialises file writes
}

// LoadOffsets reads path; a missing or unreadable file starts empty.
func LoadOffsets(path string) *Offsets {
	o := &Offsets{path: path, ms: map[string]int64{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &o.ms)
		if o.ms == nil {
			o.ms = map[string]int64{}
		}
	}
	return o
}

// Get returns the offset for pid (0 when unset).
func (o *Offsets) Get(pid string) time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return time.Duration(o.ms[pid]) * time.Millisecond
}

// Set records the offset for pid, rounded to whole milliseconds; 0 removes it.
func (o *Offsets) Set(pid string, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if ms := d.Round(time.Millisecond).Milliseconds(); ms == 0 {
		delete(o.ms, pid)
	} else {
		o.ms[pid] = ms
	}
	o.dirty = true
	if o.timer == nil {
		o.timer = time.AfterFunc(offsetsSaveDelay, func() { o.Flush() })
	} else {
		o.timer.Reset(offsetsSaveDelay)
	}
}

// Flush writes pending changes now.
func (o *Offsets) Flush() error {
	o.saveMu.Lock()
	defer o.saveMu.Unlock()
	o.mu.Lock()
	if !o.dirty {
		o.mu.Unlock()
		return nil
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	data, err := json.MarshalIndent(o.ms, "", "  ")
	o.dirty = false
	o.mu.Unlock()
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(o.path, data, 0o644); err != nil {
		o.mu.Lock()
		o.dirty = true
		o.mu.Unlock()
		return err
	}
	return nil
}

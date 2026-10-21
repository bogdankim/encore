package lastfm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bogdankim/encore/internal/config"
)

// DefaultQueuePath is ~/Library/Application Support/encore/scrobbles.jsonl.
func DefaultQueuePath() string { return filepath.Join(config.StateDir(), "scrobbles.jsonl") }

// maxAge: Last.fm ignores scrobbles older than two weeks.
const maxAge = 14 * 24 * time.Hour

func dedupeKey(t Track) string {
	return t.Artist + "\x00" + t.Track + "\x00" + strconv.FormatInt(t.Timestamp, 10)
}

// readQueue loads a JSONL queue, skipping unreadable lines, duplicates and
// plays too old to scrobble. A missing file is an empty queue.
func readQueue(path string, now time.Time) []Track {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var q []Track
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var t Track
		if json.Unmarshal(sc.Bytes(), &t) != nil || t.Artist == "" || t.Track == "" || t.Timestamp == 0 {
			continue
		}
		if k := dedupeKey(t); !seen[k] && !tooOld(t, now) {
			seen[k] = true
			q = append(q, t)
		}
	}
	return q
}

// writeQueue replaces the file with q; path "" means no persistence.
func writeQueue(path string, q []Track) error {
	if path == "" {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // one object per line
	for _, t := range q {
		if err := enc.Encode(t); err != nil {
			return err
		}
	}
	return config.WriteFileAtomic(path, buf.Bytes(), 0o600)
}

func tooOld(t Track, now time.Time) bool { return now.Sub(time.Unix(t.Timestamp, 0)) > maxAge }

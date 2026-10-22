// Package cachegc keeps encore's cache folders (art, lyrics) from growing
// without bound: files nobody has used for a while go, and when what is left
// is still over a size cap the least recently used go too.
package cachegc

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Prune removes regular files directly in dir that were last written or
// touched more than maxAge ago, then removes the oldest of the rest until
// they total at most maxBytes. A zero maxAge or maxBytes turns that limit
// off. Files in use by a writer in the last minute (temp files) are left
// alone. It returns how many files and bytes it removed. Errors are
// ignored: a cache that can't be trimmed is still a cache.
func Prune(dir string, maxAge time.Duration, maxBytes int64) (files int, freed int64) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	type file struct {
		path string
		mod  time.Time
		size int64
	}
	now := time.Now()
	var keep []file
	var total int64
	remove := func(f file) {
		if os.Remove(f.path) == nil {
			files++
			freed += f.size
		}
	}
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := file{filepath.Join(dir, e.Name()), info.ModTime(), info.Size()}
		switch {
		case now.Sub(f.mod) < time.Minute:
			keep = append(keep, f)
			total += f.size
		case maxAge > 0 && now.Sub(f.mod) > maxAge,
			strings.HasSuffix(e.Name(), ".tmp") && now.Sub(f.mod) > time.Hour:
			remove(f)
		default:
			keep = append(keep, f)
			total += f.size
		}
	}
	if maxBytes <= 0 || total <= maxBytes {
		return files, freed
	}
	slices.SortFunc(keep, func(a, b file) int { return a.mod.Compare(b.mod) })
	for _, f := range keep {
		if total <= maxBytes {
			break
		}
		remove(f)
		total -= f.size
	}
	return files, freed
}

// RemoveGlob removes the regular files that match pattern and were last
// written more than olderThan ago (0 removes them all), and returns how many
// went. It is for leftovers of a format or a crash, which nothing else will
// ever clean up.
func RemoveGlob(pattern string, olderThan time.Duration) (files int) {
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		info, err := os.Lstat(m)
		if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) < olderThan {
			continue
		}
		if os.Remove(m) == nil {
			files++
		}
	}
	return files
}

// Touch marks path as used now when it was last written or touched more than
// a day ago, so Prune keeps what is read. It costs one stat on a hit.
func Touch(path string) {
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) > 24*time.Hour {
		now := time.Now()
		os.Chtimes(path, now, now)
	}
}

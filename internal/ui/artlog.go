package ui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// With ENCORE_DEBUG_ART=1 the art store logs what it does with every load
// (asked, queued, pruned, started, done, none, failed) and what the
// now-playing art shows and why, to ~/Library/Logs/encore/art.log. It is
// for diagnosing missing covers on a real machine; off, it costs a nil
// check.

const artLogMax = 5 << 20 // rotated to art.log.1 when larger at start

var artLog = openArtLog(os.Getenv("ENCORE_DEBUG_ART") == "1")

func openArtLog(on bool) *log.Logger {
	if !on {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, "Library", "Logs", "encore", "art.log")
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return nil
	}
	if st, err := os.Stat(path); err == nil && st.Size() > artLogMax {
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	l := log.New(f, "", log.Ltime|log.Lmicroseconds)
	l.Printf("art log started (pid %d)", os.Getpid())
	return l
}

// artf logs to the art log when it is on.
func artf(format string, args ...any) {
	if artLog != nil {
		artLog.Output(2, fmt.Sprintf(format, args...))
	}
}

func (r artReq) String() string {
	kind := "tile"
	if r.hero {
		kind = "hero"
	}
	return fmt.Sprintf("%s %s pid=%s %dx%d", kind, r.key, r.pid, r.w, r.h)
}

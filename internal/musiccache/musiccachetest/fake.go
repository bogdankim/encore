// Package musiccachetest builds stand-ins for Music's Cache.db in tests.
package musiccachetest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/musiccache"
)

// Fake is a Cache.db with only the tables and columns musiccache reads.
// Its connection stays open, so writes sit in the WAL as with Music.
type Fake struct {
	Dir string

	t     testing.TB
	db    *musiccache.DB
	id    int64
	clock time.Time
}

// New creates a Fake in a temporary directory, or skips the test when
// this build has no SQLite.
func New(t testing.TB) *Fake {
	t.Helper()
	if !musiccache.Available {
		t.Skip("no SQLite in this build")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "fsCachedData"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := musiccache.OpenDB("file:"+filepath.Join(dir, "Cache.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := &Fake{Dir: dir, t: t, db: db, id: 100, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	f.exec(`PRAGMA journal_mode=WAL;
		PRAGMA wal_autocheckpoint=0;
		CREATE TABLE cfurl_cache_response(entry_ID INTEGER PRIMARY KEY AUTOINCREMENT UNIQUE, request_key TEXT UNIQUE, time_stamp NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE cfurl_cache_receiver_data(entry_ID INTEGER PRIMARY KEY, isDataOnFS INTEGER, receiver_data BLOB);`)
	return f
}

func (f *Fake) exec(sql string) {
	f.t.Helper()
	if err := f.db.Exec(sql); err != nil {
		f.t.Fatal(err)
	}
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Add stores a response for key at time at ("2006-01-02 15:04:05"); onFS
// puts the body in fsCachedData as Music does for larger ones. It returns
// the row id.
func (f *Fake) Add(key, at string, body []byte, onFS bool) int64 {
	f.t.Helper()
	f.id++
	data := fmt.Sprintf("CAST(%s AS BLOB)", quote(string(body)))
	isFS := 0
	if onFS {
		name := fmt.Sprintf("0000-%d", f.id)
		if err := os.WriteFile(filepath.Join(f.Dir, "fsCachedData", name), body, 0o644); err != nil {
			f.t.Fatal(err)
		}
		data, isFS = quote(name), 1
	}
	f.exec(fmt.Sprintf(`INSERT INTO cfurl_cache_response(entry_ID, request_key, time_stamp) VALUES (%d, %s, %s);
		INSERT INTO cfurl_cache_receiver_data(entry_ID, isDataOnFS, receiver_data) VALUES (%d, %d, %s);`,
		f.id, quote(key), quote(at), f.id, isFS, data))
	f.Touch()
	return f.id
}

// Remove deletes row id, as NSURLCache does when it evicts a response.
func (f *Fake) Remove(id int64) {
	f.t.Helper()
	f.exec(fmt.Sprintf(`DELETE FROM cfurl_cache_response WHERE entry_ID = %d; DELETE FROM cfurl_cache_receiver_data WHERE entry_ID = %d;`, id, id))
	f.Touch()
}

// Touch moves the WAL's modification time forward: Music's writes land
// within the same second, and a stat must see each one.
func (f *Fake) Touch() {
	f.t.Helper()
	f.clock = f.clock.Add(time.Second)
	if err := os.Chtimes(filepath.Join(f.Dir, "Cache.db-wal"), f.clock, f.clock); err != nil {
		f.t.Fatal(err)
	}
}

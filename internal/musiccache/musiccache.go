// Package musiccache reads response bodies from Music.app's URL cache
// (NSURLCache), ~/Library/Caches/com.apple.Music.
//
// The database also holds request and response objects with sign-in
// headers (cfurl_cache_blob_data). This package never reads that table or
// any headers: Responses reads the request URL and time of rows in
// cfurl_cache_response, Body the stored body of one row in
// cfurl_cache_receiver_data. See docs/research/README.md.
package musiccache

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	rowsSQL = `SELECT r.entry_ID, r.request_key, r.time_stamp FROM cfurl_cache_response r WHERE r.request_key LIKE ?`
	bodySQL = `SELECT d.isDataOnFS, d.receiver_data FROM cfurl_cache_receiver_data d WHERE d.entry_ID = ?`
)

// DefaultDir is Music.app's cache folder, ~/Library/Caches/com.apple.Music.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Caches", "com.apple.Music")
}

// Stamp is the modification time and size of Cache.db and its WAL. Music
// writes new responses to the WAL, so a changed Stamp means the rows may
// have changed.
type Stamp [2]FileStamp

// FileStamp is one file's modification time and size; zero when missing.
type FileStamp struct {
	Mod  time.Time
	Size int64
}

// StampOf stats Cache.db and Cache.db-wal in dir.
func StampOf(dir string) Stamp {
	db := filepath.Join(dir, "Cache.db")
	return Stamp{stat(db), stat(db + "-wal")}
}

// Missing reports whether there was no Cache.db.
func (s Stamp) Missing() bool { return s[0] == FileStamp{} }

func stat(path string) FileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return FileStamp{}
	}
	return FileStamp{fi.ModTime(), fi.Size()}
}

// Response is one cached response: its row id, request URL and the
// time_stamp column ("2006-01-02 15:04:05", UTC).
type Response struct {
	ID  int64
	Key string
	At  string
}

// Time parses At; zero if it doesn't parse.
func (r Response) Time() time.Time {
	t, _ := time.Parse(time.DateTime, r.At)
	return t
}

// Conn is an open read-only connection to Music's Cache.db.
type Conn struct {
	dir string
	db  *DB
}

// Open opens Cache.db in dir read-only. Not immutable: the newest rows are
// in the WAL, and immutable=1 would show only the last checkpoint.
func Open(dir string) (*Conn, error) {
	path := filepath.Join(dir, "Cache.db")
	db, err := OpenDB("file:"+(&url.URL{Path: path}).EscapedPath()+"?mode=ro", true)
	if err != nil {
		return nil, err
	}
	return &Conn{dir: dir, db: db}, nil
}

// Close closes the connection.
func (c *Conn) Close() {
	if c != nil {
		c.db.Close()
	}
}

// Responses lists the rows whose request URL matches the SQL LIKE pattern.
func (c *Conn) Responses(like string) ([]Response, error) {
	var out []Response
	err := c.db.Query(rowsSQL, []any{like}, func(v []any) error {
		id, _ := v[0].(int64)
		key, _ := v[1].([]byte)
		at, _ := v[2].([]byte)
		out = append(out, Response{id, string(key), string(at)})
		return nil
	})
	return out, err
}

// Body returns the body of row id: in the row, or for larger ones in a
// file under fsCachedData named by the row. ok is false when the row has no
// body (yet), or it is over max bytes.
func (c *Conn) Body(id int64, max int) (body []byte, ok bool) {
	var onFS, found bool
	var data []byte
	err := c.db.Query(bodySQL, []any{id}, func(v []any) error {
		n, _ := v[0].(int64)
		onFS = n == 1
		data, _ = v[1].([]byte)
		found = true
		return nil
	})
	if err != nil || !found {
		return nil, false
	}
	if !onFS {
		return data, len(data) > 0 && len(data) <= max
	}
	name := string(data)
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return nil, false
	}
	f, err := os.Open(filepath.Join(c.dir, "fsCachedData", name))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	return b, err == nil && len(b) > 0 && len(b) <= max
}

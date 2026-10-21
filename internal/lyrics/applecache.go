package lyrics

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// SourceApple is Apple Music's own lyrics, read from Music.app's URL cache.
const SourceApple = "apple"

// Music.app's URL cache (NSURLCache). For each song it plays, Music asks
// amp-api.music.apple.com for the song with its lyrics, and the response
// lands here; see docs/research/README.md ("Apple Music lyrics").
//
// The database also holds request and response objects with sign-in
// headers. Only the two queries below touch it: the request URL and time of
// lyric responses, then the body of those responses. Nothing else is read.
const (
	appleRowsSQL = `SELECT r.entry_ID, r.request_key, r.time_stamp FROM cfurl_cache_response r ` +
		`WHERE r.request_key LIKE 'https://amp-api.music.apple.com/v1/catalog/%' AND r.request_key LIKE '%lyrics%'`
	appleBodySQL = `SELECT d.isDataOnFS, d.receiver_data FROM cfurl_cache_receiver_data d WHERE d.entry_ID = ?`

	appleMaxBody = 4 << 20
	appleSlack   = 2 * time.Second // duration match window
)

// DefaultAppleCacheDir is Music.app's cache folder,
// ~/Library/Caches/com.apple.Music.
func DefaultAppleCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Caches", "com.apple.Music")
}

// AppleCache finds Apple Music's synced lyrics in Music.app's URL cache.
// It keeps an index of the lyric responses there and reads the database
// again only when its files change. Music fetches a song's lyrics when it
// starts playing and evicts them some time later, so the Service copies
// what it finds into its own cache.
//
// It is a Provider, and safe for concurrent use.
type AppleCache struct {
	dir string

	mu      sync.Mutex
	stamp   [2]fileStamp
	indexed bool
	entries map[int64]*appleEntry
}

type fileStamp struct {
	mod  time.Time
	size int64
}

type appleEntry struct {
	key, at string // request URL and time_stamp: a row rewritten in place reads again
	songs   []*appleSong
}

// appleSong is one song of a lyrics response.
type appleSong struct {
	ids           []string // catalog ids it answers for
	title, artist string
	dur           time.Duration
	at            string // the response's time_stamp, "2006-01-02 15:04:05" UTC
	ttml          string // "" when Apple has no lyrics for it

	once sync.Once
	lyr  *Lyrics
}

// NewAppleCache reads Music's cache in dir (normally DefaultAppleCacheDir).
// It returns nil when this build can't read SQLite.
func NewAppleCache(dir string) *AppleCache {
	if !sqliteAvailable || dir == "" {
		return nil
	}
	return &AppleCache{dir: dir, entries: map[int64]*appleEntry{}}
}

func (*AppleCache) Name() string { return SourceApple }

// Lookup returns the Apple lyrics for q, or ErrNotFound. A cache it can't
// read is not an error: it only means no Apple lyrics.
func (a *AppleCache) Lookup(_ context.Context, q Query) (*Lyrics, error) {
	if l := a.Find(q); l != nil {
		return l, nil
	}
	return nil, ErrNotFound
}

// Find returns the Apple lyrics for q, or nil. It costs two stat calls
// when Music's cache hasn't changed since the last call.
func (a *AppleCache) Find(q Query) *Lyrics {
	if a == nil || (q.StoreID == "" && (q.Title == "" || q.Artist == "")) {
		return nil
	}
	a.mu.Lock()
	a.refresh()
	var best *appleSong
	for _, e := range a.entries {
		for _, s := range e.songs {
			if s.matches(q) && (best == nil || s.at > best.at) {
				best = s
			}
		}
	}
	a.mu.Unlock()
	if best == nil {
		return nil
	}
	return best.lyrics()
}

func (s *appleSong) lyrics() *Lyrics {
	s.once.Do(func() {
		if s.ttml == "" {
			return
		}
		if l, err := ParseTTML(s.ttml); err == nil {
			l.Source = SourceApple
			s.lyr = l
		}
	})
	return s.lyr
}

// matches is a store id match, or the same title and artist (cleaned up as
// for LRCLIB) and a duration within appleSlack.
func (s *appleSong) matches(q Query) bool {
	if q.StoreID != "" && slices.Contains(s.ids, q.StoreID) {
		return true
	}
	if q.Title == "" || q.Artist == "" {
		return false
	}
	if q.Duration > 0 && s.dur > 0 && (q.Duration-s.dur > appleSlack || s.dur-q.Duration > appleSlack) {
		return false
	}
	return sameName(q.Title, s.title, CleanTitle) && sameName(q.Artist, s.artist, PrimaryArtist)
}

func sameName(a, b string, clean func(string) string) bool {
	ka, kb := Key(a), Key(b)
	return ka != "" && (ka == kb || Key(clean(a)) == Key(clean(b)))
}

func stat(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{fi.ModTime(), fi.Size()}
}

// refresh reads the index again if Music's database changed; a.mu held.
func (a *AppleCache) refresh() {
	db := filepath.Join(a.dir, "Cache.db")
	stamp := [2]fileStamp{stat(db), stat(db + "-wal")}
	if a.indexed && stamp == a.stamp {
		return
	}
	if stamp[0] == (fileStamp{}) {
		a.entries, a.stamp, a.indexed = map[int64]*appleEntry{}, stamp, true
		return
	}
	entries, err := a.read(db)
	if err != nil {
		// Locked or mid-write: keep the old index and try again next time.
		return
	}
	a.entries, a.stamp, a.indexed = entries, stamp, true
}

// read lists the lyric responses and parses the ones not seen before.
// Read-only and not immutable: the newest responses are in the WAL.
func (a *AppleCache) read(path string) (map[int64]*appleEntry, error) {
	db, err := openSQLite("file:"+(&url.URL{Path: path}).EscapedPath()+"?mode=ro", true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	type row struct {
		id      int64
		key, at string
	}
	var rows []row
	err = db.Query(appleRowsSQL, nil, func(v []any) error {
		id, _ := v[0].(int64)
		key, _ := v[1].([]byte)
		at, _ := v[2].([]byte)
		rows = append(rows, row{id, string(key), string(at)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*appleEntry, len(rows))
	for _, r := range rows {
		if e := a.entries[r.id]; e != nil && e.key == r.key && e.at == r.at {
			out[r.id] = e
			continue
		}
		body, ok := a.body(db, r.id)
		if !ok {
			continue // not written yet; the next change reads it
		}
		out[r.id] = &appleEntry{key: r.key, at: r.at, songs: parseAppleResponse(body, r.key, r.at)}
	}
	return out, nil
}

// body is a response body: in the row, or for larger ones in a file under
// fsCachedData named by the row.
func (a *AppleCache) body(db *sqliteDB, id int64) ([]byte, bool) {
	var onFS bool
	var data []byte
	found := false
	err := db.Query(appleBodySQL, []any{id}, func(v []any) error {
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
		return data, len(data) > 0
	}
	name := string(data)
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return nil, false
	}
	f, err := os.Open(filepath.Join(a.dir, "fsCachedData", name))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, appleMaxBody+1))
	return b, err == nil && len(b) <= appleMaxBody
}

// ampResponse is the part of an amp-api songs response with lyrics that
// encore uses.
type ampResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name             string `json:"name"`
			ArtistName       string `json:"artistName"`
			AlbumName        string `json:"albumName"`
			DurationInMillis int64  `json:"durationInMillis"`
			PlayParams       struct {
				ID string `json:"id"`
			} `json:"playParams"`
		} `json:"attributes"`
		Relationships map[string]struct {
			Data []struct {
				Attributes struct {
					TTML              string `json:"ttml"`
					TTMLLocalizations string `json:"ttmlLocalizations"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"relationships"`
	} `json:"data"`
	Meta struct {
		Filters struct {
			Equivalents map[string][]struct {
				ID string `json:"id"`
			} `json:"equivalents"`
		} `json:"filters"`
	} `json:"meta"`
}

// parseAppleResponse reads the songs of one response. key is the request
// URL: the id asked for (/songs/ID or filter[equivalents]=ID) also finds
// the song, as the catalog may answer with an equivalent.
func parseAppleResponse(body []byte, key, at string) []*appleSong {
	var r ampResponse
	if json.Unmarshal(body, &r) != nil {
		return nil
	}
	asked := map[string][]string{} // answer id → ids asked for
	for id, eqs := range r.Meta.Filters.Equivalents {
		for _, e := range eqs {
			asked[e.ID] = append(asked[e.ID], id)
		}
	}
	if u, err := url.Parse(key); err == nil {
		for _, id := range strings.Split(u.Query().Get("filter[equivalents]"), ",") {
			if len(r.Data) == 1 && id != "" {
				asked[r.Data[0].ID] = append(asked[r.Data[0].ID], id)
			}
		}
		if i := strings.LastIndex(u.Path, "/songs/"); i >= 0 && len(r.Data) == 1 {
			asked[r.Data[0].ID] = append(asked[r.Data[0].ID], u.Path[i+len("/songs/"):])
		}
	}
	var out []*appleSong
	for _, d := range r.Data {
		if d.Type != "" && d.Type != "songs" {
			continue
		}
		attrs := d.Attributes
		s := &appleSong{
			title: attrs.Name, artist: attrs.ArtistName,
			dur: time.Duration(attrs.DurationInMillis) * time.Millisecond,
			at:  at,
		}
		s.ids = appendID(s.ids, d.ID)
		s.ids = appendID(s.ids, attrs.PlayParams.ID)
		for _, id := range asked[d.ID] {
			s.ids = appendID(s.ids, id)
		}
		// syllable-lyrics (word timing when Apple has it) before lyrics
		for _, rel := range [...]string{"syllable-lyrics", "lyrics"} {
			for _, l := range d.Relationships[rel].Data {
				if s.ttml == "" {
					s.ttml = l.Attributes.TTML
				}
				if s.ttml == "" {
					s.ttml = l.Attributes.TTMLLocalizations
				}
			}
		}
		out = append(out, s)
	}
	return out
}

func appendID(ids []string, id string) []string {
	if id == "" || slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}

package home

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/config"
	"github.com/bogdankim/encore/internal/musiccache"
)

// ErrNotCached means neither Music's cache nor encore's copy has a Home
// page: Music hasn't shown Home since its cache was last trimmed, and
// encore never saw one.
var ErrNotCached = errors.New("home: Music has not cached its Home page")

const (
	maxBody     = 32 << 20
	fileVersion = 1
)

// DefaultStoreDir is where a Reader keeps its copy, ~/Library/Caches/encore/home.
func DefaultStoreDir() string { return filepath.Join(config.CacheDir(), "home") }

// The two responses the page is made of.
const (
	partListenNow = iota
	partNew
	nParts
)

type source struct {
	like  string // SQL LIKE over request_key
	path  string // path suffix, checked again in Go
	name  string // the name= query value
	file  string
	parse func([]byte) ([]Shelf, bool)
}

var sources = [nParts]source{
	partListenNow: {"https://amp-api%/v1/me/recommendations?%", "/v1/me/recommendations", "listen-now", "listen-now.json", parseListenNow},
	partNew:       {"https://amp-api%/v1/editorial/%/groupings?%", "/groupings", "music", "new.json", parseGroupings},
}

const playlistsLike = "https://amp-api%/v1/catalog/%/playlists%"

// part is one parsed response, as stored in encore's cache. At is the
// response's time_stamp in Music's cache.
type part struct {
	V       int     `json:"v"`
	At      string  `json:"at"`
	Shelves []Shelf `json:"shelves"`
}

func (p *part) time() time.Time {
	if p == nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.DateTime, p.At)
	return t
}

type tracksFile struct {
	V         int                    `json:"v"`
	Playlists map[string]savedTracks `json:"playlists"`
}

type savedTracks struct {
	At     string   `json:"at"`
	Tracks []string `json:"tracks"`
}

// row is the newest response of a source in Music's cache; part is nil
// when its body didn't parse.
type row struct {
	id   int64
	at   string
	part *part
}

type playlistRow struct {
	key, at string
	lists   map[string][]string
}

// Reader reads the Home page from Music's cache. It reads Music's database
// again only when Cache.db or its WAL changed, and parses a response only
// once. It is safe for concurrent use.
type Reader struct {
	music, store string
	now          func() time.Time

	mu        sync.Mutex
	stamp     musiccache.Stamp
	indexed   bool
	live      [nParts]*row
	playlists map[int64]*playlistRow

	loaded bool // the saved copy has been read
	saved  [nParts]*part
	tracks map[string]savedTracks
}

// New reads Music's cache in musicDir (musiccache.DefaultDir) and keeps its
// copy in storeDir (DefaultStoreDir; "" keeps nothing).
func New(musicDir, storeDir string) *Reader {
	return &Reader{music: musicDir, store: storeDir, now: time.Now, playlists: map[int64]*playlistRow{}}
}

// Changed reports whether Music's cache changed since the last Load: two
// stat calls. A change doesn't mean a new Home page; most are lyrics and
// other responses. Load is about as cheap when nothing changed, and
// recomputes what is on air on live stations.
func (r *Reader) Changed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.indexed || musiccache.StampOf(r.music) != r.stamp
}

// Load returns the newest Home page there is: Music's cached responses, or
// encore's copy of them once Music has dropped them. A newer response in
// Music's cache is saved first.
func (r *Reader) Load(ctx context.Context) (*Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.loaded {
		r.readSaved()
		r.loaded = true
	}
	r.refresh(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, l := range r.live {
		if l == nil || l.part == nil || (r.saved[i] != nil && l.at <= r.saved[i].At) {
			continue
		}
		r.saved[i] = l.part
		r.save(sources[i].file, l.part)
	}
	r.mergeTracks()

	if r.saved[partListenNow] == nil && r.saved[partNew] == nil {
		return nil, ErrNotCached
	}
	now := r.now()
	tracks := make(map[string][]string, len(r.tracks))
	for id, t := range r.tracks {
		tracks[id] = t.Tracks
	}
	p := &Page{Updated: r.saved[partListenNow].time()}
	if t := r.saved[partNew].time(); t.After(p.Updated) {
		p.Updated = t
	}
	if l := r.saved[partListenNow]; l != nil {
		p.Shelves = present(l.Shelves, now, tracks)
	}
	if n := r.saved[partNew]; n != nil {
		for _, s := range present(n.Shelves, now, tracks) {
			if s.Kind == KindLive {
				p.Shelves = append(p.Shelves, s)
			} else {
				p.New = append(p.New, s)
			}
		}
	}
	return p, nil
}

// refresh reads Music's database again if it changed; r.mu held. A
// database that is locked, mid-write or unreadable keeps what was read
// before, and the next call tries again.
func (r *Reader) refresh(ctx context.Context) {
	stamp := musiccache.StampOf(r.music)
	if r.indexed && stamp == r.stamp {
		return
	}
	if stamp.Missing() || !musiccache.Available {
		r.live, r.playlists = [nParts]*row{}, map[int64]*playlistRow{}
		r.stamp, r.indexed = stamp, true
		return
	}
	c, err := musiccache.Open(r.music)
	if err != nil {
		return
	}
	defer c.Close()
	var live [nParts]*row
	for i, src := range sources {
		rows, err := c.Responses(src.like)
		if err != nil {
			return
		}
		best := newest(rows, src)
		if best == nil {
			continue
		}
		if l := r.live[i]; l != nil && l.id == best.ID && l.at == best.At {
			live[i] = l
			continue
		}
		if ctx.Err() != nil {
			return
		}
		body, ok := c.Body(best.ID, maxBody)
		if !ok {
			// Not written yet: writing it changes the WAL, and so the stamp.
			live[i] = r.live[i]
			continue
		}
		l := &row{id: best.ID, at: best.At}
		if shelves, ok := src.parse(body); ok {
			l.part = &part{V: fileVersion, At: best.At, Shelves: shelves}
		}
		live[i] = l
	}
	rows, err := c.Responses(playlistsLike)
	if err != nil {
		return
	}
	playlists := make(map[int64]*playlistRow, len(rows))
	for _, resp := range rows {
		if p := r.playlists[resp.ID]; p != nil && p.key == resp.Key && p.at == resp.At {
			playlists[resp.ID] = p
			continue
		}
		body, ok := c.Body(resp.ID, maxBody)
		if !ok {
			continue
		}
		playlists[resp.ID] = &playlistRow{key: resp.Key, at: resp.At, lists: parsePlaylistTracks(resp.Key, body)}
	}
	r.live, r.playlists = live, playlists
	r.stamp, r.indexed = stamp, true
}

// newest is the latest response of src: matching path and name=, then
// the newest time_stamp.
func newest(rows []musiccache.Response, src source) *musiccache.Response {
	var best *musiccache.Response
	for i := range rows {
		u, err := url.Parse(rows[i].Key)
		if err != nil || !strings.HasSuffix(u.Path, src.path) || u.Query().Get("name") != src.name {
			continue
		}
		if best == nil || cmp.Or(cmp.Compare(rows[i].At, best.At), cmp.Compare(rows[i].ID, best.ID)) > 0 {
			best = &rows[i]
		}
	}
	return best
}

// mergeTracks takes newer playlist tracks from Music's cache, keeps those
// of playlists on the page, and saves them when they changed; r.mu held.
func (r *Reader) mergeTracks() {
	onPage := map[string]bool{}
	for _, p := range r.saved {
		if p == nil {
			continue
		}
		for _, s := range p.Shelves {
			for _, it := range s.Items {
				if it.Type == TypePlaylist {
					onPage[it.ID] = true
				}
			}
		}
	}
	changed := false
	if r.tracks == nil {
		r.tracks = map[string]savedTracks{}
	}
	for _, p := range r.playlists {
		for id, ids := range p.lists {
			if cur, ok := r.tracks[id]; onPage[id] && (!ok || p.at > cur.At) {
				r.tracks[id] = savedTracks{At: p.at, Tracks: ids}
				changed = true
			}
		}
	}
	for id := range r.tracks {
		if !onPage[id] {
			delete(r.tracks, id)
			changed = true
		}
	}
	if changed {
		r.save("playlists.json", tracksFile{V: fileVersion, Playlists: r.tracks})
	}
}

func (r *Reader) readSaved() {
	if r.store == "" {
		return
	}
	for i, src := range sources {
		var p part
		if readJSON(filepath.Join(r.store, src.file), &p) && p.V == fileVersion && p.At != "" {
			r.saved[i] = &p
		}
	}
	var t tracksFile
	if readJSON(filepath.Join(r.store, "playlists.json"), &t) && t.V == fileVersion {
		r.tracks = t.Playlists
	}
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// save writes v to the store; a failure only means no copy for next time.
func (r *Reader) save(name string, v any) {
	if r.store == "" {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = config.WriteFileAtomic(filepath.Join(r.store, name), b, 0o600)
}

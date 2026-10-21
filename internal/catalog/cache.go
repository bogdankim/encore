package catalog

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/config"
)

// DefaultCacheDir is ~/Library/Caches/encore/catalog.
func DefaultCacheDir() string { return filepath.Join(config.CacheDir(), "catalog") }

// memCache is an LRU of response bodies with a per-entry expiry.
type memCache struct {
	mu    sync.Mutex
	max   int
	ll    *list.List // front = most recent
	items map[string]*list.Element
}

type memEntry struct {
	key     string
	body    []byte
	expires time.Time
}

func newMemCache(max int) *memCache {
	return &memCache{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *memCache) get(key string, now time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*memEntry)
	if !now.Before(e.expires) {
		c.ll.Remove(el)
		delete(c.items, key)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.body, true
}

func (c *memCache) put(key string, body []byte, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*memEntry)
		e.body, e.expires = body, expires
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&memEntry{key: key, body: body, expires: expires})
	for c.ll.Len() > c.max {
		el := c.ll.Back()
		c.ll.Remove(el)
		delete(c.items, el.Value.(*memEntry).key)
	}
}

const diskVersion = 1

// diskCache keeps one JSON file per lookup URL.
type diskCache struct {
	dir       string
	pruneOnce sync.Once
}

type diskEntry struct {
	V       int             `json:"v"`
	URL     string          `json:"url"`
	Fetched time.Time       `json:"fetched"`
	Body    json.RawMessage `json:"body"`
}

func (d *diskCache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(d.dir, hex.EncodeToString(sum[:12])+".json")
}

// get returns the cached body and when it was fetched. Callers decide
// whether it's fresh: a stale entry still beats an error.
func (d *diskCache) get(key string) ([]byte, time.Time, bool) {
	data, err := os.ReadFile(d.path(key))
	if err != nil {
		return nil, time.Time{}, false
	}
	var e diskEntry
	if json.Unmarshal(data, &e) != nil || e.V != diskVersion || e.URL != key {
		return nil, time.Time{}, false
	}
	return e.Body, e.Fetched, true
}

func (d *diskCache) put(key string, body []byte, now time.Time) {
	data, err := json.Marshal(diskEntry{V: diskVersion, URL: key, Fetched: now, Body: body})
	if err != nil {
		return
	}
	// A failed cache write only costs a refetch later.
	_ = config.WriteFileAtomic(d.path(key), data, 0o644)
}

// prune deletes files untouched for longer than maxAge, once per process.
func (d *diskCache) prune(maxAge time.Duration, now time.Time) {
	d.pruneOnce.Do(func() {
		ents, err := os.ReadDir(d.dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > maxAge {
				os.Remove(filepath.Join(d.dir, e.Name()))
			}
		}
	})
}

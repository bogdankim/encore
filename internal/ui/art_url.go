package ui

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/player"
)

// Artwork of Apple Music catalog items comes from a URL rather than a pid:
// the art store fetches it over HTTP (or through ArtFetch in demo mode),
// keeps a JPEG per item and size on disk, and otherwise treats it like
// bridge artwork (same queue, workers, LRU and frame order).

const (
	urlArtTimeout = 10 * time.Second
	urlArtMaxBody = 8 << 20
)

// ArtFetch returns the image bytes (JPEG or PNG) at url.
type ArtFetch func(ctx context.Context, url string) ([]byte, error)

// errArtNotFound from an ArtFetch means the item has no artwork.
var errArtNotFound = errors.New("artwork not found")

// isArtURL reports whether an art pid is a URL to fetch rather than a
// Music persistent ID.
func isArtURL(pid string) bool { return strings.Contains(pid, "://") }

// urlArt loads artwork by URL. The zero value fetches over HTTP and keeps
// no disk cache.
type urlArt struct {
	fetch ArtFetch
	dir   string // ~/Library/Caches/encore/art; "" keeps nothing on disk
}

// load returns url's artwork at px×px. key names the item ("catalog:<id>")
// for the disk cache.
func (u urlArt) load(key, url string, px int) (data []byte, none bool, err error) {
	path := u.path(key, url, px)
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data, false, nil
		}
	}
	fetch := u.fetch
	if fetch == nil {
		fetch = httpArt
	}
	ctx, cancel := context.WithTimeout(context.Background(), urlArtTimeout)
	data, err = fetch(ctx, catalog.ResizeArt(url, px))
	cancel()
	switch {
	case errors.Is(err, errArtNotFound):
		return nil, true, nil
	case err != nil:
		return nil, false, err
	case len(data) == 0:
		return nil, true, nil
	}
	if path != "" {
		writeFileAtomic(path, data) // a failed write only costs a refetch
	}
	return data, false, nil
}

// path is the cache file for key at px: catalog-<id>-<px>.jpg, or a hash
// of the URL for keys that carry no catalog id.
func (u urlArt) path(key, url string, px int) string {
	if u.dir == "" {
		return ""
	}
	id := strings.TrimPrefix(key, player.CatalogAlbumPrefix)
	if id == key || !digits(id) {
		h := fnv.New64a()
		h.Write([]byte(catalog.ResizeArt(url, 100)))
		id = "u" + strconv.FormatUint(h.Sum64(), 16)
	}
	return filepath.Join(u.dir, fmt.Sprintf("catalog-%s-%d.jpg", id, px))
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func writeFileAtomic(path string, data []byte) {
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".art-*")
	if err != nil {
		return
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
}

var artHTTP = &http.Client{Timeout: urlArtTimeout}

// httpArt GETs url. 404 and 410 mean there is no artwork.
func httpArt(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", catalog.UserAgent)
	resp, err := artHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, errArtNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("artwork: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, urlArtMaxBody))
}

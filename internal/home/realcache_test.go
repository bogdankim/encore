package home

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/musiccache"
)

// TestRealCache reads this Mac's Music cache, read-only, and logs the
// shelves: ENCORE_REAL_HOME=1 go test -run RealCache -v ./internal/home.
// The copy goes to a temporary directory, not encore's cache.
func TestRealCache(t *testing.T) {
	if os.Getenv("ENCORE_REAL_HOME") == "" {
		t.Skip("set ENCORE_REAL_HOME=1 to read Music's cache")
	}
	store := t.TempDir()
	r := New(musiccache.DefaultDir(), store)
	start := time.Now()
	p, err := r.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := time.Since(start)
	start = time.Now()
	if _, err := r.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Logf("first Load %v, unchanged Load %v", first.Round(time.Microsecond), time.Since(start).Round(time.Microsecond))
	t.Logf("updated %s", p.Updated.Local().Format("2006-01-02 15:04"))
	withTracks, playlists := 0, 0
	log := func(section string, ss []Shelf) {
		for _, s := range ss {
			types := map[string]int{}
			for _, it := range s.Items {
				types[it.Type]++
				if it.Type == TypePlaylist {
					playlists++
					if len(it.Tracks) > 0 {
						withTracks++
					}
				}
			}
			t.Logf("%-5s %-16s %-34q %2d items %v", section, s.Kind, s.Title, len(s.Items), types)
			if s.Kind == KindLive {
				for _, it := range s.Items {
					t.Logf("        %-24s subtitle %q now %q, %d events", it.Title, it.Subtitle, it.NowPlaying, len(it.Events))
				}
			}
		}
	}
	log("home", p.Shelves)
	log("new", p.New)
	t.Logf("playlists with tracks: %d of %d", withTracks, playlists)
	if len(p.Shelves) > 0 && len(p.Shelves[0].Items) > 0 {
		t.Logf("art: %s", p.Shelves[0].Items[0].ArtURL(300))
	}
	files, _ := filepath.Glob(filepath.Join(store, "*"))
	for _, f := range files {
		fi, _ := os.Stat(f)
		b, _ := os.ReadFile(f)
		for _, bad := range []string{"geoHash", "social", "concert", "Friends"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s holds %q", filepath.Base(f), bad)
			}
		}
		t.Logf("saved %s, %d bytes", filepath.Base(f), fi.Size())
	}
}

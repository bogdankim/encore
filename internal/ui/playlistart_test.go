package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

func solid(c color.Color, px int) image.Image {
	m := image.NewNRGBA(image.Rect(0, 0, px, px))
	for y := range px {
		for x := range px {
			m.Set(x, y, c)
		}
	}
	return m
}

var (
	red    = color.NRGBA{255, 0, 0, 255}
	green  = color.NRGBA{0, 255, 0, 255}
	blue   = color.NRGBA{0, 0, 255, 255}
	white  = color.NRGBA{255, 255, 255, 255}
	purple = color.NRGBA{128, 0, 128, 255}
)

func sameColor(a, b color.Color) bool {
	r1, g1, b1, _ := a.RGBA()
	r2, g2, b2, _ := b.RGBA()
	near := func(x, y uint32) bool { return max(x, y)-min(x, y) < 0x0300 }
	return near(r1, r2) && near(g1, g2) && near(b1, b2)
}

// quadrants checks the colour at the centre of each quarter of m.
func quadrants(t *testing.T, m image.Image, want ...color.Color) {
	t.Helper()
	b := m.Bounds()
	q := b.Dx() / 4
	at := []image.Point{{q, q}, {3 * q, q}, {q, 3 * q}, {3 * q, 3 * q}}
	for i, p := range at {
		if got := m.At(p.X, p.Y); !sameColor(got, want[i]) {
			t.Errorf("quadrant %d = %v, want %v", i, got, want[i])
		}
	}
}

func TestMosaicComposition(t *testing.T) {
	for _, px := range []int{64, 65} {
		m := mosaic([]image.Image{solid(red, 30), solid(green, 600), solid(blue, 7), solid(white, 100)}, px)
		if m.Bounds() != image.Rect(0, 0, px, px) {
			t.Fatalf("%d: bounds %v", px, m.Bounds())
		}
		quadrants(t, m, red, green, blue, white)
		// no gaps: every pixel is opaque and the corners belong to their cell
		for _, p := range []image.Point{{0, 0}, {px - 1, 0}, {0, px - 1}, {px - 1, px - 1}, {px / 2, px / 2}} {
			if _, _, _, a := m.At(p.X, p.Y).RGBA(); a != 0xffff {
				t.Fatalf("%d: pixel %v not opaque", px, p)
			}
		}
		if !sameColor(m.At(px-1, px-1), white) || !sameColor(m.At(px/2-1, px/2-1), red) {
			t.Fatalf("%d: cell edges are off", px)
		}
	}
	// A cover that isn't square is cropped to its centre, not squashed.
	wide := image.NewNRGBA(image.Rect(0, 0, 90, 30))
	for y := range 30 {
		for x := range 90 {
			c := blue
			if x >= 30 && x < 60 {
				c = red
			}
			wide.Set(x, y, c)
		}
	}
	m := mosaic([]image.Image{wide, wide, wide, wide}, 40)
	if !sameColor(m.At(2, 10), red) {
		t.Fatalf("wide cover not cropped to its centre: %v", m.At(2, 10))
	}
}

func plTrack(pid, album string, hasArt bool) *library.Track {
	return &library.Track{PID: pid, HasArt: hasArt, Album: &library.Album{Key: "k" + album, ArtPID: album}}
}

func TestPlaylistArtSpec(t *testing.T) {
	p := &library.Playlist{PID: "P1", Kind: "user", Tracks: []*library.Track{
		plTrack("t1", "A", true),
		plTrack("t2", "A", true),  // same album
		plTrack("t3", "B", false), // no art
		plTrack("t4", "C", true),
		plTrack("t5", "D", true),
		plTrack("t6", "B", true), // B again, now with art
		plTrack("t7", "E", true), // fifth album: not used
	}}
	got := playlistArtSpec(p)
	want := []plArtSource{{album: "A"}, {album: "C"}, {album: "D"}, {album: "B"}}
	if got.pid != "P1" || !slices.Equal(got.sources, want) {
		t.Fatalf("spec = %+v, want %+v", got, want)
	}

	short := playlistArtSpec(&library.Playlist{PID: "P2", Tracks: p.Tracks[:2]})
	if !slices.Equal(short.sources, []plArtSource{{album: "A"}}) {
		t.Fatalf("one album: %+v", short.sources)
	}
	if empty := playlistArtSpec(&library.Playlist{PID: "P3"}); len(empty.sources) != 0 {
		t.Fatalf("empty playlist: %+v", empty.sources)
	}

	// A folder uses its playlists, looking into folders inside it.
	folder := &library.Playlist{PID: "F", Kind: "folder", Children: []*library.Playlist{
		{PID: "c1", Tracks: []*library.Track{plTrack("x", "X", true)}},
		{PID: "sub", Kind: "folder", Children: []*library.Playlist{
			{PID: "c2"},
			{PID: "c3", Tracks: []*library.Track{plTrack("y", "Y", false), plTrack("z", "Z", true)}},
		}},
		{PID: "c4"},
		{PID: "c5"},
	}}
	fs := playlistArtSpec(folder)
	wantF := []plArtSource{{"c1", "X"}, {"c2", ""}, {"c3", "Z"}, {"c4", ""}}
	if !slices.Equal(fs.sources, wantF) {
		t.Fatalf("folder = %+v, want %+v", fs.sources, wantF)
	}
}

func TestPlaylistArtKey(t *testing.T) {
	mk := func(albums ...string) *library.Playlist {
		p := &library.Playlist{PID: "ABCD"}
		for i, a := range albums {
			p.Tracks = append(p.Tracks, plTrack(string(rune('a'+i)), a, true))
		}
		return p
	}
	k := playlistArtSpec(mk("A", "B", "C", "D")).key()
	if !strings.HasPrefix(k, "plart:ABCD:") || !isPlaylistArt(k) {
		t.Fatalf("key %q", k)
	}
	if k2 := playlistArtSpec(mk("A", "B", "C", "D", "E")).key(); k2 != k {
		t.Fatalf("a fifth album changed the key: %q vs %q", k2, k)
	}
	if k2 := playlistArtSpec(mk("A", "A", "B", "C", "D")).key(); k2 != k {
		t.Fatalf("a repeated album changed the key: %q vs %q", k2, k)
	}
	if k2 := playlistArtSpec(mk("B", "A", "C", "D")).key(); k2 == k {
		t.Fatal("reordered albums kept the key")
	}
	other := mk("A", "B", "C", "D")
	other.PID = "EF01"
	if k2 := playlistArtSpec(other).key(); k2 == k {
		t.Fatal("another playlist with the same albums has the same key")
	}
	if isPlaylistArt("ABCD") || isPlaylistArt("https://x/y.jpg") {
		t.Fatal("isPlaylistArt matched a pid or URL")
	}
}

// plArtClient serves solid-colour covers for playlists and albums.
type plArtClient struct {
	bridge.Client
	dir    string
	covers map[string]color.Color // playlist pid → its own cover
	albums map[string]color.Color // album art pid → cover
	fail   map[string]bool        // album art pids that error

	mu    sync.Mutex
	calls []string
}

func (c *plArtClient) serve(name string, col color.Color, size int) (bridge.Artwork, error) {
	path := filepath.Join(c.dir, name+".png")
	f, err := os.Create(path)
	if err != nil {
		return bridge.Artwork{}, err
	}
	defer f.Close()
	if err := png.Encode(f, solid(col, size)); err != nil {
		return bridge.Artwork{}, err
	}
	return bridge.Artwork{Path: path, W: size, H: size}, nil
}

func (c *plArtClient) record(s string) {
	c.mu.Lock()
	c.calls = append(c.calls, s)
	c.mu.Unlock()
}

func (c *plArtClient) PlaylistArtwork(ctx context.Context, pid string, size int) (bridge.Artwork, error) {
	c.record("pl " + pid)
	if col, ok := c.covers[pid]; ok {
		return c.serve("pl-"+pid, col, size)
	}
	return bridge.Artwork{}, nil
}

func (c *plArtClient) Artwork(ctx context.Context, pid string, size int) (bridge.Artwork, error) {
	c.record("al " + pid)
	if c.fail[pid] {
		return bridge.Artwork{}, errors.New("timed out")
	}
	if col, ok := c.albums[pid]; ok {
		return c.serve(pid, col, size)
	}
	return bridge.Artwork{}, nil
}

func newPlArtStore(t *testing.T) (*artStore, *plArtClient) {
	c := &plArtClient{
		dir:    t.TempDir(),
		covers: map[string]color.Color{},
		albums: map[string]color.Color{"A": red, "B": green, "C": blue, "D": white},
		fail:   map[string]bool{},
	}
	s := newArtStore(c, nil)
	s.urls.dir = t.TempDir()
	return s, c
}

func loadPl(t *testing.T, s *artStore, p *library.Playlist, px int) (image.Image, bool, error) {
	t.Helper()
	spec := playlistArtSpec(p)
	key := spec.key()
	s.setPlaylist(key, spec)
	data, none, err := s.loadPlaylist(key, px)
	if err != nil || none {
		return nil, none, err
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return m, false, nil
}

func plOf(pid string, albums ...string) *library.Playlist {
	p := &library.Playlist{PID: pid}
	for i, a := range albums {
		p.Tracks = append(p.Tracks, plTrack(pid+string(rune('a'+i)), a, true))
	}
	return p
}

func TestPlaylistArtFallbackOrder(t *testing.T) {
	t.Run("own cover first", func(t *testing.T) {
		s, c := newPlArtStore(t)
		c.covers["P"] = purple
		m, _, err := loadPl(t, s, plOf("P", "A", "B", "C", "D"), 64)
		if err != nil {
			t.Fatal(err)
		}
		quadrants(t, m, purple, purple, purple, purple)
		if !slices.Equal(c.calls, []string{"pl P"}) {
			t.Fatalf("calls %v: the albums shouldn't be loaded", c.calls)
		}
	})

	t.Run("mosaic of four albums, kept on disk", func(t *testing.T) {
		s, c := newPlArtStore(t)
		p := plOf("P", "A", "A", "B", "C", "D")
		m, _, err := loadPl(t, s, p, 64)
		if err != nil {
			t.Fatal(err)
		}
		quadrants(t, m, red, green, blue, white)
		files, _ := filepath.Glob(filepath.Join(s.urls.dir, "plmosaic-P-*-64.jpg"))
		if len(files) != 1 {
			t.Fatalf("mosaic files %v", files)
		}
		c.calls = nil
		if _, _, err := loadPl(t, s, p, 64); err != nil || slices.ContainsFunc(c.calls, func(s string) bool { return strings.HasPrefix(s, "al ") }) {
			t.Fatalf("second load: %v, calls %v", err, c.calls)
		}
		// New first albums: a new mosaic replaces the old file.
		if _, _, err := loadPl(t, s, plOf("P", "D", "C", "B", "A"), 64); err != nil {
			t.Fatal(err)
		}
		files2, _ := filepath.Glob(filepath.Join(s.urls.dir, "plmosaic-P-*-64.jpg"))
		if len(files2) != 1 || files2[0] == files[0] {
			t.Fatalf("mosaic files after a change %v (before %v)", files2, files)
		}
	})

	t.Run("fewer than four albums: the first cover", func(t *testing.T) {
		s, _ := newPlArtStore(t)
		m, _, err := loadPl(t, s, plOf("P", "B", "C"), 64)
		if err != nil {
			t.Fatal(err)
		}
		quadrants(t, m, green, green, green, green)
	})

	t.Run("an album without art leaves three: the first cover", func(t *testing.T) {
		s, c := newPlArtStore(t)
		delete(c.albums, "C")
		m, _, err := loadPl(t, s, plOf("P", "A", "B", "C", "D"), 64)
		if err != nil {
			t.Fatal(err)
		}
		quadrants(t, m, red, red, red, red)
	})

	t.Run("nothing to show", func(t *testing.T) {
		s, _ := newPlArtStore(t)
		if _, none, err := loadPl(t, s, plOf("P", "Q"), 64); err != nil || !none {
			t.Fatalf("none = %v, err %v", none, err)
		}
		if _, none, err := loadPl(t, s, &library.Playlist{PID: "E"}, 64); err != nil || !none {
			t.Fatalf("empty playlist: none = %v, err %v", none, err)
		}
	})

	t.Run("an error is retried, not taken for no art", func(t *testing.T) {
		s, c := newPlArtStore(t)
		c.fail["A"] = true
		if _, none, err := loadPl(t, s, plOf("P", "A", "B", "C", "D"), 64); err == nil || none {
			t.Fatalf("none = %v, err %v", none, err)
		}
		if _, none, err := loadPl(t, s, plOf("P", "A"), 64); err == nil || none {
			t.Fatalf("single: none = %v, err %v", none, err)
		}
	})

	t.Run("folder: its playlists' covers, else their albums", func(t *testing.T) {
		s, c := newPlArtStore(t)
		c.covers["c2"] = purple
		folder := &library.Playlist{PID: "F", Kind: "folder", Children: []*library.Playlist{
			plOf("c1", "A"), plOf("c2", "B"), plOf("c3", "C"), plOf("c4", "D"),
		}}
		m, _, err := loadPl(t, s, folder, 64)
		if err != nil {
			t.Fatal(err)
		}
		quadrants(t, m, red, purple, blue, white)
	})

	t.Run("unknown key is an error", func(t *testing.T) {
		s, _ := newPlArtStore(t)
		if _, _, err := s.loadPlaylist("plart:NOPE:0", 64); err == nil {
			t.Fatal("want an error")
		}
	})
}

// On the demo library, where no playlist has a cover of its own, a
// playlist's page shows the mosaic of its albums.
func TestPlaylistHeaderShowsPlaylistArt(t *testing.T) {
	a := newArtApp(t, art.ProtoIIP, 140, 42)
	var p *library.Playlist
	for _, q := range a.lib.PlaylistsFlat {
		if q.Kind != "folder" && len(playlistArtSpec(q).sources) == mosaicCovers {
			p = q
			break
		}
	}
	if p == nil {
		t.Fatal("no demo playlist with four albums")
	}
	a.OpenPlaylist(p)
	key := playlistArtSpec(p).key()
	waitFor(t, func() bool {
		a.View()
		return slices.ContainsFunc(a.frameArt, func(q artPlace) bool { return q.key == key })
	})
}

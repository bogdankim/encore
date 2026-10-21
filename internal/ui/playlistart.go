package ui

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"hash/fnv"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

// A playlist's artwork is what Apple Music shows for it: the cover Music
// has for the playlist (Apple's for its own playlists, one the user picked,
// or one Music drew for it), else a 2×2 mosaic of the first four distinct
// album covers in it, else the cover of its first album when it has fewer
// than four. A folder's mosaic is made of its playlists' covers.
//
// The art pid of a playlist is its key, "plart:<pid>:<hash>": the hash is
// of the covers a mosaic would use, so the key changes, and the art is drawn
// again, when the playlist's first albums do. The art store looks the
// recipe up by key (see Ctx.PlaylistArt).

const (
	playlistArtPrefix = "plart:"
	mosaicCovers      = 4
	playlistSpecsMax  = 256
)

// plArtSource is one cover a mosaic may use: an album's, or for a folder, a
// playlist's own cover with its first album's as the fallback.
type plArtSource struct {
	playlist string // pid of a folder's playlist; "" for an album
	album    string // art pid of the album
}

// plArtSpec is how to draw a playlist's art when Music has no cover for it.
type plArtSpec struct {
	pid     string
	sources []plArtSource // at most mosaicCovers
}

// playlistArtSpec collects p's first distinct album covers, or for a folder
// its first playlists, looking into folders inside it.
func playlistArtSpec(p *library.Playlist) plArtSpec {
	s := plArtSpec{pid: p.PID}
	if p.Kind != "folder" {
		s.sources = firstAlbumArt(p.Tracks, mosaicCovers)
		return s
	}
	var walk func(*library.Playlist)
	walk = func(f *library.Playlist) {
		for _, ch := range f.Children {
			if len(s.sources) == mosaicCovers {
				return
			}
			if ch.Kind == "folder" {
				walk(ch)
				continue
			}
			src := plArtSource{playlist: ch.PID}
			if a := firstAlbumArt(ch.Tracks, 1); len(a) > 0 {
				src.album = a[0].album
			}
			s.sources = append(s.sources, src)
		}
	}
	walk(p)
	return s
}

// firstAlbumArt is the art of the first n distinct albums with artwork
// among tracks, in order.
func firstAlbumArt(tracks []*library.Track, n int) []plArtSource {
	var out []plArtSource
	seen := map[string]bool{}
	for _, t := range tracks {
		if len(out) == n {
			break
		}
		if !t.HasArt || t.Album == nil || t.Album.ArtPID == "" || seen[t.Album.Key] {
			continue
		}
		seen[t.Album.Key] = true
		out = append(out, plArtSource{album: t.Album.ArtPID})
	}
	return out
}

// key is the art key (and pid) of the playlist's art.
func (s plArtSpec) key() string {
	h := fnv.New64a()
	for _, src := range s.sources {
		h.Write([]byte(src.playlist + "/" + src.album + "\x00"))
	}
	return playlistArtPrefix + s.pid + ":" + strconv.FormatUint(h.Sum64(), 16)
}

var errUnknownPlaylistArt = errors.New("playlist art: unknown key")

// isPlaylistArt reports whether an art pid is a playlist's key.
func isPlaylistArt(pid string) bool { return strings.HasPrefix(pid, playlistArtPrefix) }

// PlaylistArt is the key and pid to draw playlist p's artwork with, through
// HeroArt or TileArt.
func (c *Ctx) PlaylistArt(p *library.Playlist) (key, pid string) {
	spec := playlistArtSpec(p)
	key = spec.key()
	if a, ok := c.Host.(*App); ok {
		a.art.setPlaylist(key, spec)
	}
	return key, key
}

// setPlaylist remembers how to draw the art of key.
func (s *artStore) setPlaylist(key string, spec plArtSpec) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.playlists[key]; !ok && len(s.playlists) >= playlistSpecsMax {
		clear(s.playlists)
	}
	s.playlists[key] = spec
}

// loadPlaylist returns the art of playlist key at px×px (see the top of
// this file).
func (s *artStore) loadPlaylist(key string, px int) (data []byte, none bool, err error) {
	s.mu.Lock()
	spec, ok := s.playlists[key]
	s.mu.Unlock()
	if !ok {
		return nil, false, errUnknownPlaylistArt // asked for again with its recipe
	}
	if data, ok := s.playlistCover(spec.pid, px); ok {
		return data, false, nil
	}
	path := s.mosaicPath(key, px)
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data, false, nil
		}
	}
	if len(spec.sources) >= mosaicCovers {
		imgs, err := s.coverImages(spec.sources, (px+1)/2)
		if len(imgs) == mosaicCovers {
			data, err := encodePNG(mosaic(imgs, px))
			if err != nil {
				return nil, false, err
			}
			if path != "" {
				writeMosaic(path, data)
			}
			return data, false, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	// Fewer than four covers: the first one alone, as Apple Music does.
	var firstErr error
	for _, src := range spec.sources {
		data, none, err := s.sourceArt(src, px)
		switch {
		case err != nil:
			firstErr = cmp.Or(firstErr, err)
		case !none:
			return data, false, nil
		}
	}
	return nil, firstErr == nil, firstErr
}

// coverImages loads and decodes the sources' covers at px, stopping at
// mosaicCovers. err is the first error met on the way.
func (s *artStore) coverImages(sources []plArtSource, px int) (imgs []image.Image, err error) {
	for _, src := range sources {
		data, none, e := s.sourceArt(src, px)
		if e == nil && !none {
			var img image.Image
			if img, _, e = image.Decode(bytes.NewReader(data)); e == nil {
				imgs = append(imgs, img)
			}
		}
		if e != nil && err == nil {
			err = e
		}
		if len(imgs) == mosaicCovers {
			break
		}
	}
	return imgs, err
}

// sourceArt is one mosaic cover at px.
func (s *artStore) sourceArt(src plArtSource, px int) (data []byte, none bool, err error) {
	if src.playlist != "" {
		if data, ok := s.playlistCover(src.playlist, px); ok {
			return data, false, nil
		}
	}
	if src.album == "" {
		return nil, true, nil
	}
	return s.loadBridge(src.album, px)
}

// playlistCover is the cover Music has for playlist pid at px. Without one,
// or when Music can't be asked, the mosaic stands in.
func (s *artStore) playlistCover(pid string, px int) ([]byte, bool) {
	if s.client == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), bridge.ArtworkTimeout)
	a, err := s.client.PlaylistArtwork(ctx, pid, px)
	cancel()
	if err != nil || a.Path == "" {
		return nil, false
	}
	data, err := os.ReadFile(a.Path)
	return data, err == nil && len(data) > 0
}

// mosaicPath is where the mosaic of key at px is kept on disk,
// plmosaic-<pid>-<hash>-<px>.png in the art cache, or "" with no disk cache.
func (s *artStore) mosaicPath(key string, px int) string {
	if s.urls.dir == "" {
		return ""
	}
	name := strings.ReplaceAll(strings.TrimPrefix(key, playlistArtPrefix), ":", "-")
	return filepath.Join(s.urls.dir, "plmosaic-"+name+"-"+strconv.Itoa(px)+".png")
}

// writeMosaic saves a mosaic and drops the playlist's older ones at the same
// size, made from albums it no longer starts with.
func writeMosaic(path string, data []byte) {
	base := filepath.Base(path)
	// plmosaic-<pid>-<hash>-<px>.png
	parts := strings.Split(strings.TrimSuffix(base, ".png"), "-")
	if len(parts) == 4 {
		old, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "plmosaic-"+parts[1]+"-*-"+parts[3]+".png"))
		for _, f := range old {
			if f != path {
				os.Remove(f)
			}
		}
	}
	writeFileAtomic(path, data)
}

// mosaic draws four covers as a px×px square, two by two, left to right and
// top to bottom, without gaps.
func mosaic(imgs []image.Image, px int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, px, px))
	h := px / 2
	cells := []image.Rectangle{
		image.Rect(0, 0, h, h), image.Rect(h, 0, px, h),
		image.Rect(0, h, h, px), image.Rect(h, h, px, px),
	}
	for i, r := range cells[:min(len(imgs), len(cells))] {
		draw.Draw(out, r, art.Cover(imgs[i], r.Dx(), r.Dy()), image.Point{}, draw.Src)
	}
	return out
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	return buf.Bytes(), err
}

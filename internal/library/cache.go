package library

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"github.com/bogdankim/encore/internal/bridge"
)

// The cache is a flat binary encoding of a bridge.Snapshot:
//
//	magic "ENCORELB" | version u32 LE | payload | crc32 (IEEE) of payload, u32 LE
//
// The payload is a sequence of uvarints, zigzag varints and length-prefixed
// strings. Playlist items are stored as track indexes where possible. Decoding
// copies the file into one string and slices every field out of it, so a load
// costs a handful of allocations per track instead of one per field.
//
// Version 2 adds a track's store id and a playlist's global id. Version 1
// files still load, without them, until the next snapshot replaces the cache.
const (
	cacheMagic   = "ENCORELB"
	cacheVersion = 2
)

// ErrCacheVersion means the cache was written by a different format version.
var ErrCacheVersion = errors.New("library cache: unsupported version")

// SaveCache writes s to path atomically: a temporary file in the same directory
// is synced and renamed over path.
func SaveCache(path string, s *bridge.Snapshot) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()

	w := bufio.NewWriterSize(f, 1<<16)
	var hdr [12]byte
	copy(hdr[:], cacheMagic)
	binary.LittleEndian.PutUint32(hdr[8:], cacheVersion)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	crc := crc32.NewIEEE()
	e := encoder{w: io.MultiWriter(w, crc), ver: cacheVersion}
	e.snapshot(s)
	if e.err != nil {
		return e.err
	}
	var sum [4]byte
	binary.LittleEndian.PutUint32(sum[:], crc.Sum32())
	if _, err := w.Write(sum[:]); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// LoadCache reads a snapshot written by SaveCache.
func LoadCache(path string) (*bridge.Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 16 || string(data[:8]) != cacheMagic {
		return nil, errors.New("library cache: not a cache file")
	}
	ver := binary.LittleEndian.Uint32(data[8:12])
	if ver < 1 || ver > cacheVersion {
		return nil, fmt.Errorf("%w %d", ErrCacheVersion, ver)
	}
	payload := data[12 : len(data)-4]
	if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(data[len(data)-4:]) {
		return nil, errors.New("library cache: checksum mismatch")
	}
	d := decoder{s: string(payload), ver: ver}
	s := d.snapshot()
	if d.err != nil {
		return nil, d.err
	}
	return s, nil
}

const (
	flagFavorited = 1 << iota
	flagDisliked
	flagExplicit
	flagCompilation
	flagInLibrary
	flagAvailable
	flagArt
)

type encoder struct {
	w   io.Writer
	ver uint32 // the format to write; tests write version 1
	buf []byte
	err error
}

func (e *encoder) flush() {
	if e.err == nil && len(e.buf) > 0 {
		_, e.err = e.w.Write(e.buf)
	}
	e.buf = e.buf[:0]
}

func (e *encoder) uint(v uint64) { e.buf = binary.AppendUvarint(e.buf, v) }
func (e *encoder) int(v int64)   { e.buf = binary.AppendVarint(e.buf, v) }
func (e *encoder) str(s string) {
	e.uint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *encoder) snapshot(s *bridge.Snapshot) {
	e.int(s.GeneratedAt)
	e.uint(uint64(len(s.Tracks)))
	index := make(map[string]int, len(s.Tracks))
	for i := range s.Tracks {
		t := &s.Tracks[i]
		index[t.PID] = i
		e.str(t.PID)
		e.str(t.Title)
		e.str(t.Artist)
		e.str(t.AlbumArtist)
		e.str(t.Album)
		e.str(t.AlbumPID)
		e.str(t.Genre)
		e.str(t.Composer)
		e.str(t.Kind)
		e.str(t.SortTitle)
		e.str(t.SortArtist)
		e.str(t.SortAlbum)
		e.str(t.SortAlbumArtist)
		e.int(int64(t.Year))
		e.int(int64(t.Track))
		e.int(int64(t.TrackCount))
		e.int(int64(t.Disc))
		e.int(int64(t.DiscCount))
		e.int(t.Duration)
		e.int(t.Added)
		e.int(t.LastPlayed)
		e.int(int64(t.Plays))
		e.int(int64(t.Rating))
		var flags uint64
		for _, f := range [...]struct {
			on  bool
			bit uint64
		}{
			{t.Favorited, flagFavorited}, {t.Disliked, flagDisliked},
			{t.Explicit, flagExplicit}, {t.Compilation, flagCompilation},
			{t.InLibrary, flagInLibrary}, {t.Available, flagAvailable},
			{t.Art, flagArt},
		} {
			if f.on {
				flags |= f.bit
			}
		}
		e.uint(flags)
		if e.ver >= 2 {
			e.uint(uint64(max(t.StoreID, 0)))
		}
		if len(e.buf) > 1<<15 {
			e.flush()
		}
	}
	e.uint(uint64(len(s.Playlists)))
	for i := range s.Playlists {
		p := &s.Playlists[i]
		e.str(p.PID)
		e.str(p.ParentPID)
		e.str(p.Name)
		e.str(p.Kind)
		e.str(p.Special)
		if e.ver >= 2 {
			e.str(p.GlobalID)
		}
		e.uint(uint64(len(p.Items)))
		// Items are track index + 1, or 0 followed by the pid for an item
		// that is not among the tracks.
		for _, pid := range p.Items {
			if j, ok := index[pid]; ok {
				e.uint(uint64(j) + 1)
			} else {
				e.uint(0)
				e.str(pid)
			}
		}
		if len(e.buf) > 1<<15 {
			e.flush()
		}
	}
	e.flush()
}

var errCorrupt = errors.New("library cache: corrupt payload")

type decoder struct {
	s   string
	ver uint32
	off int
	err error
}

func (d *decoder) uint() uint64 {
	if d.err != nil {
		return 0
	}
	var v uint64
	var shift uint
	for i := 0; i < binary.MaxVarintLen64; i++ {
		if d.off >= len(d.s) {
			break
		}
		c := d.s[d.off]
		d.off++
		if c < 0x80 {
			return v | uint64(c)<<shift
		}
		v |= uint64(c&0x7f) << shift
		shift += 7
	}
	d.err = errCorrupt
	return 0
}

func (d *decoder) int() int64 {
	u := d.uint()
	return int64(u>>1) ^ -int64(u&1)
}

func (d *decoder) str() string {
	n := d.uint()
	if d.err != nil {
		return ""
	}
	if n > uint64(len(d.s)-d.off) {
		d.err = errCorrupt
		return ""
	}
	s := d.s[d.off : d.off+int(n)]
	d.off += int(n)
	return s
}

// count reads a length and checks it against the bytes left, each element
// taking at least min bytes.
func (d *decoder) count(min int) int {
	n := d.uint()
	if n > uint64((len(d.s)-d.off)/min) {
		d.err = errCorrupt
		return 0
	}
	return int(n)
}

func (d *decoder) snapshot() *bridge.Snapshot {
	s := &bridge.Snapshot{GeneratedAt: d.int()}
	s.Tracks = make([]bridge.SnapshotTrack, d.count(24))
	for i := range s.Tracks {
		t := &s.Tracks[i]
		t.PID = d.str()
		t.Title = d.str()
		t.Artist = d.str()
		t.AlbumArtist = d.str()
		t.Album = d.str()
		t.AlbumPID = d.str()
		t.Genre = d.str()
		t.Composer = d.str()
		t.Kind = d.str()
		t.SortTitle = d.str()
		t.SortArtist = d.str()
		t.SortAlbum = d.str()
		t.SortAlbumArtist = d.str()
		t.Year = int(d.int())
		t.Track = int(d.int())
		t.TrackCount = int(d.int())
		t.Disc = int(d.int())
		t.DiscCount = int(d.int())
		t.Duration = d.int()
		t.Added = d.int()
		t.LastPlayed = d.int()
		t.Plays = int(d.int())
		t.Rating = int(d.int())
		flags := d.uint()
		t.Favorited = flags&flagFavorited != 0
		t.Disliked = flags&flagDisliked != 0
		t.Explicit = flags&flagExplicit != 0
		t.Compilation = flags&flagCompilation != 0
		t.InLibrary = flags&flagInLibrary != 0
		t.Available = flags&flagAvailable != 0
		t.Art = flags&flagArt != 0
		if d.ver >= 2 {
			t.StoreID = int64(d.uint())
		}
		if d.err != nil {
			return nil
		}
	}
	s.Playlists = make([]bridge.SnapshotPlaylist, d.count(6))
	for i := range s.Playlists {
		p := &s.Playlists[i]
		p.PID = d.str()
		p.ParentPID = d.str()
		p.Name = d.str()
		p.Kind = d.str()
		p.Special = d.str()
		if d.ver >= 2 {
			p.GlobalID = d.str()
		}
		p.Items = make([]string, d.count(1))
		for j := range p.Items {
			k := d.uint()
			switch {
			case k == 0:
				p.Items[j] = d.str()
			case k <= uint64(len(s.Tracks)):
				p.Items[j] = s.Tracks[k-1].PID
			default:
				d.err = errCorrupt
			}
		}
		if d.err != nil {
			return nil
		}
	}
	if d.off != len(d.s) {
		d.err = errCorrupt
	}
	return s
}

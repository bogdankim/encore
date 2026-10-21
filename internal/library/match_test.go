package library

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogdankim/encore/internal/bridge"
)

// matchLib is a small library for the matching rules: songs with and without
// catalog ids, a remaster, a live version, an album the library lists only
// through a playlist, and two followed playlists.
func matchLib() *Library {
	tr := func(pid, title, artist, album string, store int64, inLib bool) bridge.SnapshotTrack {
		return bridge.SnapshotTrack{
			PID: pid, Title: title, Artist: artist, AlbumArtist: artist, Album: album, AlbumPID: "ALB-" + album,
			InLibrary: inLib, Available: true, StoreID: store, Track: 1, TrackCount: 1,
		}
	}
	return Build(&bridge.Snapshot{
		Tracks: []bridge.SnapshotTrack{
			tr("T1", "Daylight Drive (feat. Neoplasma)", "Redmatic", "Daylight Drive - Single", 1561682197, true),
			tr("T2", "Serendipity", "Maurice Brown", "The Mood", 1649836636, true),
			tr("T3", "Yesterday - Remastered 2009", "The Beatles", "Help! (Remastered)", 0, true), // a local file
			tr("T4", "Yesterday (Live)", "The Beatles", "Live at the BBC", 0, true),
			tr("T5", "Car", "Built to Spill", "There's Nothing Wrong with Love", 41757289, true),
			tr("T6", "Hidden Track", "Somebody", "Playlist Only Album", 99, false),
			tr("T7", "Creep", "Radiohead", "Pablo Honey", 0, true),
		},
		Playlists: []bridge.SnapshotPlaylist{
			{PID: "P1", Name: "Ambient Chill", Kind: "subscription", GlobalID: "pl.bed492442a53481f98e98c6c4da9e01d", Items: []string{"T2"}},
			{PID: "P2", Name: "Mine", Kind: "user", Items: []string{"T6"}},
			{PID: "P3", Name: "Folder", Kind: "folder", GlobalID: "pl.folder"},
		},
	})
}

func TestMatchSongByID(t *testing.T) {
	l := matchLib()
	if got := l.MatchSong(1649836636, "Anything", "Anyone"); got == nil || got.PID != "T2" {
		t.Errorf("by id: %v", got)
	}
	if l.SongByStoreID(0) != nil || l.SongByStoreID(5) != nil {
		t.Error("unknown ids matched")
	}
	// a song only in a playlist is not in the library, id or not
	if l.SongByStoreID(99) != nil || l.MatchSong(99, "Hidden Track", "Somebody") != nil {
		t.Error("playlist-only song matched")
	}
}

func TestMatchSongFallback(t *testing.T) {
	l := matchLib()
	for _, tc := range []struct {
		name          string
		id            int64
		title, artist string
		want          string // pid, "" for none
	}{
		// the library song has no id: title and artist decide
		{"no id on either side", 0, "Creep", "Radiohead", "T7"},
		{"catalog id, library song without one", 31, "Creep", "Radiohead", "T7"},
		{"folded", 0, "CREEP", "radiohead", "T7"},
		{"featured artist in the credit", 0, "Creep", "Radiohead feat. Someone", "T7"},
		{"remaster note on either side", 8, "Yesterday", "The Beatles", "T3"},
		{"remaster note, other spelling", 8, "Yesterday (Remastered)", "Beatles", "T3"},
		{"live is another recording", 8, "Yesterday (Live)", "The Beatles", "T4"},
		{"studio vs live", 0, "Yesterday", "Radiohead", ""},
		{"other artist", 0, "Creep", "Stone Temple Pilots", ""},
		// the library song has an id of its own: another id is another release
		{"same song, other release", 12345, "Serendipity", "Maurice Brown", ""},
		{"same song, other release (feat. note)", 12345, "Daylight Drive", "Redmatic", ""},
		// without an id from the catalog (Home gave none) the name counts
		{"id unknown, name equal", 0, "Serendipity", "Maurice Brown", "T2"},
		{"feat. note dropped", 0, "Daylight Drive", "Redmatic", "T1"},
	} {
		got := l.MatchSong(tc.id, tc.title, tc.artist)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: matched %s", tc.name, got.PID)
		case tc.want != "" && (got == nil || got.PID != tc.want):
			t.Errorf("%s: got %v, want %s", tc.name, got, tc.want)
		}
	}
}

func TestMatchAlbum(t *testing.T) {
	l := matchLib()
	for _, tc := range []struct {
		title, artist string
		want          bool
	}{
		{"Daylight Drive - Single", "Redmatic", true},
		{"Daylight Drive", "Redmatic", true}, // the single's suffix doesn't count
		{"The Mood (Deluxe Edition)", "Maurice Brown", true},
		{"The Mood [Expanded Edition]", "Maurice Brown", true},
		{"Help!", "The Beatles", true}, // "(Remastered)" is an edition note
		{"Help!", "Beatles", true},     // "The" is optional
		{"The Mood", "Somebody Else", false},
		{"The Mood (Live)", "Maurice Brown", false}, // not an edition note
		{"Live at the BBC (Remastered)", "The Beatles", true},
		{"Playlist Only Album", "Somebody", false}, // listed only through a playlist
		{"", "Maurice Brown", false},
	} {
		if got := l.FindAlbum(tc.title, tc.artist) != nil; got != tc.want {
			t.Errorf("FindAlbum(%q, %q) = %v, want %v", tc.title, tc.artist, got, tc.want)
		}
	}
	var nilLib *Library
	if nilLib.FindAlbum("x", "y") != nil || nilLib.MatchSong(1, "x", "y") != nil || nilLib.PlaylistByGlobalID("pl.x") != nil {
		t.Error("nil library matched")
	}
}

func TestMatchPlaylist(t *testing.T) {
	l := matchLib()
	if p := l.PlaylistByGlobalID("pl.bed492442a53481f98e98c6c4da9e01d"); p == nil || p.Name != "Ambient Chill" {
		t.Errorf("by cloud id: %v", p)
	}
	for _, id := range []string{"", "pl.unknown", "pl.folder", "Ambient Chill"} {
		if l.PlaylistByGlobalID(id) != nil {
			t.Errorf("playlist %q matched", id)
		}
	}
}

func TestCacheVersion1(t *testing.T) {
	s := matchLib2Snapshot()
	path := filepath.Join(t.TempDir(), "library.cache")
	// a version 1 file, as an older encore wrote it: no ids
	var payload []byte
	{
		e := encoder{ver: 1}
		var buf collect
		e.w = &buf
		e.snapshot(s)
		payload = buf.b
	}
	hdr := make([]byte, 12)
	copy(hdr, cacheMagic)
	binary.LittleEndian.PutUint32(hdr[8:], 1)
	sum := binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(payload))
	if err := os.WriteFile(path, append(append(hdr, payload...), sum...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracks) != len(s.Tracks) || got.Tracks[0].Title != s.Tracks[0].Title || got.Tracks[0].StoreID != 0 || got.Playlists[0].GlobalID != "" {
		t.Errorf("version 1 load: %+v", got.Tracks[0])
	}
	// and it still matches by name
	if Build(got).MatchSong(1, s.Tracks[0].Title, s.Tracks[0].Artist) == nil {
		t.Error("no name match from a version 1 cache")
	}
}

type collect struct{ b []byte }

func (c *collect) Write(p []byte) (int, error) { c.b = append(c.b, p...); return len(p), nil }

func matchLib2Snapshot() *bridge.Snapshot {
	return &bridge.Snapshot{
		Tracks: []bridge.SnapshotTrack{
			{PID: "A1", Title: "Serendipity", Artist: "Maurice Brown", Album: "The Mood", InLibrary: true, Available: true, StoreID: 5},
		},
		Playlists: []bridge.SnapshotPlaylist{{PID: "P1", Name: "X", Kind: "user", GlobalID: "pl.x", Items: []string{"A1"}}},
	}
}

func BenchmarkMatchSong(b *testing.B) {
	l := Build(snapN(50000))
	t := l.Songs[len(l.Songs)/2]
	b.ReportAllocs()
	for b.Loop() {
		l.MatchSong(0, t.Title, t.Artist)
	}
}

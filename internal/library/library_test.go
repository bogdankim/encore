package library

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library/libtest"
)

func TestFold(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"abc":                 "abc",
		"The Beatles":         "the beatles",
		"  Get   Lucky\t":     "get lucky",
		"Björk":               "bjork",
		"Sigur Rós":           "sigur ros",
		"Ágætis byrjun":       "agaetis byrjun",
		"Don’t Stop":          "don't stop",
		"Rock — Roll – Again": "rock - roll - again",
		"Straße":              "strasse",
		"ﬁnal":                "final",
		"Ｆｕｌｌ":                "full",
		"Mötley Crüe":         "motley crue",
		"Beyoncé":            "beyonce",
		"zero​width":          "zerowidth",
		"ガガガ":                 "ガガガ",
		"Ελληνικά":            "ελληνικα",
		"Москва":              "москва",
		"tab\tand\nnewline":   "tab and newline",
		" leading nbsp ":      "leading nbsp",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
		got, m := FoldMap(in)
		if got != want {
			t.Errorf("FoldMap(%q) = %q, want %q", in, got, want)
		}
		if len(m) != len(got) {
			t.Errorf("FoldMap(%q) map has %d entries for %d bytes", in, len(m), len(got))
		}
	}
}

func TestFoldMap(t *testing.T) {
	s := "  Björk  Guðmundsdóttir"
	folded, m := FoldMap(s)
	if folded != "bjork gudmundsdottir" {
		t.Fatalf("folded = %q", folded)
	}
	runes := []rune(s)
	// "dott" starts at folded byte 14; it must map back to the "d" in "dóttir".
	i := strings.Index(folded, "dott")
	if runes[m[i]] != 'd' || runes[m[i+1]] != 'ó' {
		t.Errorf("map[%d] = %d (%q), map[%d] = %d (%q)", i, m[i], runes[m[i]], i+1, m[i+1], runes[m[i+1]])
	}
	f2, m2 := FoldMap("Straße")
	if f2 != "strasse" || m2[4] != 4 || m2[5] != 4 || m2[6] != 5 {
		t.Errorf("FoldMap(Straße) = %q %v", f2, m2)
	}
}

func TestCompareKeys(t *testing.T) {
	want := []string{
		"abba",
		"beatles",
		"bjork",
		"track 1",
		"track 2",
		"track 02",
		"track 3",
		"track 10",
		"track 11",
		"track 20",
		"zz top",
		"ελληνικα",
		"!!!",
		"2pac",
		"",
	}
	got := slices.Clone(want)
	slices.Reverse(got)
	slices.SortFunc(got, CompareKeys)
	if !slices.Equal(got, want) {
		t.Errorf("sorted:\n%q\nwant:\n%q", got, want)
	}
}

func TestSortKey(t *testing.T) {
	for in, want := range map[string]string{"the beatles": "beatles", "the": "the", "theory": "theory", "a tribe": "a tribe"} {
		if got := sortKey(in); got != want {
			t.Errorf("sortKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func fixture(t testing.TB) *Library {
	t.Helper()
	return Build(libtest.Fixture())
}

func names[T any](xs []T, name func(T) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = name(x)
	}
	return out
}

func TestBuildArtists(t *testing.T) {
	l := fixture(t)
	got := names(l.Artists, func(a *Artist) string { return a.Name })
	want := []string{"Beatles", "Beyoncé", "Björk", "Daft Punk", "Numbers", "Sigur Rós", "Various Artists", "!!!", "2Pac"}
	// The Beatles carry a "Beatles" sort name; displayed with "The".
	want[0] = "The Beatles"
	if !slices.Equal(got, want) {
		t.Fatalf("artists:\n%q\nwant\n%q", got, want)
	}

	b := l.Artist("Beyoncé")
	if b == nil || len(b.Albums) != 3 || b.Tracks != 4 {
		t.Fatalf("Beyoncé = %+v", b)
	}
	if got := names(b.Albums, func(a *Album) string { return a.Title }); !slices.Equal(got, []string{"Renaissance", "Lemonade", "4"}) {
		t.Errorf("Beyoncé albums newest first = %q", got)
	}
	if l.Artist("BEYONCÉ") != nil {
		t.Error("casing variant should merge into Beyoncé")
	}
	if l.Artist("Radiohead") == nil {
		t.Error("playlist-only artist should still be reachable by name")
	}
	dp := l.Artist("Daft Punk")
	if dp.Tracks != 6 {
		t.Errorf("Daft Punk tracks = %d, want 6 (compilation tracks belong to Various Artists)", dp.Tracks)
	}
}

func TestBuildAlbums(t *testing.T) {
	l := fixture(t)
	got := names(l.Albums, func(a *Album) string { return a.Title })
	want := []string{"Abbey Road", "Ágætis byrjun", "All Eyez on Me", "Counting", "Discovery", "Help!",
		"Homogenic", "Lemonade", "Myth Takes", "Random Access Memories", "Renaissance", "Summer Hits", "4"}
	if !slices.Equal(got, want) {
		t.Fatalf("albums:\n%q\nwant\n%q", got, want)
	}

	var comp, counting, sigur *Album
	for _, a := range l.Albums {
		switch a.Title {
		case "Summer Hits":
			comp = a
		case "Counting":
			counting = a
		case "Ágætis byrjun":
			sigur = a
		}
	}
	if !comp.Compilation || comp.Artist != VariousArtists || comp.Tracks[0].Group.Name != VariousArtists {
		t.Errorf("compilation = %+v", comp)
	}
	if got := names(counting.Tracks, func(t *Track) string { return t.Title }); !slices.Equal(got,
		[]string{"Track 1", "Track 2", "Track 10", "Track 3", "Track 20", "Track 11"}) {
		t.Errorf("album order should follow track numbers, got %q", got)
	}
	if sigur.PID != "" || sigur.Key != "sigur ros|agaetis byrjun" || sigur.ArtPID != sigur.Tracks[0].PID {
		t.Errorf("pid-less album: key %q pid %q art %q", sigur.Key, sigur.PID, sigur.ArtPID)
	}
	if l.Album(sigur.Key) != sigur {
		t.Error("Album(key) lookup failed")
	}
	for _, a := range l.Albums {
		var d time.Duration
		for _, t := range a.Tracks {
			d += t.Duration
		}
		if d != a.Duration || a.ArtPID == "" || a.Year == 0 {
			t.Errorf("%s: duration %v vs %v, art %q, year %d", a.Title, a.Duration, d, a.ArtPID, a.Year)
		}
	}
	for i := 1; i < len(l.RecentlyAdded); i++ {
		if l.RecentlyAdded[i].Added.After(l.RecentlyAdded[i-1].Added) {
			t.Fatal("RecentlyAdded not newest first")
		}
	}
	if len(l.RecentlyAdded) != len(l.Albums) {
		t.Error("RecentlyAdded should list every library album")
	}
}

func TestBuildTracks(t *testing.T) {
	l := fixture(t)
	for _, tr := range l.All {
		if tr.Album == nil || tr.Group == nil {
			t.Fatalf("%s: album %v group %v", tr.Title, tr.Album, tr.Group)
		}
		if l.Track(tr.PID) != tr {
			t.Fatalf("Track(%s) lookup failed", tr.PID)
		}
	}
	for i := 1; i < len(l.Songs); i++ {
		if CompareKeys(l.Songs[i-1].SortTitle, l.Songs[i].SortTitle) > 0 {
			t.Fatalf("Songs out of order at %d: %q > %q", i, l.Songs[i-1].SortTitle, l.Songs[i].SortTitle)
		}
	}
	for _, tr := range l.Songs {
		if !tr.InLibrary {
			t.Fatalf("playlist-only %q in Songs", tr.Title)
		}
	}
	if got := names(l.Favorites, func(t *Track) string { return t.Title }); !slices.Equal(got, []string{"Get Lucky"}) {
		t.Errorf("favorites = %q", got)
	}
	// "Here Comes the Sun" keeps its title; "The" only goes from the front.
	var help *Track
	for _, tr := range l.Songs {
		if tr.Title == "Help!" {
			help = tr
		}
	}
	if help.SortArtist != "beatles" || help.FoldArtist != "the beatles" || help.Group.Name != "The Beatles" {
		t.Errorf("Help!: sort %q fold %q group %q", help.SortArtist, help.FoldArtist, help.Group.Name)
	}
}

func TestPlaylistOnlyTracks(t *testing.T) {
	l := fixture(t)
	var idio *Track
	for _, tr := range l.All {
		if tr.Title == "Idioteque" {
			idio = tr
		}
	}
	chill := playlistNamed(l, "Chill")
	if idio.InLibrary || idio.Container != chill.PID {
		t.Errorf("Idioteque container = %q, want Chill %q", idio.Container, chill.PID)
	}
	if idio.Album.Title != "Kid A" || l.Album(idio.Album.Key) != idio.Album || len(idio.Album.Tracks) != 2 {
		t.Errorf("playlist-only album = %+v", idio.Album)
	}
	if slices.Contains(l.Albums, idio.Album) || slices.Contains(l.RecentlyAdded, idio.Album) || slices.Contains(l.Artists, idio.Group) {
		t.Error("playlist-only album or artist listed in library views")
	}
	for _, tr := range l.Songs {
		if tr.Container != "" {
			t.Errorf("%q in library has container %q", tr.Title, tr.Container)
		}
	}
}

func playlistNamed(l *Library, name string) *Playlist {
	for _, p := range l.PlaylistsFlat {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func TestPlaylists(t *testing.T) {
	l := fixture(t)
	if got := names(l.Playlists, func(p *Playlist) string { return p.Name }); !slices.Equal(got, []string{"Moods", "Road Trip"}) {
		t.Errorf("top level = %q (the library playlist must be left out)", got)
	}
	if got := names(l.PlaylistsFlat, func(p *Playlist) string { return p.Name }); !slices.Equal(got, []string{"Moods", "Chill", "Gym", "Road Trip"}) {
		t.Errorf("flat = %q", got)
	}
	moods, chill, gym := l.Playlists[0], playlistNamed(l, "Chill"), playlistNamed(l, "Gym")
	if chill.Parent != moods || len(moods.Children) != 2 || len(moods.Tracks) != 0 {
		t.Errorf("tree: chill parent %v, moods children %d", chill.Parent, len(moods.Children))
	}
	if moods.Duration != chill.Duration+gym.Duration || chill.Duration == 0 {
		t.Errorf("durations: moods %v chill %v gym %v", moods.Duration, chill.Duration, gym.Duration)
	}
	if got := names(chill.Tracks, func(t *Track) string { return t.Title }); !slices.Equal(got, []string{"Jóga", "Idioteque", "Digital Love"}) {
		t.Errorf("chill order = %q", got)
	}
	if l.Playlist(gym.PID) != gym {
		t.Error("Playlist(pid) lookup failed")
	}
}

func TestPlaylistCycle(t *testing.T) {
	s := &bridge.Snapshot{Playlists: []bridge.SnapshotPlaylist{
		{PID: "A", ParentPID: "B", Name: "a", Kind: "folder"},
		{PID: "B", ParentPID: "A", Name: "b", Kind: "folder"},
		{PID: "C", ParentPID: "missing", Name: "c", Kind: "user"},
	}}
	l := Build(s)
	if len(l.PlaylistsFlat) != 3 || len(l.Playlists) == 0 {
		t.Fatalf("flat %d top %d", len(l.PlaylistsFlat), len(l.Playlists))
	}
}

func TestSortTracks(t *testing.T) {
	l := fixture(t)
	title := func(ts []*Track) []string { return names(ts, func(t *Track) string { return t.Title }) }

	byArtist := SortTracks(l.Songs, ByArtist, false)
	if byArtist[0].Group.Name != "The Beatles" || byArtist[0].Title != "Come Together" {
		t.Errorf("by artist starts with %q by %q", byArtist[0].Title, byArtist[0].Artist)
	}
	if last := byArtist[len(byArtist)-1]; last.Artist != "2Pac" {
		t.Errorf("by artist ends with %q", last.Artist)
	}

	byTime := SortTracks(l.Songs, ByTime, true)
	for i := 1; i < len(byTime); i++ {
		if byTime[i].Duration > byTime[i-1].Duration {
			t.Fatal("ByTime desc out of order")
		}
	}

	counting := l.Artist("Numbers").Albums[0].Tracks
	if got := title(SortTracks(counting, ByTitle, false)); !slices.Equal(got,
		[]string{"Track 1", "Track 2", "Track 3", "Track 10", "Track 11", "Track 20"}) {
		t.Errorf("natural title sort = %q", got)
	}
	if got := title(SortTracks(counting, ByTitle, true)); got[0] != "Track 20" {
		t.Errorf("title desc = %q", got)
	}

	// Same play count: ties stay in title order whichever way plays sort.
	var ties []*Track
	for _, tr := range l.Artist("Daft Punk").Albums[0].Tracks {
		ties = append(ties, &Track{Title: tr.Title, SortTitle: tr.SortTitle, Plays: 3})
	}
	for _, desc := range []bool{false, true} {
		got := title(SortTracks(ties, ByPlays, desc))
		if !slices.IsSortedFunc(got, func(a, b string) int { return CompareKeys(Fold(a), Fold(b)) }) {
			t.Errorf("ties (desc=%v) = %q", desc, got)
		}
	}

	byAlbum := SortTracks(l.Songs, ByAlbum, false)
	if byAlbum[0].AlbumName != "Abbey Road" || byAlbum[0].Title != "Come Together" {
		t.Errorf("by album starts with %q / %q", byAlbum[0].AlbumName, byAlbum[0].Title)
	}
}

func TestSortAlbums(t *testing.T) {
	l := fixture(t)
	byYear := SortAlbums(l.Albums, ByYear, true)
	if byYear[0].Title != "Renaissance" || byYear[len(byYear)-1].Title != "Help!" {
		t.Errorf("by year desc: %q … %q", byYear[0].Title, byYear[len(byYear)-1].Title)
	}
	byArtist := SortAlbums(l.Albums, ByArtist, false)
	if got := names(byArtist[:2], func(a *Album) string { return a.Title }); !slices.Equal(got, []string{"Abbey Road", "Help!"}) {
		t.Errorf("by artist = %q", got)
	}
}

func TestBuildLarge(t *testing.T) {
	s := libtest.Snapshot(20000)
	l := Build(s)
	if len(l.All) != 20000 {
		t.Fatalf("All = %d", len(l.All))
	}
	seen := map[*Album]bool{}
	for _, a := range l.Albums {
		if seen[a] {
			t.Fatalf("album %q listed twice", a.Title)
		}
		seen[a] = true
	}
	listed := 0
	for _, ar := range l.Artists {
		for _, a := range ar.Albums {
			if !seen[a] {
				t.Fatalf("artist %q lists unlisted album %q", ar.Name, a.Title)
			}
			listed++
		}
	}
	if listed != len(l.Albums) {
		t.Errorf("artists cover %d albums, library lists %d", listed, len(l.Albums))
	}
	for _, tr := range l.All {
		if !tr.InLibrary && tr.Container == "" {
			// Only playlist-only items that no playlist mentions lack one.
			continue
		}
		if tr.InLibrary && tr.Container != "" {
			t.Fatalf("library track %q has a container", tr.Title)
		}
	}
	for i := 1; i < len(l.Artists); i++ {
		if CompareKeys(l.Artists[i-1].SortName, l.Artists[i].SortName) > 0 {
			t.Fatalf("artists out of order: %q > %q", l.Artists[i-1].SortName, l.Artists[i].SortName)
		}
	}
}

func TestCacheRoundTrip(t *testing.T) {
	s := libtest.Snapshot(3000)
	s.Playlists[0].Items = append(s.Playlists[0].Items, "DEADBEEFDEADBEEF") // not a known track
	path := filepath.Join(t.TempDir(), "sub", "library.cache")
	if err := SaveCache(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.GeneratedAt != s.GeneratedAt || !slices.Equal(got.Tracks, s.Tracks) || len(got.Playlists) != len(s.Playlists) {
		t.Fatal("tracks differ after round trip")
	}
	for i := range s.Playlists {
		a, b := s.Playlists[i], got.Playlists[i]
		if a.PID != b.PID || a.ParentPID != b.ParentPID || a.Name != b.Name || a.Kind != b.Kind ||
			a.Special != b.Special || !slices.Equal(a.Items, b.Items) {
			t.Fatalf("playlist %d differs", i)
		}
	}

	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}

	data, _ := os.ReadFile(path)
	data[len(data)/2] ^= 0xff
	os.WriteFile(path, data, 0o644)
	if _, err := LoadCache(path); err == nil {
		t.Error("corrupt cache loaded without error")
	}
	data[8] = 99
	os.WriteFile(path, data, 0o644)
	if _, err := LoadCache(path); err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("wrong version: %v", err)
	}
	os.WriteFile(path, data[:10], 0o644)
	if _, err := LoadCache(path); err == nil {
		t.Error("truncated cache loaded without error")
	}
}

var benchSnap = map[int]*bridge.Snapshot{}

func snapN(n int) *bridge.Snapshot {
	if s := benchSnap[n]; s != nil {
		return s
	}
	benchSnap[n] = libtest.Snapshot(n)
	return benchSnap[n]
}

func BenchmarkBuild50k(b *testing.B) {
	s := snapN(50000)
	b.ReportAllocs()
	for b.Loop() {
		Build(s)
	}
}

func BenchmarkLoadCacheAndBuild50k(b *testing.B) {
	path := filepath.Join(b.TempDir(), "library.cache")
	if err := SaveCache(path, snapN(50000)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		s, err := LoadCache(path)
		if err != nil {
			b.Fatal(err)
		}
		Build(s)
	}
}

func BenchmarkLoadCache50k(b *testing.B) {
	path := filepath.Join(b.TempDir(), "library.cache")
	if err := SaveCache(path, snapN(50000)); err != nil {
		b.Fatal(err)
	}
	fi, _ := os.Stat(path)
	b.ReportMetric(float64(fi.Size())/1e6, "MB")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := LoadCache(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSaveCache50k(b *testing.B) {
	path := filepath.Join(b.TempDir(), "library.cache")
	s := snapN(50000)
	for b.Loop() {
		if err := SaveCache(path, s); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSortTracksByArtist50k(b *testing.B) {
	l := Build(snapN(50000))
	for b.Loop() {
		SortTracks(l.Songs, ByArtist, false)
	}
}

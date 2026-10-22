package search

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/library/libtest"
)

func fixture(t testing.TB) (*library.Library, *Index) {
	t.Helper()
	l := library.Build(libtest.Fixture())
	return l, NewIndex(l)
}

func span(from, to int) []int {
	var out []int
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	return out
}

func songTitles(hs []TrackHit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Track.Title
	}
	return out
}

func TestEmptyQuery(t *testing.T) {
	_, x := fixture(t)
	for _, q := range []string{"", "   ", "\t"} {
		r := x.Search(q, All, 10)
		if !r.Empty() || len(r.Top) != 0 || r.Fuzzy {
			t.Errorf("Search(%q) = %+v, want empty", q, r)
		}
	}
}

func TestTopArtistForPrefix(t *testing.T) {
	_, x := fixture(t)
	r := x.Search("beat", All, 10)
	if len(r.Top) == 0 || r.Top[0].Kind != KindArtist {
		t.Fatalf("Top = %+v", r.Top)
	}
	a := r.Artists[r.Top[0].Index]
	if a.Artist.Name != "The Beatles" || !slices.Equal(a.NameMatches, span(4, 8)) {
		t.Errorf("top artist %q matches %v", a.Artist.Name, a.NameMatches)
	}
	if len(r.Songs) != 6 {
		t.Errorf("songs by The Beatles: %q", songTitles(r.Songs))
	}
	for _, h := range r.Songs {
		if !slices.Equal(h.ArtistMatches, span(4, 8)) {
			t.Errorf("%q artist matches %v", h.Track.Title, h.ArtistMatches)
		}
	}
}

func TestMultiTermPrefix(t *testing.T) {
	_, x := fixture(t)
	r := x.Search("get luc", All, 10)
	if len(r.Songs) != 1 || r.Songs[0].Track.Title != "Get Lucky" {
		t.Fatalf("songs = %q", songTitles(r.Songs))
	}
	if got := r.Songs[0].TitleMatches; !slices.Equal(got, []int{0, 1, 2, 4, 5, 6}) {
		t.Errorf("title matches = %v", got)
	}
	if len(r.Top) != 1 || r.Top[0].Kind != KindSong || r.Fuzzy {
		t.Errorf("top = %+v fuzzy %v", r.Top, r.Fuzzy)
	}
}

func TestDiacritics(t *testing.T) {
	_, x := fixture(t)
	r := x.Search("bjork", All, 10)
	if len(r.Artists) != 1 || r.Artists[0].Artist.Name != "Björk" || !slices.Equal(r.Artists[0].NameMatches, span(0, 5)) {
		t.Fatalf("artists = %+v", r.Artists)
	}
	if r.Top[0].Kind != KindArtist {
		t.Errorf("top = %+v", r.Top)
	}
	if got := songTitles(r.Songs); len(got) != 3 || !slices.Contains(got, "Army of Me") {
		t.Errorf("songs = %q", got)
	}
	// Accented query, unaccented intent, and the other way round.
	if r := x.Search("JÓGA", Songs, 10); len(r.Songs) != 1 || !slices.Equal(r.Songs[0].TitleMatches, span(0, 4)) {
		t.Errorf("JÓGA = %+v", r.Songs)
	}
	if r := x.Search("agaetis", Albums, 10); len(r.Albums) != 1 || !slices.Equal(r.Albums[0].TitleMatches, span(0, 6)) {
		t.Errorf("agaetis = %+v", r.Albums)
	}
}

func TestTermsAcrossFields(t *testing.T) {
	_, x := fixture(t)
	r := x.Search("daft lucky", All, 10)
	if len(r.Songs) != 1 || r.Songs[0].Track.Title != "Get Lucky" {
		t.Fatalf("songs = %q", songTitles(r.Songs))
	}
	h := r.Songs[0]
	if !slices.Equal(h.TitleMatches, span(4, 9)) || !slices.Equal(h.ArtistMatches, span(0, 4)) || h.AlbumMatches != nil {
		t.Errorf("matches: title %v artist %v album %v", h.TitleMatches, h.ArtistMatches, h.AlbumMatches)
	}
	if len(r.Artists) != 0 || len(r.Albums) != 0 {
		t.Errorf("artists %v albums %v: every term must match", r.Artists, r.Albums)
	}
	if len(r.Top) != 1 || r.Top[0].Kind != KindSong {
		t.Errorf("top = %+v", r.Top)
	}
}

func TestFuzzyFallback(t *testing.T) {
	_, x := fixture(t)
	r := x.Search("gtlcky", All, 10)
	if !r.Fuzzy || len(r.Songs) == 0 || r.Songs[0].Track.Title != "Get Lucky" {
		t.Fatalf("fuzzy %v songs %q", r.Fuzzy, songTitles(r.Songs))
	}
	if got := r.Songs[0].TitleMatches; !slices.Equal(got, []int{0, 2, 4, 6, 7, 8}) {
		t.Errorf("title matches = %v", got)
	}
	if r.Songs[0].Score >= strictBase || len(r.Top) != 0 {
		t.Errorf("fuzzy score %d, top %v", r.Songs[0].Score, r.Top)
	}
	if r := x.Search("qqqqzzzz", All, 10); !r.Empty() {
		t.Errorf("nonsense matched: %+v", r)
	}
}

func TestScopes(t *testing.T) {
	_, x := fixture(t)
	r := x.Search("chill", Playlists, 10)
	if len(r.Playlists) != 1 || r.Playlists[0].Playlist.Name != "Chill" || len(r.Songs) != 0 || r.Top != nil {
		t.Errorf("playlists scope = %+v", r)
	}
	r = x.Search("daft", Albums, 10)
	if len(r.Albums) != 2 || len(r.Artists) != 0 || len(r.Songs) != 0 {
		t.Errorf("albums scope: %+v", r)
	}
	r = x.Search("moods", All, 10)
	if len(r.Playlists) != 1 || r.Top[0].Kind != KindPlaylist {
		t.Errorf("folder: %+v", r)
	}
	// Playlist-only songs are not library songs.
	if r := x.Search("idioteque", Songs, 10); len(r.Songs) != 0 {
		t.Errorf("playlist-only track found: %q", songTitles(r.Songs))
	}
}

// ranked builds a library from titles and artists to check scoring order.
func ranked(t *testing.T, songs [][2]string) *Index {
	t.Helper()
	s := &bridge.Snapshot{}
	for i, sa := range songs {
		s.Tracks = append(s.Tracks, bridge.SnapshotTrack{
			PID: fmt.Sprintf("%016X", i+1), Title: sa[0], Artist: sa[1], Album: "Album " + sa[1],
			AlbumPID: fmt.Sprintf("A%015X", i+1), InLibrary: true, Duration: 1000,
		})
	}
	return NewIndex(library.Build(s))
}

func TestRankingOrder(t *testing.T) {
	x := ranked(t, [][2]string{
		{"Glove Story", "X"},                   // substring
		{"Endless Lovers", "X"},                // word prefix
		{"My Love Song", "X"},                  // whole word
		{"Lovesick Blues", "X"},                // field prefix
		{"Love", "X"},                          // exact
		{"Love Is All Around Us Tonight", "X"}, // prefix, whole word, long
		{"Love Me", "X"},                       // prefix, whole word, short
		{"Something", "Love"},                  // artist exact
	})
	got := songTitles(x.Search("love", Songs, 0).Songs)
	want := []string{"Love", "Love Me", "Love Is All Around Us Tonight", "Lovesick Blues", "Something", "My Love Song", "Endless Lovers", "Glove Story"}
	if !slices.Equal(got, want) {
		t.Errorf("order:\n%q\nwant\n%q", got, want)
	}
}

func TestTermsInOneField(t *testing.T) {
	x := ranked(t, [][2]string{
		{"Road 19", "Golden Winter"},
		{"Golden Signal Road", "X"},
	})
	if got := songTitles(x.Search("golden ro", Songs, 0).Songs); got[0] != "Golden Signal Road" {
		t.Errorf("order = %q", got)
	}
}

func TestTitleOverArtistOverAlbum(t *testing.T) {
	s := &bridge.Snapshot{Tracks: []bridge.SnapshotTrack{
		{PID: "01", Title: "Other", Artist: "Other", Album: "Robyn Live", InLibrary: true},
		{PID: "02", Title: "Other", Artist: "Robyn", Album: "Other", InLibrary: true},
		{PID: "03", Title: "Robyn", Artist: "Other", Album: "Other", InLibrary: true},
	}}
	x := NewIndex(library.Build(s))
	r := x.Search("robyn", Songs, 0)
	var got []string
	for _, h := range r.Songs {
		got = append(got, h.Track.PID)
	}
	if !slices.Equal(got, []string{"03", "02", "01"}) {
		t.Errorf("order = %v", got)
	}
}

func TestFavoriteAndPlaysBreakTies(t *testing.T) {
	s := &bridge.Snapshot{Tracks: []bridge.SnapshotTrack{
		{PID: "01", Title: "Alpha Song", InLibrary: true},
		{PID: "02", Title: "Alpha Song", InLibrary: true, Plays: 100},
		{PID: "03", Title: "Alpha Song", InLibrary: true, Favorited: true},
	}}
	x := NewIndex(library.Build(s))
	var got []string
	for _, h := range x.Search("alpha", Songs, 0).Songs {
		got = append(got, h.Track.PID)
	}
	if !slices.Equal(got, []string{"03", "02", "01"}) {
		t.Errorf("order = %v", got)
	}
}

func TestLimitAndDeterminism(t *testing.T) {
	l := library.Build(libtest.Snapshot(5000))
	x := NewIndex(l)
	full := x.Search("e", Songs, 0)
	lim := x.Search("e", Songs, 25)
	if len(lim.Songs) != 25 || len(full.Songs) <= 25 {
		t.Fatalf("limit: %d of %d", len(lim.Songs), len(full.Songs))
	}
	for i := range lim.Songs {
		if lim.Songs[i].Track != full.Songs[i].Track {
			t.Fatalf("limited result %d differs from full ranking", i)
		}
	}
	for i := 1; i < len(full.Songs); i++ {
		if full.Songs[i].Score > full.Songs[i-1].Score {
			t.Fatal("results not sorted by score")
		}
	}
	again := x.Search("e", Songs, 25)
	for i := range lim.Songs {
		if again.Songs[i].Track != lim.Songs[i].Track {
			t.Fatal("search is not deterministic")
		}
	}
}

// The buffer sweep must find exactly what a naive per-entity filter finds.
func TestSearchMatchesFilter(t *testing.T) {
	l := library.Build(libtest.Snapshot(20000))
	x := NewIndex(l)
	for _, q := range []string{"e", "love", "the", "night fire", "café", "cafe", "2", "o n", "golden road s", "jalapeno"} {
		r := x.Search(q, All, 0)
		if r.Fuzzy {
			t.Errorf("%q: unexpected fuzzy fallback", q)
			continue
		}
		want := FilterTracks(l.Songs, q)
		got := make(map[*library.Track]bool, len(r.Songs))
		for _, h := range r.Songs {
			got[h.Track] = true
		}
		if len(got) != len(want) {
			t.Errorf("%q: search %d songs, filter %d", q, len(got), len(want))
		}
		for _, tr := range want {
			if !got[tr] {
				t.Errorf("%q: search missed %q", q, tr.Title)
				break
			}
		}
		if n := len(FilterAlbums(l.Albums, q)); n != len(r.Albums) {
			t.Errorf("%q: search %d albums, filter %d", q, len(r.Albums), n)
		}
		if n := len(FilterArtists(l.Artists, q)); n != len(r.Artists) {
			t.Errorf("%q: search %d artists, filter %d", q, len(r.Artists), n)
		}
		if n := len(FilterPlaylists(l.PlaylistsFlat, q)); n != len(r.Playlists) {
			t.Errorf("%q: search %d playlists, filter %d", q, len(r.Playlists), n)
		}
	}
}

func TestFilter(t *testing.T) {
	l, _ := fixture(t)
	if got := FilterTracks(l.Songs, ""); len(got) != len(l.Songs) {
		t.Errorf("empty filter dropped tracks")
	}
	got := FilterTracks(l.Songs, "daft o")
	var titles []string
	for _, tr := range got {
		titles = append(titles, tr.Title)
	}
	// Songs order (by title) is kept, not ranking.
	want := []string{"Around the World", "Digital Love", "Get Lucky", "Give Life Back to Music", "Instant Crush", "Lose Yourself to Dance", "One More Time"}
	if !slices.Equal(titles, want) {
		t.Errorf("filter = %q, want %q", titles, want)
	}
	if got := FilterTracks(l.Songs, "gtlcky"); len(got) != 1 || got[0].Title != "Get Lucky" {
		t.Errorf("fuzzy filter = %v", got)
	}
	if got := FilterArtists(l.Artists, "the b"); len(got) != 1 || got[0].Name != "The Beatles" {
		t.Errorf("FilterArtists = %v", got)
	}
	if got := FilterPlaylists(l.PlaylistsFlat, "gym"); len(got) != 1 {
		t.Errorf("FilterPlaylists = %v", got)
	}
	if got := FilterAlbums(l.Albums, "homogenic bjork"); len(got) != 1 {
		t.Errorf("FilterAlbums = %v", got)
	}
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		field, pat string
		ok         bool
		pos        []int
	}{
		{"get lucky", "gtlcky", true, []int{0, 2, 4, 6, 7, 8}},
		{"get lucky", "lucky", true, []int{4, 5, 6, 7, 8}},
		{"abc", "abcd", false, nil},
		{"axbxc abc", "abc", true, []int{0, 2, 4}}, // the earliest window, as fzf v1
		{"bjork", "bjk", true, []int{0, 1, 4}},
		{"ελληνικα", "ελκ", true, []int{0, 2, 12}},
	}
	for _, c := range cases {
		var pos []int
		_, ok := fuzzyMatch(c.field, []rune(c.pat), &pos)
		if ok != c.ok || !slices.Equal(pos, c.pos) {
			t.Errorf("fuzzyMatch(%q, %q) = %v %v, want %v %v", c.field, c.pat, ok, pos, c.ok, c.pos)
		}
	}
	s1, _ := fuzzyMatch("get lucky", []rune("glu"), nil)
	s2, _ := fuzzyMatch("gaslucky", []rune("glu"), nil)
	if s1 <= s2 {
		t.Errorf("word starts should score higher: %d <= %d", s1, s2)
	}
}

var (
	benchLib *library.Library
	benchIdx *Index
)

// bench returns a library with about 50k songs, 5k albums and 2k artists.
func bench(b *testing.B) (*library.Library, *Index) {
	if benchIdx == nil {
		benchLib = library.Build(libtest.Snapshot(62000))
		benchIdx = NewIndex(benchLib)
	}
	return benchLib, benchIdx
}

func BenchmarkSearch(b *testing.B) {
	for _, q := range []string{"a", "lo", "love", "get luc", "daft lucky", "the beatles", "gtlcky", "zzqx"} {
		b.Run(q, func(b *testing.B) {
			_, x := bench(b)
			b.ReportAllocs()
			for b.Loop() {
				x.Search(q, All, 50)
			}
		})
	}
}

func BenchmarkFilterTracks(b *testing.B) {
	for _, q := range []string{"a", "love", "night fire", "gtlcky"} {
		b.Run(q, func(b *testing.B) {
			l, _ := bench(b)
			b.ReportAllocs()
			for b.Loop() {
				FilterTracks(l.Songs, q)
			}
		})
	}
}

// BenchmarkFilterTracksCold filters a list for the first time: the corpus
// build is included.
func BenchmarkFilterTracksCold(b *testing.B) {
	l, _ := bench(b)
	b.ReportAllocs()
	for b.Loop() {
		trackFilters.mu.Lock()
		trackFilters.entries = nil
		trackFilters.mu.Unlock()
		FilterTracks(l.Songs, "love")
	}
}

func TestFilterConcurrent(t *testing.T) {
	l := library.Build(libtest.Snapshot(3000))
	lists := [][]*library.Track{l.Songs, l.Favorites, l.All, library.SortTracks(l.Songs, library.ByArtist, false), l.Songs[:100]}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				ts := lists[(g+i)%len(lists)]
				got := FilterTracks(ts, "love")
				for _, tr := range got {
					if !strings.Contains(tr.FoldTitle+"|"+tr.FoldArtist+"|"+tr.FoldAlbum, "love") {
						t.Errorf("%q does not match", tr.Title)
						return
					}
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkNewIndex(b *testing.B) {
	l, _ := bench(b)
	b.ReportAllocs()
	for b.Loop() {
		NewIndex(l)
	}
}

func TestResetFiltersDropsCorpora(t *testing.T) {
	l := library.Build(libtest.Snapshot(200))
	FilterTracks(l.Songs, "love")
	FilterAlbums(l.Albums, "love")
	trackFilters.mu.Lock()
	n := len(trackFilters.entries)
	trackFilters.mu.Unlock()
	if n == 0 {
		t.Fatal("filtering kept no corpus; the test proves nothing")
	}
	ResetFilters()
	for name, fc := range map[string]interface{ size() int }{
		"tracks": trackFilters, "albums": albumFilters, "artists": artistFilters, "playlists": playlistFilters,
	} {
		if fc.size() != 0 {
			t.Errorf("%s: %d corpora left", name, fc.size())
		}
	}
	if got := FilterTracks(l.Songs, "love"); len(got) == 0 {
		t.Fatal("filtering stopped working after a reset")
	}
}

func (fc *filterCache[T]) size() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.entries)
}

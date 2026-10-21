package player

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/catalog"
	"github.com/bogdankim/encore/internal/library"
)

// songs makes n catalog songs of one album, 200 s each, with a few seconds'
// difference so a duration alone never picks the wrong one.
func (r *rig) songs(n int) []CatalogItem {
	out := make([]CatalogItem, n)
	for i := range out {
		out[i] = CatalogItem{
			StoreID:  fmt.Sprint(1440942200 + i),
			Title:    fmt.Sprintf("Catalog Song %d", i),
			Artist:   "Bill Evans Trio",
			Album:    "Waltz for Debby",
			AlbumID:  "1440942156",
			Duration: time.Duration(200+i*7) * time.Second,
		}
	}
	return out
}

// streaming reports Music playing catalog song s under the temporary pid raw,
// with Music's own spelling of the metadata.
func (r *rig) streaming(raw string, s CatalogItem, pos float64) {
	np := r.nowPlaying("", bridge.Playing, pos)
	np.Track = &bridge.CurrentTrack{PID: raw, Name: s.Title, Artist: s.Artist, Album: s.Album, Duration: s.Duration.Seconds() + 0.4}
	r.p.Observe(bridge.PlayerEvent{NowPlaying: np})
}

func idsOf(ss []CatalogItem) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.StoreID
	}
	return out
}

func storePIDs(ss []CatalogItem) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = StorePID(s.StoreID)
	}
	return out
}

func TestPlayCatalogIssuesPlayStore(t *testing.T) {
	r := newRig(t)
	ss := r.songs(5)
	ss = append(ss[:1], append([]CatalogItem{{Title: "no id"}}, ss[1:]...)...) // left out
	r.p.PlayCatalog(Source{Kind: KindCatalog, ID: "1440942156", Title: "Waltz for Debby"}, ss, 3)

	c := onlyCall(t, r.calls(), "playStore")
	if want := idsOf(ss[3:]); !slices.Equal(c.pids, want) || c.n != 0 {
		t.Fatalf("playStore %v start %d, want %v", c.pids, c.n, want)
	}
	s := r.p.State()
	if !s.Loading || s.Track != nil || s.Catalog == nil || *s.Catalog != ss[3] || s.Upcoming != 2 {
		t.Fatalf("loading=%v track=%v catalog=%v upcoming=%d", s.Loading, s.Track, s.Catalog, s.Upcoming)
	}
	if s.NowPlaying.Track == nil || s.NowPlaying.Track.Name != ss[3].Title {
		t.Fatalf("loading now playing %+v", s.NowPlaying.Track)
	}
	up := r.p.Upcoming()
	if !IsCatalog(up[0]) || up[0].Title != ss[4].Title || up[0].Album == nil || up[0].Group == nil {
		t.Fatalf("upcoming[0] = %+v", up[0])
	}
	if it := r.p.CatalogItem(up[0]); it == nil || *it != ss[4] {
		t.Fatalf("CatalogItem(upcoming[0]) = %v", it)
	}
	if r.p.CatalogItem(&library.Track{PID: "AAAA"}) != nil {
		t.Fatal("library track reported as catalog")
	}

	// Music still shows what it had for a few seconds: not a confirmation and
	// not dead yet.
	r.playing("C72A90C1418D5B89", 137)
	r.clock.Advance(deadGrace)
	noCalls(t, r.calls())
	if !r.p.State().Loading {
		t.Fatal("stopped loading on the old track")
	}
	r.streaming("010A45CAE15EDECB", ss[3], 0)
	s = r.p.State()
	if s.Loading || s.Track != nil || s.Catalog == nil || s.Catalog.StoreID != ss[3].StoreID {
		t.Fatalf("after start: loading=%v track=%v catalog=%v", s.Loading, s.Track, s.Catalog)
	}
	if s.NowPlaying.Track.PID != "010A45CAE15EDECB" {
		t.Fatalf("now playing pid %s", s.NowPlaying.Track.PID)
	}
	r.clock.Advance(storePendingTimeout)
	noCalls(t, r.calls())
}

func TestCatalogNextAndPreviousFollowMusic(t *testing.T) {
	r := newRig(t)
	ss := r.songs(5)
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 1)
	r.calls()
	r.flush()
	r.streaming("T1", ss[1], 0)

	// Music walks the tracklist itself: Next is Music's next.
	r.p.Next()
	if c := onlyCall(t, r.calls(), "next"); c.name != "next" {
		t.Fatal(c)
	}
	r.streaming("T1", ss[1], 0.5) // stale report
	if !r.p.State().Loading {
		t.Fatal("stale report confirmed the step")
	}
	r.streaming("T2", ss[2], 0)
	if s := r.p.State(); s.Loading || s.Catalog.StoreID != ss[2].StoreID || s.Upcoming != 2 {
		t.Fatalf("after next: loading=%v catalog=%v upcoming=%d", s.Loading, s.Catalog, s.Upcoming)
	}

	// Back within the slice is Music's previous.
	r.p.Previous()
	onlyCall(t, r.calls(), "previous")
	r.streaming("T3", ss[1], 0)
	if s := r.p.State(); s.Loading || s.Catalog.StoreID != ss[1].StoreID {
		t.Fatalf("after previous: loading=%v catalog=%v", s.Loading, s.Catalog)
	}

	// Before the slice start the tracklist is issued again from there.
	r.p.Previous()
	c := onlyCall(t, r.calls(), "playStore")
	if want := idsOf(ss); !slices.Equal(c.pids, want) {
		t.Fatalf("playStore %v, want %v", c.pids, want)
	}
}

func TestCatalogBoundaryMatchesMetadata(t *testing.T) {
	r := newRig(t)
	ss := r.songs(4)
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 0)
	r.calls()
	r.flush()
	r.streaming("T0", ss[0], 0)

	// Music moves on by itself under a new temporary pid.
	r.streaming("T1", ss[1], 0)
	if s := r.p.State(); s.Catalog == nil || s.Catalog.StoreID != ss[1].StoreID || s.Upcoming != 2 || s.Source.Kind != KindCatalog {
		t.Fatalf("after boundary: catalog=%v upcoming=%d source=%v", s.Catalog, s.Upcoming, s.Source)
	}
	r.streaming("T1", ss[1], 30)
	noCalls(t, r.calls())

	// Music's spelling may differ a little: a longer title with the right
	// artist and duration still counts.
	other := ss[2]
	other.Title += " (Take 1)"
	r.streaming("T2", other, 0)
	if s := r.p.State(); s.Catalog == nil || s.Catalog.StoreID != ss[2].StoreID {
		t.Fatalf("loose match: catalog=%v", s.Catalog)
	}

	// Something else altogether: encore lets go of the context.
	r.streaming("T9", CatalogItem{Title: "Unrelated", Artist: "Someone", Duration: 100 * time.Second}, 0)
	if s := r.p.State(); s.Catalog != nil || s.Source.Kind != "" || s.Upcoming != 0 {
		t.Fatalf("after foreign track: catalog=%v source=%v upcoming=%d", s.Catalog, s.Source, s.Upcoming)
	}
	noCalls(t, r.calls())
}

func TestSameSong(t *testing.T) {
	r := newRig(t)
	s := CatalogItem{StoreID: "1", Title: "Waltz for Debby (Take 2)", Artist: "Bill Evans Trio", Duration: 419 * time.Second}
	pid := r.p.standIn(s).PID
	for _, tc := range []struct {
		name, artist string
		dur          float64
		want         bool
	}{
		{"Waltz for Debby (Take 2)", "Bill Evans Trio", 419.6, true},
		{"WALTZ FOR DEBBY (TAKE 2)", "", 418.2, true},            // case, no artist, close duration
		{"Waltz for Debby (Take 2)", "Bill Evans Trio", 0, true}, // duration unknown
		{"Waltz for Debby (Take 2)", "Bill Evans", 425, true},    // artist contained
		{"Waltz for Debby (Take 2)", "Someone", 300, false},
		{"Waltz for Debby", "Bill Evans Trio", 419, true}, // title contained, close duration
		{"Waltz for Debby", "Bill Evans Trio", 380, false},
		{"Milestones", "Bill Evans Trio", 419, false},
	} {
		if got := r.p.sameSong(pid, library.Fold(tc.name), library.Fold(tc.artist), tc.dur); got != tc.want {
			t.Errorf("sameSong(%q, %q, %v) = %v, want %v", tc.name, tc.artist, tc.dur, got, tc.want)
		}
	}
}

func TestCatalogQueueReissuedAtBoundary(t *testing.T) {
	r := newRig(t)
	ss := r.songs(4)
	extra := CatalogItem{StoreID: "1440942999", Title: "Queued", Artist: "Someone Else", Duration: 150 * time.Second}
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 0)
	r.calls()
	r.flush()
	r.streaming("T0", ss[0], 0)

	r.p.PlayNextCatalog(extra)
	noCalls(t, r.calls()) // Music only learns at the boundary
	if up := r.p.UpcomingN(2); up[0].PID != StorePID(extra.StoreID) || up[1].PID != StorePID(ss[1].StoreID) {
		t.Fatalf("upcoming %v", pidsOf(up))
	}
	r.streaming("T1", ss[1], 0) // Music went on with its tracklist
	c := onlyCall(t, r.calls(), "playStore")
	if want := append([]string{extra.StoreID}, idsOf(ss[1:])...); !slices.Equal(c.pids, want) {
		t.Fatalf("playStore %v, want %v", c.pids, want)
	}
	r.flush()
	r.streaming("T5", extra, 0)
	if s := r.p.State(); s.Loading || s.Catalog == nil || s.Catalog.StoreID != extra.StoreID {
		t.Fatalf("queued song: loading=%v catalog=%v", s.Loading, s.Catalog)
	}

	// AddCatalogToQueue appends; library-only helpers keep working on stand-ins.
	r.p.AddCatalogToQueue(ss[0])
	r.p.PlayNext(r.p.UpcomingAt(1))
	if got := pidsOf(r.p.UpcomingN(5)); !slices.Equal(got, append(storePIDs([]CatalogItem{ss[1], ss[0]}), storePIDs(ss[1:])...)) {
		t.Fatalf("upcoming %v", got)
	}
}

func TestMixedQueueSwitchesKindAtSliceEnd(t *testing.T) {
	r := newRig(t)
	ss := r.songs(2)
	lib := r.tracks(1)
	r.p.AddToQueue(lib[0])
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 0)
	// The queued library track plays between the first song and the rest of
	// the context, so the catalog run is cut after the first song.
	c := onlyCall(t, r.calls(), "playStore")
	if !slices.Equal(c.pids, idsOf(ss[:1])) {
		t.Fatalf("playStore %v", c.pids)
	}
	r.flush()
	r.streaming("T0", ss[0], 0)
	r.streaming("T0", ss[0], 199) // last seconds

	// Music runs out of tracklist and stops: the library track goes out.
	r.stoppedEv()
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf(lib)) {
		t.Fatalf("playTracks %v", c.pids)
	}
	r.played(lib[0].PID)
	if s := r.p.State(); s.Track != lib[0] || s.Catalog != nil {
		t.Fatalf("library track: track=%v catalog=%v", s.Track, s.Catalog)
	}

	// Library track ends with Music in the queue playlist's end: the catalog
	// rest of the context goes out through PlayStore.
	r.playing(lib[0].PID, 199)
	r.stoppedEv()
	if c := onlyCall(t, r.calls(), "playStore"); !slices.Equal(c.pids, idsOf(ss[1:])) {
		t.Fatalf("playStore %v", c.pids)
	}
}

func TestLibraryQueuedDuringCatalogTracklist(t *testing.T) {
	r := newRig(t)
	ss := r.songs(2)
	lib := r.tracks(1)
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 0)
	r.calls()
	r.flush()
	r.streaming("T0", ss[0], 0)
	r.p.AddToQueue(lib[0])
	r.streaming("T1", ss[1], 0) // Music went on with the tracklist
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf(lib)) {
		t.Fatalf("playTracks %v", c.pids)
	}
}

func TestCatalogAutoplayAfterTracklistIsReplaced(t *testing.T) {
	r := newRig(t)
	ss := r.songs(1)
	lib := r.tracks(1)
	r.p.AddToQueue(lib[0])
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 0)
	r.calls()
	r.flush()
	r.streaming("T0", ss[0], 0)
	r.streaming("T0", ss[0], 198)
	// Music's Autoplay picks something after the tracklist instead of stopping.
	r.streaming("T7", CatalogItem{Title: "Autoplay pick", Artist: "Someone", Duration: 180 * time.Second}, 0)
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf(lib)) {
		t.Fatalf("playTracks %v", c.pids)
	}
}

func TestLibraryContextThenQueuedCatalogSong(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	ss := r.songs(1)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.calls()
	r.played(ts[0].PID)
	r.p.AddCatalogToQueue(ss[0])
	r.playing(ts[1].PID, 0) // Music went on through the queue playlist
	if c := onlyCall(t, r.calls(), "playStore"); !slices.Equal(c.pids, idsOf(ss)) {
		t.Fatalf("playStore %v", c.pids)
	}
	r.flush()
	r.streaming("T0", ss[0], 0)
	if s := r.p.State(); s.Loading || s.Catalog == nil {
		t.Fatalf("loading=%v catalog=%v", s.Loading, s.Catalog)
	}
	// After the catalog song, the library context resumes with PlayTracks.
	r.streaming("T0", ss[0], 199)
	r.stoppedEv()
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf(ts[1:])) {
		t.Fatalf("playTracks %v", c.pids)
	}
}

func TestDeadCatalogSongGetsLongerGrace(t *testing.T) {
	r := newRig(t)
	ss := r.songs(3)
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 0)
	r.calls()
	r.flush()
	r.stoppedEv()
	r.clock.Advance(deadGrace) // a library track would count as dead by now
	noCalls(t, r.calls())
	r.clock.Advance(storeDeadGrace - deadGrace)
	c := onlyCall(t, r.calls(), "playStore")
	if !slices.Equal(c.pids, idsOf(ss[1:])) {
		t.Fatalf("skipped to %v", c.pids)
	}
	if s := r.p.State(); s.LastError != MsgSkippedUnavailable {
		t.Fatalf("error %q", s.LastError)
	}
}

func TestPlayCatalogNothingPlayable(t *testing.T) {
	r := newRig(t)
	r.p.PlayCatalog(Source{Kind: KindCatalog}, []CatalogItem{{Title: "x"}, {StoreID: "12a"}}, 0)
	noCalls(t, r.calls())
	if s := r.p.State(); s.LastError != MsgNothingPlayable {
		t.Fatalf("error %q", s.LastError)
	}
}

func TestCatalogItemsArePruned(t *testing.T) {
	r := newRig(t)
	ss := r.songs(3)
	r.p.PlayCatalog(Source{Kind: KindCatalog}, ss, 0)
	ts := r.tracks(2)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 0)
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 1)
	r.p.mu.Lock()
	n := len(r.p.items)
	r.p.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d catalog entries kept", n)
	}
}

func TestCatalogItemFromSong(t *testing.T) {
	s := catalog.Song{ID: 1440942209, AlbumID: 1440942156, Name: "My Foolish Heart", Artist: "Bill Evans Trio",
		Album: "Waltz for Debby", Duration: 296 * time.Second,
		Artwork: "https://is1-ssl.mzstatic.com/image/thumb/Music/v4/x/source/100x100bb.jpg"}
	it := CatalogItemFromSong(s, 600)
	want := CatalogItem{StoreID: "1440942209", AlbumID: "1440942156", Title: "My Foolish Heart", Artist: "Bill Evans Trio",
		Album: "Waltz for Debby", Duration: 296 * time.Second,
		ArtURL: "https://is1-ssl.mzstatic.com/image/thumb/Music/v4/x/source/600x600bb.jpg"}
	if it != want {
		t.Fatalf("got %+v", it)
	}
}

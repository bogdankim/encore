package bridge

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestFake(t *testing.T) (*Fake, *testClock) {
	t.Helper()
	clk := &testClock{t: FakeEpoch}
	f := NewFakeWithClock(1, clk.now)
	f.artDir = t.TempDir()
	t.Cleanup(func() { f.Close() })
	return f, clk
}

// drain collects events until none arrive for a short while.
func drain(f *Fake) []Event {
	var out []Event
	for {
		select {
		case ev := <-f.Events():
			out = append(out, ev)
		case <-time.After(30 * time.Millisecond):
			return out
		}
	}
}

func playerEvents(evs []Event) []NowPlaying {
	var out []NowPlaying
	for _, ev := range evs {
		if pe, ok := ev.(PlayerEvent); ok {
			out = append(out, pe.NowPlaying)
		}
	}
	return out
}

func mustSnapshot(t *testing.T, f *Fake) *Snapshot {
	t.Helper()
	s, err := f.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFakeLibraryShape(t *testing.T) {
	f, _ := newTestFake(t)
	s := mustSnapshot(t, f)

	pids := map[string]*SnapshotTrack{}
	albums := map[string]bool{}
	artists := map[string]bool{}
	var inLib, favs, explicit, unavailable, singles, feats, nonLatin, compilations, long int
	for i := range s.Tracks {
		tr := &s.Tracks[i]
		if pids[tr.PID] != nil {
			t.Fatalf("duplicate pid %s", tr.PID)
		}
		pids[tr.PID] = tr
		if len(tr.PID) != 16 || strings.ToUpper(tr.PID) != tr.PID {
			t.Fatalf("pid %q", tr.PID)
		}
		if tr.Year < 1965 || tr.Year > 2026 {
			t.Errorf("%s: year %d", tr.Title, tr.Year)
		}
		if tr.Duration < 90_000 || tr.Duration > 541_000 {
			t.Errorf("%s: duration %d", tr.Title, tr.Duration)
		}
		if tr.Added > FakeEpoch.Unix() || tr.Added < FakeEpoch.AddDate(-6, 0, -1).Unix() {
			t.Errorf("%s: added %v", tr.Title, time.Unix(tr.Added, 0))
		}
		if tr.Track < 1 || tr.Track > tr.TrackCount || tr.Disc > tr.DiscCount {
			t.Errorf("%s: track %d/%d disc %d/%d", tr.Title, tr.Track, tr.TrackCount, tr.Disc, tr.DiscCount)
		}
		if !tr.InLibrary {
			continue
		}
		inLib++
		albums[tr.AlbumPID] = true
		artists[cmpOr(tr.AlbumArtist, tr.Artist)] = true
		if tr.Favorited {
			favs++
		}
		if tr.Explicit {
			explicit++
		}
		if !tr.Available {
			unavailable++
		}
		if strings.HasSuffix(tr.Album, " - Single") {
			singles++
			if tr.TrackCount != 1 {
				t.Errorf("single %q has trackCount %d", tr.Album, tr.TrackCount)
			}
		}
		if strings.Contains(tr.Title, "(feat. ") {
			feats++
		}
		if strings.IndexFunc(tr.Artist, func(r rune) bool { return r > unicode.MaxLatin1 && !unicode.Is(unicode.Latin, r) }) >= 0 {
			nonLatin++
		}
		if tr.Compilation && tr.AlbumArtist == "Various Artists" {
			compilations++
		}
		if len([]rune(tr.Title)) > 45 {
			long++
		}
	}
	between := func(name string, v, lo, hi int) {
		if v < lo || v > hi {
			t.Errorf("%s = %d, want %d..%d", name, v, lo, hi)
		}
	}
	between("tracks", inLib, 5500, 6500)
	between("albums", len(albums), 460, 580)
	between("artists", len(artists), 240, 290)
	between("favorites %", favs*100/inLib, 11, 19)
	between("explicit", explicit, 20, 400)
	between("unavailable", unavailable, 3, 80)
	between("singles", singles, 30, 150)
	between("feat.", feats, 50, 800)
	between("non-Latin artist tracks", nonLatin, 20, 600)
	between("compilation tracks", compilations, 100, 300)
	between("long titles", long, 5, 300)

	var folders, children, smart, subscription, outside int
	byPID := map[string]SnapshotPlaylist{}
	for _, p := range s.Playlists {
		byPID[p.PID] = p
	}
	for _, p := range s.Playlists {
		switch p.Kind {
		case "folder":
			folders++
		case "smart":
			smart++
		case "subscription":
			subscription++
			for _, pid := range p.Items {
				if !pids[pid].InLibrary {
					outside++
				}
			}
		}
		if p.ParentPID != "" {
			children++
			if byPID[p.ParentPID].Kind != "folder" {
				t.Errorf("%s: parent is not a folder", p.Name)
			}
		}
		for _, pid := range p.Items {
			if pids[pid] == nil {
				t.Fatalf("%s: unknown item %s", p.Name, pid)
			}
		}
	}
	between("playlists", len(s.Playlists), 22, 30)
	between("folders", folders, 2, 2)
	between("folder children", children, 4, 10)
	between("smart", smart, 3, 8)
	between("subscription", subscription, 1, 3) // Northbound, and the two followed Apple Music playlists
	between("subscription-only tracks", outside, 10, 60)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestFakeDeterministic(t *testing.T) {
	clk := &testClock{t: FakeEpoch}
	a := mustSnapshot(t, NewFakeWithClock(7, clk.now))
	b := mustSnapshot(t, NewFakeWithClock(7, clk.now))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed, different libraries")
	}
	c := mustSnapshot(t, NewFakeWithClock(8, clk.now))
	if c.Tracks[0].PID == a.Tracks[0].PID {
		t.Fatal("different seeds, same library")
	}
}

// albumTracks returns the in-order pids of a full album with at least n
// available tracks.
func albumTracks(t *testing.T, s *Snapshot, n int) []*SnapshotTrack {
	t.Helper()
	by := map[string][]*SnapshotTrack{}
	for i := range s.Tracks {
		tr := &s.Tracks[i]
		if tr.InLibrary && tr.DiscCount == 1 {
			by[tr.AlbumPID] = append(by[tr.AlbumPID], tr)
		}
	}
	for _, trs := range by {
		if len(trs) < n || len(trs) != trs[0].TrackCount {
			continue
		}
		ok := true
		for _, tr := range trs {
			ok = ok && tr.Available
		}
		if ok {
			slices.SortFunc(trs, func(a, b *SnapshotTrack) int { return a.Track - b.Track })
			return trs[:n]
		}
	}
	t.Fatal("no album found")
	return nil
}

func pidsOf(trs []*SnapshotTrack) []string {
	var out []string
	for _, tr := range trs {
		out = append(out, tr.PID)
	}
	return out
}

func TestFakePlayback(t *testing.T) {
	f, clk := newTestFake(t)
	ctx := context.Background()
	state := func() NowPlaying {
		t.Helper()
		np, err := f.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return np
	}
	near := func(got, want float64) bool { return got > want-0.01 && got < want+0.01 }

	np := state()
	if np.State != Paused || np.Track == nil || np.Position != 62 || np.Volume != 40 || np.Repeat != RepeatOff {
		t.Fatalf("initial state = %+v", np)
	}

	album := albumTracks(t, mustSnapshot(t, f), 3)
	if err := f.PlayTracks(ctx, pidsOf(album), nil); err != nil {
		t.Fatal(err)
	}
	np = state()
	if np.State != Playing || np.Track.PID != album[0].PID || np.Position != 0 {
		t.Fatalf("after PlayTracks: %+v", np)
	}
	if np.Track.Duration != float64(album[0].Duration)/1000 || np.Track.Name != album[0].Title {
		t.Fatalf("track = %+v", np.Track)
	}

	clk.add(10 * time.Second)
	if np = state(); !near(np.Position, 10) || np.At != clk.now().UnixMilli() {
		t.Fatalf("position after 10 s = %v", np.Position)
	}
	f.Pause(ctx)
	clk.add(5 * time.Second)
	if np = state(); np.State != Paused || !near(np.Position, 10) {
		t.Fatalf("paused: %+v", np)
	}
	f.PlayPause(ctx)
	clk.add(time.Second)
	if np = state(); np.State != Playing || !near(np.Position, 11) {
		t.Fatalf("resumed: %+v", np)
	}

	// The track ends on its own and the next one starts.
	d0 := float64(album[0].Duration) / 1000
	f.Seek(ctx, d0-1)
	clk.add(3 * time.Second)
	if np = state(); np.Track.PID != album[1].PID || !near(np.Position, 2) {
		t.Fatalf("after track end: %+v (pos %v)", np.Track, np.Position)
	}
	plays := func(pid string) int {
		for _, tr := range mustSnapshot(t, f).Tracks {
			if tr.PID == pid {
				return tr.Plays
			}
		}
		return -1
	}
	if got := plays(album[0].PID); got != album[0].Plays+1 {
		t.Fatalf("plays = %d, want %d", got, album[0].Plays+1)
	}

	// Previous: restart after 3 s, otherwise go back.
	clk.add(5 * time.Second)
	f.Previous(ctx)
	if np = state(); np.Track.PID != album[1].PID || np.Position != 0 {
		t.Fatalf("previous after 7 s: %+v", np)
	}
	f.Previous(ctx)
	if np = state(); np.Track.PID != album[0].PID {
		t.Fatalf("previous at 0 s: %+v", np.Track)
	}

	// Next runs off the end with repeat off.
	f.Next(ctx)
	f.Next(ctx)
	if np = state(); np.Track.PID != album[2].PID {
		t.Fatalf("next: %+v", np.Track)
	}
	f.Next(ctx)
	if np = state(); np.State != Stopped || np.Track != nil || np.Position != 0 {
		t.Fatalf("next at end: %+v", np)
	}
	f.Play(ctx)
	if np = state(); np.State != Playing || np.Track.PID != album[0].PID {
		t.Fatalf("play after stop: %+v", np)
	}

	// Repeat all wraps, repeat one loops.
	f.SetRepeat(ctx, RepeatAll)
	f.Next(ctx)
	f.Next(ctx)
	f.Next(ctx)
	if np = state(); np.Track.PID != album[0].PID || np.Repeat != RepeatAll {
		t.Fatalf("repeat all: %+v", np)
	}
	f.SetRepeat(ctx, RepeatOne)
	clk.add(time.Duration((d0 + 5) * float64(time.Second)))
	if np = state(); np.Track.PID != album[0].PID || !near(np.Position, 5) {
		t.Fatalf("repeat one: %+v at %v", np.Track, np.Position)
	}
	var be *Error
	if err := f.SetRepeat(ctx, "sometimes"); !errors.As(err, &be) || be.Code != 1 {
		t.Fatalf("bad repeat: %v", err)
	}

	f.SetVolume(ctx, 150)
	f.SetShuffle(ctx, true)
	if np = state(); np.Volume != 100 || !np.Shuffle {
		t.Fatalf("volume/shuffle: %+v", np)
	}
}

func TestFakePlayErrors(t *testing.T) {
	f, _ := newTestFake(t)
	ctx := context.Background()
	s := mustSnapshot(t, f)
	var be *Error
	if err := f.PlayTracks(ctx, []string{"0000000000000000"}, nil); !errors.As(err, &be) || be.Code != ErrNotFound {
		t.Fatalf("unknown pid: %v", err)
	}
	if err := f.PlayTracks(ctx, nil, nil); !errors.As(err, &be) || be.Code != 1 {
		t.Fatalf("no pids: %v", err)
	}
	if err := f.PlayPlaylist(ctx, "0000000000000000"); !errors.As(err, &be) || be.Code != ErrNotFound {
		t.Fatalf("unknown playlist: %v", err)
	}

	var sub SnapshotPlaylist
	for _, p := range s.Playlists {
		if p.Kind == "subscription" && p.GlobalID == "" { // not a followed Apple Music playlist
			sub = p
		}
	}
	var outside string
	for _, tr := range s.Tracks {
		if !tr.InLibrary {
			outside = tr.PID
			break
		}
	}
	if err := f.PlayTracks(ctx, []string{outside}, nil); !errors.As(err, &be) || be.Code != ErrNotFound {
		t.Fatalf("non-library track without container: %v", err)
	}
	if err := f.PlayTracks(ctx, []string{outside}, map[string]string{outside: sub.PID}); err != nil {
		t.Fatal(err)
	}
	if np, _ := f.State(ctx); np.Track == nil || np.Track.PID != outside {
		t.Fatalf("playing %+v", np.Track)
	}

	// A folder plays its children; an unavailable track stops at once.
	for _, p := range s.Playlists {
		if p.Kind != "folder" {
			continue
		}
		if err := f.PlayPlaylist(ctx, p.PID); err != nil {
			t.Fatal(err)
		}
		np, _ := f.State(ctx)
		var first string
		for _, c := range s.Playlists {
			if c.ParentPID == p.PID && len(c.Items) > 0 {
				first = c.Items[0]
				break
			}
		}
		if np.Track == nil || np.Track.PID != first {
			t.Fatalf("folder %s: playing %+v, want %s", p.Name, np.Track, first)
		}
		break
	}
	for _, tr := range s.Tracks {
		if !tr.Available && tr.InLibrary {
			f.PlayTracks(ctx, []string{tr.PID}, nil)
			if np, _ := f.State(ctx); np.State != Stopped {
				t.Fatalf("unavailable track: %+v", np)
			}
			break
		}
	}
}

func TestFakeFavorites(t *testing.T) {
	f, _ := newTestFake(t)
	ctx := context.Background()
	np, _ := f.State(ctx)
	pid := np.Track.PID
	drain(f)

	want := !np.Track.Favorited
	if err := f.SetFavorite(ctx, pid, want); err != nil {
		t.Fatal(err)
	}
	if np, _ = f.State(ctx); np.Track.Favorited != want {
		t.Fatal("State doesn't show the favorite")
	}
	pe := playerEvents(drain(f))
	if len(pe) != 1 || pe[0].Track.Favorited != want {
		t.Fatalf("events = %+v", pe)
	}
	f.SetFavorite(ctx, pid, true)
	s := mustSnapshot(t, f)
	for _, p := range s.Playlists {
		if p.Special == "favorites" && !slices.Contains(p.Items, pid) {
			t.Fatal("not in Favorite Songs")
		}
	}
	f.SetDislike(ctx, pid, true)
	if np, _ = f.State(ctx); np.Track.Favorited || !np.Track.Disliked {
		t.Fatalf("dislike: %+v", np.Track)
	}
	var be *Error
	if err := f.SetFavorite(ctx, "nope", true); !errors.As(err, &be) || be.Code != ErrNotFound {
		t.Fatalf("unknown pid: %v", err)
	}
}

func TestFakeEvents(t *testing.T) {
	f, clk := newTestFake(t)
	ctx := context.Background()
	album := albumTracks(t, mustSnapshot(t, f), 2)
	f.PlayTracks(ctx, pidsOf(album), nil)
	if pe := playerEvents(drain(f)); len(pe) != 1 || pe[0].State != Playing {
		t.Fatalf("after play: %+v", pe)
	}
	clk.add(500 * time.Millisecond)
	f.Tick()
	if pe := playerEvents(drain(f)); len(pe) != 0 {
		t.Fatalf("event before a second passed: %+v", pe)
	}
	clk.add(600 * time.Millisecond)
	f.Tick()
	if pe := playerEvents(drain(f)); len(pe) != 1 || pe[0].Position < 1 {
		t.Fatalf("periodic: %+v", pe)
	}
	clk.add(time.Duration(album[0].Duration) * time.Millisecond)
	f.Tick()
	if pe := playerEvents(drain(f)); len(pe) != 1 || pe[0].Track.PID != album[1].PID {
		t.Fatalf("track change: %+v", pe)
	}
	f.Pause(ctx)
	drain(f)
	clk.add(5 * time.Second)
	f.Tick()
	if pe := playerEvents(drain(f)); len(pe) != 0 {
		t.Fatalf("events while paused: %+v", pe)
	}
}

func TestFakeAirPlay(t *testing.T) {
	f, _ := newTestFake(t)
	ctx := context.Background()
	devs, err := f.AirPlayDevices(ctx)
	if err != nil || len(devs) != 4 || !devs[0].Selected || devs[0].Kind != "computer" || devs[3].Available {
		t.Fatalf("devices = %+v, %v", devs, err)
	}
	drain(f)
	if err := f.SelectAirPlay(ctx, []int{41, 29274}); err != nil {
		t.Fatal(err)
	}
	evs := drain(f)
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	ae := evs[0].(AirPlayEvent)
	if !ae.Devices[0].Selected || !ae.Devices[1].Selected || !ae.Devices[1].Active || ae.Devices[2].Selected {
		t.Fatalf("selection = %+v", ae.Devices)
	}
	var be *Error
	if err := f.SelectAirPlay(ctx, []int{30117}); !errors.As(err, &be) {
		t.Fatalf("unavailable device: %v", err)
	}
	if err := f.SelectAirPlay(ctx, []int{1}); !errors.As(err, &be) || be.Code != ErrNotFound {
		t.Fatalf("unknown device: %v", err)
	}
	f.SetAirPlayVolume(ctx, 29274, 72)
	devs, _ = f.AirPlayDevices(ctx)
	if devs[1].Volume != 72 {
		t.Fatalf("volume = %d", devs[1].Volume)
	}
}

func TestFakeArtwork(t *testing.T) {
	f, _ := newTestFake(t)
	ctx := context.Background()
	s := mustSnapshot(t, f)
	var withArt, without *SnapshotTrack
	for i := range s.Tracks {
		tr := &s.Tracks[i]
		if tr.Art && withArt == nil {
			withArt = tr
		}
		if !tr.Art && without == nil {
			without = tr
		}
	}
	a, err := f.Artwork(ctx, withArt.PID, 64)
	if err != nil || a.W != 64 || a.H != 64 {
		t.Fatalf("artwork = %+v, %v", a, err)
	}
	b, _ := os.ReadFile(a.Path)
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("png: %v", err)
	}
	colors := map[[4]uint32]bool{}
	for y := 0; y < 64; y += 4 {
		for x := 0; x < 64; x += 4 {
			r, g, b, a := img.At(x, y).RGBA()
			colors[[4]uint32{r, g, b, a}] = true
		}
	}
	if len(colors) < 3 {
		t.Fatalf("cover has %d colours", len(colors))
	}

	byAlbum, _ := f.Artwork(ctx, withArt.AlbumPID, 64)
	if byAlbum.Path != a.Path {
		t.Fatalf("album and track covers differ: %s vs %s", byAlbum.Path, a.Path)
	}
	if a, _ := f.Artwork(ctx, without.PID, 64); a.Path != "" {
		t.Fatalf("track without art: %+v", a)
	}
	if a, _ := f.Artwork(ctx, "nope", 64); a.Path != "" {
		t.Fatalf("unknown pid: %+v", a)
	}
	var again bytes.Buffer
	png.Encode(&again, renderCover(withArt.AlbumPID, 64))
	var first bytes.Buffer
	png.Encode(&first, img)
	if !bytes.Equal(again.Bytes(), first.Bytes()) {
		t.Fatal("cover not deterministic")
	}
}

var lrcLine = regexp.MustCompile(`^\[(\d\d):(\d\d)\.(\d\d)\](.*)$`)

func TestFakeLRC(t *testing.T) {
	f, _ := newTestFake(t)
	s := mustSnapshot(t, f)
	synced, empty := 0, 0
	for _, tr := range s.Tracks[:400] {
		lrc := f.FakeLRC(tr.PID)
		if instrumental(&tr) {
			if lrc != "" {
				t.Fatalf("instrumental %q has lyrics", tr.Title)
			}
			continue
		}
		if lrc == "" {
			empty++
			continue
		}
		synced++
		last, lines := -1.0, 0
		for _, l := range strings.Split(strings.TrimSpace(lrc), "\n") {
			m := lrcLine.FindStringSubmatch(l)
			if m == nil {
				if !strings.HasPrefix(l, "[ar:") && !strings.HasPrefix(l, "[ti:") && !strings.HasPrefix(l, "[al:") && !strings.HasPrefix(l, "[length:") {
					t.Fatalf("bad line %q", l)
				}
				continue
			}
			mm, _ := strconv.Atoi(m[1])
			ss, _ := strconv.Atoi(m[2])
			cs, _ := strconv.Atoi(m[3])
			at := float64(mm*60+ss) + float64(cs)/100
			if at <= last || at > float64(tr.Duration)/1000 {
				t.Fatalf("%s: line at %v after %v (duration %v)", tr.Title, at, last, tr.Duration)
			}
			last = at
			lines++
		}
		if lines < 4 {
			t.Fatalf("%s: %d lines", tr.Title, lines)
		}
	}
	if synced < 150 || empty < 50 {
		t.Fatalf("synced %d, empty %d", synced, empty)
	}
	if f.FakeLRC("nope") != "" {
		t.Fatal("unknown pid has lyrics")
	}
}

// Most synced demo lyrics are timed word by word, the same way every time:
// "[00:12.00]<00:12.00>Hold <00:12.40>on <00:13.10>".
func TestFakeLRCWords(t *testing.T) {
	f, _ := newTestFake(t)
	s := mustSnapshot(t, f)
	tag := regexp.MustCompile(`<\d\d:\d\d\.\d\d>`)
	worded, synced := 0, 0
	for _, tr := range s.Tracks[:300] {
		lrc := f.FakeLRC(tr.PID)
		if lrc == "" {
			continue
		}
		synced++
		if lrc != f.FakeLRC(tr.PID) {
			t.Fatalf("%s: lyrics change between calls", tr.Title)
		}
		if !tag.MatchString(lrc) {
			continue
		}
		worded++
		for _, l := range strings.Split(lrc, "\n") {
			m := lrcLine.FindStringSubmatch(l)
			if m == nil || m[4] == "" {
				continue
			}
			if !strings.HasPrefix(m[4], "<") || !tag.MatchString(m[4][len(m[4])-10:]) {
				t.Fatalf("%s: line %q should start and end with a word tag", tr.Title, l)
			}
			if got := strings.Join(strings.Fields(tag.ReplaceAllString(m[4], "")), " "); got == "" {
				t.Fatalf("%s: no words in %q", tr.Title, l)
			}
		}
	}
	if worded < synced/2 || worded == synced {
		t.Fatalf("%d of %d synced lyrics timed by word", worded, synced)
	}
}

func TestFakeLyrics(t *testing.T) {
	f, _ := newTestFake(t)
	ctx := context.Background()
	s := mustSnapshot(t, f)
	found := 0
	for _, tr := range s.Tracks {
		text, err := f.Lyrics(ctx, tr.PID)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Kind == "subscription" && text != "" {
			t.Fatalf("subscription track %q has Music lyrics", tr.Title)
		}
		if text != "" {
			found++
		}
	}
	if found == 0 || found > len(s.Tracks)/10 {
		t.Fatalf("%d tracks with plain lyrics", found)
	}
}

func TestFakeCloseAndContext(t *testing.T) {
	f := NewFake(3)
	f.artDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.Play(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx: %v", err)
	}
	if err := f.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	for range f.Events() {
	}
	if _, err := f.State(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkNewFake(b *testing.B) {
	for b.Loop() {
		newFake(1, time.Now)
	}
}

func BenchmarkFakeSnapshot(b *testing.B) {
	f := NewFakeWithClock(1, time.Now)
	ctx := context.Background()
	for b.Loop() {
		f.Snapshot(ctx)
	}
}

func BenchmarkRenderCover600(b *testing.B) {
	for b.Loop() {
		renderCover("ABCDEF0123456789", 600)
	}
}

func TestFakePlayStore(t *testing.T) {
	f, clk := newTestFake(t)
	ctx := context.Background()
	f.RegisterCatalog(FakeCatalogSong{StoreID: "1440942220", Name: "Waltz for Debby (Take 2)", Artist: "Bill Evans Trio", Album: "Waltz for Debby", Duration: 419})

	if err := f.PlayStore(ctx, []string{"1440942209", "1440942220", "1440942495"}, 1); err != nil {
		t.Fatal(err)
	}
	np, _ := f.State(ctx)
	if np.State != Playing || np.Track == nil {
		t.Fatalf("state %+v", np)
	}
	first := np.Track
	if first.Name != "Waltz for Debby (Take 2)" || first.Artist != "Bill Evans Trio" || first.AlbumArtist != "Bill Evans Trio" || first.Duration != 419 {
		t.Fatalf("registered song reported as %+v", first)
	}
	snap, _ := f.Snapshot(ctx)
	if slices.ContainsFunc(snap.Tracks, func(s SnapshotTrack) bool { return s.PID == first.PID }) {
		t.Fatal("catalog song's temporary pid is in the library snapshot")
	}

	// Unregistered ids get made-up names; Next walks the tracklist.
	if err := f.Next(ctx); err != nil {
		t.Fatal(err)
	}
	np, _ = f.State(ctx)
	if np.Track == nil || np.Track.Name != "Catalog song 1440942495" || np.Track.PID == first.PID ||
		np.Track.Duration < 150 || np.Track.Duration >= 300 {
		t.Fatalf("second song %+v", np.Track)
	}
	clk.add(time.Duration(np.Track.Duration+1) * time.Second)
	if np, _ = f.State(ctx); np.State != Stopped || np.Track != nil {
		t.Fatalf("after the tracklist: %+v", np)
	}

	// Played again, the same song gets a fresh temporary pid, as in Music.
	if err := f.PlayStore(ctx, []string{"1440942220"}, 0); err != nil {
		t.Fatal(err)
	}
	if np, _ = f.State(ctx); np.Track == nil || np.Track.PID == first.PID || np.Track.Name != first.Name {
		t.Fatalf("replay %+v", np.Track)
	}

	var be *Error
	for _, tc := range []struct {
		ids   []string
		start int
	}{{nil, 0}, {[]string{"1"}, 1}, {[]string{"1"}, -1}, {[]string{"12x"}, 0}} {
		if err := f.PlayStore(ctx, tc.ids, tc.start); !errors.As(err, &be) || be.Code != 1 {
			t.Errorf("PlayStore(%v, %d) = %v", tc.ids, tc.start, err)
		}
	}
	// A library play afterwards works as before.
	pid := snap.Tracks[0].PID
	if err := f.PlayTracks(ctx, []string{pid}, nil); err != nil {
		t.Fatal(err)
	}
	if np, _ = f.State(ctx); np.Track == nil || np.Track.PID != pid {
		t.Fatalf("library play %+v", np.Track)
	}
}

func TestFakeDropAirPlayRoute(t *testing.T) {
	f, _ := newTestFake(t)
	ctx := context.Background()
	if err := f.Play(ctx); err != nil {
		t.Fatal(err)
	}
	drain(f)
	if f.DropAirPlayRoute(true) {
		t.Fatal("reconnected while this computer is selected")
	}
	if err := f.SelectAirPlay(ctx, []int{29274, 29301}); err != nil {
		t.Fatal(err)
	}
	drain(f)
	if !f.DropAirPlayRoute(false) {
		t.Fatal("no reconnect on a dropped route")
	}
	var got []AirPlayRepairedEvent
	for _, ev := range drain(f) {
		if re, ok := ev.(AirPlayRepairedEvent); ok {
			got = append(got, re)
		}
	}
	if len(got) != 1 || got[0].OK || !slices.Equal(got[0].Devices, []string{"Living Room", "Kitchen"}) {
		t.Fatalf("repaired events = %+v", got)
	}
	f.SetAutoReconnect(false)
	if f.DropAirPlayRoute(true) {
		t.Fatal("reconnected with auto-reconnect off")
	}
	f.SetAutoReconnect(true)
	if err := f.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if f.DropAirPlayRoute(true) {
		t.Fatal("reconnected while paused")
	}
}

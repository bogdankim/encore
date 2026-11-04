package ui

import (
	"context"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

func pickerLib() (*library.Library, []*library.Track, *library.Playlist, *library.Playlist) {
	a := &library.Track{PID: "A", Title: "Alpha", Artist: "X", Duration: time.Minute}
	b := &library.Track{PID: "B", Title: "Beta", Artist: "X", Duration: time.Minute}
	folder := &library.Playlist{PID: "F", Name: "Moods", Kind: "folder"}
	focus := &library.Playlist{PID: "P1", Name: "Deep Focus", Kind: "user", Parent: folder, Tracks: []*library.Track{a}}
	gym := &library.Playlist{PID: "P2", Name: "Gym", Kind: "user"}
	smart := &library.Playlist{PID: "P3", Name: "Top 25", Kind: "smart"}
	sub := &library.Playlist{PID: "P4", Name: "Essentials", Kind: "subscription"}
	music := &library.Playlist{PID: "P5", Name: "Music", Kind: "user", Special: "music"}
	lib := &library.Library{PlaylistsFlat: []*library.Playlist{folder, focus, gym, smart, sub, music}}
	return lib, []*library.Track{a, b}, focus, gym
}

func pickerNames(p *PlaylistPicker) []string {
	var out []string
	for _, r := range p.rows {
		if r.pl == nil {
			out = append(out, "<new>")
		} else {
			out = append(out, r.pl.Name)
		}
	}
	return out
}

func TestPickerListsOnlyEditablePlaylists(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib, _, _, _ = pickerLib()
	p := NewPlaylistPicker(nil, "").(*PlaylistPicker)
	p.build(c)
	if got, want := pickerNames(p), []string{"Deep Focus", "Gym"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestPickerPutsRecentFirst(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib, _, _, _ = pickerLib()
	p := NewPlaylistPicker(nil, "P2").(*PlaylistPicker)
	p.build(c)
	if got := pickerNames(p)[0]; got != "Gym" {
		t.Fatalf("first row = %q", got)
	}
}

func TestPickerMarksPlaylistsThatHaveTheSongs(t *testing.T) {
	c, _ := ovTestCtx(t)
	lib, ts, _, _ := pickerLib()
	c.Lib = lib
	p := NewPlaylistPicker(ts[:1], "").(*PlaylistPicker)
	p.build(c)
	if !p.rows[0].has || p.rows[1].has {
		t.Fatalf("has = %v, %v; want true, false", p.rows[0].has, p.rows[1].has)
	}
	all := NewPlaylistPicker(ts, "").(*PlaylistPicker) // one of the two is missing
	all.build(c)
	if all.rows[0].has {
		t.Fatal("a playlist holding one of two songs is marked as having them")
	}
}

func TestPickerOffersNewPlaylistUnlessNameExists(t *testing.T) {
	c, _ := ovTestCtx(t)
	c.Lib, _, _, _ = pickerLib()
	p := NewPlaylistPicker(nil, "").(*PlaylistPicker)
	p.build(c)
	ovType(p, c, "road")
	if got, want := pickerNames(p), []string{"<new>"}; !slices.Equal(got, want) {
		t.Fatalf("rows for a new name = %v, want %v", got, want)
	}
	q := NewPlaylistPicker(nil, "").(*PlaylistPicker)
	q.build(c)
	ovType(q, c, "gym")
	if got, want := pickerNames(q), []string{"Gym"}; !slices.Equal(got, want) {
		t.Fatalf("rows for an existing name = %v, want %v", got, want)
	}
}

func TestPickerEnterAdds(t *testing.T) {
	c, h := ovTestCtx(t)
	lib, ts, _, _ := pickerLib()
	c.Lib = lib
	p := NewPlaylistPicker(ts, "P2").(*PlaylistPicker)
	p.Action(keymap.InputSubmit, 1, c)
	ovSettle(p, c, nil)
	if !slices.Contains(h.calls, "AddToPlaylist Gym 2") {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestPickerEnterCreates(t *testing.T) {
	c, h := ovTestCtx(t)
	lib, ts, _, _ := pickerLib()
	c.Lib = lib
	p := NewPlaylistPicker(ts, "").(*PlaylistPicker)
	p.build(c)
	ovType(p, c, "Road Trip")
	p.Action(keymap.InputSubmit, 1, c)
	if !slices.Contains(h.calls, "AddToNewPlaylist Road Trip 2") {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestPickerRendersExactSize(t *testing.T) {
	c, _ := ovTestCtx(t)
	tracks := c.Lib.RecentlyAdded[0].Tracks
	for _, q := range []string{"", "a", "zzqqxxjj"} {
		for _, s := range ovScreens {
			p := NewPlaylistPicker(tracks, "")
			if q != "" {
				ovType(p, c, q)
			}
			ovCheckSize(t, "picker "+q, p, c, s[0], s[1])
		}
	}
}

func TestMenusOfferAddToPlaylist(t *testing.T) {
	c, h := ovTestCtx(t)
	m := NewMenu(5, 5, c.Lib.RecentlyAdded[0].Tracks[:1]).(*MenuOverlay)
	m.Init(c)
	handled, _ := m.Action(keymap.PlaylistAdd, 1, c)
	if !handled || !slices.Contains(h.calls, "PickPlaylist 1") {
		t.Fatalf("handled = %v, calls = %v", handled, h.calls)
	}
}

func TestPlaylistMissingSkipsHeldAndRepeated(t *testing.T) {
	_, ts, focus, _ := pickerLib()
	got := playlistMissing(focus, []*library.Track{ts[0], ts[1], ts[1]})
	if len(got) != 1 || got[0].PID != "B" {
		t.Fatalf("got %v", got)
	}
}

func TestPlaylistAppendCountsDuration(t *testing.T) {
	_, ts, focus, _ := pickerLib()
	folder := focus.Parent
	before := len(focus.Tracks)
	focus.Append(ts[1])
	if len(focus.Tracks) != before+1 || focus.Duration != time.Minute || folder.Duration != time.Minute {
		t.Fatalf("tracks %d, duration %v, folder %v", len(focus.Tracks), focus.Duration, folder.Duration)
	}
}

func TestReapplyKeepsAddsUntilSnapshotHasThem(t *testing.T) {
	build := func(items ...string) *library.Library {
		return library.Build(&bridge.Snapshot{
			Tracks:    []bridge.SnapshotTrack{{PID: "A", Title: "Alpha", InLibrary: true}, {PID: "B", Title: "Beta", InLibrary: true}},
			Playlists: []bridge.SnapshotPlaylist{{PID: "P", Name: "Gym", Kind: "user", Items: items}},
		})
	}
	a := &App{plAdded: []playlistAdd{{"P", "B", time.Now()}, {"NEW", "A", time.Now()}}}
	stale := build("A")
	a.reapplyPlaylistAdds(stale)
	if got := stale.Playlist("P").Tracks; len(got) != 2 || got[1].PID != "B" {
		t.Fatalf("a stale snapshot lost the song: %d tracks", len(got))
	}
	if len(a.plAdded) != 2 {
		t.Fatalf("pending %d, want 2 (one for a playlist no snapshot has yet)", len(a.plAdded))
	}
	fresh := build("A", "B")
	a.reapplyPlaylistAdds(fresh)
	if got := fresh.Playlist("P").Tracks; len(got) != 2 {
		t.Fatalf("a fresh snapshot got the song twice: %d tracks", len(got))
	}
	if len(a.plAdded) != 1 || a.plAdded[0].pl != "NEW" {
		t.Fatalf("pending = %v", a.plAdded)
	}
	a.plAdded[0].at = time.Now().Add(-playlistAddTTL - time.Second)
	a.reapplyPlaylistAdds(fresh)
	if len(a.plAdded) != 0 {
		t.Fatal("an old add never expired")
	}
}

// newPlaylistApp is a real App on a Fake whose library is built from the Fake.
func newPlaylistApp(t *testing.T) (*App, *bridge.Fake) {
	t.Helper()
	f := bridge.NewFakeWithClock(1, time.Now)
	snap, err := f.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lib := library.Build(snap)
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	a := New(Options{
		Client: f, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th },
	})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 42})
	return a, f
}

// userPlaylist is an editable playlist with songs, and a song not on it.
func userPlaylist(t *testing.T, lib *library.Library) (*library.Playlist, *library.Track) {
	t.Helper()
	for _, p := range lib.PlaylistsFlat {
		if !playlistEditable(p) || len(p.Tracks) == 0 {
			continue
		}
		for _, s := range lib.Songs {
			if !slices.Contains(p.Tracks, s) {
				return p, s
			}
		}
	}
	t.Fatal("no editable playlist")
	return nil, nil
}

func fakeItems(t *testing.T, f *bridge.Fake, pid string) []string {
	t.Helper()
	snap, err := f.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range snap.Playlists {
		if p.PID == pid {
			return p.Items
		}
	}
	return nil
}

func TestAddToPlaylistReachesMusicAndShowsAtOnce(t *testing.T) {
	a, f := newPlaylistApp(t)
	pl, song := userPlaylist(t, a.lib)
	before := len(pl.Tracks)
	var refreshed int
	a.o.RefreshLibraryNow = func() { refreshed++ }

	a.Update(a.AddToPlaylist(pl, []*library.Track{song})())

	if items := fakeItems(t, f, pl.PID); !slices.Contains(items, song.PID) {
		t.Fatalf("Music's playlist lacks the song: %d items", len(items))
	}
	if len(pl.Tracks) != before+1 || pl.Tracks[before] != song {
		t.Fatalf("library playlist has %d tracks, want %d with the song last", len(pl.Tracks), before+1)
	}
	if want := "Added " + song.Title + " to " + pl.Name; a.status.text != want {
		t.Fatalf("status %q, want %q", a.status.text, want)
	}
	if refreshed != 1 || a.plRecent != pl.PID {
		t.Fatalf("refreshed %d, recent %q", refreshed, a.plRecent)
	}

	// adding it again changes nothing and says so
	n := len(fakeItems(t, f, pl.PID))
	if cmd := a.AddToPlaylist(pl, []*library.Track{song}); cmd != nil {
		cmd()
	}
	if got := len(fakeItems(t, f, pl.PID)); got != n {
		t.Fatalf("the song went in twice: %d items, was %d", got, n)
	}
	if want := "Already in " + pl.Name; a.status.text != want {
		t.Fatalf("status %q, want %q", a.status.text, want)
	}
}

func TestAddToPlaylistCountsSkippedAndBatches(t *testing.T) {
	a, f := newPlaylistApp(t)
	pl, _ := userPlaylist(t, a.lib)
	var fresh []*library.Track
	for _, s := range a.lib.Songs {
		if !slices.Contains(pl.Tracks, s) {
			fresh = append(fresh, s)
		}
		if len(fresh) == playlistBatch+3 {
			break
		}
	}
	if len(fresh) != playlistBatch+3 {
		t.Skip("library too small")
	}
	before := len(pl.Tracks)
	a.Update(a.AddToPlaylist(pl, append([]*library.Track{pl.Tracks[0]}, fresh...))())
	if got := len(fakeItems(t, f, pl.PID)); got != before+len(fresh) {
		t.Fatalf("%d items, want %d", got, before+len(fresh))
	}
	want := "Added " + plural(len(fresh), "song", "songs") + " to " + pl.Name + ", 1 already there"
	if a.status.text != want {
		t.Fatalf("status %q, want %q", a.status.text, want)
	}
}

func TestAddToNewPlaylistSurvivesSnapshots(t *testing.T) {
	a, f := newPlaylistApp(t)
	song := a.lib.Songs[0]
	msg := a.AddToNewPlaylist("Road Trip", []*library.Track{song})().(playlistAddedMsg)
	a.Update(msg)
	if msg.err != nil || msg.pid == "" {
		t.Fatalf("msg %+v", msg)
	}
	if got := fakeItems(t, f, msg.pid); !slices.Equal(got, []string{song.PID}) {
		t.Fatalf("items %v", got)
	}
	snap, _ := f.Snapshot(context.Background())
	a.Update(LibraryMsg{Lib: library.Build(snap)})
	if p := a.lib.Playlist(msg.pid); p == nil || p.Name != "Road Trip" || len(p.Tracks) != 1 {
		t.Fatalf("new playlist after a snapshot: %+v", p)
	}
	if a.plRecent != msg.pid || len(a.plAdded) != 0 {
		t.Fatalf("recent %q, pending %v", a.plRecent, a.plAdded)
	}
}

func TestAddToPlaylistReportsMusicRefusing(t *testing.T) {
	a, _ := newPlaylistApp(t)
	var smart *library.Playlist
	for _, p := range a.lib.PlaylistsFlat {
		if p.Kind == "smart" {
			smart = p
			break
		}
	}
	song := a.lib.Songs[0]
	before := len(smart.Tracks)
	a.Update(a.AddToPlaylist(smart, []*library.Track{song})())
	if !a.status.err || len(smart.Tracks) != before || len(a.plAdded) != 0 {
		t.Fatalf("status %q err %v, tracks %d", a.status.text, a.status.err, len(smart.Tracks))
	}
}

func TestPickPlaylistSwapsCatalogSongsForLibraryCopies(t *testing.T) {
	a, _ := newPlaylistApp(t)
	in := a.lib.Songs[0]
	stand := &library.Track{PID: player.StorePID("99999999"), Title: in.Title, Artist: in.Artist}
	gone := &library.Track{PID: player.StorePID("88888888"), Title: "No Such Song", Artist: "Nobody"}
	got := libraryTracksOf(a.lib, []*library.Track{stand, gone})
	if len(got) != 1 || got[0] != in {
		t.Fatalf("got %v", got)
	}
	a.PickPlaylist([]*library.Track{gone})
	if a.status.text != catalogListM {
		t.Fatalf("status %q", a.status.text)
	}
}

package player

import (
	"slices"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

var carSeat = Station{ID: "ra.725086732", Name: "Car Seat Headrest & Similar Artists", Kind: StationArtist}

// stationSong reports Music playing a station song under raw, dur seconds
// long (0 for live radio).
func (r *rig) stationSong(raw string, pos, dur float64) {
	np := r.nowPlaying("", bridge.Playing, pos)
	np.Track = &bridge.CurrentTrack{PID: raw, Name: "Song " + raw, Artist: "Station Artist", Duration: dur}
	r.p.Observe(bridge.PlayerEvent{NowPlaying: np})
}

// startStation plays st and confirms it with the station song S1.
func (r *rig) startStation(st Station) {
	r.t.Helper()
	r.p.PlayStation(Source{}, st)
	if c := onlyCall(r.t, r.calls(), "playStation"); c.pid != st.ID {
		r.t.Fatalf("playStation %q, want %q", c.pid, st.ID)
	}
	dur := 200.0
	if st.Kind == StationLive {
		dur = 0
	}
	r.stationSong("S1", 0, dur)
	if r.p.State().Loading {
		r.t.Fatal("station never confirmed")
	}
}

func TestStationIDs(t *testing.T) {
	if got := ArtistStationID(725086732); got != "ra.725086732" {
		t.Fatalf("ArtistStationID = %q", got)
	}
	if got := ArtistOnlyStationID(725086732); got != "ra.a-725086732" {
		t.Fatalf("ArtistOnlyStationID = %q", got)
	}
	for id, ok := range map[string]bool{
		"ra.978194965": true, "ra.u-17b7c0ffee": true, "ra.a-725086732": true,
		"": false, "ra.": false, "pl.pm-1234": false, "ra.x y": false, "1440942209": false,
	} {
		if ValidStationID(id) != ok {
			t.Errorf("ValidStationID(%q) = %v", id, !ok)
		}
	}
}

func TestPlayStation(t *testing.T) {
	r := newRig(t)
	ts := r.tracks(3)
	r.p.PlayContext(Source{Kind: KindAlbum, Title: "A"}, ts, 0)
	r.played(ts[0].PID)
	r.calls()

	r.p.PlayStation(Source{}, carSeat)
	if c := onlyCall(t, r.calls(), "playStation"); c.pid != carSeat.ID {
		t.Fatalf("playStation %q", c.pid)
	}
	s := r.p.State()
	if !s.Loading || s.Station == nil || *s.Station != carSeat || s.NowPlaying.Track != nil || s.Upcoming != 0 {
		t.Fatalf("loading=%v station=%v track=%v upcoming=%d", s.Loading, s.Station, s.NowPlaying.Track, s.Upcoming)
	}
	if want := (Source{Kind: KindStation, ID: carSeat.ID, Title: carSeat.Name}); s.Source != want {
		t.Fatalf("source %+v", s.Source)
	}
	if s.Duration != 0 {
		t.Fatalf("duration %v while loading", s.Duration)
	}

	// Music still plays the album for a second or two: not the station yet.
	r.playing(ts[0].PID, 37)
	r.clock.Advance(deadGrace)
	if !r.p.State().Loading {
		t.Fatal("old track confirmed the station")
	}
	r.stationSong("S1", 0, 241)
	s = r.p.State()
	if s.Loading || s.Track != nil || s.Catalog != nil || s.Station == nil || s.Live {
		t.Fatalf("after start: loading=%v track=%v catalog=%v station=%v live=%v", s.Loading, s.Track, s.Catalog, s.Station, s.Live)
	}
	if s.NowPlaying.Track == nil || s.NowPlaying.Track.PID != "S1" || s.Duration != 241*time.Second {
		t.Fatalf("now playing %+v duration %v", s.NowPlaying.Track, s.Duration)
	}
	r.clock.Advance(storePendingTimeout)
	noCalls(t, r.calls())

	// Music moves on by itself: followed, nothing issued.
	r.stationSong("S2", 0, 180)
	noCalls(t, r.calls())
	if s := r.p.State(); s.Station == nil || s.NowPlaying.Track.PID != "S2" || s.Source.Kind != KindStation {
		t.Fatalf("after Music's next: station=%v track=%+v", s.Station, s.NowPlaying.Track)
	}

	// Next and previous are Music's.
	r.clock.Advance(30 * time.Second)
	r.p.Next()
	onlyCall(t, r.calls(), "next")
	if pos := r.p.Position(); pos != 0 {
		t.Fatalf("position %v after next", pos)
	}
	r.stationSong("S3", 0, 200)
	r.p.Previous()
	onlyCall(t, r.calls(), "previous")
	r.stationSong("S3", 0, 200) // Music only restarted it
	noCalls(t, r.calls())
	if s := r.p.State(); s.Station == nil {
		t.Fatal("station lost")
	}

	// Playing something else ends the station.
	r.p.PlayContext(Source{Kind: KindAlbum}, ts, 1)
	onlyCall(t, r.calls(), "playTracks")
	if s := r.p.State(); s.Station != nil || s.Source.Kind != KindAlbum {
		t.Fatalf("station %v source %v", s.Station, s.Source)
	}
}

func TestPlayStationBadID(t *testing.T) {
	r := newRig(t)
	r.p.PlayStation(Source{}, Station{ID: "pl.pm-1234", Name: "Your Essentials"})
	noCalls(t, r.calls())
	if s := r.p.State(); s.LastError != MsgBadStation || s.Station != nil || s.Loading {
		t.Fatalf("error %q station %v loading %v", s.LastError, s.Station, s.Loading)
	}
}

func TestLiveStation(t *testing.T) {
	r := newRig(t)
	r.startStation(Station{ID: "ra.978194965", Name: "Apple Music 1", Kind: StationLive})
	r.clock.Advance(42 * time.Second)
	r.stationSong("S1", 0, 0) // live: Music keeps reporting 0
	r.clock.Advance(800 * time.Millisecond)
	s := r.p.State()
	if !s.Live || s.Position != 0 || s.Duration != 0 || s.NowPlaying.Position != 0 {
		t.Fatalf("live=%v position=%v duration=%v", s.Live, s.Position, s.Duration)
	}
	r.p.SeekRelative(10 * time.Second)
	noCalls(t, r.calls())

	// A station not called live that reports no duration is live too.
	r2 := newRig(t)
	r2.startStation(carSeat)
	r2.stationSong("L1", 0, 0)
	r2.clock.Advance(5 * time.Second)
	if s := r2.p.State(); !s.Live || s.Position != 0 {
		t.Fatalf("zero-duration station song: live=%v position=%v", s.Live, s.Position)
	}
}

func TestQueueDuringStation(t *testing.T) {
	r := newRig(t)
	q := r.tracks(2)
	r.startStation(carSeat)

	r.p.AddToQueue(q...)
	if s := r.p.State(); s.Upcoming != 2 || s.Queued != 2 {
		t.Fatalf("upcoming=%d queued=%d", s.Upcoming, s.Queued)
	}
	noCalls(t, r.calls())

	// At the end of the station's song the queue plays.
	r.stationSong("S2", 0, 200)
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf(q)) {
		t.Fatalf("playTracks %v", c.pids)
	}
	r.playing(q[0].PID, 0)
	s := r.p.State()
	if s.Track != q[0] || s.Station == nil || s.Live || s.Upcoming != 1 {
		t.Fatalf("track=%v station=%v upcoming=%d", s.Track, s.Station, s.Upcoming)
	}
	r.playing(q[1].PID, 0)
	noCalls(t, r.calls())

	// When the queue runs out the station starts again.
	r.stoppedEv()
	onlyCall(t, r.calls(), "playStation")
	r.stationSong("S9", 0, 200)
	if s := r.p.State(); s.Loading || s.Station == nil || s.Track != nil {
		t.Fatalf("after the queue: loading=%v station=%v track=%v", s.Loading, s.Station, s.Track)
	}
}

func TestNextDuringStationPlaysQueue(t *testing.T) {
	r := newRig(t)
	q := r.tracks(1)
	r.startStation(carSeat)
	r.p.PlayNext(q[0])
	r.p.Next()
	if c := onlyCall(t, r.calls(), "playTracks"); !slices.Equal(c.pids, pidsOf(q)) {
		t.Fatalf("playTracks %v", c.pids)
	}
	r.played(q[0].PID)
	// Nothing queued after it: next goes back to the station.
	r.p.Next()
	onlyCall(t, r.calls(), "playStation")
}

func TestStationFailures(t *testing.T) {
	r := newRig(t)
	r.c.failWith("playStation", &bridge.Error{Code: bridge.ErrStore, Message: "Music didn't take the station (CommandIgnored)"})
	r.p.PlayStation(Source{}, carSeat)
	r.flush()
	if s := r.p.State(); s.Loading || s.Station != nil || s.LastError != "Music didn't take the station (CommandIgnored)" {
		t.Fatalf("loading=%v station=%v error=%q", s.Loading, s.Station, s.LastError)
	}

	// Accepted, but Music stays stopped.
	r2 := newRig(t)
	r2.p.PlayStation(Source{}, carSeat)
	r2.flush()
	r2.stoppedEv()
	r2.clock.Advance(storeDeadGrace)
	if s := r2.p.State(); s.Loading || s.Station != nil || s.LastError != MsgStationFailed {
		t.Fatalf("loading=%v station=%v error=%q", s.Loading, s.Station, s.LastError)
	}
	noCalls(t, r2.calls()[1:])
}

func TestToggleRestartsStoppedStation(t *testing.T) {
	r := newRig(t)
	r.startStation(carSeat)
	r.p.Observe(bridge.MusicEvent{Running: false})
	r.p.Toggle()
	onlyCall(t, r.calls(), "playStation")
	if !r.p.State().Loading {
		t.Fatal("not loading after restart")
	}
}

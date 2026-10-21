package lastfm

import (
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

// sim drives a tracker the way the app does: a snapshot every step.
type sim struct {
	t      *testing.T
	tr     tracker
	now    time.Time
	np     []*Track // now-playing events
	scrobs []Track
	track  *bridge.CurrentTrack
	pos    float64
	state  bridge.PlayerState
}

func newSim(t *testing.T, dur float64) *sim {
	return &sim{
		t:     t,
		now:   time.Unix(1_760_000_000, 0),
		track: &bridge.CurrentTrack{PID: "P1", Name: "Song", Artist: "Artist", Album: "Album", Duration: dur},
		state: bridge.Playing,
	}
}

func (s *sim) observe() {
	ev := s.tr.observe(bridge.NowPlaying{State: s.state, Position: s.pos, Track: s.track}, TrackMeta{}, s.now)
	if ev.nowPlaying != nil {
		s.np = append(s.np, ev.nowPlaying)
	}
	s.scrobs = append(s.scrobs, ev.scrobbles...)
}

// play advances wall time and position together, observing every second.
func (s *sim) play(d time.Duration) {
	for i := time.Duration(0); i < d; i += time.Second {
		s.now = s.now.Add(time.Second)
		if s.state == bridge.Playing {
			s.pos += 1
		}
		s.observe()
	}
}

func TestPlayBasic(t *testing.T) {
	s := newSim(t, 200)
	start := s.now
	s.observe()
	if len(s.np) != 1 || s.np[0].Timestamp != start.Unix() || s.np[0].Duration != 200 {
		t.Fatalf("now playing at start: %+v", s.np)
	}
	s.play(99 * time.Second)
	if len(s.scrobs) != 0 {
		t.Fatal("scrobbled before half")
	}
	s.play(time.Second)
	if len(s.scrobs) != 1 || s.scrobs[0].Timestamp != start.Unix() || s.scrobs[0].Track != "Song" {
		t.Fatalf("want one scrobble at 100 s: %+v", s.scrobs)
	}
	s.play(100 * time.Second)
	if len(s.scrobs) != 1 {
		t.Fatal("scrobbled twice")
	}
}

func TestPlayFourMinuteRule(t *testing.T) {
	s := newSim(t, 900)
	s.observe()
	s.play(239 * time.Second)
	if len(s.scrobs) != 0 {
		t.Fatal("eligible before 4 min")
	}
	s.play(time.Second)
	if len(s.scrobs) != 1 {
		t.Fatal("should be eligible at 4 min")
	}
}

func TestPlayShortTrackNeverScrobbles(t *testing.T) {
	s := newSim(t, 30)
	s.observe()
	s.play(30 * time.Second)
	if len(s.scrobs) != 0 {
		t.Fatal("30 s track scrobbled")
	}
}

func TestPlayPauseNotCounted(t *testing.T) {
	s := newSim(t, 200)
	s.observe()
	s.play(60 * time.Second)
	s.state = bridge.Paused
	s.play(10 * time.Minute) // paused: wall time passes, nothing credited
	s.state = bridge.Playing
	s.play(39 * time.Second)
	if len(s.scrobs) != 0 {
		t.Fatal("pause time was credited")
	}
	if len(s.np) != 2 {
		t.Fatalf("resume after > 30 s pause should resend now playing, got %d", len(s.np))
	}
	s.play(time.Second)
	if len(s.scrobs) != 1 {
		t.Fatalf("want scrobble after 100 s of playing, got %d", len(s.scrobs))
	}
}

func TestPlayShortPauseKeepsNowPlaying(t *testing.T) {
	s := newSim(t, 200)
	s.observe()
	s.play(10 * time.Second)
	s.state = bridge.Paused
	s.play(20 * time.Second)
	s.state = bridge.Playing
	s.play(5 * time.Second)
	if len(s.np) != 1 {
		t.Fatalf("short pause resent now playing: %d", len(s.np))
	}
}

func TestPlaySeekForwardNotCredited(t *testing.T) {
	s := newSim(t, 300)
	s.observe()
	s.play(20 * time.Second)
	s.pos = 280 // jump near the end
	s.play(10 * time.Second)
	if len(s.scrobs) != 0 {
		t.Fatal("seek forward credited the skipped part")
	}
}

func TestPlayRepeatOne(t *testing.T) {
	s := newSim(t, 120)
	start := s.now
	s.observe()
	s.play(120 * time.Second)
	// Repeat one: same pid, position back near 0.
	s.pos = 0
	s.now = s.now.Add(500 * time.Millisecond)
	s.observe()
	second := s.now
	s.play(70 * time.Second)
	if len(s.scrobs) != 2 {
		t.Fatalf("want two scrobbles, got %+v", s.scrobs)
	}
	if s.scrobs[0].Timestamp != start.Unix() || s.scrobs[1].Timestamp != second.Unix() {
		t.Errorf("timestamps %d %d, want %d %d", s.scrobs[0].Timestamp, s.scrobs[1].Timestamp, start.Unix(), second.Unix())
	}
	if len(s.np) != 2 {
		t.Errorf("each play should send now playing once, got %d", len(s.np))
	}
}

func TestPlaySeekBackBeforeThresholdIsSamePlay(t *testing.T) {
	s := newSim(t, 200)
	s.observe()
	s.play(40 * time.Second)
	s.pos = 0
	s.play(60 * time.Second)
	if len(s.scrobs) != 1 || len(s.np) != 1 {
		t.Fatalf("restart before the threshold should continue the play: %d scrobbles, %d now playing", len(s.scrobs), len(s.np))
	}
}

func TestPlaySleepGapIgnored(t *testing.T) {
	s := newSim(t, 400)
	s.observe()
	s.play(60 * time.Second)
	// Laptop sleeps for an hour while "playing"; the next snapshot arrives late.
	s.now = s.now.Add(time.Hour)
	s.pos += 5
	s.observe()
	s.play(130 * time.Second)
	if len(s.scrobs) != 0 {
		t.Fatal("sleep gap was credited")
	}
	s.play(10 * time.Second)
	if len(s.scrobs) != 1 {
		t.Fatal("want scrobble after 200 s of real playing")
	}
}

func TestPlayTrackChangeCreditsTail(t *testing.T) {
	s := newSim(t, 100)
	s.observe()
	s.play(49 * time.Second)
	// Next track starts; the last second of the old one still counts.
	s.track = &bridge.CurrentTrack{PID: "P2", Name: "Next", Artist: "Artist", Duration: 200}
	s.pos = 0
	s.now = s.now.Add(time.Second)
	s.observe()
	if len(s.scrobs) != 1 || s.scrobs[0].Track != "Song" {
		t.Fatalf("want the first track scrobbled on change, got %+v", s.scrobs)
	}
	if len(s.np) != 2 || s.np[1].Track != "Next" {
		t.Fatalf("want now playing for the new track: %+v", s.np)
	}
}

func TestPlayInterpolatesPosition(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	np := bridge.NowPlaying{State: bridge.Playing, Position: 10, At: now.UnixMilli(), Track: &bridge.CurrentTrack{Duration: 60}}
	if got := position(np, now.Add(5*time.Second)); got != 15*time.Second {
		t.Errorf("position = %v", got)
	}
	if got := position(np, now.Add(time.Hour)); got != 60*time.Second {
		t.Errorf("clamped position = %v", got)
	}
	np.State = bridge.Paused
	if got := position(np, now.Add(5*time.Second)); got != 10*time.Second {
		t.Errorf("paused position = %v", got)
	}
}

func TestPlayMetaOverrides(t *testing.T) {
	var tr tracker
	ev := tr.observe(bridge.NowPlaying{State: bridge.Playing, Track: &bridge.CurrentTrack{PID: "X", Name: "n", Artist: "a", Duration: 100}},
		TrackMeta{Title: "Better Title", AlbumArtist: "AA", TrackNumber: 3, Duration: 101 * time.Second}, time.Unix(1_760_000_000, 0))
	np := ev.nowPlaying
	if np == nil || np.Track != "Better Title" || np.Artist != "a" || np.AlbumArtist != "AA" || np.TrackNumber != 3 || np.Duration != 101 {
		t.Fatalf("%+v", np)
	}
}

package lastfm

import (
	"math"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

// TrackMeta is library metadata that the player snapshot lacks or that the
// library knows better. Empty fields fall back to bridge.CurrentTrack.
type TrackMeta struct {
	Artist      string
	Title       string
	Album       string
	AlbumArtist string
	Duration    time.Duration
	TrackNumber int
}

const (
	// maxGap: an interval between observations longer than this (sleep, a
	// stalled bridge) isn't credited as playing time.
	maxGap = 10 * time.Second
	// resumeNowPlaying: resuming after a pause this long re-sends now playing.
	resumeNowPlaying = 30 * time.Second
	// restartWindow: a jump back to within this of the start, on a play that
	// already reached the threshold, starts a new play (repeat one, restart).
	restartWindow = 10 * time.Second
	minDuration   = 30 * time.Second
	maxThreshold  = 4 * time.Minute
)

// play is one listening instance of a track.
type play struct {
	pid      string
	track    Track
	duration time.Duration

	started   time.Time     // first playing observation minus position; zero until then
	played    time.Duration // credited playing time
	scrobbled bool
	pausedAt  time.Time

	lastObs time.Time
	lastPos time.Duration
	playing bool
}

func (p *play) threshold() time.Duration {
	if p.duration <= 0 {
		return maxThreshold // unknown length: only the 4 minute rule applies
	}
	return min(p.duration/2, maxThreshold)
}

func (p *play) reached() bool { return p.played >= p.threshold() }

func (p *play) eligible() bool {
	if p.scrobbled || (p.duration > 0 && p.duration <= minDuration) {
		return false
	}
	return !p.started.IsZero() && p.reached()
}

// tracker turns a stream of player snapshots into now-playing and scrobble
// events. It is pure: no I/O, no locking.
type tracker struct {
	cur *play
}

type events struct {
	nowPlaying *Track
	scrobbles  []Track
}

// observe folds one snapshot taken at now into the state.
func (t *tracker) observe(np bridge.NowPlaying, meta TrackMeta, now time.Time) events {
	var ev events
	playing := np.State == bridge.Playing && np.Track != nil
	pos := position(np, now)

	if p := t.cur; p != nil {
		dt := now.Sub(p.lastObs)
		credit := (p.playing || playing) && dt > 0 && dt <= maxGap
		samePlay := np.Track != nil && np.State != bridge.Stopped && np.Track.PID == p.pid &&
			!(p.reached() && pos < restartWindow && p.lastPos-pos > restartWindow)
		switch {
		case !samePlay:
			// Track changed, stopped, or restarted: credit the tail, at most
			// what was left of the old track.
			if credit && p.playing {
				d := dt
				if p.duration > 0 {
					d = min(d, max(0, p.duration-p.lastPos))
				}
				p.played += d
			}
			t.finish(p, &ev)
			t.cur = nil
		case credit && p.playing && playing:
			// Wall time, not position: a forward seek isn't credited and a
			// backward one doesn't lose time.
			p.played += dt
		case credit:
			// Paused or resumed in between: the position says how much of
			// the interval played.
			p.played += min(max(0, pos-p.lastPos), dt)
		}
		if t.cur != nil {
			if meta != (TrackMeta{}) {
				p.setMeta(np.Track, meta) // library details can arrive after the first snapshot
			}
			t.finish(p, &ev)
		}
	}

	if t.cur == nil && np.Track != nil && np.State != bridge.Stopped {
		t.cur = newPlay(np.Track, meta)
		t.cur.lastObs = now
	}
	p := t.cur
	if p == nil {
		return ev
	}
	switch {
	case playing && p.started.IsZero():
		p.started = now.Add(-pos)
		p.track.Timestamp = p.started.Unix()
		ev.nowPlaying = trackPtr(p.track)
	case playing && !p.playing && !p.pausedAt.IsZero() && now.Sub(p.pausedAt) > resumeNowPlaying:
		ev.nowPlaying = trackPtr(p.track)
	}
	switch {
	case playing:
		p.pausedAt = time.Time{}
	case p.playing:
		p.pausedAt = now
	}
	p.lastObs, p.lastPos, p.playing = now, pos, playing
	return ev
}

// finish queues a scrobble for p if it has just become eligible.
func (t *tracker) finish(p *play, ev *events) {
	if p.eligible() {
		p.scrobbled = true
		ev.scrobbles = append(ev.scrobbles, p.track)
	}
}

func newPlay(ct *bridge.CurrentTrack, m TrackMeta) *play {
	p := &play{pid: ct.PID}
	p.setMeta(ct, m)
	return p
}

// setMeta fills the play's track details; the timestamp is kept.
func (p *play) setMeta(ct *bridge.CurrentTrack, m TrackMeta) {
	p.track = Track{
		Artist:      or(m.Artist, ct.Artist),
		Track:       or(m.Title, ct.Name),
		Album:       or(m.Album, ct.Album),
		AlbumArtist: or(m.AlbumArtist, ct.AlbumArtist),
		TrackNumber: m.TrackNumber,
		Timestamp:   p.track.Timestamp,
	}
	p.duration = m.Duration
	if p.duration <= 0 {
		p.duration = time.Duration(ct.Duration * float64(time.Second))
	}
	p.track.Duration = int(math.Round(p.duration.Seconds()))
}

// position is the playhead at now: the reported position, advanced by the
// time since it was read when playing, and clamped to the track length.
func position(np bridge.NowPlaying, now time.Time) time.Duration {
	pos := time.Duration(np.Position * float64(time.Second))
	if np.State == bridge.Playing && np.At > 0 {
		if d := now.Sub(time.UnixMilli(np.At)); d > 0 {
			pos += d
		}
	}
	if np.Track != nil && np.Track.Duration > 0 {
		pos = min(pos, time.Duration(np.Track.Duration*float64(time.Second)))
	}
	return max(0, pos)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func trackPtr(t Track) *Track { return &t }

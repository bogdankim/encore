package lastfm

import "time"

// Play tracks one listening instance of a track and decides when it becomes
// a scrobble: track > 30s, and actually-played time >= min(duration/2, 4m).
// Played time is accumulated only while playing, so pauses don't count and a
// forward seek doesn't credit the skipped part.
type Play struct {
	Track    Track
	started  time.Time     // wall clock of first playback -> scrobble timestamp
	played   time.Duration // accumulated playing time
	lastTick time.Time
	playing  bool
	Sent     bool
}

func NewPlay(t Track, now time.Time) *Play {
	t.Timestamp = now.Unix()
	return &Play{Track: t, started: now, lastTick: now, playing: true}
}

// Tick is called on every poll from the Music.app helper.
// Gaps longer than maxGap (sleep, helper stall) are not credited.
func (p *Play) Tick(now time.Time, playing bool) {
	const maxGap = 5 * time.Second
	if p.playing {
		if d := now.Sub(p.lastTick); d > 0 && d <= maxGap {
			p.played += d
		}
	}
	p.lastTick = now
	p.playing = playing
}

func (p *Play) Threshold() time.Duration {
	d := time.Duration(p.Track.Duration) * time.Second
	if d == 0 {
		return 4 * time.Minute // unknown length: only the 4 minute rule applies
	}
	return min(d/2, 4*time.Minute)
}

func (p *Play) Eligible() bool {
	if p.Sent || (p.Track.Duration > 0 && p.Track.Duration <= 30) {
		return false
	}
	return p.played >= p.Threshold()
}

package player

import (
	"context"
	"strings"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

// Station playback: an Apple Music radio station, started by station ID
// through Client.PlayStation. Music picks every song, so while the station
// plays there is no context to walk: p.now is nil, p.station names the
// station, and Music's reports are followed as they come.
//
// The user queue still works. At the next song boundary the queued items are
// issued like any context (see stationMoved), and when Music reaches the end
// of them the station starts again, with a fresh song. All methods here
// expect p.mu to be held.

// KindStation is a context that is an Apple Music radio station.
const KindStation Kind = "station"

// StationKind says what sort of station it is, for the UI.
type StationKind string

const (
	StationPersonal  StationKind = "personal"  // the listener's own station ("ra.u-…")
	StationArtist    StationKind = "artist"    // an artist, with or without similar artists
	StationMood      StationKind = "mood"      // a genre, mood or activity station
	StationLive      StationKind = "live"      // live radio such as Apple Music 1: no timeline
	StationDiscovery StationKind = "discovery" // a discovery station Music builds for the listener
)

// Station is an Apple Music radio station encore can play.
type Station struct {
	ID     string // "ra.…"
	Name   string
	Kind   StationKind
	ArtURL string // optional
}

const stationPrefix = "ra."

// ArtistStationID is the id of the station for an artist and similar
// artists, from the catalog artist id.
func ArtistStationID(artistID int64) string { return stationPrefix + formatID(artistID) }

// ArtistOnlyStationID is the id of the station that plays only the artist.
func ArtistOnlyStationID(artistID int64) string { return stationPrefix + "a-" + formatID(artistID) }

// ValidStationID reports whether id looks like an Apple Music station id:
// "ra." and then letters, digits, "-", "_" or ".". The bridge checks the same.
func ValidStationID(id string) bool {
	rest, ok := strings.CutPrefix(id, stationPrefix)
	if !ok || rest == "" || len(id) > 128 {
		return false
	}
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// PlayStation plays the station st and makes it the context, replacing the
// current one. src.Kind is set to KindStation; an empty ID and Title are
// filled from st. The user queue is kept and plays after the first song.
func (p *Player) PlayStation(src Source, st Station) {
	defer p.lock()()
	if !ValidStationID(st.ID) {
		p.setErr(MsgBadStation)
		return
	}
	src.Kind = KindStation
	if src.ID == "" {
		src.ID = st.ID
	}
	if src.Title == "" {
		src.Title = st.Name
	}
	p.clearContext()
	p.src = src
	p.station = &st
	p.pruneCatalog()
	p.issueStation()
}

// onStation reports whether Music is playing the station itself, rather than
// queued items played during it.
func (p *Player) onStation() bool { return p.station != nil && p.now == nil }

// liveLocked reports whether a live station is playing: a station the caller
// called live, or one whose song Music reports with no duration.
func (p *Player) liveLocked() bool {
	if !p.onStation() {
		return false
	}
	if p.station.Kind == StationLive {
		return true
	}
	ct := p.np.Track
	return p.pending == nil && ct != nil && ct.Duration == 0 && p.np.State != bridge.Stopped
}

// issueStation (re)starts p.station.
func (p *Player) issueStation() {
	p.touch()
	p.now = nil
	p.fromQueue = false
	p.issued, p.issuedIdx = nil, 0
	id := p.station.ID
	p.sendPlay(pendingStation, "", func(ctx context.Context) error {
		return p.client.PlayStation(ctx, id)
	})
}

// stationConfirms reports whether np shows the station started: any song
// reported after the call returned, other than the one Music had before (or
// that one again from its start).
func (p *Player) stationConfirms(np bridge.NowPlaying) bool {
	if !p.pending.sent || np.Track == nil || np.State == bridge.Stopped {
		return false
	}
	return np.Track.PID != p.pending.fromRaw || np.Position < restartAfter.Seconds()
}

// checkStation runs while a station start is pending and its grace has
// passed. A station that leaves Music stopped, or that never replaces what
// Music was playing, didn't start.
func (p *Player) checkStation(final bool) {
	np := p.np
	switch {
	case stoppedNoTrack(np):
	case final && np.Track.PID == p.pending.fromRaw:
	case final:
		// Music plays something new but never sent a report we took as the
		// start (a late call reply); it is the station.
		p.pending = nil
		p.posHold = time.Time{}
		p.rebase(p.clock.Now())
		return
	default:
		return
	}
	p.pending = nil
	p.state = held[bridge.PlayerState]{}
	p.dropStation()
	p.setErr(MsgStationFailed)
}

// dropStation forgets the station after it failed to start.
func (p *Player) dropStation() {
	if p.station == nil {
		return
	}
	p.touch()
	p.station = nil
	p.src = Source{}
}

// stationMoved handles Music moving to another song of the station. When
// the user queued something it plays now, after the song that just ended.
func (p *Player) stationMoved() {
	if p.advance() {
		p.issue()
	}
}

// stationNext is Next with nothing queued during a station: Music's next song
// in it, or, while the last queued item plays, the station again.
func (p *Player) stationNext() {
	if !p.onStation() {
		p.issueStation()
		return
	}
	if !p.liveLocked() {
		now := p.clock.Now()
		p.base, p.baseAt = 0, now
		p.posHold = now.Add(holdFor)
	}
	p.push(job{
		transport: true,
		timeout:   bridge.DefaultTimeout,
		run:       func(ctx context.Context) error { return p.client.Next(ctx) },
	})
}

// stationPrevious asks Music to go back. A station may not allow it; whatever
// Music does shows in its next report.
func (p *Player) stationPrevious() {
	p.push(job{
		transport: true,
		timeout:   bridge.DefaultTimeout,
		run:       func(ctx context.Context) error { return p.client.Previous(ctx) },
	})
}

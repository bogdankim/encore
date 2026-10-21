package bridge

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// Stations in the Fake: an endless run of made-up catalog songs, one at a
// time, as Music plays an Apple Music radio station. Music picks every song,
// so there is no queue to read and Previous only restarts the song. A live
// station (fakeLiveStations) reports position 0 and no duration, like Apple
// Music 1; its "songs" are the show's segments.

// fakeLiveStations are the station ids the Fake plays as live radio.
var fakeLiveStations = map[string]bool{
	"ra.978194965":  true, // Apple Music 1
	"ra.1498155548": true, // Apple Music Hits
	"ra.1498157166": true, // Apple Music Country
}

// FakeLiveStation reports whether the Fake plays id as a live station.
func FakeLiveStation(id string) bool { return fakeLiveStations[id] }

// PlayStation starts the station id ("ra.…") from its first song.
func (f *Fake) PlayStation(ctx context.Context, id string) error {
	return f.do(ctx, true, func(now time.Time) error {
		if !stationID(id) {
			return &Error{Code: 1, Message: "bad station id " + id}
		}
		f.station, f.live, f.stationN = id, fakeLiveStations[id], 0
		f.nextStationSong()
		f.begin(now)
		return nil
	})
}

// leaveStation ends station playback before another play command.
func (f *Fake) leaveStation() { f.station, f.live = "", false }

// nextStationSong replaces the queue with the station's next song. Only the
// current song is kept, so an endless station never grows the queue.
func (f *Fake) nextStationSong() {
	f.stationN++
	f.tempSeq++
	r := rand.New(rand.NewPCG(uint64(fnv32(f.station)), f.stationN))
	title := wAdj[r.IntN(len(wAdj))] + " " + wNoun[r.IntN(len(wNoun))]
	artist := "The " + wPlural[r.IntN(len(wPlural))]
	album := wAdj[r.IntN(len(wAdj))] + " " + wPlural[r.IntN(len(wPlural))]
	t := &SnapshotTrack{
		PID:   fmt.Sprintf("0A5A%04X%08X", f.tempSeq&0xFFFF, fnv32(f.station)),
		Title: title, Artist: artist, Album: album, AlbumArtist: artist,
		Duration: int64(150+r.IntN(150)) * 1000, Kind: "subscription", Available: true,
	}
	f.extra = map[string]*SnapshotTrack{t.PID: t}
	f.queue, f.qi = []string{t.PID}, 0
	f.songAt = f.now()
}

// stationID reports whether s looks like an Apple Music station id: "ra."
// and then letters, digits, "-", "_" or ".".
func stationID(s string) bool {
	rest, ok := strings.CutPrefix(s, "ra.")
	if !ok || rest == "" || len(s) > 128 {
		return false
	}
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

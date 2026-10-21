package player

import (
	"context"
	"slices"
	"time"

	"github.com/bogdankim/encore/internal/bridge"
)

// Keeping Music in step with the model. All methods expect p.mu to be held.
//
// Music only knows the tracks we issued (p.issued). At each track boundary we
// compare what Music moved to with what the model wants next; when they differ
// (the user queue or shuffle changed since we issued), we re-issue starting with
// the model's track. Music has already started its own pick by then, so the
// overlap is under a second, but the current track is never restarted.

// issue sends PlayTracks for p.now and what follows it, or PlayStore when
// p.now is a catalog song. One call carries one kind: the slice ends where the
// kind changes, and the next call goes out when Music reaches the end of it.
func (p *Player) issue() {
	if p.now == nil {
		return
	}
	tracks := p.plan(SliceSize)
	store := IsCatalog(p.now)
	for i, t := range tracks {
		if IsCatalog(t) != store {
			tracks = tracks[:i]
			break
		}
	}
	pids := make([]string, len(tracks))
	if store {
		ids := make([]string, len(tracks))
		for i, t := range tracks {
			pids[i] = t.PID
			ids[i] = storeIDOf(t.PID)
		}
		p.startPlay(pids, func(ctx context.Context) error {
			return p.client.PlayStore(ctx, ids, 0)
		})
		return
	}
	var containers map[string]string
	for i, t := range tracks {
		pids[i] = t.PID
		if !t.InLibrary && t.Container != "" {
			if containers == nil {
				containers = map[string]string{}
			}
			containers[t.PID] = t.Container
		}
	}
	p.startPlay(pids, func(ctx context.Context) error {
		return p.client.PlayTracks(ctx, pids, containers)
	})
}

// issueNative plays a library playlist with Music's own context. The context
// must be exactly that playlist, in order.
func (p *Player) issueNative(pid string) {
	pids := make([]string, len(p.order))
	for i, ix := range p.order {
		pids[i] = p.orig[ix].PID
	}
	p.startPlay(pids, func(ctx context.Context) error {
		return p.client.PlayPlaylist(ctx, pid)
	})
}

func (p *Player) startPlay(pids []string, run func(ctx context.Context) error) {
	p.fixMusicShuffle(p.clock.Now())
	p.issued, p.issuedIdx = pids, 0
	if isStorePID(pids[0]) {
		p.issuedStore = true
	}
	p.sendPlay(pendingPlay, pids[0], run)
}

// sendPlay queues a play call and makes it pending; pid is the track it
// starts, "" for a station.
func (p *Player) sendPlay(kind pendingKind, pid string, run func(ctx context.Context) error) {
	now := p.clock.Now()
	p.canonRaw, p.canonPID = "", ""
	p.seq++
	seq := p.seq
	p.pending = &pending{seq: seq, pid: pid, from: p.lastPID, fromRaw: p.rawPID(), kind: kind}
	p.base, p.baseAt = 0, now
	p.posHold = now.Add(holdFor)
	p.state = held[bridge.PlayerState]{bridge.Playing, now.Add(holdFor)}
	p.w.push(job{
		key:       keyPlay,
		transport: true,
		timeout:   playTimeout,
		run:       run,
		done:      func(err error) { p.sent(seq, err) },
	})
}

// step sends Client.Next or Client.Previous; Music lands on p.now.
func (p *Player) step(run func(ctx context.Context) error) {
	now := p.clock.Now()
	p.seq++
	seq := p.seq
	p.pending = &pending{seq: seq, pid: p.now.PID, from: p.lastPID, fromRaw: p.rawPID(), kind: pendingStep}
	p.base, p.baseAt = 0, now
	p.posHold = now.Add(holdFor)
	p.w.push(job{
		transport: true,
		timeout:   bridge.DefaultTimeout,
		run:       run,
		done:      func(err error) { p.sent(seq, err) },
	})
}

// sent runs when a transport call returned.
func (p *Player) sent(seq int, err error) {
	defer p.lock()()
	if p.pending == nil || p.pending.seq != seq {
		if err != nil {
			p.setErr(humanize(err))
		}
		return
	}
	if err != nil {
		p.setErr(humanize(err))
		if p.pending.kind == pendingStation {
			p.dropStation()
		}
		p.pending = nil
		p.issued = nil
		p.state = held[bridge.PlayerState]{}
		p.posHold = time.Time{}
		return
	}
	p.pending.sent = true
	p.pending.sentAt = p.clock.Now()
	grace, timeout := p.pending.waits()
	p.clock.AfterFunc(grace, func() { p.checkPending(seq, false) })
	p.clock.AfterFunc(timeout, func() { p.checkPending(seq, true) })
}

// checkPending runs on timers after a transport call returned and Music still
// hasn't reported the track we asked for.
func (p *Player) checkPending(seq int, final bool) {
	defer p.lock()()
	if p.pending == nil || p.pending.seq != seq {
		return
	}
	if p.pending.kind == pendingStation {
		p.checkStation(final)
		return
	}
	if stoppedNoTrack(p.np) {
		p.skipDead()
		return
	}
	if final {
		p.pending = nil
		p.rebase(p.clock.Now())
		if p.lastPID != "" && (p.now == nil || p.lastPID != p.now.PID) {
			p.resync(p.lastPID)
		}
	}
}

// skipDead handles a track Music accepted and silently refused to play.
func (p *Player) skipDead() {
	p.touch()
	p.dead[p.pending.pid] = true
	p.pending = nil
	p.state = held[bridge.PlayerState]{}
	if p.advance() {
		p.setErr(MsgSkippedUnavailable)
		p.issue()
		return
	}
	p.setErr(MsgUnavailable)
	p.issued = nil
}

// Observe feeds a bridge event to the player.
func (p *Player) Observe(ev bridge.Event) {
	defer p.lock()()
	switch ev := ev.(type) {
	case bridge.PlayerEvent:
		p.observePlayer(ev.NowPlaying)
	case bridge.MusicEvent:
		if !ev.Running {
			p.pending = nil
			p.issued = nil
			p.lastPID = ""
			p.np = bridge.NowPlaying{State: bridge.Stopped, Volume: p.np.Volume, Repeat: p.np.Repeat}
			p.base, p.baseAt = 0, p.clock.Now()
		}
	case bridge.ExitEvent:
		// The bridge restarts; Music keeps playing what it had.
		p.pending = nil
	}
}

func (p *Player) observePlayer(np bridge.NowPlaying) {
	now := p.clock.Now()
	prev := p.np
	prevPID := p.lastPID
	p.np = np
	pid := p.canon(np.Track)
	p.lastPID = pid
	if p.pending == nil && !now.Before(p.posHold) {
		p.rebase(now)
	}
	if np.Shuffle {
		// Someone turned on Music's shuffle (Music.app, Control Center). Take it
		// as a request for ours and switch Music's back off.
		if p.issued != nil && now.After(p.musicShuffleFix) {
			p.setShuffleLocked(true)
		}
		p.fixMusicShuffle(now)
	}

	if p.pending != nil {
		if p.confirms(pid, np) {
			p.pending = nil
			p.posHold = time.Time{}
			p.rebase(now)
			return
		}
		if !p.pending.sent {
			return
		}
		elapsed := now.Sub(p.pending.sentAt)
		grace, timeout := p.pending.waits()
		if p.pending.kind == pendingStation {
			if elapsed >= grace {
				p.checkStation(elapsed >= timeout)
			}
			return
		}
		if stoppedNoTrack(np) && elapsed >= grace {
			p.skipDead()
			return
		}
		if elapsed < timeout {
			return
		}
		p.pending = nil
		p.rebase(now)
		prev = bridge.NowPlaying{} // judge this report on its own
		prevPID = ""
	}

	if stoppedNoTrack(np) {
		if !stoppedNoTrack(prev) {
			p.stopped()
		}
		return
	}
	if pid == prevPID || (p.now != nil && pid == p.now.PID) {
		return
	}
	if p.onStation() {
		p.stationMoved()
		return
	}
	p.boundary(pid, prev)
}

// confirms reports whether np, which canon mapped to pid, shows the track the
// pending command asked for. When that track was already playing, only a
// report from after the call that shows it near the start counts.
func (p *Player) confirms(pid string, np bridge.NowPlaying) bool {
	if p.pending.kind == pendingStation {
		return p.stationConfirms(np)
	}
	if np.Track == nil || pid != p.pending.pid {
		return false
	}
	if pid != p.pending.from && np.Track.PID != p.pending.fromRaw {
		return true
	}
	return p.pending.sent && np.Position < restartAfter.Seconds()
}

// rawPID is the persistent ID in Music's last report, before canon.
func (p *Player) rawPID() string {
	if p.np.Track == nil {
		return ""
	}
	return p.np.Track.PID
}

// boundary handles Music moving to pid on its own; prev is the report before.
func (p *Player) boundary(pid string, prev bridge.NowPlaying) {
	if k := p.musicNext(); k >= 0 && p.issued[k] == pid {
		// Music moved on through what we issued.
		p.issuedIdx = k
		if next := p.peek(); next != nil && next.PID == pid {
			p.advance()
			return
		}
		if p.advance() {
			p.issue() // the slice was stale; play what we meant to
			return
		}
		if p.station != nil {
			p.issueStation() // the queue ran out: back to the station
			return
		}
		// Nothing is meant to come next (the rest was removed, or repeat went off).
		p.issued = nil
		p.state = held[bridge.PlayerState]{bridge.Paused, p.clock.Now().Add(holdFor)}
		p.push(job{
			timeout: bridge.DefaultTimeout,
			run:     func(ctx context.Context) error { return p.client.Pause(ctx) },
		})
		return
	}
	if p.issuedIdx == len(p.issued)-1 && finishing(prev) && !p.isIssued(pid) && p.advance() {
		// Music finished what we issued and went on with a pick of its own
		// (Autoplay after a catalog tracklist). Play what we meant instead.
		p.issue()
		return
	}
	if p.issuedIdx == len(p.issued)-1 && finishing(prev) && !p.isIssued(pid) && p.station != nil {
		p.issueStation() // the queue played during a station ran out
		return
	}
	p.resync(pid)
}

// finishing reports whether np shows a track playing its last seconds.
func finishing(np bridge.NowPlaying) bool {
	return np.State == bridge.Playing && np.Track != nil && np.Track.Duration > 0 &&
		np.Position >= np.Track.Duration-finishWindow.Seconds()
}

// resync follows Music to a track we didn't expect: media keys or Music.app
// moved within our slice, or something else is playing altogether.
func (p *Player) resync(pid string) {
	if i := slices.Index(p.issued, pid); i >= 0 {
		p.issuedIdx = i
		p.syncTo(pid)
		return
	}
	p.clearContext()
	p.issued = nil
	p.now = p.lookupTrack(pid)
}

// stopped handles Music stopping on its own. At the end of a slice it starts
// the next one.
func (p *Player) stopped() {
	if len(p.issued) == 0 || p.issuedIdx < len(p.issued)-1 {
		return
	}
	if p.advance() {
		p.issue()
		return
	}
	if p.station != nil {
		p.issueStation()
		return
	}
	p.issued = nil
}

// musicNext is the index in p.issued that Music plays after the current one,
// or -1.
func (p *Player) musicNext() int {
	if len(p.issued) == 0 {
		return -1
	}
	if k := p.issuedIdx + 1; k < len(p.issued) {
		return k
	}
	if p.repeatLocked(p.clock.Now()) == bridge.RepeatAll {
		return 0
	}
	return -1
}

// rebase takes the position from the last report.
func (p *Player) rebase(now time.Time) {
	sec := p.np.Position
	if p.np.State == bridge.Playing && p.np.At > 0 {
		if lag := now.UnixMilli() - p.np.At; lag > 0 && lag < 5000 {
			sec += float64(lag) / 1000
		}
	}
	p.base, p.baseAt = sec, now
}

// fixMusicShuffle turns Music's own shuffle off when it reports it on.
func (p *Player) fixMusicShuffle(now time.Time) {
	if !p.np.Shuffle || now.Before(p.musicShuffleFix) {
		return
	}
	p.musicShuffleFix = now.Add(holdFor)
	p.push(job{
		key:     keyShuffle,
		timeout: bridge.DefaultTimeout,
		run:     func(ctx context.Context) error { return p.client.SetShuffle(ctx, false) },
	})
}

func stoppedNoTrack(np bridge.NowPlaying) bool {
	return np.State == bridge.Stopped || np.Track == nil
}

// waits returns how long after the call returned Music may show no track
// before the track counts as dead, and how long we wait for it at all. Music
// streams a catalog song for a few seconds before it reports it.
func (pd *pending) waits() (grace, timeout time.Duration) {
	if pd.kind == pendingStation || isStorePID(pd.pid) {
		return storeDeadGrace, storePendingTimeout
	}
	return deadGrace, pendingTimeout
}

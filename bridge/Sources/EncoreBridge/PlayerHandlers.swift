import Foundation

/// Transport, player settings and per-track commands. All run on the music queue.
enum PlayerHandlers {
  static func register(_ server: Server, _ music: MusicAE) {
    let monitor = PlayerMonitor.shared
    monitor.configure(server, music)

    // Never launches Music: a client polling state must not start it.
    server.register("player.state", on: server.music) { _ in
      do {
        return try monitor.read()
      } catch let error where MusicAE.isNotRunning(error) {
        return PlayerMonitor.stoppedState()
      }
    }

    /// Registers a command that launches Music when needed and is followed by `player` events.
    func command(_ method: String, launches: Bool = true, _ body: @escaping (Server.Params) throws -> Void) {
      server.register(method, on: server.music) { p in
        if launches {
          try music.withMusicLaunched { try body(p) }
        } else {
          do { try body(p) } catch let error where MusicAE.isNotRunning(error) { return [String: Any]() }
        }
        monitor.commandDidRun()
        return [String: Any]()
      }
    }

    // The first read after a transport command often still shows the old state;
    // `transport` reports the one we asked for and checks that Music got there.
    // playpause reads the state and sends pause or play: Music's own playpause
    // is dropped or applied backwards in some sessions. A track that started
    // less than 2 s ago counts as playing: Music reports stopped, then paused,
    // while it loads.
    command("player.playpause") { _ in
      let pause = try monitor.readState() == "playing" || monitor.isStartingTrack
      try monitor.transport(pause ? .pause : .play)
    }
    command("player.pause", launches: false) { _ in try monitor.transport(.pause) }
    command("player.play") { _ in try monitor.transport(.play) }
    command("player.next") { _ in try music.nextTrack() }
    command("player.previous") { _ in try music.previousTrack() }

    command("player.seek") { p in
      try music.seek(to: max(0, try p.double("seconds")))
    }
    command("player.volume") { p in
      let volume = max(0, min(100, try p.int("volume")))
      try music.setVolume(Int32(volume))
      monitor.expect("volume", volume)
    }
    command("player.repeat") { p in
      let mode = try p.string("mode")
      guard let code = PlayerMonitor.repeatCode(mode) else {
        throw Server.BridgeError.badRequest("mode must be off, one or all")
      }
      try music.setRepeat(code)
      monitor.expect("repeat", mode)
    }
    command("player.shuffle") { p in
      let enabled = try p.bool("enabled")
      try music.setShuffle(enabled)
      monitor.expect("shuffle", enabled)
    }

    let tracks = TrackFinder(music: music)
    /// Favorite and dislike are one tri-state in Music: writing `false` to one can
    /// clear the other (setting disliked to false un-favorites a song). So write
    /// only when the value actually changes.
    func setFlag(_ code: FourCharCode, _ pid: String, _ value: Bool) throws {
      try tracks.with(pid) { track in
        let flag = music.prop(code, of: track)
        if try music.get(flag)?.booleanValue != value {
          try music.set(flag, to: NSAppleEventDescriptor(boolean: value))
        }
      }
    }
    command("track.setFavorite") { p in
      let pid = try p.string("pid"), value = try p.bool("value")
      try setFlag(fcc("pLov"), pid, value)
      monitor.expectFavorite(pid, value)
    }
    command("track.setDislike") { p in
      let pid = try p.string("pid"), value = try p.bool("value")
      try setFlag(fcc("pHat"), pid, value)
      monitor.expectDislike(pid, value)
    }

    // Never launches Music: lyrics lookups and prefetches run in the background, and
    // one that lands while Music quits would start it again behind the user's back.
    server.register("track.lyrics", on: server.music) { p in
      let pid = try p.string("pid")
      let text = try tracks.with(pid) { try music.get(music.prop(fcc("pLyr"), of: $0))?.stringValue }
      return ["text": text ?? ""]
    }
  }
}

/// Finds a track by persistent ID: in `library playlist 1` first, then in a playlist
/// that contains it (tracks added only to a playlist aren't in the library).
struct TrackFinder {
  let music: MusicAE

  /// Runs `body` with a specifier for the track. Throws -1728 when no playlist has it.
  func with<T>(_ pid: String, _ body: (NSAppleEventDescriptor) throws -> T) throws -> T {
    do {
      return try body(music.track(persistentID: pid))
    } catch let e as MusicAEError where e.code == -1728 {
      var tried: Set<String> = []
      // The last snapshot usually knows a playlist that has it: one more event.
      if let container = LibraryIndex.shared.container(of: pid) {
        tried.insert(container)
        if let r = try? body(music.track(persistentID: pid, in: music.playlist(persistentID: container))) { return r }
      }
      // Without a snapshot, ask each user and subscription playlist in turn.
      guard !LibraryIndex.shared.hasSnapshot else { throw e }
      for kind in [fcc("cUsP"), fcc("cSuP")] {
        let all = music.every(kind, of: nil)
        for playlistPid in MusicAE.strings(try? music.get(music.prop(fcc("pPIS"), of: all))) where !tried.contains(playlistPid) {
          tried.insert(playlistPid)
          let playlist = music.first(kind, of: music.whose(kind, fcc("pPIS"), equals: NSAppleEventDescriptor(string: playlistPid), of: nil))
          if let r = try? body(music.track(persistentID: pid, in: playlist)) { return r }
        }
      }
      throw e
    }
  }
}

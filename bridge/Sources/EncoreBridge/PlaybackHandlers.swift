import Foundation

/// `player.playTracks` and `player.playPlaylist`. Both run on the music queue.
enum PlaybackHandlers {
  static func register(_ server: Server, _ music: MusicAE) {
    let queue = PlayQueue(music: music)

    server.register("player.playTracks", on: server.music) { p in
      guard let pids = p["pids"] as? [String], !pids.isEmpty else {
        throw Server.BridgeError.badRequest("pids must be a non-empty list of persistent IDs")
      }
      let containers = p["containers"] as? [String: String] ?? [:]
      let started = DispatchTime.now()
      let missing = try music.withMusicLaunched { () -> [String] in
        try music.ensureShuffleOff()
        return try queue.play(pids, containers: containers)
      }
      PlayerMonitor.shared.playStarted()
      PlayerMonitor.shared.commandDidRun()
      server.log(String(format: "playTracks: %d tracks in %.0f ms", pids.count, elapsedMs(since: started)))
      if !missing.isEmpty {
        server.log("playTracks: \(missing.count) tracks could not be queued: \(missing.joined(separator: ","))")
        return ["missing": missing]
      }
      return [String: Any]()
    }

    server.register("player.playPlaylist", on: server.music) { p in
      let pid = try p.string("pid")
      try music.withMusicLaunched {
        try music.ensureShuffleOff()
        try music.play(music.playlist(persistentID: pid))
      }
      PlayerMonitor.shared.playStarted()
      PlayerMonitor.shared.commandDidRun()
      return [String: Any]()
    }
  }

  static func elapsedMs(since t: DispatchTime) -> Double {
    Double(DispatchTime.now().uptimeNanoseconds - t.uptimeNanoseconds) / 1e6
  }
}

extension MusicAE {
  /// encore shuffles on its side, so Music's shuffle must be off before playing a context.
  func ensureShuffleOff() throws {
    if try get(prop(fcc("pShE")))?.booleanValue == true { try setShuffle(false) }
  }
}

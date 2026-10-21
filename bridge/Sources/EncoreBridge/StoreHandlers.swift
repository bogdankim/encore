import Foundation

/// `player.playStore`: plays Apple Music catalog songs by store ID.
/// `player.playStation`: plays an Apple Music radio station by station ID.
/// Both run on the music queue.
enum StoreHandlers {
  /// Bridge error code for a catalog request MediaRemote refused or never answered.
  static let errStore = 5

  static func register(_ server: Server, _ music: MusicAE) {
    server.register("player.playStore", on: server.music) { p in
      let ids = try storeIDs(p["ids"])
      let start = (p["start"] as? NSNumber)?.intValue ?? 0
      guard start >= 0, start < ids.count else {
        throw Server.BridgeError.badRequest("start must index ids (0..<\(ids.count))")
      }
      guard StoreQueue.isAvailable else { throw Server.BridgeError.internalError("MediaRemote is unavailable") }
      let started = DispatchTime.now()
      // The queue goes to Music as the system media app, so it only needs to be
      // running; launching it first also turns MediaRemote's own launch (and its
      // CouldNotLaunchApplication) into the usual Apple Event path. encore
      // shuffles on its side, so Music's shuffle must be off, as for playTracks.
      try music.withMusicLaunched { try music.ensureShuffleOff() }
      // A tracklist has no start index (the queue's FirstItem setters are for
      // generic track identifiers), so the list starts at `start`.
      do {
        try StoreQueue.play(Array(ids[start...]))
      } catch let f as StoreQueue.Failure {
        throw error(for: f)
      }
      let monitor = PlayerMonitor.shared
      monitor.playStarted()
      monitor.commandDidRun()
      // Music starts streaming 1–4 s later; read again then in case its
      // playerInfo notification is late.
      for ms in [2500, 4500] {
        server.music.asyncAfter(deadline: .now() + .milliseconds(ms)) { monitor.refresh() }
      }
      server.log(String(format: "playStore: %d catalog songs in %.0f ms", ids.count - start, PlaybackHandlers.elapsedMs(since: started)))
      return [String: Any]()
    }

    server.register("player.playStation", on: server.music) { p in
      let id = try stationID(p["id"])
      guard StationQueue.isAvailable else { throw Server.BridgeError.internalError("MediaRemote is unavailable") }
      let started = DispatchTime.now()
      // Launched first for the same reason as playStore: reading the player
      // state fails with -600 when Music isn't running. Music's shuffle
      // doesn't apply to a station, so it is left as it is.
      _ = try music.withMusicLaunched { try music.get(music.prop(fcc("pPlS"))) }
      do {
        try StationQueue.play(id)
      } catch let f as StoreQueue.Failure {
        throw error(for: f, what: "the station")
      }
      let monitor = PlayerMonitor.shared
      monitor.playStarted()
      monitor.commandDidRun()
      // Music usually reports the first song as the call returns; read again
      // in case its playerInfo notification is late.
      for ms in [2500, 4500] {
        server.music.asyncAfter(deadline: .now() + .milliseconds(ms)) { monitor.refresh() }
      }
      server.log(String(format: "playStation: %@ in %.0f ms", id, PlaybackHandlers.elapsedMs(since: started)))
      return [String: Any]()
    }
  }

  /// A station ID: "ra." and then letters, digits, "-", "_" or ".".
  static func stationID(_ raw: Any?) throws -> String {
    guard let s = raw as? String else { throw Server.BridgeError.badRequest("id must be a station id string") }
    let rest = s.dropFirst(3)
    guard s.hasPrefix("ra."), !rest.isEmpty, s.count <= 128,
          rest.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || "-_.".contains($0)) }) else {
      throw Server.BridgeError.badRequest("bad station id \(s)")
    }
    return s
  }

  /// Store IDs as decimal strings. JSON numbers are accepted too.
  static func storeIDs(_ raw: Any?) throws -> [String] {
    guard let list = raw as? [Any], !list.isEmpty else {
      throw Server.BridgeError.badRequest("ids must be a non-empty list of catalog song ids")
    }
    return try list.map { v in
      let s: String
      if let str = v as? String {
        s = str
      } else if let n = v as? NSNumber, !(n is Bool) {
        s = n.stringValue
      } else {
        throw Server.BridgeError.badRequest("ids must be strings of digits")
      }
      guard !s.isEmpty, s.allSatisfy({ $0.isASCII && $0.isNumber }) else {
        throw Server.BridgeError.badRequest("bad catalog song id \(s)")
      }
      return s
    }
  }

  static func error(for f: StoreQueue.Failure, what: String = "the catalog songs") -> Error {
    switch f {
    case .unavailable:
      return Server.BridgeError.internalError("MediaRemote is unavailable")
    case .timedOut:
      return Server.BridgeError(code: errStore, message: "Music didn't answer the request for \(what) in time")
    case .command(let code, let name):
      switch code {
      case 1, 4, 9: // ApplicationNotFound, CouldNotLaunchApplication, ApplicationNotInstalled
        return MusicAEError(code: -600, message: "Music isn't running (\(name))")
      default:
        return Server.BridgeError(code: errStore, message: "Music didn't take \(what) (\(name))")
      }
    }
  }
}

import Foundation

/// `playlist.addTracks` and `playlist.create`. Both run on the music queue.
enum PlaylistHandlers {
  static func register(_ server: Server, _ music: MusicAE) {
    let tracks = TrackFinder(music: music)

    server.register("playlist.addTracks", on: server.music) { p in
      let pid = try p.string("pid")
      guard let pids = p["pids"] as? [String], !pids.isEmpty else {
        throw Server.BridgeError.badRequest("pids must be a non-empty list of persistent IDs")
      }
      return try music.withMusicLaunched { () -> [String: Any] in
        let playlist = music.playlist(persistentID: pid)
        let before = try music.countTracks(of: playlist)
        var missing: [String] = []
        var lastError: MusicAEError?
        for t in pids {
          do {
            try tracks.with(t) { track in
              _ = try music.send(fcc("core"), fcc("clon"), direct: track, params: [fcc("insh"): playlist])
            }
          } catch let e as MusicAEError where !MusicAE.isNotRunning(e) {
            missing.append(t)
            lastError = e
          }
        }
        // Music can answer a duplicate without adding anything, so the count decides.
        let added = try music.countTracks(of: playlist) - before
        if added <= 0, let lastError { throw lastError }
        if !missing.isEmpty {
          server.log("playlist.addTracks: \(missing.count) tracks could not be added: \(missing.joined(separator: ","))")
        }
        return ["added": max(added, 0), "missing": missing]
      }
    }

    server.register("playlist.create", on: server.music) { p in
      let name = try p.string("name")
      guard !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
        throw Server.BridgeError.badRequest("name must not be empty")
      }
      return try music.withMusicLaunched { () -> [String: Any] in
        let props = NSAppleEventDescriptor.record()
        props.setDescriptor(NSAppleEventDescriptor(string: name), forKeyword: fcc("pnam"))
        guard let created = try music.send(fcc("core"), fcc("crel"),
                                           params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cUsP")), fcc("prdt"): props]),
              let pid = try music.get(music.prop(fcc("pPIS"), of: created))?.stringValue else {
          throw MusicAEError(code: -1728, message: "could not create the playlist")
        }
        return ["pid": pid]
      }
    }
  }
}

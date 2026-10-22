import Foundation

/// `artwork.get`: square JPEG of a track's or album's artwork, written to `path`.
/// Image bytes come from ITLibrary (artwork queue, with its own ITLibrary loaded at
/// launch) and fall back to an Apple Event (music queue) whenever ITLibrary can't
/// help: no Media & Apple Music access, a failed load, or an item it doesn't know,
/// such as a new current track.
///
/// `playlist.artwork`: the same for the cover Music shows for a playlist. ITLibrary
/// has no playlist artwork, so it is always an Apple Event.
enum ArtworkHandlers {
  static func register(_ server: Server, _ music: MusicAE) {
    let index = ArtIndex(queue: server.artwork, log: server.log)
    // Load once access is settled (it may take a prompt), so the first art doesn't
    // wait ~0.5 s.
    Permissions.shared.whenLibraryDecided(on: server.artwork) { granted in
      if granted { index.warm() }
    }

    server.register("artwork.get", on: server.artwork) { p in
      let pid = try p.string("pid").uppercased()
      let size = try p.int("size")
      let path = try p.string("path")
      guard size > 0, size <= 4096 else { throw Server.BridgeError.badRequest("size must be 1...4096") }
      guard path.hasPrefix("/") else { throw Server.BridgeError.badRequest("path must be absolute") }

      let started = DispatchTime.now()
      let (data, source) = try imageData(pid, index, server, music)
      guard let data else { return ["path": ""] }
      let (w, h) = try ArtImage.writeSquareJPEG(data, size: size, to: URL(fileURLWithPath: path))
      server.log(String(format: "artwork %@ (%@) %dpx in %.1f ms", pid, source, w, PlaybackHandlers.elapsedMs(since: started)))
      return ["path": path, "w": w, "h": h]
    }

    server.register("playlist.artwork", on: server.artwork) { p in
      let pid = try p.string("pid").uppercased()
      let size = try p.int("size")
      let path = try p.string("path")
      guard size > 0, size <= 4096 else { throw Server.BridgeError.badRequest("size must be 1...4096") }
      guard path.hasPrefix("/") else { throw Server.BridgeError.badRequest("path must be absolute") }

      let started = DispatchTime.now()
      guard let data = try server.music.sync(execute: { try music.playlistArtworkData(persistentID: pid) }) else {
        return ["path": ""]
      }
      let (w, h) = try ArtImage.writeSquareJPEG(data, size: size, to: URL(fileURLWithPath: path))
      server.log(String(format: "playlist artwork %@ %dpx in %.1f ms", pid, w, PlaybackHandlers.elapsedMs(since: started)))
      return ["path": path, "w": w, "h": h]
    }
  }

  /// Encoded image bytes for a track or album pid and where they came from ("itlib"
  /// or "ae"). nil data means the item has no artwork.
  static func imageData(_ pid: String, _ index: ArtIndex, _ server: Server, _ music: MusicAE) throws -> (Data?, String) {
    if let data = index.imageData(pid) { return (data, "itlib") }
    // Music only knows tracks; an album's art is that of one of its tracks.
    let track = LibraryIndex.shared.artTrack(forAlbum: pid) ?? pid
    return (try server.music.sync { try music.artworkData(persistentID: track) }, "ae")
  }
}

extension MusicAE {
  /// `raw data of artwork 1` of the playlist with this pid: the cover Music shows for
  /// it, whether Apple's (subscription playlists), one the user picked, or one Music
  /// made. `count of artworks` of a playlist is 0 even then, so it isn't checked.
  /// nil when the playlist has none (Library, Music Videos, some subscription
  /// playlists) or doesn't exist.
  func playlistArtworkData(persistentID pid: String) throws -> Data? {
    do {
      return try artworkData(of: playlist(persistentID: pid))
    } catch let e as MusicAEError where e.code == -1728 || e.code == -1719 {
      return nil
    }
  }

  /// `raw data of artwork 1` of the library track with this pid, or of the current
  /// track when it has this pid (e.g. a playlist-only track). nil when there is none;
  /// throws when Music can't be asked (not running, no permission, timeout), so encore
  /// doesn't take that for "no artwork".
  func artworkData(persistentID pid: String) throws -> Data? {
    do {
      if let d = try artworkData(of: track(persistentID: pid)) { return d }
    } catch let e as MusicAEError where e.code == -1728 || e.code == -1719 {
      // no such track in the library, or no artwork
    }
    let current: String?
    do {
      current = try get(prop(fcc("pPIS"), of: currentTrack))?.stringValue
    } catch let e as MusicAEError where e.code == -1728 {
      current = nil // no current track
    }
    guard current == pid else { return nil }
    return try? artworkData()
  }
}

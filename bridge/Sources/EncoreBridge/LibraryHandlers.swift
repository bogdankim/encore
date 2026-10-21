import Foundation

/// `library.snapshot`: the library as JSON at `path`. From ITLibrary plus Music's
/// favorite/library flags when Media & Apple Music access is granted, otherwise all
/// from Music through Apple Events (slower).
enum LibraryHandlers {
  static func register(_ server: Server, _ music: MusicAE) {
    server.register("library.snapshot", on: server.library) { p in
      let path = try p.string("path")
      guard path.hasPrefix("/") else { throw Server.BridgeError.badRequest("path must be absolute") }
      let r = try snapshot(to: path, server, music)
      return ["tracks": r.tracks, "playlists": r.playlists, "ms": r.ms]
    }
  }

  /// Writes a snapshot by whichever path works and logs which one it took. Waits a
  /// while for the launch-time permission prompt, if one is up.
  static func snapshot(to path: String, _ server: Server, _ music: MusicAE) throws -> LibrarySnapshotter.Result {
    let r: LibrarySnapshotter.Result
    if Permissions.shared.libraryGranted(waiting: 20) {
      do {
        r = try LibrarySnapshotter.write(to: path, music: music, musicQueue: server.music)
      } catch {
        server.log("snapshot from ITLibrary failed: \(error); reading the library from Music")
        r = try MusicSnapshotter.write(to: path, music: music, musicQueue: server.music)
      }
    } else {
      r = try MusicSnapshotter.write(to: path, music: music, musicQueue: server.music)
    }
    let note = r.source == "music"
      ? " (from Music: Media & Apple Music access is \(Permissions.shared.libraryState))"
      : r.usedMusic ? "" : " (Music not reachable: favorites and inLibrary from ITLibrary)"
    server.log("snapshot (\(r.source)): \(r.tracks) tracks, \(r.playlists) playlists, \(r.bytes) bytes in \(r.ms) ms (\(r.phases))" + note)
    return r
  }
}

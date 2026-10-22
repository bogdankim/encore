import Foundation

/// `encore-bridge --selftest`: hello, a snapshot to a temp file, artwork for a few of
/// its tracks and albums, and one player read, printed to stdout. Doesn't start the
/// server and never prompts for Automation. ENCORE_NO_ITLIBRARY=1 tests the Apple
/// Event paths.
enum SelfTest {
  static func run(_ server: Server, _ music: MusicAE) -> Int32 {
    PlayerMonitor.shared.configure(server, music)
    func json(_ obj: Any) -> String {
      let data = (try? JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys, .withoutEscapingSlashes])) ?? Data()
      return String(decoding: data, as: UTF8.self)
    }
    func ms(since t: DispatchTime) -> Int { Int((DispatchTime.now().uptimeNanoseconds - t.uptimeNanoseconds) / 1_000_000) }

    var t = DispatchTime.now()
    Permissions.shared.settleLibrary(server.log)
    print("hello \(json(HelloHandlers.fields(music))) \(ms(since: t)) ms")

    let path = FileManager.default.temporaryDirectory.appendingPathComponent("encore-selftest-\(getpid()).json").path
    defer { try? FileManager.default.removeItem(atPath: path) }
    var failed = false
    do {
      let r = try server.library.sync { try LibraryHandlers.snapshot(to: path, server, music) }
      print("snapshot source=\(r.source) tracks=\(r.tracks) playlists=\(r.playlists) bytes=\(r.bytes) ms=\(r.ms) music=\(r.usedMusic) (\(r.phases))")
      failed = !artwork(path, server, music) || failed
    } catch {
      print("snapshot FAILED: \(error)")
      failed = true
    }

    t = DispatchTime.now()
    let state: Any = server.music.sync {
      do {
        return try PlayerMonitor.shared.read()
      } catch let error where MusicAE.isNotRunning(error) {
        return PlayerMonitor.stoppedState()
      } catch {
        failed = true
        return ["error": "\(error)"]
      }
    }
    print("player.state \(json(state)) \(ms(since: t)) ms")
    return failed ? 1 : 0
  }

  /// Artwork for the first three library tracks and albums of the snapshot at `path`,
  /// by the same paths as `artwork.get`. False when none came back.
  private static func artwork(_ path: String, _ server: Server, _ music: MusicAE) -> Bool {
    struct Snap: Decodable {
      struct Track: Decodable { var pid: String; var albumPid: String; var inLibrary: Bool }
      var tracks: [Track]
    }
    guard let data = FileManager.default.contents(atPath: path),
          let snap = try? JSONDecoder().decode(Snap.self, from: data) else {
      print("artwork FAILED: can't read the snapshot")
      return false
    }
    let lib = snap.tracks.filter(\.inLibrary)
    var pids = lib.prefix(3).map(\.pid)
    var albums: [String] = []
    for t in lib where !t.albumPid.isEmpty && !albums.contains(t.albumPid) && albums.count < 3 { albums.append(t.albumPid) }
    pids += albums
    let index = ArtIndex(queue: server.artwork, log: server.log)
    var found = 0
    for pid in pids {
      let t = DispatchTime.now()
      do {
        let (data, source) = try server.artwork.sync { try ArtworkHandlers.imageData(pid, index, server, music) }
        if data != nil { found += 1 }
        print("artwork \(pid) source=\(source) bytes=\(data?.count ?? 0) \(String(format: "%.1f", PlaybackHandlers.elapsedMs(since: t))) ms")
      } catch {
        print("artwork \(pid) FAILED: \(error)")
      }
    }
    return found > 0
  }
}

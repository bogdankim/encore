import Foundation
import iTunesLibrary

/// Track and album lookups into ITLibrary for artwork. Loaded once Media & Apple
/// Music access is granted, and reloaded when asked for a pid it doesn't know, at most
/// once a minute (a reload takes ~0.5 s). A failed load is retried after a minute too;
/// meanwhile callers get nil and use the Apple Event path. Only used from the artwork
/// queue.
final class ArtIndex {
  private var lib: ITLibrary?
  private var tracks: [String: ITLibMediaItem] = [:]
  private var albums: [String: [ITLibMediaItem]] = [:]
  private var loadedAt = DispatchTime.now()
  private var failedAt: DispatchTime?
  private let log: (String) -> Void

  init(log: @escaping (String) -> Void) { self.log = log }

  /// Loads the index now unless it is loaded.
  func warm() {
    if lib == nil { load() }
  }

  /// Encoded artwork (usually JPEG) for a track or album pid, nil when ITLibrary has
  /// none or can't be used.
  func imageData(_ pid: String) -> Data? {
    guard Permissions.shared.libraryState == "granted" else { return nil }
    if lib == nil {
      if let failedAt, DispatchTime.now() < failedAt + 60 { return nil }
      load()
    } else if tracks[pid] == nil && albums[pid] == nil && DispatchTime.now() > loadedAt + 60 {
      load()
    }
    if let item = tracks[pid] { return item.artwork?.imageData }
    for item in albums[pid] ?? [] where item.hasArtworkAvailable {
      if let d = item.artwork?.imageData { return d }
    }
    return nil
  }

  private func load() {
    do {
      if let lib {
        lib.reloadData()
      } else {
        lib = try ITLibrary(apiVersion: "1.0")
      }
    } catch {
      failedAt = .now()
      log("artwork index: \(error); using Music for artwork")
      return
    }
    guard let lib else { return }
    var tracks: [String: ITLibMediaItem] = [:]
    var albums: [String: [ITLibMediaItem]] = [:]
    tracks.reserveCapacity(lib.allMediaItems.count)
    for item in lib.allMediaItems {
      tracks[ArtIndex.hex(item.persistentID)] = item
      albums[ArtIndex.hex(item.album.persistentID), default: []].append(item)
    }
    self.tracks = tracks
    self.albums = albums
    loadedAt = .now()
    failedAt = nil
  }

  static func hex(_ n: NSNumber) -> String { String(format: "%016llX", n.uint64Value) }
}

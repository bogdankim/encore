import Foundation
import iTunesLibrary

/// One track of the library as the artwork index needs it: its pid, its album's pid,
/// and a way to read its encoded cover.
struct ArtEntry {
  var pid: String
  var albumPID: String
  var hasArtwork: Bool
  var data: () -> Data?
}

/// Where the artwork index gets its entries. `load` is called again to refresh them
/// and after `release`, which lets go of whatever the source holds.
protocol ArtSource: AnyObject {
  func load() throws -> [ArtEntry]
  func release()
}

/// ITLibrary as an `ArtSource`.
final class ITLibrarySource: ArtSource {
  private var lib: ITLibrary?

  func load() throws -> [ArtEntry] {
    if let lib {
      lib.reloadData()
    } else {
      lib = try ITLibrary(apiVersion: "1.0")
    }
    guard let lib else { return [] }
    return lib.allMediaItems.map { item in
      ArtEntry(
        pid: ArtIndex.hex(item.persistentID),
        albumPID: ArtIndex.hex(item.album.persistentID),
        hasArtwork: item.hasArtworkAvailable,
        data: { item.artwork?.imageData })
    }
  }

  func release() { lib = nil }
}

/// Track and album lookups for artwork. Loaded once access is granted, and reloaded
/// when asked for a pid it doesn't know, at most once a minute (a reload takes
/// ~0.5 s). A failed load, or one that finds no tracks at all, is tried again
/// `retryAfterFailure` later; meanwhile callers get nil and use the Apple Event
/// path. Only used from the artwork queue.
final class ArtIndex {
  private let source: ArtSource
  private var tracks: [String: ArtEntry] = [:]
  private var albums: [String: [ArtEntry]] = [:]
  private var loaded = false
  private var loadedAt: DispatchTime
  private var failedAt: DispatchTime?
  private let log: (String) -> Void
  private let clock: () -> DispatchTime
  private let enabled: () -> Bool
  /// The queue this runs on, and when it was last asked for something. The index
  /// holds every track of the library, which is the biggest thing the bridge keeps
  /// (about 1 to 2 KB an item), so it is let go of after `idleFor` seconds without a
  /// request and loaded again on the next one (~0.5 s).
  private let queue: DispatchQueue
  private var lastUsed: DispatchTime
  private var releaseScheduled = false
  let idleFor: Double
  /// How long after a failed load the next request tries again. Every request in
  /// between goes through Music, one Apple Event at a time, which is far slower
  /// for a grid of covers, so this is short.
  let retryAfterFailure: Double

  init(
    queue: DispatchQueue,
    source: ArtSource = ITLibrarySource(),
    idleFor: Double = 120,
    retryAfterFailure: Double = 5,
    clock: @escaping () -> DispatchTime = { .now() },
    enabled: @escaping () -> Bool = { Permissions.shared.libraryState == "granted" },
    log: @escaping (String) -> Void
  ) {
    self.queue = queue
    self.source = source
    self.idleFor = idleFor
    self.retryAfterFailure = retryAfterFailure
    self.clock = clock
    self.enabled = enabled
    self.log = log
    loadedAt = clock()
    lastUsed = clock()
  }

  /// Loads the index now unless it is loaded.
  func warm() {
    if !loaded { load() }
    lastUsed = clock()
    scheduleRelease()
  }

  /// Whether the index is in memory.
  var isLoaded: Bool { loaded }

  /// Encoded artwork (usually JPEG) for a track or album pid, nil when ITLibrary has
  /// none or can't be used.
  func imageData(_ pid: String) -> Data? {
    guard enabled() else { return nil }
    lastUsed = clock()
    if !loaded {
      if let failedAt, clock() < failedAt + retryAfterFailure { return nil }
      load()
    } else if tracks[pid] == nil && albums[pid] == nil && clock() > loadedAt + 60 {
      load()
    }
    scheduleRelease()
    if let entry = tracks[pid] { return entry.data() }
    for entry in albums[pid] ?? [] where entry.hasArtwork {
      if let d = entry.data() { return d }
    }
    return nil
  }

  private func scheduleRelease() {
    guard !releaseScheduled, loaded else { return }
    releaseScheduled = true
    queue.asyncAfter(deadline: .now() + idleFor) { [weak self] in
      guard let self else { return }
      self.releaseScheduled = false
      if self.clock() >= self.lastUsed + self.idleFor {
        self.release()
      } else {
        self.scheduleRelease()
      }
    }
  }

  /// Lets go of the library. The next request loads it again.
  func release() {
    source.release()
    tracks = [:]
    albums = [:]
    loaded = false
    log("artwork index: released after \(Int(idleFor)) s idle")
  }

  private func load() {
    let entries: [ArtEntry]
    do {
      entries = try source.load()
    } catch {
      failedAt = clock()
      log("artwork index: \(error); using Music for artwork")
      return
    }
    // ITLibrary can answer with no items while its service is still coming up;
    // an index of nothing would send every pid to Music for a minute.
    guard !entries.isEmpty else {
      failedAt = clock()
      log("artwork index: no tracks; using Music for artwork")
      return
    }
    var tracks: [String: ArtEntry] = [:]
    var albums: [String: [ArtEntry]] = [:]
    tracks.reserveCapacity(entries.count)
    for entry in entries {
      tracks[entry.pid] = entry
      albums[entry.albumPID, default: []].append(entry)
    }
    self.tracks = tracks
    self.albums = albums
    loaded = true
    loadedAt = clock()
    failedAt = nil
  }

  static func hex(_ n: NSNumber) -> String { String(format: "%016llX", n.uint64Value) }
}

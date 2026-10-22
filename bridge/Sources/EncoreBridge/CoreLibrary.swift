import Foundation
import iTunesLibrary

// Snapshot types. JSON field names match internal/bridge/types.go.

struct SnapshotTrack: Encodable {
  var pid = "", title = "", artist = "", albumArtist = "", album = "", albumPid = ""
  var genre = "", composer = ""
  var year = 0, track = 0, trackCount = 0, disc = 0, discCount = 0
  var duration: Int64 = 0, added: Int64 = 0, lastPlayed: Int64 = 0
  var plays = 0, rating = 0
  var favorited = false, disliked = false, explicit = false, compilation = false
  var inLibrary = false, available = true
  var kind = "local"
  var art = false
  var sortTitle = "", sortArtist = "", sortAlbum = "", sortAlbumArtist = ""
  /// The Apple Music catalog id of the song (the private `storeItemID`), when it has one.
  var storeId: Int64?
}

struct SnapshotPlaylist: Encodable {
  var pid: String
  var parentPid: String
  var name: String
  var kind: String
  var special: String
  var items: [String]
  /// The playlist's cloud id ("pl.…"), the same id the Apple Music catalog and Home use.
  var globalId: String?
}

struct LibrarySnapshot: Encodable {
  var generatedAt: Int64
  var tracks: [SnapshotTrack]
  var playlists: [SnapshotPlaylist]
}

func persistentHex(_ value: UInt64) -> String {
  let digits = String(value, radix: 16, uppercase: true)
  return digits.count >= 16 ? digits : String(repeating: "0", count: 16 - digits.count) + digits
}

func persistentHex(_ number: NSNumber) -> String { persistentHex(number.uint64Value) }

/// What the bridge remembers from the latest snapshot. Thread-safe.
final class LibraryIndex: @unchecked Sendable {
  static let shared = LibraryIndex()

  private let lock = NSLock()
  private var containers: [String: String] = [:]
  private var albumArt: [String: String] = [:]
  private var snapshotTaken = false
  private var usedMusic = false

  /// A playlist that contains `pid`, for tracks that aren't in `library playlist 1`.
  func container(of pid: String) -> String? {
    lock.withLock { containers[pid] }
  }

  /// A track of album `pid` to ask Music for the album's artwork with.
  func artTrack(forAlbum pid: String) -> String? {
    lock.withLock { albumArt[pid] }
  }

  var hasSnapshot: Bool {
    lock.withLock { snapshotTaken }
  }

  /// True when the latest snapshot had to skip Music's own favorite/inLibrary data.
  var lastSnapshotLackedMusic: Bool {
    lock.withLock { snapshotTaken && !usedMusic }
  }

  func update(containers: [String: String], albumArt: [String: String], usedMusic: Bool) {
    lock.withLock {
      self.containers = containers
      self.albumArt = albumArt
      self.snapshotTaken = true
      self.usedMusic = usedMusic
    }
  }
}

/// Builds `library.snapshot` from ITLibrary plus three Apple Events. Without
/// Media & Apple Music access, MusicSnapshotter reads it all from Music instead.
enum LibrarySnapshotter {
  /// Facts only Music knows (ITLibrary has no favorited/disliked flags).
  struct MusicFacts {
    var inLibrary: Set<String>
    var favorited: Set<String>
    var disliked: Set<String>
  }

  struct Result {
    var tracks: Int, playlists: Int, ms: Int, bytes: Int, usedMusic: Bool
    /// Phase timings for the log: ITLibrary load, Apple Events, build, encode + write.
    var phases: String
    /// "itlib", or "music" for MusicSnapshotter.
    var source = "itlib"
  }

  /// Runs on the music queue. nil when Music isn't running or refuses the events;
  /// the snapshot never launches Music.
  static func musicFacts(_ music: MusicAE) -> MusicFacts? {
    guard MusicAE.isAppRunning else { return nil }
    let lib = music.libraryPlaylist
    func pids(whose code: FourCharCode) throws -> Set<String> {
      let spec = music.whose(fcc("cTrk"), code, equals: NSAppleEventDescriptor(boolean: true), of: lib)
      return Set(MusicAE.strings(try music.get(music.prop(fcc("pPIS"), of: spec))))
    }
    do {
      let all = Set(MusicAE.strings(try music.get(music.prop(fcc("pPIS"), of: music.every(fcc("cTrk"), of: lib)))))
      return MusicFacts(inLibrary: all, favorited: try pids(whose: fcc("pLov")), disliked: try pids(whose: fcc("pHat")))
    } catch {
      return nil
    }
  }

  /// Builds the snapshot and writes it to `path` atomically. Runs on the library
  /// queue; the Apple Events go to `musicQueue` while ITLibrary loads.
  static func write(to path: String, music: MusicAE, musicQueue: DispatchQueue) throws -> Result {
    let started = DispatchTime.now()
    var marks: [(String, DispatchTime)] = []
    func mark(_ name: String) { marks.append((name, .now())) }
    var facts: MusicFacts?
    var factsMs = 0
    let factsDone = DispatchGroup()
    factsDone.enter()
    musicQueue.async {
      let t = DispatchTime.now()
      facts = musicFacts(music)
      factsMs = Int((DispatchTime.now().uptimeNanoseconds - t.uptimeNanoseconds) / 1_000_000)
      factsDone.leave()
    }

    let (snapshot, containers, albumArt) = try autoreleasepool { () throws -> (LibrarySnapshot, [String: String], [String: String]) in
      let lib: ITLibrary
      do {
        lib = try ITLibrary(apiVersion: "1.0")
        mark("load")
      } catch {
        factsDone.wait()
        throw Server.BridgeError.internalError("ITLibrary: \(error.localizedDescription)")
      }
      factsDone.wait()
      mark("wait")
      let built = build(lib, facts)
      mark("build")
      return built
    }

    let bytes = try SnapshotWriter.write(snapshot, to: path)
    mark("write")

    LibraryIndex.shared.update(containers: containers, albumArt: albumArt, usedMusic: facts != nil)
    var previous = started
    var phases = marks.map { name, t in
      defer { previous = t }
      return "\(name) \((t.uptimeNanoseconds - previous.uptimeNanoseconds) / 1_000_000)"
    }
    phases.append("events \(factsMs)")
    let ms = Int((DispatchTime.now().uptimeNanoseconds - started.uptimeNanoseconds) / 1_000_000)
    return Result(tracks: snapshot.tracks.count, playlists: snapshot.playlists.count, ms: ms,
                  bytes: bytes, usedMusic: facts != nil, phases: phases.joined(separator: ", "))
  }

  /// Name of the reusable queue playlist; never part of a snapshot.
  static let queuePlaylistName = "encore"

  private static func build(_ lib: ITLibrary, _ facts: MusicFacts?) -> (LibrarySnapshot, [String: String], [String: String]) {
    let playlists = lib.allPlaylists
    // Without Music, fall back to ITLibrary's own view: the primary playlist is
    // `library playlist 1`, and the Favorite Songs playlist holds the favorites.
    var inLibrary = facts?.inLibrary ?? []
    var favorited = facts?.favorited ?? []
    if facts == nil, let primary = playlists.first(where: { $0.isPrimary }) {
      inLibrary = Set(primary.items.map { persistentHex($0.persistentID) })
    }
    // The Apple Event only covers library tracks; favorites that live only in a
    // playlist come from the Favorite Songs playlist.
    if let loved = playlists.first(where: { $0.distinguishedKind == .kindLovedSongs }) {
      for item in loved.items {
        let pid = persistentHex(item.persistentID)
        if facts == nil || !inLibrary.contains(pid) { favorited.insert(pid) }
      }
    }
    let disliked = facts?.disliked ?? []

    let cloudType = CloudType()
    let storeItemID = StoreItemID()
    var tracks: [SnapshotTrack] = []
    var pidByID: [UInt64: String] = [:]
    var albumArt: [String: String] = [:] // album pid → a track with artwork, for the Apple Event fallback
    tracks.reserveCapacity(lib.allMediaItems.count)
    pidByID.reserveCapacity(lib.allMediaItems.count)

    for item in lib.allMediaItems where item.mediaKind == .kindSong {
      let id = item.persistentID.uint64Value
      let pid = persistentHex(id)
      pidByID[id] = pid
      let album = item.album
      let albumID = album.persistentID.uint64Value
      let cloud = cloudType.value(of: item)

      var t = SnapshotTrack()
      t.pid = pid
      t.title = item.title
      t.artist = item.artist?.name ?? ""
      t.albumArtist = album.albumArtist ?? ""
      t.album = album.title ?? ""
      t.albumPid = albumID == 0 ? "" : persistentHex(albumID)
      t.genre = item.genre
      t.composer = item.composer
      t.year = item.year
      t.track = item.trackNumber
      t.trackCount = album.trackCount
      t.disc = album.discNumber
      t.discCount = album.discCount
      t.duration = Int64(item.totalTime)
      t.added = unixSeconds(item.addedDate)
      t.lastPlayed = unixSeconds(item.lastPlayedDate)
      t.plays = item.playCount
      t.rating = item.isRatingComputed ? 0 : max(0, min(100, item.rating))
      t.favorited = favorited.contains(pid)
      t.disliked = disliked.contains(pid)
      t.explicit = item.lyricsContentRating == .explicit
      t.compilation = album.isCompilation
      t.inLibrary = inLibrary.contains(pid)
      t.available = cloud != CloudType.noLongerAvailable
      t.kind = kind(of: item, cloudType: cloud)
      t.art = item.hasArtworkAvailable
      if t.art && !t.albumPid.isEmpty && albumArt[t.albumPid] == nil { albumArt[t.albumPid] = pid }
      t.sortTitle = item.sortTitle ?? ""
      t.sortArtist = item.artist?.sortName ?? ""
      t.sortAlbum = album.sortTitle ?? ""
      t.sortAlbumArtist = album.sortAlbumArtist ?? ""
      t.storeId = storeItemID.value(of: item)
      tracks.append(t)
    }

    let globalID = PlaylistGlobalID()
    var out: [SnapshotPlaylist] = []
    var containers: [String: String] = [:]
    for p in playlists where !p.isPrimary && p.isVisible && p.name != queuePlaylistName {
      guard let special = special(p.distinguishedKind) else { continue }
      let pid = persistentHex(p.persistentID)
      let items = p.items.compactMap { pidByID[$0.persistentID.uint64Value] }
      for item in items where !inLibrary.contains(item) && containers[item] == nil {
        containers[item] = pid
      }
      let gid = globalID.value(of: p)
      out.append(SnapshotPlaylist(
        pid: pid,
        parentPid: p.parentID.map(persistentHex) ?? "",
        name: p.name,
        kind: kind(of: p, globalID: gid),
        special: special,
        items: items,
        globalId: gid.flatMap { $0.isEmpty ? nil : $0 }))
    }

    let snapshot = LibrarySnapshot(generatedAt: Int64(Date().timeIntervalSince1970), tracks: tracks, playlists: out)
    return (snapshot, containers, albumArt)
  }

  private static func unixSeconds(_ date: Date?) -> Int64 {
    guard let date else { return 0 }
    return max(0, Int64(date.timeIntervalSince1970))
  }

  /// Track kind from the private `cloudType`: 8 subscription, 9 subscription that
  /// is no longer available, 3 uploaded, 2 matched, 1 purchased.
  private static func kind(of item: ITLibMediaItem, cloudType: Int?) -> String {
    if item.isPurchased { return "purchased" }
    switch cloudType {
    case 8, 9: return "subscription"
    case 1: return "purchased"
    case 2, 3: return "uploaded"
    default:
      if item.locationType == .file { return "local" }
      return item.isCloud ? "uploaded" : "local"
    }
  }

  /// Playlist kind. Apple Music catalog playlists added to the library are plain
  /// regular playlists in ITLibrary; their cloud ID ("pl.<hex>") gives them away,
  /// while the user's own playlists have "pl.u-..." IDs.
  private static func kind(of p: ITLibPlaylist, globalID: String?) -> String {
    switch p.kind {
    case .smart: return "smart"
    case .folder: return "folder"
    case .genius, .geniusMix: return "genius"
    default:
      if let g = globalID, g.hasPrefix("pl."), !g.hasPrefix("pl.u-") { return "subscription" }
      return "user"
    }
  }

  /// `special` for a distinguished playlist, nil for non-music ones (movies, TV,
  /// audiobooks, podcasts, music videos...), which the snapshot omits.
  private static func special(_ kind: ITLibDistinguishedPlaylistKind) -> String? {
    switch kind {
    case .kindNone: return ""
    case .kindMusic: return "music"
    case .kindPurchases: return "purchased"
    case .kindLovedSongs: return "favorites"
    case .kindMyTopRated: return "topRated"
    case .kindRecentlyPlayed: return "recentlyPlayed"
    case .kindRecentlyAdded: return "recentlyAdded"
    case .kindClassicalMusic: return "classical"
    default: return nil
    }
  }
}

/// Reads the private `cloudType` of ITLibMediaItem when this OS has it. KVC on a
/// missing key raises an Objective-C exception Swift can't catch, so check the
/// selector once first.
private struct CloudType {
  static let noLongerAvailable = 9
  private let supported = ITLibMediaItem.instancesRespond(to: NSSelectorFromString("cloudType"))

  func value(of item: ITLibMediaItem) -> Int? {
    guard supported else { return nil }
    return (item.value(forKey: "cloudType") as? NSNumber)?.intValue
  }
}

/// Reads the private `storeItemID` of ITLibMediaItem: the song's Apple Music catalog
/// id, the same number the iTunes Search API calls `trackId`. 0 or missing for
/// local files and uploads. Same selector check as CloudType.
private struct StoreItemID {
  private let supported = ITLibMediaItem.instancesRespond(to: NSSelectorFromString("storeItemID"))

  func value(of item: ITLibMediaItem) -> Int64? {
    guard supported, let n = item.value(forKey: "storeItemID") as? NSNumber else { return nil }
    let id = n.int64Value
    return id > 0 ? id : nil
  }
}

/// The private `globalID` of ITLibPlaylist (the cloud library ID), when present.
private struct PlaylistGlobalID {
  private let supported = ITLibPlaylist.instancesRespond(to: NSSelectorFromString("globalID"))

  func value(of playlist: ITLibPlaylist) -> String? {
    guard supported else { return nil }
    return playlist.value(forKey: "globalID") as? String
  }
}

/// Detects library changes cheaply: the size and modification time of Music's
/// database file, or, for a library stored elsewhere, a signature of a fresh
/// ITLibrary load. Runs on the library queue.
final class LibraryWatcher: @unchecked Sendable {
  private let server: Server
  private var last: String?
  private var timer: DispatchSourceTimer?

  private static let database = FileManager.default.homeDirectoryForCurrentUser
    .appendingPathComponent("Music/Music/Music Library.musiclibrary/Library.musicdb")

  init(server: Server) { self.server = server }

  func start(interval: TimeInterval = 60) {
    server.library.async { [weak self] in
      guard let self else { return }
      self.last = Self.signature()
      let timer = DispatchSource.makeTimerSource(queue: self.server.library)
      timer.setEventHandler { [weak self] in self?.check() }
      timer.schedule(deadline: .now() + interval, repeating: interval, leeway: .seconds(2))
      timer.resume()
      self.timer = timer
    }
  }

  /// Emits `library` when the signature changed, or when `force` is set.
  func check(force: Bool = false) {
    let now = Self.signature()
    let changed = now != nil && now != last
    if now != nil { last = now }
    if changed || force { server.emit("library", [String: Any]()) }
  }

  /// `musicRunning` matters only for a library stored elsewhere, which has no
  /// cheap signal: reading all of it costs about half a second of CPU and a few
  /// MB every time, and Music is the only thing that writes it, so it is left
  /// alone (nil, no information) while Music isn't running. The launch check
  /// reads it once Music is up.
  static func signature(musicRunning: Bool = MusicAE.isAppRunning) -> String? {
    if let attrs = try? FileManager.default.attributesOfItem(atPath: database.path),
       let modified = attrs[.modificationDate] as? Date, let size = attrs[.size] as? NSNumber {
      return "file:\(modified.timeIntervalSince1970):\(size)"
    }
    guard musicRunning else { return nil }
    return autoreleasepool {
      guard let lib = try? ITLibrary(apiVersion: "1.0") else { return nil }
      let items = lib.allMediaItems
      let newest = items.reduce(0.0) { max($0, $1.modifiedDate?.timeIntervalSince1970 ?? 0) }
      return "itlib:\(items.count):\(lib.allPlaylists.count):\(newest)"
    }
  }
}

import Foundation

/// `library.snapshot` without ITLibrary, for when Media & Apple Music access is
/// denied: everything comes from Music in bulk Apple Events. Library tracks take one
/// event per property, each returning that property for every track; tracks only in
/// a playlist (an Apple Music playlist's) take one `properties of every track` event
/// for that playlist. Each event costs ~8 ms however much it returns. Music has no album
/// persistent ID to give, so albumPid stays empty and encore keys albums by album
/// artist and title. Explicit flags and artwork presence aren't available either.
enum MusicSnapshotter {
  /// Track properties, one Apple Event each.
  private static let columns: [FourCharCode] = ([
    "pPIS", "pMdK", "pnam", "pArt", "pAlA", "pAlb", "pGen", "pCmp", "pYr ", "pTrN", "pTrC", "pDsN", "pDsC",
    "pDur", "pAdd", "pPlD", "pPlC", "pRte", "pRtk", "pLov", "pHat", "pClS", "pAnt", "pSNm", "pSAr", "pSAl", "pSAA",
  ] as [StaticString]).map(fcc)

  /// Runs on the library queue; each Apple Event goes through `musicQueue`, so player
  /// commands slip in between them. Never launches Music.
  static func write(to path: String, music: MusicAE, musicQueue: DispatchQueue) throws -> LibrarySnapshotter.Result {
    guard MusicAE.isAppRunning else {
      // Lets the Music launch handler know this snapshot is worth redoing.
      LibraryIndex.shared.update(containers: [:], albumArt: [:], usedMusic: false)
      throw Server.BridgeError.internalError("Music isn't running, and encore can't read the library without it")
    }
    let started = DispatchTime.now()
    var marks: [(String, DispatchTime)] = []
    func mark(_ name: String) { marks.append((name, .now())) }
    func get(_ spec: NSAppleEventDescriptor) throws -> [NSAppleEventDescriptor] {
      do {
        return list(try musicQueue.sync { try music.send(fcc("core"), fcc("getd"), direct: spec, timeout: 30) })
      } catch let e as MusicAEError where e.code == -1728 {
        return [] // an empty container has no `every track`
      }
    }

    // Library tracks.
    let libTracks = music.every(fcc("cTrk"), of: music.libraryPlaylist)
    var tracks: [SnapshotTrack] = []
    var known = Set<String>()
    for t in try columnRows(of: libTracks, music, get) {
      tracks.append(t)
      known.insert(t.pid)
    }
    for i in tracks.indices { tracks[i].inLibrary = true }
    let inLibrary = known
    mark("tracks")

    // Playlists.
    let all = music.every(fcc("cPly"), of: nil)
    let pids = try get(music.prop(fcc("pPIS"), of: all)).map { $0.stringValue ?? "" }
    let ids = try get(music.prop(fcc("ID  "), of: all)).map { $0.int32Value }
    let classes = try get(music.prop(fcc("pcls"), of: all)).map { $0.typeCodeValue }
    let names = try get(music.prop(fcc("pnam"), of: all)).map { $0.stringValue ?? "" }
    let specials = try get(music.prop(fcc("pSpK"), of: all)).map { $0.enumCodeValue }
    let visible = try get(music.prop(fcc("pvis"), of: all)).map { $0.booleanValue }
    guard [ids.count, classes.count, names.count, specials.count, visible.count].allSatisfy({ $0 == pids.count }) else {
      throw Server.BridgeError.internalError("the playlists changed while being read; try again")
    }
    // smart and genius only exist on user playlists.
    let users = music.every(fcc("cUsP"), of: nil)
    let userPids = try get(music.prop(fcc("pPIS"), of: users)).map { $0.stringValue ?? "" }
    let smart = Dictionary(zip(userPids, try get(music.prop(fcc("pSmt"), of: users)).map { $0.booleanValue }), uniquingKeysWith: { a, _ in a })
    let genius = Dictionary(zip(userPids, try get(music.prop(fcc("pGns"), of: users)).map { $0.booleanValue }), uniquingKeysWith: { a, _ in a })
    let hasFolders = classes.contains(fcc("cFoP"))

    var out: [SnapshotPlaylist] = []
    var containers: [String: String] = [:]
    var smartCandidates: [Int] = []
    var extra = 0
    for i in pids.indices {
      let special = specials[i]
      guard classes[i] != fcc("cLiP"), special != fcc("kSpL"), visible[i],
            names[i] != LibrarySnapshotter.queuePlaylistName else { continue }
      let spec = music.playlist(id: ids[i])
      var kind = "user"
      if classes[i] == fcc("cFoP") || special == fcc("kSpF") {
        kind = "folder"
      } else if classes[i] == fcc("cSuP") {
        kind = "subscription"
      } else if genius[pids[i]] == true || special == fcc("kSpG") {
        kind = "genius"
      } else if smart[pids[i]] == true {
        kind = "smart"
      }
      var parent = ""
      if hasFolders {
        // A playlist without a parent answers -1728.
        parent = (try? musicQueue.sync { try music.get(music.prop(fcc("pPIS"), of: music.prop(fcc("pPlP"), of: spec))) })??.stringValue ?? ""
      }
      var items: [String] = []
      if kind != "folder" {
        let raw = try get(music.prop(fcc("pPIS"), of: music.every(fcc("cTrk"), of: spec))).compactMap { $0.stringValue }
        // Tracks that aren't in the library (an Apple Music playlist's) need their own read.
        if raw.contains(where: { !known.contains($0) }) {
          let records = try get(music.prop(fcc("pALL"), of: music.every(fcc("cTrk"), of: spec)))
          for t in records.compactMap({ r in track { r.forKeyword($0) } }) where !known.contains(t.pid) {
            tracks.append(t)
            known.insert(t.pid)
            extra += 1
          }
        }
        items = raw.filter { known.contains($0) }
        // A smart playlist of only videos (Music Videos) isn't a music playlist.
        if kind == "smart" && special == fcc("kNon") && !raw.isEmpty && items.isEmpty { continue }
      }
      for pid in items where !inLibrary.contains(pid) && containers[pid] == nil { containers[pid] = pids[i] }
      if kind == "smart" && special == fcc("kNon") { smartCandidates.append(out.count) }
      out.append(SnapshotPlaylist(pid: pids[i], parentPid: parent, name: names[i], kind: kind,
                                  special: specialName(special), items: items))
    }
    markFavorites(&out, among: smartCandidates, tracks: tracks)
    mark("playlists")

    let snapshot = LibrarySnapshot(generatedAt: Int64(Date().timeIntervalSince1970), tracks: tracks, playlists: out)
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.withoutEscapingSlashes]
    let data = try encoder.encode(snapshot)
    try data.write(to: URL(fileURLWithPath: path), options: .atomic)
    mark("write")

    LibraryIndex.shared.update(containers: containers, albumArt: [:], usedMusic: true)
    var previous = started
    var phases = marks.map { name, t in
      defer { previous = t }
      return "\(name) \((t.uptimeNanoseconds - previous.uptimeNanoseconds) / 1_000_000)"
    }
    phases.append("\(extra) playlist-only tracks")
    let ms = Int((DispatchTime.now().uptimeNanoseconds - started.uptimeNanoseconds) / 1_000_000)
    return LibrarySnapshotter.Result(tracks: tracks.count, playlists: out.count, ms: ms, bytes: data.count,
                                     usedMusic: true, phases: phases.joined(separator: ", "), source: "music")
  }

  /// The songs of `container`, one Apple Event per property.
  private static func columnRows(of container: NSAppleEventDescriptor, _ music: MusicAE,
                                 _ get: (NSAppleEventDescriptor) throws -> [NSAppleEventDescriptor]) throws -> [SnapshotTrack] {
    var cols: [[NSAppleEventDescriptor]] = []
    for attempt in 0..<2 {
      cols = try columns.map { try get(music.prop($0, of: container)) }
      if cols.allSatisfy({ $0.count == cols[0].count }) { break }
      // The container changed between two events; read it again once.
      if attempt == 1 { throw Server.BridgeError.internalError("the library changed while being read; try again") }
    }
    let at = Dictionary(uniqueKeysWithValues: columns.enumerated().map { ($1, $0) })
    return cols[0].indices.compactMap { i in track { at[$0].map { cols[$0][i] } } }
  }

  /// A snapshot track from its Music properties, nil unless it's a song (videos are
  /// left out, as with ITLibrary). `field` returns nil for a property it lacks.
  private static func track(_ field: (FourCharCode) -> NSAppleEventDescriptor?) -> SnapshotTrack? {
    func f(_ c: StaticString) -> NSAppleEventDescriptor? {
      guard let d = field(fcc(c)), !d.isMissingValue else { return nil }
      return d
    }
    func text(_ c: StaticString) -> String { f(c)?.stringValue ?? "" }
    func int(_ c: StaticString) -> Int { f(c).map { Int($0.int32Value) } ?? 0 }
    func bool(_ c: StaticString) -> Bool { f(c)?.booleanValue ?? false }
    func unix(_ c: StaticString) -> Int64 { f(c).map(unixSeconds) ?? 0 }

    guard f("pMdK")?.enumCodeValue == fcc("kMdS") else { return nil }
    var t = SnapshotTrack()
    t.pid = text("pPIS")
    if t.pid.isEmpty { return nil }
    t.title = text("pnam")
    t.artist = text("pArt")
    t.albumArtist = text("pAlA")
    t.album = text("pAlb")
    t.genre = text("pGen")
    t.composer = text("pCmp")
    t.year = int("pYr ")
    t.track = int("pTrN")
    t.trackCount = int("pTrC")
    t.disc = int("pDsN")
    t.discCount = int("pDsC")
    t.duration = f("pDur").map { Int64(($0.doubleValue * 1000).rounded()) } ?? 0
    t.added = unix("pAdd")
    t.lastPlayed = unix("pPlD")
    t.plays = int("pPlC")
    t.rating = f("pRtk")?.enumCodeValue == fcc("kRtC") ? 0 : max(0, min(100, int("pRte")))
    t.favorited = bool("pLov")
    t.disliked = bool("pHat")
    let cloud = f("pClS")?.enumCodeValue ?? 0
    t.kind = kind(cloud)
    t.available = cloud != fcc("kRev")
    t.compilation = bool("pAnt")
    t.sortTitle = text("pSNm")
    t.sortArtist = text("pSAr")
    t.sortAlbum = text("pSAl")
    t.sortAlbumArtist = text("pSAA")
    t.art = true // unknown here; a request for art that isn't there just comes back empty
    return t
  }

  /// The Favorite Songs playlist is a plain smart playlist to Apple Events. It's the
  /// one that holds (nearly) exactly the favorited songs.
  private static func markFavorites(_ playlists: inout [SnapshotPlaylist], among candidates: [Int], tracks: [SnapshotTrack]) {
    let favorited = Set(tracks.lazy.filter(\.favorited).map(\.pid))
    guard !favorited.isEmpty else { return }
    var best: (index: Int, overlap: Int)?
    for i in candidates {
      let items = playlists[i].items
      let overlap = items.reduce(0) { $0 + (favorited.contains($1) ? 1 : 0) }
      guard Double(overlap) >= 0.9 * Double(items.count), Double(overlap) >= 0.9 * Double(favorited.count) else { continue }
      if overlap > best?.overlap ?? 0 { best = (i, overlap) }
    }
    if let best { playlists[best.index].special = "favorites" }
  }

  private static func specialName(_ code: FourCharCode) -> String {
    switch code {
    case fcc("kSpZ"): return "music"
    case fcc("kSpM"): return "purchased"
    default: return ""
    }
  }

  /// Track kind from `cloud status`.
  private static func kind(_ cloud: FourCharCode) -> String {
    switch cloud {
    case fcc("kSub"), fcc("kRev"): return "subscription"
    case fcc("kPur"): return "purchased"
    case fcc("kMat"), fcc("kUpl"): return "uploaded"
    default: return "local"
    }
  }

  /// Unix seconds of an Apple Event date: local wall-clock seconds since 1904, which
  /// Music converts with today's UTC offset whatever the date. `dateValue` applies the
  /// offset in effect at that date instead and is an hour off across a daylight saving
  /// change, so undo Music's conversion here.
  private static func unixSeconds(_ d: NSAppleEventDescriptor) -> Int64 {
    guard let ldt = d.coerce(toDescriptorType: fcc("ldt ")), ldt.data.count == 8 else {
      return d.dateValue.map { max(0, Int64($0.timeIntervalSince1970)) } ?? 0
    }
    let since1904 = ldt.data.withUnsafeBytes { Int64(littleEndian: $0.loadUnaligned(as: Int64.self)) }
    return max(0, since1904 - 2_082_844_800 - Int64(TimeZone.current.secondsFromGMT()))
  }

  private static func list(_ desc: NSAppleEventDescriptor?) -> [NSAppleEventDescriptor] {
    guard let desc else { return [] }
    if desc.descriptorType != fcc("list") { return [desc] }
    guard desc.numberOfItems > 0 else { return [] }
    return (1...desc.numberOfItems).compactMap { desc.atIndex($0) }
  }
}

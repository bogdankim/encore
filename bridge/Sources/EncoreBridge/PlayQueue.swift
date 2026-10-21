import Foundation

/// The reusable user playlist named "encore" that `player.playTracks` fills and plays.
///
/// Music snapshots a playlist when `play` is sent, so the playlist only has to be
/// complete at that moment; later edits don't affect what is playing. Only used
/// from the music queue.
final class PlayQueue {
  static let name = "encore"

  private let music: MusicAE
  private var pid: String?

  init(music: MusicAE) { self.music = music }

  /// Replaces the playlist's contents with `pids` in order and plays it from the
  /// first track. `containers` maps tracks that aren't in `library playlist 1` to
  /// a playlist that holds them. Returns the pids Music could not resolve; throws
  /// when none could be queued.
  func play(_ pids: [String], containers: [String: String]) throws -> [String] {
    let q = try clearedPlaylist()
    var containerSpecs: [String: NSAppleEventDescriptor] = [:]
    for p in pids {
      var container: NSAppleEventDescriptor?
      if let c = containers[p] {
        container = containerSpecs[c] ?? music.playlist(persistentID: c)
        containerSpecs[c] = container
      }
      // Fire and forget: Music handles events in order, so the count below waits for all of them.
      try music.sendNoReply(fcc("core"), fcc("clon"), direct: music.track(persistentID: p, in: container),
                            params: [fcc("insh"): q])
    }
    let count = try countTracks(q, timeout: 30)
    if count == 0 { throw MusicAEError(code: -1728, message: "none of the \(pids.count) tracks could be queued") }
    var missing: [String] = []
    if count != pids.count {
      let queued = Set(try queuedPIDs(q))
      missing = pids.filter { !queued.contains($0) }
    }
    try music.play(q)
    return missing
  }

  /// The queue playlist, emptied. Resolves (or creates) it when the cached pid is gone.
  private func clearedPlaylist() throws -> NSAppleEventDescriptor {
    var q: NSAppleEventDescriptor
    var count: Int
    if let pid, let n = try? countTracks(music.playlist(persistentID: pid)) {
      q = music.playlist(persistentID: pid)
      count = n
    } else {
      q = try resolve()
      count = try countTracks(q)
    }
    if count > 0 { try music.send(fcc("core"), fcc("delo"), direct: music.every(fcc("cTrk"), of: q)) }
    return q
  }

  private func resolve() throws -> NSAppleEventDescriptor {
    let byName = music.element(fcc("cUsP"), form: fcc("name"), NSAppleEventDescriptor(string: PlayQueue.name))
    let found: String
    do {
      guard let p = try music.get(music.prop(fcc("pPIS"), of: byName))?.stringValue else {
        throw MusicAEError(code: -1728, message: "queue playlist has no persistent ID")
      }
      found = p
    } catch let e as MusicAEError where e.code == -1728 {
      let props = NSAppleEventDescriptor.record()
      props.setDescriptor(NSAppleEventDescriptor(string: PlayQueue.name), forKeyword: fcc("pnam"))
      guard let created = try music.send(fcc("core"), fcc("crel"),
                                         params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cUsP")), fcc("prdt"): props]),
            let p = try music.get(music.prop(fcc("pPIS"), of: created))?.stringValue else {
        throw MusicAEError(code: -1728, message: "could not create the queue playlist")
      }
      found = p
    }
    pid = found
    return music.playlist(persistentID: found)
  }

  private func countTracks(_ container: NSAppleEventDescriptor, timeout: TimeInterval? = nil) throws -> Int {
    let n = try music.send(fcc("core"), fcc("cnte"), direct: container,
                           params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cTrk"))], timeout: timeout)
    return Int(n?.int32Value ?? 0)
  }

  private func queuedPIDs(_ q: NSAppleEventDescriptor) throws -> [String] {
    guard let l = try music.get(music.prop(fcc("pPIS"), of: music.every(fcc("cTrk"), of: q))), l.numberOfItems > 0 else { return [] }
    return (1...l.numberOfItems).compactMap { l.atIndex($0)?.stringValue }
  }
}

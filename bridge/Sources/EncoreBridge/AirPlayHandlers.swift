import Foundation

/// `airplay.list`, `airplay.select`, `airplay.volume` and the `airplay` event.
///
/// Music answers AirPlay queries from a cache that lives about a second; past that,
/// any AirPlay property read or write costs ~500 ms. That wait doesn't hold up other
/// events to Music, so AirPlay work runs on its own serial queue instead of the
/// music queue, and `airplay.list` answers from our last read while it refreshes.
enum AirPlayHandlers {
  static func register(_ server: Server, _ music: MusicAE) {
    let queue = DispatchQueue(label: "encore.airplay")
    let watcher = AirPlayWatcher(server: server, music: music, queue: queue)

    server.register("airplay.list", on: queue) { _ in
      watcher.watch()
      return ["devices": try watcher.list().map(\.json)]
    }

    server.register("airplay.select", on: queue) { p in
      guard let raw = p["ids"] as? [NSNumber], !raw.isEmpty else {
        throw Server.BridgeError.badRequest("ids must be a non-empty list of AirPlay device ids")
      }
      let ids = raw.map(\.int32Value)
      // Re-setting an unchanged route still costs ~500 ms while playing.
      let current = try music.airPlayList().filter(\.selected).map(\.id)
      if Set(current) == Set(ids) { return [String: Any]() }
      do {
        try music.selectAirPlay(ids: ids)
      } catch let e as MusicAEError where e.code != -1728 {
        server.log("set current AirPlay devices failed (\(e.code)); selecting device by device")
        try music.selectAirPlayOneByOne(ids: ids)
      }
      // Reads right after a set can still return the old selection, so report what we wrote.
      watcher.changed { d in d.selected = ids.contains(d.id) }
      return [String: Any]()
    }

    server.register("airplay.volume", on: queue) { p in
      let id = Int32(try p.int("id"))
      let volume = Int32(max(0, min(100, try p.int("volume"))))
      try music.set(music.prop(fcc("pVol"), of: music.airPlayDevice(id: id)), to: NSAppleEventDescriptor(int32: volume))
      watcher.changed { d in if d.id == id { d.volume = volume } }
      return [String: Any]()
    }

    // Fill the cache now so the picker opens without the 500 ms wait.
    queue.async { watcher.refresh() }

    let repairer = AirPlayRepairer(server: server, music: music, watcher: watcher, queue: queue,
                                   enabled: !CommandLine.arguments.contains("--no-airplay-reconnect"))
    watcher.onPublish = { repairer.routeChanged() }
    // PlayerMonitor's state belongs to the music queue.
    server.music.async {
      PlayerMonitor.shared.onChange = { state, track in queue.async { repairer.playerChanged(state: state, track: track) } }
    }
    queue.async { repairer.start() }
  }
}

/// Keeps the last AirPlay list, polls every 5 s while a client has called
/// `airplay.list` in the last 30 s, and emits `airplay` when the list differs from
/// the last one sent. Every method runs on the AirPlay queue.
final class AirPlayWatcher {
  private let server: Server
  private let music: MusicAE
  private let queue: DispatchQueue
  private var last: [MusicAE.AirPlayEntry]?
  private var watchUntil = DispatchTime.now()
  private var lastWrite: DispatchTime?
  private var timer: DispatchSourceTimer?
  /// Called after every `airplay` event, on the AirPlay queue.
  var onPublish: (() -> Void)?

  init(server: Server, music: MusicAE, queue: DispatchQueue) {
    self.server = server
    self.music = music
    self.queue = queue
  }

  /// The last list read, refreshed right after this call returns (an `airplay`
  /// event follows if it changed). Reads synchronously when there is none yet.
  func list() throws -> [MusicAE.AirPlayEntry] {
    if let last {
      queue.async { self.refresh() }
      return last
    }
    let devices = try music.airPlayList()
    last = devices
    return devices
  }

  func watch() {
    watchUntil = .now() + 30
    guard timer == nil else { return }
    let t = DispatchSource.makeTimerSource(queue: queue)
    t.schedule(deadline: .now() + 5, repeating: 5, leeway: .milliseconds(500))
    t.setEventHandler { [weak self] in self?.poll() }
    t.resume()
    timer = t
  }

  /// Re-reads the list and emits it when it changed. The first read only fills the cache.
  func refresh() {
    // A read soon after our own write can still return the state from before it.
    if let lastWrite, DispatchTime.now() < lastWrite + 2 { return }
    guard let devices = try? music.airPlayList() else { return }
    if last == nil {
      last = devices
    } else if devices != last {
      publish(devices)
    }
  }

  /// After our own write: emit the fresh list with `apply` patched in.
  func changed(_ apply: (inout MusicAE.AirPlayEntry) -> Void) {
    lastWrite = .now()
    guard var devices = try? music.airPlayList() else { return }
    for i in devices.indices { apply(&devices[i]) }
    publish(devices)
  }

  private func poll() {
    if DispatchTime.now() > watchUntil {
      timer?.cancel()
      timer = nil
      return
    }
    refresh()
  }

  private func publish(_ devices: [MusicAE.AirPlayEntry]) {
    last = devices
    server.emit("airplay", ["devices": devices.map(\.json)])
    onPublish?()
  }
}

extension MusicAE {
  struct AirPlayEntry: Equatable {
    var id: Int32
    /// Stable across Music's re-enumerations, unlike `id`.
    var pid: String
    var name: String
    var kind: String
    var selected: Bool
    var active: Bool
    var available: Bool
    var volume: Int32

    var json: [String: Any] {
      ["id": Int(id), "pid": pid, "name": name, "kind": kind, "selected": selected,
       "active": active, "available": available, "volume": Int(volume)]
    }
  }

  /// `kind` enumeration (eAPD) from Music's sdef, mapped to the protocol's kinds.
  static func airPlayKind(_ code: FourCharCode) -> String {
    switch code {
    case fcc("kAPC"): return "computer"
    case fcc("kAPH"): return "homepod"
    case fcc("kAPT"), fcc("kAPV"): return "tv"
    case fcc("kAPX"), fcc("kAPO"), fcc("kAPB"): return "speaker"
    default: return "unknown"
    }
  }

  func airPlayDevice(id: Int32) -> NSAppleEventDescriptor {
    element(fcc("cAPD"), form: fcc("ID  "), NSAppleEventDescriptor(int32: id))
  }

  /// Selects through each device's `selected`: new devices first, then the rest are
  /// deselected, so the output is never left with nothing selected in between.
  func selectAirPlayOneByOne(ids: [Int32]) throws {
    for id in ids { try set(prop(fcc("selc"), of: airPlayDevice(id: id)), to: NSAppleEventDescriptor(boolean: true)) }
    for d in try airPlayList() where d.selected && !ids.contains(d.id) {
      try set(prop(fcc("selc"), of: airPlayDevice(id: d.id)), to: NSAppleEventDescriptor(boolean: false))
    }
  }

  /// `properties of every AirPlay device`: one event; ~2 ms within a second of the last AirPlay read, ~500 ms otherwise.
  func airPlayList() throws -> [AirPlayEntry] {
    guard let l = try get(prop(fcc("pALL"), of: every(fcc("cAPD"), of: nil))), l.numberOfItems > 0 else { return [] }
    return (1...l.numberOfItems).compactMap { i in
      guard let r = l.atIndex(i) else { return nil }
      let id = r.forKeyword(fcc("ID  "))?.int32Value ?? 0
      return AirPlayEntry(
        id: id,
        pid: r.forKeyword(fcc("pPIS"))?.stringValue ?? "",
        name: r.forKeyword(fcc("pnam"))?.stringValue ?? "",
        kind: MusicAE.airPlayKind(r.forKeyword(fcc("pKnd"))?.enumCodeValue ?? 0),
        selected: r.forKeyword(fcc("selc"))?.booleanValue ?? false,
        active: r.forKeyword(fcc("pAct"))?.booleanValue ?? false,
        available: r.forKeyword(fcc("pAva"))?.booleanValue ?? false,
        volume: r.forKeyword(fcc("pVol"))?.int32Value ?? 0)
    }
  }
}

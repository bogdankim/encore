// MusicAE.swift: minimal raw Apple Event client for Music.app (no AppleScript compiler in the hot path).
// Verified on macOS 27.0 / Music 1.7. Requires Automation permission for the host process
// (and NSAppleEventsUsageDescription in Info.plist if the bridge is a bundled app).
import Foundation

@inline(__always) func fcc(_ s: StaticString) -> FourCharCode {
  s.withUTF8Buffer { $0.reduce(0) { $0 << 8 | FourCharCode($1) } }
}
func fccString(_ c: FourCharCode) -> String {
  String(bytes: withUnsafeBytes(of: c.bigEndian, Array.init), encoding: .macOSRoman) ?? "????"
}

struct MusicAEError: Error, CustomStringConvertible {
  let code: Int; let message: String
  var description: String { "AE error \(code): \(message)" }
}

final class MusicAE: @unchecked Sendable {
  static let bundleID = "com.apple.Music"
  let target = NSAppleEventDescriptor(bundleIdentifier: MusicAE.bundleID)
  var timeout: TimeInterval = 3.0

  // MARK: object specifiers
  /// property `code` of `container` (nil container = application)
  func prop(_ code: FourCharCode, of container: NSAppleEventDescriptor? = nil) -> NSAppleEventDescriptor {
    let spec = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("obj "))!
    spec.setDescriptor(NSAppleEventDescriptor(typeCode: fcc("prop")), forKeyword: fcc("want"))
    spec.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("prop")), forKeyword: fcc("form"))
    spec.setDescriptor(NSAppleEventDescriptor(typeCode: code), forKeyword: fcc("seld"))
    spec.setDescriptor(container ?? NSAppleEventDescriptor.null(), forKeyword: fcc("from"))
    return spec
  }
  /// element of class `cls` by key form: index ('indx'), unique id ('ID  '), name ('name')
  func element(_ cls: FourCharCode, form: FourCharCode, _ key: NSAppleEventDescriptor, of container: NSAppleEventDescriptor? = nil) -> NSAppleEventDescriptor {
    let spec = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("obj "))!
    spec.setDescriptor(NSAppleEventDescriptor(typeCode: cls), forKeyword: fcc("want"))
    spec.setDescriptor(NSAppleEventDescriptor(enumCode: form), forKeyword: fcc("form"))
    spec.setDescriptor(key, forKeyword: fcc("seld"))
    spec.setDescriptor(container ?? NSAppleEventDescriptor.null(), forKeyword: fcc("from"))
    return spec
  }
  /// `every <cls> of container whose <propCode> = value` (formTest). Use with .first via index wrapper.
  func whose(_ cls: FourCharCode, _ propCode: FourCharCode, equals value: NSAppleEventDescriptor, of container: NSAppleEventDescriptor?) -> NSAppleEventDescriptor {
    // comparison descriptor: { relo: =, obj1: property propCode of 'exmn' (examined item), obj2: value }
    let its = NSAppleEventDescriptor(descriptorType: fcc("exmn"), data: nil)!
    let cmp = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("cmpd"))!
    cmp.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("=   ")), forKeyword: fcc("relo"))
    cmp.setDescriptor(prop(propCode, of: its), forKeyword: fcc("obj1"))
    cmp.setDescriptor(value, forKeyword: fcc("obj2"))
    let spec = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("obj "))!
    spec.setDescriptor(NSAppleEventDescriptor(typeCode: cls), forKeyword: fcc("want"))
    spec.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("test")), forKeyword: fcc("form"))
    spec.setDescriptor(cmp, forKeyword: fcc("seld"))
    spec.setDescriptor(container ?? NSAppleEventDescriptor.null(), forKeyword: fcc("from"))
    return spec
  }
  /// `every track of container whose persistent ID = a or persistent ID = b or ...` (one AE; result order = container order, NOT list order)
  func tracks(persistentIDs hexes: [String], in container: NSAppleEventDescriptor? = nil) -> NSAppleEventDescriptor {
    let its = NSAppleEventDescriptor(descriptorType: fcc("exmn"), data: nil)!
    let terms = NSAppleEventDescriptor.list()
    for (i, h) in hexes.enumerated() {
      let cmp = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("cmpd"))!
      cmp.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("=   ")), forKeyword: fcc("relo"))
      cmp.setDescriptor(prop(fcc("pPIS"), of: its), forKeyword: fcc("obj1"))
      cmp.setDescriptor(NSAppleEventDescriptor(string: h), forKeyword: fcc("obj2"))
      terms.insert(cmp, at: i + 1)
    }
    let test: NSAppleEventDescriptor
    if hexes.count == 1 { test = terms.atIndex(1)! } else {
      test = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("logi"))!
      test.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("OR  ")), forKeyword: fcc("logc"))
      test.setDescriptor(terms, forKeyword: fcc("term"))
    }
    let spec = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("obj "))!
    spec.setDescriptor(NSAppleEventDescriptor(typeCode: fcc("cTrk")), forKeyword: fcc("want"))
    spec.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("test")), forKeyword: fcc("form"))
    spec.setDescriptor(test, forKeyword: fcc("seld"))
    spec.setDescriptor(container ?? libraryPlaylist, forKeyword: fcc("from"))
    return spec
  }

  /// `first <cls> of (filtered spec)` = element index 1 of the test specifier
  func first(_ cls: FourCharCode, of spec: NSAppleEventDescriptor) -> NSAppleEventDescriptor {
    element(cls, form: fcc("indx"), NSAppleEventDescriptor(int32: 1), of: spec)
  }

  /// `every <cls> of container`
  func every(_ cls: FourCharCode, of container: NSAppleEventDescriptor?) -> NSAppleEventDescriptor {
    var all = fcc("all ")
    let abso = NSAppleEventDescriptor(descriptorType: fcc("abso"), bytes: &all, length: 4)!
    return element(cls, form: fcc("indx"), abso, of: container)
  }

  // Common specifiers
  var libraryPlaylist: NSAppleEventDescriptor { element(fcc("cLiP"), form: fcc("indx"), NSAppleEventDescriptor(int32: 1)) }
  var currentTrack: NSAppleEventDescriptor { prop(fcc("pTrk")) }
  func playlist(id: Int32) -> NSAppleEventDescriptor { element(fcc("cPly"), form: fcc("ID  "), NSAppleEventDescriptor(int32: id)) }
  func playlist(persistentID hex: String) -> NSAppleEventDescriptor {
    first(fcc("cPly"), of: whose(fcc("cPly"), fcc("pPIS"), equals: NSAppleEventDescriptor(string: hex), of: nil))
  }
  func track(persistentID hex: String, in container: NSAppleEventDescriptor? = nil) -> NSAppleEventDescriptor {
    first(fcc("cTrk"), of: whose(fcc("cTrk"), fcc("pPIS"), equals: NSAppleEventDescriptor(string: hex), of: container ?? libraryPlaylist))
  }
  func track(index: Int32, of container: NSAppleEventDescriptor) -> NSAppleEventDescriptor {
    element(fcc("cTrk"), form: fcc("indx"), NSAppleEventDescriptor(int32: index), of: container)
  }

  // MARK: sending
  @discardableResult
  func send(_ cls: FourCharCode, _ id: FourCharCode, direct: NSAppleEventDescriptor? = nil,
            params: [FourCharCode: NSAppleEventDescriptor] = [:], timeout: TimeInterval? = nil) throws -> NSAppleEventDescriptor? {
    try checkConsent()
    let ev = NSAppleEventDescriptor(eventClass: cls, eventID: id, targetDescriptor: target, returnID: -1, transactionID: 0)
    if let direct { ev.setParam(direct, forKeyword: fcc("----")) }
    for (k, v) in params { ev.setParam(v, forKeyword: k) }
    let reply: NSAppleEventDescriptor
    do {
      reply = try ev.sendEvent(options: [.waitForReply, .canInteract, MusicAE.noConsentPrompt], timeout: timeout ?? self.timeout)
    } catch let e as NSError {
      throw MusicAEError(code: e.code, message: e.localizedDescription)
    }
    // application-level errors come back in the reply as 'errn' / 'errs'
    if let errn = reply.paramDescriptor(forKeyword: fcc("errn")), errn.int32Value != 0 {
      throw MusicAEError(code: Int(errn.int32Value), message: reply.paramDescriptor(forKeyword: fcc("errs"))?.stringValue ?? "")
    }
    return reply.paramDescriptor(forKeyword: fcc("----"))
  }
  /// fire-and-forget (no reply). Music processes events in order, so a later synchronous call acts as a barrier.
  func sendNoReply(_ cls: FourCharCode, _ id: FourCharCode, direct: NSAppleEventDescriptor? = nil, params: [FourCharCode: NSAppleEventDescriptor] = [:]) throws {
    try checkConsent()
    let ev = NSAppleEventDescriptor(eventClass: cls, eventID: id, targetDescriptor: target, returnID: -1, transactionID: 0)
    if let direct { ev.setParam(direct, forKeyword: fcc("----")) }
    for (k, v) in params { ev.setParam(v, forKeyword: k) }
    _ = try ev.sendEvent(options: [.noReply, MusicAE.noConsentPrompt], timeout: timeout)
  }
  /// kAEDoNotPromptForUserConsent: an event that would need the Automation prompt
  /// fails at once with -1744 instead of showing it and waiting for the answer.
  /// Only Permissions prompts, once, at a time it chooses.
  static let noConsentPrompt = NSAppleEventDescriptor.SendOptions(rawValue: 0x0002_0000)

  /// While Automation is being checked or asked for, fail at once with -1744
  /// instead of waiting behind the prompt. While Music is launching and TCC can't
  /// answer for it yet, fail with -600, which launch waits retry.
  private func checkConsent() throws {
    switch Permissions.shared.automationHold {
    case .none: return
    case .launching: throw MusicAEError(code: -600, message: "Music is starting")
    case .checking, .prompting: throw MusicAEError(code: -1744, message: "waiting for permission to control Music")
    }
  }
  func get(_ spec: NSAppleEventDescriptor) throws -> NSAppleEventDescriptor? { try send(fcc("core"), fcc("getd"), direct: spec) }
  func set(_ spec: NSAppleEventDescriptor, to value: NSAppleEventDescriptor) throws {
    try send(fcc("core"), fcc("setd"), direct: spec, params: [fcc("data"): value])
  }

  // MARK: player commands (codes from Music.sdef)
  func play(_ spec: NSAppleEventDescriptor? = nil) throws { try send(fcc("hook"), fcc("Play"), direct: spec) }
  func playPause() throws { try send(fcc("hook"), fcc("PlPs")) }
  func pause() throws { try send(fcc("hook"), fcc("Paus")) }
  func stop() throws { try send(fcc("hook"), fcc("Stop")) }
  func nextTrack() throws { try send(fcc("hook"), fcc("Next")) }
  func previousTrack() throws { try send(fcc("hook"), fcc("Prev")) }
  func backTrack() throws { try send(fcc("hook"), fcc("Back")) }
  func seek(to seconds: Double) throws { try set(prop(fcc("pPos")), to: NSAppleEventDescriptor(double: seconds)) }
  func setShuffle(_ on: Bool) throws { try set(prop(fcc("pShE")), to: NSAppleEventDescriptor(boolean: on)) }
  /// repeat: "kRpO" off, "kRp1" one, "kAll" all
  func setRepeat(_ code: FourCharCode) throws { try set(prop(fcc("pRpt")), to: NSAppleEventDescriptor(enumCode: code)) }
  func setVolume(_ v: Int32) throws { try set(prop(fcc("pVol")), to: NSAppleEventDescriptor(int32: v)) }

  // MARK: state snapshot (one AE per field, ~8 ms each on this machine)
  struct Snapshot { var state: String; var position: Double?; var name: String?; var artist: String?; var album: String?; var duration: Double?; var persistentID: String?; var databaseID: Int32? }
  func snapshot() throws -> Snapshot {
    let st = try get(prop(fcc("pPlS")))?.enumCodeValue ?? 0
    let state = [fcc("kPSS"): "stopped", fcc("kPSP"): "playing", fcc("kPSp"): "paused", fcc("kPSF"): "fastForwarding", fcc("kPSR"): "rewinding"][st] ?? fccString(st)
    var s = Snapshot(state: state)
    let pos = try get(prop(fcc("pPos")))
    if let pos, pos.descriptorType != fcc("msng") { s.position = pos.doubleValue }
    if state == "stopped" { return s }
    let ct = currentTrack
    s.name = try get(prop(fcc("pnam"), of: ct))?.stringValue
    s.artist = try get(prop(fcc("pArt"), of: ct))?.stringValue
    s.album = try get(prop(fcc("pAlb"), of: ct))?.stringValue
    s.duration = try get(prop(fcc("pDur"), of: ct))?.doubleValue
    s.persistentID = try get(prop(fcc("pPIS"), of: ct))?.stringValue
    s.databaseID = try get(prop(fcc("pDID"), of: ct))?.int32Value
    return s
  }
  /// whole `properties` record of a track in ONE event; returns [fourCC: descriptor]
  func properties(of spec: NSAppleEventDescriptor) throws -> [String: NSAppleEventDescriptor] {
    guard let rec = try get(prop(fcc("pALL"), of: spec)) else { return [:] }
    var out: [String: NSAppleEventDescriptor] = [:]
    if rec.numberOfItems > 0 { for i in 1...rec.numberOfItems { out[fccString(rec.keywordForDescriptor(at: i))] = rec.atIndex(i) } }
    return out
  }

  // MARK: higher-level helpers (verified)

  /// now-playing in 3 AEs (~25 ms): state, position, and the whole `properties` record of current track.
  struct NowPlaying { var state: String; var position: Double?; var track: [String: NSAppleEventDescriptor] }
  func nowPlaying() throws -> NowPlaying {
    let st = try get(prop(fcc("pPlS")))?.enumCodeValue ?? 0
    let state = [fcc("kPSS"): "stopped", fcc("kPSP"): "playing", fcc("kPSp"): "paused", fcc("kPSF"): "fastForwarding", fcc("kPSR"): "rewinding"][st] ?? fccString(st)
    let pos = try get(prop(fcc("pPos")))
    let position = (pos == nil || pos!.descriptorType == fcc("msng")) ? nil : pos!.doubleValue
    if state == "stopped" { return NowPlaying(state: state, position: nil, track: [:]) }
    // keys: pnam name, pArt artist, pAlb album, pAlA album artist, pDur duration, pPIS persistent ID,
    // pDID database ID, pLov favorited, pHat disliked, pRte rating, pClS cloud status, pTrN track number ...
    return NowPlaying(state: state, position: position, track: (try? properties(of: currentTrack)) ?? [:])
  }

  /// JPEG/PNG bytes of the first artwork of a track (default: current track). ~5-10 ms.
  func artworkData(of track: NSAppleEventDescriptor? = nil) throws -> Data? {
    let art = element(fcc("cArt"), form: fcc("indx"), NSAppleEventDescriptor(int32: 1), of: track ?? currentTrack)
    return try get(prop(fcc("pRaw"), of: art))?.data
  }

  /// Reusable queue playlist: cleared and refilled in order, then played. Music snapshots the playlist
  /// at `play` time, so the list must be complete before play; editing it later does not affect playback.
  private var queueSpec: NSAppleEventDescriptor?
  func playQueue(_ pids: [String], name: String = "encore-queue") throws {
    if queueSpec == nil || (try? get(prop(fcc("pnam"), of: queueSpec!))) == nil {
      // find existing by name, else create
      let byName = element(fcc("cUsP"), form: fcc("name"), NSAppleEventDescriptor(string: name))
      if (try? get(prop(fcc("ID  "), of: byName))) != nil {
        let id = try get(prop(fcc("ID  "), of: byName))!.int32Value
        queueSpec = element(fcc("cUsP"), form: fcc("ID  "), NSAppleEventDescriptor(int32: id))
      } else {
        let props = NSAppleEventDescriptor.record()
        props.setDescriptor(NSAppleEventDescriptor(string: name), forKeyword: fcc("pnam"))
        queueSpec = try send(fcc("core"), fcc("crel"), params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cUsP")), fcc("prdt"): props])
      }
    }
    let q = queueSpec!
    _ = try? send(fcc("core"), fcc("delo"), direct: every(fcc("cTrk"), of: q)) // -1728 when already empty
    for p in pids { try sendNoReply(fcc("core"), fcc("clon"), direct: track(persistentID: p), params: [fcc("insh"): q]) }
    let count = try countTracks(of: q)   // synchronous call = barrier: Music handles events in order
    if count != pids.count { throw MusicAEError(code: -1, message: "queue has \(count) of \(pids.count) tracks (missing/unplayable IDs?)") }
    try play(q)
  }
  /// `count tracks of container` (core/cnte)
  func countTracks(of container: NSAppleEventDescriptor) throws -> Int {
    Int(try send(fcc("core"), fcc("cnte"), direct: container, params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cTrk"))])?.int32Value ?? 0)
  }
  func deleteQueue() throws { if let q = queueSpec { try send(fcc("core"), fcc("delo"), direct: q); queueSpec = nil } }

  /// AirPlay: one AE per property list (`name of every AirPlay device` etc.). Avoid `current AirPlay devices` (~500 ms).
  struct AirPlayDevice { var id: Int32; var name: String; var kind: String; var selected: Bool; var active: Bool; var available: Bool; var volume: Int32 }
  func airPlayDevices() throws -> [AirPlayDevice] {
    let all = every(fcc("cAPD"), of: nil)
    func list(_ c: FourCharCode) throws -> [NSAppleEventDescriptor] {
      guard let l = try get(prop(c, of: all)) else { return [] }
      return (0..<l.numberOfItems).map { l.atIndex($0 + 1)! }
    }
    let ids = try list(fcc("ID  ")), names = try list(fcc("pnam")), kinds = try list(fcc("pKnd")),
        sel = try list(fcc("selc")), act = try list(fcc("pAct")), avail = try list(fcc("pAva")), vol = try list(fcc("pVol"))
    return ids.indices.map { i in
      AirPlayDevice(id: ids[i].int32Value, name: names[i].stringValue ?? "", kind: fccString(kinds[i].enumCodeValue),
                    selected: sel[i].booleanValue, active: act[i].booleanValue, available: avail[i].booleanValue, volume: vol[i].int32Value)
    }
  }
  /// set current AirPlay devices to {AirPlay device id X, ...}
  func selectAirPlay(ids: [Int32]) throws {
    let l = NSAppleEventDescriptor.list()
    for (i, id) in ids.enumerated() { l.insert(element(fcc("cAPD"), form: fcc("ID  "), NSAppleEventDescriptor(int32: id)), at: i + 1) }
    try set(prop(fcc("pAPD")), to: l)
  }
}


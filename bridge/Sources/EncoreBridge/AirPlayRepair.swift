import AppKit
import CoreAudio
import Foundation

/// Reconnects AirPlay when Music falls back to the Mac's speakers while an
/// AirPlay device stays selected. `StaleRouteDetector` has the reasoning and
/// the thresholds; this class gathers its readings and runs the reconnect.
///
/// Readings come when something could have changed: CoreAudio listeners on
/// Music's process object (running output, its devices) and on the process
/// list (Music launching or quitting), the player state or track changing, the
/// AirPlay selection changing, the Mac waking or the screen unlocking. While
/// Music plays there is also a slow poll, in case a change slipped past them.
/// A reading costs no Apple Event unless CoreAudio shows Music playing locally.
///
/// Every reading that looks stale, every decision and every reconnect goes to
/// bridge.log with the raw CoreAudio values, prefixed `airplay route:`.
/// `--no-airplay-reconnect` (config `[airplay] auto_reconnect = false`) keeps
/// the detection and the log but never touches the route.
///
/// Every method runs on the AirPlay queue.
final class AirPlayRepairer: @unchecked Sendable {
  /// Wait between selecting this computer and selecting the old devices again.
  static let switchPause: TimeInterval = 0.4
  /// How long Music's local output may take to stop after the reconnect.
  static let verifyFor: TimeInterval = 5
  static let verifyEvery: TimeInterval = 0.5
  /// The poll while Music plays.
  static let pollEvery: TimeInterval = 30
  /// Readings after a wake or an unlock, in seconds: the route usually breaks
  /// then, and Music may take a moment to resume.
  static let afterWake: [TimeInterval] = [2, 8, 20]

  private let server: Server
  private let music: MusicAE
  private let watcher: AirPlayWatcher
  private let queue: DispatchQueue
  private let enabled: Bool

  private var detector = StaleRouteDetector()
  private var playerState = "stopped"
  private var track: String?
  private var reconnecting = false
  private var pending: DispatchWorkItem?
  private var pendingAt: DispatchTime?
  private var poll: DispatchSourceTimer?
  /// The last line logged for a reading, so an unchanged state isn't logged again.
  private var lastLogged = ""

  /// The process object the listeners are on, and the listener block.
  private var watched: AudioObjectID = 0
  private var processListener: AudioObjectPropertyListenerBlock?
  private var observers: [NSObjectProtocol] = []

  init(server: Server, music: MusicAE, watcher: AirPlayWatcher, queue: DispatchQueue, enabled: Bool) {
    self.server = server
    self.music = music
    self.watcher = watcher
    self.queue = queue
    self.enabled = enabled
  }

  /// Installs the listeners and notification observers. Call once, on the AirPlay queue.
  func start() {
    var list = Self.address(kAudioHardwarePropertyProcessObjectList)
    let status = AudioObjectAddPropertyListenerBlock(AudioObjectID(kAudioObjectSystemObject), &list, queue) { [weak self] _, _ in
      self?.schedule(after: 0.2, "audio processes changed")
    }
    if status != noErr { log("can't watch CoreAudio's process list (\(status))") }

    let workspace = NSWorkspace.shared.notificationCenter
    for name in [NSWorkspace.didWakeNotification, NSWorkspace.screensDidWakeNotification, NSWorkspace.sessionDidBecomeActiveNotification] {
      observers.append(workspace.addObserver(forName: name, object: nil, queue: nil) { [weak self] n in
        self?.queue.async { self?.woke(n.name.rawValue) }
      })
    }
    observers.append(DistributedNotificationCenter.default().addObserver(
      forName: Notification.Name("com.apple.screenIsUnlocked"), object: nil, queue: nil
    ) { [weak self] _ in
      self?.queue.async { self?.woke("screen unlocked") }
    })
    log("watching for a stale route (auto-reconnect \(enabled ? "on" : "off"))")
    schedule(after: 1, "start")
  }

  // MARK: triggers

  /// The player state or the current track changed. From PlayerMonitor.
  func playerChanged(state: String, track: String?) {
    let wasPlaying = playerState == "playing"
    playerState = state
    self.track = track
    setPolling(state == "playing")
    if state == "playing" || wasPlaying { schedule(after: 0.5, "player \(state)") }
  }

  /// The AirPlay selection changed (ours, the user's in encore, or seen in Music).
  func routeChanged() {
    guard !reconnecting else { return }
    schedule(after: 1, "AirPlay selection changed")
  }

  private func woke(_ why: String) {
    // Logged whatever it finds, so the log holds the state right after a wake.
    lastLogged = ""
    for (i, s) in Self.afterWake.enumerated() {
      queue.asyncAfter(deadline: .now() + s) { [weak self] in self?.check(i == 0 ? why : "\(why) +\(Int(s)) s") }
    }
  }

  /// Reads after `seconds`, unless a reading is already due sooner.
  private func schedule(after seconds: TimeInterval, _ why: String) {
    let at = DispatchTime.now() + seconds
    if let pendingAt, pendingAt <= at, pending != nil { return }
    pending?.cancel()
    let work = DispatchWorkItem { [weak self] in
      self?.pending = nil
      self?.pendingAt = nil
      self?.check(why)
    }
    pending = work
    pendingAt = at
    queue.asyncAfter(deadline: at, execute: work)
  }

  private func setPolling(_ on: Bool) {
    guard on else {
      poll?.cancel()
      poll = nil
      return
    }
    guard poll == nil else { return }
    let t = DispatchSource.makeTimerSource(queue: queue)
    t.schedule(deadline: .now() + Self.pollEvery, repeating: Self.pollEvery, leeway: .seconds(2))
    t.setEventHandler { [weak self] in self?.check("poll") }
    t.resume()
    poll = t
  }

  // MARK: checking

  private func check(_ why: String) {
    guard !reconnecting else { return }
    let audio = readAudio()
    var obs = StaleRouteDetector.Observation(
      now: Self.now(), playsLocally: audio.playsLocally, playing: false, selected: [], track: track)
    var all: [MusicAE.AirPlayEntry] = []
    if audio.playsLocally {
      // Only now ask Music: the AirPlay list costs ~500 ms when its cache is cold.
      do {
        playerState = PlayerMonitor.stateName(try music.get(music.prop(fcc("pPlS")))?.enumCodeValue ?? 0)
        all = try music.airPlayList()
      } catch {
        log("\(why): can't read Music (\(error)); \(audio.summary)")
        return
      }
      obs.playing = playerState == "playing"
      obs.selected = all.filter(\.selected).map { .init(pid: $0.pid, name: $0.name, kind: $0.kind) }
    }
    let verdict = detector.evaluate(obs)
    let selected = obs.selected.map { "\($0.name) (\($0.kind))" }.joined(separator: ", ")
    let line = "\(audio.summary); state \(audio.playsLocally ? playerState : "-"); selected [\(selected)] → \(verdict)"
    // Readings that look stale every time, the rest (and a route given up on) only when they change.
    let routine = verdict == .fine("this computer is selected") || verdict == .gaveUp
    if line != lastLogged || (audio.playsLocally && !routine) {
      log("\(why): \(line)")
    }
    lastLogged = line

    switch verdict {
    case .fine:
      break
    case .confirming(let s), .waiting(let s):
      schedule(after: s, "recheck")
    case .gaveUp:
      break
    case .reconnect:
      guard enabled else {
        log("would reconnect to \(names(obs.selected)) but auto-reconnect is off")
        detector.skipped(at: Self.now())
        schedule(after: StaleRouteDetector.minInterval, "recheck")
        return
      }
      reconnect(all.filter(\.selected), in: all)
    }
  }

  // MARK: reconnecting

  /// Selects this computer, waits `switchPause`, selects `selected` again by
  /// persistent ID, then watches Music's local output stop.
  private func reconnect(_ selected: [MusicAE.AirPlayEntry], in all: [MusicAE.AirPlayEntry]) {
    let names = selected.map(\.name)
    guard let computer = all.first(where: { $0.kind == "computer" }) else {
      log("can't reconnect: Music lists no computer to switch through")
      finish(names, ok: false)
      return
    }
    reconnecting = true
    log("reconnecting \(names.joined(separator: ", ")): selecting \(computer.name), then them again")
    let t = DispatchTime.now()
    var ids: [Int32] = selected.map(\.id)
    do {
      try music.selectAirPlay(ids: [computer.id])
      Thread.sleep(forTimeInterval: Self.switchPause)
      // Music can renumber devices; find the old ones again by persistent ID.
      let fresh = try music.airPlayList()
      ids = selected.map { old in fresh.first { !old.pid.isEmpty && $0.pid == old.pid }?.id ?? old.id }
      try music.selectAirPlay(ids: ids)
    } catch {
      log("reconnect failed: \(error)")
      // Put the old selection back, so a failure never leaves the computer selected.
      try? music.selectAirPlayOneByOne(ids: ids)
      watcher.changed { d in d.selected = ids.contains(d.id) }
      finish(names, ok: false)
      return
    }
    watcher.changed { d in d.selected = ids.contains(d.id) }
    log(String(format: "reselected after %.0f ms; waiting for local output to stop", PlaybackHandlers.elapsedMs(since: t)))
    verify(names, until: Self.now() + Self.verifyFor, quiet: 0)
  }

  /// Done once two readings in a row show no local output.
  private func verify(_ names: [String], until deadline: TimeInterval, quiet: Int) {
    queue.asyncAfter(deadline: .now() + Self.verifyEvery) { [weak self] in
      guard let self else { return }
      let audio = self.readAudio()
      let q = audio.playsLocally ? 0 : quiet + 1
      if q >= 2 {
        self.log("reconnected: \(audio.summary)")
        self.finish(names, ok: true)
      } else if Self.now() >= deadline {
        self.log("still playing locally \(Int(Self.verifyFor)) s after the reconnect: \(audio.summary)")
        self.finish(names, ok: false)
      } else {
        self.verify(names, until: deadline, quiet: q)
      }
    }
  }

  private func finish(_ names: [String], ok: Bool) {
    reconnecting = false
    detector.reconnected(ok: ok, at: Self.now())
    if !ok && detector.failures >= StaleRouteDetector.maxFailures {
      log("giving up on this route until the selection or the track changes")
    }
    server.emit("airplay.repaired", ["devices": names, "ok": ok])
    schedule(after: ok ? 2 : StaleRouteDetector.minInterval, "after reconnect")
  }

  // MARK: CoreAudio

  /// Reads Music's process object and keeps the listeners on the current one.
  private func readAudio() -> MusicAudioReading {
    let r = Self.readMusicAudio()
    watch(r.process)
    return r
  }

  /// What CoreAudio says about Music's output now. Reads only; any queue.
  static func readMusicAudio() -> MusicAudioReading {
    var r = MusicAudioReading()
    r.defaultOutput = Self.name(Self.get(AudioObjectID(kAudioObjectSystemObject), kAudioHardwarePropertyDefaultOutputDevice))
    guard let app = NSRunningApplication.runningApplications(withBundleIdentifier: MusicAE.bundleID).first else { return r }
    r.pid = app.processIdentifier
    let obj = Self.processObject(pid: r.pid)
    guard obj != 0 else { return r }
    r.process = obj
    r.runningOutput = Self.get(obj, kAudioProcessPropertyIsRunningOutput) != 0
    r.outputs = Self.list(obj, kAudioProcessPropertyDevices, scope: kAudioObjectPropertyScopeOutput).map { d in
      .init(id: d, name: Self.name(d),
            transport: fccString(Self.get(d, kAudioDevicePropertyTransportType)),
            running: Self.get(d, kAudioDevicePropertyDeviceIsRunningSomewhere) != 0)
    }
    return r
  }

  /// Moves the property listeners to `obj` (0: none).
  private func watch(_ obj: AudioObjectID) {
    guard obj != watched else { return }
    if watched != 0, let processListener {
      for var a in Self.processAddresses { AudioObjectRemovePropertyListenerBlock(watched, &a, queue, processListener) }
    }
    watched = obj
    processListener = nil
    guard obj != 0 else { return }
    let block: AudioObjectPropertyListenerBlock = { [weak self] _, _ in
      self?.schedule(after: 0.2, "Music's audio output changed")
    }
    for var a in Self.processAddresses {
      let status = AudioObjectAddPropertyListenerBlock(obj, &a, queue, block)
      if status != noErr { log("can't watch Music's audio process \(obj) (\(status))") }
    }
    processListener = block
  }

  private static let processAddresses = [
    address(kAudioProcessPropertyIsRunningOutput),
    address(kAudioProcessPropertyDevices, scope: kAudioObjectPropertyScopeOutput),
  ]

  private static func address(_ sel: AudioObjectPropertySelector, scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeGlobal) -> AudioObjectPropertyAddress {
    AudioObjectPropertyAddress(mSelector: sel, mScope: scope, mElement: kAudioObjectPropertyElementMain)
  }

  /// A UInt32 property (flags, transport types and object IDs all are), 0 when it can't be read.
  private static func get(_ obj: AudioObjectID, _ sel: AudioObjectPropertySelector) -> UInt32 {
    var a = address(sel)
    var v: UInt32 = 0
    var size = UInt32(MemoryLayout<UInt32>.size)
    return AudioObjectGetPropertyData(obj, &a, 0, nil, &size, &v) == noErr ? v : 0
  }

  private static func list(_ obj: AudioObjectID, _ sel: AudioObjectPropertySelector, scope: AudioObjectPropertyScope) -> [AudioObjectID] {
    var a = address(sel, scope: scope)
    var size: UInt32 = 0
    guard AudioObjectGetPropertyDataSize(obj, &a, 0, nil, &size) == noErr, size > 0 else { return [] }
    var ids = [AudioObjectID](repeating: 0, count: Int(size) / MemoryLayout<AudioObjectID>.size)
    guard AudioObjectGetPropertyData(obj, &a, 0, nil, &size, &ids) == noErr else { return [] }
    return Array(ids.prefix(Int(size) / MemoryLayout<AudioObjectID>.size))
  }

  private static func name(_ obj: AudioObjectID) -> String {
    guard obj != 0 else { return "none" }
    var a = address(kAudioObjectPropertyName)
    var s: Unmanaged<CFString>?
    var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
    guard AudioObjectGetPropertyData(obj, &a, 0, nil, &size, &s) == noErr, let s else { return "?" }
    return s.takeRetainedValue() as String
  }

  /// CoreAudio's process object for `pid`, 0 when it has none.
  private static func processObject(pid: pid_t) -> AudioObjectID {
    var a = address(kAudioHardwarePropertyTranslatePIDToProcessObject)
    var pid = pid
    var obj = AudioObjectID(kAudioObjectUnknown)
    var size = UInt32(MemoryLayout<AudioObjectID>.size)
    let status = AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &a, UInt32(MemoryLayout<pid_t>.size), &pid, &size, &obj)
    return status == noErr ? obj : 0
  }

  // MARK: helpers

  /// Seconds on a clock that keeps counting while the Mac sleeps.
  private static func now() -> TimeInterval { Double(clock_gettime_nsec_np(CLOCK_MONOTONIC)) / 1e9 }

  private func names(_ devs: [StaleRouteDetector.Device]) -> String { devs.map(\.name).joined(separator: ", ") }

  private func log(_ s: String) { server.log("airplay route: " + s) }
}

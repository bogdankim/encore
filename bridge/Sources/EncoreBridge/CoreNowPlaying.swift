import Foundation

/// Reads Music's player state as a `NowPlaying` object and pushes `player` events.
/// Every method except `configure` must run on `server.music`.
final class PlayerMonitor: @unchecked Sendable {
  static let shared = PlayerMonitor()

  private var server: Server?
  private var music: MusicAE?

  private var ticker: DispatchSourceTimer?
  private var ticks = 0
  private var pendingNotification: DispatchWorkItem?
  private var followUps: [DispatchWorkItem] = []

  /// Everything in NowPlaying except state and position, as Music last reported it.
  private struct Settings {
    var volume: Int
    var shuffle: Bool
    var repeatMode: String
    var track: Track?
  }
  private struct Track {
    var pid, name, artist, album, albumArtist: String
    var duration: Double
    var favorited, disliked: Bool
  }
  private var cached: Settings?
  private(set) var lastState = "stopped"
  /// The last event sent, without `at`, to drop exact repeats while not playing.
  private var lastEmitted: NSDictionary?
  /// Called on the music queue when the reported state or the current track
  /// changes (the AirPlay route check listens).
  var onChange: ((_ state: String, _ track: String?) -> Void)?
  private var lastChange: (state: String, track: String?)?

  /// Values we just wrote. Music can return the old value for up to ~1 s after a
  /// set, so reads report the written value until Music agrees or it expires.
  private struct Expected { let value: AnyHashable; let until: DispatchTime }
  private var expected: [String: Expected] = [:]
  private static let expectWindowMs = 1500
  private static let expectWindow = DispatchTimeInterval.milliseconds(expectWindowMs)

  /// Bumped by every transport command, so a check for an older one gives up.
  private var transportSeq = 0
  /// When the last play command started a track. Music reports paused while it loads.
  private var playStartedAt: DispatchTime?

  func configure(_ server: Server, _ music: MusicAE) {
    self.server = server
    self.music = music
  }

  // MARK: reading

  /// A full read is 6 Apple Events: player state, position, volume, shuffle, repeat
  /// and the `properties` record of the current track. A light read (`full: false`)
  /// is 3: state, position and the current track's persistent ID; it reuses the
  /// last full read while the track is the same.
  func read(full: Bool = true) throws -> [String: Any] {
    guard let music else { throw Server.BridgeError.internalError("player monitor not configured") }
    let state = Self.stateName(try music.get(music.prop(fcc("pPlS")))?.enumCodeValue ?? 0)
    let before = Date()
    let position = try music.get(music.prop(fcc("pPos")))
    let at = before.addingTimeInterval(Date().timeIntervalSince(before) / 2)

    var settings: Settings
    if !full, let cached, try currentPID(music) == cached.track?.pid {
      settings = cached
    } else {
      settings = try readSettings(music)
      cached = settings
    }
    lastState = state

    var seconds = 0.0
    if state != "stopped", let position, !position.isMissingValue { seconds = position.doubleValue }
    var track: Any = NSNull()
    if let t = settings.track {
      track = [
        "pid": t.pid, "name": t.name, "artist": t.artist, "album": t.album,
        "albumArtist": t.albumArtist, "duration": t.duration,
        "favorited": resolve("fav:" + t.pid, t.favorited),
        "disliked": resolve("dis:" + t.pid, t.disliked),
      ] as [String: Any]
    }
    return [
      "state": resolve("state", state),
      "position": seconds,
      "at": unixMillis(at),
      "volume": resolve("volume", settings.volume),
      "shuffle": resolve("shuffle", settings.shuffle),
      "repeat": resolve("repeat", settings.repeatMode),
      "track": track,
    ]
  }

  /// Music's player state, without expected values. One Apple Event.
  func readState() throws -> String {
    guard let music else { throw Server.BridgeError.internalError("player monitor not configured") }
    lastState = Self.stateName(try music.get(music.prop(fcc("pPlS")))?.enumCodeValue ?? 0)
    return lastState
  }

  private func readSettings(_ music: MusicAE) throws -> Settings {
    var s = Settings(
      volume: Int(try music.get(music.prop(fcc("pVol")))?.int32Value ?? 0),
      shuffle: try music.get(music.prop(fcc("pShE")))?.booleanValue ?? false,
      repeatMode: Self.repeatName(try music.get(music.prop(fcc("pRpt")))?.enumCodeValue ?? 0))
    do {
      let p = try music.properties(of: music.currentTrack)
      if let pid = p["pPIS"]?.stringValue {
        s.track = Track(
          pid: pid,
          name: p["pnam"]?.stringValue ?? "",
          artist: p["pArt"]?.stringValue ?? "",
          album: p["pAlb"]?.stringValue ?? "",
          albumArtist: p["pAlA"]?.stringValue ?? "",
          duration: p["pDur"].flatMap { $0.isMissingValue ? nil : $0.doubleValue } ?? 0,
          favorited: p["pLov"]?.booleanValue ?? false,
          disliked: p["pHat"]?.booleanValue ?? false)
      }
    } catch let error where !MusicAE.isNotRunning(error) {
      // -1728: no current track.
    }
    return s
  }

  /// Persistent ID of the current track, nil when there is none.
  private func currentPID(_ music: MusicAE) throws -> String? {
    do {
      return try music.get(music.prop(fcc("pPIS"), of: music.currentTrack))?.stringValue
    } catch let e as MusicAEError where e.code == -1728 {
      return nil
    }
  }

  /// The state reported while Music isn't running.
  static func stoppedState() -> [String: Any] {
    ["state": "stopped", "position": 0.0, "at": unixMillis(), "volume": 0, "shuffle": false,
     "repeat": "off", "track": NSNull()]
  }

  static func stateName(_ code: FourCharCode) -> String {
    switch code {
    case fcc("kPSP"), fcc("kPSF"), fcc("kPSR"): return "playing"
    case fcc("kPSp"): return "paused"
    default: return "stopped"
    }
  }

  static func repeatName(_ code: FourCharCode) -> String {
    switch code {
    case fcc("kRp1"): return "one"
    case fcc("kAll"): return "all"
    default: return "off"
    }
  }

  static func repeatCode(_ mode: String) -> FourCharCode? {
    switch mode {
    case "off": return fcc("kRpO")
    case "one": return fcc("kRp1")
    case "all": return fcc("kAll")
    default: return nil
    }
  }

  // MARK: expected values

  /// Report `value` for `key` ("state", "volume", "shuffle", "repeat") until Music agrees.
  func expect(_ key: String, _ value: AnyHashable) {
    expected[key] = Expected(value: value, until: .now() + Self.expectWindow)
    cached = nil
  }

  /// Music keeps favorite and dislike exclusive: setting one clears the other.
  func expectFavorite(_ pid: String, _ value: Bool) {
    expect("fav:" + pid, value)
    if value { expect("dis:" + pid, false) }
  }

  func expectDislike(_ pid: String, _ value: Bool) {
    expect("dis:" + pid, value)
    if value { expect("fav:" + pid, false) }
  }

  private func resolve<T: Hashable>(_ key: String, _ actual: T) -> T {
    guard let e = expected[key] else { return actual }
    if e.until < .now() || e.value == AnyHashable(actual) {
      expected[key] = nil
      return actual
    }
    return (e.value.base as? T) ?? actual
  }

  // MARK: events

  /// Reads the state and emits a `player` event.
  func refresh(full: Bool = true) {
    guard let server else { return }
    let state: [String: Any]
    do {
      state = try read(full: full)
    } catch {
      guard MusicAE.isNotRunning(error) else {
        // -1744: waiting for the Automation prompt, which Permissions logs.
        if (error as? MusicAEError)?.code != -1744 { server.log("player read failed: \(error)") }
        return
      }
      cached = nil
      state = Self.stoppedState()
    }
    let playing = state["state"] as? String == "playing"
    var comparable = state
    comparable["at"] = nil
    let key = comparable as NSDictionary
    if playing || key != lastEmitted { server.emit("player", state) }
    lastEmitted = key
    notifyChange(state["state"] as? String ?? "stopped", (state["track"] as? [String: Any])?["pid"] as? String)
    // Tick on what Music says, not on what we expect: if it ignored a pause, the
    // ticks report it once the expectation runs out.
    setTicking(playing || lastState == "playing")
  }

  /// A playerInfo notification arrived. Music sends bursts, so wait 80 ms for the last one.
  func notificationArrived() {
    guard let server else { return }
    pendingNotification?.cancel()
    let work = DispatchWorkItem { [weak self] in self?.refresh() }
    pendingNotification = work
    server.music.asyncAfter(deadline: .now() + .milliseconds(80), execute: work)
  }

  /// Call after any command that changes the player. Music applies changes
  /// asynchronously, so read soon, once more after it settles, and once after
  /// expected values run out, so an event never leaves one standing.
  func commandDidRun() {
    guard let server else { return }
    followUps.forEach { $0.cancel() }
    followUps = [150, 700, Self.expectWindowMs + 100].map { ms in
      let work = DispatchWorkItem { [weak self] in self?.refresh() }
      server.music.asyncAfter(deadline: .now() + .milliseconds(ms), execute: work)
      return work
    }
  }

  // MARK: transport

  /// playTracks or playPlaylist started a track. Cancels any pending pause or play check.
  func playStarted() {
    transportSeq += 1
    playStartedAt = .now()
  }

  /// A play command started a track less than 2 s ago.
  var isStartingTrack: Bool {
    guard let playStartedAt else { return false }
    return DispatchTime.now() < playStartedAt + .seconds(2)
  }

  /// Pauses or resumes and makes sure Music did. Music sometimes ignores the
  /// Apple Event (a pause while a track loads, or a session where its scripting
  /// layer thinks it is paused while it plays), so check at 400 ms and again at
  /// 1.2 s, after a loading track has started, and send it again through
  /// MediaRemote if Music is still in the old state. Both commands are idempotent.
  func transport(_ want: MediaRemote.Command) throws {
    guard let music else { throw Server.BridgeError.internalError("player monitor not configured") }
    transportSeq += 1
    let seq = transportSeq
    let was = lastState
    switch want {
    case .pause:
      try music.pause()
      if was == "playing" { expect("state", "paused") }
    case .play:
      try music.play()
      if was == "paused" { expect("state", "playing") }
    }
    for ms in [400, 1200] {
      server?.music.asyncAfter(deadline: .now() + .milliseconds(ms)) { [weak self] in
        self?.checkTransport(want, seq)
      }
    }
  }

  private func checkTransport(_ want: MediaRemote.Command, _ seq: Int) {
    guard seq == transportSeq, let server, let music else { return }
    guard let state = try? readState() else { return }
    let ignored = want == .pause ? state == "playing" : state == "paused"
    guard ignored else { return }
    let name = want == .pause ? "pause" : "play"
    if MediaRemote.send(want) {
      server.log("\(name): Music is still \(state), sent again through MediaRemote")
    } else {
      server.log("\(name): Music is still \(state), sent again")
      try? want == .pause ? music.pause() : music.play()
    }
    let work = DispatchWorkItem { [weak self] in self?.refresh() }
    server.music.asyncAfter(deadline: .now() + .milliseconds(300), execute: work)
  }

  /// Music quit: no reads (they would fail), just report stopped.
  func musicQuit() {
    pendingNotification?.cancel()
    followUps.forEach { $0.cancel() }
    followUps = []
    expected = [:]
    cached = nil
    lastState = "stopped"
    lastEmitted = nil
    setTicking(false)
    server?.emit("player", Self.stoppedState())
    notifyChange("stopped", nil)
  }

  private func notifyChange(_ state: String, _ track: String?) {
    guard onChange != nil else { return }
    if let lastChange, lastChange.state == state, lastChange.track == track { return }
    lastChange = (state, track)
    onChange?(state, track)
  }

  /// While playing, emit once per second, one second after the latest event. Every
  /// fifth tick is a full read so changes made in Music itself (volume, favorite) show up.
  private func setTicking(_ on: Bool) {
    guard on, let server else {
      ticker?.cancel()
      ticker = nil
      return
    }
    if ticker == nil {
      let timer = DispatchSource.makeTimerSource(queue: server.music)
      timer.setEventHandler { [weak self] in
        guard let self else { return }
        self.ticks += 1
        self.refresh(full: self.ticks % 5 == 0)
      }
      timer.schedule(deadline: .now() + 1, repeating: 1, leeway: .milliseconds(50))
      timer.resume()
      ticker = timer
    } else {
      ticker?.schedule(deadline: .now() + 1, repeating: 1, leeway: .milliseconds(50))
    }
  }
}

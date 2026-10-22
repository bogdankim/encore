import Foundation

/// Decides when Music's AirPlay route has gone stale and needs a reconnect.
///
/// The macOS bug: after a long idle or a lock and unlock, Music keeps an AirPlay
/// device (a HomePod) selected but plays through the Mac's own output. Apple
/// Events can't tell: `selected` and `active` read the same as in a healthy
/// session. CoreAudio can. While Music streams to AirPlay only, its process
/// object runs no output (`IsRunningOutput` 0, no output devices), because
/// Music sends AirPlay itself. When it falls back, the process runs output on a
/// local device. So: playing, this computer not selected, and Music's process
/// running output on a device that isn't an AirPlay one, for `confirm` seconds
/// in a row, means stale. The fix is the one people do by hand in Music:
/// select this computer, then the devices they had again.
///
/// Pure value type with no clock of its own, so it can be unit tested. The
/// caller passes the time and the readings, and schedules a new reading when a
/// verdict asks for one.
struct StaleRouteDetector {
  /// How long the stale signs must hold before acting. Right after play, pause
  /// or a route change, Music can run local output for a moment on its way to
  /// the new route.
  static let confirm: TimeInterval = 3
  /// At most one reconnect per this many seconds.
  static let minInterval: TimeInterval = 60
  /// Failed reconnects allowed for one route and track before giving up on it.
  static let maxFailures = 2

  struct Device: Equatable {
    var pid: String
    var name: String
    var kind: String
  }

  struct Observation {
    /// Seconds on a monotonic clock.
    var now: TimeInterval
    /// Music's process runs output on a device that isn't an AirPlay one.
    var playsLocally: Bool
    /// Music reports `playing`.
    var playing: Bool
    /// Music's selected AirPlay devices.
    var selected: [Device]
    /// Persistent ID of the current track, nil when there is none.
    var track: String?
  }

  enum Verdict: Equatable {
    /// Nothing to do; the reason is for the log.
    case fine(String)
    /// The stale signs showed up; read again in this many seconds.
    case confirming(recheckIn: TimeInterval)
    /// Stale, but the last reconnect was too recent; read again then.
    case waiting(recheckIn: TimeInterval)
    /// Stale, and reconnects already failed `maxFailures` times on this route and track.
    case gaveUp
    /// Reconnect now.
    case reconnect
  }

  private(set) var suspectSince: TimeInterval?
  private(set) var failures = 0
  private var lastAttempt: TimeInterval?
  /// The selected devices and track of the last stale-looking reading. A new
  /// route or track gets fresh tries.
  private var key: String?

  mutating func evaluate(_ o: Observation) -> Verdict {
    guard o.playsLocally else {
      suspectSince = nil
      return .fine("no local output")
    }
    let k = o.selected.map(\.pid).sorted().joined(separator: ",") + "|" + (o.track ?? "")
    if k != key {
      key = k
      failures = 0
      suspectSince = nil
    }
    guard o.playing else {
      suspectSince = nil
      return .fine("not playing")
    }
    guard !o.selected.isEmpty else {
      suspectSince = nil
      return .fine("no AirPlay device selected")
    }
    guard !o.selected.contains(where: { $0.kind == "computer" }) else {
      suspectSince = nil
      return .fine("this computer is selected")
    }
    guard let since = suspectSince else {
      suspectSince = o.now
      return .confirming(recheckIn: Self.confirm)
    }
    let held = o.now - since
    if held < Self.confirm { return .confirming(recheckIn: Self.confirm - held) }
    if failures >= Self.maxFailures { return .gaveUp }
    if let lastAttempt, o.now - lastAttempt < Self.minInterval {
      return .waiting(recheckIn: Self.minInterval - (o.now - lastAttempt))
    }
    return .reconnect
  }

  /// A reconnect finished at `now`. A success starts over; a failure counts
  /// toward `maxFailures`.
  mutating func reconnected(ok: Bool, at now: TimeInterval) {
    lastAttempt = now
    if ok {
      failures = 0
      suspectSince = nil
    } else {
      failures += 1
    }
  }

  /// A reconnect was due but auto-reconnect is off: wait `minInterval` before
  /// saying so again, without counting a failure.
  mutating func skipped(at now: TimeInterval) {
    lastAttempt = now
  }
}

/// What CoreAudio says about Music's audio output at one moment.
struct MusicAudioReading: Equatable {
  struct Output: Equatable {
    var id: UInt32
    var name: String
    /// kAudioDevicePropertyTransportType as four characters: `bltn` built-in,
    /// `airp` AirPlay, `usb `, `blue`, `virt`, `grup` aggregate, ...
    var transport: String
    /// kAudioDevicePropertyDeviceIsRunningSomewhere.
    var running: Bool
  }

  /// Music's pid, 0 when it isn't running.
  var pid: Int32 = 0
  /// Music's CoreAudio process object, 0 when CoreAudio has none for it (Music
  /// hasn't opened audio yet).
  var process: UInt32 = 0
  /// kAudioProcessPropertyIsRunningOutput.
  var runningOutput = false
  /// kAudioProcessPropertyDevices, output scope.
  var outputs: [Output] = []
  /// The system's default output device.
  var defaultOutput = ""

  /// Music runs output on a device of its own Mac. Music's own AirPlay doesn't
  /// go through CoreAudio, so any output device that isn't a CoreAudio AirPlay
  /// device (the system-wide kind) means the sound comes out here.
  var playsLocally: Bool {
    runningOutput && outputs.contains { $0.transport != "airp" }
  }

  /// One line for bridge.log.
  var summary: String {
    guard pid != 0 else { return "Music not running" }
    let devs = outputs.map { "\($0.name) [\($0.transport) id \($0.id)\($0.running ? " running" : "")]" }
    return "music pid \(pid) process \(process) runningOutput \(runningOutput ? 1 : 0) outputs [\(devs.joined(separator: ", "))] default output \(defaultOutput)"
  }
}

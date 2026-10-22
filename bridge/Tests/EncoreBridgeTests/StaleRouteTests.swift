import Foundation
import Testing
@testable import encore_bridge

private typealias Detector = StaleRouteDetector

private let homePod = Detector.Device(pid: "A4D1", name: "Living Room", kind: "homepod")
private let kitchen = Detector.Device(pid: "C0FF", name: "Kitchen", kind: "speaker")
private let mac = Detector.Device(pid: "6F2C", name: "MacBook Pro", kind: "computer")

/// Music playing `track` to `selected`, with or without local output.
private func obs(_ now: Double, local: Bool = true, playing: Bool = true,
                 _ selected: [Detector.Device] = [homePod], track: String? = "T1") -> Detector.Observation {
  .init(now: now, playsLocally: local, playing: playing, selected: selected, track: track)
}

/// A detector that has seen the stale signs for the whole confirm window and reconnects.
private func confirmed(at start: Double = 0) -> Detector {
  var d = Detector()
  _ = d.evaluate(obs(start))
  #expect(d.evaluate(obs(start + Detector.confirm)) == .reconnect)
  return d
}

@Test func `healthy AirPlay playback is fine`() {
  var d = Detector()
  #expect(d.evaluate(obs(0, local: false)) == .fine("no local output"))
  #expect(d.suspectSince == nil)
}

@Test func `never acts while this computer is selected`() {
  var d = Detector()
  #expect(d.evaluate(obs(0, [mac])) == .fine("this computer is selected"))
  #expect(d.evaluate(obs(10, [mac, homePod])) == .fine("this computer is selected"))
  #expect(d.evaluate(obs(20, [])) == .fine("no AirPlay device selected"))
}

@Test func `never acts while paused`() {
  var d = Detector()
  #expect(d.evaluate(obs(0, playing: false)) == .fine("not playing"))
  #expect(d.evaluate(obs(10, playing: false)) == .fine("not playing"))
}

@Test func `local output must hold for the confirm window`() {
  var d = Detector()
  #expect(d.evaluate(obs(0)) == .confirming(recheckIn: Detector.confirm))
  #expect(d.evaluate(obs(1)) == .confirming(recheckIn: Detector.confirm - 1))
  #expect(d.evaluate(obs(Detector.confirm)) == .reconnect)
}

@Test func `a transient gap restarts the confirm window`() {
  var d = Detector()
  _ = d.evaluate(obs(0))
  #expect(d.evaluate(obs(1.5, local: false)) == .fine("no local output"))
  #expect(d.evaluate(obs(2)) == .confirming(recheckIn: Detector.confirm))
  #expect(d.evaluate(obs(2 + Detector.confirm)) == .reconnect)
}

@Test func `a route or track change restarts the confirm window`() {
  var d = Detector()
  _ = d.evaluate(obs(0))
  #expect(d.evaluate(obs(2, [homePod, kitchen])) == .confirming(recheckIn: Detector.confirm))
  #expect(d.evaluate(obs(4, [homePod, kitchen], track: "T2")) == .confirming(recheckIn: Detector.confirm))
}

@Test func `at most one reconnect a minute`() {
  var d = confirmed()
  d.reconnected(ok: true, at: 4)
  #expect(d.suspectSince == nil)
  _ = d.evaluate(obs(10))
  let v = d.evaluate(obs(10 + Detector.confirm))
  #expect(v == .waiting(recheckIn: Detector.minInterval - (6 + Detector.confirm)))
  #expect(d.evaluate(obs(4 + Detector.minInterval)) == .reconnect)
}

@Test func `gives up after two failures until the route or track changes`() {
  var d = confirmed()
  d.reconnected(ok: false, at: 4)
  #expect(d.evaluate(obs(5)) == .waiting(recheckIn: Detector.minInterval - 1))
  #expect(d.evaluate(obs(4 + Detector.minInterval)) == .reconnect)
  d.reconnected(ok: false, at: 70)
  #expect(d.failures == Detector.maxFailures)
  #expect(d.evaluate(obs(500)) == .gaveUp)

  // The next track gets fresh tries, after the confirm window.
  #expect(d.evaluate(obs(501, track: "T2")) == .confirming(recheckIn: Detector.confirm))
  #expect(d.failures == 0)
  #expect(d.evaluate(obs(501 + Detector.confirm, track: "T2")) == .reconnect)
}

@Test func `a success clears the failures`() {
  var d = confirmed()
  d.reconnected(ok: false, at: 4)
  d.reconnected(ok: true, at: 70)
  #expect(d.failures == 0)
}

@Test func `auto-reconnect off waits a minute without counting a failure`() {
  var d = confirmed()
  d.skipped(at: 3)
  #expect(d.failures == 0)
  #expect(d.evaluate(obs(10)) == .waiting(recheckIn: Detector.minInterval - 7))
}

@Test func `local means a running output that isn't a CoreAudio AirPlay device`() {
  let speakers = MusicAudioReading.Output(id: 91, name: "MacBook Pro Speakers", transport: "bltn", running: true)
  let airplay = MusicAudioReading.Output(id: 120, name: "AirPlay", transport: "airp", running: true)
  var r = MusicAudioReading(pid: 501, process: 77, runningOutput: false, outputs: [], defaultOutput: "MacBook Pro Speakers")
  #expect(!r.playsLocally)  // the healthy reading measured on macOS 27
  r.runningOutput = true
  #expect(!r.playsLocally)  // running, but on no device it names
  r.outputs = [airplay]
  #expect(!r.playsLocally)
  r.outputs = [airplay, speakers]
  #expect(r.playsLocally)
  r.runningOutput = false
  #expect(!r.playsLocally)
}

/// `ENCORE_AUDIO_PROBE=1 swift test --filter probe` prints what the bridge
/// would read from CoreAudio right now. It only reads, and never talks to
/// Music. Run it while the HomePod has dropped out to see the broken state.
@Test(.enabled(if: ProcessInfo.processInfo.environment["ENCORE_AUDIO_PROBE"] != nil))
func `probe Music's audio output`() {
  let r = AirPlayRepairer.readMusicAudio()
  print("airplay route probe: \(r.summary); playsLocally \(r.playsLocally)")
  #expect(!r.defaultOutput.isEmpty)
}

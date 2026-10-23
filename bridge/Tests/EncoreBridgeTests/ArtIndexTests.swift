import Foundation
import Testing
@testable import encore_bridge

private final class FakeSource: ArtSource {
  var entries: [ArtEntry]
  var failures = 0
  var loads = 0
  var releases = 0

  init(_ entries: [ArtEntry]) { self.entries = entries }

  struct Failure: Error, CustomStringConvertible { var description: String { "service gone" } }

  func load() throws -> [ArtEntry] {
    loads += 1
    if failures > 0 {
      failures -= 1
      throw Failure()
    }
    return entries
  }

  func release() { releases += 1 }
}

private func entry(_ pid: String, album: String, art: String?) -> ArtEntry {
  ArtEntry(pid: pid, albumPID: album, hasArtwork: art != nil, data: { art.map { Data($0.utf8) } })
}

/// An index on its own queue with a clock the test moves.
private final class Rig {
  let source: FakeSource
  let queue = DispatchQueue(label: "test.art")
  var seconds: UInt64 = 1000
  var logs: [String] = []
  var index: ArtIndex!

  init(_ entries: [ArtEntry], idleFor: Double = 120) {
    source = FakeSource(entries)
    index = ArtIndex(
      queue: queue, source: source, idleFor: idleFor,
      clock: { [unowned self] in DispatchTime(uptimeNanoseconds: self.seconds * 1_000_000_000) },
      enabled: { true }, log: { [unowned self] in self.logs.append($0) })
  }

  func advance(_ s: UInt64) { seconds += s }
  func data(_ pid: String) -> String? { queue.sync { index.imageData(pid) }.map { String(decoding: $0, as: UTF8.self) } }
}

private let library = [
  entry("T1", album: "A1", art: "cover-1"),
  entry("T2", album: "A1", art: nil),
  entry("T3", album: "A2", art: "cover-2"),
]

@Suite struct ArtIndexTests {
  @Test func answersTrackAndAlbumPIDs() {
    let r = Rig(library)
    #expect(r.data("T1") == "cover-1")
    #expect(r.data("A1") == "cover-1")
    #expect(r.data("A2") == "cover-2")
    #expect(r.data("T2") == nil)
    #expect(r.source.loads == 1)
  }

  /// A request after the index was released loads it again and gets the cover,
  /// not "none".
  @Test func requestAfterReleaseGetsTheFile() {
    let r = Rig(library)
    #expect(r.data("A1") == "cover-1")
    r.queue.sync { r.index.release() }
    #expect(r.queue.sync { r.index.isLoaded } == false)
    #expect(r.source.releases == 1)
    #expect(r.data("A1") == "cover-1")
    #expect(r.data("T3") == "cover-2")
    #expect(r.source.loads == 2)
  }

  /// The release the queue schedules itself, after the idle time, and a request
  /// after it works.
  @Test func idleReleaseThenRequest() async throws {
    let r = Rig(library, idleFor: 0.05)
    #expect(r.data("A1") == "cover-1")
    r.advance(1) // the clock says the index was idle for longer than idleFor
    for _ in 0..<100 {
      if !r.queue.sync(execute: { r.index.isLoaded }) { break }
      try await Task.sleep(for: .milliseconds(20))
    }
    #expect(r.queue.sync { r.index.isLoaded } == false)
    #expect(r.data("A1") == "cover-1")
    #expect(r.queue.sync { r.index.isLoaded })
  }

  /// The release waits while the clock says the index was used meanwhile.
  @Test func busyIndexIsNotReleased() async throws {
    let r = Rig(library, idleFor: 0.05)
    #expect(r.data("A1") == "cover-1")
    try await Task.sleep(for: .milliseconds(200)) // the clock hasn't moved: not idle
    #expect(r.queue.sync { r.index.isLoaded })
    #expect(r.source.releases == 0)
  }

  /// After a reload that failed, requests go to Music for a few seconds and then
  /// the index loads again.
  @Test func failedLoadIsRetriedSoon() {
    let r = Rig(library)
    r.source.failures = 1
    #expect(r.data("A1") == nil)
    #expect(r.source.loads == 1)
    r.advance(2)
    #expect(r.data("A1") == nil) // inside the back-off: no new load
    #expect(r.source.loads == 1)
    r.advance(4)
    #expect(r.data("A1") == "cover-1")
    #expect(r.source.loads == 2)
    #expect(r.logs.first?.contains("using Music for artwork") == true)
  }

  @Test func reloadAfterReleaseThatFailsRecovers() {
    let r = Rig(library)
    #expect(r.data("A1") == "cover-1")
    r.queue.sync { r.index.release() }
    r.source.failures = 2
    #expect(r.data("A1") == nil)
    r.advance(6)
    #expect(r.data("A1") == nil)
    r.advance(6)
    #expect(r.data("A1") == "cover-1")
  }

  /// ITLibrary can answer with nothing while its service starts. That is not an
  /// index to keep for a minute.
  @Test func emptyLoadIsAFailure() {
    let r = Rig([])
    #expect(r.data("A1") == nil)
    #expect(r.queue.sync { r.index.isLoaded } == false)
    r.source.entries = library
    r.advance(6)
    #expect(r.data("A1") == "cover-1")
    #expect(r.queue.sync { r.index.isLoaded })
  }

  @Test func unknownPIDReloadsAtMostOnceAMinute() {
    let r = Rig(library)
    #expect(r.data("A1") == "cover-1")
    #expect(r.data("nope") == nil)
    #expect(r.source.loads == 1)
    r.advance(61)
    r.source.entries.append(entry("T9", album: "A9", art: "cover-9"))
    #expect(r.data("A9") == "cover-9")
    #expect(r.source.loads == 2)
    #expect(r.data("nope") == nil)
    #expect(r.source.loads == 2)
  }

  @Test func disabledIndexAnswersNothing() {
    let source = FakeSource(library)
    let index = ArtIndex(queue: DispatchQueue(label: "t"), source: source, enabled: { false }, log: { _ in })
    #expect(index.imageData("A1") == nil)
    #expect(source.loads == 0)
  }
}

@Suite struct ArtFallbackTests {
  /// Music's "nothing" is only an answer once a snapshot can tell albums from
  /// tracks.
  @Test func cannotTellTable() {
    #expect(ArtworkHandlers.cannotTell(found: false, mapped: false, hasSnapshot: false))
    #expect(!ArtworkHandlers.cannotTell(found: false, mapped: false, hasSnapshot: true))
    #expect(!ArtworkHandlers.cannotTell(found: false, mapped: true, hasSnapshot: false))
    #expect(!ArtworkHandlers.cannotTell(found: true, mapped: false, hasSnapshot: false))
  }
}

import Foundation
import Testing
@testable import encore_bridge

@Test func `streamed snapshot is the same JSON as JSONEncoder's`() throws {
  var tracks: [SnapshotTrack] = []
  for i in 0..<4_321 { // more than two chunks, not a multiple of the chunk size
    var t = SnapshotTrack()
    t.pid = String(format: "%016llX", UInt64(i))
    t.title = "Song \(i) \"quoted\" / slash é 日本"
    t.artist = "Artist \(i / 12)"
    t.duration = 200_000
    t.favorited = i % 7 == 0
    t.storeId = i % 3 == 0 ? Int64(1_000 + i) : nil
    tracks.append(t)
  }
  let playlists = [
    SnapshotPlaylist(pid: "A", parentPid: "", name: "Mix", kind: "user", special: "", items: ["0", "1"], globalId: "pl.x"),
    SnapshotPlaylist(pid: "B", parentPid: "A", name: "Empty", kind: "folder", special: "", items: [], globalId: nil),
  ]
  let snapshot = LibrarySnapshot(generatedAt: 1_760_000_000, tracks: tracks, playlists: playlists)

  let path = NSTemporaryDirectory() + "snapshot-writer-\(UUID().uuidString).json"
  defer { try? FileManager.default.removeItem(atPath: path) }
  let size = try SnapshotWriter.write(snapshot, to: path)

  let streamed = try Data(contentsOf: URL(fileURLWithPath: path))
  #expect(size == streamed.count)
  #expect(!FileManager.default.fileExists(atPath: path + ".tmp"))

  let encoder = JSONEncoder()
  encoder.outputFormatting = [.withoutEscapingSlashes]
  let whole = try encoder.encode(snapshot)
  let a = try JSONSerialization.jsonObject(with: streamed) as! NSDictionary
  let b = try JSONSerialization.jsonObject(with: whole) as! NSDictionary
  #expect(a == b)
  #expect((a["tracks"] as! [Any]).count == 4_321)
}

@Test func `empty snapshot streams as valid JSON`() throws {
  let path = NSTemporaryDirectory() + "snapshot-writer-\(UUID().uuidString).json"
  defer { try? FileManager.default.removeItem(atPath: path) }
  try SnapshotWriter.write(LibrarySnapshot(generatedAt: 5, tracks: [], playlists: []), to: path)
  let obj = try JSONSerialization.jsonObject(with: Data(contentsOf: URL(fileURLWithPath: path))) as! NSDictionary
  #expect(obj["generatedAt"] as? Int == 5)
  #expect((obj["tracks"] as! [Any]).isEmpty && (obj["playlists"] as! [Any]).isEmpty)
}

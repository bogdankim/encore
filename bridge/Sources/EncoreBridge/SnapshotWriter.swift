import Foundation

/// Writes a `LibrarySnapshot` as JSON without building it all in memory.
///
/// `JSONEncoder.encode(snapshot)` holds a tree of the whole document next to the
/// output: measured with a 100,000-track snapshot it peaked at +481 MB resident
/// for a 66 MB file. This encodes the tracks a chunk at a time and appends each
/// chunk to the file, which peaked at +17 MB for the same bytes (and the same
/// time, about 0.7 s). The JSON means the same; only the key order inside an
/// object may differ from run to run, as it does with JSONEncoder.
enum SnapshotWriter {
  static let chunk = 2_000

  /// Writes `snapshot` to `path` atomically (a temporary file next to it is
  /// renamed over it) and returns the size in bytes.
  @discardableResult
  static func write(_ snapshot: LibrarySnapshot, to path: String) throws -> Int {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.withoutEscapingSlashes]
    let tmp = path + ".tmp"
    guard FileManager.default.createFile(atPath: tmp, contents: nil) else {
      throw Server.BridgeError.internalError("can't create \(tmp)")
    }
    do {
      let out = try FileHandle(forWritingTo: URL(fileURLWithPath: tmp))
      defer { try? out.close() }
      var size = 0
      func put(_ d: Data) throws {
        try out.write(contentsOf: d)
        size += d.count
      }
      try put(Data("{\"generatedAt\":\(snapshot.generatedAt),\"tracks\":[".utf8))
      var i = 0
      while i < snapshot.tracks.count {
        let end = min(i + chunk, snapshot.tracks.count)
        try autoreleasepool {
          var d = try encoder.encode(Array(snapshot.tracks[i..<end]))
          d.removeFirst() // [
          d.removeLast() // ]
          if i > 0 { try put(Data(",".utf8)) }
          try put(d)
        }
        i = end
      }
      try put(Data("],\"playlists\":".utf8))
      try put(try encoder.encode(snapshot.playlists))
      try put(Data("}".utf8))
      try out.close()
      if rename(tmp, path) != 0 {
        throw Server.BridgeError.internalError("can't rename \(tmp): \(String(cString: strerror(errno)))")
      }
      return size
    } catch {
      try? FileManager.default.removeItem(atPath: tmp)
      throw error
    }
  }
}

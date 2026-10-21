import Foundation

/// Plays an Apple Music radio station in Music by station ID ("ra.…") through
/// the same MediaRemote system app playback queue as StoreQueue, with a queue
/// of type 2 (RadioStation). Personal ("ra.u-…"), artist ("ra.<artistId>",
/// "ra.a-<artistId>") and live stations ("ra.978194965", Apple Music 1) all
/// start this way; Music then picks every song itself. Verified live on
/// macOS 27 (see docs/research, "Station playback").
enum StationQueue {
  private typealias SetI32 = @convention(c) (OpaquePointer, Int32) -> Void
  private typealias SetStr = @convention(c) (OpaquePointer, CFString) -> Void

  private static let setIDType = StoreQueue.sym("MRSystemAppPlaybackQueueSetRadioStationIDType", SetI32.self)
  private static let setString = StoreQueue.sym("MRSystemAppPlaybackQueueSetRadioStationStringIdentifier", SetStr.self)

  /// MRSystemAppPlaybackQueueCopyTypeDescription(2) is "RadioStation".
  static let typeRadioStation: Int32 = 2
  /// The station ID kind: 1 is an integer PID, 2 a string identifier. The
  /// string setter traps unless the kind is 2.
  private static let idTypeString: Int32 = 2

  static var isAvailable: Bool { StoreQueue.isAvailable && setIDType != nil && setString != nil }

  /// Replaces Music's queue with the station `id` and asks it to start. Blocks
  /// until MediaRemote answers, up to `timeout`: 1–3 s, and audio starts at
  /// about the same time.
  static func play(_ id: String, timeout: TimeInterval = 5) throws {
    guard isAvailable, let create = StoreQueue.create, let setIDType, let setString,
          let q = create(kCFAllocatorDefault, typeRadioStation) else { throw StoreQueue.Failure.unavailable }
    setIDType(q, idTypeString)
    setString(q, id as CFString)
    try StoreQueue.send(q, timeout: timeout)
  }
}

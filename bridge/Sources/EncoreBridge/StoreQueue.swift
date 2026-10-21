import Foundation

/// Plays Apple Music catalog songs in Music by store ID through MediaRemote's
/// system app playback queue, the private API Siri and HomePod handoff use.
/// Music streams them whether or not they are in the library. Signatures were
/// read from the macOS 27 MediaRemote disassembly and verified live (see
/// docs/research, "Catalog playback").
///
/// A queue of type 3 (iTunes Store tracklist) goes to the system media
/// application, which is Music, not to whatever app is now playing.
enum StoreQueue {
  typealias Create = @convention(c) (CFAllocator?, Int32) -> OpaquePointer?
  private typealias SetIDs = @convention(c) (OpaquePointer, CFArray) -> Void
  typealias SetBool = @convention(c) (OpaquePointer, Bool) -> Void
  typealias Destroy = @convention(c) (OpaquePointer) -> Void
  // The completion's second argument is not always a CFArray (retaining it
  // crashed), so it stays an untyped pointer that is never touched.
  private typealias SetQueue = @convention(c) (
    OpaquePointer, DispatchQueue?, (@convention(block) (UInt32, UnsafeRawPointer?) -> Void)?
  ) -> Void
  private typealias Describe = @convention(c) (UInt32) -> Unmanaged<CFString>?

  private static let lib = dlopen("/System/Library/PrivateFrameworks/MediaRemote.framework/MediaRemote", RTLD_LAZY)
  /// A MediaRemote function by name, or nil when this macOS lacks it.
  static func sym<T>(_ name: String, _: T.Type) -> T? {
    guard let lib, let p = dlsym(lib, name) else { return nil }
    return unsafeBitCast(p, to: T.self)
  }
  static let create = sym("MRSystemAppPlaybackQueueCreate", Create.self)
  private static let setIDs = sym("MRSystemAppPlaybackQueueSetTracklistStoreIDs", SetIDs.self)
  static let setImmediate = sym("MRSystemAppPlaybackQueueSetIsRequestingImmediatePlayback", SetBool.self)
  static let destroy = sym("MRSystemAppPlaybackQueueDestroy", Destroy.self)
  private static let setQueue = sym("MRMediaRemoteSetSystemAppPlaybackQueue", SetQueue.self)
  private static let describe = sym("MRMediaRemoteCopySendCommandErrorDescription", Describe.self)

  /// MRSystemAppPlaybackQueueCopyTypeDescription(3) is "iTunesStoreTracklist".
  static let typeStoreTracklist: Int32 = 3

  static var isAvailable: Bool {
    create != nil && setIDs != nil && setImmediate != nil && destroy != nil && setQueue != nil
  }

  enum Failure: Error {
    case unavailable
    case timedOut
    /// A MediaRemote send-command error: 1 ApplicationNotFound, 2 ConnectionFailed,
    /// 3 CommandIgnored, 4 CouldNotLaunchApplication, 5 TimedOut, 7 InvalidCommandOptions,
    /// 8 NoCommandHandlers, 9 ApplicationNotInstalled, 10 NotSupported, 11 XPC.
    case command(UInt32, String)
  }

  static func describe(_ code: UInt32) -> String {
    describe?(code)?.takeRetainedValue() as String? ?? "error \(code)"
  }

  /// Replaces Music's queue with `storeIDs` (catalog song ids, in play order) and
  /// asks it to start the first one. Blocks until MediaRemote answers, up to
  /// `timeout`. Audio starts a few seconds later, while Music fetches the songs.
  static func play(_ storeIDs: [String], timeout: TimeInterval = 5) throws {
    guard isAvailable, let create, let setIDs,
          let q = create(kCFAllocatorDefault, typeStoreTracklist) else { throw Failure.unavailable }
    setIDs(q, storeIDs as CFArray)
    try send(q, timeout: timeout)
  }

  /// Asks for immediate playback of `q` and hands it to Music as the system
  /// media app, then destroys it. Blocks until MediaRemote answers, up to `timeout`.
  static func send(_ q: OpaquePointer, timeout: TimeInterval) throws {
    guard let setImmediate, let destroy, let setQueue else { throw Failure.unavailable }
    setImmediate(q, true)
    let done = DispatchSemaphore(value: 0)
    let lock = NSLock()
    var result: UInt32?
    setQueue(q, DispatchQueue.global()) { err, _ in
      destroy(q)
      lock.lock()
      result = err
      lock.unlock()
      done.signal()
    }
    // On a timeout the callback may still come; it destroys the queue then.
    guard done.wait(timeout: .now() + timeout) == .success else { throw Failure.timedOut }
    lock.lock()
    let err = result ?? 0
    lock.unlock()
    if err != 0 { throw Failure.command(err, describe(err)) }
  }
}

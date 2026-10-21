import AppKit
import Foundation

/// Music.app lifecycle helpers shared by the handlers.
extension MusicAE {
  static var isAppRunning: Bool {
    !NSRunningApplication.runningApplications(withBundleIdentifier: bundleID).isEmpty
  }

  /// True for the errors a send returns when Music isn't running or quit mid-call.
  static func isNotRunning(_ error: Error) -> Bool {
    guard let e = error as? MusicAEError else { return false }
    return e.code == -600 || e.code == -609
  }

  /// Runs `body`. If Music isn't running, launches it in the background, waits until
  /// it answers Apple Events and runs `body` once more.
  func withMusicLaunched<T>(_ body: () throws -> T) throws -> T {
    do {
      return try body()
    } catch let error where MusicAE.isNotRunning(error) {
      // -609: the connection broke, so Music is quitting or just quit.
      if (error as? MusicAEError)?.code == -609 { MusicAE.noteQuit() }
      try launchAndWait()
      return try body()
    }
  }

  /// How long after Music quits before encore starts it again. A Music launched
  /// while the last one is still shutting down can fail to open the library: it
  /// says the library can't be found and asks the user to choose one. On macOS 27
  /// that followed relaunches 0.1 to 0.6 s after a quit.
  static let relaunchGrace: TimeInterval = 3

  private static let quitLock = NSLock()
  private static var lastQuit: Date?

  /// Records that Music quit (didTerminateApplication, or a -609 reply).
  static func noteQuit(at date: Date = Date()) {
    quitLock.withLock { lastQuit = date }
  }

  /// The earliest time encore may launch Music again, if it saw Music quit.
  static var relaunchAllowedAt: Date? {
    quitLock.withLock { lastQuit?.addingTimeInterval(relaunchGrace) }
  }

  /// Launches Music without activating it and waits until it answers, up to
  /// `timeout` seconds. A Music that is still quitting is left to finish first: it
  /// refuses events (-609, then -600 while its process is still listed), and
  /// launching then would start a second Music next to the one shutting down.
  func launchAndWait(timeout: TimeInterval = 10) throws {
    let deadline = Date().addingTimeInterval(timeout)
    var launched = false
    while true {
      if !launched && !MusicAE.isAppRunning {
        if let allowed = MusicAE.relaunchAllowedAt {
          let wait = min(allowed, deadline).timeIntervalSinceNow
          if wait > 0 { usleep(useconds_t(wait * 1_000_000)) }
        }
        if !MusicAE.isAppRunning { try launch(deadline: deadline) }
        launched = true
      }
      do {
        _ = try send(fcc("core"), fcc("getd"), direct: prop(fcc("pPlS")), timeout: 2)
        return
      } catch let e as MusicAEError where e.code != -1743 && e.code != -1744 && Date() < deadline {
        usleep(200_000)
      }
    }
  }

  private func launch(deadline: Date) throws {
    guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: MusicAE.bundleID) else {
      throw MusicAEError(code: -600, message: "Music.app is not installed")
    }
    let config = NSWorkspace.OpenConfiguration()
    config.activates = false
    config.addsToRecentItems = false
    let done = DispatchSemaphore(value: 0)
    var launchError: Error?
    NSWorkspace.shared.openApplication(at: url, configuration: config) { _, error in
      launchError = error
      done.signal()
    }
    _ = done.wait(timeout: .now() + max(1, deadline.timeIntervalSinceNow))
    if let launchError {
      throw MusicAEError(code: -600, message: "could not launch Music: \(launchError.localizedDescription)")
    }
  }

  /// Polls until Music answers an Apple Event. Never launches it. A freshly launched
  /// Music refuses events for a moment (-600, -609, -1712...).
  func waitUntilAnswering(timeout: TimeInterval) throws {
    let deadline = Date().addingTimeInterval(timeout)
    while true {
      do {
        _ = try send(fcc("core"), fcc("getd"), direct: prop(fcc("pPlS")), timeout: 2)
        return
      } catch let e as MusicAEError where e.code != -1743 && e.code != -1744 && Date() < deadline {
        usleep(200_000)
      }
    }
  }

  /// Persistent IDs from a list reply (`persistent ID of every track of ...`).
  static func strings(_ desc: NSAppleEventDescriptor?) -> [String] {
    guard let desc else { return [] }
    if desc.descriptorType != fcc("list") { return desc.stringValue.map { [$0] } ?? [] }
    guard desc.numberOfItems > 0 else { return [] }
    return (1...desc.numberOfItems).compactMap { desc.atIndex($0)?.stringValue }
  }
}

extension NSAppleEventDescriptor {
  var isMissingValue: Bool { descriptorType == fcc("msng") }
}

func unixMillis(_ date: Date = Date()) -> Int64 {
  Int64((date.timeIntervalSince1970 * 1000).rounded())
}

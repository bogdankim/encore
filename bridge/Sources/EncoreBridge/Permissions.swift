import CoreServices
import Darwin
import Foundation
import iTunesLibrary

/// The two privacy (TCC) permissions the bridge needs: Automation of Music, for Apple
/// Events, and Media & Apple Music, for ITLibrary. encore spawns the bridge as its own
/// responsible process (internal/bridge/spawn_darwin.go), so macOS asks about encore
/// itself, with the usage strings in bridge/Info.plist. `start` asks for both at
/// launch, one after the other, so the prompts come right away and not mid-use.
///
/// Only this class prompts. Every other Apple Event goes out with
/// kAEDoNotPromptForUserConsent (MusicAE.send), and while Automation isn't settled
/// (being checked, the prompt is up, or Music is still launching) MusicAE.send fails
/// at once instead of waiting behind the prompt, so no queue stalls and encore's
/// watchdog has nothing to time out on.
final class Permissions: @unchecked Sendable {
  static let shared = Permissions()

  /// ENCORE_NO_ITLIBRARY=1 acts as if Media & Apple Music access was denied, to test
  /// the Apple Event paths.
  static let itlibraryOff: Bool = {
    let v = ProcessInfo.processInfo.environment["ENCORE_NO_ITLIBRARY"] ?? ""
    return v != "" && v != "0"
  }()

  /// ENCORE_NO_PROMPT=1 never shows a permission prompt: what TCC hasn't decided
  /// stays undecided. For testing against a real Music without new prompts.
  static let noPrompt: Bool = {
    let v = ProcessInfo.processInfo.environment["ENCORE_NO_PROMPT"] ?? ""
    return v != "" && v != "0"
  }()

  /// Why Apple Events to Music are held back.
  enum Hold {
    case none
    /// Asking TCC (without prompting) at launch.
    case checking
    /// The Automation prompt is up, here or in another encore-bridge.
    case prompting
    /// Music just launched and TCC can't answer for it yet.
    case launching
  }

  private let queue = DispatchQueue(label: "encore.permissions")
  private let lock = NSLock()
  private var library: String
  private var hold = Hold.none
  /// This process showed the prompt to control Music. It shows it at most once.
  private var askedAutomation = false
  /// TCC said Automation is granted, so a Music launch needs no hold.
  private var automationGranted = false
  private var settled = false
  private let decided = DispatchGroup()

  private init() {
    library = Permissions.libraryPreflight()
    decided.enter()
  }

  /// Media & Apple Music: "granted", "denied" or "unknown" (not decided yet).
  var libraryState: String { lock.withLock { library } }

  /// What holds Apple Events back now.
  var automationHold: Hold { lock.withLock { hold } }

  /// True while Automation is being checked or asked for. hello then reports
  /// "pending", and Apple Events fail at once with -1744.
  var isAutomationPending: Bool {
    let h = automationHold
    return h == .checking || h == .prompting
  }

  private func setHold(_ h: Hold) {
    lock.withLock { hold = h }
  }

  /// Waits up to `timeout` for the launch-time Media & Apple Music decision and
  /// reports whether ITLibrary may be used.
  func libraryGranted(waiting timeout: TimeInterval) -> Bool {
    _ = decided.wait(timeout: .now() + timeout)
    return libraryState == "granted"
  }

  /// Runs `body` on `queue` once Media & Apple Music is decided.
  func whenLibraryDecided(on queue: DispatchQueue, _ body: @escaping (_ granted: Bool) -> Void) {
    decided.notify(queue: queue) { body(self.libraryState == "granted") }
  }

  /// Asks at launch: Automation first, since nothing works without it, then Media &
  /// Apple Music. After a prompt it tells encore the answers and asks for a new
  /// snapshot, since snapshots taken meanwhile fell back or failed.
  ///
  /// Call before any handler can send an Apple Event. Apple Events are held from
  /// here until TCC has said whether it needs to ask; this waits up to half a
  /// second for that, which is plenty when the answer is on file. If TCC takes
  /// longer (another prompt is up), encore hears "pending" right away.
  func start(_ server: Server, _ music: MusicAE) {
    guard MusicAE.isAppRunning else {
      queue.async { if self.settleLibrary(server.log) { self.announce(server, music, musicChanged: false) } }
      return
    }
    setHold(.checking)
    let checked = DispatchSemaphore(value: 0)
    queue.async {
      let askedAutomation = self.settleAutomation(music, server, checked: checked)
      let askedLibrary = self.settleLibrary(server.log)
      if askedAutomation || askedLibrary { self.announce(server, music, musicChanged: askedAutomation) }
    }
    if checked.wait(timeout: .now() + 0.5) == .timedOut {
      server.log("TCC is slow to answer for Music; holding Apple Events until it does")
      emitPending(server)
    }
  }

  /// Music is launching (NSWorkspace said so). Unless Automation is known to be
  /// granted, hold Apple Events until TCC can answer for the new Music, so a call
  /// made meanwhile can't wait behind a prompt. Runs on the posting thread, before
  /// `musicLaunched`.
  func musicLaunching() {
    lock.withLock { if hold == .none && !automationGranted { hold = .launching } }
  }

  /// Music just launched: ask for Automation now if launch couldn't, waiting a few
  /// seconds for Music to register. Runs off the music queue.
  func musicLaunched(_ server: Server, _ music: MusicAE) {
    // TCC answers at once unless a prompt is up somewhere; then it can take until
    // that prompt is answered, and encore should hear why nothing works.
    var upgraded = false // guarded by lock
    let status = automationStatus(music, waitForLaunch: 10) {
      let now = self.lock.withLock { () -> Bool in
        guard self.hold == .launching else { return false }
        self.hold = .prompting
        upgraded = true
        return true
      }
      if now { self.emitPending(server) }
    }
    guard status == OSStatus(errAEEventWouldRequireUserConsent) else {
      noteStatus(status)
      let held = lock.withLock { () -> Bool in
        if hold == .launching || upgraded { hold = .none }
        return upgraded
      }
      if held { announce(server, music, musicChanged: true) }
      return
    }
    // The prompt may have to wait for the launch-time Media & Apple Music prompt.
    setHold(.prompting)
    emitPending(server)
    queue.sync {
      if settleAutomation(music, server) { announce(server, music, musicChanged: true) }
    }
  }

  private func emitPending(_ server: Server) {
    server.emit("permissions", ["automation": "pending", "library": libraryState])
  }

  private func announce(_ server: Server, _ music: MusicAE, musicChanged: Bool) {
    server.emit("permissions", ["automation": HelloHandlers.automation(music), "library": libraryState])
    server.emit("library", [String: Any]())
    if musicChanged && MusicAE.isAppRunning { server.emit("music", ["running": true]) }
  }

  /// TCC's Automation answer for Music without prompting, polling while Music
  /// hasn't registered yet (procNotFound), up to `waitForLaunch` seconds. This
  /// blocks while a prompt is up (seen on macOS 27: minutes, until it's
  /// answered); `slow` runs once on another thread when one call takes over a
  /// second.
  private func automationStatus(_ music: MusicAE, waitForLaunch: TimeInterval = 0, slow: (() -> Void)? = nil) -> OSStatus {
    guard let desc = music.target.aeDesc else { return OSStatus(procNotFound) }
    var warned = false
    func determine() -> OSStatus {
      guard let slow, !warned else { return AEDeterminePermissionToAutomateTarget(desc, typeWildCard, typeWildCard, false) }
      let started = Date()
      let watch = DispatchWorkItem(block: slow)
      DispatchQueue.global().asyncAfter(deadline: .now() + 1, execute: watch)
      let status = AEDeterminePermissionToAutomateTarget(desc, typeWildCard, typeWildCard, false)
      watch.cancel()
      warned = Date().timeIntervalSince(started) >= 1
      return status
    }
    let deadline = Date().addingTimeInterval(waitForLaunch)
    var status = determine()
    while status == OSStatus(procNotFound) && Date() < deadline {
      usleep(200_000)
      status = determine()
    }
    return status
  }

  private func noteStatus(_ status: OSStatus) {
    if status == noErr { lock.withLock { automationGranted = true } }
  }

  /// Settles Automation for Music, then releases the hold. Shows the prompt only
  /// when TCC hasn't decided, this process hasn't asked before, and no other
  /// encore-bridge has a prompt up; otherwise it waits for that one's answer.
  /// Returns whether the answer may have changed. `checked` is signalled once TCC
  /// has said whether it needs to ask.
  private func settleAutomation(_ music: MusicAE, _ server: Server, checked: DispatchSemaphore? = nil) -> Bool {
    var signalled = false
    func signal() {
      if !signalled { checked?.signal() }
      signalled = true
    }
    defer {
      setHold(.none)
      signal()
    }
    let consent = OSStatus(errAEEventWouldRequireUserConsent)
    var status = automationStatus(music)
    noteStatus(status)
    guard status == consent else { return false }
    setHold(.prompting)
    signal()
    if checked != nil { emitPending(server) }

    let marker = PromptMarker.automation
    if marker.heldElsewhere() {
      server.log("another encore-bridge asked to control Music; waiting for that answer")
      while marker.heldElsewhere() {
        usleep(1_000_000)
        status = automationStatus(music)
        if status != consent {
          noteStatus(status)
          server.log(status == noErr ? "Automation for Music: granted" : "Automation for Music: not granted (\(status))")
          return true
        }
      }
    }
    let first = lock.withLock { () -> Bool in
      defer { askedAutomation = true }
      return !askedAutomation
    }
    guard first, let desc = music.target.aeDesc else { return false }
    if Permissions.noPrompt {
      server.log("ENCORE_NO_PROMPT: not asking for permission to control Music")
      return false
    }
    server.log("asking for permission to control Music")
    marker.claim()
    status = AEDeterminePermissionToAutomateTarget(desc, fcc("core"), fcc("getd"), true)
    marker.release()
    noteStatus(status)
    server.log(status == noErr ? "Automation for Music: granted" : "Automation for Music: not granted (\(status))")
    return true
  }

  /// Decides Media & Apple Music once: asks TCC, prompting when it hasn't decided,
  /// and when TCC can't say, tries ITLibrary, which asks TCC itself through
  /// AMPLibraryAgent. Waits instead while another encore-bridge's prompt is up.
  /// Returns whether the user may have been asked.
  @discardableResult
  func settleLibrary(_ log: (String) -> Void) -> Bool {
    if lock.withLock({ settled }) { return false }
    var state = Permissions.libraryPreflight()
    var asked = false
    let marker = PromptMarker.library
    if state == "unknown" && marker.heldElsewhere() {
      log("another encore-bridge asked for Media & Apple Music access; waiting for that answer")
      while state == "unknown" && marker.heldElsewhere() {
        usleep(1_000_000)
        state = Permissions.libraryPreflight()
      }
      asked = state != "unknown"
    }
    if state == "unknown" && Permissions.noPrompt {
      log("ENCORE_NO_PROMPT: not asking for Media & Apple Music access")
    } else if state == "unknown" {
      marker.claim()
      if let request = TCC.request {
        asked = true
        log("asking for Media & Apple Music access")
        let done = DispatchSemaphore(value: 0)
        var granted = false
        request("kTCCServiceMediaLibrary" as CFString, nil) { ok in
          granted = ok
          done.signal()
        }
        done.wait()
        state = granted ? "granted" : Permissions.libraryPreflight()
      }
      if state == "unknown" {
        asked = true
        state = autoreleasepool { (try? ITLibrary(apiVersion: "1.0")) != nil } ? "granted" : "denied"
      }
      marker.release()
    }
    log("Media & Apple Music: \(state)" + (Permissions.itlibraryOff ? " (ENCORE_NO_ITLIBRARY)" : ""))
    lock.withLock {
      library = state
      settled = true
    }
    decided.leave()
    return asked
  }

  /// TCC's answer for Media & Apple Music without prompting.
  static func libraryPreflight() -> String {
    if itlibraryOff { return "denied" }
    guard let preflight = TCC.preflight else { return "unknown" }
    switch preflight("kTCCServiceMediaLibrary" as CFString, nil) {
    case 0: return "granted"
    case 1: return "denied"
    default: return "unknown"
    }
  }
}

/// A small file that says an encore-bridge is showing a permission prompt, so a
/// bridge started meanwhile (encore restarted it, or a second encore runs) waits
/// for that answer instead of asking again. It holds the asking pid and the time
/// it asked. It counts while that process lives and for a minute after it died,
/// since its prompt can stay up; after ten minutes it no longer counts.
struct PromptMarker {
  static let automation = PromptMarker(name: "automation")
  static let library = PromptMarker(name: "media-library")

  let url: URL

  init(name: String) {
    let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first
      ?? URL(fileURLWithPath: NSTemporaryDirectory())
    url = caches.appendingPathComponent("encore/prompts/\(name)")
  }

  func claim() {
    try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
    try? "\(getpid()) \(Int(Date().timeIntervalSince1970))\n".write(to: url, atomically: true, encoding: .utf8)
  }

  func release() {
    try? FileManager.default.removeItem(at: url)
  }

  /// Another process's prompt may be up.
  func heldElsewhere() -> Bool {
    guard let text = try? String(contentsOf: url, encoding: .utf8) else { return false }
    let parts = text.split(whereSeparator: \.isWhitespace)
    guard parts.count == 2, let pid = Int32(parts[0]), let at = Double(parts[1]) else { return false }
    if pid == getpid() { return false }
    let age = Date().timeIntervalSince1970 - at
    guard age >= -60, age < 600 else { return false }
    if kill(pid, 0) == 0 || errno == EPERM { return true }
    return age < 60
  }
}

/// Private TCC.framework calls, looked up at run time so a missing symbol just means
/// "unknown". TCCAccessPreflight: 0 granted, 1 denied, 2 not decided.
private enum TCC {
  typealias Preflight = @convention(c) (CFString, CFDictionary?) -> Int32
  typealias Request = @convention(c) (CFString, CFDictionary?, @escaping @convention(block) (Bool) -> Void) -> Void

  static let handle = dlopen("/System/Library/PrivateFrameworks/TCC.framework/TCC", RTLD_LAZY)
  static let preflight: Preflight? = symbol("TCCAccessPreflight")
  static let request: Request? = symbol("TCCAccessRequest")

  private static func symbol<T>(_ name: String) -> T? {
    guard let handle, let sym = dlsym(handle, name) else { return nil }
    return unsafeBitCast(sym, to: T.self)
  }
}

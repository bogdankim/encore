import CoreServices
import Foundation

/// `hello`: protocol version, whether Music runs, and the Automation and Media &
/// Apple Music permissions.
enum HelloHandlers {
  static func register(_ server: Server, _ music: MusicAE) {
    // Its own queue: hello must answer even while a snapshot or a slow command runs.
    let queue = DispatchQueue(label: "encore.hello")
    server.register("hello", on: queue) { _ in
      fields(music)
    }
  }

  static func fields(_ music: MusicAE) -> [String: Any] {
    ["version": "1", "musicRunning": MusicAE.isAppRunning, "automation": automation(music),
     "library": Permissions.shared.libraryState]
  }

  /// Asks TCC without prompting. Music must be running for a definite answer; when
  /// it isn't, the result is "undetermined". "pending" while the bridge checks or
  /// waits for the prompt, so hello never waits on TCC then.
  static func automation(_ music: MusicAE) -> String {
    if Permissions.shared.isAutomationPending { return "pending" }
    guard let desc = music.target.aeDesc else { return "undetermined" }
    let status = AEDeterminePermissionToAutomateTarget(desc, typeWildCard, typeWildCard, false)
    switch status {
    case noErr: return "granted"
    case OSStatus(errAEEventNotPermitted): return "denied"
    default: return "undetermined"
    }
  }
}

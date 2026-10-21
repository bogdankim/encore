import Foundation

/// Play and pause through MediaRemote (private framework), addressed to Music by
/// bundle ID. A fallback for when Music ignores the Apple Event: in some playback
/// sessions Music's scripting layer believes it is paused while it plays, and then
/// drops every `pause` and turns `playpause` into `play` (see docs/research). The
/// same command sent through MediaRemote reaches Music's playback engine directly.
enum MediaRemote {
  enum Command: UInt32 {
    case play = 0
    case pause = 1
  }

  private typealias SendToApp = @convention(c) (
    UInt32, CFDictionary?, UnsafeRawPointer?, CFString, UInt32, DispatchQueue?,
    (@convention(block) (UInt32, CFArray?) -> Void)?
  ) -> Void

  private static let sendToApp: SendToApp? = {
    guard let lib = dlopen("/System/Library/PrivateFrameworks/MediaRemote.framework/MediaRemote", RTLD_LAZY),
          let sym = dlsym(lib, "MRMediaRemoteSendCommandToApp") else { return nil }
    return unsafeBitCast(sym, to: SendToApp.self)
  }()

  static var isAvailable: Bool { sendToApp != nil }

  /// Sends `command` to Music without waiting. Returns false when MediaRemote is unavailable.
  @discardableResult
  static func send(_ command: Command) -> Bool {
    guard let sendToApp else { return false }
    sendToApp(command.rawValue, nil, nil, MusicAE.bundleID as CFString, 0, DispatchQueue.global()) { _, _ in }
    return true
  }
}

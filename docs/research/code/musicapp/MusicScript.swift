// MusicScript.swift: compile an AppleScript "library" once, then call its handlers with arguments
// (NSAppleScript.executeAppleEvent + 'ascr'/'psbr'). Use for multi-step commands that are awkward as raw AEs.
// NSAppleScript is not thread-safe: own one instance per thread/serial queue. Verified to work off the main thread.
import Foundation

final class MusicScript {
  static let source = #"""
  on nowPlaying()
    tell application "Music"
      if player state is stopped then return {"stopped"}
      set t to current track
      return {player state as text, player position, name of t, artist of t, album of t, duration of t, persistent ID of t, database ID of t}
    end tell
  end nowPlaying

  on playLibraryTrack(pid)
    tell application "Music" to play (first track of library playlist 1 whose persistent ID is pid)
  end playLibraryTrack

  on playPlaylist(pid)
    tell application "Music" to play (first playlist whose persistent ID is pid)
  end playPlaylist

  -- Reuses (or creates) a queue playlist, fills it with the given library tracks in order and plays it.
  on playQueue(queueName, pids)
    tell application "Music"
      if exists (user playlist queueName) then
        set q to user playlist queueName
        delete every track of q
      else
        set q to make new user playlist with properties {name:queueName}
      end if
      set lp to library playlist 1
      repeat with p in pids
        duplicate (first track of lp whose persistent ID is (contents of p)) to q
      end repeat
      play q
    end tell
  end playQueue

  on deletePlaylistNamed(n)
    tell application "Music"
      if exists (user playlist n) then delete user playlist n
    end tell
  end deletePlaylistNamed
  """#

  private let script: NSAppleScript
  init() throws {
    script = NSAppleScript(source: Self.source)!
    var err: NSDictionary?
    guard script.compileAndReturnError(&err) else { throw NSError(domain: "MusicScript", code: 1, userInfo: err as? [String: Any]) }
  }

  @discardableResult
  func call(_ handler: String, _ args: [Any] = []) throws -> NSAppleEventDescriptor {
    func fcc(_ s: String) -> FourCharCode { s.utf8.reduce(0) { $0 << 8 | FourCharCode($1) } }
    let ev = NSAppleEventDescriptor(eventClass: fcc("ascr"), eventID: fcc("psbr"), targetDescriptor: .currentProcess(), returnID: -1, transactionID: 0)
    ev.setParam(NSAppleEventDescriptor(string: handler.lowercased()), forKeyword: fcc("snam")) // handler names are case-insensitive, sent lowercased
    let list = NSAppleEventDescriptor.list()
    for (i, a) in args.enumerated() { list.insert(Self.desc(a), at: i + 1) }
    ev.setParam(list, forKeyword: fcc("----"))
    var err: NSDictionary?
    let r = script.executeAppleEvent(ev, error: &err)
    if let err { throw NSError(domain: "MusicScript", code: (err[NSAppleScript.errorNumber] as? Int) ?? -1, userInfo: [NSLocalizedDescriptionKey: err[NSAppleScript.errorMessage] ?? "?"]) }
    return r
  }
  static func desc(_ a: Any) -> NSAppleEventDescriptor {
    switch a {
    case let s as String: return NSAppleEventDescriptor(string: s)
    case let i as Int: return NSAppleEventDescriptor(int32: Int32(i))
    case let d as Double: return NSAppleEventDescriptor(double: d)
    case let b as Bool: return NSAppleEventDescriptor(boolean: b)
    case let l as [Any]: let d = NSAppleEventDescriptor.list(); for (i, x) in l.enumerated() { d.insert(desc(x), at: i + 1) }; return d
    default: return NSAppleEventDescriptor.null()
    }
  }
}

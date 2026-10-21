import AppKit
import Foundation

/// Pushed events: `player` (playerInfo notifications, our commands, 1 Hz while
/// playing), `music` (launch/quit) and `library` (database changes).
enum EventHandlers {
  private static var observers: [NSObjectProtocol] = []

  static func register(_ server: Server, _ music: MusicAE) {
    let monitor = PlayerMonitor.shared
    monitor.configure(server, music)
    let library = LibraryWatcher(server: server)
    library.start()

    // Music also posts com.apple.iTunes.playerInfo with the same payload; one is enough.
    observers.append(DistributedNotificationCenter.default().addObserver(
      forName: Notification.Name("com.apple.Music.playerInfo"), object: nil, queue: nil
    ) { _ in
      server.music.async { monitor.notificationArrived() }
    })

    let workspace = NSWorkspace.shared.notificationCenter
    func isMusic(_ n: Notification) -> Bool {
      (n.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication)?.bundleIdentifier == MusicAE.bundleID
    }
    observers.append(workspace.addObserver(forName: NSWorkspace.didLaunchApplicationNotification, object: nil, queue: nil) { n in
      guard isMusic(n) else { return }
      Permissions.shared.musicLaunching()
      server.emit("music", ["running": true])
      // Wait off the music queue until Music answers, then publish its state and
      // check the library: a snapshot taken while Music was down lacks its favorites.
      DispatchQueue.global().async {
        Permissions.shared.musicLaunched(server, music)
        guard (try? music.waitUntilAnswering(timeout: 15)) != nil else { return }
        server.music.async { monitor.refresh() }
        server.library.async { library.check(force: LibraryIndex.shared.lastSnapshotLackedMusic) }
      }
    })
    observers.append(workspace.addObserver(forName: NSWorkspace.didTerminateApplicationNotification, object: nil, queue: nil) { n in
      guard isMusic(n) else { return }
      MusicAE.noteQuit()
      server.emit("music", ["running": false])
      server.music.async { monitor.musicQuit() }
    })

    // Start the 1 Hz ticker if Music is already playing.
    if MusicAE.isAppRunning { server.music.async { monitor.refresh() } }
  }
}

import AppKit
import Foundation

// encore-bridge: see docs/design.md. Each feature file registers its methods.
let server = Server()
let music = MusicAE()

if CommandLine.arguments.contains("--selftest") { exit(SelfTest.run(server, music)) }

// Ask for Automation and Media & Apple Music now, so any prompts come at launch.
// First, so the Apple Events handlers send as they register find Automation checked.
Permissions.shared.start(server, music)

HelloHandlers.register(server, music)
LibraryHandlers.register(server, music)
EventHandlers.register(server, music)
PlayerHandlers.register(server, music)
PlaybackHandlers.register(server, music)
StoreHandlers.register(server, music)
AirPlayHandlers.register(server, music)
ArtworkHandlers.register(server, music)

server.run()
// Distributed and NSWorkspace notifications need a running main run loop.
RunLoop.main.run()

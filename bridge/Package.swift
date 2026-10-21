// swift-tools-version: 6.0
import Foundation
import PackageDescription

// Info.plist goes into the binary's __TEXT,__info_plist section: it gives the bridge
// a bundle identifier and the usage strings macOS shows in permission prompts.
let infoPlist = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("Info.plist").path

let package = Package(
  name: "encore-bridge",
  platforms: [.macOS(.v14)],
  targets: [
    .executableTarget(
      name: "encore-bridge",
      path: "Sources/EncoreBridge",
      swiftSettings: [.swiftLanguageMode(.v5)],
      linkerSettings: [
        .linkedFramework("iTunesLibrary"), .linkedFramework("AppKit"),
        .unsafeFlags(["-Xlinker", "-sectcreate", "-Xlinker", "__TEXT", "-Xlinker", "__info_plist", "-Xlinker", infoPlist]),
      ]
    )
  ]
)

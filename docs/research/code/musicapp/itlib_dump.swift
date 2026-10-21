import Foundation
import iTunesLibrary
let t0 = Date()
let lib = try ITLibrary(apiVersion: "1.0")
let items = lib.allMediaItems
print("items", items.count, "load", Date().timeIntervalSince(t0))
var kinds: [String:Int] = [:]
for it in items { kinds["\(it.mediaKind.rawValue)/cloud=\(it.isCloud)/drm=\(it.isDRMProtected)/loc=\(it.location != nil)", default: 0] += 1 }
print(kinds)
for it in items.prefix(5) {
  let pid = it.persistentID.uint64Value
  print(String(format: "%016llX", pid), it.title, "|", it.album.title ?? "-", "|", it.artist?.name ?? "-", "| track", it.trackNumber, "hasArt", it.hasArtworkAvailable, "kind", it.kind ?? "-")
}
print("playlists", lib.allPlaylists.count)
for p in lib.allPlaylists.prefix(40) { print(String(format: "%016llX", p.persistentID.uint64Value), p.name, p.items.count, p.distinguishedKind.rawValue, p.isPrimary, p.isVisible, p.kind.rawValue) }

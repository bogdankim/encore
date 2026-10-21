import Foundation
import iTunesLibrary
let lib = try ITLibrary(apiVersion: "1.0")
let hex = { (n: NSNumber) in String(format: "%016llX", n.uint64Value) }
let libPl = lib.allPlaylists.first { $0.isPrimary }!
var albums: [UInt64: [ITLibMediaItem]] = [:]
for it in libPl.items { albums[it.album.persistentID.uint64Value, default: []].append(it) }
for (_, a) in albums where a.count == 12 || a.count == 13 {
  let s = a.sorted { ($0.album.discNumber, $0.trackNumber) < ($1.album.discNumber, $1.trackNumber) }
  print(s[0].album.title ?? "-", "|", s.map { hex($0.persistentID) }.joined(separator: ","))
}

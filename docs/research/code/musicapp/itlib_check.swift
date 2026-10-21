import Foundation
import iTunesLibrary
let lib = try ITLibrary(apiVersion: "1.0")
let hex = { (n: NSNumber) in String(format: "%016llX", n.uint64Value) }
let libPl = lib.allPlaylists.first { $0.isPrimary }!
let inLib = Set(libPl.items.map { hex($0.persistentID) })
let all = lib.allMediaItems
print("all", all.count, "in Library playlist", inLib.count)
for want in ["00000000000000A6", "00000000000000A7", "00000000000000A4"] {
  if let it = all.first(where: { hex($0.persistentID) == want }) {
    let pls = lib.allPlaylists.filter { $0.items.contains { $0.persistentID == it.persistentID } }.map { $0.name }
    print(want, it.title, "| album:", it.album.title ?? "-", "| inLib:", inLib.contains(want), "| playlists:", pls, "| cloud", it.isCloud, "| playStatus", it.playStatus.rawValue, "| userDisabled", it.isUserDisabled)
  } else { print(want, "not found") }
}
// Counts of items not in library by playlist
var notInLibByPl: [String:Int] = [:]
for p in lib.allPlaylists where !p.isPrimary { for i in p.items where !inLib.contains(hex(i.persistentID)) { notInLibByPl[p.name, default: 0] += 1 } }
print(notInLibByPl)
// first album with >=12 tracks in library
var albums: [String: [ITLibMediaItem]] = [:]
for it in libPl.items { albums["\(it.album.persistentID)", default: []].append(it) }
let big = albums.values.filter { $0.count >= 10 }.sorted { $0.count > $1.count }
for a in big.prefix(5) { let s = a.sorted { ($0.album.discCount, $0.trackNumber) < ($1.album.discCount, $1.trackNumber) }; print(a.count, s[0].album.title ?? "-", "|", s[0].album.albumArtist ?? s[0].artist?.name ?? "-", "| albumPID", hex(s[0].album.persistentID), s.prefix(3).map { "\($0.trackNumber):\(hex($0.persistentID))" }) }

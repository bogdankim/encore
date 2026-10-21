import Foundation
import iTunesLibrary
let lib = try ITLibrary(apiVersion: "1.0")
let hex = { (n: NSNumber) in String(format: "%016llX", n.uint64Value) }
let keys = ["cloudType", "cloudPlaybackEndpointType", "downloadable", "storeItemID", "subscriptionAdamID", "cloudUniversalLibraryID", "playlistOnly", "expirationDate", "playlistCount", "locationType"]
for it in lib.allMediaItems where ["00000000000000A8", "00000000000000A0"].contains(hex(it.persistentID)) {
  print(hex(it.persistentID), it.title, keys.map { "\($0)=\(it.value(forKey: $0) ?? "nil")" }.joined(separator: " "))
}
// distribution of cloudType / endpoint over library playlist items
let libPl = lib.allPlaylists.first { $0.isPrimary }!
var d: [String: Int] = [:]
for it in libPl.items { d["cloudType=\(it.value(forKey: "cloudType") ?? "nil") endpoint=\(it.value(forKey: "cloudPlaybackEndpointType") ?? "nil") loc=\(it.locationType.rawValue)", default: 0] += 1 }
print(d)
var d2: [String: Int] = [:]
for it in lib.allMediaItems where !libPl.items.contains(where: { $0.persistentID == it.persistentID }) { d2["cloudType=\(it.value(forKey: "cloudType") ?? "nil") endpoint=\(it.value(forKey: "cloudPlaybackEndpointType") ?? "nil") playlistOnly=\(it.value(forKey: "playlistOnly") ?? "nil")", default: 0] += 1 }
print("non-library items:", d2)

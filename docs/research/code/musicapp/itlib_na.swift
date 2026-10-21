import Foundation
import iTunesLibrary
let lib = try ITLibrary(apiVersion: "1.0")
let hex = { (n: NSNumber) in String(format: "%016llX", n.uint64Value) }
for it in lib.allMediaItems where ["00000000000000A8", "00000000000000A0"].contains(hex(it.persistentID)) {
  print(hex(it.persistentID), it.title, "cloud", it.isCloud, "purchased", it.isPurchased, "drm", it.isDRMProtected, "userDisabled", it.isUserDisabled, "playStatus", it.playStatus.rawValue, "locationType", it.locationType.rawValue, "kind", it.kind ?? "-", "fileType", it.fileType, "size", it.fileSize)
  for k in ["cloudStatus", "isPlaylistOnly", "isUnavailable", "cloudLibraryKind", "storeTrackID", "storeID", "playlistOnly", "unavailable", "cloudMatchState"] {
    if it.responds(to: NSSelectorFromString(k)) { print("  ", k, it.value(forKey: k) ?? "nil") }
  }
}
// list all properties via Obj-C runtime
var n: UInt32 = 0
if let props = class_copyPropertyList(ITLibMediaItem.self, &n) { print((0..<Int(n)).map { String(cString: property_getName(props[$0])) }.joined(separator: " ")) }

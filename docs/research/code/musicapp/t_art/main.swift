import Foundation
import iTunesLibrary
import ImageIO
setvbuf(stdout, nil, _IONBF, 0)
let m = MusicAE()
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
func magic(_ d: Data) -> String { d.starts(with: [0xFF, 0xD8, 0xFF]) ? "JPEG" : d.starts(with: [0x89, 0x50, 0x4E, 0x47]) ? "PNG" : "other(\(d.prefix(4).map { String(format: "%02x", $0) }.joined()))" }
func dims(_ d: Data) -> String { if let r = NSBitmapImageRepFromData(d) { return r } ; return "?" }
func NSBitmapImageRepFromData(_ d: Data) -> String? { guard let src = CGImageSourceCreateWithData(d as CFData, nil), let p = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any] else { return nil }; return "\(p[kCGImagePropertyPixelWidth] ?? "?")x\(p[kCGImagePropertyPixelHeight] ?? "?")" }

// (a) now playing: raw data of artwork 1 of current track (one AE)
let art1 = m.element(fcc("cArt"), form: fcc("indx"), NSAppleEventDescriptor(int32: 1), of: m.currentTrack)
for _ in 0..<3 {
  let t = now(); let d = try m.get(m.prop(fcc("pRaw"), of: art1))!
  print(String(format: "AE raw data of artwork 1 of current track: %.0f ms, descType=%@, %d bytes, %@ %@", now() - t, fccString(d.descriptorType), d.data.count, magic(d.data), dims(d.data)))
}
do { let t = now(); let d = try m.get(m.prop(fcc("pPCT"), of: art1))!; print(String(format: "AE 'data' (picture) of artwork 1: %.0f ms, descType=%@, %d bytes %@", now() - t, fccString(d.descriptorType), d.data.count, magic(d.data))) } catch { print("data err", error) }
// library track by pid
let art2 = m.element(fcc("cArt"), form: fcc("indx"), NSAppleEventDescriptor(int32: 1), of: m.track(persistentID: "00000000000000A0"))
do { let t = now(); let d = try m.get(m.prop(fcc("pRaw"), of: art2))!; print(String(format: "AE raw data artwork of library track by pid: %.0f ms, %d bytes %@ %@", now() - t, d.data.count, magic(d.data), dims(d.data))) } catch { print("lib art err", error) }
// playlist-only track
let art3 = m.element(fcc("cArt"), form: fcc("indx"), NSAppleEventDescriptor(int32: 1), of: m.track(persistentID: "00000000000000A4", in: m.playlist(id: 21441)))
do { let t = now(); let d = try m.get(m.prop(fcc("pRaw"), of: art3))!; print(String(format: "AE raw data artwork of playlist-only track: %.0f ms, %d bytes %@ %@", now() - t, d.data.count, magic(d.data), dims(d.data))) } catch { print("pl-only art err", error) }

// (b) ITLibrary
var t = now()
let lib = try ITLibrary(apiVersion: "1.0")
print(String(format: "ITLibrary load %.0f ms", now() - t))
let items = lib.allMediaItems
var withArtFlag = 0, withArtObj = 0, withData = 0, bytes = 0
var fmts: [String: Int] = [:]
t = now()
for it in items.prefix(300) {
  if it.hasArtworkAvailable { withArtFlag += 1 }
  if let a = it.artwork { withArtObj += 1; if let d = a.imageData { withData += 1; bytes += d.count; fmts["\(a.imageDataFormat.rawValue)/\(magic(d))", default: 0] += 1 } }
}
print(String(format: "ITLib first 300 items: hasArtworkAvailable=%d artwork!=nil=%d imageData!=nil=%d avg %d bytes, %.0f ms total (%.2f ms/item) fmts %@", withArtFlag, withArtObj, withData, withData > 0 ? bytes / withData : 0, now() - t, (now() - t) / 300, "\(fmts)"))
let hex = { (n: NSNumber) in String(format: "%016llX", n.uint64Value) }
if let it = items.first(where: { hex($0.persistentID) == "00000000000000A9" }) {
  t = now(); let d = it.artwork?.imageData; print(String(format: "ITLib artwork for now-playing track: %@ %.1f ms %@", d.map { "\($0.count) bytes \(magic($0)) \(dims($0))" } ?? "nil", now() - t, it.artwork.map { "\($0.imageDataFormat.rawValue)" } ?? "-"))
}
// all items scan
t = now(); var n2 = 0
for it in items where it.artwork?.imageData != nil { n2 += 1 }
print(String(format: "ITLib all %d items with imageData: %d (%.0f ms)", items.count, n2, now() - t))

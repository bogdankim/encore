import Foundation
let m = MusicAE()
func ms(_ label: String, _ n: Int = 20, _ f: () throws -> Any?) {
  var ts: [Double] = []; var r: Any? = nil
  for _ in 0..<n { let t = DispatchTime.now().uptimeNanoseconds; do { r = try f() } catch { r = error }; ts.append(Double(DispatchTime.now().uptimeNanoseconds - t)/1e6) }
  ts.sort(); print(String(format: "%-40@ p50 %.2f ms max %.2f -> ", label as NSString, ts[n/2], ts.last!) + String(describing: r ?? "nil").prefix(160))
}
ms("snapshot (8 AEs)") { let s = try m.snapshot(); return "\(s.state) \(s.position ?? -1) \(s.name ?? "") / \(s.artist ?? "") / \(s.persistentID ?? "") / \(s.databaseID ?? 0) dur \(s.duration ?? 0)" }
ms("properties of current track (1 AE)") { let p = try m.properties(of: m.currentTrack); return "\(p.count) keys name=\(p["pnam"]?.stringValue ?? "") fav=\(p["pLov"]?.booleanValue ?? false)" }
ms("whose pID lookup in library (1 AE)") { try m.get(m.prop(fcc("pnam"), of: m.track(persistentID: "00000000000000A0")))?.stringValue }
ms("whose pID in playlist id 21441 (1 AE)") { try m.get(m.prop(fcc("pnam"), of: m.track(persistentID: "00000000000000A4", in: m.playlist(id: 21441))))?.stringValue }
ms("playlist by persistent ID name") { try m.get(m.prop(fcc("pnam"), of: m.playlist(persistentID: "00000000000000A5")))?.stringValue }
ms("whose miss (nonexistent pid)", 3) { try m.get(m.prop(fcc("pnam"), of: m.track(persistentID: "0000000000000001")))?.stringValue }
ms("shuffle/repeat/mute/vol (4 AEs)") { "\(try m.get(m.prop(fcc("pShE")))!.booleanValue) \(fccString(try m.get(m.prop(fcc("pRpt")))!.enumCodeValue)) \(try m.get(m.prop(fcc("pMut")))!.booleanValue) \(try m.get(m.prop(fcc("pVol")))!.int32Value)" }

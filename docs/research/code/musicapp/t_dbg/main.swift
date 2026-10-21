import Foundation
let m = MusicAE()
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
let all = m.every(fcc("cAPD"), of: nil)
for round in 0..<2 {
for c in ["ID  ", "pnam", "pKnd", "selc", "pAct", "pAva", "pVol", "pAud", "pMAC"] {
  let code: FourCharCode = c.utf8.reduce(0) { $0 << 8 | FourCharCode($1) }
  let t = now(); let r = try? m.get(m.prop(code, of: all)); print(round, c, String(format: "%.1f ms", now() - t), r?.numberOfItems ?? -1)
}}
let t = now(); _ = try? m.get(m.prop(fcc("pAPD"))); print(String(format: "current AirPlay devices %.1f ms", now() - t))

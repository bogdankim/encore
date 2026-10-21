import Foundation
setvbuf(stdout, nil, _IONBF, 0)
let m = MusicAE()
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
func cur() -> String { ((try? m.get(m.prop(fcc("pPIS"), of: m.currentTrack))) ?? nil)?.stringValue ?? "-" }
func waitFor(_ pred: () -> Bool, _ ms: Double = 6000) -> Double { let t = now(); while !pred() && now() - t < ms { usleep(5000) }; return now() - t }
let album = "00000000000000AA,00000000000000AB,00000000000000AC,00000000000000AD,00000000000000A9,00000000000000AE,00000000000000AF,00000000000000B0,00000000000000B1,00000000000000B2,00000000000000B3,00000000000000B4".split(separator: ",").map(String.init)
let startN = Int(CommandLine.arguments.dropFirst().first ?? "6")!
try m.setShuffle(false)
let props = NSAppleEventDescriptor.record(); props.setDescriptor(NSAppleEventDescriptor(string: "encore-test-tmp"), forKeyword: fcc("pnam"))
let t0 = now()
let pl = try m.send(fcc("core"), fcc("crel"), params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cUsP")), fcc("prdt"): props])!
for p in album { try m.send(fcc("core"), fcc("clon"), direct: m.track(persistentID: p), params: [fcc("insh"): pl]) }
let t1 = now()
try m.play(pl)
let tStart = waitFor({ cur() == album[0] })
print(String(format: "built %d-track playlist in %.0f ms; play -> track1 current after %.0f ms", album.count, t1 - t0, tStart))
// skip to N
let ts = now()
func pos() -> Double { (((try? m.get(m.prop(fcc("pPos")))) ?? nil)?.doubleValue ?? 0) }
_ = waitFor({ pos() > 0.05 })
print(String(format: "track 1 audible after %.0f ms total", now() - t1))
for i in 1..<startN {
  let tn = now(); try m.nextTrack()
  let w = waitFor({ cur() == album[i] }, 5000); let w2 = waitFor({ pos() > 0.02 && pos() < 3 }, 5000)
  print(String(format: "  next -> #%d current after %.0f ms, audible after +%.0f ms (cur=#%d)", i + 1, w, w2, (album.firstIndex(of: cur()) ?? -2) + 1)) ; _ = tn
}
print(String(format: "reached track %d in %.0f ms", startN, now() - ts))
let audio = waitFor({ (((try? m.get(m.prop(fcc("pPos")))) ?? nil)?.doubleValue ?? 0) > 0.05 })
print(String(format: "audio position > 0 after another %.0f ms", audio))
try m.previousTrack(); _ = waitFor({ cur() == album[startN - 2] }, 3000); print("previous ->", album.firstIndex(of: cur()).map { $0 + 1 } ?? -1)
try m.stop()
try m.send(fcc("core"), fcc("delo"), direct: pl)

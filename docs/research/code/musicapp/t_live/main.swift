import Foundation
setvbuf(stdout, nil, _IONBF, 0)
let m = MusicAE()
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
func cur() -> String { ((try? m.get(m.prop(fcc("pPIS"), of: m.currentTrack))) ?? nil)?.stringValue ?? "-" }
func pos() -> Double { (((try? m.get(m.prop(fcc("pPos")))) ?? nil)?.doubleValue ?? 0) }
func state() -> String { fccString(((try? m.get(m.prop(fcc("pPlS")))) ?? nil)?.enumCodeValue ?? 0) }
func waitFor(_ pred: () -> Bool, _ ms: Double = 6000) -> Double { let t = now(); while !pred() && now() - t < ms { usleep(5000) }; return now() - t }
let album = "00000000000000AA,00000000000000AB,00000000000000AC,00000000000000AD,00000000000000A9,00000000000000AE".split(separator: ",").map(String.init)
func mk(_ name: String) throws -> NSAppleEventDescriptor {
  let props = NSAppleEventDescriptor.record(); props.setDescriptor(NSAppleEventDescriptor(string: name), forKeyword: fcc("pnam"))
  return try m.send(fcc("core"), fcc("crel"), params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cUsP")), fcc("prdt"): props])!
}
func add(_ p: String, _ pl: NSAppleEventDescriptor) throws { try m.send(fcc("core"), fcc("clon"), direct: m.track(persistentID: p), params: [fcc("insh"): pl]) }
let n = Int(CommandLine.arguments[1])!  // initial tracks before play
try m.setShuffle(false)
let pl = try mk("encore-test-tmp")
for p in album.prefix(n) { try add(p, pl) }
try m.play(pl)
_ = waitFor({ pos() > 0.05 && cur() == album[0] })
for p in album.dropFirst(n) { try add(p, pl) }
print("initial \(n), appended \(album.count - n) after play started")
for i in 1..<album.count {
  try m.nextTrack()
  _ = waitFor({ cur() != album[i-1] }, 4000); _ = waitFor({ pos() > 0.02 && pos() < 3 }, 4000)
  print("  next -> #\((album.firstIndex(of: cur()) ?? -2) + 1) state \(state())")
}
// delete the playlist while it is playing
let pl2 = try mk("encore-test-tmp2"); try add(album[0], pl2); try add(album[1], pl2)
try m.send(fcc("core"), fcc("delo"), direct: pl)
usleep(800_000)
print("after deleting the playing temp playlist: state \(state()) cur #\((album.firstIndex(of: cur()) ?? -2) + 1)")
try m.play(pl2); _ = waitFor({ cur() == album[0] }, 3000)
print("playing pl2: state \(state()) cur #\((album.firstIndex(of: cur()) ?? -2) + 1)")
try m.stop(); try m.send(fcc("core"), fcc("delo"), direct: pl2)

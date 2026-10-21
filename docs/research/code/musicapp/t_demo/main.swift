// Demo/verification for MusicAE.swift. Leaves Music stopped and deletes its queue playlist.
import Foundation
setvbuf(stdout, nil, _IONBF, 0)
let m = MusicAE()
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
func timed<T>(_ label: String, _ f: () throws -> T) rethrows -> T { let t = now(); let r = try f(); print(String(format: "%-28@ %7.1f ms", label as NSString, now() - t)); return r }
let album = "00000000000000AA,00000000000000AB,00000000000000AC,00000000000000AD,00000000000000A9,00000000000000AE,00000000000000AF,00000000000000B0,00000000000000B1,00000000000000B2,00000000000000B3,00000000000000B4".split(separator: ",").map(String.init)
try m.setShuffle(false)
try timed("playQueue album from #4") { try m.playQueue(Array(album[3...]), name: "encore-test-tmp") }
Thread.sleep(forTimeInterval: 1.5)
let np = try timed("nowPlaying (3 AEs)") { try m.nowPlaying() }
print("  ", np.state, np.position ?? -1, np.track["pnam"]?.stringValue ?? "-", np.track["pPIS"]?.stringValue ?? "-", "fav", np.track["pLov"]?.booleanValue ?? false, "cloud", fccString(np.track["pClS"]?.enumCodeValue ?? 0))
try timed("next") { try m.nextTrack() }
Thread.sleep(forTimeInterval: 1.5)
print("   after next:", try m.nowPlaying().track["pnam"]?.stringValue ?? "-")
try timed("seek 30s") { try m.seek(to: 30) }
Thread.sleep(forTimeInterval: 0.3)
print("   position:", try m.nowPlaying().position ?? -1)
let art = try timed("artwork current track") { try m.artworkData() }
print("   artwork bytes:", art?.count ?? 0)
try timed("playQueue album from #1 (reuse)") { try m.playQueue(album, name: "encore-test-tmp") }
Thread.sleep(forTimeInterval: 1.5)
print("   now:", try m.nowPlaying().track["pnam"]?.stringValue ?? "-")
let devs = try timed("airPlayDevices") { try m.airPlayDevices() }
for d in devs { print("  ", d) }
let selected = devs.filter(\.selected).map(\.id)
try timed("re-select current AirPlay") { try m.selectAirPlay(ids: selected) }
print("   selected after:", try m.airPlayDevices().filter(\.selected).map(\.name))
try m.stop()
try timed("deleteQueue") { try m.deleteQueue() }

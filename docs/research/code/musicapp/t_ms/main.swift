import Foundation
setvbuf(stdout, nil, _IONBF, 0)
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
let ms = try MusicScript()
var t = now(); try ms.call("playLibraryTrack", ["00000000000000A9"]); print(String(format: "playLibraryTrack: %.0f ms", now() - t))
Thread.sleep(forTimeInterval: 1.5)
for _ in 0..<3 { t = now(); let r = try ms.call("nowPlaying"); print(String(format: "nowPlaying: %.0f ms -> %@ / %@ / %@ pos %.3f", now() - t, r.atIndex(3)?.stringValue ?? "-", r.atIndex(7)?.stringValue ?? "-", r.atIndex(1)?.stringValue ?? "-", r.atIndex(2)?.doubleValue ?? -1)) }
let album = "00000000000000AA,00000000000000AB,00000000000000AC,00000000000000AD,00000000000000A9,00000000000000AE,00000000000000AF,00000000000000B0,00000000000000B1,00000000000000B2,00000000000000B3,00000000000000B4".split(separator: ",").map(String.init)
t = now(); try ms.call("playQueue", ["encore-test-tmp", Array(album[4...])]); print(String(format: "playQueue (8 tracks, creates playlist): %.0f ms", now() - t))
Thread.sleep(forTimeInterval: 1.5)
t = now(); try ms.call("playQueue", ["encore-test-tmp", Array(album[2...])]); print(String(format: "playQueue (10 tracks, reuses playlist): %.0f ms", now() - t))
Thread.sleep(forTimeInterval: 1.5)
print(try ms.call("nowPlaying").atIndex(3)?.stringValue ?? "-")
do { try ms.call("playLibraryTrack", ["0000000000000001"]) } catch { print("error path:", error) }
t = now(); try ms.call("playPlaylist", ["00000000000000A5"]); print(String(format: "playPlaylist by pid: %.0f ms", now() - t))
Thread.sleep(forTimeInterval: 1.5)
print(try ms.call("nowPlaying").atIndex(3)?.stringValue ?? "-")
try ms.call("deletePlaylistNamed", ["encore-test-tmp"])

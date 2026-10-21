import Foundation
let m = MusicAE()
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
func playAndWait(_ label: String, _ spec: NSAppleEventDescriptor, want: String? = nil) {
  let t0 = now()
  do { try m.play(spec) } catch { print(label, "play error:", error); return }
  let t1 = now()
  var pidAt = -1.0, audioAt = -1.0, pid = ""
  while now() - t0 < 8000 {
    let p = (try? m.get(m.prop(fcc("pPIS"), of: m.currentTrack)))??.stringValue ?? ""
    if pidAt < 0, !p.isEmpty, want == nil || p == want { pidAt = now() - t0; pid = p }
    if pidAt >= 0, let pos = try? m.get(m.prop(fcc("pPos"))), pos.descriptorType != fcc("msng"), pos.doubleValue > 0.05 { audioAt = now() - t0; break }
  }
  let name = (try? m.get(m.prop(fcc("pnam"), of: m.currentTrack)))??.stringValue ?? "?"
  let cpl = (try? m.get(m.prop(fcc("pnam"), of: m.prop(fcc("pPla")))))??.stringValue ?? "?"
  print(String(format: "%@: play() returned in %.0f ms, currentTrack pid %@ at %.0f ms, position>0.05 at %.0f ms | %@ | current playlist: %@", label, t1 - t0, pid, pidAt, audioAt, name, cpl))
}
let args = CommandLine.arguments
if args.count >= 3 && args[1] == "lib" { playAndWait("library track \(args[2])", m.track(persistentID: args[2]), want: args[2]) }
if args.count >= 4 && args[1] == "pl" { playAndWait("track \(args[3]) in playlist id \(args[2])", m.track(persistentID: args[3], in: m.playlist(id: Int32(args[2])!)), want: args[3]) }
if args.count >= 4 && args[1] == "plidx" { playAndWait("track index \(args[3]) of playlist id \(args[2])", m.track(index: Int32(args[3])!, of: m.playlist(id: Int32(args[2])!))) }
if args.count >= 3 && args[1] == "playlist" { playAndWait("playlist id \(args[2])", m.playlist(id: Int32(args[2])!)) }

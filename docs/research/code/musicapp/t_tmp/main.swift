import Foundation
setvbuf(stdout, nil, _IONBF, 0)
let m = MusicAE()
func now() -> Double { Double(DispatchTime.now().uptimeNanoseconds) / 1e6 }
let tmpName = "encore-test-tmp"

/// make new user playlist with properties {name: tmpName}; returns its specifier (by unique id)
func makeTempPlaylist() throws -> NSAppleEventDescriptor {
  let props = NSAppleEventDescriptor.record()
  props.setDescriptor(NSAppleEventDescriptor(string: tmpName), forKeyword: fcc("pnam"))
  let r = try m.send(fcc("core"), fcc("crel"), params: [fcc("kocl"): NSAppleEventDescriptor(typeCode: fcc("cUsP")), fcc("prdt"): props])!
  return r // object specifier returned by Music: user playlist id NNN
}
func deletePlaylist(_ spec: NSAppleEventDescriptor) throws { try m.send(fcc("core"), fcc("delo"), direct: spec) }

func pidsOf(_ pl: NSAppleEventDescriptor) throws -> [String] {
  // persistent ID of every track of pl
  guard let l = try m.get(m.prop(fcc("pPIS"), of: m.every(fcc("cTrk"), of: pl))) else { return [] }
  return (0..<l.numberOfItems).map { l.atIndex($0 + 1)!.stringValue! }
}

func run(_ pids: [String], mode: String) throws {
  // clean up a leftover
  let pl = try makeTempPlaylist()
  let t0 = now()
  if mode == "noreply" {
    for p in pids { try m.sendNoReply(fcc("core"), fcc("clon"), direct: m.track(persistentID: p), params: [fcc("insh"): pl]) }
    _ = try m.get(m.prop(fcc("pnam"), of: pl)) // barrier
  } else if mode == "or" {
    try m.send(fcc("core"), fcc("clon"), direct: m.tracks(persistentIDs: pids), params: [fcc("insh"): pl], timeout: 60)
  } else {
    for p in pids { do { try m.send(fcc("core"), fcc("clon"), direct: m.track(persistentID: p), params: [fcc("insh"): pl], timeout: 10) } catch { print("dup failed", p, error, pl); throw error } }
  }
  let t1 = now()
  let got = try pidsOf(pl)
  print(String(format: "%@ n=%d: duplicate %.0f ms (%.1f ms/track); playlist has %d tracks; order preserved: %@", mode, pids.count, t1 - t0, (t1 - t0) / Double(pids.count), got.count, got == pids ? "yes" : "NO"))
  if CommandLine.arguments.contains("play") {
    let tp = now(); try m.play(pl); print(String(format: "play temp playlist returned in %.0f ms", now() - tp))
    Thread.sleep(forTimeInterval: 2.5)
    for _ in 0..<3 {
      let before = try m.get(m.prop(fcc("pPIS"), of: m.currentTrack))?.stringValue ?? "-"
      let tn = now(); try m.nextTrack()
      var after = before
      while after == before && now() - tn < 5000 { after = (try? m.get(m.prop(fcc("pPIS"), of: m.currentTrack)))??.stringValue ?? "-" }
      print(String(format: "  next: %@ -> %@ (index in list %d) in %.0f ms", before, after, (pids.firstIndex(of: after) ?? -2) + 1, now() - tn))
    }
    try m.stop()
  }
  if !CommandLine.arguments.contains("keep") { try deletePlaylist(pl) }
}
let album = "00000000000000AA,00000000000000AB,00000000000000AC,00000000000000AD,00000000000000A9,00000000000000AE,00000000000000AF,00000000000000B0,00000000000000B1,00000000000000B2,00000000000000B3,00000000000000B4".split(separator: ",").map(String.init)
let big = try String(contentsOfFile: "pids200.txt", encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines).split(separator: ",").map(String.init)
let which = CommandLine.arguments.dropFirst().first ?? "all"
if which == "all" || which == "a12" { try run(album, mode: "loop"); try run(album, mode: "or") }
if which == "all" || which == "a200" { try run(big, mode: "loop"); try run(big, mode: "or") }
if which == "playalbum" { try run(album, mode: "or") }
if which == "rev" { try run(Array(album.reversed()), mode: "or"); try run(Array(big.reversed()), mode: "or"); try run(Array(big.shuffled()), mode: "loop") }

if which == "nr" { try run(album, mode: "noreply"); try run(Array(big.shuffled()), mode: "noreply"); try run(Array(big.shuffled()), mode: "loop") }

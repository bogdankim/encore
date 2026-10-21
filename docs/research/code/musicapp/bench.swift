// Latency benchmark: osascript CLI vs NSAppleScript vs OSAKit vs ScriptingBridge vs raw Apple Events
// build: swiftc -O bench.swift -o bench
import Foundation
import OSAKit
import ScriptingBridge

let N = 200
func bench(_ label: String, n: Int = N, _ body: () throws -> Any?) {
  var times: [Double] = []
  var last: Any? = nil
  for _ in 0..<n {
    let t = DispatchTime.now().uptimeNanoseconds
    do { last = try body() } catch { last = "ERR \(error)" }
    times.append(Double(DispatchTime.now().uptimeNanoseconds - t) / 1e6)
  }
  times.sort()
  let mean = times.reduce(0, +) / Double(times.count)
  print(String(format: "%-44@ n=%3d mean %.3f ms  p50 %.3f  p95 %.3f  max %.3f  -> %@", label as NSString, n, mean, times[n/2], times[Int(Double(n)*0.95)], times.last!, String(describing: last ?? "nil") as NSString))
}

let src = "tell application \"Music\" to get {player position, player state}"

// 1. osascript CLI
bench("osascript CLI", n: 20) {
  let p = Process(); p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript"); p.arguments = ["-e", src]
  let pipe = Pipe(); p.standardOutput = pipe; try p.run(); p.waitUntilExit()
  return String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
}

// 2. NSAppleScript compiled once
let nsas = NSAppleScript(source: src)!
var err: NSDictionary?
nsas.compileAndReturnError(&err)
bench("NSAppleScript (precompiled)") {
  var e: NSDictionary?
  let d = nsas.executeAndReturnError(&e)
  return "\(d.atIndex(1)!.doubleValue) \(d.atIndex(2)!.typeCodeValue == 0x6B505353 ? "stopped" : String(format: "%08X", d.atIndex(2)!.enumCodeValue))"
}
// 2b. NSAppleScript compile every call
bench("NSAppleScript (compile each call)", n: 50) {
  var e: NSDictionary?
  return NSAppleScript(source: src)!.executeAndReturnError(&e).atIndex(1)?.doubleValue
}

// 3. OSAKit
let osa = OSAScript(source: src, language: OSALanguage(forName: "AppleScript"))
var oerr: NSDictionary?
osa.compileAndReturnError(&oerr)
bench("OSAKit OSAScript (precompiled)") {
  var e: NSDictionary?
  return osa.executeAndReturnError(&e)?.atIndex(1)?.doubleValue
}

// 4. ScriptingBridge (dynamic KVC, no generated header)
let sb = SBApplication(bundleIdentifier: "com.apple.Music")!
bench("ScriptingBridge KVC playerPosition") {
  return sb.value(forKey: "playerPosition")
}
bench("ScriptingBridge KVC position+state (2 AEs)") {
  let a = sb.value(forKey: "playerPosition"); let b = sb.value(forKey: "playerState")
  return "\(a ?? "nil") \(b ?? "nil")"
}

// 5. Raw Apple Event: core/getd on a property specifier, sent with NSAppleEventDescriptor.sendEvent
let target = NSAppleEventDescriptor(bundleIdentifier: "com.apple.Music")
func propSpec(_ code: FourCharCode) -> NSAppleEventDescriptor {
  let spec = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("obj "))!
  spec.setDescriptor(NSAppleEventDescriptor(typeCode: fcc("prop")), forKeyword: fcc("want"))
  spec.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("prop")), forKeyword: fcc("form"))
  spec.setDescriptor(NSAppleEventDescriptor(typeCode: code), forKeyword: fcc("seld"))
  spec.setDescriptor(NSAppleEventDescriptor.null(), forKeyword: fcc("from"))
  return spec
}
func getProp(_ code: FourCharCode) throws -> NSAppleEventDescriptor {
  let ev = NSAppleEventDescriptor(eventClass: fcc("core"), eventID: fcc("getd"), targetDescriptor: target, returnID: -1, transactionID: 0)
  ev.setParam(propSpec(code), forKeyword: fcc("----"))
  let reply = try ev.sendEvent(options: [.waitForReply], timeout: 2.0)
  return reply.paramDescriptor(forKeyword: fcc("----"))!
}
func fcc(_ s: String) -> FourCharCode { s.utf8.reduce(0) { $0 << 8 | FourCharCode($1) } }
bench("Raw AE sendEvent pPos") { try getProp(fcc("pPos")).doubleValue }
bench("Raw AE sendEvent pPos + pPlS (2 AEs)") {
  let p = try getProp(fcc("pPos")).doubleValue; let s = try getProp(fcc("pPlS")).enumCodeValue
  return "\(p) \(String(format: "%08X", s))"
}
// list of property specifiers in one event: get {pPos, pPlS} -> pass list of specifiers
bench("Raw AE single getd with list of 2 specs") {
  let ev = NSAppleEventDescriptor(eventClass: fcc("core"), eventID: fcc("getd"), targetDescriptor: target, returnID: -1, transactionID: 0)
  let list = NSAppleEventDescriptor.list(); list.insert(propSpec(fcc("pPos")), at: 1); list.insert(propSpec(fcc("pPlS")), at: 2)
  ev.setParam(list, forKeyword: fcc("----"))
  let r = try ev.sendEvent(options: [.waitForReply], timeout: 2.0).paramDescriptor(forKeyword: fcc("----"))
  return r.map { "\($0.numberOfItems) items: \($0.atIndex(1)?.doubleValue ?? -1)" }
}

// Threading test: NSAppleScript on a background thread
let g = DispatchGroup(); g.enter()
Thread.detachNewThread {
  let s = NSAppleScript(source: src)!
  var e: NSDictionary?
  s.compileAndReturnError(&e)
  bench("NSAppleScript on background Thread", n: 50) { var e: NSDictionary?; return s.executeAndReturnError(&e).atIndex(1)?.doubleValue }
  bench("Raw AE on background Thread", n: 50) { try getProp(fcc("pPos")).doubleValue }
  g.leave()
}
g.wait()

import Foundation
func fcc(_ s: String) -> FourCharCode { s.utf8.reduce(0) { $0 << 8 | FourCharCode($1) } }
let target = NSAppleEventDescriptor(bundleIdentifier: "com.apple.Music")
func appProp(_ code: String) -> NSAppleEventDescriptor {
  let spec = NSAppleEventDescriptor.record().coerce(toDescriptorType: fcc("obj "))!
  spec.setDescriptor(NSAppleEventDescriptor(typeCode: fcc("prop")), forKeyword: fcc("want"))
  spec.setDescriptor(NSAppleEventDescriptor(enumCode: fcc("prop")), forKeyword: fcc("form"))
  spec.setDescriptor(NSAppleEventDescriptor(typeCode: fcc(code)), forKeyword: fcc("seld"))
  spec.setDescriptor(NSAppleEventDescriptor.null(), forKeyword: fcc("from"))
  return spec
}
func get(_ spec: NSAppleEventDescriptor) throws -> NSAppleEventDescriptor? {
  let ev = NSAppleEventDescriptor(eventClass: fcc("core"), eventID: fcc("getd"), targetDescriptor: target, returnID: -1, transactionID: 0)
  ev.setParam(spec, forKeyword: fcc("----"))
  return try ev.sendEvent(options: [.waitForReply], timeout: 2).paramDescriptor(forKeyword: fcc("----"))
}
var prev = -1.0; var changes: [(Double, Double)] = []
let start = Date()
while Date().timeIntervalSince(start) < 2 {
  let p = try get(appProp("pPos"))!.doubleValue
  let t = Date().timeIntervalSince(start)
  if p != prev { changes.append((t, p)); prev = p }
}
print("distinct values in 2s:", changes.count)
for (i, c) in changes.prefix(12).enumerated() { print(String(format: "t=%.4f pos=%.6f", c.0, c.1), i > 0 ? String(format: "dpos=%.4f dt=%.4f", c.1 - changes[i-1].1, c.0 - changes[i-1].0) : "") }
// descriptor type of the raw reply
print("type:", String(describing: try get(appProp("pPos"))!.descriptorType), "doubles", fcc("doub"), "sing", fcc("sing"))
// single event 'properties' of app
let t0 = Date(); let all = try get(appProp("pALL"))!; print("pALL ms", Date().timeIntervalSince(t0)*1000, "items", all.numberOfItems)
for i in 1...all.numberOfItems { let k = all.keywordForDescriptor(at: i); let d = all.atIndex(i)!; print(" ", String(bytes: withUnsafeBytes(of: k.bigEndian, Array.init), encoding: .ascii)!, d.stringValue ?? String(d.descriptorType)) }

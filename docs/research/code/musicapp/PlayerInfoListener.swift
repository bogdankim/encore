// PlayerInfoListener.swift: prints every com.apple.Music.playerInfo distributed notification as one JSON line.
// build: swiftc -O PlayerInfoListener.swift -o playerinfo-listener
import Foundation
setvbuf(stdout, nil, _IOLBF, 0)
let center = DistributedNotificationCenter.default()
let names = ["com.apple.Music.playerInfo", "com.apple.iTunes.playerInfo", "com.apple.Music.sourceSaved", "com.apple.Music.libraryChanged"]
let fmt = ISO8601DateFormatter(); fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
func jsonable(_ v: Any) -> Any {
  switch v {
  case let d as [AnyHashable: Any]: return Dictionary(uniqueKeysWithValues: d.map { ("\($0.key)", jsonable($0.value)) })
  case let a as [Any]: return a.map(jsonable)
  case let n as NSNumber: return n
  case let s as String: return s
  case let d as Date: return fmt.string(from: d)
  case let d as Data: return "<\(d.count) bytes>"
  default: return "\(type(of: v)): \(v)"
  }
}
var observers: [NSObjectProtocol] = []
for name in names {
  observers.append(center.addObserver(forName: Notification.Name(name), object: nil, queue: .main) { n in
    var out: [String: Any] = ["t": fmt.string(from: Date()), "name": n.name.rawValue, "object": n.object.map { "\($0)" } ?? NSNull()]
    out["userInfo"] = jsonable(n.userInfo ?? [:])
    let data = try! JSONSerialization.data(withJSONObject: out, options: [.sortedKeys])
    print(String(data: data, encoding: .utf8)!)
  })
}
// suspensionBehavior: deliverImmediately is the default for addObserver(forName:) only when the app is active; a CLI never suspends.
print("{\"listening\": \(names)}".replacingOccurrences(of: "[", with: "[\"").replacingOccurrences(of: "]", with: "\"]").replacingOccurrences(of: ", ", with: "\", \""))
RunLoop.main.run()

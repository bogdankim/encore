import Foundation

/// JSON-lines server on stdin/stdout. See docs/design.md, "Bridge protocol".
/// Handlers run on the queue they were registered with; replies and events are
/// serialized through `out`.
final class Server: @unchecked Sendable {
  typealias Params = [String: Any]
  typealias Handler = (Params) throws -> Any

  struct BridgeError: Error {
    let code: Int
    let message: String
    static func badRequest(_ m: String) -> BridgeError { BridgeError(code: 1, message: m) }
    static func internalError(_ m: String) -> BridgeError { BridgeError(code: 3, message: m) }
    static func expired(_ m: String) -> BridgeError { BridgeError(code: 4, message: m) }
  }

  /// Serial queue for everything that sends Apple Events to Music.app.
  let music = DispatchQueue(label: "encore.music")
  /// Queue for library snapshots and ITLibrary work, so it never delays player commands.
  let library = DispatchQueue(label: "encore.library")
  /// Serial queue for artwork, so art never waits behind a snapshot.
  let artwork = DispatchQueue(label: "encore.artwork")

  private let out = DispatchQueue(label: "encore.out")
  private var handlers: [String: (DispatchQueue, Handler)] = [:]

  func register(_ method: String, on queue: DispatchQueue, _ handler: @escaping Handler) {
    handlers[method] = (queue, handler)
  }

  /// Push an event to encore.
  func emit(_ event: String, _ data: Any) {
    write(["event": event, "data": data])
  }

  func log(_ s: String) {
    FileHandle.standardError.write(("[bridge] " + s + "\n").data(using: .utf8)!)
  }

  private func write(_ obj: [String: Any]) {
    out.async {
      guard let data = try? JSONSerialization.data(withJSONObject: obj, options: [.withoutEscapingSlashes]) else {
        self.log("unserializable output for \(obj.keys)")
        return
      }
      FileHandle.standardOutput.write(data)
      FileHandle.standardOutput.write(Data([0x0A]))
    }
  }

  private func handle(line: Data) {
    guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
          let id = obj["id"] as? Int, let method = obj["method"] as? String else {
      log("bad request line")
      return
    }
    let params = obj["params"] as? Params ?? [:]
    // Unix ms after which encore no longer waits for the reply; 0 = none.
    let deadline = (obj["deadline"] as? NSNumber)?.int64Value ?? 0
    guard let (queue, handler) = handlers[method] else {
      write(["id": id, "error": ["code": 2, "message": "unknown method \(method)"]])
      return
    }
    let received = DispatchTime.now()
    queue.async {
      let waited = PlaybackHandlers.elapsedMs(since: received)
      if waited > 250 { self.log(String(format: "%@ waited %.0f ms in the queue", method, waited)) }
      // Skip work encore gave up on while it sat in the queue, so a burst
      // of presses doesn't all fire once Music answers again.
      if deadline > 0, Int64(Date().timeIntervalSince1970 * 1000) > deadline {
        self.log("\(method) expired in the queue; skipped")
        let e = BridgeError.expired("\(method) expired before it ran")
        self.write(["id": id, "error": ["code": e.code, "message": e.message]])
        return
      }
      do {
        let result = try handler(params)
        self.write(["id": id, "result": result])
      } catch let e as BridgeError {
        self.write(["id": id, "error": ["code": e.code, "message": e.message]])
      } catch let e as MusicAEError {
        self.write(["id": id, "error": ["code": e.code, "message": e.message]])
      } catch {
        self.write(["id": id, "error": ["code": 3, "message": "\(error)"]])
      }
    }
  }

  /// Reads stdin until EOF on a background thread; exits the process at EOF.
  func run() {
    Thread.detachNewThread {
      var buffer = Data()
      let stdin = FileHandle.standardInput
      while true {
        let chunk = stdin.availableData
        if chunk.isEmpty { exit(0) }
        buffer.append(chunk)
        while let nl = buffer.firstIndex(of: 0x0A) {
          let line = buffer[buffer.startIndex..<nl]
          buffer.removeSubrange(buffer.startIndex...nl)
          if !line.isEmpty { self.handle(line: Data(line)) }
        }
      }
    }
  }
}

extension Dictionary where Key == String, Value == Any {
  func string(_ k: String) throws -> String {
    guard let v = self[k] as? String else { throw Server.BridgeError.badRequest("missing string param \(k)") }
    return v
  }
  func int(_ k: String) throws -> Int {
    guard let v = self[k] as? NSNumber else { throw Server.BridgeError.badRequest("missing int param \(k)") }
    return v.intValue
  }
  func double(_ k: String) throws -> Double {
    guard let v = self[k] as? NSNumber else { throw Server.BridgeError.badRequest("missing number param \(k)") }
    return v.doubleValue
  }
  func bool(_ k: String) throws -> Bool {
    guard let v = self[k] as? Bool else { throw Server.BridgeError.badRequest("missing bool param \(k)") }
    return v
  }
}

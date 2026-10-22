import Foundation
import Testing
@testable import encore_bridge

@Test func `expired written values are dropped when the next one is written`() async throws {
  let monitor = PlayerMonitor()
  monitor.expectFavorite("AAAA", true) // two keys: Music keeps favorite and dislike exclusive
  monitor.expectFavorite("BBBB", true)
  #expect(monitor.pendingExpectations == 4)

  monitor.expect("volume", 50, window: .milliseconds(20))
  try await Task.sleep(for: .milliseconds(60))
  monitor.expect("shuffle", true) // sweeps "volume" with the others still inside their window
  #expect(monitor.pendingExpectations == 5)

  monitor.expect("repeat", "all", window: .milliseconds(1))
  try await Task.sleep(for: .milliseconds(20))
  monitor.expect("state", "paused", window: .milliseconds(1))
  try await Task.sleep(for: .milliseconds(20))
  monitor.expect("shuffle", false)
  #expect(monitor.pendingExpectations == 5) // fav/dis x2 and shuffle; repeat and state are gone
}

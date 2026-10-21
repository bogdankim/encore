import Foundation
let m = MusicAE()
for threads in [1, 2, 4, 8] {
  let per = 40 / threads
  let t0 = Date(); let g = DispatchGroup()
  for _ in 0..<threads { g.enter(); Thread.detachNewThread { for _ in 0..<per { _ = try? m.get(m.prop(fcc("pPos"))) }; g.leave() } }
  g.wait()
  let dt = Date().timeIntervalSince(t0)
  print(String(format: "threads=%d total 40 AEs in %.1f ms => %.2f ms/AE", threads, dt*1000, dt*1000/40))
}

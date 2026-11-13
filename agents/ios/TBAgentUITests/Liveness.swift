// How a connection's requests reach the ops (Server.swift), and what the agent answers at
// once instead of in turn (issue #48).
//
// The ops queue runs one request at a time, and some take long: a launch waits up to a
// minute for the app, a tree read waits for a slow app. tb reconnecting meanwhile (its
// connection dropped, or a new run) asks the runner who it is: the hello, then "info"
// without the keyboard check, within a few seconds. Served behind the op in flight, a busy
// runner did not answer them, and tb took it for a dead one, stopped it and started another.
// They are answered at once now, from the connection's own thread, with only the state kept
// safe for any thread (Vitals, the event counters), and say which op runs and for how long.
//
// An op that never returns would leave a runner that answers the probe and nothing else. Once
// the op in flight has run past its limit (Vitals.limitMs) the runner counts as stuck: the
// hello and the probe wait in turn behind that op again, so tb hears nothing and replaces the
// runner, as it does a runner that does not answer at all.
import Foundation

final class Dispatcher {
    private let queue: DispatchQueue
    private let vitals: Vitals

    init(queue: DispatchQueue, vitals: Vitals) {
        self.queue = queue
        self.vitals = vitals
    }

    /// Whether a request is answered at once rather than in turn on the ops queue: the
    /// liveness probe, which reads nothing the ops change mid-run, unless the op in flight has
    /// run past its limit (stuck).
    func answersAtOnce(_ r: Request) -> Bool {
        if vitals.stuck { return false }
        switch r.op {
        case "ping": return true
        case "info": return !r.bool("keyboard", true) // the keyboard check needs the main thread
        default: return false
        }
    }

    /// Serves one connection: greet sends the hello at once (in turn on the ops queue when the
    /// op in flight is stuck), before anything else on it; read hands over each request line
    /// until the connection closes. The probe's requests go to now, on this thread; the others
    /// to queued, in turn on the ops queue.
    func serve(greet: @escaping () -> Void, read: ((Data) -> Void) -> Void,
               now: @escaping (Request) -> Void, queued: @escaping (Request) -> Void) {
        if vitals.stuck {
            NSLog("tb: \(vitals.state.op) has run for \(vitals.state.ms) ms: the hello waits for it")
            queue.async { greet() }
        } else {
            greet()
        }
        read { line in
            guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else {
                NSLog("tb: unreadable request: \(String(decoding: line, as: UTF8.self))")
                return
            }
            let req = Request(raw: obj)
            if self.answersAtOnce(req) {
                now(req)
            } else {
                self.queue.async { queued(req) }
            }
        }
    }
}

/// Calls the ops gave up on that still run on the main thread (Bounded.run, issue #130): an
/// XCTest call waiting on an app that does not answer. The op answered and the ops queue is idle,
/// but the main thread stays held, and every later call there waits behind it. Vitals reports
/// the oldest as the runner's busy op, so one held past its op's limit makes the runner stuck
/// and tb replaces it (issue #48) instead of reusing a runner that fails every op.
final class HeldMain {
    static let shared = HeldMain()

    struct Hold: Equatable {
        let op: String
        let since: UInt64
        let limitMs: Int
    }

    private let lock = NSLock()
    private var holds: [Int: Hold] = [:]
    private var next = 0
    private var current: (op: String, limitMs: Int) = ("", Vitals.stuckAfterMs)
    let clock: () -> UInt64

    init(clock: @escaping () -> UInt64 = { DispatchTime.now().uptimeNanoseconds }) { self.clock = clock }

    /// The op in flight, whose calls given up on are counted as its.
    func opBegins(_ op: String, limitMs: Int) { lock.lock(); current = (op, limitMs); lock.unlock() }

    /// A call that started at since was given up on and still runs: its id for released.
    func held(since: UInt64) -> Int {
        lock.lock(); defer { lock.unlock() }
        next += 1
        holds[next] = Hold(op: current.op.isEmpty ? "a call" : current.op, since: since, limitMs: current.limitMs)
        return next
    }

    /// The call given up on ended.
    func released(_ id: Int) { lock.lock(); holds[id] = nil; lock.unlock() }

    /// The oldest call given up on that still runs.
    var oldest: Hold? { lock.lock(); defer { lock.unlock() }; return holds.values.min { $0.since < $1.since } }
}

/// What the hello and the liveness probe report that the ops change: written on the ops
/// queue, read from any thread.
final class Vitals {
    /// An op runs at most this long before the runner counts as stuck: longer than a launch
    /// wait (60 s) and the accessibility timeouts. An op given its own timeout gets that, plus
    /// stuckMarginMs.
    static let stuckAfterMs = 90_000
    static let stuckMarginMs = 30_000

    /// The op in flight ("" when none), how long it has run, and when it counts as stuck.
    struct Busy: Equatable {
        var op = ""
        var ms = 0
        var limitMs = 0
    }

    private let lock = NSLock()
    private let clock: () -> UInt64 // uptime, ns
    private var _w = 0, _h = 0
    private var _keyboard = false
    private var _treeMode = ""
    private var _quietMs = 0
    private var _busy = ""
    private var _since: UInt64 = 0
    private var _limitMs = 0

    /// Calls given up on that still hold the main thread (issue #130).
    let held: HeldMain

    init(clock: @escaping () -> UInt64 = { DispatchTime.now().uptimeNanoseconds }, held: HeldMain = .shared) {
        self.clock = clock
        self.held = held
    }

    /// The limit for request r: stuckAfterMs, or its own "timeout" or the host's reply limit
    /// ("replyMs", issues #46, #130) plus stuckMarginMs if longer.
    static func limitMs(_ r: Request) -> Int {
        max(stuckAfterMs, max(r.int("timeout", 0), r.int("replyMs", 0)) + stuckMarginMs)
    }

    /// The screen (the app's frame at the last tree read), whether a keyboard was up then, the
    /// tree mode and the quiet window.
    func set(w: Int, h: Int, keyboard: Bool, treeMode: String, quietMs: Int) {
        lock.lock(); defer { lock.unlock() }
        _w = w; _h = h; _keyboard = keyboard; _treeMode = treeMode; _quietMs = quietMs
    }

    var size: (w: Int, h: Int) { lock.lock(); defer { lock.unlock() }; return (_w, _h) }
    var keyboard: Bool { lock.lock(); defer { lock.unlock() }; return _keyboard }
    var treeMode: String { lock.lock(); defer { lock.unlock() }; return _treeMode }
    var quietMs: Int { lock.lock(); defer { lock.unlock() }; return _quietMs }

    /// The ops queue starts op, which counts as stuck after limitMs.
    func begin(_ op: String, limitMs: Int) {
        let now = clock()
        held.opBegins(op, limitMs: limitMs)
        lock.lock(); defer { lock.unlock() }
        _busy = op; _since = now; _limitMs = limitMs
    }

    /// The op in flight ended.
    func end() {
        lock.lock(); defer { lock.unlock() }
        _busy = ""; _since = 0; _limitMs = 0
    }

    /// The op in flight, or a call given up on that still holds the main thread (issue #130),
    /// whichever is nearer its limit: a runner whose main thread is held past the limit of the op
    /// that left it counts as stuck, even with the ops queue idle.
    var state: Busy {
        let now = clock()
        var b = Busy()
        lock.lock()
        if !_busy.isEmpty { b = Busy(op: _busy, ms: Int((now &- _since) / 1_000_000), limitMs: _limitMs) }
        lock.unlock()
        if let h = held.oldest {
            let at = held.clock()
            let hb = Busy(op: "\(h.op) (given up on, still holding the main thread)",
                          ms: Int((at > h.since ? at - h.since : 0) / 1_000_000), limitMs: h.limitMs)
            if b.op.isEmpty || hb.ms - hb.limitMs > b.ms - b.limitMs { b = hb }
        }
        return b
    }

    /// Whether the op in flight, or a call given up on that still holds the main thread, has run
    /// past its limit.
    var stuck: Bool {
        let b = state
        return !b.op.isEmpty && b.ms > b.limitMs
    }
}

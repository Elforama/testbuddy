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

    init(clock: @escaping () -> UInt64 = { DispatchTime.now().uptimeNanoseconds }) { self.clock = clock }

    /// The limit for request r: stuckAfterMs, or its own "timeout" plus stuckMarginMs if longer.
    static func limitMs(_ r: Request) -> Int {
        max(stuckAfterMs, r.int("timeout", 0) + stuckMarginMs)
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
        lock.lock(); defer { lock.unlock() }
        _busy = op; _since = now; _limitMs = limitMs
    }

    /// The op in flight ended.
    func end() {
        lock.lock(); defer { lock.unlock() }
        _busy = ""; _since = 0; _limitMs = 0
    }

    var state: Busy {
        let now = clock()
        lock.lock(); defer { lock.unlock() }
        if _busy.isEmpty { return Busy() }
        return Busy(op: _busy, ms: Int((now &- _since) / 1_000_000), limitMs: _limitMs)
    }

    /// Whether the op in flight has run past its limit.
    var stuck: Bool {
        let b = state
        return !b.op.isEmpty && b.ms > b.limitMs
    }
}

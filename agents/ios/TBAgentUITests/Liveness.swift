import Foundation

final class Dispatcher {
    private let queue: DispatchQueue
    private let vitals: Vitals

    init(queue: DispatchQueue, vitals: Vitals) {
        self.queue = queue
        self.vitals = vitals
    }

    func answersAtOnce(_ r: Request) -> Bool {
        if vitals.stuck { return false }
        switch r.op {
        case "ping": return true
        case "info": return !r.bool("keyboard", true)
        default: return false
        }
    }

    func serve(greet: @escaping () -> Void, read: ((Data) -> Void) -> Void,
               seen: @escaping (Request) -> Void = { _ in },
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
            seen(req)
            if self.answersAtOnce(req) {
                now(req)
            } else {
                self.queue.async { queued(req) }
            }
        }
    }
}

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

    func opBegins(_ op: String, limitMs: Int) { lock.lock(); current = (op, limitMs); lock.unlock() }

    func held(since: UInt64) -> Int {
        lock.lock(); defer { lock.unlock() }
        next += 1
        holds[next] = Hold(op: current.op.isEmpty ? "a call" : current.op, since: since, limitMs: current.limitMs)
        return next
    }

    func released(_ id: Int) { lock.lock(); holds[id] = nil; lock.unlock() }

    var oldest: Hold? { lock.lock(); defer { lock.unlock() }; return holds.values.min { $0.since < $1.since } }
}

final class Vitals {
    static let stuckAfterMs = 90_000
    static let stuckMarginMs = 30_000

    struct Busy: Equatable {
        var op = ""
        var ms = 0
        var limitMs = 0
    }

    private let lock = NSLock()
    private let clock: () -> UInt64
    private var _w = 0, _h = 0
    private var _keyboard = false
    private var _treeMode = ""
    private var _quietMs = 0
    private var _busy = ""
    private var _since: UInt64 = 0
    private var _limitMs = 0

    let held: HeldMain

    init(clock: @escaping () -> UInt64 = { DispatchTime.now().uptimeNanoseconds }, held: HeldMain = .shared) {
        self.clock = clock
        self.held = held
    }

    static func limitMs(_ r: Request) -> Int {
        max(stuckAfterMs, max(r.int("timeout", 0), r.int("replyMs", 0)) + stuckMarginMs)
    }

    func set(w: Int, h: Int, keyboard: Bool, treeMode: String, quietMs: Int) {
        lock.lock(); defer { lock.unlock() }
        _w = w; _h = h; _keyboard = keyboard; _treeMode = treeMode; _quietMs = quietMs
    }

    var size: (w: Int, h: Int) { lock.lock(); defer { lock.unlock() }; return (_w, _h) }
    var keyboard: Bool { lock.lock(); defer { lock.unlock() }; return _keyboard }
    var treeMode: String { lock.lock(); defer { lock.unlock() }; return _treeMode }
    var quietMs: Int { lock.lock(); defer { lock.unlock() }; return _quietMs }

    func begin(_ op: String, limitMs: Int) {
        let now = clock()
        held.opBegins(op, limitMs: limitMs)
        lock.lock(); defer { lock.unlock() }
        _busy = op; _since = now; _limitMs = limitMs
    }

    func end() {
        lock.lock(); defer { lock.unlock() }
        _busy = ""; _since = 0; _limitMs = 0
    }

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

    var stuck: Bool {
        let b = state
        return !b.op.isEmpty && b.ms > b.limitMs
    }
}

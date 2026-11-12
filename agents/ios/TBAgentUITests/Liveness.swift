// How a connection's requests reach the ops (Server.swift), and what the agent answers at
// once instead of in turn (issue #48).
//
// The ops queue runs one request at a time, and some take long: a launch waits up to a
// minute for the app, a tree read waits for a slow app. tb reconnecting meanwhile (its
// connection dropped, or a new run) asks the runner who it is: the hello, then "info"
// without the keyboard check, within a few seconds. Served behind the op in flight, a busy
// runner did not answer them, and tb took it for a dead one, stopped it and started another.
// They are answered at once now, from the connection's own thread, with only the state kept
// safe for any thread (Vitals, the event counters). A runner that does not answer them is
// truly stuck, and tb still replaces it.
import Foundation

final class Dispatcher {
    private let queue: DispatchQueue

    init(queue: DispatchQueue) { self.queue = queue }

    /// Whether a request is answered at once rather than in turn on the ops queue: the
    /// liveness probe, which reads nothing the ops change mid-run.
    static func answersAtOnce(_ r: Request) -> Bool {
        switch r.op {
        case "ping": return true
        case "info": return !r.bool("keyboard", true) // the keyboard check needs the main thread
        default: return false
        }
    }

    /// Serves one connection: greet sends the hello at once, before anything else on it; read
    /// hands over each request line until the connection closes. The probe's requests go to
    /// now, on this thread; the others to queued, in turn on the ops queue.
    func serve(greet: () -> Void, read: ((Data) -> Void) -> Void,
               now: @escaping (Request) -> Void, queued: @escaping (Request) -> Void) {
        greet()
        read { line in
            guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else {
                NSLog("tb: unreadable request: \(String(decoding: line, as: UTF8.self))")
                return
            }
            let req = Request(raw: obj)
            if Dispatcher.answersAtOnce(req) {
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
    private let lock = NSLock()
    private var _w = 0, _h = 0
    private var _keyboard = false
    private var _treeMode = ""
    private var _quietMs = 0
    private var _busy = ""

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

    /// The op the ops queue is running ("" when none): the probe says what keeps it busy.
    var busy: String {
        get { lock.lock(); defer { lock.unlock() }; return _busy }
        set { lock.lock(); _busy = newValue; lock.unlock() }
    }
}

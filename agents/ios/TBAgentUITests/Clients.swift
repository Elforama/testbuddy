import Foundation

final class Clients<C: AnyObject> {
    private let lock = NSLock()
    private var _current: C?
    private var waiting: [C] = []
    private var greetedConns: [C] = []
    private var _events: C?
    private var _idleArmed = false

    private let onEvents: (C?) -> Void
    private let onIdle: (Bool) -> Void

    init(events: @escaping (C?) -> Void = { _ in }, idle: @escaping (Bool) -> Void = { _ in }) {
        onEvents = events
        onIdle = idle
    }

    var current: C? { lock.lock(); defer { lock.unlock() }; return _current }
    var events: C? { lock.lock(); defer { lock.unlock() }; return _events }
    var idleArmed: Bool { lock.lock(); defer { lock.unlock() }; return _idleArmed }

    static func probes(_ op: String) -> Bool { op == "ping" || op == "info" }

    @discardableResult
    func arrived(_ c: C) -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard _current == nil else { waiting.append(c); return false }
        become(c)
        return true
    }

    func greeted(_ c: C) {
        lock.lock(); defer { lock.unlock() }
        if !greetedConns.contains(where: { $0 === c }) { greetedConns.append(c) }
        if _current === c { setEvents(c) }
    }

    func request(_ c: C, op: String) -> C? {
        lock.lock(); defer { lock.unlock() }
        guard !Clients.probes(op), let i = waiting.firstIndex(where: { $0 === c }) else { return nil }
        waiting.remove(at: i)
        let old = _current
        become(c)
        return old
    }

    func left(_ c: C) {
        lock.lock(); defer { lock.unlock() }
        greetedConns.removeAll { $0 === c }
        if let i = waiting.firstIndex(where: { $0 === c }) { waiting.remove(at: i); return }
        guard _current === c else { return }
        _current = nil
        if let next = waiting.popLast() {
            become(next)
        } else {
            setEvents(nil)
            _idleArmed = true
            onIdle(true)
        }
    }

    private func become(_ c: C) {
        _current = c
        setEvents(greetedConns.contains(where: { $0 === c }) ? c : nil)
        if _idleArmed { _idleArmed = false }
        onIdle(false)
    }

    private func setEvents(_ c: C?) {
        _events = c
        onEvents(c)
    }
}

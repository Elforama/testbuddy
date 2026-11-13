// Which connection is the runner's client (Server.swift): the one whose requests are its
// flow, that gets the pushed events, and whose leaving arms the idle exit.
//
// tb probes eight ports from a simulator's preferred one (the hello, then "info") when it
// connects, reconnects or releases a device, and two simulators' windows can overlap: tb for
// another simulator may probe this runner's port in the middle of a flow (issue #47). The
// runner made every new connection its client and closed the one before, so that probe
// knocked the flow's connection off. Now a connection that comes while there is a client
// waits: it gets the hello and its ping and info answered, which tell the other tb whose
// runner this is, and closing it changes nothing. It takes over at its first other op (a tb
// whose client before it is gone or hung: tb never runs two flows on one simulator), or when
// the client leaves (tb reconnecting can dial before the runner has read its old connection's
// end). A connection with no client before it is the client at once.
//
// The events go to the client only once it has said hello: the host takes the first message
// on a connection for the hello. Every decision and what it does (the events' connection, the
// idle timer) happen under one lock, in order, so a late one cannot undo a newer one.
import Foundation

final class Clients<C: AnyObject> {
    private let lock = NSLock()
    private var _current: C?
    /// Connections that came while there was a client, oldest first.
    private var waiting: [C] = []
    /// Open connections that have sent their hello.
    private var greetedConns: [C] = []
    private var _events: C?
    private var _idleArmed = false

    /// Called under the lock: events(c) sends the pushed events to c (nil: to none);
    /// idle(true) arms the idle exit (the last client left), idle(false) cancels it.
    private let onEvents: (C?) -> Void
    private let onIdle: (Bool) -> Void

    init(events: @escaping (C?) -> Void = { _ in }, idle: @escaping (Bool) -> Void = { _ in }) {
        onEvents = events
        onIdle = idle
    }

    /// The client: nil when none is connected.
    var current: C? { lock.lock(); defer { lock.unlock() }; return _current }
    /// The connection the pushed events go to.
    var events: C? { lock.lock(); defer { lock.unlock() }; return _events }
    /// Whether the idle exit is armed (by a client leaving, with none left).
    var idleArmed: Bool { lock.lock(); defer { lock.unlock() }; return _idleArmed }

    /// The ops of tb's probe, which do not make a connection the client.
    static func probes(_ op: String) -> Bool { op == "ping" || op == "info" }

    /// A connection was accepted. Whether it is the client now: only when there was none.
    @discardableResult
    func arrived(_ c: C) -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard _current == nil else { waiting.append(c); return false }
        become(c)
        return true
    }

    /// Connection c sent its hello: the events reach it from now on if it is the client.
    func greeted(_ c: C) {
        lock.lock(); defer { lock.unlock() }
        if !greetedConns.contains(where: { $0 === c }) { greetedConns.append(c) }
        if _current === c { setEvents(c) }
    }

    /// Connection c sent a request op. Returns the client it replaces, for the caller to
    /// close: when c was waiting and op is not the probe's.
    func request(_ c: C, op: String) -> C? {
        lock.lock(); defer { lock.unlock() }
        guard !Clients.probes(op), let i = waiting.firstIndex(where: { $0 === c }) else { return nil }
        waiting.remove(at: i)
        let old = _current
        become(c)
        return old
    }

    /// Connection c closed. If it was the client, the newest connection still waiting takes
    /// its place; with none, the idle exit is armed.
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

    // Under the lock.
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

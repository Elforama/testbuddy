// Which connection is the runner's client (Server.swift): the one whose requests are its
// flow, that gets the pushed events, and whose leaving re-arms the idle exit.
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
import Foundation

final class Clients<C: AnyObject> {
    private let lock = NSLock()
    private var _current: C?
    /// Connections that came while there was a client, oldest first.
    private var waiting: [C] = []

    init() {}

    /// The client: nil when none is connected.
    var current: C? { lock.lock(); defer { lock.unlock() }; return _current }

    /// The ops of tb's probe, which do not make a connection the client.
    static func probes(_ op: String) -> Bool { op == "ping" || op == "info" }

    /// A connection was accepted. Returns the client it replaces, for the caller to close:
    /// never, now; it is the client at once only when there is none.
    func arrived(_ c: C) -> C? {
        lock.lock(); defer { lock.unlock() }
        if _current == nil { _current = c } else { waiting.append(c) }
        return nil
    }

    /// Connection c sent a request op. Returns the client it replaces, for the caller to
    /// close: when c was waiting and op is not the probe's.
    func request(_ c: C, op: String) -> C? {
        lock.lock(); defer { lock.unlock() }
        guard !Clients.probes(op), let i = waiting.firstIndex(where: { $0 === c }) else { return nil }
        waiting.remove(at: i)
        let old = _current
        _current = c
        return old
    }

    /// Connection c closed. last: it was the client and none is left; next: the connection
    /// that is the client now (the newest still waiting), if c was the client.
    func left(_ c: C) -> (last: Bool, next: C?) {
        lock.lock(); defer { lock.unlock() }
        if let i = waiting.firstIndex(where: { $0 === c }) { waiting.remove(at: i); return (false, nil) }
        guard _current === c else { return (false, nil) }
        _current = waiting.popLast()
        return (_current == nil, _current)
    }
}

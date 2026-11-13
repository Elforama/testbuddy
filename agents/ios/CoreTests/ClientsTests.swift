import XCTest
@testable import TBAgentCore

/// Issue #47: tb probes eight ports from a simulator's preferred one, with the hello and an
/// "info", when it connects, reconnects or releases a device. Two simulators' windows can
/// overlap, so tb for simulator B may probe the port where simulator A's runner serves A's
/// run. The runner made every new connection its client and closed the one before: B's probe
/// knocked A's connection off in the middle of a flow.
final class ClientsTests: XCTestCase {
    final class Conn {}

    /// Clients that record where the events go and the idle timer, as Server.swift does.
    private func clients() -> (Clients<Conn>, idle: () -> [Bool]) {
        var idle: [Bool] = []
        let c = Clients<Conn>(idle: { idle.append($0) })
        return (c, { idle })
    }

    /// A connection that is the client and has said hello, running a flow.
    private func flowClient(_ clients: Clients<Conn>) -> Conn {
        let flow = Conn()
        XCTAssertTrue(clients.arrived(flow))
        clients.greeted(flow)
        XCTAssertNil(clients.request(flow, op: "tap"))
        return flow
    }

    func testAProbeDoesNotDisconnectTheClientRunningAFlow() {
        let (clients, _) = clients()
        let flow = flowClient(clients), probe = Conn()
        XCTAssertFalse(clients.arrived(probe), "a probe's connection took over from the client running a flow")
        clients.greeted(probe)
        XCTAssertNil(clients.request(probe, op: "info"), "a probe's info closed the client running a flow")
        XCTAssertNil(clients.request(probe, op: "ping"), "a probe's ping closed the client running a flow")
        XCTAssertTrue(clients.current === flow)
        XCTAssertTrue(clients.events === flow, "the probe took the flow's events")
        clients.left(probe)
        XCTAssertFalse(clients.idleArmed, "the probe leaving made the runner idle")
        XCTAssertTrue(clients.current === flow)
        XCTAssertTrue(clients.events === flow)
    }

    /// A client that stopped answering (a hung tb) is still replaced: by a new connection's
    /// first request that is not part of the probe.
    func testAConnectionThatSendsAnOpReplacesTheClient() {
        let (clients, _) = clients()
        let stuck = flowClient(clients), next = Conn()
        clients.arrived(next)
        clients.greeted(next)
        _ = clients.request(next, op: "info")
        XCTAssertTrue(clients.request(next, op: "launch") === stuck, "a new connection's op did not replace the client")
        XCTAssertTrue(clients.current === next)
        XCTAssertTrue(clients.events === next)
        clients.left(stuck)
        XCTAssertFalse(clients.idleArmed, "the replaced client leaving made the runner idle")
        XCTAssertTrue(clients.events === next, "the replaced client leaving took the events")
        XCTAssertNil(clients.request(next, op: "tap"))
        clients.left(next)
        XCTAssertTrue(clients.idleArmed)
        XCTAssertNil(clients.current)
        XCTAssertNil(clients.events)
    }

    /// The server learns of each request as it arrives, before it waits for its turn: a new
    /// connection takes over from a hung client even while the ops queue is busy.
    func testTheDispatcherReportsEachRequestBeforeItsTurn() {
        let q = DispatchQueue(label: "test.ops")
        let gate = DispatchSemaphore(value: 0)
        q.async { gate.wait() }
        defer { gate.signal() }
        var seen: [String] = []
        Dispatcher(queue: q, vitals: Vitals()).serve(
            greet: {},
            read: { onLine in
                onLine(Data(#"{"id":1,"op":"info","keyboard":false}"#.utf8))
                onLine(Data(#"{"id":2,"op":"launch","bundleId":"a"}"#.utf8))
            },
            seen: { r in seen.append(r.op) },
            now: { _ in },
            queued: { _ in })
        XCTAssertEqual(seen, ["info", "launch"])
    }

    /// The first connection, with no client, is the client at once: tb that adopts a runner
    /// gets its events from the hello on, before any op.
    func testTheFirstConnectionIsTheClientAtOnce() {
        let (clients, idle) = clients()
        let a = Conn()
        XCTAssertTrue(clients.arrived(a))
        XCTAssertTrue(clients.current === a)
        XCTAssertEqual(idle(), [false], "the idle exit was not cancelled")
        XCTAssertNil(clients.events, "events before the hello")
        clients.greeted(a)
        XCTAssertTrue(clients.events === a)
    }

    /// tb reconnecting can dial before the runner has read its old connection's end: when the
    /// client leaves, the newest connection still open takes its place, so the events reach it.
    func testWhenTheClientLeavesTheNewestConnectionTakesOver() {
        let (clients, _) = clients()
        let old = flowClient(clients), probe = Conn(), again = Conn()
        clients.arrived(probe)
        clients.greeted(probe)
        clients.arrived(again)
        clients.greeted(again)
        _ = clients.request(again, op: "info")
        clients.left(old)
        XCTAssertFalse(clients.idleArmed)
        XCTAssertTrue(clients.current === again)
        XCTAssertTrue(clients.events === again)
        clients.left(again) // probe is still open
        XCTAssertFalse(clients.idleArmed)
        XCTAssertTrue(clients.current === probe)
        XCTAssertTrue(clients.events === probe)
        clients.left(probe)
        XCTAssertTrue(clients.idleArmed)
    }

    /// #47 review: a connection that takes over before its own thread has sent the hello gets no
    /// events until it has: the host reads the first message on a connection as the hello.
    func testAConnectionPromotedBeforeItsHelloGetsNoEventsUntilIt() {
        let (clients, _) = clients()
        let old = flowClient(clients), next = Conn()
        clients.arrived(next)
        clients.left(old) // next takes over, its hello not sent yet
        XCTAssertTrue(clients.current === next)
        XCTAssertNil(clients.events, "events went to a connection before its hello")
        clients.greeted(next)
        XCTAssertTrue(clients.events === next)

        let third = Conn()
        clients.arrived(third)
        XCTAssertTrue(clients.request(third, op: "launch") === next) // before its hello
        XCTAssertNil(clients.events, "events went to a connection before its hello")
        clients.greeted(third)
        XCTAssertTrue(clients.events === third)
    }

    /// #47 review: a promotion decided earlier cannot undo a newer one. Each decision sets the
    /// events and the idle timer as it is made, under one lock.
    func testAStalePromotionDoesNotOverrideANewerOne() {
        let (clients, idle) = clients()
        let old = flowClient(clients), next = Conn(), newer = Conn()
        clients.arrived(next)
        clients.greeted(next)
        clients.arrived(newer)
        clients.greeted(newer)
        clients.left(old)           // newer (the newest waiting) takes over
        XCTAssertTrue(clients.events === newer)
        XCTAssertTrue(clients.request(next, op: "tap") === newer) // next takes over after it
        XCTAssertTrue(clients.events === next)
        clients.left(newer)         // the replaced client leaving changes nothing
        XCTAssertTrue(clients.events === next, "a stale promotion took the events")
        XCTAssertTrue(clients.current === next)
        XCTAssertFalse(clients.idleArmed)
        clients.left(next)
        XCTAssertTrue(clients.idleArmed)
        XCTAssertEqual(idle().last, true, "the runner never exits after its last client left")
    }

    /// #47 review: the idle exit is never armed while a client is connected, and is cancelled
    /// whenever a connection becomes the client.
    func testTheIdleExitIsNotArmedWhileAClientIsConnected() {
        let (clients, idle) = clients()
        let a = flowClient(clients), probe = Conn()
        clients.arrived(probe)
        clients.left(probe)
        XCTAssertFalse(clients.idleArmed)
        XCTAssertFalse(idle().contains(true), "a probe leaving armed the idle exit")
        clients.left(a)
        XCTAssertTrue(clients.idleArmed)
        let b = Conn()
        XCTAssertTrue(clients.arrived(b))
        XCTAssertFalse(clients.idleArmed)
        XCTAssertEqual(idle().last, false)
    }
}

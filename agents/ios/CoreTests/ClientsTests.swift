import XCTest
@testable import TBAgentCore

/// Issue #47: tb probes eight ports from a simulator's preferred one, with the hello and an
/// "info", when it connects, reconnects or releases a device. Two simulators' windows can
/// overlap, so tb for simulator B may probe the port where simulator A's runner serves A's
/// run. The runner made every new connection its client and closed the one before: B's probe
/// knocked A's connection off in the middle of a flow.
final class ClientsTests: XCTestCase {
    final class Conn {}

    func testAProbeDoesNotDisconnectTheClientRunningAFlow() {
        let clients = Clients<Conn>()
        let flow = Conn(), probe = Conn()
        XCTAssertNil(clients.arrived(flow))
        XCTAssertNil(clients.request(flow, op: "tap"))
        XCTAssertNil(clients.arrived(probe), "a probe's connection closed the client running a flow")
        XCTAssertNil(clients.request(probe, op: "info"), "a probe's info closed the client running a flow")
        XCTAssertNil(clients.request(probe, op: "ping"), "a probe's ping closed the client running a flow")
        XCTAssertTrue(clients.current === flow, "the probe took the flow's events")
        XCTAssertEqual(clients.left(probe).last, false, "the probe leaving made the runner idle")
        XCTAssertTrue(clients.current === flow)
    }

    /// A client that stopped answering (a hung tb) is still replaced: by a new connection's
    /// first request that is not part of the probe.
    func testAConnectionThatSendsAnOpReplacesTheClient() {
        let clients = Clients<Conn>()
        let stuck = Conn(), next = Conn()
        XCTAssertNil(clients.arrived(stuck))
        _ = clients.arrived(next)
        _ = clients.request(next, op: "info")
        XCTAssertTrue(clients.request(next, op: "launch") === stuck, "a new connection's op did not replace the client")
        XCTAssertTrue(clients.current === next)
        XCTAssertEqual(clients.left(stuck).last, false, "the replaced client leaving made the runner idle")
        XCTAssertNil(clients.request(next, op: "tap"))
        XCTAssertEqual(clients.left(next).last, true)
        XCTAssertNil(clients.current)
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
        let clients = Clients<Conn>()
        let a = Conn()
        XCTAssertNil(clients.arrived(a))
        XCTAssertTrue(clients.current === a)
    }

    /// tb reconnecting can dial before the runner has read its old connection's end: when the
    /// client leaves, the newest connection still open takes its place, so the events reach it.
    func testWhenTheClientLeavesTheNewestConnectionTakesOver() {
        let clients = Clients<Conn>()
        let old = Conn(), probe = Conn(), again = Conn()
        _ = clients.arrived(old)
        _ = clients.arrived(probe)
        _ = clients.arrived(again)
        _ = clients.request(again, op: "info")
        let left = clients.left(old)
        XCTAssertFalse(left.last)
        XCTAssertTrue(left.next === again)
        XCTAssertTrue(clients.current === again)
        XCTAssertEqual(clients.left(again).last, false) // probe is still open
        XCTAssertTrue(clients.current === probe)
        XCTAssertEqual(clients.left(probe).last, true)
    }
}

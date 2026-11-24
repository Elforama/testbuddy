import XCTest
@testable import TBAgentCore

final class ClientsTests: XCTestCase {
    final class Conn {}

    private func clients() -> (Clients<Conn>, idle: () -> [Bool]) {
        var idle: [Bool] = []
        let c = Clients<Conn>(idle: { idle.append($0) })
        return (c, { idle })
    }

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
        clients.left(again)
        XCTAssertFalse(clients.idleArmed)
        XCTAssertTrue(clients.current === probe)
        XCTAssertTrue(clients.events === probe)
        clients.left(probe)
        XCTAssertTrue(clients.idleArmed)
    }

    func testAConnectionPromotedBeforeItsHelloGetsNoEventsUntilIt() {
        let (clients, _) = clients()
        let old = flowClient(clients), next = Conn()
        clients.arrived(next)
        clients.left(old)
        XCTAssertTrue(clients.current === next)
        XCTAssertNil(clients.events, "events went to a connection before its hello")
        clients.greeted(next)
        XCTAssertTrue(clients.events === next)

        let third = Conn()
        clients.arrived(third)
        XCTAssertTrue(clients.request(third, op: "launch") === next)
        XCTAssertNil(clients.events, "events went to a connection before its hello")
        clients.greeted(third)
        XCTAssertTrue(clients.events === third)
    }

    func testAStalePromotionDoesNotOverrideANewerOne() {
        let (clients, idle) = clients()
        let old = flowClient(clients), next = Conn(), newer = Conn()
        clients.arrived(next)
        clients.greeted(next)
        clients.arrived(newer)
        clients.greeted(newer)
        clients.left(old)
        XCTAssertTrue(clients.events === newer)
        XCTAssertTrue(clients.request(next, op: "tap") === newer)
        XCTAssertTrue(clients.events === next)
        clients.left(newer)
        XCTAssertTrue(clients.events === next, "a stale promotion took the events")
        XCTAssertTrue(clients.current === next)
        XCTAssertFalse(clients.idleArmed)
        clients.left(next)
        XCTAssertTrue(clients.idleArmed)
        XCTAssertEqual(idle().last, true, "the runner never exits after its last client left")
    }

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

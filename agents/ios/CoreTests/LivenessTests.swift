import Foundation
import XCTest
@testable import TBAgentCore

final class LivenessTests: XCTestCase {
    private func line(_ s: String) -> Data { Data(s.utf8) }

    private func busyQueue() -> (DispatchQueue, release: () -> Void) {
        let q = DispatchQueue(label: "test.ops")
        let gate = DispatchSemaphore(value: 0)
        q.async { gate.wait() }
        return (q, { gate.signal() })
    }

    func testABusyRunnerGreetsAndAnswersTheProbe() {
        let (q, release) = busyQueue()
        defer { release() }
        let greeted = expectation(description: "hello sent")
        let probed = expectation(description: "info answered")
        Dispatcher(queue: q, vitals: Vitals()).serve(
            greet: { greeted.fulfill() },
            read: { onLine in onLine(self.line(#"{"id":1,"op":"info","keyboard":false}"#)) },
            now: { r in if r.op == "info" { probed.fulfill() } },
            queued: { r in XCTFail("\(r.op) waited behind the op in flight") })
        wait(for: [greeted, probed], timeout: 1, enforceOrder: true)
    }

    func testABusyRunnerAnswersPing() {
        let (q, release) = busyQueue()
        defer { release() }
        let pinged = expectation(description: "ping answered")
        Dispatcher(queue: q, vitals: Vitals()).serve(
            greet: {},
            read: { onLine in onLine(self.line(#"{"id":2,"op":"ping"}"#)) },
            now: { r in if r.op == "ping" { pinged.fulfill() } },
            queued: { r in XCTFail("\(r.op) waited behind the op in flight") })
        wait(for: [pinged], timeout: 1)
    }

    func testOtherRequestsRunInTurn() {
        let (q, release) = busyQueue()
        var ran: [String] = []
        let lock = NSLock()
        let done = expectation(description: "queued requests ran")
        done.expectedFulfillmentCount = 3
        Dispatcher(queue: q, vitals: Vitals()).serve(
            greet: {},
            read: { onLine in
                onLine(self.line(#"{"id":1,"op":"tap","x":1,"y":2}"#))
                onLine(self.line(#"{"id":2,"op":"info"}"#))
                onLine(self.line(#"{"id":3,"op":"tree"}"#))
            },
            now: { r in XCTFail("\(r.op) was answered out of turn") },
            queued: { r in lock.lock(); ran.append(r.op); lock.unlock(); done.fulfill() })
        lock.lock(); XCTAssertEqual(ran, [], "ran before the op in flight ended"); lock.unlock()
        release()
        wait(for: [done], timeout: 1)
        XCTAssertEqual(ran, ["tap", "info", "tree"])
    }

    func testAnOpPastItsLimitHoldsTheHelloAndTheProbe() {
        var now: UInt64 = 1_000_000_000
        let vitals = Vitals(clock: { now })
        vitals.begin("launch", limitMs: Vitals.stuckAfterMs)
        now += UInt64(Vitals.stuckAfterMs + 1) * 1_000_000
        XCTAssertEqual(vitals.state, Vitals.Busy(op: "launch", ms: Vitals.stuckAfterMs + 1, limitMs: Vitals.stuckAfterMs))
        let (q, release) = busyQueue()
        var greeted = false, answered: [String] = []
        let queued = expectation(description: "the hello and probe ran in turn")
        queued.expectedFulfillmentCount = 3
        Dispatcher(queue: q, vitals: vitals).serve(
            greet: { greeted = true; queued.fulfill() },
            read: { onLine in
                onLine(self.line(#"{"id":1,"op":"info","keyboard":false}"#))
                onLine(self.line(#"{"id":2,"op":"ping"}"#))
            },
            now: { r in answered.append(r.op) },
            queued: { _ in queued.fulfill() })
        XCTAssertFalse(greeted, "a stuck runner said hello at once")
        XCTAssertEqual(answered, [], "a stuck runner answered the probe at once")
        release()
        wait(for: [queued], timeout: 1)
    }

    func testAnOpWithinItsLimitIsBusyNotStuck() {
        var now: UInt64 = 1_000_000_000
        let vitals = Vitals(clock: { now })
        XCTAssertEqual(vitals.state, Vitals.Busy())
        let wait = Request(raw: ["op": "awaitPackage", "timeout": 120_000])
        XCTAssertEqual(Vitals.limitMs(wait), 120_000 + Vitals.stuckMarginMs)
        XCTAssertEqual(Vitals.limitMs(Request(raw: ["op": "launch"])), Vitals.stuckAfterMs)
        vitals.begin("awaitPackage", limitMs: Vitals.limitMs(wait))
        now += 100_000 * 1_000_000
        XCTAssertFalse(vitals.stuck)
        XCTAssertTrue(Dispatcher(queue: DispatchQueue(label: "t"), vitals: vitals).answersAtOnce(Request(raw: ["op": "ping"])))
        now += 60_000 * 1_000_000
        XCTAssertTrue(vitals.stuck)
        vitals.end()
        XCTAssertFalse(vitals.stuck)
        XCTAssertEqual(vitals.state, Vitals.Busy())
    }

    func testAnOpIsNotStuckBeforeItsReplyLimit() {
        XCTAssertEqual(Vitals.limitMs(Request(raw: ["op": "tree"])), Vitals.stuckAfterMs)
        XCTAssertEqual(Vitals.limitMs(Request(raw: ["op": "tree", "replyMs": 100_000])), 100_000 + Vitals.stuckMarginMs)
    }

    func testAnUnreadableLineIsDropped() {
        let (q, release) = busyQueue()
        defer { release() }
        Dispatcher(queue: q, vitals: Vitals()).serve(
            greet: {}, read: { onLine in onLine(self.line("not json")) },
            now: { _ in XCTFail("answered") }, queued: { _ in XCTFail("queued") })
    }
}

import Foundation
import XCTest
@testable import TBAgentCore

/// Issue #48: tb asks a runner it connects to who it is (the hello, then "info" without the
/// keyboard check) and gives it a few seconds. Both were served on the ops queue, behind the
/// op in flight: a runner busy with a launch wait or a slow tree read did not answer, and tb
/// took it for a dead one, stopped it and started another. They are answered at once now;
/// every other request still runs in turn on the ops queue.
final class LivenessTests: XCTestCase {
    private func line(_ s: String) -> Data { Data(s.utf8) }

    /// An ops queue held by a long op until release is called.
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
        Dispatcher(queue: q).serve(
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
        Dispatcher(queue: q).serve(
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
        Dispatcher(queue: q).serve(
            greet: {},
            read: { onLine in
                onLine(self.line(#"{"id":1,"op":"tap","x":1,"y":2}"#))
                onLine(self.line(#"{"id":2,"op":"info"}"#)) // the keyboard check needs the main thread
                onLine(self.line(#"{"id":3,"op":"tree"}"#))
            },
            now: { r in XCTFail("\(r.op) was answered out of turn") },
            queued: { r in lock.lock(); ran.append(r.op); lock.unlock(); done.fulfill() })
        lock.lock(); XCTAssertEqual(ran, [], "ran before the op in flight ended"); lock.unlock()
        release()
        wait(for: [done], timeout: 1)
        XCTAssertEqual(ran, ["tap", "info", "tree"])
    }

    func testAnUnreadableLineIsDropped() {
        let (q, release) = busyQueue()
        defer { release() }
        Dispatcher(queue: q).serve(
            greet: {}, read: { onLine in onLine(self.line("not json")) },
            now: { _ in XCTFail("answered") }, queued: { _ in XCTFail("queued") })
    }
}

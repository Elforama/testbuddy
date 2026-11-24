import XCTest
@testable import TBAgentCore

private final class Waiter {
    let cond = NSCondition()
    let main = DispatchQueue(label: "fake main thread")
    let clock: VirtualClock
    init() { clock = VirtualClock(holding: main) }
    func wait(_ done: () -> Bool, _ timeoutMs: Int) -> Bool {
        clock.waitFor(ms: timeoutMs) { cond.lock(); defer { cond.unlock() }; return done() }
    }
    func locked(_ f: () -> Void) { cond.lock(); f(); cond.broadcast(); cond.unlock() }
    var gate: InputGate { InputGate(wait: { self.wait($0, $1) }, locked: { self.locked($0) }, clock: clock) }
    func ms(since t0: UInt64) -> Int { Int((clock.now() - t0) / 1_000_000) }
}

private func bounded(_ q: DispatchQueue, ms: Int, _ clock: VirtualClock) -> (@escaping () throws -> Void) throws -> Void {
    { f in try Bounded.run(on: q, by: clock.now() + UInt64(ms) * 1_000_000, clock: clock, f) }
}

final class InputGateTests: XCTestCase {
    func testButtonPressWaitsForTheUnconfirmedInput() throws {
        let w = Waiter()
        let gate = w.gate
        let tap = InputGate.Event()
        gate.leave(tap)
        var confirmedAt: UInt64 = 0
        w.clock.at(200) { w.locked { confirmedAt = w.clock.now(); tap.done = true } }
        var pressedAt: UInt64 = 0
        let t0 = w.clock.now()
        try gate.call(deadlineMs: 5000, on: { try $0() }) { pressedAt = w.clock.now() }
        XCTAssertGreaterThan(pressedAt, 0, "the press was not sent")
        XCTAssertGreaterThan(confirmedAt, 0, "the press was sent before the tap was confirmed (issue #286)")
        XCTAssertGreaterThanOrEqual(pressedAt, confirmedAt, "the press went out while the tap was unconfirmed (issue #286)")
        XCTAssertEqual(w.ms(since: t0), 200, "the press waited for the confirmation, 200 ms on, and no longer")
        XCTAssertFalse(gate.unconfirmed)
    }

    func testShortDeadlineThrowsInputNotSentAndSendsNothing() {
        let w = Waiter()
        let gate = w.gate
        gate.leave(InputGate.Event())
        var pressed = false
        let t0 = w.clock.now()
        XCTAssertThrowsError(try gate.call(deadlineMs: 50, on: { try $0() }) { pressed = true }) { e in
            XCTAssertTrue((e as? InputNotSent)?.why.contains("not confirmed") ?? false, "\(e)")
        }
        XCTAssertFalse(pressed, "the press went out while the earlier input was unconfirmed (issue #286)")
        XCTAssertEqual(w.ms(since: t0), 50, "given up on at the op's deadline")
        XCTAssertTrue(gate.unconfirmed)
    }

    func testRecordLeftUnconfirmedHoldsTheNextButtonPress() throws {
        let w = Waiter()
        let gate = w.gate
        let rec = InputGate.Event()
        try gate.send(deadlineMs: 5000) { _ in gate.leave(rec) }
        XCTAssertTrue(gate.unconfirmed)
        var pressed = false
        XCTAssertThrowsError(try gate.call(deadlineMs: 100, on: { try $0() }) { pressed = true })
        XCTAssertFalse(pressed)
        w.locked { rec.done = true }
        try gate.call(deadlineMs: 100, on: { try $0() }) { pressed = true }
        XCTAssertTrue(pressed)
    }

    func testCallGivenUpOnAfterItStartedHoldsTheNextEvent() throws {
        let w = Waiter()
        let gate = w.gate
        let main = w.main
        let press = w.clock.blocker()
        let t0 = w.clock.now()
        XCTAssertThrowsError(try gate.call(deadlineMs: 5000, on: bounded(main, ms: 100, w.clock)) { press.wait() })
        XCTAssertEqual(w.ms(since: t0), 100, "given up on at its deadline")
        XCTAssertTrue(gate.unconfirmed, "a press still running was not kept as unconfirmed")
        var second = false
        XCTAssertThrowsError(try gate.call(deadlineMs: 100, on: { try $0() }) { second = true })
        XCTAssertFalse(second)
        press.release()
        main.sync {}
        try gate.call(deadlineMs: 2000, on: { try $0() }) { second = true }
        XCTAssertTrue(second)
    }

    func testInlineCallStillRunningHoldsTheNextEvent() throws {
        let w = Waiter()
        let gate = w.gate
        let main = w.main
        let tap = w.clock.blocker()
        let running = DispatchSemaphore(value: 0)
        main.async {
            try? gate.call(deadlineMs: 5000, on: { try $0() }) {
                running.signal()
                tap.wait()
            }
        }
        XCTAssertEqual(running.wait(timeout: .now() + VirtualClock.hangGuard), .success)
        XCTAssertTrue(gate.unconfirmed, "a call still running inline was not the unconfirmed event (issue #286)")
        var second = false
        XCTAssertThrowsError(try gate.call(deadlineMs: 100, on: { try $0() }) { second = true }) { e in
            XCTAssertTrue((e as? InputNotSent)?.why.contains("still running on the main thread") ?? false, "\(e)")
        }
        XCTAssertFalse(second, "an event went out while the inline call still ran (issue #286)")
        tap.release()
        main.sync {}
        try gate.call(deadlineMs: 2000, on: { try $0() }) { second = true }
        XCTAssertTrue(second)
    }

    func testCallRunsInsideItsWrap() throws {
        var order: [String] = []
        let gate = InputGate(wait: { _, _ in true }, locked: { $0() },
                             wrap: { f in order.append("made"); return { order.append("in"); try f(); order.append("out") } })
        try gate.call(deadlineMs: nil, on: { try $0() }) { order.append("f") }
        XCTAssertEqual(order, ["made", "in", "f", "out"])
    }

    func testCallNeverStartedLeavesNothingUnconfirmed() throws {
        let w = Waiter()
        let gate = w.gate
        let main = w.main
        let earlier = w.clock.hold()
        main.async { earlier.wait() }
        var ran = false
        XCTAssertThrowsError(try gate.call(deadlineMs: 5000, on: bounded(main, ms: 100, w.clock)) { ran = true })
        XCTAssertFalse(gate.unconfirmed)
        earlier.release()
        main.sync {}
        XCTAssertFalse(ran)
    }
}

final class IssuesTests: XCTestCase {
    static let homeRefused = "Failed to press Home button: Unable to perform <XCDeviceEvent:0x600000c2d8c0 page 12 usage 64 duration 0.01s>, only one gesture can be performed at a time."
    static let qos = "Thread running at User-initiated quality-of-service class waiting on a lower QoS thread running at Default quality-of-service class. Investigate ways to avoid priority inversions"

    func testRefusedDeviceEventIsFatalAndTransient() {
        XCTAssertEqual(XCTestIssue.classify(Self.homeRefused), .fatal(transient: true))
        XCTAssertEqual(XCTestIssue.classify("Failed to press lock button: Unable to perform <XCDeviceEvent:0x1 page 12 usage 48 duration 0.01s>, only one gesture can be performed at a time."),
                       .fatal(transient: true))
    }

    func testOtherFailedDeviceCallsAreFatal() {
        for text in ["Failed to press Volume Up button: error",
                     "Failed to synthesize event: Some error",
                     "Failed to set device orientation: Some error",
                     "Failed to activate application 'dev.testbuddy.bench' (current state: Running Background)",
                     "Failed to terminate dev.testbuddy.bench after 60.0s; state is still `Running Foreground`.",
                     "Failed to launch dev.testbuddy.bench: error"] {
            XCTAssertEqual(XCTestIssue.classify(text), .fatal(transient: false), text)
        }
    }

    func testWarningsAndReadsAreNoise() {
        for text in [Self.qos,
                     "Thread running at User-interactive quality-of-service class waiting on a lower QoS thread running at Default quality-of-service class. Investigate ways to avoid priority inversions",
                     "Failed to get matching snapshot: Error kAXErrorServerNotFound getting snapshot for element <AXUIElementRef 0x1>",
                     "Timed out snapshotting dev.testbuddy.bench, app is either unresponsive or taking too long to snapshot.",
                     "Failed to determine hittability of Button: error",
                     "Failed to get XCUIApplicationProcess for PID 123, querying AX for the bundle ID",
                     "Application dev.testbuddy.bench is not running",
                     ""] {
            XCTAssertEqual(XCTestIssue.classify(text), .noise, text)
        }
    }

    func testSinkFailsTheOpOnAFatalIssueOnly() {
        let sink = IssueSink()
        let gen = sink.begin()
        sink.note(Self.qos)
        XCTAssertNil(sink.failure("pressing key 3"))
        sink.within(gen) { sink.note(Self.homeRefused) }
        let e = sink.failure("pressing key 3")
        XCTAssertNotNil(e, "the key op answered success over XCTest's refusal (issue #286)")
        XCTAssertEqual(e?.transient, true)
        XCTAssertTrue(e?.message.contains("only one gesture can be performed at a time") ?? false, e?.message ?? "")
        XCTAssertTrue(e?.message.hasPrefix("pressing key 3") ?? false, e?.message ?? "")
        sink.begin()
        XCTAssertNil(sink.failure("pressing key 3"))
    }

    func testLateIssueOfAnAbandonedCallIsNotTheNextOps() {
        let sink = IssueSink()
        let first = sink.begin()
        let raised = DispatchSemaphore(value: 0)
        let go = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            sink.within(first) {
                _ = go.wait(timeout: .now() + 5)
                sink.note(Self.homeRefused)
            }
            raised.signal()
        }
        _ = sink.begin()
        go.signal()
        XCTAssertEqual(raised.wait(timeout: .now() + 5), .success)
        XCTAssertNil(sink.failure("pressing key 3"), "the next op took the abandoned press's refusal (issue #286)")
    }

    func testFatalIssueOutsideTheOpsOwnCallIsNotTransient() {
        let sink = IssueSink()
        _ = sink.begin()
        sink.note(Self.homeRefused)
        let e = sink.failure("pressing key 3")
        XCTAssertNotNil(e)
        XCTAssertEqual(e?.transient, false, "a refusal raised outside the op's own call was taken as transient (issue #286)")
    }

    func testSinkTakesIssuesFromAnyThread() {
        let sink = IssueSink()
        sink.begin()
        DispatchQueue.concurrentPerform(iterations: 50) { i in sink.note(i == 25 ? Self.homeRefused : Self.qos) }
        XCTAssertNotNil(sink.failure("rotating"))
    }
}

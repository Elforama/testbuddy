import XCTest
@testable import TBAgentCore

/// The waiter the agent gives the gate (Events.awaitInput and Events.signal), on a lock of its
/// own, and its time: a virtual clock (issue #314). A wait runs what falls due on the clock (a
/// confirmation testmanagerd sends later) and otherwise ends at its timeout, with no real time
/// passing: how long the gate waited is read on the clock, never the runner's.
private final class Waiter {
    let cond = NSCondition()
    let clock = VirtualClock()
    func wait(_ done: () -> Bool, _ timeoutMs: Int) -> Bool {
        clock.waitFor(ms: timeoutMs) { cond.lock(); defer { cond.unlock() }; return done() }
    }
    func locked(_ f: () -> Void) { cond.lock(); f(); cond.broadcast(); cond.unlock() }
    var gate: InputGate { InputGate(wait: { self.wait($0, $1) }, locked: { self.locked($0) }, clock: clock) }
    /// ms of the clock since t0.
    func ms(since t0: UInt64) -> Int { Int((clock.now() - t0) / 1_000_000) }
}

/// Runs a call on the main thread as the agent's key op does (AppWait.read): on a queue of its
/// own here, given up on at a deadline on clock. Work given up on before it started never runs.
private func bounded(_ q: DispatchQueue, ms: Int, _ clock: VirtualClock) -> (@escaping () throws -> Void) throws -> Void {
    { f in try Bounded.run(on: q, by: clock.now() + UInt64(ms) * 1_000_000, clock: clock, f) }
}

final class InputGateTests: XCTestCase {
    /// Issue #286: a tap whose app stays busy is confirmed by testmanagerd only later; the tap op
    /// answered on the app's "touches completed" and left the record unconfirmed. A Home press
    /// sent then was refused by XCTest ("only one gesture can be performed at a time"). Every
    /// device event goes through the gate: the press starts only once the earlier input is
    /// confirmed.
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

    /// Issue #286: with the op's deadline too short to see the earlier input confirmed, the press
    /// is not sent at all: InputNotSent, which the op reports as transient (nothing was sent).
    func testShortDeadlineThrowsInputNotSentAndSendsNothing() {
        let w = Waiter()
        let gate = w.gate
        gate.leave(InputGate.Event()) // never confirmed
        var pressed = false
        let t0 = w.clock.now()
        XCTAssertThrowsError(try gate.call(deadlineMs: 50, on: { try $0() }) { pressed = true }) { e in
            XCTAssertTrue((e as? InputNotSent)?.why.contains("not confirmed") ?? false, "\(e)")
        }
        XCTAssertFalse(pressed, "the press went out while the earlier input was unconfirmed (issue #286)")
        XCTAssertEqual(w.ms(since: t0), 50, "given up on at the op's deadline")
        // Still unconfirmed: the op asked again waits for it too.
        XCTAssertTrue(gate.unconfirmed)
    }

    /// Issue #286: the same gate for play()'s records: a record sent while one is unconfirmed
    /// waits for it, and a record left unconfirmed holds the next device event of any kind.
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

    /// Issue #286: a button press the op gave up on after it started (the main thread held by
    /// the app) finishes later on its own: until it does, it is the unconfirmed event, and the
    /// next event waits for it.
    func testCallGivenUpOnAfterItStartedHoldsTheNextEvent() throws {
        let w = Waiter()
        let gate = w.gate
        let main = DispatchQueue(label: "fake main thread")
        let press = w.clock.blocker() // XCTest's press, still waiting on testmanagerd
        let t0 = w.clock.now()
        XCTAssertThrowsError(try gate.call(deadlineMs: 5000, on: bounded(main, ms: 100, w.clock)) { press.wait() })
        XCTAssertEqual(w.ms(since: t0), 100, "given up on at its deadline")
        XCTAssertTrue(gate.unconfirmed, "a press still running was not kept as unconfirmed")
        var second = false
        XCTAssertThrowsError(try gate.call(deadlineMs: 100, on: { try $0() }) { second = true })
        XCTAssertFalse(second)
        press.release()
        main.sync {} // the press ends
        try gate.call(deadlineMs: 2000, on: { try $0() }) { second = true }
        XCTAssertTrue(second)
    }

    /// Issue #286 (review): a call that runs inline on the main thread (typing's
    /// XCUIElement.tap(), inside a read the op gives up on) is the unconfirmed event while it
    /// runs: an op that went on meanwhile does not send through it.
    func testInlineCallStillRunningHoldsTheNextEvent() throws {
        let w = Waiter()
        let gate = w.gate
        let main = DispatchQueue(label: "fake main thread")
        let tap = w.clock.blocker() // field.tap(), still waiting on the app
        let running = DispatchSemaphore(value: 0)
        main.async {
            try? gate.call(deadlineMs: 5000, on: { try $0() }) {
                running.signal()
                tap.wait()
            }
        }
        // A guard against a hang only.
        XCTAssertEqual(running.wait(timeout: .now() + VirtualClock.hangGuard), .success)
        XCTAssertTrue(gate.unconfirmed, "a call still running inline was not the unconfirmed event (issue #286)")
        var second = false
        XCTAssertThrowsError(try gate.call(deadlineMs: 100, on: { try $0() }) { second = true }) { e in
            // The error says why: a call hung on the main thread, not a missing confirmation.
            XCTAssertTrue((e as? InputNotSent)?.why.contains("still running on the main thread") ?? false, "\(e)")
        }
        XCTAssertFalse(second, "an event went out while the inline call still ran (issue #286)")
        tap.release()
        main.sync {} // the call ends
        try gate.call(deadlineMs: 2000, on: { try $0() }) { second = true }
        XCTAssertTrue(second)
    }

    /// Issue #286 (review): the gate runs each call inside wrap, made on the op's thread before
    /// the call (the op's XCTest issue generation), and runs it where run runs it.
    func testCallRunsInsideItsWrap() throws {
        var order: [String] = []
        let gate = InputGate(wait: { _, _ in true }, locked: { $0() },
                             wrap: { f in order.append("made"); return { order.append("in"); try f(); order.append("out") } })
        try gate.call(deadlineMs: nil, on: { try $0() }) { order.append("f") }
        XCTAssertEqual(order, ["made", "in", "f", "out"])
    }

    /// A call given up on before it started never runs: it leaves nothing unconfirmed.
    func testCallNeverStartedLeavesNothingUnconfirmed() throws {
        let w = Waiter()
        let gate = w.gate
        let main = DispatchQueue(label: "fake main thread")
        let earlier = w.clock.hold() // an earlier call holds the main thread
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
    /// Issue #286: the simulator log of the failing run.
    static let homeRefused = "Failed to press Home button: Unable to perform <XCDeviceEvent:0x600000c2d8c0 page 12 usage 64 duration 0.01s>, only one gesture can be performed at a time."
    static let qos = "Thread running at User-initiated quality-of-service class waiting on a lower QoS thread running at Default quality-of-service class. Investigate ways to avoid priority inversions"

    /// Issue #286: XCTest's refusal of the Home press is fatal (the op must not answer success)
    /// and transient (nothing took effect: asking again cannot press twice).
    func testRefusedDeviceEventIsFatalAndTransient() {
        XCTAssertEqual(XCTestIssue.classify(Self.homeRefused), .fatal(transient: true))
        XCTAssertEqual(XCTestIssue.classify("Failed to press lock button: Unable to perform <XCDeviceEvent:0x1 page 12 usage 48 duration 0.01s>, only one gesture can be performed at a time."),
                       .fatal(transient: true))
    }

    /// Issue #286: other failed device events and app lifecycle calls fail their op too; not
    /// transient, as XCTest does not say whether anything took effect.
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

    /// Issue #286: XCTest's performance and QoS warnings, and the reads the agent already
    /// handles on its own, never fail an op (they show up on most runs).
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

    /// Issue #286: an op never answers success over a fatal issue raised inside it; noise alone
    /// does not fail it; each op starts with an empty sink.
    func testSinkFailsTheOpOnAFatalIssueOnly() {
        let sink = IssueSink()
        let gen = sink.begin()
        sink.note(Self.qos)
        XCTAssertNil(sink.failure("pressing key 3"))
        sink.within(gen) { sink.note(Self.homeRefused) } // raised inside the op's own press
        let e = sink.failure("pressing key 3")
        XCTAssertNotNil(e, "the key op answered success over XCTest's refusal (issue #286)")
        XCTAssertEqual(e?.transient, true)
        XCTAssertTrue(e?.message.contains("only one gesture can be performed at a time") ?? false, e?.message ?? "")
        XCTAssertTrue(e?.message.hasPrefix("pressing key 3") ?? false, e?.message ?? "")
        sink.begin()
        XCTAssertNil(sink.failure("pressing key 3"))
    }

    /// Issue #286 (review): a press the last op gave up on after it started raises its refusal
    /// while the next op runs. It is that press's, not the next op's: only logged. Were it
    /// filed as the next op's transient error, the host's CI retry would press Home twice.
    func testLateIssueOfAnAbandonedCallIsNotTheNextOps() {
        let sink = IssueSink()
        let first = sink.begin()
        let raised = DispatchSemaphore(value: 0)
        let go = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            sink.within(first) { // the abandoned press, still running on the main thread
                _ = go.wait(timeout: .now() + 5)
                sink.note(Self.homeRefused)
            }
            raised.signal()
        }
        _ = sink.begin() // the next op
        go.signal()
        XCTAssertEqual(raised.wait(timeout: .now() + 5), .success)
        XCTAssertNil(sink.failure("pressing key 3"), "the next op took the abandoned press's refusal (issue #286)")
    }

    /// Issue #286 (review): a fatal issue raised outside the op's own gated call fails the op,
    /// but never as transient: whether its own event went is not known.
    func testFatalIssueOutsideTheOpsOwnCallIsNotTransient() {
        let sink = IssueSink()
        _ = sink.begin()
        sink.note(Self.homeRefused)
        let e = sink.failure("pressing key 3")
        XCTAssertNotNil(e)
        XCTAssertEqual(e?.transient, false, "a refusal raised outside the op's own call was taken as transient (issue #286)")
    }

    /// Issues are noted on whatever thread XCTest raises them.
    func testSinkTakesIssuesFromAnyThread() {
        let sink = IssueSink()
        sink.begin()
        DispatchQueue.concurrentPerform(iterations: 50) { i in sink.note(i == 25 ? Self.homeRefused : Self.qos) }
        XCTAssertNotNil(sink.failure("rotating"))
    }
}

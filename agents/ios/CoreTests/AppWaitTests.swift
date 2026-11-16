import Foundation
import XCTest
@testable import TBAgentCore

/// Issue #130: after #46 bounded type and erase, the tree read (which every command depends
/// on), clear, key and hideKeyboard still waited on the agent's main thread with no limit. An
/// XCTest read there waits for the app to answer accessibility, which an app whose main thread
/// is busy does not do: the host timed out blind ("agent did not answer"), and the request it
/// gave up on held the next ones. They answer by the host's reply limit now, saying what they
/// waited for.
final class AppWaitTests: XCTestCase {
    /// The reads' time (issue #314): a virtual clock, so a deadline is reached in the test's
    /// time, not the runner's.
    /// The fake main thread, the one queue the app can hold (VirtualClock.heldQueue).
    private var main = DispatchQueue(label: "fake main thread")
    private var clock = VirtualClock()
    override func setUp() {
        main = DispatchQueue(label: "fake main thread")
        clock = VirtualClock(holding: main)
    }

    /// A main thread held by an earlier XCTest call until release is called, or, given ms, for
    /// ms of the clock.
    private func busyMain(forMs ms: Int? = nil) -> (DispatchQueue, release: () -> Void) {
        let q = main
        let busy = ms.map { clock.hold(forMs: $0) } ?? clock.hold()
        q.async { busy.wait() }
        return (q, { busy.release() })
    }

    private func deadline(answerInMs ms: Int) -> TypingDeadline {
        let now = clock.now()
        return TypingDeadline(start: now, answerBy: now + UInt64(ms) * 1_000_000, keysMs: 0)
    }

    /// Runs f on another thread (the ops queue): nil if it never answers (VirtualClock.hangGuard,
    /// a guard against a hang only: how long it took is read on the clock).
    private func answer<T>(_ f: @escaping () throws -> T) -> Result<T, Error>? {
        let done = DispatchSemaphore(value: 0)
        var result: Result<T, Error>?
        DispatchQueue.global().async {
            result = Result { try f() }
            done.signal()
        }
        return done.wait(timeout: .now() + VirtualClock.hangGuard) == .timedOut ? nil : result
    }

    /// The main thread is busy (the app does not answer the XCTest call ahead of the read): the
    /// read answers by its deadline, transient, saying it never reached the main thread.
    func testATreeReadAnswersByItsDeadlineWhileTheMainThreadIsBusy() {
        let (q, release) = busyMain()
        defer { release() }
        var wrote = false
        let clock = self.clock
        guard let r = answer({ try ScreenRead.run(on: q, by: self.deadline(answerInMs: 500), clock: clock) { _ in wrote = true } }) else {
            return XCTFail("the tree read never answered (the main thread stayed busy)")
        }
        XCTAssertEqual(clock.elapsedMs, 500, "answered at \(clock.elapsedMs) ms, not at its deadline")
        guard case .failure(let err) = r else { return XCTFail("the read succeeded on a busy main thread") }
        XCTAssertEqual((err as? AgentError)?.transient, true, "nothing was read: the host may ask again (\(err))")
        XCTAssertTrue("\(err)".contains("reading the screen"), "\(err)")
        XCTAssertTrue("\(err)".contains("the app did not answer accessibility"), "\(err)")
        XCTAssertTrue("\(err)".contains("waiting for the main thread"), "the error says what it waited for: \(err)")
        release()
        q.sync {}
        XCTAssertFalse(wrote, "the abandoned read ran after the op answered")
    }

    /// The snapshot itself waits for the app: the read answers by its deadline, saying so, and
    /// the late snapshot does not touch the reply.
    func testATreeReadAnswersByItsDeadlineWhileTheSnapshotWaits() {
        let q = main
        let app = clock.blocker()
        let clock = self.clock
        guard let r = answer({
            try ScreenRead.run(on: q, by: self.deadline(answerInMs: 500), clock: clock) { w in
                app.wait()
                w.put("windows", "late")
            }
        }) else {
            app.release()
            return XCTFail("the tree read never answered (the snapshot waited for the app)")
        }
        XCTAssertEqual(clock.elapsedMs, 500, "answered at \(clock.elapsedMs) ms, not at its deadline")
        app.release()
        q.sync {}
        guard case .failure(let err) = r else { return XCTFail("the read succeeded although the snapshot never ended") }
        XCTAssertTrue("\(err)".contains("taking the snapshot"), "the error says what it waited for: \(err)")
        XCTAssertEqual((err as? AgentError)?.transient, true, "\(err)")
    }

    /// Review of #314: a hold on the main thread holds only work there. Work on another queue is
    /// free, however long it takes in real time: its wait is not cut to its deadline.
    func testWorkOnAnotherQueueIsNotHeldByTheMainThreadsHold() throws {
        let (_, release) = busyMain()
        defer { release() }
        let other = DispatchQueue(label: "another queue")
        let v = try Bounded.run(on: other, by: clock.now() + 200_000_000, clock: clock) { () -> Int in
            Thread.sleep(forTimeInterval: 0.05) // real work, longer than a wait's 1 ms poll
            return 7
        }
        XCTAssertEqual(v, 7, "work free to run was given up on")
        XCTAssertEqual(clock.elapsedMs, 0)
    }

    /// A read in time gives its fields, which the op adds to its reply.
    func testATreeReadInTimeGivesItsFields() throws {
        let q = DispatchQueue(label: "fake main thread")
        let fields = try ScreenRead.run(on: q, by: deadline(answerInMs: 2000), clock: clock) { w in w.put("seq", 3).put("keyboard", false) }
        let reply = JW().obj().put("id", 7).put("ok", true).fields(of: fields).put("seq", 4).end()
        XCTAssertEqual(String(decoding: reply.bytes, as: UTF8.self), #"{"id":7,"ok":true,"seq":3,"keyboard":false,"seq":4}"#)
        // Without a deadline (a host that sends no replyMs) the read waits, as before.
        XCTAssertEqual(String(decoding: try ScreenRead.run(on: q, by: nil) { $0.put("a", 1) }.bytes, as: UTF8.self), #"{"a":1}"#)
    }

    /// Issue #255: the agent logged how long a slow read waited for the app's main thread, but
    /// did not tell the host, so a read that did answer, slowly, could not be named "app main
    /// thread held". A read held there for over a second says so in its fields (mainWaitMs); a
    /// prompt read does not, so replies stay as they were.
    func testATreeReadHeldOnTheMainThreadReportsTheWait() throws {
        let (q, release) = busyMain(forMs: 1300)
        defer { release() }
        let fields = try ScreenRead.run(on: q, by: deadline(answerInMs: 5000), clock: clock) { w in w.put("seq", 3) }
        let json = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(fields.bytes)) as? [String: Any])
        let waited = try XCTUnwrap(json["mainWaitMs"] as? Int, "no mainWaitMs in \(json)")
        XCTAssertEqual(waited, 1300, "the main thread was held 1300 ms: \(json)")
        XCTAssertEqual(json["seq"] as? Int, 3)

        let prompt = try ScreenRead.run(on: DispatchQueue(label: "idle main thread"), by: deadline(answerInMs: 5000), clock: clock) { w in w.put("seq", 4) }
        XCTAssertEqual(String(decoding: prompt.bytes, as: UTF8.self), #"{"seq":4}"#, "a prompt read sends no mainWaitMs")
    }

    /// Item 2: Input.play waited up to 10 s for an earlier record testmanagerd had not
    /// confirmed, whatever the op's deadline. With one, it waits no longer than the time the
    /// op gave the record; without one, 10 s as before.
    func testThePendingRecordWaitIsBoundedByTheOpsDeadline() {
        XCTAssertEqual(PendingInput.waitMs(boundedBy: 1500), 1500)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: 60_000), 10_000)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: -5), 0)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: nil), 10_000)
    }

    /// Review of #130: after waiting for an earlier record, a record still gets its own
    /// timeout, cut only to what is left of the op's deadline. A swipe confirmed early whose
    /// real end comes 8 s later, then Enter (own 10 s) with 18 s left of the key op: the Enter
    /// has 10 s, not the 2 s that "timeout minus the wait" left it.
    func testTheWaitForAnEarlierRecordDoesNotEatTheRecordsOwnTime() {
        let deadlineMs = 18_000, waitedMs = 8_000
        XCTAssertEqual(PendingInput.confirmMs(ownMs: 10_000, leftMs: deadlineMs - waitedMs), 10_000)
        XCTAssertEqual(PendingInput.confirmMs(ownMs: 10_000, leftMs: 4_000), 4_000, "never past the op's deadline")
        XCTAssertEqual(PendingInput.confirmMs(ownMs: 10_000, leftMs: nil), 10_000, "no deadline: its own timeout")
        XCTAssertNil(PendingInput.confirmMs(ownMs: 10_000, leftMs: 100), "too little left: not sent")
    }

    /// Review of #130: a call given up on that still runs on the main thread (an XCTest call
    /// waiting on an app that never answers) holds it while the ops queue is idle. Vitals report
    /// it as the busy op; past the limit of the op that left it, the runner counts as stuck, so
    /// tb replaces it (issue #48) instead of reusing a runner whose every op fails.
    func testAMainThreadHeldPastItsOpsLimitMakesTheRunnerStuck() {
        var now: UInt64 = 1_000_000_000
        let held = HeldMain(clock: { now })
        let v = Vitals(clock: { now }, held: held)
        v.begin("tree", limitMs: 90_000)
        let id = held.held(since: now)
        v.end()
        XCTAssertEqual(v.state.op, "tree (given up on, still holding the main thread)", "the ops queue is idle, the main thread is not")
        XCTAssertFalse(v.stuck)
        now += 91_000 * 1_000_000
        XCTAssertEqual(v.state.ms, 91_000)
        XCTAssertEqual(v.state.limitMs, 90_000)
        XCTAssertTrue(v.stuck, "held past its op's limit: stuck")
        held.released(id)
        XCTAssertEqual(v.state, Vitals.Busy())
        XCTAssertFalse(v.stuck)
    }

    /// Bounded.run records work it gave up on while it still runs, and forgets it when it ends;
    /// work that never started holds nothing.
    func testBoundedRunRecordsWorkItGaveUpOn() {
        let held = HeldMain()
        let q = main
        let app = clock.blocker()
        XCTAssertThrowsError(try Bounded.run(on: q, by: clock.now() + 200_000_000, held: held, clock: clock) {
            app.wait()
        })
        XCTAssertNotNil(held.oldest, "the call still runs on its queue")
        XCTAssertThrowsError(try Bounded.run(on: q, by: clock.now() + 100_000_000, held: held, clock: clock) { 1 })
        XCTAssertEqual(clock.elapsedMs, 300, "each given up on at its deadline")
        app.release()
        q.sync {}
        XCTAssertNil(held.oldest, "the call ended; the one queued behind it never started")
    }

    /// The error of an op that gave up on the app: how long, what it waited for, what was done.
    func testTheErrorSaysWhatWasWaitedFor() {
        let start = VirtualClock.start
        let e = AppWait.notAnswering("pressing key 66", since: start, "checking which app is in front", "the key was not pressed",
                                     now: start + 2_500_000_000)
        XCTAssertTrue(e.transient)
        XCTAssertTrue(e.message.hasPrefix("pressing key 66 did not start: the app did not answer accessibility for 2.5 s (checking which app is in front); the key was not pressed"), e.message)
    }
}

import Foundation
import XCTest
@testable import TBAgentCore

final class AppWaitTests: XCTestCase {
    private var main = DispatchQueue(label: "fake main thread")
    private var clock = VirtualClock()
    override func setUp() {
        main = DispatchQueue(label: "fake main thread")
        clock = VirtualClock(holding: main)
    }

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

    private func answer<T>(_ f: @escaping () throws -> T) -> Result<T, Error>? {
        let done = DispatchSemaphore(value: 0)
        var result: Result<T, Error>?
        DispatchQueue.global().async {
            result = Result { try f() }
            done.signal()
        }
        return done.wait(timeout: .now() + VirtualClock.hangGuard) == .timedOut ? nil : result
    }

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

    func testWorkOnAnotherQueueIsNotHeldByTheMainThreadsHold() throws {
        let (_, release) = busyMain()
        defer { release() }
        let other = DispatchQueue(label: "another queue")
        let v = try Bounded.run(on: other, by: clock.now() + 200_000_000, clock: clock) { () -> Int in
            Thread.sleep(forTimeInterval: 0.05)
            return 7
        }
        XCTAssertEqual(v, 7, "work free to run was given up on")
        XCTAssertEqual(clock.elapsedMs, 0)
    }

    func testATreeReadInTimeGivesItsFields() throws {
        let q = DispatchQueue(label: "fake main thread")
        let fields = try ScreenRead.run(on: q, by: deadline(answerInMs: 2000), clock: clock) { w in w.put("seq", 3).put("keyboard", false) }
        let reply = JW().obj().put("id", 7).put("ok", true).fields(of: fields).put("seq", 4).end()
        XCTAssertEqual(String(decoding: reply.bytes, as: UTF8.self), #"{"id":7,"ok":true,"seq":3,"keyboard":false,"seq":4}"#)
        XCTAssertEqual(String(decoding: try ScreenRead.run(on: q, by: nil) { $0.put("a", 1) }.bytes, as: UTF8.self), #"{"a":1}"#)
    }

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

    func testThePendingRecordWaitIsBoundedByTheOpsDeadline() {
        XCTAssertEqual(PendingInput.waitMs(boundedBy: 1500), 1500)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: 60_000), 10_000)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: -5), 0)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: nil), 10_000)
    }

    func testTheWaitForAnEarlierRecordDoesNotEatTheRecordsOwnTime() {
        let deadlineMs = 18_000, waitedMs = 8_000
        XCTAssertEqual(PendingInput.confirmMs(ownMs: 10_000, leftMs: deadlineMs - waitedMs), 10_000)
        XCTAssertEqual(PendingInput.confirmMs(ownMs: 10_000, leftMs: 4_000), 4_000, "never past the op's deadline")
        XCTAssertEqual(PendingInput.confirmMs(ownMs: 10_000, leftMs: nil), 10_000, "no deadline: its own timeout")
        XCTAssertNil(PendingInput.confirmMs(ownMs: 10_000, leftMs: 100), "too little left: not sent")
    }

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

    func testTheErrorSaysWhatWasWaitedFor() {
        let start = VirtualClock.start
        let e = AppWait.notAnswering("pressing key 66", since: start, "checking which app is in front", "the key was not pressed",
                                     now: start + 2_500_000_000)
        XCTAssertTrue(e.transient)
        XCTAssertTrue(e.message.hasPrefix("pressing key 66 did not start: the app did not answer accessibility for 2.5 s (checking which app is in front); the key was not pressed"), e.message)
    }
}

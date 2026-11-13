import Foundation
import XCTest
@testable import TBAgentCore

/// SpringBoard keeping up with a gesture before it is played (issue #137): when SpringBoard is
/// behind, the app's gesture recognizers are held until it catches up, and a swipe that lifts
/// before then does nothing (a pager stays on its page).
final class SystemWaitTests: XCTestCase {
    /// Time the test drives (issue #228): SpringBoard's answers are events due at a time on it,
    /// and the wait's pauses and waits for an answer move it on, running the events due by then.
    /// Nothing depends on how promptly the runner schedules threads: on a loaded CI runner an
    /// answer asked for 50 ms later came back late enough to change the outcome.
    private final class FakeClock {
        private(set) var now: TimeInterval = 0
        private var due: [(at: TimeInterval, f: () -> Void)] = []

        /// Runs f once the clock reaches s seconds from now.
        func after(_ s: TimeInterval, _ f: @escaping () -> Void) { due.append((now + s, f)) }

        /// Runs the events due by end, in their order, until done holds; the clock then reads
        /// the time done came to hold, or end.
        private func run(until end: TimeInterval, done: () -> Bool) -> Bool {
            while !done() {
                guard let i = due.indices.filter({ due[$0].at <= end }).min(by: { due[$0].at < due[$1].at }) else {
                    now = max(now, end)
                    return false
                }
                let e = due.remove(at: i)
                now = max(now, e.at)
                e.f()
            }
            return true
        }

        var clock: SystemWait.Clock {
            SystemWait.Clock(now: { self.now },
                             sleep: { s in _ = self.run(until: self.now + s) { false } },
                             wait: { sem, until in self.run(until: until) { sem.wait(timeout: .now()) == .success } })
        }
    }

    /// A SpringBoard that is behind for its first `delay` seconds (nil: forever) and answers
    /// "idle" at once after that, counting the asks.
    private final class FakeSpringBoard {
        let clock: FakeClock
        let until: TimeInterval?
        var asks = 0
        init(_ clock: FakeClock, delay: TimeInterval?) { self.clock = clock; until = delay.map { clock.now + $0 } }
        func ask(_ done: @escaping (Error?) -> Void) {
            asks += 1
            guard let until else { return }
            clock.after(max(0, until - clock.now)) { done(nil) }
        }
    }

    /// The outcome of f and how long it took on the clock.
    private func timed(_ clock: FakeClock, _ f: () -> SystemWait.Outcome) -> (SystemWait.Outcome, TimeInterval) {
        let t0 = clock.now
        let r = f()
        return (r, clock.now - t0)
    }

    // Issue #137: in CI, SpringBoard confirmed a pager swipe's touches 1.8 s after the app did
    // (on time for every other gesture), the swipe lifted while the app's pan was still held,
    // and the page did not turn. The swipe has to wait for SpringBoard first.
    func testAGestureWaitsForASpringBoardThatIsBehind() {
        let c = FakeClock()
        let sb = FakeSpringBoard(c, delay: 0.6)
        let w = SystemWait(clock: c.clock, ask: sb.ask)
        let (r, s) = timed(c) { w.wait() }
        // Its first answer was late: asked again, after a pause, to see it keep up.
        XCTAssertEqual(r, .answered(asks: 2), "the gesture should wait for SpringBoard's answer")
        XCTAssertGreaterThanOrEqual(s, 0.6 + SystemWait.afterBehindS - 0.05, "the gesture should not be played before SpringBoard answers")
        XCTAssertEqual(sb.asks, 2)
    }

    func testOnlySwipesAndDragsWaitForSpringBoard() throws {
        let rec = Recorder()
        XCTAssertTrue(try Gestures.swipe(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 300, y: 10), ms: 400).waitsForSystem)
        XCTAssertTrue(try Gestures.swipe(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 300, y: 10), ms: 400, rest: true).waitsForSystem)
        XCTAssertTrue(try Gestures.drag(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 10, y: 300)).waitsForSystem)
        // A held tap is recognized late, not lost; and a wait between finding a target and
        // touching it could leave its coordinates stale.
        XCTAssertFalse(try Gestures.tap(rec, at: CGPoint(x: 1, y: 1)).waitsForSystem)
        XCTAssertFalse(try Gestures.tap(rec, at: CGPoint(x: 1, y: 1), holdMs: 1000).waitsForSystem)
        XCTAssertFalse(try Gestures.taps(rec, at: CGPoint(x: 1, y: 1), count: 2).waitsForSystem)
    }

    // Measured on iOS 27 with SpringBoard stopped 2 s of every 8 s: a swipe played 30 ms after
    // its late "idle" answer was still held for 765 ms (it had not caught up on touches).
    func testALateAnswerIsNotEnoughSpringBoardMustAnswerPromptly() {
        let c = FakeClock()
        var asks = 0
        let w = SystemWait(waitS: 2, promptS: 0.1, clock: c.clock) { done in
            asks += 1
            c.after(asks < 3 ? 0.3 : 0) { done(nil) }
        }
        let (r, s) = timed(c) { w.wait() }
        XCTAssertEqual(r, .answered(asks: 3))
        XCTAssertGreaterThanOrEqual(s, 2 * (0.3 + SystemWait.afterBehindS) - 0.05)
    }

    func testTheWaitOutlastsSpringBoardBeingBehindInCI() {
        // #137's CI log: SpringBoard answered ~2.3 s after the swipe started.
        XCTAssertGreaterThanOrEqual(SystemWait.waitS, 2.3 + 0.5)
        // The host allows 15 s over the gesture for its reply.
        XCTAssertLessThanOrEqual(SystemWait.waitS, 5)
    }

    func testASpringBoardThatKeepsUpCostsNothing() {
        let c = FakeClock()
        let sb = FakeSpringBoard(c, delay: 0)
        let w = SystemWait(clock: c.clock, ask: sb.ask)
        let (r, s) = timed(c) { w.wait() }
        XCTAssertEqual(r, .answered(asks: 1))
        XCTAssertLessThan(s, 0.2)
    }

    // Review of #137: an error from XCTest must not pass for SpringBoard's answer.
    func testAnErrorIsNotAnAnswer() {
        struct Refused: Error {}
        var asks = 0
        let w = SystemWait(waitS: 0.5) { done in asks += 1; done(Refused()) }
        for _ in 0..<(SystemWait.missesToStop + 1) { XCTAssertEqual(w.wait(), .notAsked) }
        XCTAssertEqual(asks, SystemWait.missesToStop + 1, "an error is not a miss: asking goes on")
    }

    func testASpringBoardThatCannotBeAskedIsNotWaitedFor() {
        struct NoProcess: Error {}
        let c = FakeClock()
        let w = SystemWait(clock: c.clock) { _ in throw NoProcess() }
        let (r, s) = timed(c) { w.wait() }
        XCTAssertEqual(r, .notAsked)
        XCTAssertLessThan(s, 0.05)
    }

    func testTheWaitIsBoundedAndPausesAskingASpringBoardThatNeverAnswers() {
        let c = FakeClock()
        let sb = FakeSpringBoard(c, delay: nil)
        let w = SystemWait(waitS: 0.1, clock: c.clock, ask: sb.ask)
        for _ in 0..<SystemWait.missesToStop {
            let (r, s) = timed(c) { w.wait(pid: 7) }
            XCTAssertEqual(r, .noAnswer)
            XCTAssertLessThan(s, 0.5)
        }
        for _ in 0..<SystemWait.retryAfter {
            let (r, s) = timed(c) { w.wait(pid: 7) }
            XCTAssertEqual(r, .notAsked, "after \(SystemWait.missesToStop) waits with no answer, gestures should not wait")
            XCTAssertLessThan(s, 0.05)
        }
        XCTAssertEqual(sb.asks, SystemWait.missesToStop)
        // Asked again after retryAfter gestures; one more miss turns it off again.
        XCTAssertEqual(w.wait(pid: 7), .noAnswer)
        XCTAssertEqual(w.wait(pid: 7), .notAsked)
        XCTAssertEqual(sb.asks, SystemWait.missesToStop + 1)
    }

    // Review of #137: after a respring, a new SpringBoard is asked afresh.
    func testANewSpringBoardIsAskedAfresh() {
        let c = FakeClock()
        var answer = false
        var asks = 0
        let w = SystemWait(waitS: 0.1, clock: c.clock) { done in asks += 1; if answer { done(nil) } }
        for _ in 0..<SystemWait.missesToStop { XCTAssertEqual(w.wait(pid: 100), .noAnswer) }
        XCTAssertEqual(w.wait(pid: 100), .notAsked)
        answer = true
        XCTAssertEqual(w.wait(pid: 200), .answered(asks: 1))
        XCTAssertEqual(asks, SystemWait.missesToStop + 1)
    }

    // Review of #137: a SpringBoard that answers, however slowly, is there: not a miss.
    // Issue #228: on the real clock this flaked on a loaded CI runner (an answer due in 50 ms
    // came after the wait's pause had used up its time, and SpringBoard was asked once a wait);
    // on the test's clock each wait asks at 0 and 0.25 s, answered 50 ms later each time.
    func testASlowAnswerIsNotAMiss() {
        let c = FakeClock()
        var asks = 0
        let w = SystemWait(waitS: 0.4, promptS: 0.01, clock: c.clock) { done in
            asks += 1
            c.after(0.05) { done(nil) }
        }
        for _ in 0..<(SystemWait.missesToStop + 1) { XCTAssertEqual(w.wait(), .late) }
        // Asked again after each late answer, and not once the wait's time is up.
        XCTAssertEqual(asks, 2 * (SystemWait.missesToStop + 1), "SpringBoard should still be asked")
    }

    // Issue #228: the agent's own clock (the tests above drive theirs) takes an answer that
    // came before it waits, and gives up on none at its deadline, not before.
    func testTheSystemClockWaitsForAnAnswerUntilItsDeadline() {
        let clock = SystemWait.Clock.system
        let answered = DispatchSemaphore(value: 0)
        answered.signal()
        XCTAssertTrue(clock.wait(answered, clock.now() - 1), "an answer already in is taken, even past the deadline")
        let t0 = clock.now()
        XCTAssertFalse(clock.wait(DispatchSemaphore(value: 0), t0 + 0.05))
        XCTAssertGreaterThanOrEqual(clock.now() - t0, 0.05 - 0.001)
        clock.sleep(0.02)
        XCTAssertGreaterThanOrEqual(clock.now() - t0, 0.07 - 0.001)
    }

    // Issues #137 and #139 (item 3): when SpringBoard is behind, its "touches completed" for one
    // gesture came seconds late, during the next, which was then reported done while still
    // being played. A completion only confirms a record once that record has lifted.
    func testACompletionThatComesBeforeTheLiftDoesNotConfirm() throws {
        let g = try Gestures.swipe(Recorder(), from: CGPoint(x: 201, y: 438), to: CGPoint(x: 0, y: 438), ms: 400)
        XCTAssertEqual(g.liftAt, 0.45, accuracy: 1e-9)
        // SpringBoard's late completion of the previous swipe, 190 ms into this one.
        XCTAssertFalse(Gestures.confirmsEarly(endAt: 0.19, liftAt: g.liftAt))
        XCTAssertFalse(Gestures.confirmsEarly(endAt: -0.5, liftAt: g.liftAt), "one that came before the record started")
    }

    func testAnOnTimeCompletionStillConfirmsFromAnyProcess() throws {
        // The app's or SpringBoard's (right after a launch the app posts none for its first
        // touches: SpringBoard's is the one that comes, ~30 ms after the lift).
        let tap = try Gestures.tap(Recorder(), at: CGPoint(x: 1, y: 1))
        XCTAssertTrue(Gestures.confirmsEarly(endAt: tap.liftAt + 0.03, liftAt: tap.liftAt))
        // testmanagerd plays a short tap faster than its offsets: its completions came 9 ms in.
        XCTAssertTrue(Gestures.confirmsEarly(endAt: 0.009, liftAt: tap.liftAt))
        XCTAssertFalse(Gestures.confirmsEarly(endAt: -0.01, liftAt: tap.liftAt), "one from before the tap was sent")
        let g = try Gestures.swipe(Recorder(), from: CGPoint(x: 201, y: 438), to: CGPoint(x: 0, y: 438), ms: 400, rest: true)
        XCTAssertTrue(Gestures.confirmsEarly(endAt: g.liftAt + 0.03, liftAt: g.liftAt))
    }
}

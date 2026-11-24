import Foundation
import XCTest
@testable import TBAgentCore

final class SystemWaitTests: XCTestCase {
    private final class FakeClock {
        private(set) var now: TimeInterval = 0
        private var due: [(at: TimeInterval, f: () -> Void)] = []

        func after(_ s: TimeInterval, _ f: @escaping () -> Void) { due.append((now + s, f)) }

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

    private func timed(_ clock: FakeClock, _ f: () -> SystemWait.Outcome) -> (SystemWait.Outcome, TimeInterval) {
        let t0 = clock.now
        let r = f()
        return (r, clock.now - t0)
    }

    func testAGestureWaitsForASpringBoardThatIsBehind() {
        let c = FakeClock()
        let sb = FakeSpringBoard(c, delay: 0.6)
        let w = SystemWait(clock: c.clock, ask: sb.ask)
        let (r, s) = timed(c) { w.wait() }
        XCTAssertEqual(r, .answered(asks: 2), "the gesture should wait for SpringBoard's answer")
        XCTAssertGreaterThanOrEqual(s, 0.6 + SystemWait.afterBehindS - 0.05, "the gesture should not be played before SpringBoard answers")
        XCTAssertEqual(sb.asks, 2)
    }

    func testOnlySwipesAndDragsWaitForSpringBoard() throws {
        let rec = Recorder()
        XCTAssertTrue(try Gestures.swipe(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 300, y: 10), ms: 400).waitsForSystem)
        XCTAssertTrue(try Gestures.swipe(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 300, y: 10), ms: 400, rest: true).waitsForSystem)
        XCTAssertTrue(try Gestures.drag(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 10, y: 300)).waitsForSystem)
        XCTAssertFalse(try Gestures.tap(rec, at: CGPoint(x: 1, y: 1)).waitsForSystem)
        XCTAssertFalse(try Gestures.tap(rec, at: CGPoint(x: 1, y: 1), holdMs: 1000).waitsForSystem)
        XCTAssertFalse(try Gestures.taps(rec, at: CGPoint(x: 1, y: 1), count: 2).waitsForSystem)
    }

    func testTheWaitIsCIModes() throws {
        let rec = Recorder()
        let swipe = try Gestures.swipe(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 300, y: 10), ms: 400)
        let drag = try Gestures.drag(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 10, y: 300))
        let tap = try Gestures.tap(rec, at: CGPoint(x: 1, y: 1))
        XCTAssertTrue(Gestures.waitsForSystem(swipe, ci: true))
        XCTAssertTrue(Gestures.waitsForSystem(drag, ci: true))
        XCTAssertFalse(Gestures.waitsForSystem(tap, ci: true))
        XCTAssertFalse(Gestures.waitsForSystem(swipe, ci: false), "a swipe waited for SpringBoard outside CI mode")
        XCTAssertFalse(Gestures.waitsForSystem(drag, ci: false), "a drag waited for SpringBoard outside CI mode")
        XCTAssertFalse(Gestures.waitsForSystem(tap, ci: false))
    }

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
        XCTAssertGreaterThanOrEqual(SystemWait.waitS, 2.3 + 0.5)
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
        XCTAssertEqual(w.wait(pid: 7), .noAnswer)
        XCTAssertEqual(w.wait(pid: 7), .notAsked)
        XCTAssertEqual(sb.asks, SystemWait.missesToStop + 1)
    }

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

    func testASlowAnswerIsNotAMiss() {
        let c = FakeClock()
        var asks = 0
        let w = SystemWait(waitS: 0.4, promptS: 0.01, clock: c.clock) { done in
            asks += 1
            c.after(0.05) { done(nil) }
        }
        for _ in 0..<(SystemWait.missesToStop + 1) { XCTAssertEqual(w.wait(), .late) }
        XCTAssertEqual(asks, 2 * (SystemWait.missesToStop + 1), "SpringBoard should still be asked")
    }

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

    func testACompletionThatComesBeforeTheLiftDoesNotConfirm() throws {
        let g = try Gestures.swipe(Recorder(), from: CGPoint(x: 201, y: 438), to: CGPoint(x: 0, y: 438), ms: 400)
        XCTAssertEqual(g.liftAt, 0.45, accuracy: 1e-9)
        XCTAssertFalse(Gestures.confirmsEarly(endAt: 0.19, liftAt: g.liftAt))
        XCTAssertFalse(Gestures.confirmsEarly(endAt: -0.5, liftAt: g.liftAt), "one that came before the record started")
    }

    func testAnOnTimeCompletionStillConfirmsFromAnyProcess() throws {
        let tap = try Gestures.tap(Recorder(), at: CGPoint(x: 1, y: 1))
        XCTAssertTrue(Gestures.confirmsEarly(endAt: tap.liftAt + 0.03, liftAt: tap.liftAt))
        XCTAssertTrue(Gestures.confirmsEarly(endAt: 0.009, liftAt: tap.liftAt))
        XCTAssertFalse(Gestures.confirmsEarly(endAt: -0.01, liftAt: tap.liftAt), "one from before the tap was sent")
        let g = try Gestures.swipe(Recorder(), from: CGPoint(x: 201, y: 438), to: CGPoint(x: 0, y: 438), ms: 400, rest: true)
        XCTAssertTrue(Gestures.confirmsEarly(endAt: g.liftAt + 0.03, liftAt: g.liftAt))
    }
}

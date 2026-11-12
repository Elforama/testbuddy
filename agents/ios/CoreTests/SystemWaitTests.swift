import Foundation
import XCTest
@testable import TBAgentCore

/// SpringBoard keeping up with a gesture before it is played (issue #137): when SpringBoard is
/// behind, the app's gesture recognizers are held until it catches up, and a swipe that lifts
/// before then does nothing (a pager stays on its page).
final class SystemWaitTests: XCTestCase {
    /// A SpringBoard that is behind for its first `delay` seconds (nil: forever) and answers
    /// "idle" at once after that, counting the asks.
    private final class FakeSpringBoard {
        let until: Date?
        var asks = 0
        init(delay: TimeInterval?) { until = delay.map { Date().addingTimeInterval($0) } }
        func ask(_ done: @escaping (Error?) -> Void) {
            asks += 1
            guard let until else { return }
            DispatchQueue.global().asyncAfter(deadline: .now() + max(0, until.timeIntervalSinceNow)) { done(nil) }
        }
    }

    private func timed(_ f: () -> SystemWait.Outcome) -> (SystemWait.Outcome, TimeInterval) {
        let t0 = Date()
        let r = f()
        return (r, Date().timeIntervalSince(t0))
    }

    // Issue #137: in CI, SpringBoard confirmed a pager swipe's touches 1.8 s after the app did
    // (on time for every other gesture), the swipe lifted while the app's pan was still held,
    // and the page did not turn. The swipe has to wait for SpringBoard first.
    func testAGestureWaitsForASpringBoardThatIsBehind() {
        let sb = FakeSpringBoard(delay: 0.6)
        let w = SystemWait(ask: sb.ask)
        let (r, s) = timed { w.wait() }
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
        var asks = 0
        let w = SystemWait(waitS: 2, promptS: 0.1) { done in
            asks += 1
            DispatchQueue.global().asyncAfter(deadline: .now() + (asks < 3 ? 0.3 : 0)) { done(nil) }
        }
        let (r, s) = timed { w.wait() }
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
        let sb = FakeSpringBoard(delay: 0)
        let w = SystemWait(ask: sb.ask)
        let (r, s) = timed { w.wait() }
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
        let w = SystemWait { _ in throw NoProcess() }
        let (r, s) = timed { w.wait() }
        XCTAssertEqual(r, .notAsked)
        XCTAssertLessThan(s, 0.05)
    }

    func testTheWaitIsBoundedAndPausesAskingASpringBoardThatNeverAnswers() {
        let sb = FakeSpringBoard(delay: nil)
        let w = SystemWait(waitS: 0.1, ask: sb.ask)
        for _ in 0..<SystemWait.missesToStop {
            let (r, s) = timed { w.wait(pid: 7) }
            XCTAssertEqual(r, .noAnswer)
            XCTAssertLessThan(s, 0.5)
        }
        for _ in 0..<SystemWait.retryAfter {
            let (r, s) = timed { w.wait(pid: 7) }
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
        var answer = false
        var asks = 0
        let w = SystemWait(waitS: 0.1) { done in asks += 1; if answer { done(nil) } }
        for _ in 0..<SystemWait.missesToStop { XCTAssertEqual(w.wait(pid: 100), .noAnswer) }
        XCTAssertEqual(w.wait(pid: 100), .notAsked)
        answer = true
        XCTAssertEqual(w.wait(pid: 200), .answered(asks: 1))
        XCTAssertEqual(asks, SystemWait.missesToStop + 1)
    }

    // Review of #137: a SpringBoard that answers, however slowly, is there: not a miss.
    func testASlowAnswerIsNotAMiss() {
        var asks = 0
        let w = SystemWait(waitS: 0.4, promptS: 0.01) { done in
            asks += 1
            DispatchQueue.global().asyncAfter(deadline: .now() + 0.05) { done(nil) }
        }
        for _ in 0..<(SystemWait.missesToStop + 1) { XCTAssertEqual(w.wait(), .late) }
        XCTAssertGreaterThan(asks, SystemWait.missesToStop + 1, "SpringBoard should still be asked")
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

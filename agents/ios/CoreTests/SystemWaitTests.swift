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
        func ask(_ done: @escaping () -> Void) {
            asks += 1
            guard let until else { return }
            DispatchQueue.global().asyncAfter(deadline: .now() + max(0, until.timeIntervalSinceNow)) { done() }
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
        XCTAssertEqual(r, .answered, "the gesture should wait for SpringBoard's answer")
        XCTAssertGreaterThanOrEqual(s, 0.55, "the gesture should not be played before SpringBoard answers")
        // Its first answer was late: asked again, after a pause, to see it keep up.
        XCTAssertEqual(sb.asks, 2)
        XCTAssertGreaterThanOrEqual(s, 0.6 + SystemWait.afterBehindS - 0.05)
    }

    // Measured on iOS 27 with SpringBoard stopped 2 s of every 8 s: a swipe played 30 ms after
    // its late "idle" answer was still held for 765 ms (it had not caught up on touches).
    func testALateAnswerIsNotEnoughSpringBoardMustAnswerPromptly() {
        var asks = 0
        let w = SystemWait(waitS: 2, promptS: 0.1) { done in
            asks += 1
            // Behind on the first two asks, prompt on the third.
            DispatchQueue.global().asyncAfter(deadline: .now() + (asks < 3 ? 0.3 : 0)) { done() }
        }
        let (r, s) = timed { w.wait() }
        XCTAssertEqual(r, .answered)
        XCTAssertEqual(asks, 3)
        XCTAssertGreaterThanOrEqual(s, 2 * (0.3 + SystemWait.afterBehindS) - 0.05)
    }

    func testASpringBoardThatStaysBehindRunsTheWaitOut() {
        let w = SystemWait(waitS: 0.5, promptS: 0.05) { done in
            DispatchQueue.global().asyncAfter(deadline: .now() + 0.1) { done() }
        }
        let (r, s) = timed { w.wait() }
        XCTAssertEqual(r, .ranOut)
        XCTAssertLessThan(s, 0.5 + SystemWait.afterBehindS + 0.15)
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
        XCTAssertEqual(r, .answered)
        XCTAssertLessThan(s, 0.2)
    }

    func testTheWaitIsBoundedAndStopsAskingASpringBoardThatNeverAnswers() {
        let sb = FakeSpringBoard(delay: nil)
        let w = SystemWait(waitS: 0.1, ask: sb.ask)
        for _ in 0..<SystemWait.missesToStop {
            let (r, s) = timed { w.wait() }
            XCTAssertEqual(r, .ranOut)
            XCTAssertLessThan(s, 0.5)
        }
        let (r, s) = timed { w.wait() }
        XCTAssertEqual(r, .notAsked, "after \(SystemWait.missesToStop) waits that ran out, gestures should not wait any more")
        XCTAssertLessThan(s, 0.05)
        XCTAssertEqual(sb.asks, SystemWait.missesToStop)
    }

    func testAnAnswerResetsTheMisses() {
        var answer = false
        var asks = 0
        let w = SystemWait(waitS: 0.1) { done in
            asks += 1
            if answer { done() }
        }
        for _ in 0..<(SystemWait.missesToStop - 1) { XCTAssertEqual(w.wait(), .ranOut) }
        answer = true
        XCTAssertEqual(w.wait(), .answered)
        answer = false
        for _ in 0..<(SystemWait.missesToStop - 1) { XCTAssertEqual(w.wait(), .ranOut) }
        XCTAssertEqual(asks, 2 * SystemWait.missesToStop - 1)
    }

    func testAnOpWithItsOwnDeadlineWaitsLessAndDoesNotCountAMiss() {
        let sb = FakeSpringBoard(delay: nil)
        let w = SystemWait(waitS: 0.3, ask: sb.ask)
        for _ in 0..<(SystemWait.missesToStop + 1) {
            let (r, s) = timed { w.wait(atMost: 0.05) }
            XCTAssertEqual(r, .ranOut)
            XCTAssertLessThan(s, 0.2)
        }
        XCTAssertEqual(sb.asks, SystemWait.missesToStop + 1, "short waits should not stop the asking")
    }

    func testASpringBoardThatCannotBeAskedIsNotWaitedFor() {
        struct NoProcess: Error {}
        let w = SystemWait { _ in throw NoProcess() }
        let (r, s) = timed { w.wait() }
        XCTAssertEqual(r, .notAsked)
        XCTAssertLessThan(s, 0.05)
    }

    // Issues #137 and #139 (item 3): SpringBoard's "touches completed" for a gesture it was
    // behind on came seconds late, and confirmed the next gesture while that one was still
    // playing. With another app in front, only that app's confirms a gesture early.
    func testOnlyTheAppInFrontConfirmsAGestureEarly() {
        let sb: Int32 = 1513, app: Int32 = 45720, other: Int32 = 777
        XCTAssertFalse(Gestures.confirmsEarly(pid: sb, springboard: sb, foreground: app), "SpringBoard behind the app")
        XCTAssertTrue(Gestures.confirmsEarly(pid: app, springboard: sb, foreground: app))
        XCTAssertTrue(Gestures.confirmsEarly(pid: sb, springboard: sb, foreground: sb), "SpringBoard in front (an alert, the home screen)")
        XCTAssertTrue(Gestures.confirmsEarly(pid: sb, springboard: sb, foreground: 0), "the foreground is not known")
        XCTAssertTrue(Gestures.confirmsEarly(pid: other, springboard: sb, foreground: app), "another process (a view service in front of the app)")
        XCTAssertTrue(Gestures.confirmsEarly(pid: sb, springboard: 0, foreground: app), "SpringBoard is not known")
    }
}

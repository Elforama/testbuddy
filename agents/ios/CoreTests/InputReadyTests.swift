import Foundation
import TBTouchGuard
import XCTest
@testable import TBAgentCore

/// Issue #290: after launchApp without stopping the app (or any return from the background),
/// the app is listed in front, tree and all, while SpringBoard still animates its window in,
/// scaled. A tap sent then reached the app at the point under the scaled window, ~30 pt above
/// the target: it took the Back button instead of the counter. Input to a probed app now waits
/// for the app to become active, on the probe's notification.
final class InputReadyTests: XCTestCase {
    /// A clock the fake wait moves: each check after an event is `stepMs` later.
    private final class Clock {
        var ns: UInt64 = 1_000_000_000
        func advance(ms: Int) { ns += UInt64(ms) * 1_000_000 }
    }

    /// Events.awaitCondition on a script: the phase after each event, `stepMs` apart.
    private func wait(_ clock: Clock, phases: [AppPhase], current: UnsafeMutablePointer<AppPhase>, stepMs: Int,
                      asked: UnsafeMutablePointer<Int>) -> (Int, () -> Bool) -> Bool {
        { timeoutMs, check in
            asked.pointee = timeoutMs
            var elapsed = 0
            if check() { return true }
            for p in phases {
                if elapsed + stepMs > timeoutMs { clock.advance(ms: timeoutMs - elapsed); return false }
                elapsed += stepMs
                clock.advance(ms: stepMs)
                current.pointee = p
                if check() { return true }
            }
            clock.advance(ms: timeoutMs - elapsed)
            return false
        }
    }

    private func run(start: AppPhase, then phases: [AppPhase], stepMs: Int = 100, leftMs: Int? = nil) -> (InputReady.Outcome, asked: Int) {
        let clock = Clock()
        let current = UnsafeMutablePointer<AppPhase>.allocate(capacity: 1)
        let asked = UnsafeMutablePointer<Int>.allocate(capacity: 1)
        defer { current.deallocate(); asked.deallocate() }
        current.pointee = start
        asked.pointee = -1
        let out = InputReady.await(phase: { current.pointee }, leftMs: leftMs, now: { clock.ns },
                                   wait: wait(clock, phases: phases, current: current, stepMs: stepMs, asked: asked))
        return (out, asked.pointee)
    }

    /// The bug: the app was entering (the opening animation still running) when the tap went.
    /// The input waits until the probe says it is active, and no longer.
    func testInputWaitsForAnEnteringAppToBecomeActive() {
        let (out, asked) = run(start: .entering, then: [.entering, .active], stepMs: 120)
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 240, ready: true, from: .entering))
        XCTAssertEqual(asked, InputReady.capMs)
    }

    /// An active app (the usual case), an app without the probe, or one behind an alert: no
    /// wait at all. Inactive is not waited on: it stays so until someone deals with what is in
    /// front, and the touch is for that.
    func testOnlyAnAppOnItsWayInHoldsInput() {
        for phase in [AppPhase.active, .unknown, .inactive] {
            let (out, asked) = run(start: phase, then: [.active])
            XCTAssertEqual(out, InputReady.Outcome(waitedMs: 0, ready: true, from: phase), "\(phase)")
            XCTAssertEqual(asked, -1, "\(phase): no wait")
        }
    }

    /// Seen on the simulator: the app is listed in front before UIKit tells it it is coming
    /// back, so its probe still says background. That is on its way in too.
    func testAnAppListedInFrontStillInTheBackgroundIsOnItsWayIn() {
        let (out, _) = run(start: .background, then: [.entering, .active], stepMs: 50)
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 100, ready: true, from: .background))
    }

    /// Something else came in front while it was entering (a permission alert at launch): the
    /// wait ends there.
    func testAResignWhileEnteringEndsTheWait() {
        let (out, _) = run(start: .entering, then: [.inactive])
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 100, ready: true, from: .entering))
    }

    /// An app that never finishes entering: the input goes at the cap, saying so (ready false),
    /// not later; and never past the op's own deadline.
    func testTheWaitIsBoundedByTheCapAndTheOpsDeadline() {
        let (capped, asked) = run(start: .entering, then: Array(repeating: .entering, count: 100))
        XCTAssertEqual(capped, InputReady.Outcome(waitedMs: InputReady.capMs, ready: false, from: .entering))
        XCTAssertEqual(asked, InputReady.capMs)

        let (byDeadline, askedByDeadline) = run(start: .entering, then: Array(repeating: .entering, count: 100), leftMs: 450)
        XCTAssertEqual(byDeadline, InputReady.Outcome(waitedMs: 450, ready: false, from: .entering))
        XCTAssertEqual(askedByDeadline, 450)

        let (noTime, askedNoTime) = run(start: .entering, then: [.active], leftMs: 0)
        XCTAssertEqual(noTime, InputReady.Outcome(waitedMs: 0, ready: false, from: .entering))
        XCTAssertEqual(askedNoTime, -1)
    }

    /// The probe's side (TBTouchGuard.h): the phase each UIApplication notification leads to.
    func testProbePhases() {
        // A process starts entering; its first activation ends the opening animation.
        XCTAssertEqual(tbNextPhase(TBPhaseEntering, TBLifeDidBecomeActive), TBPhaseActive)
        // Home: resign, then background.
        XCTAssertEqual(tbNextPhase(TBPhaseActive, TBLifeWillResignActive), TBPhaseInactive)
        XCTAssertEqual(tbNextPhase(TBPhaseInactive, TBLifeDidEnterBackground), TBPhaseBackground)
        // The return (issue #290): entering until it is active again.
        XCTAssertEqual(tbNextPhase(TBPhaseBackground, TBLifeWillEnterForeground), TBPhaseEntering)
        XCTAssertEqual(tbNextPhase(TBPhaseEntering, TBLifeDidBecomeActive), TBPhaseActive)
        // An alert over an app on its way in: no longer entering.
        XCTAssertEqual(tbNextPhase(TBPhaseEntering, TBLifeWillResignActive), TBPhaseInactive)
        // A resign that comes after the background (never seen, but harmless) keeps background.
        XCTAssertEqual(tbNextPhase(TBPhaseBackground, TBLifeWillResignActive), TBPhaseBackground)
        // The values the agent reads are the probe's.
        for (c, s) in [(TBPhaseUnknown, AppPhase.unknown), (TBPhaseActive, .active), (TBPhaseEntering, .entering),
                       (TBPhaseInactive, .inactive), (TBPhaseBackground, .background)] {
            XCTAssertEqual(AppPhase(state: UInt64(c.rawValue)), s)
        }
        XCTAssertEqual(AppPhase(state: 99), .unknown)
    }
}

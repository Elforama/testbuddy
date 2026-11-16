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
    /// A clock the fake wait moves.
    private final class Clock {
        var ns: UInt64 = 1_000_000_000
        func advance(ms: Int) { ns += UInt64(ms) * 1_000_000 }
    }

    /// The app as the agent sees it: its probe's phase, and whether it is the app in front.
    private final class App {
        var phase: AppPhase
        var inFront = true
        init(_ phase: AppPhase) { self.phase = phase }
    }

    private struct Step {
        let phase: AppPhase
        let inFront: Bool
        init(_ phase: AppPhase, inFront: Bool = true) { self.phase = phase; self.inFront = inFront }
    }

    /// Events.awaitCondition on a script: the app after each event, `stepMs` apart.
    private func wait(_ clock: Clock, _ app: App, _ steps: [Step], stepMs: Int, asked: @escaping (Int) -> Void) -> (Int, () -> Bool) -> Bool {
        { timeoutMs, check in
            asked(timeoutMs)
            var elapsed = 0
            if check() { return true }
            for s in steps {
                if elapsed + stepMs > timeoutMs { clock.advance(ms: timeoutMs - elapsed); return false }
                elapsed += stepMs
                clock.advance(ms: stepMs)
                app.phase = s.phase
                app.inFront = s.inFront
                if check() { return true }
            }
            clock.advance(ms: timeoutMs - elapsed)
            return false
        }
    }

    private func run(start: AppPhase, then steps: [Step], stepMs: Int = 100, leftMs: Int? = nil) -> (InputReady.Outcome, asked: Int) {
        let clock = Clock()
        let app = App(start)
        var asked = -1
        let out = InputReady.await(phase: { app.phase }, inFront: { app.inFront }, leftMs: leftMs, now: { clock.ns },
                                   wait: wait(clock, app, steps, stepMs: stepMs, asked: { asked = $0 }))
        return (out, asked)
    }

    private func steps(_ phases: AppPhase...) -> [Step] { phases.map { Step($0) } }

    /// The bug: the app was entering (the opening animation still running) when the tap went.
    /// The input waits until the probe says it is active, and no longer.
    func testInputWaitsForAnEnteringAppToBecomeActive() {
        let (out, asked) = run(start: .entering, then: steps(.entering, .active), stepMs: 120)
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 240, ready: true, from: .entering, end: .active))
        XCTAssertEqual(asked, InputReady.capMs)
    }

    /// An active app (the usual case), an app without the probe, or an inactive one: no wait
    /// at all. Inactive is not waited on: it stays so until someone deals with what is in
    /// front, and the touch is for that.
    func testOnlyAnAppOnItsWayInHoldsInput() {
        for phase in [AppPhase.active, .unknown, .inactive] {
            let (out, asked) = run(start: phase, then: steps(.active))
            XCTAssertEqual(out, InputReady.Outcome(waitedMs: 0, ready: true, from: phase, end: .none), "\(phase)")
            XCTAssertEqual(asked, -1, "\(phase): no wait")
        }
    }

    /// Seen on the simulator: the app is listed in front before UIKit tells it it is coming
    /// back, so its probe still says background. That is on its way in too.
    func testAnAppListedInFrontStillInTheBackgroundIsOnItsWayIn() {
        let (out, _) = run(start: .background, then: steps(.entering, .active), stepMs: 50)
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 100, ready: true, from: .background, end: .active))
    }

    /// Issue #290 review: an app launched under a system alert never becomes active, and UIKit
    /// posts no "will resign active" to an app that never was: it stays entering. What ends the
    /// wait is SpringBoard in front (its alert). An app not in front is never waited on at all.
    func testAnAppNoLongerInFrontEndsTheWait() {
        let (out, _) = run(start: .entering, then: [Step(.entering), Step(.entering, inFront: false)])
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 200, ready: true, from: .entering, end: .leftFront))

        let clock = Clock()
        var waited = false
        let none = InputReady.await(phase: { .entering }, inFront: { false }, leftMs: nil, now: { clock.ns },
                                    wait: { _, _ in waited = true; return false })
        XCTAssertEqual(none, InputReady.Outcome(waitedMs: 0, ready: true, from: .entering, end: .none))
        XCTAssertFalse(waited)
    }

    /// An app that never finishes entering under something SpringBoard does not list in front:
    /// the input goes at the cap, saying so (ready false, end timedOut, for the trace), not
    /// later; and never past the op's own deadline.
    func testTheWaitIsBoundedByTheCapAndTheOpsDeadline() {
        let stuck = Array(repeating: Step(.entering), count: 100)
        let (capped, asked) = run(start: .entering, then: stuck)
        XCTAssertEqual(capped, InputReady.Outcome(waitedMs: InputReady.capMs, ready: false, from: .entering, end: .timedOut))
        XCTAssertEqual(asked, InputReady.capMs)

        let (byDeadline, askedByDeadline) = run(start: .entering, then: stuck, leftMs: 450)
        XCTAssertEqual(byDeadline, InputReady.Outcome(waitedMs: 450, ready: false, from: .entering, end: .timedOut))
        XCTAssertEqual(askedByDeadline, 450)

        let (noTime, askedNoTime) = run(start: .entering, then: steps(.active), leftMs: 0)
        XCTAssertEqual(noTime, InputReady.Outcome(waitedMs: 0, ready: false, from: .entering, end: .timedOut))
        XCTAssertEqual(askedNoTime, -1)
    }

    /// Issue #290 review: on a cold launch the agent often has not heard the probe's hello when
    /// the app's window appears, so a hold keyed on "probed" never held, and the launch's first
    /// tree was read mid-animation. The hold reads only the phase (the probe sets it as it
    /// loads, before the app has a window): the read waits until the app is active.
    func testALaunchsFirstReadWaitsForTheAppToBecomeActive() {
        let ms: UInt64 = 1_000_000
        let t0: UInt64 = 5_000 * ms
        let hold = LaunchHold(now: t0, timeoutMs: 30_000)
        // Not yet in front (SpringBoard still listed): no hold; the read itself waits for the window.
        XCTAssertFalse(hold.holds(phase: .entering, inFront: false, now: t0))
        XCTAssertNil(hold.heldMs(now: t0))
        // In front, entering: held, and looked at again by the cap at the latest.
        XCTAssertTrue(hold.holds(phase: .entering, inFront: true, now: t0 + 40 * ms))
        XCTAssertEqual(hold.recheckMs(now: t0 + 40 * ms), InputReady.capMs - 40)
        XCTAssertTrue(hold.holds(phase: .entering, inFront: true, now: t0 + 150 * ms))
        // Active: the read goes.
        XCTAssertFalse(hold.holds(phase: .active, inFront: true, now: t0 + 290 * ms))
        XCTAssertEqual(hold.heldMs(now: t0 + 400 * ms), 250)
        XCTAssertFalse(hold.timedOut)
        XCTAssertEqual(hold.recheckMs(now: t0 + 400 * ms), 0)
        // Once ended, never again (a later return to the background is not this launch's).
        XCTAssertFalse(hold.holds(phase: .entering, inFront: true, now: t0 + 500 * ms))

        // An app with no probe, or one already active: never held.
        for phase in [AppPhase.unknown, .active, .inactive] {
            let h = LaunchHold(now: t0, timeoutMs: 30_000)
            XCTAssertFalse(h.holds(phase: phase, inFront: true, now: t0), "\(phase)")
            XCTAssertNil(h.heldMs(now: t0))
        }
    }

    /// The launch hold ends at the cap (or the launch's own timeout), saying it timed out, and
    /// when an alert takes the front.
    func testTheLaunchHoldIsBounded() {
        let ms: UInt64 = 1_000_000
        let t0: UInt64 = 5_000 * ms
        let capped = LaunchHold(now: t0, timeoutMs: 30_000)
        XCTAssertTrue(capped.holds(phase: .entering, inFront: true, now: t0))
        XCTAssertFalse(capped.holds(phase: .entering, inFront: true, now: t0 + UInt64(InputReady.capMs) * ms))
        XCTAssertTrue(capped.timedOut)
        XCTAssertEqual(capped.heldMs(now: t0 + 9_000 * ms), InputReady.capMs)

        let short = LaunchHold(now: t0, timeoutMs: 800)
        XCTAssertTrue(short.holds(phase: .entering, inFront: true, now: t0 + 799 * ms))
        XCTAssertFalse(short.holds(phase: .entering, inFront: true, now: t0 + 800 * ms))
        XCTAssertTrue(short.timedOut)

        let alert = LaunchHold(now: t0, timeoutMs: 30_000)
        XCTAssertTrue(alert.holds(phase: .entering, inFront: true, now: t0))
        XCTAssertFalse(alert.holds(phase: .entering, inFront: false, now: t0 + 100 * ms))
        XCTAssertFalse(alert.timedOut)
        XCTAssertEqual(alert.heldMs(now: t0 + 200 * ms), 100)
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
        // A resign while entering, should UIKit post one, leaves it inactive (not waited on).
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

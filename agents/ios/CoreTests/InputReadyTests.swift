import Foundation
import TBTouchGuard
import XCTest
@testable import TBAgentCore

final class InputReadyTests: XCTestCase {
    private final class Clock {
        var ns: UInt64 = 1_000_000_000
        func advance(ms: Int) { ns += UInt64(ms) * 1_000_000 }
    }

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

    func testInputWaitsForAnEnteringAppToBecomeActive() {
        let (out, asked) = run(start: .entering, then: steps(.entering, .active), stepMs: 120)
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 240, ready: true, from: .entering, end: .active))
        XCTAssertEqual(asked, InputReady.capMs)
    }

    func testOnlyAnAppOnItsWayInHoldsInput() {
        for phase in [AppPhase.active, .unknown, .inactive] {
            let (out, asked) = run(start: phase, then: steps(.active))
            XCTAssertEqual(out, InputReady.Outcome(waitedMs: 0, ready: true, from: phase, end: .none), "\(phase)")
            XCTAssertEqual(asked, -1, "\(phase): no wait")
        }
    }

    func testAnAppListedInFrontStillInTheBackgroundIsOnItsWayIn() {
        let (out, _) = run(start: .background, then: steps(.entering, .active), stepMs: 50)
        XCTAssertEqual(out, InputReady.Outcome(waitedMs: 100, ready: true, from: .background, end: .active))
    }

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

    func testALaunchsFirstReadWaitsForTheAppToBecomeActive() {
        let ms: UInt64 = 1_000_000
        let t0: UInt64 = 5_000 * ms
        let hold = LaunchHold(now: t0, timeoutMs: 30_000)
        XCTAssertFalse(hold.holds(phase: .entering, inFront: false, now: t0))
        XCTAssertNil(hold.heldMs(now: t0))
        XCTAssertTrue(hold.holds(phase: .entering, inFront: true, now: t0 + 40 * ms))
        XCTAssertEqual(hold.recheckMs(now: t0 + 40 * ms), InputReady.capMs - 40)
        XCTAssertTrue(hold.holds(phase: .entering, inFront: true, now: t0 + 150 * ms))
        XCTAssertFalse(hold.holds(phase: .active, inFront: true, now: t0 + 290 * ms))
        XCTAssertEqual(hold.heldMs(now: t0 + 400 * ms), 250)
        XCTAssertFalse(hold.timedOut)
        XCTAssertEqual(hold.recheckMs(now: t0 + 400 * ms), 0)
        XCTAssertFalse(hold.holds(phase: .entering, inFront: true, now: t0 + 500 * ms))

        for phase in [AppPhase.unknown, .active, .inactive] {
            let h = LaunchHold(now: t0, timeoutMs: 30_000)
            XCTAssertFalse(h.holds(phase: phase, inFront: true, now: t0), "\(phase)")
            XCTAssertNil(h.heldMs(now: t0))
        }
    }

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

    func testProbePhases() {
        XCTAssertEqual(tbNextPhase(TBPhaseEntering, TBLifeDidBecomeActive), TBPhaseActive)
        XCTAssertEqual(tbNextPhase(TBPhaseActive, TBLifeWillResignActive), TBPhaseInactive)
        XCTAssertEqual(tbNextPhase(TBPhaseInactive, TBLifeDidEnterBackground), TBPhaseBackground)
        XCTAssertEqual(tbNextPhase(TBPhaseBackground, TBLifeWillEnterForeground), TBPhaseEntering)
        XCTAssertEqual(tbNextPhase(TBPhaseEntering, TBLifeDidBecomeActive), TBPhaseActive)
        XCTAssertEqual(tbNextPhase(TBPhaseEntering, TBLifeWillResignActive), TBPhaseInactive)
        XCTAssertEqual(tbNextPhase(TBPhaseBackground, TBLifeWillResignActive), TBPhaseBackground)
        for (c, s) in [(TBPhaseUnknown, AppPhase.unknown), (TBPhaseActive, .active), (TBPhaseEntering, .entering),
                       (TBPhaseInactive, .inactive), (TBPhaseBackground, .background)] {
            XCTAssertEqual(AppPhase(state: UInt64(c.rawValue)), s)
        }
        XCTAssertEqual(AppPhase(state: 99), .unknown)
    }
}

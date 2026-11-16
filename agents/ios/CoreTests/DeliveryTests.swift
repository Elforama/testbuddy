import Foundation
import XCTest
@testable import TBAgentCore

/// Issue #334: a tap went to an app while UIKit was presenting a full-screen cover. The system
/// (pid 1716 in the CI log) reported the touches completed; the app never did, and went on
/// (its run loop idle 106 ms later, the cover's ViewDidAppear 150 ms later) without them. The
/// agent counted "touches completed" from any process, so the tap was reported done. Now it
/// tells, per process, whether the app in front got a record's touches. Times are seconds from
/// the record's start; the record lifts at 0.01 s.
final class DeliveryTests: XCTestCase {
    let app: Int32 = 49175, system: Int32 = 1716
    let lift = 0.01

    func seen(_ pid: Int32, _ at: TimeInterval, _ kind: Delivery.Kind) -> Delivery.Seen { .init(pid: pid, at: at, kind: kind) }

    // The CI case: the system's "touches completed", then the app idle and posting, never its own.
    func testTheAppRanOnWithoutTheTouches() {
        let s = [seen(system, 0.023, .inputEnd), seen(app, 0.129, .idle), seen(app, 0.173, .other)]
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.13), .notDelivered)
    }

    // Every other tap of that run: the app's own "touches completed" came too.
    func testTheAppReportedTheTouches() {
        for s in [[seen(system, 0.022, .inputEnd), seen(app, 0.054, .inputEnd)],
                  [seen(app, 0.066, .inputEnd), seen(system, 0.066, .inputEnd)],
                  [seen(system, 0.022, .inputEnd), seen(app, 0.150, .idle), seen(app, 0.160, .inputEnd)]] {
            XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.2), .delivered)
        }
    }

    // A stalled app posts nothing until it runs again, and then its touches come first (in the
    // run, one tap's came 1.85 s after the system's): no verdict but unknown, never notDelivered.
    func testAStalledAppIsNotJudged() {
        let s = [seen(system, 0.024, .inputEnd)]
        XCTAssertNil(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.3))
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.024 + Delivery.waitS), .unknown)
    }

    // What the app posted just after the system's report may have been on its way before the
    // touch reached it: only what comes graceS later counts.
    func testActivityWithinTheGraceProvesNothing() {
        let s = [seen(system, 0.023, .inputEnd), seen(app, 0.05, .idle), seen(app, 0.06, .other)]
        XCTAssertNil(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.1))
    }

    // Other activity (a commit, a view appearing) can come from the end of a long frame, with
    // the touch handled the next turn: proof only once lateS has passed with still no report.
    func testOtherActivityNeedsTheLateWait() {
        var s = [seen(system, 0.023, .inputEnd), seen(app, 0.15, .other)]
        XCTAssertNil(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.16))
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.15 + Delivery.lateS), .notDelivered)
        s.append(seen(app, 0.17, .inputEnd))
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.15 + Delivery.lateS), .delivered)
    }

    // A double tap: the first contact's "touches completed" is no proof the second one came.
    func testOnlyReportsFromTheLastLiftCount() {
        let dt = 0.3 // the second contact lifts 0.3 s in
        let s = [seen(app, 0.12, .inputEnd), seen(system, 0.31, .inputEnd), seen(app, 0.45, .idle)]
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: dt, seen: s, now: 0.46), .notDelivered)
    }

    // The app in front is the system's own (SpringBoard): its report is the app's.
    func testTheAppInFrontIsTheSystem() {
        let s = [seen(system, 0.023, .inputEnd), seen(system, 0.2, .idle)]
        XCTAssertEqual(Delivery.verdict(app: system, liftAt: lift, seen: s, now: 0.3), .delivered)
    }

    // An unknown app: no verdict.
    func testNoAppIsUnknown() {
        XCTAssertEqual(Delivery.verdict(app: 0, liftAt: lift, seen: [seen(system, 0.02, .inputEnd)], now: 0.1), .unknown)
    }
}

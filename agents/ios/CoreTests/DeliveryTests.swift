import Foundation
import XCTest
@testable import TBAgentCore

/// Issue #334: a tap went to an app while UIKit was presenting a full-screen cover. The system
/// (pid 1716 in the CI log) reported the touches completed; the app never did, and went on
/// without them. The agent counted "touches completed" from any process, so the tap was
/// reported done. Now it tells, per process, whether the app in front got a record's touches,
/// and says "not delivered" only on proof. Times are seconds from the record's start; the
/// record lifts at 0.01 s.
///
/// Every timeline is judged at each moment it could be (every 5 ms, each entry seen only from
/// its time on), not only once whole: an agent asked mid-way must not answer "not delivered"
/// for a tap whose report is still coming (review of b846c56).
final class DeliveryTests: XCTestCase {
    let app: Int32 = 49175, system: Int32 = 1716
    let lift = 0.01

    func seen(_ pid: Int32, _ at: TimeInterval, _ kind: Delivery.Kind) -> Delivery.Seen { .init(pid: pid, at: at, kind: kind) }

    /// The verdicts at every moment from 0 to end: what had been seen by then.
    func verdicts(_ timeline: [Delivery.Seen], app: Int32? = nil, liftAt: TimeInterval? = nil, to end: TimeInterval)
        -> [(now: TimeInterval, v: Delivery.Verdict?)] {
        var out: [(TimeInterval, Delivery.Verdict?)] = []
        var now = 0.0
        while now <= end + 1e-9 {
            let past = timeline.filter { $0.at <= now + 1e-9 }
            out.append((now, Delivery.verdict(app: app ?? self.app, liftAt: liftAt ?? lift, seen: past, now: now)))
            now += 0.005
        }
        return out
    }

    /// Never "not delivered" at any moment.
    func assertNeverNotDelivered(_ timeline: [Delivery.Seen], to end: TimeInterval, file: StaticString = #filePath, line: UInt = #line) {
        for (now, v) in verdicts(timeline, to: end) where v == .notDelivered {
            XCTFail("not delivered at \(now) s", file: file, line: line)
            return
        }
    }

    // The CI case, with the agent's own idle asks: the system's "touches completed", then the
    // app idle past the margin, and idle again a quiet window later, never its own report.
    // "Not delivered" only from that second idle on.
    func testTheAppRanOnWithoutTheTouches() {
        let proof = 0.30 + Delivery.quietS + 0.02
        let s = [seen(system, 0.023, .inputEnd), seen(app, 0.129, .idle), seen(app, 0.30, .idle), seen(app, proof, .idle)]
        for (now, v) in verdicts(s, to: proof + 0.1) {
            XCTAssertEqual(v, now >= proof - 1e-9 ? .notDelivered : nil, "at \(now) s")
        }
    }

    // The app's own report, whenever it comes before the proof, is delivery.
    func testTheAppReportedTheTouches() {
        for s in [[seen(system, 0.022, .inputEnd), seen(app, 0.054, .inputEnd)],
                  [seen(app, 0.066, .inputEnd), seen(system, 0.066, .inputEnd)]] {
            assertNeverNotDelivered(s, to: 1)
            XCTAssertEqual(verdicts(s, to: 0.1).last?.v, .delivered)
        }
    }

    // Idle, then the report late (reviewer's timeline, and one just inside the quiet window):
    // never "not delivered" at any moment.
    func testIdleThenALateReport() {
        for s in [[seen(system, 0.022, .inputEnd), seen(app, 0.150, .idle), seen(app, 0.160, .inputEnd)],
                  [seen(system, 0.022, .inputEnd), seen(app, 0.30, .idle), seen(app, 0.30 + Delivery.quietS - 0.01, .inputEnd),
                   seen(app, 0.30 + Delivery.quietS + 0.01, .idle)]] {
            assertNeverNotDelivered(s, to: 1)
            XCTAssertEqual(verdicts(s, to: 1).last?.v, .delivered)
        }
    }

    // The app's report lags behind the system's (a loaded runner): idles before the margin are
    // no proof, and one past it needs the quiet window, in which the report comes.
    func testTheAppsReportLags() {
        let s = [seen(system, 0.02, .inputEnd), seen(app, 0.05, .idle), seen(app, 0.2, .idle),
                 seen(app, 0.28, .idle), seen(app, 0.5, .inputEnd), seen(app, 0.7, .idle)]
        assertNeverNotDelivered(s, to: 1)
        XCTAssertEqual(verdicts(s, to: 1).last?.v, .delivered)
    }

    // Other posts (a commit the probe reports on another channel, a view appearing) are no
    // proof, however late: a commit can come just before a stall, the touch handled after it.
    func testOtherPostsAreNoProof() {
        let s = [seen(system, 0.02, .inputEnd), seen(app, 0.3, .other), seen(app, 0.7, .other), seen(app, 1.2, .other)]
        assertNeverNotDelivered(s, to: 3)
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.02 + Delivery.waitS), .unknown)
    }

    // A commit, then a main-thread stall: the app posts nothing until it runs again, then
    // reports its touches (idle and report within a few ms, in either order).
    func testAStallAfterACommit() {
        for s in [[seen(system, 0.02, .inputEnd), seen(app, 0.05, .other), seen(app, 1.200, .idle), seen(app, 1.205, .inputEnd)],
                  [seen(system, 0.02, .inputEnd), seen(app, 0.05, .other), seen(app, 1.200, .inputEnd), seen(app, 1.205, .idle)]] {
            assertNeverNotDelivered(s, to: 2)
            XCTAssertEqual(verdicts(s, to: 1.3).last?.v, .delivered)
        }
    }

    // A stalled app posts nothing: no verdict, then unknown.
    func testAStalledAppIsNotJudged() {
        let s = [seen(system, 0.024, .inputEnd)]
        assertNeverNotDelivered(s, to: 3)
        XCTAssertNil(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.3))
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: s, now: 0.024 + Delivery.waitS), .unknown)
    }

    // No report from the system either: unknown soon, not after the whole wait.
    func testNoSystemReportIsUnknownSoon() {
        XCTAssertNil(Delivery.verdict(app: app, liftAt: lift, seen: [], now: 0.1))
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: lift, seen: [], now: lift + Delivery.noSystemS), .unknown)
    }

    // A double tap: the first contact's report is no proof the second one came.
    func testOnlyReportsFromTheLastLiftCount() {
        let dt = 0.3 // the second contact lifts 0.3 s in
        let p = 0.31 + Delivery.marginS + 0.01
        let s = [seen(app, 0.12, .inputEnd), seen(system, 0.31, .inputEnd), seen(app, p, .idle), seen(app, p + Delivery.quietS, .idle)]
        XCTAssertEqual(Delivery.verdict(app: app, liftAt: dt, seen: s, now: p + Delivery.quietS), .notDelivered)
    }

    // The app in front is the system's own (SpringBoard): its report is the app's.
    func testTheAppInFrontIsTheSystem() {
        let s = [seen(system, 0.023, .inputEnd), seen(system, 0.4, .idle), seen(system, 0.8, .idle)]
        XCTAssertEqual(Delivery.verdict(app: system, liftAt: lift, seen: s, now: 0.9), .delivered)
    }

    // An unknown app: no verdict.
    func testNoAppIsUnknown() {
        XCTAssertEqual(Delivery.verdict(app: 0, liftAt: lift, seen: [seen(system, 0.02, .inputEnd)], now: 0.1), .unknown)
    }

    // The agent asks the app for its run loop going idle (the proof) only once the system has
    // reported and the margin has passed, and then once per half quiet window.
    func testWhenTheAgentAsksForIdle() {
        let s = [seen(system, 0.02, .inputEnd)]
        XCTAssertFalse(Delivery.asksIdle(app: app, liftAt: lift, seen: [], now: 0.5, lastAsk: nil))
        XCTAssertFalse(Delivery.asksIdle(app: app, liftAt: lift, seen: s, now: 0.1, lastAsk: nil))
        XCTAssertTrue(Delivery.asksIdle(app: app, liftAt: lift, seen: s, now: 0.02 + Delivery.marginS, lastAsk: nil))
        XCTAssertFalse(Delivery.asksIdle(app: app, liftAt: lift, seen: s, now: 0.30, lastAsk: 0.28))
        XCTAssertTrue(Delivery.asksIdle(app: app, liftAt: lift, seen: s, now: 0.28 + Delivery.quietS / 2, lastAsk: 0.28))
        XCTAssertFalse(Delivery.asksIdle(app: app, liftAt: lift, seen: s + [seen(app, 0.2, .inputEnd)], now: 0.5, lastAsk: nil))
    }
}

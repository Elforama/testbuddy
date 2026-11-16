import Foundation
import XCTest
@testable import TBAgentCore

/// Issue #334: a tap went to an app while UIKit was presenting a full-screen cover. SpringBoard
/// (pid 1716 in the CI log, "osservice<com.apple.SpringBoard>:1716") reported the touches
/// completed; the app never did, and went on without them. The agent counted "touches
/// completed" from any process, so the tap was reported done. Now it tells whether the app in
/// front got a record's touches, and says "not delivered" only on proof. Times are seconds from
/// the record's start; the record lifts at 0.01 s.
///
/// Every timeline is judged at each moment it could be (every 5 ms, each entry seen only from
/// its time on), not only once whole: an agent asked mid-way must not answer "not delivered"
/// for a tap whose report is still coming (review of b846c56).
final class DeliveryTests: XCTestCase {
    let app: Int32 = 49175, system: Int32 = 1716, picker: Int32 = 52011
    let lift = 0.01

    func seen(_ pid: Int32, _ at: TimeInterval, _ kind: Delivery.Kind) -> Delivery.Seen { .init(pid: pid, at: at, kind: kind) }
    /// The app's answer at `at` to the agent's ask to go idle made at `asked`.
    func answer(_ at: TimeInterval, asked: TimeInterval) -> Delivery.Seen { .init(pid: app, at: at, kind: .idleAnswer, askedAt: asked) }

    /// The verdicts at every moment from 0 to end: what had been seen by then.
    func verdicts(_ timeline: [Delivery.Seen], to end: TimeInterval) -> [(now: TimeInterval, v: Delivery.Verdict?)] {
        var out: [(TimeInterval, Delivery.Verdict?)] = []
        var now = 0.0
        while now <= end + 1e-9 {
            let past = timeline.filter { $0.at <= now + 1e-9 }
            out.append((now, Delivery.verdict(app: app, system: system, liftAt: lift, seen: past, now: now)))
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

    // The CI case: SpringBoard's report, then the app answering the agent's asks to go idle
    // promptly, past the margin and again a quiet window later, never reporting the touch.
    // "Not delivered" only from that second answer on.
    func testTheAppRanOnWithoutTheTouches() {
        let a1 = 0.023 + Delivery.marginS + 0.01, a2 = a1 + 0.02 + Delivery.quietS
        let s = [seen(system, 0.023, .inputEnd), answer(a1 + 0.02, asked: a1), answer(a2 + 0.02, asked: a2)]
        for (now, v) in verdicts(s, to: a2 + 0.2) {
            XCTAssertEqual(v, now >= a2 + 0.02 - 1e-9 ? .notDelivered : nil, "at \(now) s")
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

    // A prompt answer, then the report late (just inside the quiet window): never "not
    // delivered" at any moment.
    func testAnAnswerThenALateReport() {
        let a1 = 0.3
        let s = [seen(system, 0.022, .inputEnd), answer(a1, asked: a1 - 0.01), seen(app, a1 + Delivery.quietS - 0.02, .inputEnd),
                 answer(a1 + Delivery.quietS + 0.01, asked: a1 + Delivery.quietS)]
        assertNeverNotDelivered(s, to: 1.5)
        XCTAssertEqual(verdicts(s, to: 1.5).last?.v, .delivered)
    }

    // Answers to asks made before the margin are no proof: the touch may not be queued yet.
    func testAnswersBeforeTheMarginAreNoProof() {
        let s = [seen(system, 0.02, .inputEnd), answer(0.06, asked: 0.05), answer(0.21, asked: 0.2), answer(0.36, asked: 0.35)]
        assertNeverNotDelivered(s, to: 0.4)
    }

    // Review of 3270d53: the answers to the agent's asks come back over the automation session,
    // not the relay that carries the app's report. A stalled app with the touch queued answers
    // them only as it resumes, and its report, relayed slowly, comes after them. An answer that
    // came late (the app was busy when asked) makes the verdict unknown.
    func testAStallThenALateReportOverASlowRelay() {
        let s = [seen(system, 0.02, .inputEnd), answer(0.95, asked: 0.28), answer(0.95 + Delivery.quietS + 0.05, asked: 0.95 + Delivery.quietS),
                 seen(app, 1.6, .inputEnd)]
        assertNeverNotDelivered(s, to: 2.5)
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: Array(s.prefix(2)), now: 0.95), .unknown)
    }

    // A stall that ends before the first ask: the app handled the queued touch and posted its
    // report before answering; a relay up to the quiet window slower than the answers still
    // brings the report before the second answer.
    func testAStallBeforeTheFirstAsk() {
        let a1 = 0.02 + Delivery.marginS
        let s = [seen(system, 0.02, .inputEnd), answer(a1 + 0.01, asked: a1), seen(app, a1 + Delivery.quietS - 0.05, .inputEnd),
                 answer(a1 + Delivery.quietS + 0.02, asked: a1 + Delivery.quietS + 0.01)]
        assertNeverNotDelivered(s, to: 2)
        XCTAssertEqual(verdicts(s, to: 2).last?.v, .delivered)
    }

    // Other posts (a view appearing, the probe's commits over notifyd) are no proof, however
    // late: a commit can come just before a stall, the touch handled after it.
    func testOtherPostsAreNoProof() {
        let s = [seen(system, 0.02, .inputEnd), seen(app, 0.3, .other), seen(app, 0.7, .other), seen(app, 1.2, .other)]
        assertNeverNotDelivered(s, to: 3)
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: s, now: 0.02 + Delivery.waitS), .unknown)
    }

    // A stalled app answers nothing: no verdict, then unknown.
    func testAStalledAppIsNotJudged() {
        let s = [seen(system, 0.024, .inputEnd)]
        assertNeverNotDelivered(s, to: 3)
        XCTAssertNil(Delivery.verdict(app: app, system: system, liftAt: lift, seen: s, now: 0.3))
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: s, now: 0.024 + Delivery.waitS), .unknown)
    }

    // No report from SpringBoard either: unknown soon, not after the whole wait.
    func testNoSystemReportIsUnknownSoon() {
        XCTAssertNil(Delivery.verdict(app: app, system: system, liftAt: lift, seen: [], now: 0.1))
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: [], now: lift + Delivery.noSystemS), .unknown)
    }

    // A double tap: the first contact's report is no proof the second one came.
    func testOnlyReportsFromTheLastLiftCount() {
        let dt = 0.3 // the second contact lifts 0.3 s in
        let a1 = 0.31 + Delivery.marginS, a2 = a1 + 0.01 + Delivery.quietS
        let s = [seen(app, 0.12, .inputEnd), seen(system, 0.31, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)]
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: dt, seen: s, now: a2 + 0.02), .notDelivered)
    }

    // The app in front is SpringBoard itself: its report is the app's.
    func testTheAppInFrontIsTheSystem() {
        let s = [seen(system, 0.023, .inputEnd)]
        XCTAssertEqual(Delivery.verdict(app: system, system: system, liftAt: lift, seen: s, now: 0.9), .delivered)
    }

    // An unknown app: no verdict.
    func testNoAppIsUnknown() {
        XCTAssertEqual(Delivery.verdict(app: 0, system: system, liftAt: lift, seen: [seen(system, 0.02, .inputEnd)], now: 0.1), .unknown)
    }

    // Review of 3270d53: another process inside the app's window (the photo picker, an in-app
    // Safari view, the share sheet) takes the touch and reports it: that is not SpringBoard's
    // report, and never "not delivered" (selecting a photo twice would toggle it back).
    func testAnotherProcessInTheAppsWindowTookIt() {
        let a1 = 0.02 + Delivery.marginS, a2 = a1 + Delivery.quietS
        for s in [[seen(picker, 0.03, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)],
                  [seen(system, 0.02, .inputEnd), seen(picker, 0.03, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)]] {
            assertNeverNotDelivered(s, to: 2)
            XCTAssertEqual(verdicts(s, to: 0.05).last?.v, .delivered)
        }
    }

    // With SpringBoard's pid not known, its report cannot be told: unknown.
    func testNoKnownSystemIsUnknown() {
        let a1 = 0.02 + Delivery.marginS, a2 = a1 + Delivery.quietS
        let s = [seen(system, 0.02, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)]
        XCTAssertEqual(Delivery.verdict(app: app, system: 0, liftAt: lift, seen: s, now: a2 + 0.02), .unknown)
    }

    // The agent asks the app to go idle only once SpringBoard has reported and the margin has
    // passed, then a quiet window after the last answer, and not while an ask is unanswered.
    func testWhenTheAgentAsksForIdle() {
        let s = [seen(system, 0.02, .inputEnd)]
        let m = 0.02 + Delivery.marginS
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: [], now: 0.5, lastAsk: nil))
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s, now: 0.1, lastAsk: nil))
        XCTAssertTrue(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s, now: m, lastAsk: nil))
        let answered = s + [answer(m + 0.01, asked: m)]
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: answered, now: m + 0.05, lastAsk: m))
        XCTAssertTrue(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: answered, now: m + 0.01 + Delivery.quietS, lastAsk: m))
        // Still unanswered: no second ask.
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s, now: m + Delivery.quietS, lastAsk: m))
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s + [seen(app, 0.2, .inputEnd)], now: 0.5, lastAsk: nil))
    }

    // Review of 3270d53: a token from an agent that ran before (the host kept it across a
    // restart) must not name a record of the new one: each agent's tokens start at a random base.
    func testTokensOfAnotherAgentDoNotMatch() {
        var a = DeliveryTokens(), b = DeliveryTokens()
        let ta = (0..<16).map { _ in a.take() }, tb = (0..<16).map { _ in b.take() }
        XCTAssertTrue(Set(ta).isDisjoint(with: tb), "\(ta) and \(tb)")
        XCTAssertTrue(ta.allSatisfy { $0 > 0 })
    }
}

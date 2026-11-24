import Foundation
import XCTest
@testable import TBAgentCore

final class DeliveryTests: XCTestCase {
    let app: Int32 = 49175, system: Int32 = 1716, picker: Int32 = 52011
    let lift = 0.01

    func seen(_ pid: Int32, _ at: TimeInterval, _ kind: Delivery.Kind) -> Delivery.Seen { .init(pid: pid, at: at, kind: kind) }
    func answer(_ at: TimeInterval, asked: TimeInterval) -> Delivery.Seen { .init(pid: app, at: at, kind: .idleAnswer, askedAt: asked) }

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

    func assertNeverNotDelivered(_ timeline: [Delivery.Seen], to end: TimeInterval, file: StaticString = #filePath, line: UInt = #line) {
        for (now, v) in verdicts(timeline, to: end) where v == .notDelivered {
            XCTFail("not delivered at \(now) s", file: file, line: line)
            return
        }
    }

    func testTheAppRanOnWithoutTheTouches() {
        let a1 = 0.023 + Delivery.marginS + 0.01, a2 = a1 + 0.02 + Delivery.quietS
        let s = [seen(system, 0.023, .inputEnd), answer(a1 + 0.02, asked: a1), answer(a2 + 0.02, asked: a2)]
        for (now, v) in verdicts(s, to: a2 + 0.2) {
            XCTAssertEqual(v, now >= a2 + 0.02 - 1e-9 ? .notDelivered : nil, "at \(now) s")
        }
    }

    func testTheAppReportedTheTouches() {
        for s in [[seen(system, 0.022, .inputEnd), seen(app, 0.054, .inputEnd)],
                  [seen(app, 0.066, .inputEnd), seen(system, 0.066, .inputEnd)]] {
            assertNeverNotDelivered(s, to: 1)
            XCTAssertEqual(verdicts(s, to: 0.1).last?.v, .delivered)
        }
    }

    func testAnAnswerThenALateReport() {
        let a1 = 0.3
        let s = [seen(system, 0.022, .inputEnd), answer(a1, asked: a1 - 0.01), seen(app, a1 + Delivery.quietS - 0.02, .inputEnd),
                 answer(a1 + Delivery.quietS + 0.01, asked: a1 + Delivery.quietS)]
        assertNeverNotDelivered(s, to: 1.5)
        XCTAssertEqual(verdicts(s, to: 1.5).last?.v, .delivered)
    }

    func testAnswersBeforeTheMarginAreNoProof() {
        let s = [seen(system, 0.02, .inputEnd), answer(0.06, asked: 0.05), answer(0.21, asked: 0.2), answer(0.36, asked: 0.35)]
        assertNeverNotDelivered(s, to: 0.4)
    }

    func testAStallThenALateReportOverASlowRelay() {
        let s = [seen(system, 0.02, .inputEnd), answer(0.95, asked: 0.28), answer(0.95 + Delivery.quietS + 0.05, asked: 0.95 + Delivery.quietS),
                 seen(app, 1.6, .inputEnd)]
        assertNeverNotDelivered(s, to: 2.5)
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: Array(s.prefix(2)), now: 0.95), .unknown)
    }

    func testAStallBeforeTheFirstAsk() {
        let a1 = 0.02 + Delivery.marginS
        let s = [seen(system, 0.02, .inputEnd), answer(a1 + 0.01, asked: a1), seen(app, a1 + Delivery.quietS - 0.05, .inputEnd),
                 answer(a1 + Delivery.quietS + 0.02, asked: a1 + Delivery.quietS + 0.01)]
        assertNeverNotDelivered(s, to: 2)
        XCTAssertEqual(verdicts(s, to: 2).last?.v, .delivered)
    }

    func testOtherPostsAreNoProof() {
        let s = [seen(system, 0.02, .inputEnd), seen(app, 0.3, .other), seen(app, 0.7, .other), seen(app, 1.2, .other)]
        assertNeverNotDelivered(s, to: 3)
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: s, now: 0.02 + Delivery.waitS), .unknown)
    }

    func testAStalledAppIsNotJudged() {
        let s = [seen(system, 0.024, .inputEnd)]
        assertNeverNotDelivered(s, to: 3)
        XCTAssertNil(Delivery.verdict(app: app, system: system, liftAt: lift, seen: s, now: 0.3))
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: s, now: 0.024 + Delivery.waitS), .unknown)
    }

    func testNoSystemReportIsUnknownSoon() {
        XCTAssertNil(Delivery.verdict(app: app, system: system, liftAt: lift, seen: [], now: 0.1))
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: lift, seen: [], now: lift + Delivery.noSystemS), .unknown)
    }

    func testOnlyReportsFromTheLastLiftCount() {
        let dt = 0.3
        let a1 = 0.31 + Delivery.marginS, a2 = a1 + 0.01 + Delivery.quietS
        let s = [seen(app, 0.12, .inputEnd), seen(system, 0.31, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)]
        XCTAssertEqual(Delivery.verdict(app: app, system: system, liftAt: dt, seen: s, now: a2 + 0.02), .notDelivered)
    }

    func testTheAppInFrontIsTheSystem() {
        let s = [seen(system, 0.023, .inputEnd)]
        XCTAssertEqual(Delivery.verdict(app: system, system: system, liftAt: lift, seen: s, now: 0.9), .delivered)
    }

    func testNoAppIsUnknown() {
        XCTAssertEqual(Delivery.verdict(app: 0, system: system, liftAt: lift, seen: [seen(system, 0.02, .inputEnd)], now: 0.1), .unknown)
    }

    func testAnotherProcessInTheAppsWindowTookIt() {
        let a1 = 0.02 + Delivery.marginS, a2 = a1 + Delivery.quietS
        for s in [[seen(picker, 0.03, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)],
                  [seen(system, 0.02, .inputEnd), seen(picker, 0.03, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)]] {
            assertNeverNotDelivered(s, to: 2)
            XCTAssertEqual(verdicts(s, to: 0.05).last?.v, .delivered)
        }
    }

    func testNoKnownSystemIsUnknown() {
        let a1 = 0.02 + Delivery.marginS, a2 = a1 + Delivery.quietS
        let s = [seen(system, 0.02, .inputEnd), answer(a1 + 0.01, asked: a1), answer(a2 + 0.01, asked: a2)]
        XCTAssertEqual(Delivery.verdict(app: app, system: 0, liftAt: lift, seen: s, now: a2 + 0.02), .unknown)
    }

    func testWhenTheAgentAsksForIdle() {
        let s = [seen(system, 0.02, .inputEnd)]
        let m = 0.02 + Delivery.marginS
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: [], now: 0.5, lastAsk: nil))
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s, now: 0.1, lastAsk: nil))
        XCTAssertTrue(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s, now: m, lastAsk: nil))
        let answered = s + [answer(m + 0.01, asked: m)]
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: answered, now: m + 0.05, lastAsk: m))
        XCTAssertTrue(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: answered, now: m + 0.01 + Delivery.quietS, lastAsk: m))
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s, now: m + Delivery.quietS, lastAsk: m))
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: s + [seen(app, 0.2, .inputEnd)], now: 0.5, lastAsk: nil))
    }

    func testTokensOfAnotherAgentDoNotMatch() {
        var a = DeliveryTokens(), b = DeliveryTokens()
        let ta = (0..<16).map { _ in a.take() }, tb = (0..<16).map { _ in b.take() }
        XCTAssertTrue(Set(ta).isDisjoint(with: tb), "\(ta) and \(tb)")
        XCTAssertTrue(ta.allSatisfy { $0 > 0 })
    }
}

final class DeliveryEvidenceTests: XCTestCase {
    let app: Int32 = 49175, system: Int32 = 1716

    func testEvidenceHasSpringBoardsReportAndTheAnswerLags() {
        let s = [Delivery.Seen(pid: system, at: 0.03, kind: .inputEnd),
                 Delivery.Seen(pid: app, at: 0.31, kind: .idleAnswer, askedAt: 0.28),
                 Delivery.Seen(pid: app, at: 0.86, kind: .idleAnswer, askedAt: 0.81)]
        let e = Delivery.evidence(app: app, system: system, liftAt: 0.01, seen: s)
        XCTAssertEqual(e.reportAt, 0.03)
        XCTAssertEqual(e.answerLags.map { ($0 * 1000).rounded() }, [30, 50])
        XCTAssertNil(Delivery.evidence(app: app, system: system, liftAt: 0.01, seen: []).reportAt)
    }

    func testTrackedOnlyInCIMode() {
        XCTAssertFalse(Delivery.tracks(ci: false, probeSawTouch: false, app: app))
        XCTAssertTrue(Delivery.tracks(ci: true, probeSawTouch: false, app: app))
        XCTAssertFalse(Delivery.tracks(ci: true, probeSawTouch: true, app: app))
        XCTAssertFalse(Delivery.tracks(ci: true, probeSawTouch: false, app: 0))
    }
}

final class SwipeDeliveryTests: XCTestCase {
    let app: Int32 = 49175, system: Int32 = 1716
    let lift = 0.6, implicitAt = 5.65

    func answer(_ at: TimeInterval, asked: TimeInterval) -> Delivery.Seen { .init(pid: app, at: at, kind: .idleAnswer, askedAt: asked) }

    func verdicts(_ timeline: [Delivery.Seen], from start: TimeInterval, to end: TimeInterval, implicitAt: TimeInterval?) -> [(now: TimeInterval, v: Delivery.Verdict?)] {
        var out: [(TimeInterval, Delivery.Verdict?)] = []
        var now = start
        while now <= end + 1e-9 {
            let past = timeline.filter { $0.at <= now + 1e-9 }
            out.append((now, Delivery.verdict(app: app, system: system, liftAt: lift, seen: past, now: now, implicitAt: implicitAt)))
            now += 0.005
        }
        return out
    }

    var a1: TimeInterval { implicitAt + Delivery.marginS + 0.01 }
    var a2: TimeInterval { a1 + 0.02 + Delivery.quietS }

    func testAnImplicitlyConfirmedSwipeIsUnknown() {
        let s = [answer(a1 + 0.02, asked: a1), answer(a2 + 0.02, asked: a2)]
        for (now, v) in verdicts(s, from: implicitAt, to: a2 + 0.2, implicitAt: implicitAt) {
            XCTAssertEqual(v, .unknown, "at \(now) s")
        }
        let e = Delivery.evidence(app: app, system: system, liftAt: lift, seen: s, implicitAt: implicitAt)
        XCTAssertEqual(e.reportAt, implicitAt)
        XCTAssertTrue(e.implicit)
        XCTAssertFalse(Delivery.asksIdle(app: app, system: system, liftAt: lift, seen: [], now: implicitAt + Delivery.marginS,
                                         lastAsk: nil, implicitAt: implicitAt))
    }

    func testABlockedAppIsUnknown() {
        let s = [answer(a1 + 0.4, asked: a1)]
        for (_, v) in verdicts(s, from: implicitAt, to: a1 + 1.5, implicitAt: implicitAt) {
            XCTAssertNotEqual(v, .notDelivered)
        }
        XCTAssertEqual(verdicts(s, from: implicitAt, to: a1 + 0.5, implicitAt: implicitAt).last?.v, .unknown)
    }

    func testTheAppsLateReportIsDelivery() {
        let s = [Delivery.Seen(pid: app, at: 5.8, kind: .inputEnd), answer(a1 + 0.02, asked: a1), answer(a2 + 0.02, asked: a2)]
        XCTAssertEqual(verdicts(s, from: implicitAt, to: a2 + 0.1, implicitAt: implicitAt).last?.v, .delivered)
    }

    func testWithoutTheImplicitConfirmationNoReportIsUnknown() {
        let s = [answer(a1 + 0.02, asked: a1), answer(a2 + 0.02, asked: a2)]
        XCTAssertEqual(verdicts(s, from: implicitAt, to: a2 + 0.1, implicitAt: nil).last?.v, .unknown)
    }
}

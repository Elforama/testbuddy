import Foundation
import XCTest
@testable import TBAgentCore

final class Recorder: PathBuilder {
    enum Step: Equatable {
        case down(CGPoint, TimeInterval)
        case move(CGPoint, TimeInterval)
        case lift(TimeInterval)
    }

    final class Path {
        var steps: [Step] = []
        var drag: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = []
        var dragLiftAt: TimeInterval?
    }

    var explicit = true

    func touch(at p: CGPoint, offset: TimeInterval) -> Path {
        let path = Path()
        path.steps.append(.down(p, offset))
        return path
    }

    func move(_ path: Path, to p: CGPoint, at offset: TimeInterval) { path.steps.append(.move(p, offset)) }
    func lift(_ path: Path, at offset: TimeInterval) { path.steps.append(.lift(offset)) }

    func explicitDrag(_ moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)], liftAt: TimeInterval) -> Path? {
        guard explicit else { return nil }
        let path = Path()
        path.drag = moves
        path.dragLiftAt = liftAt
        return path
    }
}

final class GesturesTests: XCTestCase {
    private let rec = Recorder()

    func testATapHoldsLongEnoughForAControlToSeeIt() throws {
        let g = try Gestures.tap(rec, at: CGPoint(x: 10, y: 20))
        XCTAssertEqual(g.paths.count, 1)
        XCTAssertEqual(g.paths[0].steps, [.down(CGPoint(x: 10, y: 20), 0), .lift(Gestures.tapHoldMs / 1000)])
        XCTAssertTrue(g.early)
        XCTAssertEqual(g.timeout, 10 + Gestures.tapHoldMs / 1000, accuracy: 1e-9)
    }

    func testAPressIsATapHeldLonger() throws {
        let g = try Gestures.tap(rec, at: CGPoint(x: 1, y: 1), holdMs: 3000)
        XCTAssertEqual(g.paths[0].steps.last, .lift(3))
        XCTAssertEqual(g.timeout, 13, accuracy: 1e-9)
    }

    func testAZeroHoldStillLifts() throws {
        let g = try Gestures.tap(rec, at: CGPoint(x: 1, y: 1), holdMs: 0)
        XCTAssertEqual(g.paths[0].steps.last, .lift(0.001))
    }

    func testADoubleTapHoldsEachTouchAndKeepsThePairWithinTheGestureWindow() throws {
        let g = try Gestures.taps(rec, at: CGPoint(x: 5, y: 6), count: 2)
        XCTAssertEqual(g.paths.count, 2)
        XCTAssertEqual(g.paths[0].steps, [.down(CGPoint(x: 5, y: 6), 0), .lift(0.1)])
        XCTAssertEqual(g.paths[1].steps, [.down(CGPoint(x: 5, y: 6), 0.2), .lift(0.2 + 0.1)])
        XCTAssertFalse(g.early)
    }

    func testTapsAreAtLeastOne() throws {
        XCTAssertEqual(try Gestures.taps(rec, at: .zero, count: 0).paths.count, 1)
    }

    func testASwipeGlidesOverItsDurationAndLiftsMoving() throws {
        let g = try Gestures.swipe(rec, from: CGPoint(x: 100, y: 600), to: CGPoint(x: 100, y: 200), ms: 400)
        XCTAssertEqual(g.paths[0].steps, [.down(CGPoint(x: 100, y: 600), 0), .move(CGPoint(x: 100, y: 200), 0.05), .lift(0.05 + 0.4)])
        XCTAssertEqual(g.timeout, 10.45, accuracy: 1e-9)
    }

    func testASwipeThatPlacesAControlRestsBeforeMovingAndBeforeLifting() throws {
        let a = CGPoint(x: 201, y: 606), z = CGPoint(x: 401, y: 606)
        let g = try Gestures.swipe(rec, from: a, to: z, ms: 400, rest: true)
        let moves = g.paths[0].drag
        let r = Gestures.swipeRestS, e = Gestures.swipeEndCreepS
        let y = CGPoint(x: z.x - Gestures.swipeEndCreepPt, y: z.y)
        XCTAssertEqual(moves.map { $0.p }, [a, a, y, z], "the finger should rest at the start, glide, then creep to the end: \(g.paths[0].steps)")
        guard moves.count == 4 else { return }
        XCTAssertEqual(moves[1].offset, 0, accuracy: 1e-9)
        XCTAssertEqual(moves[1].duration, r, accuracy: 1e-9)
        XCTAssertEqual(moves[2].offset, r, accuracy: 1e-9)
        XCTAssertEqual(moves[2].duration, 0.4, accuracy: 1e-9)
        XCTAssertEqual(moves[3].offset, r + 0.4, accuracy: 1e-9)
        XCTAssertEqual(moves[3].duration, e, accuracy: 1e-9)
        XCTAssertEqual(g.paths[0].dragLiftAt ?? 0, r + 0.4 + e, accuracy: 1e-9)
        XCTAssertEqual(g.timeout, 10 + r + 0.4 + e, accuracy: 1e-9)
        XCTAssertGreaterThan(r, 0.15, "longer than a scroll view's touch delay")
    }

    func testASwipeThatPlacesAControlKeepsMovingAtTheEndPastAStallOfASecond() throws {
        let a = CGPoint(x: 201, y: 606), z = CGPoint(x: 401, y: 606)
        let g = try Gestures.swipe(rec, from: a, to: z, ms: 400, rest: true)
        let moves = g.paths[0].drag
        guard moves.count == 4 else { return XCTFail("no resting swipe: \(g.paths[0].steps)") }
        let stall = 0.925, frames = 3.0 / 60
        let glideEnd = moves[2].p, creep = Double(z.x - glideEnd.x)
        XCTAssertEqual(moves[3].p, z, "the finger should lift where the swipe ends")
        XCTAssertEqual(glideEnd.y, z.y)
        XCTAssertGreaterThanOrEqual(creep, 4, "the finger should still be moving at the end: \(moves.map { $0.p })")
        XCTAssertLessThanOrEqual(creep / moves[3].duration, 10, "too fast at the lift: it would fling")
        XCTAssertGreaterThanOrEqual(moves[3].duration, stall + frames + 0.5, "the creep should outlast #182's stall, with half a second to spare")
        XCTAssertEqual(g.paths[0].dragLiftAt ?? 0, moves[3].offset + moves[3].duration, accuracy: 1e-9)
        XCTAssertEqual(moves[0].p, a)
        XCTAssertEqual(moves[1].p, a)
        XCTAssertEqual(moves[1].duration, Gestures.swipeRestS, accuracy: 1e-9)
        XCTAssertLessThan(Gestures.swipeRestS, 0.5)
    }

    func testWithoutExplicitMovesARestingSwipeStillRests() throws {
        rec.explicit = false
        let a = CGPoint(x: 10, y: 600), z = CGPoint(x: 300, y: 600)
        let g = try Gestures.swipe(rec, from: a, to: z, ms: 400, rest: true)
        let r = Gestures.swipeRestS, e = Gestures.swipeEndCreepS
        let y = CGPoint(x: z.x - Gestures.swipeEndCreepPt, y: z.y)
        XCTAssertEqual(g.paths[0].steps, [.down(a, 0), .move(a, r), .move(y, r + 0.4), .move(z, r + 0.4 + e), .lift(r + 0.4 + e)])
    }

    func testASwipeThatDoesNotRestStillLiftsMoving() throws {
        let g = try Gestures.swipe(rec, from: CGPoint(x: 100, y: 600), to: CGPoint(x: 100, y: 200), ms: 400, rest: false)
        XCTAssertEqual(g.paths[0].steps.last, .lift(0.05 + 0.4))
        XCTAssertTrue(g.paths[0].drag.isEmpty)
    }

    func testAVeryQuickSwipeStillTakes50Ms() throws {
        let g = try Gestures.swipe(rec, from: .zero, to: CGPoint(x: 0, y: 100), ms: 10)
        XCTAssertEqual(g.paths[0].steps.last, .lift(0.05 + 0.05))
    }

    func testADragKicksGlidesThenEndsSlowly() throws {
        let a = CGPoint(x: 100, y: 500), z = CGPoint(x: 100, y: 100)
        let g = try Gestures.drag(rec, from: a, to: z)
        let moves = g.paths[0].drag
        XCTAssertEqual(moves.map { $0.p }, [a, CGPoint(x: 100, y: 488), CGPoint(x: 100, y: 120), z])
        XCTAssertEqual(moves[0].offset, 0)
        XCTAssertEqual(moves[1].offset, 0)
        XCTAssertEqual(moves[2].offset, 0.04, accuracy: 1e-9)
        XCTAssertEqual(moves[3].offset, 0.19, accuracy: 1e-9)
        XCTAssertEqual(moves[1].duration, 0.04, accuracy: 1e-9)
        XCTAssertEqual(moves[2].duration, 0.15, accuracy: 1e-9)
        XCTAssertEqual(moves[3].duration, 0.1, accuracy: 1e-9)
        XCTAssertEqual(g.paths[0].dragLiftAt ?? 0, 0.29, accuracy: 1e-9)
        XCTAssertEqual(g.timeout, 10.29, accuracy: 1e-9)
    }

    func testADragsTailByToolkitAndCIMode() throws {
        XCTAssertTrue(Gestures.tail(flutter: false, ci: false) == (20, 200))
        XCTAssertTrue(Gestures.tail(flutter: false, ci: true) == (40, 200))
        XCTAssertTrue(Gestures.tail(flutter: true, ci: false) == (40, 80))
        XCTAssertTrue(Gestures.tail(flutter: true, ci: true) == (40, 80))
        let t = Gestures.tail(flutter: false, ci: true)
        let g = try Gestures.drag(rec, from: CGPoint(x: 100, y: 500), to: CGPoint(x: 100, y: 100), tailSpeed: t.speed, tailPt: t.pt)
        XCTAssertEqual(g.paths[0].drag[2].p, CGPoint(x: 100, y: 140))
        XCTAssertEqual(g.paths[0].drag[3].duration, 0.2, accuracy: 1e-9)
        XCTAssertEqual(g.paths[0].dragLiftAt ?? 0, 0.39, accuracy: 1e-9)
    }

    func testFlutterDragSlowsOnlyTheTail() throws {
        let t = Gestures.tail(flutter: true, ci: false)
        let drag = try Gestures.drag(rec, from: .zero, to: CGPoint(x: 0, y: 400), tailSpeed: t.speed, tailPt: t.pt)
        XCTAssertEqual(drag.paths[0].drag.last!.duration, 0.5, accuracy: 1e-9)
        XCTAssertEqual(drag.paths[0].dragLiftAt!, 0.69, accuracy: 1e-9)
    }

    func testAShortDragScalesItsKickAndTail() throws {
        let g = try Gestures.drag(rec, from: CGPoint(x: 0, y: 0), to: CGPoint(x: 40, y: 0))
        let moves = g.paths[0].drag
        XCTAssertEqual(Double(moves[1].p.x), 4, accuracy: 1e-9)
        XCTAssertEqual(Double(moves[2].p.x), 30, accuracy: 1e-9)
    }

    func testWithoutExplicitMovesADragArrivesAtEachPoint() throws {
        rec.explicit = false
        let g = try Gestures.drag(rec, from: CGPoint(x: 0, y: 400), to: CGPoint(x: 0, y: 0))
        XCTAssertEqual(g.paths[0].steps.count, 5)
        XCTAssertEqual(g.paths[0].steps[0], .down(CGPoint(x: 0, y: 400), 0))
        guard case .move(let end, let at) = g.paths[0].steps[3] else { return XCTFail("\(g.paths[0].steps)") }
        XCTAssertEqual(end, CGPoint(x: 0, y: 0))
        XCTAssertEqual(at, 0.29, accuracy: 1e-9)
        XCTAssertEqual(g.paths[0].steps[4], .lift(at))
    }

    func testADragConfirmedOnlyImplicitlyIsUnconfirmed() throws {
        let g = try Gestures.drag(rec, from: CGPoint(x: 0, y: 400), to: CGPoint(x: 0, y: 0))
        XCTAssertTrue(g.early)
        XCTAssertTrue(Gestures.implicitlyConfirmed(early: g.early, endedEarly: false, confirmedAt: g.liftAt + 5.02, liftAt: g.liftAt))
        XCTAssertFalse(Gestures.implicitlyConfirmed(early: g.early, endedEarly: true, confirmedAt: g.liftAt + 5.02, liftAt: g.liftAt),
                       "the app's touches completed ended the wait")
        XCTAssertFalse(Gestures.implicitlyConfirmed(early: g.early, endedEarly: false, confirmedAt: g.liftAt + 0.2, liftAt: g.liftAt),
                       "testmanagerd's usual confirmation")
        XCTAssertFalse(Gestures.implicitlyConfirmed(early: g.early, endedEarly: false, confirmedAt: g.liftAt + 4.5, liftAt: g.liftAt),
                       "a late confirmation, but before testmanagerd's 5 s run out")
        XCTAssertFalse(Gestures.implicitlyConfirmed(early: false, endedEarly: false, confirmedAt: g.liftAt + 5.02, liftAt: g.liftAt),
                       "a record whose wait no touches completed may end")
    }

    func testADragShorterThanAPointIsATap() throws {
        let g = try Gestures.drag(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 10.5, y: 10))
        XCTAssertEqual(g.paths[0].steps, [.down(CGPoint(x: 10, y: 10), 0), .lift(Gestures.tapHoldMs / 1000)])
    }

    func testAPressAndHoldDragRestsThenMovesInStepsThenRestsAndLifts() throws {
        let a = CGPoint(x: 200, y: 275), z = CGPoint(x: 200, y: 375)
        let g = try Gestures.holdDrag(rec, from: a, to: z, holdS: 0.8, ms: 1000)
        let moves = g.paths[0].drag
        XCTAssertEqual(moves.map { $0.p }, [a, a, CGPoint(x: 200, y: 295), CGPoint(x: 200, y: 315), CGPoint(x: 200, y: 335),
                                            CGPoint(x: 200, y: 355), z, z])
        guard moves.count == 8 else { return }
        XCTAssertEqual(moves[1].offset, 0, accuracy: 1e-9)
        XCTAssertEqual(moves[1].duration, 0.8, accuracy: 1e-9)
        XCTAssertGreaterThan(moves[1].duration, 0.5)
        for k in 2...6 {
            XCTAssertEqual(moves[k].offset, 0.8 + Double(k - 2) * 0.2, accuracy: 1e-9, "step \(k - 1)")
            XCTAssertEqual(moves[k].duration, 0.2, accuracy: 1e-9, "step \(k - 1)")
            XCTAssertLessThanOrEqual(abs(Double(moves[k].p.y - moves[k - 1].p.y)), Gestures.holdDragStepPt + 1e-9)
        }
        XCTAssertEqual(moves[7].offset, 1.8, accuracy: 1e-9)
        XCTAssertEqual(moves[7].duration, Gestures.holdDragDropS, accuracy: 1e-9)
        XCTAssertEqual(g.paths[0].dragLiftAt ?? 0, 1.8 + Gestures.holdDragDropS, accuracy: 1e-9)
        XCTAssertEqual(g.liftAt, 1.8 + Gestures.holdDragDropS, accuracy: 1e-9)
        XCTAssertEqual(g.timeout, 10 + g.liftAt, accuracy: 1e-9)
        XCTAssertTrue(g.waitsForSystem, "a drag waits for SpringBoard in CI mode, as a swipe does (#137)")
    }

    func testAPressAndHoldDragsStepsStayShortAndEndOnTheTarget() throws {
        let a = CGPoint(x: 10, y: 10), z = CGPoint(x: 133, y: 517)
        let g = try Gestures.holdDrag(rec, from: a, to: z, holdS: 0.6, ms: 700)
        let moves = g.paths[0].drag
        let glide = Array(moves.dropFirst(2).dropLast())
        XCTAssertEqual(glide.last?.p, z)
        XCTAssertEqual(moves.last?.p, z)
        var prev = a
        for m in glide {
            let d = (pow(Double(m.p.x - prev.x), 2) + pow(Double(m.p.y - prev.y), 2)).squareRoot()
            XCTAssertLessThanOrEqual(d, Gestures.holdDragStepPt + 1e-9)
            prev = m.p
        }
        XCTAssertEqual(glide.map { $0.duration }.reduce(0, +), 0.7, accuracy: 1e-9, "the move takes the flow's duration")
    }

    func testAPressAndHoldDragsMoveIsNeverInstant() throws {
        let g = try Gestures.holdDrag(rec, from: .zero, to: CGPoint(x: 0, y: 10), holdS: 0, ms: 0)
        let moves = g.paths[0].drag
        XCTAssertEqual(moves[1].duration, 0)
        XCTAssertEqual(moves[2].duration, Gestures.holdDragMinMoveS, accuracy: 1e-9)
    }

    func testWithoutExplicitMovesAPressAndHoldDragStillHolds() throws {
        rec.explicit = false
        let a = CGPoint(x: 100, y: 100), z = CGPoint(x: 100, y: 140)
        let g = try Gestures.holdDrag(rec, from: a, to: z, holdS: 0.8, ms: 400)
        let d = Gestures.holdDragDropS
        let want: [Recorder.Step] = [.down(a, 0), .move(a, 0.8), .move(CGPoint(x: 100, y: 120), 1.0), .move(z, 1.2),
                                     .move(z, 1.2 + d), .lift(1.2 + d)]
        XCTAssertEqual(g.paths[0].steps.count, want.count, "\(g.paths[0].steps)")
        for (got, w) in zip(g.paths[0].steps, want) {
            switch (got, w) {
            case let (.down(p, t), .down(q, u)), let (.move(p, t), .move(q, u)):
                XCTAssertEqual(p, q); XCTAssertEqual(t, u, accuracy: 1e-9)
            case let (.lift(t), .lift(u)):
                XCTAssertEqual(t, u, accuracy: 1e-9)
            default:
                XCTFail("\(got), want \(w)")
            }
        }
    }
}

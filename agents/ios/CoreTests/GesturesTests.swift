import Foundation
import XCTest
@testable import TBAgentCore

/// Records the paths a gesture builds, in place of XCTest's XCPointerEventPath.
final class Recorder: PathBuilder {
    enum Step: Equatable {
        case down(CGPoint, TimeInterval)
        case move(CGPoint, TimeInterval)
        case lift(TimeInterval)
    }

    final class Path {
        var steps: [Step] = []
        /// The moves of an explicit drag: point, offset, duration.
        var drag: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = []
        var dragLiftAt: TimeInterval?
    }

    /// Whether this "Xcode" has explicit move events.
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

/// The timing of the iOS agent's gestures: tb's taps, swipes and drags are only as good as it.
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

    /// Issue #127: a swipe from a slider left it at 71 instead of 80+ on a slow simulator. The
    /// finger lifted while still gliding: when the app was behind, the last moves reached it in
    /// one batch with the lift, and the slider settled where its thumb had got to, not where the
    /// finger was (and a batch with the touch down loses its moves the same way: a control in a
    /// scroll view only gets the touch once the scroll view's 150 ms touch delay ran out). A
    /// swipe that places what it starts on rests at its start before it moves and at its end
    /// before it lifts.
    func testASwipeThatPlacesAControlRestsBeforeMovingAndBeforeLifting() throws {
        let a = CGPoint(x: 201, y: 606), z = CGPoint(x: 401, y: 606)
        let g = try Gestures.swipe(rec, from: a, to: z, ms: 400, rest: true)
        let moves = g.paths[0].drag
        let r = Gestures.swipeRestS, e = Gestures.swipeEndCreepS
        let y = CGPoint(x: z.x - Gestures.swipeEndCreepPt, y: z.y) // the glide stops short; the finger creeps the rest
        XCTAssertEqual(moves.map { $0.p }, [a, a, y, z], "the finger should rest at the start, glide, then creep to the end: \(g.paths[0].steps)")
        guard moves.count == 4 else { return }
        XCTAssertEqual(moves[1].offset, 0, accuracy: 1e-9)
        XCTAssertEqual(moves[1].duration, r, accuracy: 1e-9)   // still at the start
        XCTAssertEqual(moves[2].offset, r, accuracy: 1e-9)
        XCTAssertEqual(moves[2].duration, 0.4, accuracy: 1e-9) // the glide: the flow's duration
        XCTAssertEqual(moves[3].offset, r + 0.4, accuracy: 1e-9)
        XCTAssertEqual(moves[3].duration, e, accuracy: 1e-9)   // creeping at the end
        XCTAssertEqual(g.paths[0].dragLiftAt ?? 0, r + 0.4 + e, accuracy: 1e-9) // lifted only then
        XCTAssertEqual(g.timeout, 10 + r + 0.4 + e, accuracy: 1e-9)
        XCTAssertGreaterThan(r, 0.15, "longer than a scroll view's touch delay")
    }

    /// Issue #182: #127's rests were not enough. In CI the bench app stopped handling events
    /// about 130 ms into the glide, for about 925 ms (its simulator log: no accessibility event,
    /// an idle request answered 680 ms late), and the slider settled at 74, not 100. Measured
    /// with a touch logger and the app stopped 900 ms of every 1.2 s: the moves that came during
    /// a stall reach the app as one move, a slider's pan that begins on it needs another move to
    /// follow the finger, and a finger held still sends none (56 instead of 100). So the finger
    /// does not stand still at the end: it creeps the last few points, for long enough that it
    /// still moves once a stall like that one is over, even one that starts as the glide ends.
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
        // The rest before moving is unchanged: past a scroll view's touch delay, short of a long press.
        XCTAssertEqual(moves[0].p, a)
        XCTAssertEqual(moves[1].p, a)
        XCTAssertEqual(moves[1].duration, Gestures.swipeRestS, accuracy: 1e-9)
        XCTAssertLessThan(Gestures.swipeRestS, 0.5)
    }

    /// Issue #127, in an Xcode without explicit move events: the same rests, by moveToPoints.
    func testWithoutExplicitMovesARestingSwipeStillRests() throws {
        rec.explicit = false
        let a = CGPoint(x: 10, y: 600), z = CGPoint(x: 300, y: 600)
        let g = try Gestures.swipe(rec, from: a, to: z, ms: 400, rest: true)
        let r = Gestures.swipeRestS, e = Gestures.swipeEndCreepS
        let y = CGPoint(x: z.x - Gestures.swipeEndCreepPt, y: z.y)
        XCTAssertEqual(g.paths[0].steps, [.down(a, 0), .move(a, r), .move(y, r + 0.4), .move(z, r + 0.4 + e), .lift(r + 0.4 + e)])
    }

    /// A swipe that does not place a control (a direction, two points) still lifts moving and flings.
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
        XCTAssertEqual(moves.map { $0.p }, [a, CGPoint(x: 100, y: 488), CGPoint(x: 100, y: 140), z])
        XCTAssertEqual(moves[0].offset, 0)
        XCTAssertEqual(moves[1].offset, 0)
        XCTAssertEqual(moves[2].offset, 0.04, accuracy: 1e-9)
        XCTAssertEqual(moves[3].offset, 0.19, accuracy: 1e-9)
        XCTAssertEqual(moves[1].duration, 0.04, accuracy: 1e-9)
        XCTAssertEqual(moves[2].duration, 0.15, accuracy: 1e-9)
        // The tail: 40 pt at 200 pt/s, too slow to fling on.
        XCTAssertEqual(moves[3].duration, 0.2, accuracy: 1e-9)
        XCTAssertEqual(g.paths[0].dragLiftAt ?? 0, 0.39, accuracy: 1e-9)
        XCTAssertEqual(g.timeout, 10.39, accuracy: 1e-9)
    }

    func testFlutterDragSlowsOnlyTheTail() throws {
        let drag = try Gestures.drag(rec, from: .zero, to: CGPoint(x: 0, y: 400), tailSpeed: 80)
        XCTAssertEqual(drag.paths[0].drag.last!.duration, 0.5, accuracy: 1e-9)
        XCTAssertEqual(drag.paths[0].dragLiftAt!, 0.69, accuracy: 1e-9)
    }

    func testAShortDragScalesItsKickAndTail() throws {
        let g = try Gestures.drag(rec, from: CGPoint(x: 0, y: 0), to: CGPoint(x: 40, y: 0))
        let moves = g.paths[0].drag
        XCTAssertEqual(Double(moves[1].p.x), 4, accuracy: 1e-9)  // len / 10
        XCTAssertEqual(Double(moves[2].p.x), 30, accuracy: 1e-9) // tail len / 4
    }

    func testWithoutExplicitMovesADragArrivesAtEachPoint() throws {
        rec.explicit = false
        let g = try Gestures.drag(rec, from: CGPoint(x: 0, y: 400), to: CGPoint(x: 0, y: 0))
        XCTAssertEqual(g.paths[0].steps.count, 5)
        XCTAssertEqual(g.paths[0].steps[0], .down(CGPoint(x: 0, y: 400), 0))
        guard case .move(let end, let at) = g.paths[0].steps[3] else { return XCTFail("\(g.paths[0].steps)") }
        XCTAssertEqual(end, CGPoint(x: 0, y: 0))
        XCTAssertEqual(at, 0.39, accuracy: 1e-9)
        XCTAssertEqual(g.paths[0].steps[4], .lift(at))
    }

    /// Issue #111: a drag testmanagerd confirmed only once its 5 s wait for the app's "touches
    /// completed" timed out is reported as unconfirmed; one the app confirmed, or one confirmed
    /// in the usual ~200 ms, is not.
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
}

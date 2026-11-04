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

    func testADoubleTapIsTwoPathsAnIntervalApart() throws {
        let g = try Gestures.taps(rec, at: CGPoint(x: 5, y: 6), count: 2)
        XCTAssertEqual(g.paths.count, 2)
        XCTAssertEqual(g.paths[0].steps, [.down(CGPoint(x: 5, y: 6), 0), .lift(0.06)])
        XCTAssertEqual(g.paths[1].steps, [.down(CGPoint(x: 5, y: 6), 0.2), .lift(0.2 + 0.06)])
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

    func testADragShorterThanAPointIsATap() throws {
        let g = try Gestures.drag(rec, from: CGPoint(x: 10, y: 10), to: CGPoint(x: 10.5, y: 10))
        XCTAssertEqual(g.paths[0].steps, [.down(CGPoint(x: 10, y: 10), 0), .lift(Gestures.tapHoldMs / 1000)])
    }
}

import Foundation
import TBTouchGuard
import XCTest
@testable import TBAgentCore

/// Issue #337: the press the app saw (TBTouchGuard.h tbPressRecord, TouchGuard.Press). Times are
/// seconds on one virtual clock, as the probe's CACurrentMediaTime and UITouch timestamps are.
final class PressTests: XCTestCase {
    private let nonce: UInt16 = 7

    /// downAt and liftAt: the system's times; downDone: the down delivered; liftArrived: the
    /// lift reached sendEvent.
    private func press(downAt: Double = 100, downDone: Double, liftAt: Double, liftArrived: Double) -> TouchGuard.Press? {
        TouchGuard.Press.read(tbPressRecord(nonce, downAt, downDone, liftAt, liftArrived), nonce: nonce)
    }

    /// Issue #337, from the CI trace: the down took 133 ms to be delivered (a 132 ms stall), and
    /// the lift, sent 129 ms after the down, came 5 ms after that. The app saw 5 ms of a 129 ms
    /// press: cut short.
    func testAStalledDownCutsThePressShort() {
        let p = press(downDone: 100.133, liftAt: 100.129, liftArrived: 100.138)
        XCTAssertEqual(p, TouchGuard.Press(seenMs: 5, pressMs: 129, stallMs: 133))
        XCTAssertTrue(p!.cutShort)
    }

    /// The same press with its down delivered at once: the app saw nearly all of it.
    func testAPromptDownIsAFullPress() {
        let p = press(downDone: 100.009, liftAt: 100.129, liftArrived: 100.130)!
        XCTAssertEqual(p.seenMs, 121)
        XCTAssertFalse(p.cutShort)
    }

    /// A plain 18 ms tap whose down took 10 ms: seen 8 ms, under half, but no stall: a tap
    /// is not cut short by ordinary delivery.
    func testAnOrdinaryTapIsNotCutShort() {
        let p = press(downDone: 100.010, liftAt: 100.018, liftArrived: 100.018)!
        XCTAssertEqual(p.seenMs, 8)
        XCTAssertFalse(p.cutShort)
    }

    /// A stall in the down of a 3 s long press still leaves the app most of the press.
    func testAStallInALongPressLeavesMostOfIt() {
        let p = press(downDone: 100.150, liftAt: 103.0, liftArrived: 103.001)!
        XCTAssertEqual(p.stallMs, 150)
        XCTAssertFalse(p.cutShort)
        // A stall of 2 s in the same press: the app sees under half of it.
        XCTAssertTrue(press(downDone: 102.0, liftAt: 103.0, liftArrived: 103.001)!.cutShort)
    }

    /// A lift the app got before its down was delivered (as the trace's stall can order them)
    /// counts as no press at all, not a negative one.
    func testALiftBeforeTheDeliveredDownIsNoPress() {
        let p = press(downDone: 100.140, liftAt: 100.100, liftArrived: 100.130)!
        XCTAssertEqual(p.seenMs, 0)
        XCTAssertTrue(p.cutShort)
    }

    /// A double tap publishes the touch cut shortest: its first press here, kept over a full
    /// second one; another request's record is replaced.
    func testTheShortestPressOfAGestureIsKept() {
        let first = tbPressRecord(nonce, 100, 100.133, 100.129, 100.138)
        let second = tbPressRecord(nonce, 100.300, 100.302, 100.400, 100.401)
        XCTAssertEqual(tbPressKeep(first, second), first)
        XCTAssertEqual(tbPressKeep(second, first), first)
        let earlier = tbPressRecord(nonce &- 1, 90, 90.2, 90.1, 90.3)
        XCTAssertEqual(tbPressKeep(earlier, second), second)
    }

    /// No result for this request (none, or another request's): nothing known, nothing said.
    func testNoResultMeansNothing() {
        XCTAssertNil(TouchGuard.Press.read(0, nonce: nonce))
        XCTAssertNil(TouchGuard.Press.read(tbPressRecord(nonce &+ 1, 100, 100.2, 100.1, 100.3), nonce: nonce))
        XCTAssertNil(TouchGuard.Press.read(tbPressRecord(0, 100, 100.2, 100.1, 100.3), nonce: 0))
        XCTAssertEqual(TouchGuard.Press.next(UInt16.max), 1)
    }
}

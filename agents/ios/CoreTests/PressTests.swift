import Foundation
import TBTouchGuard
import XCTest
@testable import TBAgentCore

final class PressTests: XCTestCase {
    private let nonce: UInt16 = 7

    private func press(downAt: Double = 100, downDone: Double, liftAt: Double, liftArrived: Double) -> TouchGuard.Press? {
        TouchGuard.Press.read(tbPressRecord(nonce, downAt, downDone, liftAt, liftArrived), nonce: nonce)
    }

    func testAStalledDownCutsThePressShort() {
        let p = press(downDone: 100.133, liftAt: 100.129, liftArrived: 100.138)
        XCTAssertEqual(p, TouchGuard.Press(seenMs: 5, pressMs: 129, stallMs: 133))
        XCTAssertTrue(p!.cutShort)
    }

    func testAPromptDownIsAFullPress() {
        let p = press(downDone: 100.009, liftAt: 100.129, liftArrived: 100.130)!
        XCTAssertEqual(p.seenMs, 121)
        XCTAssertFalse(p.cutShort)
    }

    func testAnOrdinaryTapIsNotCutShort() {
        let p = press(downDone: 100.010, liftAt: 100.018, liftArrived: 100.018)!
        XCTAssertEqual(p.seenMs, 8)
        XCTAssertFalse(p.cutShort)
    }

    func testAStallInALongPressLeavesMostOfIt() {
        let p = press(downDone: 100.150, liftAt: 103.0, liftArrived: 103.001)!
        XCTAssertEqual(p.stallMs, 150)
        XCTAssertFalse(p.cutShort)
        XCTAssertTrue(press(downDone: 102.0, liftAt: 103.0, liftArrived: 103.001)!.cutShort)
    }

    func testALiftBeforeTheDeliveredDownIsNoPress() {
        let p = press(downDone: 100.140, liftAt: 100.100, liftArrived: 100.130)!
        XCTAssertEqual(p.seenMs, 0)
        XCTAssertTrue(p.cutShort)
    }

    func testTheShortestPressOfAGestureIsKept() {
        let first = tbPressRecord(nonce, 100, 100.133, 100.129, 100.138)
        let second = tbPressRecord(nonce, 100.300, 100.302, 100.400, 100.401)
        XCTAssertEqual(tbPressKeep(first, second), first)
        XCTAssertEqual(tbPressKeep(second, first), first)
        let earlier = tbPressRecord(nonce &- 1, 90, 90.2, 90.1, 90.3)
        XCTAssertEqual(tbPressKeep(earlier, second), second)
    }

    func testNoResultMeansNothing() {
        XCTAssertNil(TouchGuard.Press.read(0, nonce: nonce))
        XCTAssertNil(TouchGuard.Press.read(tbPressRecord(nonce &+ 1, 100, 100.2, 100.1, 100.3), nonce: nonce))
        XCTAssertNil(TouchGuard.Press.read(tbPressRecord(0, 100, 100.2, 100.1, 100.3), nonce: 0))
        XCTAssertEqual(TouchGuard.Press.next(UInt16.max), 1)
    }
}

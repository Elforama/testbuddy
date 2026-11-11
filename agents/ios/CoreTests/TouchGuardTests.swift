import CoreGraphics
import Foundation
import TBTouchGuard
import XCTest
@testable import TBAgentCore

/// The probe's decision at touch-down (TBTouchGuard.h), with the frame the agent packed for it.
final class TouchGuardTests: XCTestCase {
    private func frame(_ r: CGRect) -> TBFrame {
        TBFrame(x: r.origin.x, y: r.origin.y, w: r.size.width, h: r.size.height)
    }

    /// Issue #103: "Shift" moves the target down 600 ms after its tap. The agent read the target at
    /// rest, then sent the touch as the move began: at touch-down its live frame had gone 2.94 pt
    /// down, the point still on it. The touch reached the app, SwiftUI's button (laid out where
    /// the move ends) did not take it, and the tap reported success. (The frames are from that
    /// touch on a simulator.) A target that moved between the read and the touch is moving: the
    /// touch must be withheld, so the host finds the target again once it is still.
    func testATargetThatMovedSinceTheReadIsWithheld() {
        let read = CGRect(x: 20, y: 470, width: 72.333333333333329, height: 34.333333333333314)
        let live = CGRect(x: 20, y: 472.93870544433594, width: 72.333333333333329, height: 34.333333333333314)
        XCTAssertTrue(tbWithholdTouch(frame(live), 56, 487, TouchGuard.pack(read)))
    }

    /// The same target, still: the touch goes through.
    func testAStillTargetIsTouched() {
        let read = CGRect(x: 20, y: 470, width: 72.333333333333329, height: 34.333333333333314)
        XCTAssertFalse(tbWithholdTouch(frame(read), 56, 487, TouchGuard.pack(read)))
    }

    /// A button pulsing around its middle (scaled 1.0 to 1.15, forever) changes size but not
    /// place: it stays tappable, as before.
    func testAPulsingTargetIsTouched() {
        let read = CGRect(x: 20, y: 200, width: 60, height: 40)
        let live = read.insetBy(dx: -4.5, dy: -3)
        XCTAssertFalse(tbWithholdTouch(frame(live), 50, 220, TouchGuard.pack(read)))
    }

    /// Without a frame from the agent (a retry that lets a target that never stops moving be
    /// tapped), only the point is checked, as before.
    func testWithoutAFrameOnlyThePointCounts() {
        let live = CGRect(x: 20, y: 472.94, width: 72.33, height: 34.33)
        XCTAssertFalse(tbWithholdTouch(frame(live), 56, 487, 0))
        XCTAssertTrue(tbWithholdTouch(frame(live), 56, 600, 0))
    }

    /// A touch off the target is withheld, armed or not.
    func testATouchOffTheTargetIsWithheld() {
        let read = CGRect(x: 20, y: 686, width: 72.33, height: 34.33)
        XCTAssertTrue(tbWithholdTouch(frame(read), 56, 487, TouchGuard.pack(read)))
    }

    /// The agent's packing (Swift) and the probe's (C) agree, to a quarter point.
    func testPackingRoundTrips() {
        for r in [CGRect(x: 20, y: 470, width: 72.333, height: 34.333), CGRect(x: -12.5, y: -3, width: 402, height: 874),
                  CGRect(x: 0, y: 0, width: 1366, height: 1024)] {
            let packed = TouchGuard.pack(r)
            XCTAssertNotEqual(packed, 0)
            XCTAssertEqual(packed, tbPackFrame(frame(r)))
            let f = tbUnpackFrame(packed)
            XCTAssertEqual(f.x, r.origin.x, accuracy: 0.125)
            XCTAssertEqual(f.y, r.origin.y, accuracy: 0.125)
            XCTAssertEqual(f.w, r.size.width, accuracy: 0.125)
            XCTAssertEqual(f.h, r.size.height, accuracy: 0.125)
        }
        XCTAssertEqual(TouchGuard.pack(.zero), 0)
        XCTAssertEqual(TouchGuard.pack(CGRect(x: 20, y: 9000, width: 10, height: 10)), 0)
        XCTAssertEqual(TouchGuard.pack(CGRect(x: CGFloat.nan, y: 0, width: 10, height: 10)), 0)
    }

    /// Issue #131: in CI, a tap on a SwiftUI dialog button went down on the button (its press
    /// showed: "the screen changed"), but the touch never ended normally: it was cancelled (the
    /// system ended it when its virtual digitizer went away, about 150 ms later), so the button
    /// did not act. The probe's receipt still said the touch was taken on the target, and the tap
    /// counted as done. A touch that ended cancelled reached no handler as a tap: its receipt
    /// must say so, and must not say it was taken.
    func testACancelledTouchIsNotReportedAsTaken() {
        let token = TouchGuard.nonce(counter: 7, idHash: 0xC0FFEE)
        let receipt = tbTouchReceipt(token, true, true)
        XCTAssertEqual(TouchGuard.receipt(receipt, token: token), .cancelled)
        XCTAssertEqual(receipt & TBReceiptTargeted, 0)
    }

    /// A touch that ended normally is taken, as before, on the target or not.
    func testATouchThatEndedIsTaken() {
        let token = TouchGuard.nonce(counter: 7, idHash: 0xC0FFEE)
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token, true, false), token: token), .ended(targeted: true))
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token, false, false), token: token), .ended(targeted: false))
        // Another touch's receipt says nothing about this one.
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token + 1, true, true), token: token), TouchGuard.Receipt.none)
    }

    /// Issue #131: a nonce never sets the receipt's flag bits, whatever its counter, so a flag
    /// is never read as part of a nonce, nor a nonce as a flag.
    func testANonceLeavesTheReceiptFlagsFree() {
        XCTAssertEqual(TouchGuard.targetedFlag, TBReceiptTargeted)
        XCTAssertEqual(TouchGuard.cancelledFlag, TBReceiptCancelled)
        var counter: UInt32 = UInt32.max - 3
        for _ in 0..<8 {
            counter = TouchGuard.next(counter)
            XCTAssertNotEqual(counter, 0)
            let token = TouchGuard.nonce(counter: counter, idHash: UInt32.max)
            XCTAssertEqual(token & (TBReceiptTargeted | TBReceiptCancelled), 0, "counter \(counter)")
        }
        XCTAssertEqual(TouchGuard.nonce(counter: UInt32.max, idHash: 0) & (TBReceiptTargeted | TBReceiptCancelled), 0)
    }
}

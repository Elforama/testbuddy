import CoreGraphics
import Foundation
import TBTouchGuard
import XCTest
@testable import TBAgentCore

final class TouchGuardTests: XCTestCase {
    private func frame(_ r: CGRect) -> TBFrame {
        TBFrame(x: r.origin.x, y: r.origin.y, w: r.size.width, h: r.size.height)
    }

    func testATargetThatMovedSinceTheReadIsWithheld() {
        let read = CGRect(x: 20, y: 470, width: 72.333333333333329, height: 34.333333333333314)
        let live = CGRect(x: 20, y: 472.93870544433594, width: 72.333333333333329, height: 34.333333333333314)
        XCTAssertTrue(tbWithholdTouch(frame(live), 56, 487, TouchGuard.pack(read)))
    }

    func testAStillTargetIsTouched() {
        let read = CGRect(x: 20, y: 470, width: 72.333333333333329, height: 34.333333333333314)
        XCTAssertFalse(tbWithholdTouch(frame(read), 56, 487, TouchGuard.pack(read)))
    }

    func testAPulsingTargetIsTouched() {
        let read = CGRect(x: 20, y: 200, width: 60, height: 40)
        let live = read.insetBy(dx: -4.5, dy: -3)
        XCTAssertFalse(tbWithholdTouch(frame(live), 50, 220, TouchGuard.pack(read)))
    }

    func testWithoutAFrameOnlyThePointCounts() {
        let live = CGRect(x: 20, y: 472.94, width: 72.33, height: 34.33)
        XCTAssertFalse(tbWithholdTouch(frame(live), 56, 487, 0))
        XCTAssertTrue(tbWithholdTouch(frame(live), 56, 600, 0))
    }

    func testATouchOffTheTargetIsWithheld() {
        let read = CGRect(x: 20, y: 686, width: 72.33, height: 34.33)
        XCTAssertTrue(tbWithholdTouch(frame(read), 56, 487, TouchGuard.pack(read)))
    }

    func testWhyATouchIsWithheld() {
        let read = CGRect(x: 20, y: 470, width: 72.333333333333329, height: 34.333333333333314)
        let moved = CGRect(x: 20, y: 472.93870544433594, width: 72.333333333333329, height: 34.333333333333314)
        XCTAssertEqual(tbWithholdReason(frame(moved), 56, 487, TouchGuard.pack(read)), TBWithheldMoved)
        XCTAssertEqual(tbWithholdReason(frame(read), 56, 487, TouchGuard.pack(read)), TBWithheldNone)
        let away = CGRect(x: 20, y: 686, width: 72.33, height: 34.33)
        XCTAssertEqual(tbWithholdReason(frame(away), 56, 487, TouchGuard.pack(read)), TBWithheldOffTarget)
        XCTAssertEqual(tbWithholdReason(frame(away), 56, 487, 0), TBWithheldOffTarget)
    }

    func testARejectionSaysWhetherTheTargetMoved() {
        let token = TouchGuard.nonce(counter: 7, idHash: 0xC0FFEE)
        XCTAssertEqual(TouchGuard.rejection(tbRejection(token, true), token: token), .moved)
        XCTAssertEqual(TouchGuard.rejection(tbRejection(token, false), token: token), .offTarget)
        XCTAssertEqual(TouchGuard.rejection(tbRejection(token + 1, true), token: token), TouchGuard.Rejection.none)
        XCTAssertEqual(TouchGuard.rejection(0, token: token), TouchGuard.Rejection.none)
        XCTAssertEqual(TouchGuard.movedFlag, TBRejectedMoved)
        XCTAssertEqual(token & TBRejectedMoved, 0)
    }

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

    func testACancelledTouchIsNotReportedAsTaken() {
        let token = TouchGuard.nonce(counter: 7, idHash: 0xC0FFEE)
        let receipt = tbTouchReceipt(token, true, true)
        XCTAssertEqual(TouchGuard.receipt(receipt, token: token), .cancelled)
        XCTAssertEqual(receipt & TBReceiptTargeted, 0)
    }

    func testATouchThatEndedIsTaken() {
        let token = TouchGuard.nonce(counter: 7, idHash: 0xC0FFEE)
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token, true, false), token: token), .ended(targeted: true))
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token, false, false), token: token), .ended(targeted: false))
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token + 1, true, true), token: token), TouchGuard.Receipt.none)
    }

    func testTheTouchFieldSaysHowTheTouchEnded() {
        let token = TouchGuard.nonce(counter: 7, idHash: 0xC0FFEE)
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token, true, false), token: token).word, "ended")
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token, false, false), token: token).word, "ended")
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token, true, true), token: token).word, "cancelled")
        XCTAssertEqual(TouchGuard.receipt(tbTouchReceipt(token + 1, true, false), token: token).word, "none")
    }

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

    func testSolidChangeSinceTheHostsRead() {
        var mark = TouchGuard.SolidMark()
        XCTAssertFalse(mark.changed(since: 0), "no change at all")
        mark.note(seq: 1, restless: false)
        mark.note(seq: 2, restless: true)
        mark.note(seq: 3, restless: true)
        XCTAssertFalse(mark.changed(since: 1), "only a ticker's changes after the read")
        XCTAssertFalse(mark.changed(since: 3), "nothing after the read")
        XCTAssertTrue(mark.changed(since: 0), "a solid change after the read")
        mark.note(seq: 4, restless: false)
        XCTAssertTrue(mark.changed(since: 3))
        mark.note(seq: 5, restless: true)
        XCTAssertTrue(mark.changed(since: 3), "a later ticker change keeps the solid one")
        XCTAssertFalse(mark.changed(since: 4))
    }

    private let screenW = 402.0, screenH = 874.0
    private let button = TBFrame(x: 16, y: 733, w: 139, h: 38)

    func testTheButtonsOwnViewIsTheTarget() {
        XCTAssertTrue(tbViewIsTarget(TBFrame(x: 16.33, y: 732.67, w: 138.67, h: 38.33), button, screenW, screenH))
    }

    func testALabelInsideIsNotItself() {
        XCTAssertFalse(tbViewIsTarget(TBFrame(x: 30, y: 742, w: 110, h: 20), button, screenW, screenH))
    }

    func testAVeilAndItsSpinnerAreNotTheTarget() {
        XCTAssertFalse(tbViewIsTarget(TBFrame(x: 0, y: 0, w: screenW, h: screenH), button, screenW, screenH))
        let middle = TBFrame(x: 51, y: 412, w: 300, h: 50)
        XCTAssertFalse(tbViewIsTarget(TBFrame(x: 182.5, y: 418.5, w: 37, h: 37), middle, screenW, screenH))
    }

    func testABigTargetTellsNothing() {
        let big = TBFrame(x: 0, y: 0, w: screenW, h: screenH * 0.6)
        XCTAssertFalse(tbViewIsTarget(big, big, screenW, screenH))
    }

    private func view(_ f: TBFrame, element: Bool = false, concealed: Bool = false) -> TBChainView {
        TBChainView(frame: f, element: element, concealed: concealed)
    }
    private func reached(_ chain: [TBChainView]) -> Bool {
        tbTouchReachedTarget(chain, Int32(chain.count), button, screenW, screenH)
    }
    private var screen: TBFrame { TBFrame(x: 0, y: 0, w: screenW, h: screenH) }
    private let row = TBFrame(x: 0, y: 700, w: 402, h: 100)

    func testTheButtonItselfIsReached() {
        XCTAssertTrue(reached([view(button, element: true), view(row), view(screen)]))
    }

    func testALabelInsideTheButtonIsReached() {
        let label = TBFrame(x: 30, y: 742, w: 110, h: 20)
        XCTAssertTrue(reached([view(label, element: true), view(button, element: true), view(row)]))
        XCTAssertTrue(reached([view(label), view(button, element: true), view(row)]))
    }

    func testAVeilInsideAContainerTheButtonsSizeIsNotReached() {
        XCTAssertFalse(reached([view(button, concealed: true), view(button), view(row), view(screen)]))
        XCTAssertFalse(reached([view(button), view(button), view(row), view(screen)]),
                       "a veil that is only no element, not marked hidden")
    }

    func testASiblingOverlayTheButtonsSizeIsNotReached() {
        XCTAssertFalse(reached([view(button, concealed: true), view(row), view(screen)]))
        XCTAssertFalse(reached([view(button), view(row), view(screen)]))
    }

    func testAConcealedViewOnTheWayIsNotReached() {
        let label = TBFrame(x: 30, y: 742, w: 110, h: 20)
        XCTAssertFalse(reached([view(label, concealed: true), view(button, element: true)]))
    }

    func testAHostingViewIsNotReached() {
        XCTAssertFalse(reached([view(screen), view(screen)]))
    }

    func testAWatchedTouchIsArmedWithTheProbesMarker() {
        XCTAssertEqual(TouchGuard.watchedTouch, TBWatchedTouch)
        XCTAssertEqual(TouchGuard.watchFrame([16, 733, 155, 771]), CGRect(x: 16, y: 733, width: 139, height: 38))
        XCTAssertNil(TouchGuard.watchFrame([16, 733, 16, 771]))
        XCTAssertNil(TouchGuard.watchFrame([]))
    }
}

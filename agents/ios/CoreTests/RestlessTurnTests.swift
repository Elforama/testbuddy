import TBTouchGuard
import XCTest

/// The probe's restless verdict for a run loop turn (TBTouchGuard.h tbTurnEnd), from the
/// changes and touches TBProbe.m notes in it.
final class RestlessTurnTests: XCTestCase {
    private let ms: UInt64 = 1_000_000

    /// Issue #309: on the bench's "Tap, then away" screen, the counter's SwiftUI text layer
    /// changed all through two rotations in a row (landscape, then portrait: 521 ms of changes,
    /// gaps under 400 ms), so the probe found it restless and tagged it on its next change: the
    /// tap's new count, 381 ms after the rotation's last change. That turn's only change was the
    /// tagged layer, it went out restless, and the agent waited out the tap's 1 s no-change window
    /// for a tap that had worked. A turn that handles a touch, with a source that had been still
    /// for more than a frame, is the touch's change, whatever the source did before.
    func testATouchedSourceThatHadStoppedIsAChange() {
        var turn = TBTurn()
        let rotationEnd: UInt64 = 10_000 * ms
        let tap = rotationEnd + 381 * ms
        tbTurnNoteTouch(&turn)
        tbTurnNoteChange(&turn, rotationEnd, tap)
        XCTAssertFalse(tbTurnEnd(&turn, true), "the tap's change went out restless")
    }

    /// A source that changes frame by frame (an endless animation) changes in every turn, the
    /// touch's too: on its own, that is no change of the touch's.
    func testAnEndlessAnimationInATouchedTurnStaysRestless() {
        var turn = TBTurn()
        let now: UInt64 = 10_000 * ms
        tbTurnNoteTouch(&turn)
        tbTurnNoteChange(&turn, now - 17 * ms, now)
        XCTAssertTrue(tbTurnEnd(&turn, true))
    }

    /// With no touch, a restless source that changes after a pause (a ticker on a screen that
    /// comes back) stays restless: it is known as one.
    func testARestlessSourceAfterAPauseWithNoTouchStaysRestless() {
        var turn = TBTurn()
        let now: UInt64 = 10_000 * ms
        tbTurnNoteChange(&turn, now - 2_000 * ms, now)
        XCTAssertTrue(tbTurnEnd(&turn, true))
        tbTurnNoteChange(&turn, 0, now)
        XCTAssertTrue(tbTurnEnd(&turn, true))
    }

    /// A turn's touch is that turn's only: the verdict starts the next turn afresh, so a later
    /// tick of a ticker is not taken for the touch's change.
    func testATouchCountsForItsOwnTurnOnly() {
        var turn = TBTurn()
        let now: UInt64 = 10_000 * ms
        tbTurnNoteTouch(&turn)
        XCTAssertFalse(tbTurnEnd(&turn, false))
        tbTurnNoteChange(&turn, now - 500 * ms, now)
        XCTAssertTrue(tbTurnEnd(&turn, true))
    }

    /// A source with no restless verdict is a change, touch or not.
    func testASourceThatIsNotRestlessIsAChange() {
        var turn = TBTurn()
        tbTurnNoteChange(&turn, 0, 10_000 * ms)
        XCTAssertFalse(tbTurnEnd(&turn, false))
    }
}

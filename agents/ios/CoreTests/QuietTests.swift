import XCTest
@testable import TBAgentCore

/// When the app's idle answer settles the UI (Events.quietFired).
final class QuietTests: XCTestCase {
    func testIdleAnswerWithNothingChangedSettles() {
        let arm = QuietArm(gen: 3, solid: 10)
        XCTAssertTrue(arm.settles(gen: 3, solid: 10))
    }

    func testIdleAnswerAfterAReArmDoesNotSettle() {
        XCTAssertFalse(QuietArm(gen: 3, solid: 10).settles(gen: 4, solid: 11))
    }

    // Issue #170: a change counts at once (the solid count, under the events lock) but re-arms
    // the timer only later, from the events queue. An idle answer handled in between (a
    // popover's first commit as a main-thread hang ends, with the app's answer to the timer
    // armed before the hang already queued) settled the UI with that change in it: the host's
    // wait for a still screen returned at once, and its tap landed as the popover began to
    // present and was dropped. That answer must not settle: the change's own timer will.
    func testChangeCountedButNotYetReArmedDoesNotSettle() {
        XCTAssertFalse(QuietArm(gen: 3, solid: 10).settles(gen: 3, solid: 11))
    }

    // Issue #170 review: an arming carries its own change's count. Change 10 re-arms while
    // change 11 is counted but not re-armed yet: the idle answer to that arming must not
    // settle with 11 in it; 11's own arming does.
    func testArmingCarriesItsOwnChangesCount() {
        let first = QuietArm.next(after: nil, solid: 10)
        XCTAssertEqual(first, QuietArm(gen: 1, solid: 10))
        XCTAssertFalse(first.settles(gen: first.gen, solid: 11))
        let second = QuietArm.next(after: first, solid: 11)
        XCTAssertEqual(second, QuietArm(gen: 2, solid: 11))
        XCTAssertTrue(second.settles(gen: second.gen, solid: 11))
    }

    // Two changes counted on two threads (the events queue, and an op's read fallback) can
    // re-arm out of order. The late arming for the earlier change keeps the later count, so
    // the UI still settles.
    func testArmingOutOfOrderKeepsTheLaterCount() {
        let later = QuietArm.next(after: nil, solid: 12)
        let late = QuietArm.next(after: later, solid: 11)
        XCTAssertEqual(late, QuietArm(gen: 2, solid: 12))
        XCTAssertTrue(late.settles(gen: late.gen, solid: 12))
    }
}

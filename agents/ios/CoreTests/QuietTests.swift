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
}

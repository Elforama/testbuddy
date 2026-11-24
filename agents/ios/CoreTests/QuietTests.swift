import XCTest
@testable import TBAgentCore

final class QuietTests: XCTestCase {
    func testIdleAnswerWithNothingChangedSettles() {
        let arm = QuietArm(gen: 3, solid: 10)
        XCTAssertTrue(arm.settles(gen: 3, solid: 10))
    }

    func testIdleAnswerAfterAReArmDoesNotSettle() {
        XCTAssertFalse(QuietArm(gen: 3, solid: 10).settles(gen: 4, solid: 11))
    }

    func testChangeCountedButNotYetReArmedDoesNotSettle() {
        XCTAssertFalse(QuietArm(gen: 3, solid: 10).settles(gen: 3, solid: 11))
    }

    func testArmingCarriesItsOwnChangesCount() {
        let first = QuietArm.next(after: nil, solid: 10)
        XCTAssertEqual(first, QuietArm(gen: 1, solid: 10))
        XCTAssertFalse(first.settles(gen: first.gen, solid: 11))
        let second = QuietArm.next(after: first, solid: 11)
        XCTAssertEqual(second, QuietArm(gen: 2, solid: 11))
        XCTAssertTrue(second.settles(gen: second.gen, solid: 11))
    }

    func testArmingOutOfOrderKeepsTheLaterCount() {
        let later = QuietArm.next(after: nil, solid: 12)
        let late = QuietArm.next(after: later, solid: 11)
        XCTAssertEqual(late, QuietArm(gen: 2, solid: 12))
        XCTAssertTrue(late.settles(gen: late.gen, solid: 12))
    }
}

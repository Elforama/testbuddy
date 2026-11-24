import XCTest
@testable import TBAgentCore

final class LaunchWaitTests: XCTestCase {
    private let support = "com.apple.dt.xctest.automation-support.error"

    private func mainWindow(_ cause: String) -> String {
        #"Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window \#(cause)""#
    }

    func testNoForegroundWindowYetIsWaitedOn() {
        XCTAssertEqual(LaunchWait.classify(domain: "com.apple.dt.xctest.ui-testing.error", code: 10001, text: "anything", ci: true), .notReadyYet)
    }

    func testOutsideCIAccessibilityNotAnsweringYetEndsTheWait() {
        for cause in ["kAXErrorIPCTimeout", "kAXErrorServerNotFound", "kAXErrorCannotComplete"] {
            XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: mainWindow(cause), ci: false), .fatal, cause)
        }
        XCTAssertEqual(LaunchWait.classify(domain: "com.apple.dt.xctest.ui-testing.error", code: 10001, text: "anything", ci: false), .notReadyYet)
    }

    func testAccessibilityNotAnsweringYetIsWaitedOn() {
        for cause in ["kAXErrorIPCTimeout", "kAXErrorServerNotFound", "kAXErrorCannotComplete"] {
            XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: mainWindow(cause), ci: true), .notAnswering(cause: cause), cause)
        }
    }

    func testOtherFailuresEndTheWait() {
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: mainWindow("kAXErrorInvalidUIElement"), ci: true), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: mainWindow("kAXErrorAPIDisabled"), ci: true), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: "kAXErrorServerNotFound", ci: true), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 7, text: mainWindow("kAXErrorServerNotFound"), ci: true), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: "NSCocoaErrorDomain", code: 8, text: mainWindow("kAXErrorServerNotFound"), ci: true), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: "com.apple.dt.xctest.ui-testing.error", code: 10002, text: "x", ci: true), .fatal)
    }

    func testCauseInText() {
        XCTAssertEqual(LaunchWait.notAnsweringCause("launch of a pid 1 had no readable tree within 30000 ms: Error getting main window kAXErrorServerNotFound"), "kAXErrorServerNotFound")
        XCTAssertNil(LaunchWait.notAnsweringCause("launched process a pid 1 exited before the first tree"))
    }

    func testAgentErrorsAreNotTransientUnlessSaid() {
        XCTAssertFalse(AgentError("x").transient)
        XCTAssertTrue(AgentError("x", transient: true).transient)
    }
}

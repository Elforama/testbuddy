import XCTest
@testable import TBAgentCore

/// Which failed reads of a just-launched app's first tree the agent keeps waiting on.
final class LaunchWaitTests: XCTestCase {
    private let support = "com.apple.dt.xctest.automation-support.error"

    private func mainWindow(_ cause: String) -> String {
        #"Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window \#(cause)""#
    }

    func testNoForegroundWindowYetIsWaitedOn() {
        XCTAssertEqual(LaunchWait.classify(domain: "com.apple.dt.xctest.ui-testing.error", code: 10001, text: "anything"), .notReadyYet)
    }

    /// Issue #85 (kAXErrorIPCTimeout) and issue #126 (kAXErrorServerNotFound): the app is
    /// alive but its accessibility has not answered yet.
    func testAccessibilityNotAnsweringYetIsWaitedOn() {
        for cause in ["kAXErrorIPCTimeout", "kAXErrorServerNotFound", "kAXErrorCannotComplete"] {
            XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: mainWindow(cause)), .notAnswering(cause: cause), cause)
        }
    }

    func testOtherFailuresEndTheWait() {
        // Another accessibility cause: the window is gone, or accessibility is off.
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: mainWindow("kAXErrorInvalidUIElement")), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: mainWindow("kAXErrorAPIDisabled")), .fatal)
        // The cause outside "Error getting main window", or in another error.
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 8, text: "kAXErrorServerNotFound"), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: support, code: 7, text: mainWindow("kAXErrorServerNotFound")), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: "NSCocoaErrorDomain", code: 8, text: mainWindow("kAXErrorServerNotFound")), .fatal)
        XCTAssertEqual(LaunchWait.classify(domain: "com.apple.dt.xctest.ui-testing.error", code: 10002, text: "x"), .fatal)
    }

    /// The host matches the same family in the text of an agent that does not mark its errors.
    func testCauseInText() {
        XCTAssertEqual(LaunchWait.notAnsweringCause("launch of a pid 1 had no readable tree within 30000 ms: Error getting main window kAXErrorServerNotFound"), "kAXErrorServerNotFound")
        XCTAssertNil(LaunchWait.notAnsweringCause("launched process a pid 1 exited before the first tree"))
    }

    func testAgentErrorsAreNotTransientUnlessSaid() {
        XCTAssertFalse(AgentError("x").transient)
        XCTAssertTrue(AgentError("x", transient: true).transient)
    }
}

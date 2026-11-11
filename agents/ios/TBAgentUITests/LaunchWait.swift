// What the agent makes of a failed read of a just-launched app's first tree (awaitPackage with
// readTree). Some failures say only that the app is not ready or not answering accessibility
// yet: the agent keeps waiting on those, within the request's timeout. Every other failure is
// the launch's own and ends the wait at once. (A launched process that has exited is checked
// before every read and fails at once whatever the read said.)
import Foundation

enum LaunchWait {
    /// Accessibility causes in XCTest's "Error getting main window" (automation-support error 8)
    /// that mean the launched app has not answered yet: the query timed out (issue #85), the
    /// app's accessibility server is not up yet (issue #126), or the message to it could not
    /// complete. Other causes (an invalid element, accessibility turned off) are not waited on.
    static let notAnsweringYet = ["kAXErrorIPCTimeout", "kAXErrorServerNotFound", "kAXErrorCannotComplete"]

    /// After a not-answering error the read is tried again on the next event or after this many
    /// ms, whichever comes first: an app whose accessibility is not up may send no event.
    static let recheckMs = 250

    enum Failure: Equatable {
        /// XCTest's ui-testing error 10001: the app has no foreground window yet.
        case notReadyYet
        /// The app's accessibility has not answered yet (one of notAnsweringYet).
        case notAnswering(cause: String)
        /// The launch's own failure.
        case fatal
    }

    /// Classifies a read's error by its NSError domain and code and its text ("\(error)"). ci:
    /// the host's CI mode ("ci" on the request).
    static func classify(domain: String, code: Int, text: String, ci: Bool) -> Failure {
        if domain == "com.apple.dt.xctest.ui-testing.error" && code == 10001 { return .notReadyYet }
        // A CI allowance: an app whose accessibility has not answered yet (issues #85 and #126,
        // on busy runners) is waited on only in the host's CI mode; elsewhere it fails the
        // launch at once, as before.
        if ci, domain == "com.apple.dt.xctest.automation-support.error" && code == 8, let cause = notAnsweringCause(text) {
            return .notAnswering(cause: cause)
        }
        return .fatal
    }

    /// The cause in text when it is XCTest's "Error getting main window" with one of
    /// notAnsweringYet, else nil.
    static func notAnsweringCause(_ text: String) -> String? {
        guard text.contains("Error getting main window") else { return nil }
        return notAnsweringYet.first { text.contains($0) }
    }
}

import Foundation

enum LaunchWait {
    static let notAnsweringYet = ["kAXErrorIPCTimeout", "kAXErrorServerNotFound", "kAXErrorCannotComplete"]

    static let recheckMs = 250

    enum Failure: Equatable {
        case notReadyYet
        case notAnswering(cause: String)
        case fatal
    }

    static func classify(domain: String, code: Int, text: String, ci: Bool) -> Failure {
        if domain == "com.apple.dt.xctest.ui-testing.error" && code == 10001 { return .notReadyYet }
        if ci, domain == "com.apple.dt.xctest.automation-support.error" && code == 8, let cause = notAnsweringCause(text) {
            return .notAnswering(cause: cause)
        }
        return .fatal
    }

    static func notAnsweringCause(_ text: String) -> String? {
        guard text.contains("Error getting main window") else { return nil }
        return notAnsweringYet.first { text.contains($0) }
    }
}

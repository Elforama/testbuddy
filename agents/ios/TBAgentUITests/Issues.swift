// XCTest issues raised inside an op (issue #286): an op never answers success over an XCTest
// failure raised inside it.
//
// Some XCTest calls report a failure only as an XCTIssue, never by throwing: XCUIDevice's
// button presses and orientation, XCUIElement.tap(), an app's launch and activation. The
// agent records no issue (one would end its single test, and the agent with it:
// TBAgentUITests.record), so before #286 it only logged them, and an op whose Home press XCTest
// had refused answered success. Now each op starts an empty sink (Ops.handle), every issue goes
// in it, and the op fails on a fatal one.
//
// Fatal is an allowlist, so harmless noise cannot fail an op. Seen in practice in the agent's
// simulator logs: XCTest's QoS warnings ("Thread running at User-initiated quality-of-service
// class waiting on a lower QoS thread …"), on most runs, and the refused Home press. XCTest's
// framework strings hold the rest: its failed actions read "Failed to <action>" and its reads'
// failures ("Failed to get matching snapshot", "Timed out snapshotting …") are ones the agent
// already deals with where it reads (its own snapshots throw; XCUIElementQuery reads such as the
// keyboard check fall back). Fatal: a device event or app lifecycle call that failed. Transient:
// testmanagerd refused the event outright ("only one gesture can be performed at a time"),
// so nothing took effect and asking again cannot press twice. Everything else is logged (and
// pushed to the trace when it is on), as before.
import Foundation

enum XCTestIssue: Equatable {
    case fatal(transient: Bool)
    case noise

    /// testmanagerd refused the event: it never happened.
    static let refused = ["only one gesture can be performed at a time"]

    /// XCTest's failures of a device event or an app's lifecycle ("Failed to <action>").
    static let fatalPrefixes = [
        "Failed to press ",               // XCUIDevice: Home, lock, volume buttons
        "Failed to synthesize event",     // XCUIElement.tap() and other synthesized events
        "Failed to set device orientation", "Failed to set orientation", "Failed to rotate",
        "Failed to launch",
        "Failed to activate application",
        "Failed to terminate",
    ]

    static func classify(_ text: String) -> XCTestIssue {
        if refused.contains(where: { text.contains($0) }) { return .fatal(transient: true) }
        if fatalPrefixes.contains(where: { text.hasPrefix($0) }) { return .fatal(transient: false) }
        return .noise
    }
}

/// The issues XCTest raised during the current op. XCTest raises them on any thread.
final class IssueSink {
    private let lock = NSLock()
    private var fatal: (text: String, transient: Bool)?

    init() {}

    /// A new op starts: earlier issues are forgotten.
    func begin() {
        lock.lock(); fatal = nil; lock.unlock()
    }

    /// An issue XCTest raised: kept when fatal (the first one). Returns its kind.
    @discardableResult func note(_ text: String) -> XCTestIssue {
        let kind = XCTestIssue.classify(text)
        if case .fatal(let transient) = kind {
            lock.lock()
            if fatal == nil { fatal = (text, transient) }
            lock.unlock()
        }
        return kind
    }

    /// The op's error when a fatal issue was raised during it (doing: what the op was doing,
    /// "pressing key 3"), nil otherwise.
    func failure(_ doing: String) -> AgentError? {
        lock.lock(); defer { lock.unlock() }
        guard let fatal else { return nil }
        let outcome = fatal.transient ? "; nothing was sent" : ""
        return AgentError("\(doing): XCTest reported: \(fatal.text)\(outcome)", transient: fatal.transient)
    }
}

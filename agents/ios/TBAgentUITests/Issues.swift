import Foundation

enum XCTestIssue: Equatable {
    case fatal(transient: Bool)
    case noise

    static let refused = ["only one gesture can be performed at a time"]

    static let fatalPrefixes = [
        "Failed to press ",
        "Failed to synthesize event",
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

final class IssueSink {
    struct Noted {
        let kind: XCTestIssue
        let stale: Bool
    }

    private let lock = NSLock()
    private var generation: UInt64 = 0
    private var active: UInt64?
    private var fatal: (text: String, transient: Bool)?

    init() {}

    @discardableResult func begin() -> UInt64 {
        lock.lock(); defer { lock.unlock() }
        generation += 1
        fatal = nil
        return generation
    }

    var current: UInt64 { lock.lock(); defer { lock.unlock() }; return generation }

    func within<T>(_ gen: UInt64, _ f: () throws -> T) rethrows -> T {
        lock.lock(); let before = active; active = gen; lock.unlock()
        defer { lock.lock(); active = before; lock.unlock() }
        return try f()
    }

    @discardableResult func note(_ text: String) -> Noted {
        let kind = XCTestIssue.classify(text)
        lock.lock(); defer { lock.unlock() }
        let stale = active.map { $0 != generation } ?? false
        if case .fatal(let refused) = kind, !stale, fatal == nil {
            fatal = (text, refused && active == generation)
        }
        return Noted(kind: kind, stale: stale)
    }

    func failure(_ doing: String) -> AgentError? {
        lock.lock(); defer { lock.unlock() }
        guard let fatal else { return nil }
        let outcome = fatal.transient ? "; nothing was sent" : ""
        return AgentError("\(doing): XCTest reported: \(fatal.text)\(outcome)", transient: fatal.transient)
    }
}

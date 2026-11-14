// Which app is in front, and which app a screen read reads (Apps.foreground, Tree.windows).
// Kept apart from XCTest so its decisions are unit-tested (swift test).
import Foundation

enum Front {
    static let springboard = "com.apple.springboard"

    struct App: Equatable {
        let pid: Int32
        let bundle: String
        init(_ pid: Int32, _ bundle: String) { self.pid = pid; self.bundle = bundle }
    }

    /// The app in front among the accessibility client's active applications (front-most first,
    /// bundle ids "" where unknown): an app other than SpringBoard, those SpringBoard reported
    /// in the background dropped while that leaves any, XCTest's state deciding between several
    /// (isForeground); SpringBoard when it is the only one. nil when the list is empty: the
    /// query failed (issue #142: kAXErrorIPCTimeout on a starved simulator), since a client that
    /// answers always lists SpringBoard at least.
    static func pick(active: [App], background: Set<Int32>, springboardPid: Int32,
                     isForeground: ([App]) -> App?) -> App? {
        if active.isEmpty { return nil }
        var candidates = active.filter { $0.bundle != springboard && !$0.bundle.isEmpty }
        let running = candidates.filter { !background.contains($0.pid) }
        if !running.isEmpty { candidates = running }
        if candidates.count > 1 { return isForeground(candidates) ?? candidates.first }
        if let one = candidates.first { return one }
        // SpringBoard, even when its bundle id could not be looked up.
        if let sb = active.first(where: { $0.bundle == springboard || ($0.pid == springboardPid && springboardPid != 0) }) {
            return App(sb.pid, springboard)
        }
        return nil
    }

    /// What a screen read reads.
    enum Read: Equatable {
        /// An app other than SpringBoard: its windows (and a SpringBoard alert over it, if any).
        case app(App)
        /// SpringBoard alone in front: its home screen, its app switcher, or one of its alerts.
        case springboard(pid: Int32)
        /// Which app is in front is not known (the query failed, issue #142): the last app seen
        /// in front, still running in the foreground. Never cached: the next read asks again.
        case lastApp(App)
        /// Not known, and no app was seen in front: nothing can be read (a launch's first-tree
        /// wait asks again).
        case unknown
    }

    /// How many times a read asks again which app is in front, when the first ask failed and no
    /// app is known to be in front.
    static let requeries = 2

    /// What a read reads, given the app picked as in front (nil or "": the query failed),
    /// the last app other than SpringBoard seen in front if it still runs in the foreground
    /// (nil otherwise), and requery, which asks again (nil: failed again).
    ///
    /// A failed query with an app known in front reads that app, not SpringBoard: in #142 the
    /// agent read SpringBoard's switcher (the app as a card) while the app was in front, and
    /// an assertion waited 28.6 s on SpringBoard's tree. With no app known (after pressKey home
    /// or stopApp, SpringBoard really is in front), the read asks again up to `requeries` times
    /// and then reads SpringBoard, as before: a read that works must not fail. In a launch's
    /// first-tree wait (launching), it asks nothing more: the wait asks again itself.
    ///
    /// A CI allowance (the host's CI mode, ci: "ci" on the tree request): #142 was seen on a CI
    /// simulator only. Outside CI mode a failed query reads SpringBoard at once, asking nothing
    /// more, as before the fix.
    static func read(front: App?, last: App?, launching: Bool = false, ci: Bool = false, requery: () -> App? = { nil }) -> Read {
        func known(_ a: App?) -> Read? {
            guard let a, !a.bundle.isEmpty else { return nil }
            return a.bundle == springboard ? .springboard(pid: a.pid) : .app(a)
        }
        if let r = known(front) { return r }
        if !ci { return .springboard(pid: 0) }
        if let last, !last.bundle.isEmpty, last.bundle != springboard { return .lastApp(last) }
        if launching { return .unknown }
        for _ in 0..<requeries {
            if let r = known(requery()) { return r }
        }
        return .springboard(pid: 0)
    }
}

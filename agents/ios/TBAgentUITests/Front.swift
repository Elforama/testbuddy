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
        /// in front, if it still runs. Never cached: the next read asks again.
        case lastApp(App)
        /// Not known, and no app was seen in front: nothing can be read.
        case unknown
    }

    /// What to read, given the app picked as in front (nil: not known) and the last app other
    /// than SpringBoard seen in front that still runs. Not knowing is never SpringBoard alone:
    /// in #142 that read SpringBoard's switcher (the app as a card) while the app was in front,
    /// and a 17 s assertion waited it out on SpringBoard's tree.
    static func read(front: App?, last: App?) -> Read {
        guard let front, !front.bundle.isEmpty else {
            if let last, !last.bundle.isEmpty, last.bundle != springboard { return .lastApp(last) }
            return .unknown
        }
        if front.bundle == springboard { return .springboard(pid: front.pid) }
        return .app(front)
    }
}

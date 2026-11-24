import Foundation

enum Front {
    static let springboard = "com.apple.springboard"

    struct App: Equatable {
        let pid: Int32
        let bundle: String
        init(_ pid: Int32, _ bundle: String) { self.pid = pid; self.bundle = bundle }
    }

    static func pick(active: [App], background: Set<Int32>, springboardPid: Int32,
                     isForeground: ([App]) -> App?) -> App? {
        if active.isEmpty { return nil }
        var candidates = active.filter { $0.bundle != springboard && !$0.bundle.isEmpty }
        let running = candidates.filter { !background.contains($0.pid) }
        if !running.isEmpty { candidates = running }
        if candidates.count > 1 { return isForeground(candidates) ?? candidates.first }
        if let one = candidates.first { return one }
        if let sb = active.first(where: { $0.bundle == springboard || ($0.pid == springboardPid && springboardPid != 0) }) {
            return App(sb.pid, springboard)
        }
        return nil
    }

    enum Read: Equatable {
        case app(App)
        case springboard(pid: Int32)
        case lastApp(App)
        case unknown
    }

    static let requeries = 2

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

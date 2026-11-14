// What the agent keeps per process (by pid). Kept apart from XCTest so it is unit-tested
// (swift test).
//
// Pids are reused. Issue #53: in a long-lived runner, a new process with an old one's pid
// inherited what was kept for the old one (an app the probe never loaded counted as probed, so
// its accessibility changes were ignored and the read-after-action check skipped). The issue
// asked to forget a pid when testmanagerd reports its app not running, but on Xcode 27 no app
// state update comes at all (checked on a simulator, for apps simctl and XCTest launch), and
// SpringBoard reports an app that ended as "suspended", as one sent to the background. So each
// entry is kept with its process's start time, and is let go of once the pid names another
// process, or none.
import Darwin
import Foundation

/// Which process a pid names: its start time (sysctl KERN_PROC_PID, µs), nil when no process
/// has that pid. Simulator apps are processes of the Mac, which the runner can look up.
enum ProcessStart {
    static func of(_ pid: Int32) -> UInt64? {
        guard pid > 0 else { return nil }
        var info = kinfo_proc()
        var size = MemoryLayout<kinfo_proc>.stride
        var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PID, pid]
        guard sysctl(&mib, 4, &info, &size, nil, 0) == 0, size > 0 else { return nil }
        let t = info.kp_proc.p_starttime
        return UInt64(t.tv_sec) * 1_000_000 + UInt64(t.tv_usec)
    }
}

/// Values kept per process, under a lock (any thread). An entry is the process's while its pid
/// names a process started when the entry was made; a lookup that fails (nil) both times keeps
/// it, as before issue #53.
final class PerPid<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var values: [Int32: (start: UInt64?, value: Value)] = [:]
    private let startOf: (Int32) -> UInt64?
    private let keepEnded: Bool

    /// keepEnded: an entry outlives its process (Apps.background: an app that ended counts as
    /// not in front). startOf: ProcessStart.of, replaced in tests.
    init(keepEnded: Bool = false, startOf: @escaping (Int32) -> UInt64? = ProcessStart.of) {
        self.keepEnded = keepEnded
        self.startOf = startOf
    }

    /// Whether an entry made when pid's process started at `start` is still that process's.
    /// Locked.
    private func current(_ pid: Int32, _ start: UInt64?) -> (ok: Bool, now: UInt64?) {
        let now = startOf(pid)
        return (now == start || (keepEnded && now == nil), now)
    }

    /// pid's entry if it is still its process's; one that is not is let go of. Locked.
    private func entry(_ pid: Int32) -> Value? {
        guard let e = values[pid] else { return nil }
        if current(pid, e.start).ok { return e.value }
        values.removeValue(forKey: pid)
        return nil
    }

    subscript(pid: Int32) -> Value? {
        lock.lock(); defer { lock.unlock() }
        return entry(pid)
    }

    /// Reads and changes pid's value in one step (nil: none, or another process's).
    @discardableResult
    func update<R>(_ pid: Int32, _ change: (inout Value?) -> R) -> R {
        lock.lock(); defer { lock.unlock() }
        var v = entry(pid)
        // A new entry takes the start of whichever process has the pid now: a late notification
        // from an ended process could be credited to a new one, which needs the pid reused within
        // milliseconds (accepted).
        let start = values[pid]?.start ?? startOf(pid)
        let r = change(&v)
        if let v { values[pid] = (start, v) } else { values.removeValue(forKey: pid) }
        return r
    }

    /// The pids with a value that is still their process's.
    var pids: [Int32] {
        lock.lock(); defer { lock.unlock() }
        return values.keys.filter { entry($0) != nil }
    }

    func forget(_ pid: Int32) {
        lock.lock(); defer { lock.unlock() }
        values.removeValue(forKey: pid)
    }
}

/// A process as it was seen (Apps' last app in front).
struct SeenProcess: Equatable {
    let pid: Int32
    let bundle: String
    let start: UInt64?

    init(pid: Int32, bundle: String, startOf: (Int32) -> UInt64? = ProcessStart.of) {
        self.pid = pid
        self.bundle = bundle
        self.start = startOf(pid)
    }

    /// Whether pid still names this process.
    func isCurrent(startOf: (Int32) -> UInt64? = ProcessStart.of) -> Bool { startOf(pid) == start }
}

/// Which process a set of per-process registrations (ControlReceipts' notify tokens) is made
/// for. Issue #53: one that failed is not taken for done, and is tried again on the next use.
struct PidRegistration {
    private(set) var pid: Int32 = 0

    /// Registers for pid unless that is done (register: whether it worked). Whether it is.
    mutating func ensure(_ pid: Int32, register: () -> Bool) -> Bool {
        if self.pid == pid && pid != 0 { return true }
        let ok = register()
        self.pid = ok ? pid : 0
        return ok
    }
}

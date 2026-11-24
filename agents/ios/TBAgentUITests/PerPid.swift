import Darwin
import Foundation

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

final class PerPid<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var values: [Int32: (start: UInt64?, value: Value)] = [:]
    private let startOf: (Int32) -> UInt64?
    private let keepEnded: Bool

    init(keepEnded: Bool = false, startOf: @escaping (Int32) -> UInt64? = ProcessStart.of) {
        self.keepEnded = keepEnded
        self.startOf = startOf
    }

    private func current(_ pid: Int32, _ start: UInt64?) -> (ok: Bool, now: UInt64?) {
        let now = startOf(pid)
        return (now == start || (keepEnded && now == nil), now)
    }

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

    @discardableResult
    func update<R>(_ pid: Int32, _ change: (inout Value?) -> R) -> R {
        lock.lock(); defer { lock.unlock() }
        var v = entry(pid)
        let start = values[pid]?.start ?? startOf(pid)
        let r = change(&v)
        if let v { values[pid] = (start, v) } else { values.removeValue(forKey: pid) }
        return r
    }

    var pids: [Int32] {
        lock.lock(); defer { lock.unlock() }
        return values.keys.filter { entry($0) != nil }
    }

    func forget(_ pid: Int32) {
        lock.lock(); defer { lock.unlock() }
        values.removeValue(forKey: pid)
    }
}

struct SeenProcess: Equatable {
    let pid: Int32
    let bundle: String
    let start: UInt64?

    init(pid: Int32, bundle: String, startOf: (Int32) -> UInt64? = ProcessStart.of) {
        self.pid = pid
        self.bundle = bundle
        self.start = startOf(pid)
    }

    func isCurrent(startOf: (Int32) -> UInt64? = ProcessStart.of) -> Bool { startOf(pid) == start }
}

struct PidRegistration {
    private(set) var pid: Int32 = 0

    mutating func ensure(_ pid: Int32, register: () -> Bool) -> Bool {
        if self.pid == pid && pid != 0 { return true }
        let ok = register()
        self.pid = ok ? pid : 0
        return ok
    }
}

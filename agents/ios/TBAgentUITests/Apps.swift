import XCTest

func onMain<T>(_ f: () throws -> T) rethrows -> T {
    if Thread.isMainThread { return try f() }
    return try DispatchQueue.main.sync(execute: f)
}

final class Apps {
    static let shared = Apps()
    static let springboard = "com.apple.springboard"

    private let lock = NSLock()
    private var dirty = true
    private var fgPid: Int32 = 0
    private var fgBundle = ""
    private var springboardPid: Int32 = 0
    private var fgProcess: NSObject?
    private var fgProcessPid: Int32 = -1
    private var sbProcess: NSObject?
    private var sbProcessPid: Int32 = 0
    private var last: SeenProcess?
    private let background = PerPid<Bool>(keepEnded: true)
    private var apps: [String: XCUIApplication] = [:]

    func invalidate() { lock.lock(); dirty = true; lock.unlock() }

    var foregroundBundleIfKnown: String { lock.lock(); defer { lock.unlock() }; return fgBundle }
    var foregroundPidIfKnown: Int32 { lock.lock(); defer { lock.unlock() }; return fgPid }
    var springboardPidIfKnown: Int32 { lock.lock(); defer { lock.unlock() }; return springboardPid }

    func springboardProcess() -> NSObject? {
        var pid = springboardPidIfKnown
        if pid == 0, let sb = onMain({ Private.systemApplicationPid() }) {
            lock.lock(); springboardPid = sb; lock.unlock()
            pid = sb
        }
        lock.lock()
        if let p = sbProcess, sbProcessPid == pid { lock.unlock(); return p }
        lock.unlock()
        let p: NSObject? = onMain { Private.process(of: app(Apps.springboard)) }
        lock.lock(); sbProcess = p; sbProcessPid = pid; lock.unlock()
        return p
    }

    func bundleIfKnown(pid: Int32) -> String {
        lock.lock(); defer { lock.unlock() }
        if pid == fgPid { return fgBundle }
        if pid == springboardPid { return Apps.springboard }
        return ""
    }

    func onScreen(pid: Int32) -> Bool {
        lock.lock(); defer { lock.unlock() }
        if dirty || fgPid == 0 || pid == 0 { return true }
        return pid == fgPid || pid == springboardPid
    }

    func isSpringboardBehind(pid: Int32) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return pid != 0 && pid == springboardPid && fgPid != springboardPid && fgPid != 0
    }

    func noteState(pid: Int32, bundle: String, state: UInt64) {
        lock.lock(); defer { lock.unlock() }
        if state == 4 { background.forget(pid) } else if pid > 0 { background.update(pid) { $0 = true } }
        dirty = true
    }

    func noteSuspended(pid: Int32, suspended: Bool) {
        lock.lock(); defer { lock.unlock() }
        if suspended {
            background.update(pid) { $0 = true }
            if pid == fgPid { dirty = true }
        } else {
            background.forget(pid)
            if pid != fgPid { dirty = true }
        }
    }

    func app(_ bundle: String) -> XCUIApplication {
        if let a = apps[bundle] { return a }
        let a = XCUIApplication(bundleIdentifier: bundle)
        apps[bundle] = a
        return a
    }

    func forgetProcess(_ bundle: String) {
        onMain { apps.removeValue(forKey: bundle) }
        lock.lock()
        dirty = true
        fgProcess = nil
        fgProcessPid = -1
        lock.unlock()
    }

    struct Foreground { let pid: Int32; let bundle: String; let all: [(pid: Int32, bundle: String)] }

    func foreground() -> Foreground {
        lock.lock()
        if !dirty && fgPid != 0 {
            let f = Foreground(pid: fgPid, bundle: fgBundle, all: [])
            lock.unlock()
            return f
        }
        dirty = false
        lock.unlock()
        let active = onMain { Private.activeApps() }.map { (pid: $0.pid, bundle: Private.bundleID(pid: $0.pid) ?? "") }
        lock.lock()
        let bg = Set(background.pids)
        let needSB = springboardPid == 0
        lock.unlock()
        if needSB, let sb = onMain({ Private.systemApplicationPid() }) { lock.lock(); springboardPid = sb; lock.unlock() }
        let pick = Front.pick(active: active.map { Front.App($0.pid, $0.bundle) }, background: bg,
                              springboardPid: springboardPidIfKnown) { candidates in
            onMain { candidates.first { app($0.bundle).state == .runningForeground } }
        }
        guard let pick else {
            lock.lock(); dirty = true; fgPid = 0; fgBundle = ""; lock.unlock()
            return Foreground(pid: 0, bundle: "", all: active)
        }
        let f = Foreground(pid: pick.pid, bundle: pick.bundle, all: active)
        lock.lock()
        fgPid = f.pid
        fgBundle = f.bundle
        background.forget(f.pid)
        for a in active where a.bundle == Apps.springboard { springboardPid = a.pid }
        if f.bundle != Apps.springboard && !f.bundle.isEmpty { last = SeenProcess(pid: f.pid, bundle: f.bundle) }
        if f.bundle == Apps.springboard, let l = last, background[l.pid] == nil, l.isCurrent() { dirty = true }
        lock.unlock()
        return f
    }

    func lastApp() -> (pid: Int32, bundle: String)? {
        lock.lock(); defer { lock.unlock() }
        guard let l = last, background[l.pid] == nil, l.isCurrent() else { return nil }
        return (l.pid, l.bundle)
    }

    func foregroundProcess() -> NSObject? {
        let f = foreground()
        guard f.pid != 0, !f.bundle.isEmpty, f.bundle != Apps.springboard else { return nil }
        lock.lock()
        if fgProcessPid == f.pid, let p = fgProcess { lock.unlock(); return p }
        lock.unlock()
        let p: NSObject? = onMain { Private.process(of: app(f.bundle)) }
        lock.lock()
        fgProcess = p
        fgProcessPid = p == nil ? -1 : f.pid
        lock.unlock()
        return p
    }
}

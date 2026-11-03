// Which app is in front, and the XCTest objects for it. The foreground is looked up again only
// after an event marked it stale (a screen change, an alert, a process or app-state update).
import XCTest

/// Runs f on the main thread (XCTest's APIs belong there) and returns its result.
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
    /// The last app other than SpringBoard seen in front (what a SpringBoard alert covers).
    private var last: (pid: Int32, bundle: String)?
    /// Pids testmanagerd reported in the background or gone.
    private var background: Set<Int32> = []
    /// Main thread only.
    private var apps: [String: XCUIApplication] = [:]

    func invalidate() { lock.lock(); dirty = true; lock.unlock() }

    var foregroundBundleIfKnown: String { lock.lock(); defer { lock.unlock() }; return fgBundle }
    var foregroundPidIfKnown: Int32 { lock.lock(); defer { lock.unlock() }; return fgPid }

    func bundleIfKnown(pid: Int32) -> String {
        lock.lock(); defer { lock.unlock() }
        if pid == fgPid { return fgBundle }
        if pid == springboardPid { return Apps.springboard }
        return ""
    }

    /// Whether an event from pid is a change on screen: the foreground app's or SpringBoard's
    /// (its alerts), or anyone's while the foreground is not known.
    func onScreen(pid: Int32) -> Bool {
        lock.lock(); defer { lock.unlock() }
        if dirty || fgPid == 0 || pid == 0 { return true }
        return pid == fgPid || pid == springboardPid
    }

    /// SpringBoard's event while another app is in front.
    func isSpringboardBehind(pid: Int32) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return pid != 0 && pid == springboardPid && fgPid != springboardPid && fgPid != 0
    }

    func noteState(pid: Int32, bundle: String, state: UInt64) {
        lock.lock(); defer { lock.unlock() }
        // XCUIApplication.State: 1 not running, 2 suspended, 3 background, 4 foreground.
        if state == 4 { background.remove(pid) } else if pid > 0 { background.insert(pid) }
        dirty = true
    }

    /// SpringBoard's PidStatusChanged: a process was suspended (gone to the background) or
    /// resumed. Only the foreground app suspending, or any app resuming, can change the foreground.
    func noteSuspended(pid: Int32, suspended: Bool) {
        lock.lock(); defer { lock.unlock() }
        if suspended {
            background.insert(pid)
            if pid == fgPid { dirty = true }
        } else {
            background.remove(pid)
            if pid != fgPid { dirty = true }
        }
    }

    /// The XCUIApplication for a bundle id (one per id). Main thread.
    func app(_ bundle: String) -> XCUIApplication {
        if let a = apps[bundle] { return a }
        let a = XCUIApplication(bundleIdentifier: bundle)
        apps[bundle] = a
        return a
    }

    struct Foreground { let pid: Int32; let bundle: String; let all: [(pid: Int32, bundle: String)] }

    /// The app in front: the accessibility client's active applications (2–7 ms; testmanagerd
    /// names each pid once), SpringBoard when it is the only one (its home screen, or one of
    /// its alerts over an app). During a switch two apps can be listed: XCTest's state decides.
    func foreground() -> Foreground {
        lock.lock()
        if !dirty && fgPid != 0 {
            let f = Foreground(pid: fgPid, bundle: fgBundle, all: [])
            lock.unlock()
            return f
        }
        dirty = false
        lock.unlock()
        // On the main thread: off it, XCTest's accessibility client took ~205 ms instead of 2–7 ms.
        let active = onMain { Private.activeApps() }.map { (pid: $0.pid, bundle: Private.bundleID(pid: $0.pid) ?? "") }
        lock.lock()
        let bg = background
        let needSB = springboardPid == 0
        lock.unlock()
        if needSB, let sb = onMain({ Private.systemApplicationPid() }) { lock.lock(); springboardPid = sb; lock.unlock() }
        var candidates = active.filter { $0.bundle != Apps.springboard && !$0.bundle.isEmpty }
        // The accessibility client also lists apps just sent to the background: drop those
        // SpringBoard reported suspended, and ask XCTest only if that still leaves several.
        let running = candidates.filter { !bg.contains($0.pid) }
        if !running.isEmpty { candidates = running }
        var pick = candidates.first
        if candidates.count > 1 {
            pick = onMain { candidates.first { app($0.bundle).state == .runningForeground } } ?? candidates.first
        }
        if pick == nil { pick = active.first }
        let f = Foreground(pid: pick?.pid ?? 0, bundle: pick?.bundle ?? "", all: active)
        lock.lock()
        fgPid = f.pid
        fgBundle = f.bundle
        background.remove(f.pid)
        for a in active where a.bundle == Apps.springboard { springboardPid = a.pid }
        if f.bundle != Apps.springboard && !f.bundle.isEmpty { last = (f.pid, f.bundle) }
        // SpringBoard alone in front with an app behind it: an alert, or a switch in progress.
        // Not cached: the next use asks again, as the app's return may come with no event.
        if f.bundle == Apps.springboard, let l = last, !background.contains(l.pid) { dirty = true }
        lock.unlock()
        return f
    }

    /// The last app other than SpringBoard that was in front, if it still runs.
    func lastApp() -> (pid: Int32, bundle: String)? {
        lock.lock(); defer { lock.unlock() }
        guard let l = last, !background.contains(l.pid) else { return nil }
        return l
    }

    /// The XCUIApplicationProcess of the foreground app (idle notifications), nil for SpringBoard
    /// or when XCTest has none.
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

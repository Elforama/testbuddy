// Turns what the system pushes to the runner into "the UI changed" and "the UI settled".
// docs/research/ios-events.md has the measurements behind every choice here.
//
// Pushed signals (nothing here polls):
//   * The probe (agents/ios/TBProbe), injected into apps tb launches: a Darwin notification
//     after every Core Animation commit that changed the app's layer tree. This is the only
//     signal for in-place changes (a label's text, a color, a view shown or added): UIKit's
//     accessibility posts nothing for those without VoiceOver.
//   * Accessibility notifications. testmanagerd forwards those this runner registered for
//     (registerForAccessibilityNotification:reply:) to -[XCTRunnerDaemonSession
//     receivedAccessibilityNotification:fromElement:payload:], hooked here: screen and layout
//     changes, alerts (SpringBoard's too), first responder, and XCTest's "user testing" events
//     (ViewDidAppear/ViewDidDisappear with the controller, TouchEventsCompleted, ...).
//   * testmanagerd's app state updates (daemonDidUpdatePID:forApplicationWithBundleID:andState:).
//   * App idle: XCTAutomationSupport in the app answers "tell me when your main run loop is
//     idle" and "when no UIView animation runs" over the automation session.
//
// A change bumps `seq`, pushes "changed" (coalesced to one per 30 ms) and re-arms a quiet
// timer. When it fires the app is asked for idle; when it answers and nothing changed
// meanwhile, "settled" is pushed. Waiting threads block on one condition these callbacks
// signal, each with a deadline at the earliest moment its answer could change.
import XCTest
import notify

final class Events {
    static let shared = Events()

    let q = DispatchQueue(label: "tb.events", qos: .userInitiated)
    private let idleQ = DispatchQueue(label: "tb.idle", qos: .userInitiated)
    private let cond = NSCondition()

    // Guarded by cond.
    private var _seq: Int64 = 0          // every change, restless ones included
    private var _settledSeq: Int64 = 0
    private var _solid: Int64 = 0        // changes that hold the UI unsettled
    private var _settledSolid: Int64 = 0 // the solid count when the UI last settled
    private var _wake: Int64 = 0          // any event at all (structural ones included)
    private var _idleTokens: [Int64: Bool] = [:]
    private var _nextToken: Int64 = 0
    private var _inputEnds: Int64 = 0     // TouchEventsCompleted / ButtonEventsCompleted / ScrollEventCompleted

    var seq: Int64 { cond.lock(); defer { cond.unlock() }; return _seq }
    var settledSeq: Int64 { cond.lock(); defer { cond.unlock() }; return _settledSeq }
    /// The solid change count: what action settles compare against.
    var solid: Int64 { cond.lock(); defer { cond.unlock() }; return _solid }
    var inputEnds: Int64 { cond.lock(); defer { cond.unlock() }; return _inputEnds }

    // Events queue only.
    var quietMs = 100
    private var quietTimer: DispatchSourceTimer?
    private var gen: Int64 = 0
    private var lastPushNs: UInt64 = 0
    private var pendingPush = false
    private(set) var counts: [Int32: Int] = [:]

    /// Where pushed events go (the connected host).
    weak var client: Client?
    /// Push every raw notification as an "ax" / "probe" event (research and diagnostics).
    var trace = false

    private let regLock = NSLock()
    private var _registered: [Int32: NSNumber] = [:]
    var registered: [Int32: NSNumber] { regLock.lock(); defer { regLock.unlock() }; return _registered }

    /// AX notifications that count as a UI change in the app that posts them.
    var changeKinds: Set<Int32> = Events.defaultChange
    /// Those that may mean another app or window is in front now.
    var structuralKinds: Set<Int32> = [1000, 1006, 1059]
    private(set) var hookInstalled = false
    private(set) var stateHookInstalled = false

    /// Registered at start: UIKit accessibility's notification range, plus XCTest's user-testing events.
    static let defaultRegister: [Int32] = Array(1000...1075) + [4002]
    /// Everything but notifications about processes (1021), input (1028) and orientation (1011).
    static let defaultChange: Set<Int32> = Set(1000...1075).subtracting([1011, 1021, 1028])

    static let names: [Int32: String] = [
        1000: "ScreenChanged", 1001: "LayoutChanged", 1006: "Alert", 1008: "Announcement",
        1009: "PageScrolled", 1011: "OrientationChanged", 1018: "FirstResponderChanged",
        1021: "PidStatusChanged", 1028: "HIDEventReceived", 1053: "NativeFocusItemDidChange",
        4002: "UserTesting",
    ]

    // MARK: probe

    static let probeName = "dev.testbuddy.probe.changed"
    private var probeToken: Int32 = 0
    private let probeLock = NSLock()
    private var probed: [Int32: UInt32] = [:]
    private(set) var probeListening = false

    func isProbed(_ pid: Int32) -> Bool { probeLock.lock(); defer { probeLock.unlock() }; return probed[pid] != nil }
    var probedPids: [Int32] { probeLock.lock(); defer { probeLock.unlock() }; return Array(probed.keys) }

    private func listenToProbe() {
        let status = notify_register_dispatch(Events.probeName, &probeToken, q) { [weak self] tok in
            var state: UInt64 = 0
            notify_get_state(tok, &state)
            let low = UInt32(truncatingIfNeeded: state)
            self?.onProbe(pid: Int32(truncatingIfNeeded: state >> 32), count: low & 0x7fff_ffff, restless: low & 0x8000_0000 != 0)
        }
        probeListening = status == NOTIFY_STATUS_OK
    }

    /// An app's probe: its layer tree changed (count > 0), or it loaded (count 0). Events queue.
    private func onProbe(pid: Int32, count: UInt32, restless: Bool) {
        guard pid > 0 else { return }
        probeLock.lock()
        let first = probed[pid] == nil
        probed[pid] = count
        probeLock.unlock()
        counts[-1, default: 0] += 1
        if trace {
            push(JW().obj().put("ev", "probe").put("pid", pid).put("count", Int(count))
                .put("ms", Double(DispatchTime.now().uptimeNanoseconds / 1000) / 1000).end().bytes)
        }
        if count == 0 && !first { return }
        guard Apps.shared.onScreen(pid: pid) else { wake(); return }
        change(structural: first, pid: pid, restless: restless && !first)
    }

    // MARK: hooks

    func install() {
        listenToProbe()
        hookInstalled = hook("XCTRunnerDaemonSession", "receivedAccessibilityNotification:fromElement:payload:")
        stateHookInstalled = hookAppState()
        hookOrientation("_XCT_interfaceOrientationDidChange:", kind: "interface")
        hookOrientation("_XCT_deviceOrientationDidChange:", kind: "device")
        NSLog("tb: AX notification hook \(hookInstalled ? "installed" : "MISSING"), app state hook \(stateHookInstalled ? "installed" : "missing"), probe listener \(probeListening ? "on" : "off")")
    }

    private func hook(_ className: String, _ selName: String) -> Bool {
        guard let cls = NSClassFromString(className) else { return false }
        let sel = NSSelectorFromString(selName)
        guard let m = class_getInstanceMethod(cls, sel) else { return false }
        typealias Orig = @convention(c) (AnyObject, Selector, Int32, AnyObject?, AnyObject?) -> Void
        let orig = unsafeBitCast(method_getImplementation(m), to: Orig.self)
        let block: @convention(block) (AnyObject, Int32, AnyObject?, AnyObject?) -> Void = { [weak self] me, n, element, payload in
            self?.received(n, element as? NSObject, payload)
            orig(me, sel, n, element, payload)
        }
        method_setImplementation(m, imp_implementationWithBlock(block))
        return true
    }

    /// testmanagerd → runner: an app's pid or state changed (launch, activation, exit).
    private func hookAppState() -> Bool {
        guard let cls = NSClassFromString("XCTRunnerDaemonSession") else { return false }
        let sel = NSSelectorFromString("daemonDidUpdatePID:forApplicationWithBundleID:andState:")
        guard let m = class_getInstanceMethod(cls, sel) else { return false }
        typealias Orig = @convention(c) (AnyObject, Selector, Int32, AnyObject?, UInt64) -> Void
        let orig = unsafeBitCast(method_getImplementation(m), to: Orig.self)
        let block: @convention(block) (AnyObject, Int32, AnyObject?, UInt64) -> Void = { [weak self] me, pid, bundle, state in
            self?.appState(pid: pid, bundle: bundle as? String ?? "", state: state)
            orig(me, sel, pid, bundle, state)
        }
        method_setImplementation(m, imp_implementationWithBlock(block))
        return true
    }

    // Guarded by cond: orientation updates from testmanagerd (count, last value).
    private var _orientationUpdates: Int64 = 0
    private var _orientationChanged: Int64 = 0
    var orientationChanged: Int64 { cond.lock(); defer { cond.unlock() }; return _orientationChanged }

    /// Until an OrientationChanged notification after `since` (the rotation is over), up to timeoutMs.
    func awaitOrientationChanged(since: Int64, timeoutMs: Int) -> Bool {
        awaitInput({ _orientationChanged > since }, inputEndsAtLeast: nil, timeoutMs: timeoutMs)
    }
    var orientationUpdates: Int64 { cond.lock(); defer { cond.unlock() }; return _orientationUpdates }

    /// testmanagerd → runner: the device or the interface turned.
    private func hookOrientation(_ selName: String, kind: String) {
        guard let cls = NSClassFromString("XCTRunnerDaemonSession") else { return }
        let sel = NSSelectorFromString(selName)
        guard let m = class_getInstanceMethod(cls, sel) else { return }
        typealias Orig = @convention(c) (AnyObject, Selector, Int64) -> Void
        let orig = unsafeBitCast(method_getImplementation(m), to: Orig.self)
        let block: @convention(block) (AnyObject, Int64) -> Void = { [weak self] me, value in
            if let self {
                self.cond.lock(); if kind == "interface" { self._orientationUpdates += 1 }; self._wake += 1; self.cond.broadcast(); self.cond.unlock()
                let t = DispatchTime.now().uptimeNanoseconds
                self.q.async {
                    if self.trace { self.push(JW().obj().put("ev", "orientation").put("kind", kind).put("value", Int(value)).put("ms", Double(t / 1000) / 1000).end().bytes) }
                }
            }
            orig(me, sel, value)
        }
        method_setImplementation(m, imp_implementationWithBlock(block))
    }

    /// Registers AX notifications with testmanagerd. Returns the ids it accepted.
    func register(_ ids: [Int32], timeout: TimeInterval = 5) -> (ok: [Int32], failed: [Int32: String]) {
        guard let session = try? Private.session(),
              let (f, s) = try? RT.imp(session, "registerForAccessibilityNotification:reply:", as: (@convention(c) (NSObject, Selector, Int32, @escaping (NSNumber?, NSError?) -> Void) -> Void).self)
        else { return ([], [0: "registerForAccessibilityNotification:reply: is missing"]) }
        let group = DispatchGroup()
        let lock = NSLock()
        var ok: [Int32] = [], failed: [Int32: String] = [:]
        let have = registered
        for id in ids where have[id] == nil {
            group.enter()
            f(session, s, id) { token, err in
                lock.lock()
                if let token, err == nil {
                    ok.append(id)
                    self.regLock.lock(); self._registered[id] = token; self.regLock.unlock()
                } else { failed[id] = err?.localizedDescription ?? "no token" }
                lock.unlock()
                group.leave()
            }
        }
        _ = group.wait(timeout: .now() + timeout)
        lock.lock(); defer { lock.unlock() }
        return (ok.sorted(), failed)
    }

    func unregister(_ ids: [Int32]) {
        guard let session = try? Private.session(),
              let (f, s) = try? RT.imp(session, "unregisterForAccessibilityNotification:registrationToken:reply:", as: (@convention(c) (NSObject, Selector, Int32, NSNumber, @escaping (NSError?) -> Void) -> Void).self)
        else { return }
        for id in ids {
            regLock.lock()
            let token = _registered.removeValue(forKey: id)
            regLock.unlock()
            if let token { f(session, s, id, token) { _ in } }
        }
    }

    // MARK: incoming

    private func received(_ n: Int32, _ element: NSObject?, _ payload: AnyObject?) {
        let t = DispatchTime.now().uptimeNanoseconds
        let pid = element.flatMap(Private.pid(of:)) ?? 0
        q.async { self.onNotification(n, pid: pid, payload: payload as? Data, at: t) }
    }

    private func onNotification(_ n: Int32, pid: Int32, payload: Data?, at t: UInt64) {
        counts[n, default: 0] += 1
        var info: [String: Any]?
        if n == 4002 || n == 1021 || trace, let payload { info = Events.decode(payload) }
        if trace {
            let w = JW().obj().put("ev", "ax").put("n", n).put("name", Events.names[n] ?? "AX\(n)").put("pid", pid)
                .put("bundle", Apps.shared.bundleIfKnown(pid: pid)).put("ms", Double(t / 1000) / 1000)
            if let info { w.put("payload", info.map { "\($0.key)=\($0.value)" }.sorted().joined(separator: " ")) }
            push(w.end().bytes)
        }
        switch n {
        case 4002:
            // XCTest's user-testing events, posted by the app's UIKit accessibility.
            switch info?["event"] as? String ?? "" {
            case "TouchEventsCompleted", "ButtonEventsCompleted", "ScrollEventCompleted":
                cond.lock(); _inputEnds += 1; cond.broadcast(); cond.unlock()
            case "ViewDidAppear", "ViewDidDisappear":
                if Apps.shared.onScreen(pid: pid) { change(structural: true, pid: pid) }
            default: break
            }
        case 1011:
            // OrientationChanged: posted when a rotation's animation is over (rotate waits for it).
            cond.lock(); _orientationChanged += 1; _wake += 1; cond.broadcast(); cond.unlock()
        case 1021:
            // A process was suspended or resumed (SpringBoard tells): track who is in the
            // background, and look the foreground up again only if it can have changed.
            if let p = (info?["pid"] as? NSNumber)?.int32Value {
                let suspended = (info?["suspended-status"] as? String) == "suspended"
                Apps.shared.noteSuspended(pid: p, suspended: suspended)
            } else {
                Apps.shared.invalidate()
            }
            wake()
        default:
            guard changeKinds.contains(n) else { return }
            let structural = structuralKinds.contains(n)
            if structural { Apps.shared.invalidate() }
            // The probe reports a probed app's changes when they reach the screen; its other
            // notifications (layout, AX1025...) come later or describe nothing visible.
            if !structural && isProbed(pid) { wake(); return }
            // SpringBoard behind an app: only its alerts and screens matter, not its status bar.
            if !structural && Apps.shared.isSpringboardBehind(pid: pid) { wake(); return }
            if Apps.shared.onScreen(pid: pid) || structural { change(structural: structural, pid: pid) } else { wake() }
        }
    }

    private func appState(pid: Int32, bundle: String, state: UInt64) {
        q.async {
            Apps.shared.noteState(pid: pid, bundle: bundle, state: state)
            if self.trace {
                self.push(JW().obj().put("ev", "appState").put("pid", pid).put("bundle", bundle).put("state", Int(state)).end().bytes)
            }
            self.wake()
        }
    }

    /// Wakes waiters without counting a UI change (another app's event, a process update).
    private func wake() {
        cond.lock(); _wake += 1; cond.broadcast(); cond.unlock()
    }

    /// A UI change (events queue, or any thread for an op's own conclusion). A restless one
    /// (only layers that never stop changing, like a stopwatch) bumps seq, so readers see it,
    /// but does not hold the UI unsettled.
    func change(structural: Bool, pid: Int32, restless: Bool = false) {
        cond.lock()
        _seq += 1
        _wake += 1
        if !restless { _solid += 1 }
        let s = _seq
        cond.broadcast()
        cond.unlock()
        if structural { Apps.shared.invalidate() }
        q.async { self.afterChange(s, restless: restless) }
    }

    private func afterChange(_ s: Int64, restless: Bool = false) {
        // Coalesce: at most one "changed" per 30 ms; "settled" carries the final seq.
        let now = DispatchTime.now().uptimeNanoseconds
        if now - lastPushNs >= 30_000_000 {
            lastPushNs = now
            pushChanged(s)
        } else if !pendingPush {
            pendingPush = true
            q.asyncAfter(deadline: .now() + .nanoseconds(Int(30_000_000 - (now - lastPushNs)))) {
                self.pendingPush = false
                self.lastPushNs = DispatchTime.now().uptimeNanoseconds
                self.pushChanged(self.seq)
            }
        }
        if !restless { armQuiet() }
    }

    private func pushChanged(_ s: Int64) {
        let w = JW().obj().put("ev", "changed").put("seq", s)
        let pkg = Apps.shared.foregroundBundleIfKnown
        if !pkg.isEmpty { w.put("pkg", pkg) }
        push(w.end().bytes)
    }

    private func armQuiet() {
        gen += 1
        let g = gen
        quietTimer?.cancel()
        let t = DispatchSource.makeTimerSource(queue: q)
        t.schedule(deadline: .now() + .milliseconds(quietMs), leeway: .milliseconds(1))
        t.setEventHandler { [weak self] in self?.quietFired(g) }
        t.resume()
        quietTimer = t
    }

    /// No change for the quiet window: settled once the app also says it is idle.
    private func quietFired(_ g: Int64) {
        guard g == gen else { return }
        appIdle(capMs: animationCapMs) { [weak self] idle in
            guard let self else { return }
            self.noteAnimationCap(hit: !idle)
            guard g == self.gen else { return } // changed meanwhile: that change's timer retries
            self.cond.lock()
            self._settledSeq = self._seq
            self._settledSolid = self._solid
            let s = self._seq
            self.cond.broadcast()
            self.cond.unlock()
            self.push(JW().obj().put("ev", "settled").put("seq", s).end().bytes)
        }
    }

    // MARK: app idle

    // An app with an animation that never ends (a spinner, a bouncing ball) never reports
    // "animations idle", so every settle would wait the full cap. Once the cap has run out in
    // the foreground app, later settles in it use a short cap: the probe already sees what
    // moves frame by frame.
    private var endlessPid: Int32 = 0
    var animationCapMs: Int {
        let pid = Apps.shared.foregroundPidIfKnown
        return pid != 0 && pid == endlessPid ? 300 : 3000
    }

    func noteAnimationCap(hit: Bool) {
        let pid = Apps.shared.foregroundPidIfKnown
        if hit && pid != 0 { endlessPid = pid }
    }

    /// Calls done on the events queue once the foreground app reports its main run loop idle,
    /// then its UIView animations idle (XCTest's quiescence, asynchronously), each capped: an
    /// animation that never ends never reports idle. done(false) when the cap ran out.
    func appIdle(capMs: Int, _ done: @escaping (Bool) -> Void) {
        idleQ.async {
            guard let process = Apps.shared.foregroundProcess() else {
                self.q.async { done(true) }
                return
            }
            let lock = NSLock()
            var finished = false
            let finish: (Bool) -> Void = { ok in
                lock.lock(); defer { lock.unlock() }
                if finished { return }
                finished = true
                self.q.async { done(ok) }
            }
            self.idleQ.asyncAfter(deadline: .now() + .milliseconds(capMs)) { finish(false) }
            do {
                try Private.notifyWhenRunLoopIdle(process) { _ in
                    guard Private.supportsAnimationsIdle(process) else { finish(true); return }
                    do { try Private.notifyWhenAnimationsIdle(process) { _ in finish(true) } } catch { finish(true) }
                }
            } catch {
                finish(true)
            }
        }
    }

    /// Asks the app for idle now; the token is done when it answers (or its cap runs out).
    func requestIdleToken(capMs: Int) -> Int64 {
        cond.lock()
        _nextToken += 1
        let token = _nextToken
        _idleTokens[token] = false
        cond.unlock()
        appIdle(capMs: capMs) { _ in
            self.cond.lock()
            if self._idleTokens[token] != nil { self._idleTokens[token] = true }
            self.cond.broadcast()
            self.cond.unlock()
        }
        return token
    }

    func dropToken(_ token: Int64) { cond.lock(); _idleTokens.removeValue(forKey: token); cond.unlock() }

    // MARK: input confirmation

    /// Runs f under the events lock and wakes waiters (input confirmations).
    @discardableResult
    func signal<T>(_ f: () -> T) -> T {
        cond.lock(); defer { cond.broadcast(); cond.unlock() }
        return f()
    }

    /// Until done() (checked under the events lock) or, when inputEndsAtLeast is given, until
    /// the apps reported that many input sequences completed. True if either happened in time.
    func awaitInput(_ done: () -> Bool, inputEndsAtLeast: Int64?, timeoutMs: Int) -> Bool {
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(max(timeoutMs, 0)) * 1_000_000
        cond.lock()
        defer { cond.unlock() }
        while !done() && !(inputEndsAtLeast.map { _inputEnds >= $0 } ?? false) {
            if DispatchTime.now().uptimeNanoseconds >= deadline { return false }
            cond.wait(until: Events.date(deadline))
        }
        return true
    }

    // MARK: waiting

    enum Settle: String { case changed, noChange = "no_change", timeout }

    /// After an action taken when seq was `since`: changed once it changed and went quiet,
    /// no_change if nothing changed within noChangeMs AND the app reported idle since the
    /// action (it is not about to change), timeout if it kept changing.
    /// With reactOnly, the change starting is enough: the host waits for the rest when it next
    /// needs a still screen.
    func awaitSettled(since: Int64, noChangeMs: Int, timeoutMs: Int, idleToken: Int64?, reactOnly: Bool = false) -> Settle {
        let start = DispatchTime.now().uptimeNanoseconds
        let deadline = start + UInt64(max(timeoutMs, 0)) * 1_000_000
        let giveUp = start + UInt64(max(noChangeMs, 0)) * 1_000_000
        defer { if let idleToken { dropToken(idleToken) } }
        cond.lock()
        defer { cond.unlock() }
        while true {
            let now = DispatchTime.now().uptimeNanoseconds
            if _solid != since {
                if reactOnly || _settledSolid == _solid { return .changed }
            } else if now >= giveUp && (idleToken.map { _idleTokens[$0] ?? true } ?? true) {
                return .noChange
            }
            if now >= deadline { return _solid == since ? .noChange : .timeout }
            var until = deadline
            if _solid == since && now < giveUp { until = min(until, giveUp) }
            cond.wait(until: Events.date(until))
        }
    }

    /// Until no change is pending (settled covers the latest seq).
    func awaitQuiet(timeoutMs: Int) -> Bool {
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(max(timeoutMs, 0)) * 1_000_000
        cond.lock()
        defer { cond.unlock() }
        while _settledSolid != _solid {
            if DispatchTime.now().uptimeNanoseconds >= deadline { return false }
            cond.wait(until: Events.date(deadline))
        }
        return true
    }

    /// Until check() is true, checking at the start and again only when an event arrives.
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool {
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(max(timeoutMs, 0)) * 1_000_000
        var seen: Int64 = -1
        while true {
            cond.lock()
            while _wake == seen {
                if DispatchTime.now().uptimeNanoseconds >= deadline { cond.unlock(); return false }
                cond.wait(until: Events.date(deadline))
            }
            seen = _wake
            cond.unlock()
            if check() { return true }
            if DispatchTime.now().uptimeNanoseconds >= deadline { return false }
        }
    }

    /// Until seq moves past `since`, up to timeoutMs. True if it moved.
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool {
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(max(timeoutMs, 0)) * 1_000_000
        cond.lock()
        defer { cond.unlock() }
        while _seq == since {
            if DispatchTime.now().uptimeNanoseconds >= deadline { return false }
            cond.wait(until: Events.date(deadline))
        }
        return true
    }

    private static func date(_ uptimeNs: UInt64) -> Date {
        let now = DispatchTime.now().uptimeNanoseconds
        return Date(timeIntervalSinceNow: uptimeNs > now ? Double(uptimeNs - now) / 1e9 : 0)
    }

    // MARK: output

    func push(_ bytes: [UInt8]) { client?.send(bytes) }

    func stats() -> [Int32: Int] { q.sync { counts } }

    /// A notification payload: an archived dictionary.
    static func decode(_ data: Data) -> [String: Any]? {
        let classes: [AnyClass] = [NSDictionary.self, NSArray.self, NSString.self, NSNumber.self, NSData.self, NSValue.self, NSNull.self, NSDate.self]
        if let o = try? NSKeyedUnarchiver.unarchivedObject(ofClasses: classes, from: data) as? [String: Any] { return o }
        if let o = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any] { return o }
        return nil
    }
}

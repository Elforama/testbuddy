import XCTest
import notify

final class Events {
    static let shared = Events()

    let q = DispatchQueue(label: "tb.events", qos: .userInitiated)
    private let idleQ = DispatchQueue(label: "tb.idle", qos: .userInitiated)
    private let cond = NSCondition()

    private var _seq: Int64 = 0
    private var _settledSeq: Int64 = 0
    private var _solid: Int64 = 0
    private var _settledSolid: Int64 = 0
    private var _solidMark = TouchGuard.SolidMark()
    private var _wake: Int64 = 0
    private var _idleTokens: [Int64: Bool] = [:]
    private var _nextToken: Int64 = 0
    private var _inputEnds: Int64 = 0
    private var _lastInputEndAt: UInt64 = 0
    private var _ends: [(pid: Int32, at: UInt64)] = []
    private var _answers: [(pid: Int32, at: UInt64, asked: UInt64)] = []
    private var _deliveries: [Int64: (app: Int32, start: UInt64, liftAt: TimeInterval, implicitAt: TimeInterval?)] = [:]
    private var _verdicts: [Int64: (verdict: Delivery.Verdict, evidence: Delivery.Evidence, liftAt: TimeInterval)] = [:]
    private var _progress: [Int64: Delivery.Evidence] = [:]
    private var _tokens = DeliveryTokens()

    var seq: Int64 { cond.lock(); defer { cond.unlock() }; return _seq }
    var settledSeq: Int64 { cond.lock(); defer { cond.unlock() }; return _settledSeq }
    var solid: Int64 { cond.lock(); defer { cond.unlock() }; return _solid }
    var inputEnds: Int64 { cond.lock(); defer { cond.unlock() }; return _inputEnds }
    func solidChange(since readSeq: Int64) -> Bool { cond.lock(); defer { cond.unlock() }; return _solidMark.changed(since: readSeq) }

    var quietMs = 100
    private var quietTimer: DispatchSourceTimer?
    private var gen: Int64 = 0
    private var lastArm: QuietArm?
    private var lastPushNs: UInt64 = 0
    private var pendingPush = false
    private(set) var counts: [Int32: Int] = [:]

    weak var client: Client?
    var trace = false

    private let regLock = NSLock()
    private var _registered: [Int32: NSNumber] = [:]
    var registered: [Int32: NSNumber] { regLock.lock(); defer { regLock.unlock() }; return _registered }

    var changeKinds: Set<Int32> = Events.defaultChange
    var structuralKinds: Set<Int32> = [1000, 1006, 1059]
    private(set) var hookInstalled = false
    private(set) var stateHookInstalled = false

    static let defaultRegister: [Int32] = Array(1000...1075) + [4002]
    static let defaultChange: Set<Int32> = Set(1000...1075).subtracting([1011, 1021, 1028])

    static let names: [Int32: String] = [
        1000: "ScreenChanged", 1001: "LayoutChanged", 1006: "Alert", 1008: "Announcement",
        1009: "PageScrolled", 1011: "OrientationChanged", 1018: "FirstResponderChanged",
        1021: "PidStatusChanged", 1028: "HIDEventReceived", 1053: "NativeFocusItemDidChange",
        4002: "UserTesting",
    ]

    static let probeName = "dev.testbuddy.probe.changed"
    static let busyName = probeName + ".busy"
    static let lifecycleName = probeName + ".lifecycle"
    static func lifecycleName(_ pid: Int32) -> String { "dev.testbuddy.lifecycle.\(pid)" }
    private var probeToken: Int32 = 0
    private var busyToken: Int32 = 0
    private var lifecycleToken: Int32 = 0
    private let probed = PerPid<UInt32>()
    private let busyUntil = PerPid<UInt64>()
    private(set) var probeListening = false

    func busyUntil(_ pid: Int32) -> UInt64 { busyUntil[pid] ?? 0 }

    func isProbed(_ pid: Int32) -> Bool { probed[pid] != nil }
    var probedPids: [Int32] { probed.pids }

    private func listenToProbe() {
        let status = notify_register_dispatch(Events.probeName, &probeToken, q) { [weak self] tok in
            var state: UInt64 = 0
            notify_get_state(tok, &state)
            let low = UInt32(truncatingIfNeeded: state)
            self?.onProbe(pid: Int32(truncatingIfNeeded: state >> 32), count: low & 0x7fff_ffff, restless: low & 0x8000_0000 != 0)
        }
        probeListening = status == NOTIFY_STATUS_OK
        notify_register_dispatch(Events.lifecycleName, &lifecycleToken, q) { [weak self] _ in self?.wake() }
        notify_register_dispatch(Events.busyName, &busyToken, q) { [weak self] tok in
            var state: UInt64 = 0
            notify_get_state(tok, &state)
            self?.onBusy(pid: Int32(truncatingIfNeeded: state >> 32), ms: UInt32(truncatingIfNeeded: state))
        }
    }

    private let phaseLock = NSLock()
    private var phaseFor = PidRegistration()
    private var phaseToken: Int32 = 0

    func appPhase(_ pid: Int32) -> AppPhase {
        guard pid > 0 else { return .unknown }
        phaseLock.lock(); defer { phaseLock.unlock() }
        let previous = phaseFor.pid, previousToken = phaseToken
        let ok = phaseFor.ensure(pid) {
            var token: Int32 = 0
            guard notify_register_check(Events.lifecycleName(pid), &token) == NOTIFY_STATUS_OK else { return false }
            if previous != 0 { notify_cancel(previousToken) }
            phaseToken = token
            return true
        }
        guard ok else { return .unknown }
        var state: UInt64 = 0
        notify_get_state(phaseToken, &state)
        return AppPhase(state: state)
    }

    func awaitInputReady(_ pid: Int32, leftMs: Int?) -> InputReady.Outcome {
        let out = InputReady.await(phase: { [unowned self] in self.appPhase(pid) },
                                   inFront: { Apps.shared.foreground().pid == pid }, leftMs: leftMs,
                                   now: { DispatchTime.now().uptimeNanoseconds },
                                   wait: { ms, check in self.awaitCondition(timeoutMs: ms, check) })
        if out.end != .none {
            NSLog("tb: pid %d was %@: waited %d ms for it to take input (%@)%@", pid, "\(out.from)", out.waitedMs,
                  out.end.rawValue, out.ready ? "" : "; sending anyway")
        }
        if trace && out.end != .none {
            push(JW().obj().put("ev", "inputReady").put("pid", pid).put("waitedMs", out.waitedMs).put("ready", out.ready)
                .put("end", out.end.rawValue).end().bytes)
        }
        return out
    }

    private func onBusy(pid: Int32, ms: UInt32) {
        guard pid > 0, ms > 0 else { return }
        let until = DispatchTime.now().uptimeNanoseconds + UInt64(min(ms, 3000)) * 1_000_000
        busyUntil.update(pid) { $0 = max($0 ?? 0, until) }
        if trace {
            push(JW().obj().put("ev", "busy").put("pid", pid).put("ms", Int(ms)).end().bytes)
        }
    }

    private func onProbe(pid: Int32, count: UInt32, restless: Bool) {
        guard pid > 0 else { return }
        let first = probed.update(pid) { v -> Bool in let first = v == nil; v = count; return first }
        counts[-1, default: 0] += 1
        if trace {
            push(JW().obj().put("ev", "probe").put("pid", pid).put("count", Int(count)).put("restless", restless)
                .put("ms", Double(DispatchTime.now().uptimeNanoseconds / 1000) / 1000).end().bytes)
        }
        if count == 0 && !first { return }
        guard Apps.shared.onScreen(pid: pid) else { wake(); return }
        change(structural: first, pid: pid, restless: restless && !first)
    }

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

    private var _orientationUpdates: Int64 = 0
    private var _orientationChanged: Int64 = 0
    var orientationChanged: Int64 { cond.lock(); defer { cond.unlock() }; return _orientationChanged }

    func awaitOrientationChanged(since: Int64, timeoutMs: Int) -> Bool {
        awaitInput({ _orientationChanged > since }, inputEndConfirms: nil, timeoutMs: timeoutMs)
    }
    var orientationUpdates: Int64 { cond.lock(); defer { cond.unlock() }; return _orientationUpdates }

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
        if pid > 0 && n == 4002 {
            switch info?["event"] as? String ?? "" {
            case "TouchEventsCompleted", "ButtonEventsCompleted", "ScrollEventCompleted": noteSeen(pid, at: t, .inputEnd)
            default: break
            }
        }
        switch n {
        case 4002:
            switch info?["event"] as? String ?? "" {
            case "TouchEventsCompleted", "ButtonEventsCompleted", "ScrollEventCompleted":
                cond.lock(); _inputEnds += 1; _lastInputEndAt = max(_lastInputEndAt, t); cond.broadcast(); cond.unlock()
            case "ViewDidAppear", "ViewDidDisappear":
                if Apps.shared.onScreen(pid: pid) { change(structural: true, pid: pid) }
            default: break
            }
        case 1011:
            cond.lock(); _orientationChanged += 1; _wake += 1; cond.broadcast(); cond.unlock()
        case 1021:
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
            if !structural && isProbed(pid) { wake(); return }
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

    private func wake() {
        cond.lock(); _wake += 1; cond.broadcast(); cond.unlock()
    }

    func change(structural: Bool, pid: Int32, restless: Bool = false) {
        cond.lock()
        _seq += 1
        _wake += 1
        if !restless { _solid += 1 }
        _solidMark.note(seq: _seq, restless: restless)
        let s = _seq
        let solid = _solid
        cond.broadcast()
        cond.unlock()
        if structural { Apps.shared.invalidate() }
        q.async { self.afterChange(s, solid: solid, restless: restless) }
    }

    private func afterChange(_ s: Int64, solid: Int64, restless: Bool = false) {
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
        if !restless { armQuiet(solid: solid) }
    }

    private func pushChanged(_ s: Int64) {
        let w = JW().obj().put("ev", "changed").put("seq", s)
        let pkg = Apps.shared.foregroundBundleIfKnown
        if !pkg.isEmpty { w.put("pkg", pkg) }
        push(w.end().bytes)
    }

    private func armQuiet(solid: Int64) {
        let arm = QuietArm.next(after: lastArm, solid: solid)
        lastArm = arm
        gen = arm.gen
        quietTimer?.cancel()
        let t = DispatchSource.makeTimerSource(queue: q)
        t.schedule(deadline: .now() + .milliseconds(quietMs), leeway: .milliseconds(1))
        t.setEventHandler { [weak self] in self?.quietFired(arm) }
        t.resume()
        quietTimer = t
    }

    private func quietFired(_ arm: QuietArm) {
        guard arm.gen == gen else { return }
        appIdle(capMs: animationCapMs) { [weak self] idle in
            guard let self else { return }
            self.noteAnimationCap(hit: !idle)
            self.cond.lock()
            guard arm.settles(gen: self.gen, solid: self._solid) else {
                self.cond.unlock()
                return
            }
            self._settledSeq = self._seq
            self._settledSolid = self._solid
            let s = self._seq
            self.cond.broadcast()
            self.cond.unlock()
            self.push(JW().obj().put("ev", "settled").put("seq", s).end().bytes)
        }
    }

    private var endlessPid: Int32 = 0
    var animationCapMs: Int {
        let pid = Apps.shared.foregroundPidIfKnown
        return pid != 0 && pid == endlessPid && !isProbed(pid) ? 300 : 3000
    }

    func noteAnimationCap(hit: Bool) {
        let pid = Apps.shared.foregroundPidIfKnown
        if hit && pid != 0 && !isProbed(pid) { endlessPid = pid }
    }

    func appIdle(capMs: Int, _ done: @escaping (Bool) -> Void) {
        idleQ.async {
            guard let process = Apps.shared.foregroundProcess() else {
                self.q.async { done(true) }
                return
            }
            let pid = Apps.shared.foregroundPidIfKnown
            let probed = pid != 0 && self.isProbed(pid)
            let lock = NSLock()
            var finished = false
            let finish: (Bool) -> Void = { ok in
                lock.lock(); defer { lock.unlock() }
                if finished { return }
                finished = true
                self.q.async { done(ok) }
            }
            self.idleQ.asyncAfter(deadline: .now() + .milliseconds(capMs)) { finish(false) }
            func untilNotBusy() {
                let until = self.busyUntil(pid), now = DispatchTime.now().uptimeNanoseconds
                if until <= now { finish(true); return }
                self.idleQ.asyncAfter(deadline: .now() + .nanoseconds(Int(until - now))) { untilNotBusy() }
            }
            do {
                try Private.notifyWhenRunLoopIdle(process) { _ in
                    if probed { self.idleQ.async { untilNotBusy() }; return }
                    guard Private.supportsAnimationsIdle(process) else { finish(true); return }
                    do { try Private.notifyWhenAnimationsIdle(process) { _ in finish(true) } } catch { finish(true) }
                }
            } catch {
                finish(true)
            }
        }
    }

    func askSystemIdle(_ done: @escaping (Error?) -> Void) throws {
        guard let p = Apps.shared.springboardProcess() else { throw AgentError("XCTest has no SpringBoard process") }
        try Private.notifyWhenRunLoopIdle(p) { done($0) }
    }

    func traceSystemWait(ms: Int, outcome: String) {
        if trace { push(JW().obj().put("ev", "systemWait").put("ms", ms).put("outcome", outcome).end().bytes) }
    }

    func traceIssue(_ text: String, fatal: Bool) {
        if trace { push(JW().obj().put("ev", "xctIssue").put("text", text).put("fatal", fatal).end().bytes) }
    }

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

    @discardableResult
    func signal<T>(_ f: () -> T) -> T {
        cond.lock(); defer { cond.broadcast(); cond.unlock() }
        return f()
    }

    private func noteSeen(_ pid: Int32, at t: UInt64, _ kind: Delivery.Kind) {
        guard kind == .inputEnd else { return }
        cond.lock()
        _ends.append((pid, t))
        _ends.removeAll { t > 10_000_000_000 && $0.at < t - 10_000_000_000 }
        cond.broadcast()
        cond.unlock()
    }

    private func noteAnswer(_ pid: Int32, at t: UInt64, asked: UInt64) {
        cond.lock()
        _answers.append((pid, t, asked))
        _answers.removeAll { t > 10_000_000_000 && $0.at < t - 10_000_000_000 }
        cond.broadcast()
        cond.unlock()
    }

    func newDelivery(app: Int32, start: UInt64, liftAt: TimeInterval, implicitAt: TimeInterval? = nil) -> Int64 {
        cond.lock()
        let token = _tokens.take()
        _deliveries[token] = (app, start, liftAt, implicitAt)
        _deliveries = _deliveries.filter { $0.key > token - 16 }
        _verdicts = _verdicts.filter { $0.key > token - 16 }
        _progress = _progress.filter { $0.key > token - 16 }
        cond.unlock()
        DispatchQueue.global(qos: .userInitiated).async { self.judge(token) }
        return token
    }

    func awaitDelivery(token: Int64, waitMs: Int) -> (verdict: Delivery.Verdict, evidence: Delivery.Evidence, liftAt: TimeInterval, pending: Bool) {
        cond.lock()
        defer { cond.unlock() }
        let none = Delivery.Evidence(reportAt: nil, answerLags: [])
        guard let d = _deliveries[token] else { return (.unknown, none, 0, false) }
        let by = DispatchTime.now().uptimeNanoseconds + UInt64(max(waitMs, 0)) * 1_000_000
        while _verdicts[token] == nil {
            if DispatchTime.now().uptimeNanoseconds >= by { return (.unknown, _progress[token] ?? none, d.liftAt, true) }
            cond.wait(until: Events.date(by))
        }
        guard let v = _verdicts[token] else { return (.unknown, none, d.liftAt, false) }
        return (v.verdict, v.evidence, v.liftAt, false)
    }

    private func judge(_ token: Int64) {
        var system = Apps.shared.springboardPidIfKnown
        if system == 0 { _ = Apps.shared.springboardProcess(); system = Apps.shared.springboardPidIfKnown }
        cond.lock()
        defer { cond.broadcast(); cond.unlock() }
        guard let d = _deliveries[token] else { return }
        let by = d.start + UInt64((d.liftAt + Delivery.waitS + 2) * 1e9)
        var lastAsk: TimeInterval?
        var verdict = Delivery.Verdict.unknown
        var evidence = Delivery.Evidence(reportAt: nil, answerLags: [])
        let secs = { (t: UInt64) in t >= d.start ? Double(t - d.start) / 1e9 : -Double(d.start - t) / 1e9 }
        func seenNow() -> [Delivery.Seen] {
            (_ends.map { Delivery.Seen(pid: $0.pid, at: secs($0.at), kind: .inputEnd) }
             + _answers.map { Delivery.Seen(pid: $0.pid, at: secs($0.at), kind: .idleAnswer, askedAt: secs($0.asked)) })
                .filter { $0.at >= 0 }.sorted { $0.at < $1.at }
        }
        while true {
            let now = DispatchTime.now().uptimeNanoseconds
            let seen = seenNow()
            evidence = Delivery.evidence(app: d.app, system: system, liftAt: d.liftAt, seen: seen, implicitAt: d.implicitAt)
            _progress[token] = evidence
            if let v = Delivery.verdict(app: d.app, system: system, liftAt: d.liftAt, seen: seen, now: secs(now), implicitAt: d.implicitAt) {
                verdict = v
                break
            }
            if now >= by { break }
            if Delivery.asksIdle(app: d.app, system: system, liftAt: d.liftAt, seen: seen, now: secs(now), lastAsk: lastAsk, implicitAt: d.implicitAt) {
                lastAsk = secs(now)
                askIdle(app: d.app)
            }
            cond.wait(until: Events.date(min(now + 20_000_000, by)))
        }
        _verdicts[token] = (verdict, evidence, d.liftAt)
        if verdict != .delivered {
            NSLog("tb: delivery %lld: %@ (app %d, SpringBoard %d, its report %@ s in, idle answers in %@ s)", token, verdict.rawValue, d.app, system,
                  evidence.reportAt.map { String(format: "%.3f", $0) } ?? "none", evidence.answerLags.map { String(format: "%.3f", $0) }.joined(separator: ", "))
        }
        guard verdict == .notDelivered else { return }
        cond.broadcast()
        let from = d.liftAt - Gestures.confirmSlackS
        let watchUntil = DispatchTime.now().uptimeNanoseconds + 3_000_000_000
        while DispatchTime.now().uptimeNanoseconds < watchUntil {
            if let late = seenNow().first(where: { $0.pid == d.app && $0.kind == .inputEnd && $0.at >= from }) {
                NSLog("tb: delivery %lld: the app's own report came %.3f s in, after the notDelivered verdict", token, late.at)
                return
            }
            cond.wait(until: Events.date(watchUntil))
        }
    }

    private func askIdle(app: Int32) {
        let asked = DispatchTime.now().uptimeNanoseconds
        idleQ.async {
            guard Apps.shared.foregroundPidIfKnown == app, let process = Apps.shared.foregroundProcess() else { return }
            try? Private.notifyWhenRunLoopIdle(process) { _ in
                self.noteAnswer(app, at: DispatchTime.now().uptimeNanoseconds, asked: asked)
            }
        }
    }

    func awaitInput(_ done: () -> Bool, inputEndConfirms: ((UInt64) -> Bool)?, timeoutMs: Int) -> Bool {
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(max(timeoutMs, 0)) * 1_000_000
        cond.lock()
        defer { cond.unlock() }
        while !done() && !(_lastInputEndAt > 0 && (inputEndConfirms.map { $0(_lastInputEndAt) } ?? false)) {
            if DispatchTime.now().uptimeNanoseconds >= deadline { return false }
            cond.wait(until: Events.date(deadline))
        }
        return true
    }

    enum Settle: String { case changed, noChange = "no_change", timeout }

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

    func awaitCondition(timeoutMs: Int, recheckMs: () -> Int = { 0 }, _ check: () -> Bool) -> Bool {
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(max(timeoutMs, 0)) * 1_000_000
        var seen: Int64 = -1
        while true {
            let again = UInt64(max(recheckMs(), 0)) * 1_000_000
            let until = again > 0 ? min(deadline, DispatchTime.now().uptimeNanoseconds + again) : deadline
            cond.lock()
            while _wake == seen {
                let now = DispatchTime.now().uptimeNanoseconds
                if now >= deadline { cond.unlock(); return false }
                if now >= until { break }
                cond.wait(until: Events.date(until))
            }
            seen = _wake
            cond.unlock()
            if check() { return true }
            if DispatchTime.now().uptimeNanoseconds >= deadline { return false }
        }
    }

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

    func push(_ bytes: [UInt8]) { client?.send(bytes) }

    func stats() -> [Int32: Int] { q.sync { counts } }

    static func decode(_ data: Data) -> [String: Any]? {
        let classes: [AnyClass] = [NSDictionary.self, NSArray.self, NSString.self, NSNumber.self, NSData.self, NSValue.self, NSNull.self, NSDate.self]
        if let o = try? NSKeyedUnarchiver.unarchivedObject(ofClasses: classes, from: data) as? [String: Any] { return o }
        if let o = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any] { return o }
        return nil
    }
}

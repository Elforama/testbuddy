// The ops of docs/protocol.md as iOS does them, plus the iOS-only ones and diagnostics.
import XCTest
import notify

final class Ops {
    let tree = Tree()
    lazy var input = Input(tree: tree)
    private let receipts = ControlReceipts()
    private lazy var typingShell = TypingShell(ops: self)
    private lazy var typist = Typist(device: typingShell)
    private(set) var exitRequested = false
    /// What the hello and the liveness probe report, safe to read while an op runs.
    let vitals = Vitals()

    /// The screen's bounds, read once on the main thread: the size until a tree read has seen the app.
    private let screen: CGSize

    /// Main thread (the agent's start).
    init() {
        screen = UIScreen.main.bounds.size
        noteVitals()
    }

    /// Any thread: sent at once on a new connection, even while an op runs (issue #48).
    func hello() -> [UInt8] {
        let size = vitals.size
        let w = JW().obj().put("ev", "hello").put("platform", "ios").put("version", Agent.shared.build.isEmpty ? Agent.version : Agent.shared.build)
            .put("agent", Agent.version).put("build", Agent.shared.build).put("udid", Agent.shared.udid)
            .put("w", size.w).put("h", size.h).put("seq", Events.shared.seq).put("pid", Int(getpid()))
            .put("axHook", Events.shared.hookInstalled)
        return w.end().bytes
    }

    /// The liveness probe's reply (Dispatcher.answersAtOnce: ping, info without the keyboard
    /// check), from any thread, even while an op runs (issue #48). "busy" names that op,
    /// "busyMs" how long it has run, "busyLimitMs" when the runner counts as stuck.
    func live(_ r: Request) -> [UInt8] {
        let w = JW().obj().put("id", r.id).put("ok", true)
        if r.op == "info" {
            let size = vitals.size
            w.put("w", size.w).put("h", size.h).put("keyboard", vitals.keyboard)
                .put("platform", "ios").put("agent", Agent.version).put("build", Agent.shared.build).put("udid", Agent.shared.udid)
                .put("pid", Int(getpid())).put("settledSeq", Events.shared.settledSeq)
                .put("quietMs", vitals.quietMs).put("axHook", Events.shared.hookInstalled)
                .put("axRegistered", Events.shared.registered.count).put("treeMode", vitals.treeMode)
        }
        let busy = vitals.state
        return w.put("seq", Events.shared.seq).put("busy", busy.op).put("busyMs", busy.ms).put("busyLimitMs", busy.limitMs).end().bytes
    }

    /// Copies what the probe reports into vitals. On the ops queue after each op, and once at
    /// start on the main thread.
    func noteVitals() {
        let size = screenSize()
        vitals.set(w: Int(size.width), h: Int(size.height), keyboard: tree.lastKeyboard, treeMode: tree.mode, quietMs: Events.shared.quietMs)
    }

    private func screenSize() -> CGSize {
        let f = tree.lastAppFrame
        if f.width > 0 && f.height > 0 { return f.size }
        return screen
    }

    /// Runs one request, writing its result fields into w. Returns binary data, if any.
    func handle(_ r: Request, _ w: JW) throws -> Data? {
        switch r.op {
        case "ping":
            w.put("seq", Events.shared.seq)
        case "info":
            let size = screenSize()
            let kb = r.bool("keyboard", true) ? keyboardShown() : tree.lastKeyboard
            w.put("w", Int(size.width)).put("h", Int(size.height)).put("keyboard", kb)
                .put("platform", "ios").put("agent", Agent.version).put("build", Agent.shared.build).put("udid", Agent.shared.udid)
                .put("pid", Int(getpid())).put("seq", Events.shared.seq).put("settledSeq", Events.shared.settledSeq)
                .put("quietMs", Events.shared.quietMs).put("axHook", Events.shared.hookInstalled)
                .put("axRegistered", Events.shared.registered.count).put("treeMode", tree.mode)
        case "tree":
            // With the host's reply limit (replyMs), the read answers before it whatever the app
            // does (issue #130): a snapshot the app has not answered by then is given up on.
            let asked = DispatchTime.now().uptimeNanoseconds
            let deadline = r.has("replyMs") ? TypingDeadline.forRequest(start: asked, replyMs: r.int("replyMs", 0), chars: 0) : nil
            if r.bool("waitQuiet", false) {
                var quietMs = r.int("timeout", 3000)
                if let deadline {
                    // Half the time left at most: the read needs the rest.
                    let now = DispatchTime.now().uptimeNanoseconds
                    quietMs = min(quietMs, deadline.answerBy > now ? Int((deadline.answerBy - now) / 2_000_000) : 0)
                }
                _ = Events.shared.awaitQuiet(timeoutMs: quietMs)
            }
            let seq = Events.shared.seq
            let mode = r.has("mode") ? r.str("mode") : nil
            let ci = r.bool("ci", false) // the host's CI mode (Front.read's fallback, issue #142)
            // A read that never ends would log nothing: say where it is after 10 s.
            let stage = ReadStage()
            DispatchQueue.global().asyncAfter(deadline: .now() + 10) {
                if let where_ = stage.unfinished() { NSLog("tb: tree read still running after 10 s: \(where_)") }
            }
            defer { stage.set("done") }
            do {
                let fields = try ScreenRead.run(on: .main, by: deadline) { [tree] j in
                    stage.reached()
                    // A read the op gave up on publishes nothing it saw (issue #130).
                    try tree.write(j, mode: mode, seq: seq, ci: ci, wanted: { !stage.givenUp })
                }
                w.fields(of: fields)
            } catch let e as AgentError where e.transient {
                stage.giveUp()
                NSLog("tb: \(e.message)")
                throw e
            }
            // A slow read says where its time went: waiting for the main thread, or the snapshot.
            // A wait for the main thread of ScreenRead.mainWaitShownMs or more is in the reply
            // too (mainWaitMs, added by ScreenRead.run, issue #255): the host names it in a
            // stall's signs. It is timed from ScreenRead.run's start; this log's wait is timed
            // from `asked`, so it also counts waitQuiet (up to its timeout) and can be longer.
            let ms = { (from: UInt64, to: UInt64) in Int((to - from) / 1_000_000) }
            let done = DispatchTime.now().uptimeNanoseconds
            if ms(asked, done) > 2000 {
                NSLog("tb: slow tree read: \(ms(asked, done)) ms (\(ms(asked, stage.reachedAt ?? done)) ms waiting for the main thread, snapshot \(Int(tree.lastMs.snap)) ms, \(tree.lastMs.nodes) nodes, mode \(mode ?? tree.mode))")
            }
            w.put("seq", seq)
        case "tap", "doubleTap", "press", "swipe", "key", "type", "erase", "clear", "hideKeyboard":
            try action(r, w)
        case "settle":
            let token = Events.shared.requestIdleToken(capMs: min(r.int("timeout", 5000), 3000))
            let res = Events.shared.awaitSettled(since: r.int64("since", Events.shared.solid), noChangeMs: r.int("noChange", 250),
                                                 timeoutMs: r.int("timeout", 5000), idleToken: token)
            w.put("result", res.rawValue).put("seq", Events.shared.seq)
        case "quiet":
            w.put("quiet", Events.shared.awaitQuiet(timeoutMs: r.int("timeout", 5000))).put("seq", Events.shared.seq)
        case "awaitPackage":
            let pkg = try r.needStr("pkg")
            if r.bool("readTree", false) {
                try awaitLaunchTree(pkg, pid: Int32(r.int("pid", 0)), timeout: r.int("timeout", 30000), ci: r.bool("ci", false), into: w)
                break
            }
            var behindAlert = false
            let found = Events.shared.awaitCondition(timeoutMs: r.int("timeout", 10000)) {
                Apps.shared.invalidate()
                let fg = Apps.shared.foreground().bundle
                if fg == pkg { return true }
                // A system alert (a permission prompt, even one left from an earlier run) puts
                // SpringBoard in front, and the accessibility client lists only it; the app is
                // still running in the foreground behind it: launched. The flow deals with the
                // alert.
                if fg == Apps.springboard, onMain({ Apps.shared.app(pkg).state == .runningForeground }) {
                    behindAlert = true
                    return true
                }
                return false
            }
            w.put("found", found).put("behindAlert", behindAlert)
        case "foreground":
            Apps.shared.invalidate()
            let t0 = DispatchTime.now().uptimeNanoseconds
            let fg = Apps.shared.foreground()
            w.put("ms", Double(DispatchTime.now().uptimeNanoseconds - t0) / 1e6)
            w.put("pkg", fg.bundle).put("pid", Int(fg.pid))
            w.key("active").arr()
            for a in fg.all { w.obj().put("pid", Int(a.pid)).put("bundle", a.bundle).end() }
            w.end()
        case "screenshot":
            return try screenshot(r, w)
        case "rotate":
            try rotate(r.str("to", "portrait"))
        case "setQuiet":
            let ms = max(30, min(r.int("ms", 120), 5000))
            Events.shared.q.sync { Events.shared.quietMs = ms }
            w.put("quiet", ms)
        case "exit":
            exitRequested = true
            w.put("bye", true)
        case "keep":
            Server.keep = true
            w.put("keep", true)

        // iOS only
        case "launch":
            let id = try r.needStr("bundleId")
            let args = r.strings("args"), env = r.dict("env")
            onMain {
                let app = Apps.shared.app(id)
                app.launchArguments = args
                app.launchEnvironment = env
                app.launch()
            }
            Apps.shared.invalidate()
            w.put("state", onMain { Int(Apps.shared.app(id).state.rawValue) })
        case "terminate":
            let id = try r.needStr("bundleId")
            onMain { Apps.shared.app(id).terminate() }
            Apps.shared.invalidate()
        case "activate":
            let id = try r.needStr("bundleId")
            onMain { Apps.shared.app(id).activate() }
            Apps.shared.invalidate()
        case "appState":
            let id = try r.needStr("bundleId")
            w.put("state", onMain { Int(Apps.shared.app(id).state.rawValue) })
        case "treeMode":
            if r.has("mode") { tree.mode = r.str("mode") }
            w.put("mode", tree.mode)

        // diagnostics and research
        case "methods":
            let name = try r.needStr("class")
            w.key("methods").arr()
            for m in (r.bool("protocol", false) ? RT.protocolMethods(name) : RT.methods(of: name)) { w.str(m) }
            w.end()
        case "axRegister":
            let res = Events.shared.register(r.ints("ids").map { Int32($0) })
            w.key("ok").arr(); for i in res.ok { w.int(i) }; w.end()
            w.key("failed").obj(); for (k, v) in res.failed { w.put(String(k), v) }; w.end()
        case "axUnregister":
            Events.shared.unregister(r.ints("ids").map { Int32($0) })
        case "axTrace":
            let on = r.bool("on", true)
            Events.shared.q.sync { Events.shared.trace = on }
            w.put("trace", on)
        case "axChange":
            // Which notification ids count as a UI change.
            let ids = Set(r.ints("ids").map { Int32($0) })
            Events.shared.q.sync { Events.shared.changeKinds = ids }
        case "axStats":
            w.key("counts").obj()
            for (k, v) in Events.shared.stats().sorted(by: { $0.key < $1.key }) { w.put(String(k), v) }
            w.end()
            w.key("registered").arr(); for k in Events.shared.registered.keys.sorted() { w.int(k) }; w.end()
        case "fgReport":
            // What each way of finding the foreground app returns (diagnostics).
            let client = try Private.axClient()
            for name in ["activeApplications", "activeForegroundApplications", "_activeForegroundApplicationsWithPotentiallyInterruptingUI", "systemApplication"] {
                let o = RT.get(client, name)
                let list = (o as? [NSObject]) ?? (o.map { [$0 as! NSObject] } ?? [])
                w.key(name).arr()
                for e in list { w.str("\(e) pid=\(Private.pid(of: e).map(String.init) ?? "?") bundle=\(Private.pid(of: e).flatMap(Private.bundleID(pid:)) ?? "?")") }
                w.end()
            }
            if r.has("bundleId") {
                let id = r.str("bundleId")
                let st = onMain { Int(Apps.shared.app(id).state.rawValue) }
                w.put("state", st)
                let again = (RT.get(client, "activeApplications") as? [NSObject] ?? []).map { Private.pid(of: $0) ?? -1 }
                w.key("activeAfter").arr(); for p in again { w.int(p) }; w.end()
            }
            if let (f, s) = try? RT.imp(client, "elementAtPoint:error:", as: (@convention(c) (NSObject, Selector, CGPoint, UnsafeMutablePointer<NSError?>?) -> Unmanaged<NSObject>?).self) {
                var err: NSError?
                let e = f(client, s, CGPoint(x: 200, y: 400), &err)?.takeUnretainedValue()
                w.put("hit", "\(String(describing: e)) pid=\(e.flatMap(Private.pid(of:)).map(String.init) ?? "?") err=\(err?.localizedDescription ?? "")")
            }
        case "flutterSurface":
            // Diagnostic: AX can expose the underlying Flutter menu before a native
            // sheet finishes dismissing. Recheck the actual point on change callbacks.
            let pid = Apps.shared.foreground().pid
            let x: Double = try r.need("x"), y: Double = try r.need("y")
            let owned = Events.shared.isProbed(pid) && Events.shared.awaitCondition(timeoutMs: r.int("timeout", 0)) {
                Apps.shared.foreground().pid == pid && FlutterAdapter.ownsPoint(pid: pid, x: x, y: y)
            }
            w.put("flutter", owned)
        case "idle":
            // Times the app's idle callbacks (run loop, then animations).
            let proc = Apps.shared.foregroundProcess()
            w.put("process", proc.map { String(describing: $0) } ?? "none")
            if let proc {
                w.put("session", String(describing: RT.get(proc, "automationSessionNonBlocking")))
                w.put("supportsAnim", Private.supportsAnimationsIdle(proc))
            }
            let t0 = DispatchTime.now().uptimeNanoseconds
            let done = DispatchSemaphore(value: 0)
            var ok = false
            Events.shared.appIdle(capMs: r.int("cap", 3000)) { ok = $0; done.signal() }
            done.wait()
            w.put("idle", ok).put("ms", Double(DispatchTime.now().uptimeNanoseconds - t0) / 1e6)
        default:
            throw AgentError("unknown op \"\(r.op)\"")
        }
        return nil
    }

    // awaitCondition records its event generation before checking. A lifecycle callback
    // arriving during a snapshot cannot be lost between the check and the wait.
    // A read that fails because the app is not ready or not answering accessibility yet
    // (LaunchWait) is tried again: on the next event, and after a not-answering error also after
    // LaunchWait.recheckMs, since an app whose accessibility is not up may send no event at all.
    private func awaitLaunchTree(_ pkg: String, pid: Int32, timeout: Int, ci: Bool, into w: JW) throws {
        Apps.shared.forgetProcess(pkg)
        var failure: Error?
        var lastTransient: Error?
        var notAnswering = false
        let found = Events.shared.awaitCondition(timeoutMs: timeout, recheckMs: { notAnswering ? LaunchWait.recheckMs : 0 }) {
            if pid > 0, kill(pid, 0) == -1, errno == ESRCH {
                failure = AgentError("launched process \(pkg) pid \(pid) exited before the first tree")
                return true
            }
            let seq = Events.shared.seq
            Apps.shared.invalidate()
            do {
                try onMain { try self.tree.write(w, seq: seq, launchBundle: pkg, launchPID: pid, ci: ci) }
                w.put("seq", seq)
                return true
            } catch is LaunchWindowChanged {
                return false
            } catch {
                let e = error as NSError
                switch LaunchWait.classify(domain: e.domain, code: e.code, text: "\(error)", ci: ci) {
                case .fatal:
                    failure = error
                    return true
                case .notReadyYet:
                    notAnswering = false
                case .notAnswering:
                    notAnswering = true
                }
                lastTransient = error
                Apps.shared.forgetProcess(pkg)
                return false
            }
        }
        if let error = failure { throw error }
        if !found {
            // Still not answering accessibility at the end: transient, tb may ask again.
            throw AgentError("launch of \(pkg) pid \(pid) had no readable tree within \(timeout) ms: \(lastTransient?.localizedDescription ?? "waiting for foreground window")", transient: notAnswering)
        }
        w.put("found", true)
    }

    // MARK: actions

    /// Runs an input action, then (unless settle=false) waits for the app to react: it changed
    /// and went quiet, or nothing changed within noChange ms and the app said it was idle.
    private func action(_ r: Request, _ w: JW) throws {
        let since = Events.shared.seq
        let sinceSolid = Events.shared.solid
        let settle = r.bool("settle", true)
        // type and erase (issue #46), and clear, key and hideKeyboard when the host sends its
        // reply limit (issue #130), answer before it whatever the app does: none of their reads
        // waits for it past their deadline.
        let typing = r.op == "type" || r.op == "erase"
        let deadline: TypingDeadline? = typing || r.op == "clear" || r.has("replyMs") ? opDeadline(r) : nil
        let (doing, nothingSent) = Ops.wording(r)
        func appRead<T>(by: UInt64?, _ what: String, _ f: @escaping () throws -> T) throws -> T {
            try AppWait.read(on: .main, by: deadline == nil ? nil : by, f) { t in
                AppWait.notAnswering(doing, since: deadline?.start ?? 0, Typist.waitedFor(what, t), nothingSent)
            }
        }
        // An app without the probe announces only structural changes (docs/research/ios-events.md):
        // for it, one read after the action tells whether it changed in place.
        let fgPid = try appRead(by: deadline?.keysBy, "checking which app is in front") { Apps.shared.foreground().pid }
        let probed = Events.shared.isProbed(fgPid)
        var before: Int?
        if settle && !probed {
            if let s = tree.lastSignature, s.seq == since { before = s.hash } else {
                before = try? appRead(by: deadline?.keysBy, "reading the screen") { try self.tree.signature() }
            }
        }
        var armedReceipt: UInt64?
        defer { if armedReceipt != nil { receipts.disarm() } }
        let t0 = DispatchTime.now().uptimeNanoseconds
        switch r.op {
        case "tap":
            let point = CGPoint(x: try r.need("x"), y: try r.need("y"))
            let id = r.str("targetID")
            var frame: CGRect?
            if !id.isEmpty {
                guard fgPid == Int32(r.int("targetPID", 0)) else { throw AgentError("process changed before tap on \(id)") }
                // Layout can move before the corresponding event reaches the runner.
                // Validate even if the generation still matches the host's last read.
                frame = try onMain { try tree.containsTap(id: id, pid: fgPid, point: point) }
                w.put("guarded", true)
                if frame == nil {
                    w.put("withheld", "preflight").put("seq", Events.shared.seq)
                    return
                }
            }
            // The probe withholds the touch if the target moved since this read (issue #103),
            // unless the host allows a target that never stops moving.
            let still = r.bool("targetMoving", false) ? nil : frame
            armedReceipt = probed && !id.isEmpty ? receipts.arm(pid: fgPid, id: id, point: point, frame: still) : nil
            // Issue #149: a solid change since the host's read, as the touch goes out. Taken
            // before the touch, so the tap's own press and action do not count.
            let changedSinceRead = !id.isEmpty && r.has("targetSeq") ? Events.shared.solidChange(since: Int64(r.int("targetSeq", 0))) : nil
            try input.tap(point.x, point.y, holdMs: r.double("hold", Input.tapHoldMs))
            if let receipt = armedReceipt {
                let ending = receipts.awaitCompletion(receipt)
                // "moved": the target moved since the read above (issue #103), which the host
                // taps again only in its CI mode; "delivery": the point was off the target.
                switch receipts.rejection(receipt) {
                case .moved:
                    w.put("withheld", "moved").put("seq", Events.shared.seq)
                    return
                case .offTarget:
                    w.put("withheld", "delivery").put("seq", Events.shared.seq)
                    return
                case .none:
                    break
                }
                // Issue #131: the touch reached the app and ended cancelled, so nothing took it
                // as a tap (unless its control reported its action: then it did act).
                // Issue #134: how the touch ended goes in the answer, for the host's trace.
                w.put("touch", ending.word)
                if ending == .cancelled && !receipts.confirmed(receipt) {
                    w.put("cancelled", true).put("probed", probed).put("seq", Events.shared.seq)
                    return
                }
                if ending == .ended(targeted: true) { w.put("targeted", true) }
            }
            if let changedSinceRead { w.put("changedSinceRead", changedSinceRead) }
        case "doubleTap": try input.taps(try r.need("x"), try r.need("y"), count: 2)
        case "press": try input.tap(try r.need("x"), try r.need("y"), holdMs: r.double("ms", 3000))
        case "swipe":
            if r.bool("drag", false) {
                let x: Double = try r.need("x1"), y: Double = try r.need("y1")
                let flutter = probed && FlutterAdapter.ownsPoint(pid: fgPid, x: x, y: y)
                w.put("flutterDrag", flutter)
                try input.drag(x, y, try r.need("x2"), try r.need("y2"),
                               tailSpeed: flutter ? FlutterAdapter.dragSpeed : Gestures.dragTailPtPerS, ci: r.bool("ci", false))
            } else {
                try input.swipe(try r.need("x1"), try r.need("y1"), try r.need("x2"), try r.need("y2"), ms: r.double("ms", 400),
                                rest: r.bool("rest", false), ci: r.bool("ci", false))
            }
            // Issue #111: testmanagerd confirmed it only when its wait for the app timed out.
            if input.lastImplicitlyConfirmed { w.put("unconfirmed", true) }
        case "key": try key(r, by: deadline, doing: doing)
        case "type":
            let text = try r.needStr("text")
            let id = r.str("targetID")
            typingShell.newOp()
            try typist.type(text, target: id.isEmpty ? nil : (id, Int32(r.int("targetPID", 0))), by: deadline!, ci: r.bool("ci", false))
        case "erase":
            let id = r.str("targetID")
            typingShell.newOp()
            try typist.erase(r.int("n", 50), target: id.isEmpty ? nil : (id, Int32(r.int("targetPID", 0))), by: deadline!, ci: r.bool("ci", false))
        case "clear":
            typingShell.newOp()
            w.put("cleared", try typist.clear(by: deadline!))
        case "hideKeyboard":
            input.hostCI = r.bool("ci", false) // its swipes wait for SpringBoard in CI mode (#137)
            do { w.put("hidden", try hideKeyboard(by: deadline?.answerBy)) } catch is Bounded.TimedOut {
                throw hideKeyboardTries > 0
                    ? AppWait.notAnsweringAfter(doing, since: deadline?.start ?? 0, "checking whether the keyboard shows",
                                                "\(hideKeyboardTries) of its tries were sent")
                    : AppWait.notAnswering(doing, since: deadline?.start ?? 0, "checking whether the keyboard shows", nothingSent)
            }
        default: break
        }
        let t1 = DispatchTime.now().uptimeNanoseconds
        w.put("injectMs", Int((t1 - t0) / 1_000_000))
        if settle {
            var timeout = r.int("timeout", 5000)
            if let deadline { timeout = min(timeout, deadline.answerBy > t1 ? Int((deadline.answerBy - t1) / 1_000_000) : 0) }
            let noChange = r.int("noChange", 250)
            let token = Events.shared.requestIdleToken(capMs: min(timeout, Events.shared.animationCapMs))
            var res = Events.shared.awaitSettled(since: sinceSolid, noChangeMs: noChange, timeoutMs: timeout, idleToken: token, reactOnly: r.bool("react", false))
            if res == .noChange, let before, let after = try? appRead(by: deadline?.answerBy, "reading the screen", { try self.tree.signature() }), after != before {
                // It changed without saying so: count it, and let it settle.
                Events.shared.change(structural: false, pid: fgPid)
                let left = max(0, timeout - Int((DispatchTime.now().uptimeNanoseconds - t1) / 1_000_000))
                res = Events.shared.awaitSettled(since: sinceSolid, noChangeMs: 0, timeoutMs: left, idleToken: nil)
                w.put("seenBy", "read")
            }
            w.put("result", res.rawValue).put("settleMs", Int((DispatchTime.now().uptimeNanoseconds - t1) / 1_000_000))
        }
        if let receipt = armedReceipt, receipts.confirmed(receipt) { w.put("clicked", true) }
        w.put("probed", probed).put("seq", Events.shared.seq)
    }

    func keyboardShown() -> Bool {
        onMain {
            let fg = Apps.shared.foreground()
            guard !fg.bundle.isEmpty else { return false }
            return Apps.shared.app(fg.bundle).keyboards.firstMatch.exists
        }
    }

    /// Hides the keyboard. In an app with the probe: its first responder resigns (the probe
    /// does it on request: a Darwin notification), which works on any screen. Otherwise as
    /// the classic way: a short drag in the middle of the screen (scroll views dismiss on drag),
    /// then sideways. Each try is followed by an event-driven settle. With a deadline (uptime
    /// ns), no read waits for the app past it (Bounded.TimedOut) and no try starts after it, or
    /// waits for its input's confirmation past it; hideKeyboardTries says how many were sent.
    func hideKeyboard(by deadline: UInt64? = nil) throws -> Bool {
        hideKeyboardTries = 0
        func read<T>(_ f: @escaping () -> T) throws -> T {
            guard let deadline else { return onMain(f) }
            return try Bounded.run(on: .main, by: deadline, f)
        }
        func leftMs(_ ms: Int) -> Int {
            guard let deadline else { return ms }
            let now = DispatchTime.now().uptimeNanoseconds
            return min(ms, deadline > now ? Int((deadline - now) / 1_000_000) : 0)
        }
        if try !read({ self.keyboardShown() }) { return true }
        let fg = try read { Apps.shared.foreground() }
        var tries: [() throws -> Void] = []
        if Events.shared.isProbed(fg.pid) {
            tries.append { notify_post("dev.testbuddy.probe.resign.\(fg.pid)") }
        }
        let size = screenSize()
        let cx = Double(size.width) / 2, cy = Double(size.height) / 2
        let inputMs = { deadline == nil ? nil : max(leftMs(10_000), 1) }
        tries.append { try self.input.swipe(cx, cy, cx, cy - Double(size.height) * 0.03, ms: 50, timeoutMs: inputMs()) }
        tries.append { try self.input.swipe(cx, cy, cx - Double(size.width) * 0.1, cy, ms: 50, timeoutMs: inputMs()) }
        for attempt in tries {
            if deadline != nil && leftMs(1) == 0 { break }
            let since = Events.shared.solid
            hideKeyboardTries += 1
            try attempt()
            let token = Events.shared.requestIdleToken(capMs: min(1500, Events.shared.animationCapMs))
            _ = Events.shared.awaitSettled(since: since, noChangeMs: 300, timeoutMs: leftMs(3000), idleToken: token)
            if try !read({ self.keyboardShown() }) { return true }
        }
        throw AgentError("could not hide the keyboard")
    }

    /// How many tries the last hideKeyboard sent (an error after one says so).
    private var hideKeyboardTries = 0

    /// When a bounded op answers: before the host's reply limit (replyMs, or for type, erase and
    /// clear from a host that does not send it, the host's formula). The time kept for the input
    /// is the keys' (type, erase), or one key's (clear, key, hideKeyboard: their first input).
    private func opDeadline(_ r: Request) -> TypingDeadline {
        let chars: Int
        switch r.op {
        case "type": chars = r.str("text").count
        case "erase": chars = max(r.int("n", 50), 0)
        default: chars = 1
        }
        let reply = r.has("replyMs") ? r.int("replyMs", 0)
            : TypingDeadline.hostReplyMs(timeoutMs: r.int("timeout", 5000), perCharMs: r.op == "type" ? 40 : r.op == "erase" ? 20 : 0, chars: chars)
        return TypingDeadline.forRequest(start: DispatchTime.now().uptimeNanoseconds, replyMs: reply, chars: chars)
    }

    /// What an action op is doing, for its error, and what giving up before any input means.
    static func wording(_ r: Request) -> (doing: String, nothingSent: String) {
        switch r.op {
        case "type": return ("typing", "no keys were sent")
        case "erase": return ("erasing", "no keys were sent")
        case "clear": return ("clearing", "no keys were sent")
        case "key": return ("pressing key \(r.int("code", 0))", "the key was not pressed")
        case "hideKeyboard": return ("hiding the keyboard", "nothing was sent")
        default: return (r.op, "nothing was sent")
        }
    }

    /// The key op, bounded by the op's deadline when the host sent one (issue #130): its
    /// keystroke's confirmation, and a button press (home, lock, volume), which runs on the main
    /// thread, given up on at it. A press given up on before it started was not made (transient).
    private func key(_ r: Request, by deadline: TypingDeadline?, doing: String) throws {
        let code = r.int("code", 0)
        guard let deadline else { return try input.key(code, long: r.bool("long", false)) }
        let left = { () -> Int in
            let now = DispatchTime.now().uptimeNanoseconds
            return deadline.answerBy > now ? Int((deadline.answerBy - now) / 1_000_000) : 0
        }
        do {
            try input.key(code, long: r.bool("long", false), timeoutMs: max(left(), 1), onMain: { f in
                try AppWait.read(on: .main, by: deadline.answerBy, f) { t in
                    t.started
                        ? AppWait.notAnsweringAfter(doing, since: deadline.start, "the button press", "the press was sent")
                        : AppWait.notAnswering(doing, since: deadline.start, "waiting for the main thread, held by an earlier call to the app", "the key was not pressed")
                }
            })
        } catch let e as InputNotSent {
            throw AgentError("\(doing) did not start: \(e.why); the key was not pressed", transient: true)
        }
    }

    // MARK: screenshot, rotation

    private func screenshot(_ r: Request, _ w: JW) throws -> Data {
        let scale = r.double("scale", 1)
        let format = r.str("format", "png").lowercased()
        let quality = r.int("quality", 80)
        let (data, size): (Data?, CGSize) = onMain {
            let shot = XCUIScreen.main.screenshot()
            var img = shot.image
            if scale > 0.05 && scale < 0.99 {
                let target = CGSize(width: max(1, (img.size.width * img.scale * scale).rounded()), height: max(1, (img.size.height * img.scale * scale).rounded()))
                let fmt = UIGraphicsImageRendererFormat()
                fmt.scale = 1
                img = UIGraphicsImageRenderer(size: target, format: fmt).image { _ in img.draw(in: CGRect(origin: .zero, size: target)) }
            }
            let px = CGSize(width: img.size.width * img.scale, height: img.size.height * img.scale)
            if format == "jpeg" || format == "jpg" { return (img.jpegData(compressionQuality: CGFloat(quality) / 100), px) }
            if scale > 0.05 && scale < 0.99 { return (img.pngData(), px) }
            return (shot.pngRepresentation, px)
        }
        guard let data else { throw AgentError("screenshot failed") }
        w.put("w", Int(size.width)).put("h", Int(size.height)).put("format", format == "jpg" ? "jpeg" : format)
        return data
    }

    private func rotate(_ to: String) throws {
        let o: UIDeviceOrientation
        switch to {
        case "portrait": o = .portrait
        case "landscape", "landscapeLeft": o = .landscapeLeft
        case "landscapeRight": o = .landscapeRight
        case "upsideDown": o = .portraitUpsideDown
        default: throw AgentError("unknown orientation \"\(to)\"")
        }
        let since = Events.shared.solid
        let turned = Events.shared.orientationChanged
        let already = onMain { XCUIDevice.shared.orientation == o }
        onMain { XCUIDevice.shared.orientation = o }
        if already { return }
        // The app turns after this returns. UIKit's accessibility posts OrientationChanged (1011)
        // once the rotation's animation is over (~340 ms; touches during it are lost): wait for
        // it (an app that does not support the orientation never turns: 1.5 s cap), then settle.
        _ = Events.shared.awaitOrientationChanged(since: turned, timeoutMs: 1500)
        let token = Events.shared.requestIdleToken(capMs: Events.shared.animationCapMs)
        _ = Events.shared.awaitSettled(since: since, noChangeMs: 0, timeoutMs: 3000, idleToken: token)
        Apps.shared.invalidate()
        _ = try? onMain { try tree.signature() } // refreshes the UI's orientation for touch coordinates
    }
}

/// Where a tree read is, for the watchdog that logs a stuck one (set and read on any thread).
final class ReadStage: @unchecked Sendable {
    private let lock = NSLock()
    private var stage = "waiting for the main thread"
    private var reachedTime: UInt64?
    func set(_ s: String) { lock.lock(); stage = s; lock.unlock() }
    /// On the main thread: the read reached it and starts the snapshot.
    func reached() {
        let t = DispatchTime.now().uptimeNanoseconds
        lock.lock(); stage = "in the snapshot (the main thread was reached)"; reachedTime = t; lock.unlock()
    }
    /// When the read reached the main thread, if it did.
    var reachedAt: UInt64? { lock.lock(); defer { lock.unlock() }; return reachedTime }
    private var _givenUp = false
    /// The op answered without the read (its deadline passed).
    func giveUp() { lock.lock(); _givenUp = true; lock.unlock() }
    var givenUp: Bool { lock.lock(); defer { lock.unlock() }; return _givenUp }
    /// The stage, or nil once the read is done.
    func unfinished() -> String? { lock.lock(); defer { lock.unlock() }; return stage == "done" ? nil : stage }
}

// A receipt is positive evidence for a named UIKit action. Absence is ambiguous
// (e.g. a SwiftUI gesture); it can never authorize repeating a physical tap.
private final class ControlReceipts {
    private var registration = PidRegistration()
    private var request: Int32 = -1
    private var result: Int32 = -1
    private var pointToken: Int32 = -1
    private var rejectedToken: Int32 = -1
    private var completedToken: Int32 = -1
    private var deadlineToken: Int32 = -1
    private var frameToken: Int32 = -1
    private let completionQueue = DispatchQueue(label: "dev.testbuddy.control.completed")
    private var counter: UInt32 = UInt32.random(in: 1 ... (UInt32.max >> 2)) // TouchGuard.nonce

    deinit { close() }
    private func close() {
        if request >= 0 { notify_cancel(request); request = -1 }
        if result >= 0 { notify_cancel(result); result = -1 }
        if pointToken >= 0 { notify_cancel(pointToken); pointToken = -1 }
        if rejectedToken >= 0 { notify_cancel(rejectedToken); rejectedToken = -1 }
        if completedToken >= 0 { notify_cancel(completedToken); completedToken = -1 }
        if deadlineToken >= 0 { notify_cancel(deadlineToken); deadlineToken = -1 }
        if frameToken >= 0 { notify_cancel(frameToken); frameToken = -1 }
    }
    /// frame: the target as read just before the touch; the probe withholds the touch if it
    /// moved since (TBTouchGuard.h). nil: only the point is checked.
    func arm(pid: Int32, id: String, point: CGPoint, frame: CGRect?) -> UInt64? {
        let registered = registration.ensure(pid) {
            close()
            guard notify_register_check("dev.testbuddy.control.request.\(pid)", &request) == NOTIFY_STATUS_OK,
                  notify_register_check("dev.testbuddy.control.result.\(pid)", &result) == NOTIFY_STATUS_OK,
                  notify_register_check("dev.testbuddy.control.point.\(pid)", &pointToken) == NOTIFY_STATUS_OK,
                  notify_register_check("dev.testbuddy.control.rejected.\(pid)", &rejectedToken) == NOTIFY_STATUS_OK,
                  notify_register_dispatch("dev.testbuddy.control.completed.\(pid)", &completedToken, completionQueue, { _ in
                      Events.shared.signal {}
                  }) == NOTIFY_STATUS_OK,
                  notify_register_check("dev.testbuddy.control.deadline.\(pid)", &deadlineToken) == NOTIFY_STATUS_OK,
                  notify_register_check("dev.testbuddy.control.frame.\(pid)", &frameToken) == NOTIFY_STATUS_OK else {
                close()
                return false
            }
            return true
        }
        guard registered, request >= 0, result >= 0 else { return nil }
        let xy = UInt64(UInt32(bitPattern: Int32(point.x.rounded()))) << 32 | UInt64(UInt32(bitPattern: Int32(point.y.rounded())))
        // A lost connection must never leave a future manual touch armed.
        let deadline = DispatchTime.now().uptimeNanoseconds + 1_000_000_000
        guard notify_set_state(pointToken, xy) == NOTIFY_STATUS_OK,
              notify_set_state(frameToken, frame.map(TouchGuard.pack) ?? 0) == NOTIFY_STATUS_OK,
              notify_set_state(deadlineToken, deadline) == NOTIFY_STATUS_OK else { return nil }
        counter = TouchGuard.next(counter)
        let hash = id.utf8.reduce(UInt32(2166136261)) { ($0 ^ UInt32($1)) &* 16777619 }
        let token = TouchGuard.nonce(counter: counter, idHash: hash)
        return notify_set_state(request, token) == NOTIFY_STATUS_OK ? token : nil
    }
    func confirmed(_ token: UInt64) -> Bool {
        var value: UInt64 = 0
        return notify_get_state(result, &value) == NOTIFY_STATUS_OK && value == token
    }
    /// Whether, and why, the probe withheld the touch with this token.
    func rejection(_ token: UInt64) -> TouchGuard.Rejection {
        var value: UInt64 = 0
        guard notify_get_state(rejectedToken, &value) == NOTIFY_STATUS_OK else { return .none }
        return TouchGuard.rejection(value, token: token)
    }
    func awaitCompletion(_ token: UInt64) -> TouchGuard.Receipt {
        // XCTest's early completion may precede sendEvent's touch-end dispatch. Wait
        // for this nonce, not an unrelated input callback. No callback is ambiguous.
        var value: UInt64 = 0
        let matched = Events.shared.awaitInput({
            notify_get_state(completedToken, &value) == NOTIFY_STATUS_OK && TouchGuard.receipt(value, token: token) != .none
        }, inputEndConfirms: nil, timeoutMs: 100)
        return matched ? TouchGuard.receipt(value, token: token) : .none
    }
    func disarm() { if request >= 0 { notify_set_state(request, 0) } }
}

/// The typing's XCTest reads and input; Typing.swift decides when to use them and bounds every
/// read by the op's deadline (issue #46).
final class TypingShell: TypingDevice {
    private unowned let ops: Ops
    init(ops: Ops) { self.ops = ops }

    var readQueue: DispatchQueue { .main }

    func foregroundPID() -> Int32 { Apps.shared.foreground().pid }
    /// One snapshot (Tree.focus). A snapshot the app did not give is no focus and no keyboard,
    /// as the queries it replaced answered (issue #51); logged once an op.
    func focus() -> FocusState {
        do { return try ops.tree.focus() } catch {
            lock.lock()
            let first = !failureLogged
            failureLogged = true
            lock.unlock()
            if first { NSLog("tb: reading the focus: %@ (later failures in this op are not logged)", "\(error)") }
            return FocusState(field: nil, keyboard: false)
        }
    }

    /// Whether this op logged a failed focus read. Reset by the ops queue, set on the main
    /// thread (a read the op gave up on can still end there).
    private let lock = NSLock()
    private var failureLogged = false
    func newOp() { lock.lock(); failureLogged = false; lock.unlock() }
    func field(id: String, pid: Int32) throws -> FieldGeometry { try ops.tree.field(id: id, pid: pid) }

    /// The tap op's touch: synthesized, with no wait for the app to go idle.
    func tap(_ p: CGPoint, timeoutMs: Int) throws { try ops.input.tap(p.x, p.y, timeoutMs: timeoutMs) }
    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws { try ops.input.typeNow(text, cps: cps, timeoutMs: timeoutMs) }
    func selectAll(timeoutMs: Int) throws { try ops.input.selectAll(timeoutMs: timeoutMs) }
    func hideKeyboard(withinMs: Int) throws -> Bool {
        try ops.hideKeyboard(by: DispatchTime.now().uptimeNanoseconds + UInt64(max(withinMs, 0)) * 1_000_000)
    }
    /// The refocus outside the host's CI mode, as before issue #46: XCUIElement.tap(), which
    /// first waits for the app to go idle. Runs on the main thread (readQueue).
    func tapElement(id: String, pid: Int32) throws {
        let fg = Apps.shared.foreground()
        guard fg.pid == pid else { throw AgentError("process changed while refocusing \(id)") }
        let fields = Apps.shared.app(fg.bundle).descendants(matching: .any).matching(identifier: id)
        guard fields.count == 1 else { throw AgentError("cannot uniquely identify field \(id)") }
        let field = fields.element(boundBy: 0)
        guard [.textField, .secureTextField, .textView, .searchField].contains(field.elementType) else {
            throw AgentError("expected an editable field at \(id)")
        }
        field.tap()
    }

    var seq: Int64 { Events.shared.seq }
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool { Events.shared.awaitChange(since: since, timeoutMs: timeoutMs) }
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool { Events.shared.awaitCondition(timeoutMs: timeoutMs, check) }
    func log(_ s: String) { NSLog("%@", s) }
}

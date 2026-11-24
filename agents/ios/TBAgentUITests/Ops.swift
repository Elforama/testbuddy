import XCTest
import notify

final class Ops {
    let tree = Tree()
    lazy var input = Input(tree: tree)
    private let receipts = ControlReceipts()
    private let presses = PressProbe()
    private lazy var typingShell = TypingShell(ops: self)
    private lazy var typist = Typist(device: typingShell)
    private(set) var exitRequested = false
    let vitals = Vitals()

    private let screen: CGSize

    init() {
        screen = UIScreen.main.bounds.size
        noteVitals()
        _ = input
    }

    func hello() -> [UInt8] {
        let size = vitals.size
        let w = JW().obj().put("ev", "hello").put("platform", "ios").put("version", Agent.shared.build.isEmpty ? Agent.version : Agent.shared.build)
            .put("agent", Agent.version).put("build", Agent.shared.build).put("udid", Agent.shared.udid)
            .put("w", size.w).put("h", size.h).put("seq", Events.shared.seq).put("pid", Int(getpid()))
            .put("axHook", Events.shared.hookInstalled)
        return w.end().bytes
    }

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

    func noteVitals() {
        let size = screenSize()
        vitals.set(w: Int(size.width), h: Int(size.height), keyboard: tree.lastKeyboard, treeMode: tree.mode, quietMs: Events.shared.quietMs)
    }

    private func screenSize() -> CGSize {
        let f = tree.lastAppFrame
        if f.width > 0 && f.height > 0 { return f.size }
        return screen
    }

    static let issues = IssueSink()

    func handle(_ r: Request, _ w: JW) throws -> Data? {
        Ops.issues.begin()
        let data: Data?
        do {
            data = try perform(r, w)
        } catch let e as AgentError {
            if let x = Ops.issues.failure(Ops.wording(r).doing) {
                throw AgentError("\(e.message) (\(x.message))", transient: e.transient && x.transient)
            }
            throw e
        }
        if let x = Ops.issues.failure(Ops.wording(r).doing) {
            NSLog("tb: \(x.message)")
            throw x
        }
        return data
    }

    private func perform(_ r: Request, _ w: JW) throws -> Data? {
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
            let asked = DispatchTime.now().uptimeNanoseconds
            let deadline = r.has("replyMs") ? TypingDeadline.forRequest(start: asked, replyMs: r.int("replyMs", 0), chars: 0) : nil
            if r.bool("waitQuiet", false) {
                var quietMs = r.int("timeout", 3000)
                if let deadline {
                    let now = DispatchTime.now().uptimeNanoseconds
                    quietMs = min(quietMs, deadline.answerBy > now ? Int((deadline.answerBy - now) / 2_000_000) : 0)
                }
                _ = Events.shared.awaitQuiet(timeoutMs: quietMs)
            }
            let seq = Events.shared.seq
            let mode = r.has("mode") ? r.str("mode") : nil
            let ci = r.bool("ci", false)
            let stage = ReadStage()
            DispatchQueue.global().asyncAfter(deadline: .now() + 10) {
                if let where_ = stage.unfinished() { NSLog("tb: tree read still running after 10 s: \(where_)") }
            }
            defer { stage.set("done") }
            do {
                let fields = try ScreenRead.run(on: .main, by: deadline) { [tree] j in
                    stage.reached()
                    try tree.write(j, mode: mode, seq: seq, ci: ci, wanted: { !stage.givenUp })
                }
                w.fields(of: fields)
            } catch let e as AgentError where e.transient {
                stage.giveUp()
                NSLog("tb: \(e.message)")
                throw e
            }
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
        case "delivery":
            let v = Events.shared.awaitDelivery(token: r.int64("token", 0), waitMs: min(r.int("timeout", 2500), 5000))
            w.put("verdict", v.verdict.rawValue).put("seq", Events.shared.seq)
            if v.pending { w.put("pending", true) }
            if let at = v.evidence.reportAt { w.put("reportMs", Int(((at - v.liftAt) * 1000).rounded())) }
            if v.evidence.implicit { w.put("implicit", true) }
            let a = w.key("answerMs").arr()
            for lag in v.evidence.answerLags { a.int(Int((lag * 1000).rounded())) }
            a.end()
        case "quiet":
            w.put("quiet", Events.shared.awaitQuiet(timeoutMs: r.int("timeout", 5000))).put("seq", Events.shared.seq)
        case "awaitPackage":
            let pkg = try r.needStr("pkg")
            if r.bool("readTree", false) {
                try awaitLaunchTree(pkg, pid: Int32(r.int("pid", 0)), timeout: r.int("timeout", 30000), ci: r.bool("ci", false), into: w)
                break
            }
            var behindAlert = false
            var inFront: Int32 = 0
            let opStart = DispatchTime.now().uptimeNanoseconds
            let timeout = r.int("timeout", 10000)
            let found = Events.shared.awaitCondition(timeoutMs: timeout) {
                Apps.shared.invalidate()
                let front = Apps.shared.foreground()
                let fg = front.bundle
                if fg == pkg { inFront = front.pid; return true }
                if fg == Apps.springboard, onMain({ Apps.shared.app(pkg).state == .runningForeground }) {
                    behindAlert = true
                    return true
                }
                return false
            }
            if found && !behindAlert { awaitInputReady(launched: inFront, leftMs: Ops.leftMs(timeout, since: opStart), into: w) }
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

        case "launch":
            let id = try r.needStr("bundleId")
            let args = r.strings("args"), env = r.dict("env")
            try device(deadlineMs: Ops.lifecycleGateMs, doing: "launching \(id)") {
                let app = Apps.shared.app(id)
                app.launchArguments = args
                app.launchEnvironment = env
                app.launch()
            }
            Apps.shared.invalidate()
            w.put("state", onMain { Int(Apps.shared.app(id).state.rawValue) })
        case "terminate":
            let id = try r.needStr("bundleId")
            try device(deadlineMs: Ops.lifecycleGateMs, doing: "terminating \(id)") { Apps.shared.app(id).terminate() }
            Apps.shared.invalidate()
        case "activate":
            let id = try r.needStr("bundleId")
            let opStart = DispatchTime.now().uptimeNanoseconds
            try device(deadlineMs: Ops.lifecycleGateMs, doing: "activating \(id)") { Apps.shared.app(id).activate() }
            Apps.shared.invalidate()
            awaitInputReady(launched: 0, leftMs: Ops.leftMs(Ops.lifecycleGateMs, since: opStart), into: w)
        case "appState":
            let id = try r.needStr("bundleId")
            w.put("state", onMain { Int(Apps.shared.app(id).state.rawValue) })
        case "treeMode":
            if r.has("mode") { tree.mode = r.str("mode") }
            w.put("mode", tree.mode)

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
            let ids = Set(r.ints("ids").map { Int32($0) })
            Events.shared.q.sync { Events.shared.changeKinds = ids }
        case "axStats":
            w.key("counts").obj()
            for (k, v) in Events.shared.stats().sorted(by: { $0.key < $1.key }) { w.put(String(k), v) }
            w.end()
            w.key("registered").arr(); for k in Events.shared.registered.keys.sorted() { w.int(k) }; w.end()
        case "fgReport":
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
            let pid = Apps.shared.foreground().pid
            let x: Double = try r.need("x"), y: Double = try r.need("y")
            let owned = Events.shared.isProbed(pid) && Events.shared.awaitCondition(timeoutMs: r.int("timeout", 0)) {
                Apps.shared.foreground().pid == pid && FlutterAdapter.ownsPoint(pid: pid, x: x, y: y)
            }
            w.put("flutter", owned)
        case "idle":
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

    private func awaitInputReady(launched pid: Int32, leftMs: Int, into w: JW) {
        let pid = pid > 0 ? pid : Apps.shared.foreground().pid
        let ready = Events.shared.awaitInputReady(pid, leftMs: leftMs)
        if ready.end != .none { w.put("awaitedInputMs", ready.waitedMs) }
        if !ready.ready { w.put("appActive", false) }
    }

    static func leftMs(_ timeoutMs: Int, since: UInt64) -> Int {
        let used = Int((DispatchTime.now().uptimeNanoseconds &- since) / 1_000_000)
        return max(0, timeoutMs - used)
    }

    private func awaitLaunchTree(_ pkg: String, pid: Int32, timeout: Int, ci: Bool, into w: JW) throws {
        Apps.shared.forgetProcess(pkg)
        var failure: Error?
        var lastTransient: Error?
        var notAnswering = false
        let now = { DispatchTime.now().uptimeNanoseconds }
        let hold = LaunchHold(now: now(), timeoutMs: timeout)
        let found = Events.shared.awaitCondition(timeoutMs: timeout, recheckMs: {
            notAnswering ? LaunchWait.recheckMs : hold.recheckMs(now: now())
        }) {
            if pid > 0, kill(pid, 0) == -1, errno == ESRCH {
                failure = AgentError("launched process \(pkg) pid \(pid) exited before the first tree")
                return true
            }
            let seq = Events.shared.seq
            Apps.shared.invalidate()
            if pid > 0 {
                let phase = Events.shared.appPhase(pid)
                if hold.holds(phase: phase, inFront: phase.holdsInput && Apps.shared.foreground().pid == pid, now: now()) { return false }
            }
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
            throw AgentError("launch of \(pkg) pid \(pid) had no readable tree within \(timeout) ms: \(lastTransient?.localizedDescription ?? "waiting for foreground window")", transient: notAnswering)
        }
        if let held = hold.heldMs(now: now()) {
            w.put("awaitedInputMs", held)
            if hold.timedOut {
                w.put("appActive", false)
                NSLog("tb: launch of %@ pid %d: still entering after %d ms; reading the tree anyway", pkg, pid, held)
            }
        }
        w.put("found", true)
    }

    private func action(_ r: Request, _ w: JW) throws {
        var since = Events.shared.seq
        var sinceSolid = Events.shared.solid
        let settle = r.bool("settle", true)
        let typing = r.op == "type" || r.op == "erase"
        let deadline: TypingDeadline? = typing || r.op == "clear" || r.has("replyMs") ? opDeadline(r) : nil
        let (doing, nothingSent) = Ops.wording(r)
        func appRead<T>(by: UInt64?, _ what: String, _ f: @escaping () throws -> T) throws -> T {
            try AppWait.read(on: .main, by: deadline == nil ? nil : by, f) { t in
                AppWait.notAnswering(doing, since: deadline?.start ?? 0, Typist.waitedFor(what, t), nothingSent)
            }
        }
        let fgPid = try appRead(by: deadline?.keysBy, "checking which app is in front") { Apps.shared.foreground().pid }
        let probed = Events.shared.isProbed(fgPid)
        if probed {
            let left = (deadline?.keysBy).map { by -> Int in
                let now = DispatchTime.now().uptimeNanoseconds
                return by > now ? Int((by - now) / 1_000_000) : 0
            }
            let ready = Events.shared.awaitInputReady(fgPid, leftMs: left)
            if ready.end != .none {
                w.put("awaitedInputMs", ready.waitedMs)
                since = Events.shared.seq
                sinceSolid = Events.shared.solid
            }
            if !ready.ready { w.put("appActive", false) }
        }
        var before: Int?
        if settle && !probed {
            if let s = tree.lastSignature, s.seq == since { before = s.hash } else {
                before = try? appRead(by: deadline?.keysBy, "reading the screen") { try self.tree.signature() }
            }
        }
        var armedReceipt: UInt64?
        defer { if armedReceipt != nil { receipts.disarm() } }
        var pressAsked: UInt16?
        defer { if pressAsked != nil { presses.disarm() } }
        if probed && (r.op == "tap" || r.op == "doubleTap" || r.op == "press") { pressAsked = presses.arm(pid: fgPid) }
        let t0 = DispatchTime.now().uptimeNanoseconds
        switch r.op {
        case "tap":
            let point = CGPoint(x: try r.need("x"), y: try r.need("y"))
            let id = r.str("targetID")
            var frame: CGRect?
            if !id.isEmpty {
                guard fgPid == Int32(r.int("targetPID", 0)) else { throw AgentError("process changed before tap on \(id)") }
                let check = try onMain { try tree.containsTap(id: id, pid: fgPid, point: point) }
                w.put("guarded", true)
                switch check {
                case .tap(let f):
                    frame = f
                case .withheld(let why, let live):
                    w.put("withheld", "preflight").put("withheldWhy", why)
                    if let live, live.minX.isFinite, live.minY.isFinite, live.maxX.isFinite, live.maxY.isFinite {
                        w.key("liveFrame").arr().int(Int(live.minX.rounded())).int(Int(live.minY.rounded()))
                            .int(Int(live.maxX.rounded())).int(Int(live.maxY.rounded())).end()
                    }
                    w.put("seq", Events.shared.seq)
                    return
                }
            }
            let still = r.bool("targetMoving", false) ? nil : frame
            let watch = id.isEmpty ? TouchGuard.watchFrame(r.ints("watchFrame")) : nil
            if probed && !id.isEmpty {
                armedReceipt = receipts.arm(pid: fgPid, id: id, point: point, frame: still)
            } else if probed, let watch {
                armedReceipt = receipts.arm(pid: fgPid, idHash: TouchGuard.watchedTouch, point: point, frame: watch)
            }
            let changedSinceRead = !id.isEmpty && r.has("targetSeq") ? Events.shared.solidChange(since: Int64(r.int("targetSeq", 0))) : nil
            try input.tap(point.x, point.y, holdMs: r.double("hold", Input.tapHoldMs))
            var probeSawTouch = false
            if let receipt = armedReceipt, watch != nil {
                let ending = receipts.awaitCompletion(receipt)
                probeSawTouch = ending != .none
                if ending == .ended(targeted: true) { w.put("reached", true) }
            } else if let receipt = armedReceipt {
                let ending = receipts.awaitCompletion(receipt)
                probeSawTouch = ending != .none
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
                w.put("touch", ending.word)
                if ending == .cancelled && !receipts.confirmed(receipt) {
                    w.put("cancelled", true).put("probed", probed).put("seq", Events.shared.seq)
                    return
                }
                if ending == .ended(targeted: true) { w.put("targeted", true) }
            }
            if let changedSinceRead { w.put("changedSinceRead", changedSinceRead) }
            if Delivery.tracks(ci: r.bool("ci", false), probeSawTouch: probeSawTouch, app: fgPid), let rec = input.lastRecord {
                w.put("delivery", Events.shared.newDelivery(app: fgPid, start: rec.start, liftAt: rec.liftAt))
            }
        case "doubleTap":
            try input.taps(try r.need("x"), try r.need("y"), count: 2)
            if Delivery.tracks(ci: r.bool("ci", false), probeSawTouch: false, app: fgPid), let rec = input.lastRecord {
                w.put("delivery", Events.shared.newDelivery(app: fgPid, start: rec.start, liftAt: rec.liftAt))
            }
        case "press": try input.tap(try r.need("x"), try r.need("y"), holdMs: r.double("ms", 3000))
        case "swipe":
            if r.double("hold", 0) > 0 {
                try input.holdDrag(try r.need("x1"), try r.need("y1"), try r.need("x2"), try r.need("y2"),
                                   holdMs: r.double("hold", 0), ms: r.double("ms", 1000), ci: r.bool("ci", false))
            } else if r.bool("drag", false) {
                let x: Double = try r.need("x1"), y: Double = try r.need("y1")
                let flutter = probed && FlutterAdapter.ownsPoint(pid: fgPid, x: x, y: y)
                w.put("flutterDrag", flutter)
                let ci = r.bool("ci", false), tail = Gestures.tail(flutter: flutter, ci: ci)
                try input.drag(x, y, try r.need("x2"), try r.need("y2"), tailSpeed: tail.speed, tailPt: tail.pt, ci: ci)
            } else {
                try input.swipe(try r.need("x1"), try r.need("y1"), try r.need("x2"), try r.need("y2"), ms: r.double("ms", 400),
                                rest: r.bool("rest", false), ci: r.bool("ci", false))
            }
            if input.lastImplicitlyConfirmed { w.put("unconfirmed", true) }
            if Delivery.tracks(ci: r.bool("ci", false), probeSawTouch: false, app: fgPid), let rec = input.lastRecord {
                w.put("delivery", Events.shared.newDelivery(app: fgPid, start: rec.start, liftAt: rec.liftAt, implicitAt: rec.implicitAt))
            }
        case "key": try key(r, by: deadline, doing: doing)
        case "type":
            let text = try r.needStr("text")
            let id = r.str("targetID")
            typingShell.newOp()
            try typist.type(text, target: id.isEmpty ? nil : (id, Int32(r.int("targetPID", 0))), by: deadline!, ci: r.bool("ci", false))
        case "erase":
            let id = r.str("targetID")
            typingShell.newOp()
            if try typist.erase(r.int("n", 50), target: id.isEmpty ? nil : (id, Int32(r.int("targetPID", 0))), by: deadline!, ci: r.bool("ci", false)) {
                w.put("erase", "repaired")
            }
        case "clear":
            typingShell.newOp()
            w.put("cleared", try typist.clear(by: deadline!))
        case "hideKeyboard":
            input.hostCI = r.bool("ci", false)
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
                Events.shared.change(structural: false, pid: fgPid)
                let left = max(0, timeout - Int((DispatchTime.now().uptimeNanoseconds - t1) / 1_000_000))
                res = Events.shared.awaitSettled(since: sinceSolid, noChangeMs: 0, timeoutMs: left, idleToken: nil)
                w.put("seenBy", "read")
            }
            w.put("result", res.rawValue).put("settleMs", Int((DispatchTime.now().uptimeNanoseconds - t1) / 1_000_000))
        }
        if let receipt = armedReceipt, r.str("targetID").count > 0, receipts.confirmed(receipt) { w.put("clicked", true) }
        if let nonce = pressAsked, let press = presses.read(nonce), press.cutShort {
            w.put("pressSeenMs", press.seenMs).put("pressMs", press.pressMs).put("pressStallMs", press.stallMs)
        }
        w.put("probed", probed).put("seq", Events.shared.seq)
    }

    func keyboardShown() -> Bool {
        onMain {
            let fg = Apps.shared.foreground()
            guard !fg.bundle.isEmpty else { return false }
            return Apps.shared.app(fg.bundle).keyboards.firstMatch.exists
        }
    }

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

    private var hideKeyboardTries = 0

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

    static func wording(_ r: Request) -> (doing: String, nothingSent: String) {
        switch r.op {
        case "type": return ("typing", "no keys were sent")
        case "erase": return ("erasing", "no keys were sent")
        case "clear": return ("clearing", "no keys were sent")
        case "key": return ("pressing key \(r.int("code", 0))", "the key was not pressed")
        case "hideKeyboard": return ("hiding the keyboard", "nothing was sent")
        case "rotate": return ("rotating the device", "the device was not turned")
        case "launch", "terminate", "activate": return ("\(r.op == "launch" ? "launching" : r.op == "terminate" ? "terminating" : "activating") \(r.str("bundleId"))", "nothing was sent")
        default: return (r.op, "nothing was sent")
        }
    }

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

    static let rotateGateMs = 5000
    static let lifecycleGateMs = 10_000

    private func device(deadlineMs: Int, doing: String = "acting on the device", nothingSent: String = "nothing was sent",
                        _ f: @escaping () throws -> Void) throws {
        do {
            try input.gate.call(deadlineMs: deadlineMs, on: { try onMain($0) }, f)
        } catch let e as InputNotSent {
            throw AgentError("\(doing) did not start: \(e.why); \(nothingSent)", transient: true)
        }
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
        try device(deadlineMs: Ops.rotateGateMs, doing: "rotating the device", nothingSent: "the device was not turned") {
            XCUIDevice.shared.orientation = o
        }
        if already { return }
        _ = Events.shared.awaitOrientationChanged(since: turned, timeoutMs: 1500)
        let token = Events.shared.requestIdleToken(capMs: Events.shared.animationCapMs)
        _ = Events.shared.awaitSettled(since: since, noChangeMs: 0, timeoutMs: 3000, idleToken: token)
        Apps.shared.invalidate()
        _ = try? onMain { try tree.signature() }
    }
}

final class ReadStage: @unchecked Sendable {
    private let lock = NSLock()
    private var stage = "waiting for the main thread"
    private var reachedTime: UInt64?
    func set(_ s: String) { lock.lock(); stage = s; lock.unlock() }
    func reached() {
        let t = DispatchTime.now().uptimeNanoseconds
        lock.lock(); stage = "in the snapshot (the main thread was reached)"; reachedTime = t; lock.unlock()
    }
    var reachedAt: UInt64? { lock.lock(); defer { lock.unlock() }; return reachedTime }
    private var _givenUp = false
    func giveUp() { lock.lock(); _givenUp = true; lock.unlock() }
    var givenUp: Bool { lock.lock(); defer { lock.unlock() }; return _givenUp }
    func unfinished() -> String? { lock.lock(); defer { lock.unlock() }; return stage == "done" ? nil : stage }
}

private final class PressProbe {
    private var registration = PidRegistration()
    private var request: Int32 = -1
    private var result: Int32 = -1
    private var nonce = UInt16.random(in: 1 ... UInt16.max)
    private var name = ""

    deinit { close() }
    private func close() {
        if request >= 0 { notify_cancel(request); request = -1 }
        if result >= 0 { notify_cancel(result); result = -1 }
    }
    private func ask(_ value: UInt64) -> Bool {
        guard notify_set_state(request, value) == NOTIFY_STATUS_OK else { return false }
        notify_post(name)
        return true
    }
    func arm(pid: Int32) -> UInt16? {
        let registered = registration.ensure(pid) {
            close()
            name = "dev.testbuddy.press.request.\(pid)"
            guard notify_register_check(name, &request) == NOTIFY_STATUS_OK,
                  notify_register_check("dev.testbuddy.press.result.\(pid)", &result) == NOTIFY_STATUS_OK else {
                close()
                return false
            }
            return true
        }
        guard registered, request >= 0 else { return nil }
        nonce = TouchGuard.Press.next(nonce)
        return ask(UInt64(nonce)) ? nonce : nil
    }
    func read(_ nonce: UInt16) -> TouchGuard.Press? {
        var value: UInt64 = 0
        guard result >= 0, notify_get_state(result, &value) == NOTIFY_STATUS_OK else { return nil }
        return TouchGuard.Press.read(value, nonce: nonce)
    }
    func disarm() { if request >= 0 { _ = ask(0) } }
}

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
    private var counter: UInt32 = UInt32.random(in: 1 ... (UInt32.max >> 2))

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
    func arm(pid: Int32, id: String, point: CGPoint, frame: CGRect?) -> UInt64? {
        arm(pid: pid, idHash: id.utf8.reduce(UInt32(2166136261)) { ($0 ^ UInt32($1)) &* 16777619 }, point: point, frame: frame)
    }
    func arm(pid: Int32, idHash hash: UInt32, point: CGPoint, frame: CGRect?) -> UInt64? {
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
        let deadline = DispatchTime.now().uptimeNanoseconds + 1_000_000_000
        guard notify_set_state(pointToken, xy) == NOTIFY_STATUS_OK,
              notify_set_state(frameToken, frame.map(TouchGuard.pack) ?? 0) == NOTIFY_STATUS_OK,
              notify_set_state(deadlineToken, deadline) == NOTIFY_STATUS_OK else { return nil }
        counter = TouchGuard.next(counter)
        let token = TouchGuard.nonce(counter: counter, idHash: hash)
        return notify_set_state(request, token) == NOTIFY_STATUS_OK ? token : nil
    }
    func confirmed(_ token: UInt64) -> Bool {
        var value: UInt64 = 0
        return notify_get_state(result, &value) == NOTIFY_STATUS_OK && value == token
    }
    func rejection(_ token: UInt64) -> TouchGuard.Rejection {
        var value: UInt64 = 0
        guard notify_get_state(rejectedToken, &value) == NOTIFY_STATUS_OK else { return .none }
        return TouchGuard.rejection(value, token: token)
    }
    func awaitCompletion(_ token: UInt64) -> TouchGuard.Receipt {
        var value: UInt64 = 0
        let matched = Events.shared.awaitInput({
            notify_get_state(completedToken, &value) == NOTIFY_STATUS_OK && TouchGuard.receipt(value, token: token) != .none
        }, inputEndConfirms: nil, timeoutMs: 100)
        return matched ? TouchGuard.receipt(value, token: token) : .none
    }
    func disarm() { if request >= 0 { notify_set_state(request, 0) } }
}

final class TypingShell: TypingDevice {
    private unowned let ops: Ops
    init(ops: Ops) { self.ops = ops }

    var readQueue: DispatchQueue { .main }

    func foregroundPID() -> Int32 { Apps.shared.foreground().pid }
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

    private let lock = NSLock()
    private var failureLogged = false
    func newOp() { lock.lock(); failureLogged = false; lock.unlock() }
    func field(id: String, pid: Int32) throws -> FieldGeometry { try ops.tree.field(id: id, pid: pid) }

    func tap(_ p: CGPoint, timeoutMs: Int) throws { try ops.input.tap(p.x, p.y, timeoutMs: timeoutMs) }
    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws { try ops.input.typeNow(text, cps: cps, timeoutMs: timeoutMs) }
    func selectAll(timeoutMs: Int) throws { try ops.input.selectAll(timeoutMs: timeoutMs) }
    func caretDown(timeoutMs: Int) throws { try ops.input.caretDown(timeoutMs: timeoutMs) }
    func selection() -> TextSelection? { ops.tree.selection() }
    func hideKeyboard(withinMs: Int) throws -> Bool {
        try ops.hideKeyboard(by: DispatchTime.now().uptimeNanoseconds + UInt64(max(withinMs, 0)) * 1_000_000)
    }
    func tapElement(id: String, pid: Int32, timeoutMs: Int) throws {
        let fg = Apps.shared.foreground()
        guard fg.pid == pid else { throw AgentError("process changed while refocusing \(id)") }
        let fields = Apps.shared.app(fg.bundle).descendants(matching: .any).matching(identifier: id)
        guard fields.count == 1 else { throw AgentError("cannot uniquely identify field \(id)") }
        let field = fields.element(boundBy: 0)
        guard [.textField, .secureTextField, .textView, .searchField].contains(field.elementType) else {
            throw AgentError("expected an editable field at \(id)")
        }
        do {
            try ops.input.gate.call(deadlineMs: max(timeoutMs, 1), on: { try $0() }) { field.tap() }
        } catch let e as InputNotSent {
            throw AgentError("tapping field \(id) did not start: \(e.why); no keys were sent", transient: true)
        }
    }

    var seq: Int64 { Events.shared.seq }
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool { Events.shared.awaitChange(since: since, timeoutMs: timeoutMs) }
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool { Events.shared.awaitCondition(timeoutMs: timeoutMs, check) }
    func log(_ s: String) { NSLog("%@", s) }
}

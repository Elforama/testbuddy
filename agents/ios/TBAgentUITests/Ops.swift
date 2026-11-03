// The ops of docs/protocol.md as iOS does them, plus the iOS-only ones and diagnostics.
import XCTest
import notify

final class Ops {
    let tree = Tree()
    lazy var input = Input(tree: tree)
    private(set) var exitRequested = false

    func hello() -> [UInt8] {
        let size = screenSize()
        let w = JW().obj().put("ev", "hello").put("platform", "ios").put("version", Agent.shared.build.isEmpty ? Agent.version : Agent.shared.build)
            .put("agent", Agent.version).put("build", Agent.shared.build).put("udid", Agent.shared.udid)
            .put("w", Int(size.width)).put("h", Int(size.height)).put("seq", Events.shared.seq).put("pid", Int(getpid()))
            .put("axHook", Events.shared.hookInstalled)
        return w.end().bytes
    }

    private func screenSize() -> CGSize {
        let f = tree.lastAppFrame
        if f.width > 0 && f.height > 0 { return f.size }
        return UIScreen.main.bounds.size
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
            if r.bool("waitQuiet", false) { _ = Events.shared.awaitQuiet(timeoutMs: r.int("timeout", 3000)) }
            let seq = Events.shared.seq
            let mode = r.has("mode") ? r.str("mode") : nil
            try onMain { try tree.write(w, mode: mode, seq: seq) }
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
            let found = Events.shared.awaitCondition(timeoutMs: r.int("timeout", 10000)) {
                Apps.shared.invalidate()
                return Apps.shared.foreground().bundle == pkg
            }
            w.put("found", found)
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

    // MARK: actions

    /// Runs an input action, then (unless settle=false) waits for the app to react: it changed
    /// and went quiet, or nothing changed within noChange ms and the app said it was idle.
    private func action(_ r: Request, _ w: JW) throws {
        let since = Events.shared.seq
        let sinceSolid = Events.shared.solid
        let settle = r.bool("settle", true)
        // An app without the probe announces only structural changes (docs/research/ios-events.md):
        // for it, one read after the action tells whether it changed in place.
        let fgPid = Apps.shared.foreground().pid
        let probed = Events.shared.isProbed(fgPid)
        var before: Int?
        if settle && !probed {
            if let s = tree.lastSignature, s.seq == since { before = s.hash } else { before = try? onMain { try tree.signature() } }
        }
        let t0 = DispatchTime.now().uptimeNanoseconds
        switch r.op {
        case "tap": try input.tap(try r.need("x"), try r.need("y"), holdMs: r.double("hold", Input.tapHoldMs))
        case "doubleTap": try input.taps(try r.need("x"), try r.need("y"), count: 2)
        case "press": try input.tap(try r.need("x"), try r.need("y"), holdMs: r.double("ms", 3000))
        case "swipe":
            if r.bool("drag", false) {
                try input.drag(try r.need("x1"), try r.need("y1"), try r.need("x2"), try r.need("y2"))
            } else {
                try input.swipe(try r.need("x1"), try r.need("y1"), try r.need("x2"), try r.need("y2"), ms: r.double("ms", 400))
            }
        case "key": try input.key(r.int("code", 0), long: r.bool("long", false))
        case "type":
            try input.type(try r.needStr("text"))
        case "erase": try input.erase(r.int("n", 50))
        case "clear": w.put("cleared", try input.clear())
        case "hideKeyboard": w.put("hidden", try hideKeyboard())
        default: break
        }
        let t1 = DispatchTime.now().uptimeNanoseconds
        w.put("injectMs", Int((t1 - t0) / 1_000_000))
        if settle {
            let timeout = r.int("timeout", 5000)
            let noChange = r.int("noChange", 250)
            let token = Events.shared.requestIdleToken(capMs: min(timeout, Events.shared.animationCapMs))
            var res = Events.shared.awaitSettled(since: sinceSolid, noChangeMs: noChange, timeoutMs: timeout, idleToken: token, reactOnly: r.bool("react", false))
            if res == .noChange, let before, let after = try? onMain({ try tree.signature() }), after != before {
                // It changed without saying so: count it, and let it settle.
                Events.shared.change(structural: false, pid: fgPid)
                let left = max(0, timeout - Int((DispatchTime.now().uptimeNanoseconds - t1) / 1_000_000))
                res = Events.shared.awaitSettled(since: sinceSolid, noChangeMs: 0, timeoutMs: left, idleToken: nil)
                w.put("seenBy", "read")
            }
            w.put("result", res.rawValue).put("settleMs", Int((DispatchTime.now().uptimeNanoseconds - t1) / 1_000_000))
        }
        w.put("probed", probed).put("seq", Events.shared.seq)
    }

    private func keyboardShown() -> Bool {
        onMain {
            let fg = Apps.shared.foreground()
            guard !fg.bundle.isEmpty else { return false }
            return Apps.shared.app(fg.bundle).keyboards.firstMatch.exists
        }
    }

    /// Hides the keyboard. In an app with the probe: its first responder resigns (the probe
    /// does it on request: a Darwin notification), which works on any screen. Otherwise as
    /// the classic way: a short drag in the middle of the screen (scroll views dismiss on drag),
    /// then sideways. Each try is followed by an event-driven settle.
    private func hideKeyboard() throws -> Bool {
        if !keyboardShown() { return true }
        let fg = Apps.shared.foreground()
        var tries: [() throws -> Void] = []
        if Events.shared.isProbed(fg.pid) {
            tries.append { notify_post("dev.testbuddy.probe.resign.\(fg.pid)") }
        }
        let size = screenSize()
        let cx = Double(size.width) / 2, cy = Double(size.height) / 2
        tries.append { try self.input.swipe(cx, cy, cx, cy - Double(size.height) * 0.03, ms: 50) }
        tries.append { try self.input.swipe(cx, cy, cx - Double(size.width) * 0.1, cy, ms: 50) }
        for attempt in tries {
            let since = Events.shared.solid
            try attempt()
            let token = Events.shared.requestIdleToken(capMs: min(1500, Events.shared.animationCapMs))
            _ = Events.shared.awaitSettled(since: since, noChangeMs: 300, timeoutMs: 3000, idleToken: token)
            if !keyboardShown() { return true }
        }
        throw AgentError("could not hide the keyboard")
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

import XCTest

struct XCTPaths: PathBuilder {
    func touch(at p: CGPoint, offset: TimeInterval) throws -> NSObject { try Private.touchPath(at: p, offset: offset) }
    func move(_ path: NSObject, to p: CGPoint, at offset: TimeInterval) throws { try Private.move(path, to: p, at: offset) }
    func lift(_ path: NSObject, at offset: TimeInterval) throws { try Private.lift(path, at: offset) }
    func explicitDrag(_ moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)], liftAt: TimeInterval) throws -> NSObject? {
        try Private.explicitDrag(moves, liftAt: liftAt)
    }
}

final class Input {
    static let tapHoldMs = Gestures.tapHoldMs
    static let typeRate = Typist.typeRate

    let tree: Tree
    private let paths = XCTPaths()
    init(tree: Tree) { self.tree = tree }

    private func hardware(_ p: CGPoint) -> CGPoint {
        guard tree.landscape else { return p }
        let o = onMain { XCUIDevice.shared.orientation }
        let b = UIScreen.main.nativeBounds, scale = UIScreen.main.nativeScale
        let w = b.width / scale, h = b.height / scale
        if o == .landscapeRight { return CGPoint(x: p.y, y: h - p.x) }
        return CGPoint(x: w - p.y, y: p.x)
    }

    let gate = InputGate(wait: { done, ms in Events.shared.awaitInput(done, inputEndConfirms: nil, timeoutMs: ms) },
                         locked: { f in Events.shared.signal(f) },
                         wrap: { f in let gen = Ops.issues.current; return { try Ops.issues.within(gen, f) } })
    private final class Flag { var on = false }

    private(set) var lastImplicitlyConfirmed = false

    private(set) var lastRecord: (start: UInt64, liftAt: TimeInterval, implicitAt: TimeInterval?)?

    private func play(_ paths: [NSObject], timeout own: TimeInterval, deadlineMs: Int? = nil, early: Bool = false,
                      liftAt: TimeInterval = 0, system: Bool = false) throws {
        lastImplicitlyConfirmed = false
        lastRecord = nil
        try gate.send(deadlineMs: deadlineMs) { budget in
            guard let confirmMs = PendingInput.confirmMs(ownMs: Int(own * 1000), leftMs: budget.leftMs()) else {
                throw InputNotSent(why: "the op's time ran out waiting for the previous input")
            }
            try playNow(paths, timeout: Double(confirmMs) / 1000, early: early, liftAt: liftAt, system: system)
        }
    }

    private func playNow(_ paths: [NSObject], timeout: TimeInterval, early: Bool, liftAt: TimeInterval, system: Bool) throws {
        if system { Input.awaitSystem() }
        let rec = try Private.record(paths)
        let p = InputGate.Event()
        let start = DispatchTime.now().uptimeNanoseconds
        try Private.synthesize(rec) { err in
            Events.shared.signal { p.error = err; p.done = true }
        }
        let endedEarly = Flag()
        let confirms: ((UInt64) -> Bool)? = early ? { at in
            let yes = Gestures.confirmsEarly(endAt: (Double(at) - Double(start)) / 1e9, liftAt: liftAt)
            if yes { endedEarly.on = true }
            return yes
        } : nil
        let ok = Events.shared.awaitInput({ p.done }, inputEndConfirms: confirms, timeoutMs: Int(timeout * 1000))
        if !ok { throw AgentError(String(format: "the input was not confirmed within %.1f s", timeout)) }
        let confirmedAt = Double(DispatchTime.now().uptimeNanoseconds - start) / 1e9
        let implicit = Gestures.implicitlyConfirmed(early: early, endedEarly: endedEarly.on, confirmedAt: confirmedAt, liftAt: liftAt)
        if implicit {
            lastImplicitlyConfirmed = true
            NSLog("tb: input confirmed %.1f s after its lift with no touches completed from the app: its touches may not have reached it", confirmedAt - liftAt)
        }
        lastRecord = (start, liftAt, implicit ? confirmedAt : nil)
        let finished = Events.shared.signal { p.done }
        if !finished { gate.leave(p); return }
        if let e = p.error { throw AgentError("input failed: \(e.localizedDescription)") }
    }

    private func play(_ g: Gesture<NSObject>, timeoutMs: Int? = nil, ci: Bool = false) throws {
        try play(g.paths, timeout: g.timeout, deadlineMs: timeoutMs, early: g.early, liftAt: g.liftAt,
                 system: Gestures.waitsForSystem(g, ci: ci))
    }

    private static let system = SystemWait { done in try Events.shared.askSystemIdle(done) }

    private static func awaitSystem() {
        let t0 = DispatchTime.now().uptimeNanoseconds
        let r = system.wait(pid: Apps.shared.springboardPidIfKnown)
        let ms = Int((DispatchTime.now().uptimeNanoseconds - t0) / 1_000_000)
        let outcome: String
        switch r {
        case .answered(let asks): outcome = asks == 1 ? "answered" : "answered after \(asks) asks"
        case .late: outcome = "late"
        case .noAnswer: outcome = "no answer"
        case .notAsked: outcome = "not asked"
        }
        NSLog("tb: SpringBoard wait before the gesture: %d ms, %@", ms, outcome)
        Events.shared.traceSystemWait(ms: ms, outcome: outcome)
    }

    func tap(_ x: Double, _ y: Double, holdMs: Double = Input.tapHoldMs, timeoutMs: Int? = nil) throws {
        try play(Gestures.tap(paths, at: hardware(CGPoint(x: x, y: y)), holdMs: holdMs), timeoutMs: timeoutMs)
    }

    func taps(_ x: Double, _ y: Double, count: Int) throws {
        try play(Gestures.taps(paths, at: hardware(CGPoint(x: x, y: y)), count: count))
    }

    func swipe(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, ms: Double, rest: Bool = false, timeoutMs: Int? = nil,
               ci: Bool? = nil) throws {
        if let ci { hostCI = ci }
        try play(Gestures.swipe(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)), ms: ms, rest: rest),
                 timeoutMs: timeoutMs, ci: hostCI)
    }

    func holdDrag(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, holdMs: Double, ms: Double, ci: Bool? = nil) throws {
        if let ci { hostCI = ci }
        try play(Gestures.holdDrag(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)),
                                   holdS: holdMs / 1000, ms: ms),
                 ci: hostCI)
    }

    func drag(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, tailSpeed: Double, tailPt: Double, ci: Bool? = nil) throws {
        if let ci { hostCI = ci }
        try play(Gestures.drag(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)),
                               tailSpeed: tailSpeed, tailPt: tailPt),
                 ci: hostCI)
    }

    var hostCI = false

    func typeNow(_ text: String, cps: Int, timeoutMs: Int? = nil) throws {
        let path = try Private.textPath()
        try Private.type(path, text, at: 0, cps: cps)
        let own = 10 + Double(text.count) / Double(max(cps, 1))
        try play([path], timeout: own, deadlineMs: timeoutMs)
    }

    private func pressKeyboardKey(_ key: String, modifiers: UInt = 0, timeoutMs: Int? = nil) throws {
        let path = try Private.textPath()
        try Private.typeKey(path, key, modifiers: modifiers, at: 0)
        try play([path], timeout: 10, deadlineMs: timeoutMs)
    }

    func selectAll(timeoutMs: Int) throws {
        try pressKeyboardKey("a", modifiers: XCUIElement.KeyModifierFlags.command.rawValue, timeoutMs: timeoutMs)
    }

    func caretDown(timeoutMs: Int) throws {
        try pressKeyboardKey(XCUIKeyboardKey.downArrow.rawValue, timeoutMs: timeoutMs)
    }

    func key(_ code: Int, long: Bool, timeoutMs: Int? = nil,
             onMain main: (@escaping () throws -> Void) throws -> Void = { try onMain($0) }) throws {
        func button(_ f: @escaping () throws -> Void) throws { try gate.call(deadlineMs: timeoutMs, on: main, f) }
        switch code {
        case 66: try typeNow("\n", cps: Input.typeRate, timeoutMs: timeoutMs)
        case 67: try typeNow("\u{8}", cps: Input.typeRate, timeoutMs: timeoutMs)
        case 61: try pressKeyboardKey(XCUIKeyboardKey.tab.rawValue, timeoutMs: timeoutMs)
        case 3: try button { XCUIDevice.shared.press(.home) }
        case 26: try button { try Private.pressLock() }
        case 24: try button { try Private.pressButton(2) }
        case 25: try button { try Private.pressButton(3) }
        case 111: try pressKeyboardKey(XCUIKeyboardKey.escape.rawValue, timeoutMs: timeoutMs)
        case 4: throw AgentError("iOS has no back key")
        default: throw AgentError("key code \(code) is not supported on iOS")
        }
    }
}

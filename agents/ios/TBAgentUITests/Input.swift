// Input synthesized through testmanagerd (XCSynthesizedEventRecord), without XCUIElement's
// waits for the app to go idle before and after. Every call returns once testmanagerd has
// confirmed the app received the events (its completion, pushed, not polled).
import XCTest

/// XCTest's finger paths (Private.swift), for Gestures.swift.
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

    // MARK: coordinates

    /// Touches are given in the app's UI points and played in the hardware's portrait points.
    private func hardware(_ p: CGPoint) -> CGPoint {
        guard tree.landscape else { return p }
        let o = onMain { XCUIDevice.shared.orientation }
        let b = UIScreen.main.nativeBounds, scale = UIScreen.main.nativeScale
        let w = b.width / scale, h = b.height / scale
        if o == .landscapeRight { return CGPoint(x: p.y, y: h - p.x) }
        return CGPoint(x: w - p.y, y: p.x)
    }

    // MARK: playing

    /// The last record not yet confirmed by testmanagerd (see play).
    private var pending: Pending?
    private final class Pending { var done = false; var error: Error? }

    /// Plays a record. Returns once testmanagerd confirms the app received it (~200 ms after
    /// its last event) or, with early, once a process reports its touches completed after the
    /// record's lift at liftAt (XCTest's TouchEventsCompleted user-testing event, ~30 ms after
    /// the lift; one that comes before is another record's, Gestures.confirmsEarly): both
    /// pushed. A record not yet confirmed is waited for before the next one is played:
    /// overlapping records could mix their events. With system, the record then waits for
    /// SpringBoard to keep up (SystemWait, issue #137): it holds the app's gestures until it
    /// has ruled on the touch.
    private func play(_ paths: [NSObject], timeout: TimeInterval, early: Bool = false, liftAt: TimeInterval = 0,
                      system: Bool = false) throws {
        if let p = pending {
            _ = Events.shared.awaitInput({ p.done }, inputEndConfirms: nil, timeoutMs: 10_000)
            pending = nil
        }
        if system { Input.awaitSystem() }
        let rec = try Private.record(paths)
        let p = Pending()
        let start = DispatchTime.now().uptimeNanoseconds
        try Private.synthesize(rec) { err in
            Events.shared.signal { p.error = err; p.done = true }
        }
        let confirms: ((UInt64) -> Bool)? = early ? { at in
            Gestures.confirmsEarly(endAt: (Double(at) - Double(start)) / 1e9, liftAt: liftAt)
        } : nil
        let ok = Events.shared.awaitInput({ p.done }, inputEndConfirms: confirms, timeoutMs: Int(timeout * 1000))
        if !ok { throw AgentError(String(format: "the input was not confirmed within %.1f s", timeout)) }
        let finished = Events.shared.signal { p.done }
        if !finished { pending = p; return }
        if let e = p.error { throw AgentError("input failed: \(e.localizedDescription)") }
    }

    /// timeoutMs, when given, shortens the gesture's own confirmation timeout.
    private func play(_ g: Gesture<NSObject>, timeoutMs: Int? = nil) throws {
        try play(g.paths, timeout: timeoutMs.map { min(g.timeout, Double($0) / 1000) } ?? g.timeout, early: g.early,
                 liftAt: g.liftAt, system: g.waitsForSystem)
    }

    /// SpringBoard keeping up, for swipes and drags (one for the agent: it stops asking a
    /// SpringBoard that does not answer for a while).
    private static let system = SystemWait { done in try Events.shared.askSystemIdle(done) }

    /// Waits for SpringBoard (SystemWait), logging how long and how it ended (the simulator log
    /// and the trace show whether the wait happens, issue #137).
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

    /// count taps at a point as one gesture (one finger path per tap).
    func taps(_ x: Double, _ y: Double, count: Int) throws {
        try play(Gestures.taps(paths, at: hardware(CGPoint(x: x, y: y)), count: count))
    }

    /// The flow format's swipe (Gestures.swipe).
    func swipe(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, ms: Double, rest: Bool = false) throws {
        try play(Gestures.swipe(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)), ms: ms, rest: rest))
    }

    /// A drag that leaves the content where the finger lifts, with no fling (Gestures.drag).
    func drag(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, tailSpeed: Double) throws {
        try play(Gestures.drag(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)), tailSpeed: tailSpeed))
    }

    // MARK: keyboard

    /// Types text into whatever has the focus, at cps characters a second. timeoutMs, when
    /// given, shortens the wait for testmanagerd's confirmation.
    func typeNow(_ text: String, cps: Int, timeoutMs: Int? = nil) throws {
        let path = try Private.textPath()
        try Private.type(path, text, at: 0, cps: cps)
        let own = 10 + Double(text.count) / Double(max(cps, 1))
        try play([path], timeout: timeoutMs.map { min(own, Double($0) / 1000) } ?? own)
    }

    private func pressKeyboardKey(_ key: XCUIKeyboardKey) throws {
        let path = try Private.textPath()
        try Private.typeKey(path, key.rawValue, modifiers: 0, at: 0)
        try play([path], timeout: 10)
    }

    /// Select all, then delete: empties the focused field in one step.
    func clear() throws -> Bool {
        guard let value = focusedValue() else { return false }
        if value.isEmpty { return true }
        let path = try Private.textPath()
        try Private.typeKey(path, "a", modifiers: XCUIElement.KeyModifierFlags.command.rawValue, at: 0)
        try play([path], timeout: 10)
        try typeNow("\u{8}", cps: Input.typeRate)
        if let after = focusedValue(), !after.isEmpty, after.count >= value.count {
            // Select-all did not take (some fields refuse ⌘A): delete key by key.
            try typeNow(String(repeating: "\u{8}", count: after.count), cps: Input.typeRate)
        }
        return true
    }

    /// The value the focused text field shows ("" when it shows only its placeholder), nil
    /// when no field has keyboard focus. Main thread hop.
    func focusedValue() -> String? { focusedField()?.value }

    func focusedField() -> FocusedField? {
        onMain {
            let fg = Apps.shared.foreground()
            guard !fg.bundle.isEmpty else { return nil }
            let q = Apps.shared.app(fg.bundle).descendants(matching: .any).matching(NSPredicate(format: "hasKeyboardFocus == YES")).firstMatch
            guard q.exists, let s = try? q.snapshot() else { return nil }
            let v = s.value as? String ?? ""
            return FocusedField(id: s.identifier,
                                value: v == (s.placeholderValue ?? "\u{0}") ? "" : v,
                                reportedLength: v.count)
        }
    }

    /// Android key codes as iOS keys.
    func key(_ code: Int, long: Bool) throws {
        switch code {
        case 66: try typeNow("\n", cps: Input.typeRate)                 // ENTER: return
        case 67: try typeNow("\u{8}", cps: Input.typeRate)              // DEL: delete
        case 61: try pressKeyboardKey(.tab)                             // TAB: move focus
        case 3: onMain { XCUIDevice.shared.press(.home) }              // HOME
        case 26: try onMain { try Private.pressLock() }                 // POWER: lock
        case 24: try onMain { try Private.pressButton(2) }              // VOLUME_UP
        case 25: try onMain { try Private.pressButton(3) }              // VOLUME_DOWN
        case 111: try pressKeyboardKey(.escape)                         // ESCAPE
        case 4: throw AgentError("iOS has no back key")
        default: throw AgentError("key code \(code) is not supported on iOS")
        }
    }
}

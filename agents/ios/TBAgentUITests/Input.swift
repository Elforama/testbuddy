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
    /// timeout: the record's own confirmation timeout. deadlineMs: the time the op has left
    /// (issue #130), or nil. With one, the wait for the previous record ends at the deadline; a
    /// previous record still unconfirmed then is InputNotSent (nothing of this one went, and it
    /// stays pending, so an op asked again waits for it too). This record then waits for its own
    /// confirmation for its own timeout, cut to what is left (PendingInput.confirmMs). Without
    /// one, the wait for the previous record is up to 10 s, as before.
    private func play(_ paths: [NSObject], timeout own: TimeInterval, deadlineMs: Int? = nil, early: Bool = false,
                      liftAt: TimeInterval = 0, system: Bool = false) throws {
        let endBy = deadlineMs.map { DispatchTime.now().uptimeNanoseconds + UInt64(max($0, 0)) * 1_000_000 }
        let leftMs = { () -> Int? in
            guard let endBy else { return nil }
            let now = DispatchTime.now().uptimeNanoseconds
            return endBy > now ? Int((endBy - now) / 1_000_000) : 0
        }
        if let p = pending {
            let waitMs = PendingInput.waitMs(boundedBy: leftMs())
            let done = Events.shared.awaitInput({ p.done }, inputEndConfirms: nil, timeoutMs: waitMs)
            if !done && deadlineMs != nil {
                throw InputNotSent(why: String(format: "the previous input was still not confirmed after %.1f s", Double(waitMs) / 1000))
            }
            pending = nil
        }
        guard let confirmMs = PendingInput.confirmMs(ownMs: Int(own * 1000), leftMs: leftMs()) else {
            throw InputNotSent(why: "the op's time ran out waiting for the previous input")
        }
        let timeout = Double(confirmMs) / 1000
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

    /// timeoutMs, when given, shortens the gesture's own confirmation timeout. ci: the host's CI
    /// mode, in which a swipe or drag waits for SpringBoard first (Gestures.waitsForSystem).
    private func play(_ g: Gesture<NSObject>, timeoutMs: Int? = nil, ci: Bool = false) throws {
        try play(g.paths, timeout: g.timeout, deadlineMs: timeoutMs, early: g.early, liftAt: g.liftAt,
                 system: Gestures.waitsForSystem(g, ci: ci))
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

    /// The flow format's swipe (Gestures.swipe). timeoutMs, when given, shortens its
    /// confirmation timeout (an op's deadline).
    /// ci: the host's CI mode ("ci" on the swipe request), in which a swipe or drag waits for
    /// SpringBoard first (issue #137); nil (the agent's own swipes, hideKeyboard's): as the last
    /// swipe request said.
    func swipe(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, ms: Double, rest: Bool = false, timeoutMs: Int? = nil,
               ci: Bool? = nil) throws {
        if let ci { hostCI = ci }
        try play(Gestures.swipe(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)), ms: ms, rest: rest),
                 timeoutMs: timeoutMs, ci: hostCI)
    }

    /// A drag that leaves the content where the finger lifts, with no fling (Gestures.drag).
    func drag(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, tailSpeed: Double, ci: Bool? = nil) throws {
        if let ci { hostCI = ci }
        try play(Gestures.drag(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)), tailSpeed: tailSpeed),
                 ci: hostCI)
    }

    /// The host's CI mode, as the last swipe request said (swipe, drag).
    private var hostCI = false

    // MARK: keyboard

    /// Types text into whatever has the focus, at cps characters a second. timeoutMs, when
    /// given, shortens the wait for testmanagerd's confirmation (an op's deadline).
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

    /// ⌘A in the focused field (the clear op: Typist.clear decides, issue #130).
    func selectAll(timeoutMs: Int) throws {
        try pressKeyboardKey("a", modifiers: XCUIElement.KeyModifierFlags.command.rawValue, timeoutMs: timeoutMs)
    }

    /// Android key codes as iOS keys. timeoutMs, when given, shortens the wait for a
    /// keystroke's confirmation, and main runs a button press on the main thread (which an
    /// earlier call to a busy app can hold): both bounded by the op's deadline (issue #130).
    func key(_ code: Int, long: Bool, timeoutMs: Int? = nil,
             onMain main: (@escaping () throws -> Void) throws -> Void = { try onMain($0) }) throws {
        switch code {
        case 66: try typeNow("\n", cps: Input.typeRate, timeoutMs: timeoutMs)        // ENTER: return
        case 67: try typeNow("\u{8}", cps: Input.typeRate, timeoutMs: timeoutMs)     // DEL: delete
        case 61: try pressKeyboardKey(XCUIKeyboardKey.tab.rawValue, timeoutMs: timeoutMs)     // TAB: move focus
        case 3: try main { XCUIDevice.shared.press(.home) }                          // HOME
        case 26: try main { try Private.pressLock() }                                // POWER: lock
        case 24: try main { try Private.pressButton(2) }                             // VOLUME_UP
        case 25: try main { try Private.pressButton(3) }                             // VOLUME_DOWN
        case 111: try pressKeyboardKey(XCUIKeyboardKey.escape.rawValue, timeoutMs: timeoutMs) // ESCAPE
        case 4: throw AgentError("iOS has no back key")
        default: throw AgentError("key code \(code) is not supported on iOS")
        }
    }
}

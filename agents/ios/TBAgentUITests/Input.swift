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
    /// Typing: after the first character (the keyboard may drop keys while it reacts to it),
    /// the rest at this rate.
    static let typeRate = 100
    static let firstKeyRate = 30
    static let firstKeyWaitMs = 500

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
    /// its last event) or, with early, once the app itself reports its touches completed
    /// (XCTest's TouchEventsCompleted user-testing event, ~30 ms after the lift): both pushed.
    /// A record not yet confirmed is waited for before the next one is played: overlapping
    /// records could mix their events.
    private func play(_ paths: [NSObject], timeout: TimeInterval, early: Bool = false) throws {
        if let p = pending {
            _ = Events.shared.awaitInput({ p.done }, inputEndsAtLeast: nil, timeoutMs: 10_000)
            pending = nil
        }
        let rec = try Private.record(paths)
        let p = Pending()
        let ends = Events.shared.inputEnds
        try Private.synthesize(rec) { err in
            Events.shared.signal { p.error = err; p.done = true }
        }
        let ok = Events.shared.awaitInput({ p.done }, inputEndsAtLeast: early ? ends + 1 : nil, timeoutMs: Int(timeout * 1000))
        if !ok { throw AgentError("the input was not confirmed within \(Int(timeout)) s") }
        let finished = Events.shared.signal { p.done }
        if !finished { pending = p; return }
        if let e = p.error { throw AgentError("input failed: \(e.localizedDescription)") }
    }

    private func play(_ g: Gesture<NSObject>) throws {
        try play(g.paths, timeout: g.timeout, early: g.early)
    }

    func tap(_ x: Double, _ y: Double, holdMs: Double = Input.tapHoldMs) throws {
        try play(Gestures.tap(paths, at: hardware(CGPoint(x: x, y: y)), holdMs: holdMs))
    }

    /// count taps at a point as one gesture (one finger path per tap).
    func taps(_ x: Double, _ y: Double, count: Int) throws {
        try play(Gestures.taps(paths, at: hardware(CGPoint(x: x, y: y)), count: count))
    }

    /// The flow format's swipe (Gestures.swipe).
    func swipe(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, ms: Double) throws {
        try play(Gestures.swipe(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2)), ms: ms))
    }

    /// A drag that leaves the content where the finger lifts, with no fling (Gestures.drag).
    func drag(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double) throws {
        try play(Gestures.drag(paths, from: hardware(CGPoint(x: x1, y: y1)), to: hardware(CGPoint(x: x2, y: y2))))
    }

    // MARK: keyboard

    private func typeNow(_ text: String, cps: Int) throws {
        let path = try Private.textPath()
        try Private.type(path, text, at: 0, cps: cps)
        try play([path], timeout: 10 + Double(text.count) / Double(max(cps, 1)))
    }

    /// Types into the focused field: the first character alone, then, once the field shows it
    /// (or after firstKeyWaitMs), the rest: the keyboard may drop keys while it reacts to the
    /// first. The field is looked at on change events, not polled; a change elsewhere (a
    /// ticker) is no sign the keyboard is ready. With no field reporting keyboard focus (React
    /// Native groups fields so), everything goes at the first key's slow rate, as the classic typing does
    /// without asking.
    func type(_ text: String, initialValue: String? = nil) throws {
        guard let first = text.first else { return }
        guard let before = initialValue ?? focusedValue() ?? awaitFocus() else {
            try typeNow(text, cps: Input.firstKeyRate)
            return
        }
        var since = Events.shared.seq
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(Input.firstKeyWaitMs) * 1_000_000
        try typeNow(String(first), cps: Input.firstKeyRate)
        let rest = String(text.dropFirst())
        if rest.isEmpty { return }
        while DispatchTime.now().uptimeNanoseconds < deadline {
            let left = Int((deadline - DispatchTime.now().uptimeNanoseconds) / 1_000_000)
            if !Events.shared.awaitChange(since: since, timeoutMs: left) { break }
            since = Events.shared.seq
            if let now = focusedValue(), now != before { break }
        }
        // A one-time-code field legitimately advances to its next box after the first key.
        try typeNow(rest, cps: Input.typeRate)
    }

    /// How long typing waits for a field to take the focus when none has it and no keyboard
    /// shows: keys sent before the focus lands go nowhere (on a slow CI simulator a password
    /// field tapped just before typed nothing, "Secret length: 0").
    static let focusWaitMs = 2000

    /// The focused field's value once a field takes the focus, waiting on change events up to
    /// focusWaitMs; nil at once while a keyboard shows (a React Native field types with no field
    /// reporting focus), or when none takes it in time.
    private func awaitFocus() -> String? {
        if keyboardShown() { return nil }
        var since = Events.shared.seq
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(Input.focusWaitMs) * 1_000_000
        while DispatchTime.now().uptimeNanoseconds < deadline {
            let left = Int((deadline - DispatchTime.now().uptimeNanoseconds) / 1_000_000)
            if !Events.shared.awaitChange(since: since, timeoutMs: left) { break }
            since = Events.shared.seq
            if let v = focusedValue() { return v }
            if keyboardShown() { return nil }
        }
        let v = focusedValue()
        NSLog("tb: no field took the focus within \(Input.focusWaitMs) ms of typing\(v == nil ? "; typing anyway" : "")")
        return v
    }

    private func keyboardShown() -> Bool {
        onMain {
            let fg = Apps.shared.foreground()
            guard !fg.bundle.isEmpty else { return false }
            return Apps.shared.app(fg.bundle).keyboards.firstMatch.exists
        }
    }

    /// Up to n deletes, no more than the focused field holds.
    func erase(_ n: Int) throws {
        var count = max(n, 0)
        if let value = focusedValue() { count = min(count, value.count) }
        if count == 0 { return }
        try typeNow(String(repeating: "\u{8}", count: count), cps: Input.typeRate)
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

    func focusedField() -> (id: String, value: String)? {
        onMain {
            let fg = Apps.shared.foreground()
            guard !fg.bundle.isEmpty else { return nil }
            let q = Apps.shared.app(fg.bundle).descendants(matching: .any).matching(NSPredicate(format: "hasKeyboardFocus == YES")).firstMatch
            guard q.exists, let s = try? q.snapshot() else { return nil }
            let v = s.value as? String ?? ""
            return (s.identifier, v == (s.placeholderValue ?? "\u{0}") ? "" : v)
        }
    }

    /// Android key codes as iOS keys.
    func key(_ code: Int, long: Bool) throws {
        switch code {
        case 66: try typeNow("\n", cps: Input.typeRate)                 // ENTER: return
        case 67: try typeNow("\u{8}", cps: Input.typeRate)              // DEL: delete
        case 61: try typeNow("\t", cps: Input.typeRate)                 // TAB
        case 3: onMain { XCUIDevice.shared.press(.home) }              // HOME
        case 26: try onMain { try Private.pressLock() }                 // POWER: lock
        case 24: try onMain { try Private.pressButton(2) }              // VOLUME_UP
        case 25: try onMain { try Private.pressButton(3) }              // VOLUME_DOWN
        case 111:                                                       // ESCAPE
            let path = try Private.textPath()
            try Private.typeKey(path, XCUIKeyboardKey.escape.rawValue, modifiers: 0, at: 0)
            try play([path], timeout: 10)
        case 4: throw AgentError("iOS has no back key")
        default: throw AgentError("key code \(code) is not supported on iOS")
        }
    }
}

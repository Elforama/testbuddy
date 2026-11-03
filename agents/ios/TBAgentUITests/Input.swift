// Input synthesized through testmanagerd (XCSynthesizedEventRecord), without XCUIElement's
// waits for the app to go idle before and after. Every call returns once testmanagerd has
// confirmed the app received the events (its completion, pushed, not polled).
import XCTest

final class Input {
    /// A tap's hold: long enough for UIKit controls to see it span a frame (a UISwitch needs
    /// ~17 ms), short enough that a nested scroll view paging itself cannot cancel it.
    static let tapHoldMs = 18.0
    /// Repeated taps (doubleTap): the flow format's timing, which React Native and UIKit's
    /// double-tap recognizers both take as a double tap.
    static let repeatIntervalMs = 200.0
    static let repeatHoldMs = 60.0
    /// Typing: after the first character (the keyboard may drop keys while it reacts to it),
    /// the rest at this rate.
    static let typeRate = 100
    static let firstKeyRate = 30
    static let firstKeyWaitMs = 500
    /// A no-fling drag: a 12 pt kick in 40 ms, the bulk in 150 ms, a 40 pt tail at 200 pt/s.
    static let dragKickPt = 12.0, dragKickS = 0.04, dragFastS = 0.15
    static let dragTailPt = 40.0, dragTailPtPerS = 200.0

    let tree: Tree
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

    func tap(_ x: Double, _ y: Double, holdMs: Double = Input.tapHoldMs) throws {
        let path = try Private.touchPath(at: hardware(CGPoint(x: x, y: y)))
        try Private.lift(path, at: max(holdMs, 1) / 1000)
        try play([path], timeout: 10 + holdMs / 1000, early: true)
    }

    /// count taps at a point as one gesture (one finger path per tap).
    func taps(_ x: Double, _ y: Double, count: Int, intervalMs: Double = Input.repeatIntervalMs, holdMs: Double = Input.repeatHoldMs) throws {
        var paths: [NSObject] = []
        let p = hardware(CGPoint(x: x, y: y))
        for k in 0..<max(count, 1) {
            let offset = Double(k) * intervalMs / 1000
            let path = try Private.touchPath(at: p, offset: offset)
            try Private.lift(path, at: offset + holdMs / 1000)
            paths.append(path)
        }
        try play(paths, timeout: 10 + Double(count) * intervalMs / 1000)
    }

    /// The flow format's swipe: down at the start, a glide to the end over the duration, lift there
    /// (still moving, so content flings on as it would under a finger).
    func swipe(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double, ms: Double) throws {
        let path = try Private.touchPath(at: hardware(CGPoint(x: x1, y: y1)))
        let start = 0.05
        try Private.move(path, to: hardware(CGPoint(x: x2, y: y2)), at: start)
        let liftAt = start + max(ms, 50) / 1000
        try Private.lift(path, at: liftAt)
        try play([path], timeout: 10 + liftAt, early: true)
    }

    /// A drag that leaves the content where the finger lifts, with no fling (scrollUntilVisible).
    /// A short kick takes the pan past its slop (at full speed the content lags the finger), a
    /// fast stretch covers most of the way, and a slow tail ends it: a scroll view keeps the
    /// speed of the last moves, and the tail's is too low to coast on.
    func drag(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double) throws {
        let a = hardware(CGPoint(x: x1, y: y1)), b = hardware(CGPoint(x: x2, y: y2))
        let dx = Double(b.x - a.x), dy = Double(b.y - a.y)
        let len = (dx * dx + dy * dy).squareRoot()
        guard len >= 1 else { return try tap(x1, y1) }
        func point(_ d: Double) -> CGPoint { CGPoint(x: Double(a.x) + dx / len * d, y: Double(a.y) + dy / len * d) }
        let kick = min(Input.dragKickPt, len / 10), tail = min(Input.dragTailPt, len / 4)
        let fastEnd = Input.dragKickS + Input.dragFastS
        let liftAt = fastEnd + tail / Input.dragTailPtPerS
        if let path = try Private.explicitDrag([(a, 0, 0), (point(kick), 0, Input.dragKickS),
                                                (point(len - tail), Input.dragKickS, Input.dragFastS),
                                                (b, fastEnd, liftAt - fastEnd)], liftAt: liftAt) {
            try play([path], timeout: 10 + liftAt, early: true)
            return
        }
        // No explicit move events in this Xcode: moveToPoints, which this one plays as arriving
        // at each point at its offset (measured: the list then moved the finger's distance and
        // stopped at the lift).
        let path = try Private.touchPath(at: a)
        try Private.move(path, to: point(kick), at: Input.dragKickS)
        try Private.move(path, to: point(len - tail), at: fastEnd)
        try Private.move(path, to: b, at: liftAt)
        try Private.lift(path, at: liftAt)
        try play([path], timeout: 10 + liftAt, early: true)
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
    func type(_ text: String) throws {
        guard let first = text.first else { return }
        guard let before = focusedValue() else {
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
        try typeNow(rest, cps: Input.typeRate)
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
    func focusedValue() -> String? {
        onMain {
            let fg = Apps.shared.foreground()
            guard !fg.bundle.isEmpty else { return nil }
            let q = Apps.shared.app(fg.bundle).descendants(matching: .any).matching(NSPredicate(format: "hasKeyboardFocus == YES")).firstMatch
            guard q.exists, let s = try? q.snapshot() else { return nil }
            let v = s.value as? String ?? ""
            return v == (s.placeholderValue ?? "\u{0}") ? "" : v
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

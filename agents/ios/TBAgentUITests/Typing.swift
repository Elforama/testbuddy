// Typing into a field (the type and erase ops): which field has the focus, refocusing the
// field the host asked for, and the keys. The decisions are here, without XCTest; the agent's
// XCTest reads and its input are a TypingDevice (TypingShell in Ops.swift).
import Foundation

/// The field with keyboard focus.
struct FocusedField: Equatable {
    let id: String
    /// What it shows ("" when it shows only its placeholder).
    let value: String
    /// The length XCTest reports (the placeholder's, for an empty field).
    let reportedLength: Int
}

/// Where a field is, in the app's points, read in one snapshot with the screen and the keyboard.
struct FieldGeometry: Equatable {
    let frame: CGRect
    let screen: CGRect
    /// The keyboard's frame when one shows.
    let keyboard: CGRect?
}

/// When a type or erase op answers (uptime ns): before the host's reply limit.
struct TypingDeadline {
    /// Kept between the answer and the host's reply limit.
    static let marginMs = 2000

    /// The host's reply limit for a request that does not carry one (internal/driver/ios:
    /// the op's timeout, perCharMs a character, and 15 s).
    static func hostReplyMs(timeoutMs: Int, perCharMs: Int, chars: Int) -> Int {
        timeoutMs + perCharMs * chars + 15_000
    }

    /// The time kept for the keys themselves: the first, the wait for the field to show it,
    /// the rest, and testmanagerd's confirmations.
    static func keysMs(chars: Int) -> Int { 2000 + 40 * chars }

    let start: UInt64
    let answerBy: UInt64
    let keysMs: Int

    /// The last moment to start sending keys.
    var keysBy: UInt64 {
        let keep = UInt64(keysMs) * 1_000_000
        return answerBy > start + keep ? answerBy - keep : start
    }

    static func forRequest(start: UInt64, replyMs: Int, chars: Int) -> TypingDeadline {
        let answerMs = max(replyMs - marginMs, replyMs / 2)
        return TypingDeadline(start: start, answerBy: start + UInt64(max(answerMs, 0)) * 1_000_000, keysMs: keysMs(chars: chars))
    }
}

/// Runs work on a queue (XCTest's reads on the main thread) and waits for it up to a deadline.
enum Bounded {
    struct TimedOut: Error {
        /// Whether the work had started (it then finishes on its queue, its result dropped).
        var started = false
    }

    private final class Box<T> {
        let lock = NSLock()
        let done = DispatchSemaphore(value: 0)
        var abandoned = false
        var started = false
        var startedAt: UInt64 = 0
        var heldID: Int?
        var result: Result<T, Error>?
    }

    /// f's result, or TimedOut at the deadline (uptime ns). Work that has not started by then
    /// never runs; work that has started finishes on its queue and its result is dropped. Such
    /// work is in held until it ends (issue #130): by default HeldMain.shared for the main
    /// queue, which the runner's vitals report.
    static func run<T>(on queue: DispatchQueue, by deadline: UInt64, held: HeldMain? = nil, _ f: @escaping () throws -> T) throws -> T {
        if queue === DispatchQueue.main && Thread.isMainThread { return try f() }
        let held = held ?? (queue === DispatchQueue.main ? HeldMain.shared : nil)
        let box = Box<T>()
        queue.async {
            box.lock.lock()
            if box.abandoned { box.lock.unlock(); return }
            box.started = true
            box.startedAt = held?.clock() ?? 0
            box.lock.unlock()
            let r = Result { try f() }
            box.lock.lock()
            box.result = r
            if let id = box.heldID { held?.released(id) }
            box.lock.unlock()
            box.done.signal()
        }
        _ = box.done.wait(timeout: DispatchTime(uptimeNanoseconds: deadline))
        box.lock.lock()
        defer { box.lock.unlock() }
        guard let r = box.result else {
            box.abandoned = true
            if box.started, let held { box.heldID = held.held(since: box.startedAt) }
            throw TimedOut(started: box.started)
        }
        return try r.get()
    }
}

/// How typing gives a field the focus back, from where the field is now.
enum Refocus: Equatable {
    /// A touch at this point (the middle of the field's part on screen).
    case touch(CGPoint)
    /// Hide the keyboard first, then look again.
    case hideKeyboardFirst
    /// Not touching it, and why.
    case refuse(String)

    static func plan(_ id: String, _ g: FieldGeometry) -> Refocus {
        let visible = g.frame.intersection(g.screen)
        guard !visible.isNull, visible.width >= 1, visible.height >= 1 else {
            return .refuse("field \(id) is off screen (at \(Int(g.frame.minX)),\(Int(g.frame.minY)), \(Int(g.frame.width))x\(Int(g.frame.height))): not touching it")
        }
        // Another field's keyboard: it can rise over this one between the read and the touch,
        // and a touch on a key would type it, so it goes first.
        if g.keyboard != nil { return .hideKeyboardFirst }
        return .touch(CGPoint(x: visible.midX, y: visible.midY))
    }
}

protocol TypingDevice: AnyObject {
    /// The queue XCTest's reads run on (the main thread). A read there waits for the app to
    /// answer accessibility, which an app whose main thread is busy may not do for many seconds:
    /// typing gives up on every read it makes there at its deadline.
    var readQueue: DispatchQueue { get }
    // Reads, run on readQueue.
    func foregroundPID() -> Int32
    func focusedField() -> FocusedField?
    func keyboardShown() -> Bool
    /// The editable field with this identifier (the only one) in pid's app; throws if there is
    /// none, several, or it is not editable.
    func field(id: String, pid: Int32) throws -> FieldGeometry
    // Input: synthesized through testmanagerd (the tap op's path, no wait for the app to go
    // idle), off readQueue; each returns once testmanagerd confirmed it, or throws after timeoutMs.
    func tap(_ p: CGPoint, timeoutMs: Int) throws
    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws
    /// ⌘A in the focused field, confirmed like the keys.
    func selectAll(timeoutMs: Int) throws
    /// Hides the keyboard within ms; throws Bounded.TimedOut if the app did not answer a read.
    func hideKeyboard(withinMs: Int) throws -> Bool
    /// XCUIElement.tap() on the editable field with this identifier (the only one) in pid's app,
    /// run on readQueue: XCTest first waits for the app to go idle. How typing gives a field the
    /// focus back outside the host's CI mode.
    func tapElement(id: String, pid: Int32) throws
    // Events.
    var seq: Int64 { get }
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool
    func log(_ s: String)
}

final class Typist {
    /// Typing: after the first character (the keyboard may drop keys while it reacts to it),
    /// the rest at this rate.
    static let typeRate = 100
    static let firstKeyRate = 30
    static let firstKeyWaitMs = 500
    /// How long typing waits for a field to take the focus when none has it and no keyboard
    /// shows: keys sent before the focus lands go nowhere (on a slow CI simulator a password
    /// field tapped just before typed nothing, "Secret length: 0").
    static let focusWaitMs = 2000

    let device: TypingDevice
    init(device: TypingDevice) { self.device = device }

    private static func now() -> UInt64 { DispatchTime.now().uptimeNanoseconds }
    private static func ms(until t: UInt64) -> Int { t > now() ? Int((t - now()) / 1_000_000) : 0 }

    /// The error of an op that gave up on the app before sending any key (issue #46): it says
    /// what it waited for, and the host may ask again.
    static func notAnswering(_ verb: String, since start: UInt64, _ what: String, touched: Bool = false) -> AgentError {
        AppWait.notAnswering(verb, since: start, what, touched ? "no keys were sent (the field was touched to focus it)" : "no keys were sent")
    }

    /// What a read given up on waited for: the read itself, or the main thread before it.
    static func waitedFor(_ what: String, _ t: Bounded.TimedOut) -> String {
        t.started ? what : "\(what), behind an earlier call to the app holding the main thread"
    }

    /// One type or erase op: its deadline and the characters confirmed so far.
    private final class Run {
        let deadline: TypingDeadline
        let verb: String
        var chars: Int
        var confirmed = 0
        /// Input other than the keys went (clear's select-all).
        var sentOther = false
        /// The field was touched to give it the focus.
        var touched = false
        init(_ deadline: TypingDeadline, _ verb: String, _ chars: Int) { self.deadline = deadline; self.verb = verb; self.chars = chars }
        var noKeys: String { touched ? "no keys were sent (the field was touched to focus it)" : "no keys were sent" }
    }

    /// A read before the keys: given up on at the last moment to start them.
    private func read<T>(_ run: Run, _ what: String, _ f: @escaping () throws -> T) throws -> T {
        do { return try Bounded.run(on: device.readQueue, by: run.deadline.keysBy, f) } catch let t as Bounded.TimedOut {
            throw Typist.notAnswering(run.verb, since: run.deadline.start, Typist.waitedFor(what, t), touched: run.touched)
        }
    }

    /// Sends keys. Keys that went but were not confirmed are not sent again (they may still
    /// arrive): the error says how many, and is not one to ask again on.
    private func keys(_ run: Run, _ text: String, cps: Int) throws {
        do {
            try device.typeKeys(text, cps: cps, timeoutMs: max(Typist.ms(until: run.deadline.answerBy), 1000))
        } catch let e as InputNotSent {
            // None of these keys went (issue #130): before any key, as safe to ask again as a
            // read the app did not answer.
            if run.confirmed == 0 && !run.sentOther {
                throw AgentError("\(run.verb) did not start: \(e.why); \(run.noKeys)", transient: true)
            }
            throw AgentError("\(run.verb) \(run.chars) characters: \(run.confirmed) of \(run.chars) characters confirmed, the next \(text.count) not sent (\(e.why))")
        } catch {
            throw AgentError("\(run.verb) \(run.chars) characters: \(run.confirmed) of \(run.chars) characters confirmed, the next \(text.count) not (\(error)): they may still arrive, so they are not sent again")
        }
        run.confirmed += text.count
    }

    /// Types into the focused field (the target's, when the host named one): the first
    /// character alone, then, once the field shows it (or after firstKeyWaitMs), the rest: the
    /// keyboard may drop keys while it reacts to the first. The field is looked at on change
    /// events, not polled; a change elsewhere (a ticker) is no sign the keyboard is ready. With
    /// no field reporting keyboard focus (React Native groups fields so), everything goes at
    /// the first key's slow rate, as the classic typing does without asking.
    /// Every read waits for the app until the deadline at most (issue #46). ci: the host's CI
    /// mode, which decides how the target gets the focus back (ensureFocus).
    func type(_ text: String, target: (id: String, pid: Int32)? = nil, by deadline: TypingDeadline, ci: Bool = false) throws {
        let run = Run(deadline, "typing", text.count)
        var before: String?
        if let target { before = try ensureFocus(run, target.id, pid: target.pid, ci: ci).value }
        guard let first = text.first else { return }
        if before == nil {
            before = try read(run, "checking which field has keyboard focus") { self.device.focusedField()?.value }
            if before == nil { before = try awaitFocus(run) }
        }
        guard let before else {
            try keys(run, text, cps: Typist.firstKeyRate)
            return
        }
        var since = device.seq
        let firstKeyBy = min(Typist.now() + UInt64(Typist.firstKeyWaitMs) * 1_000_000, deadline.answerBy)
        try keys(run, String(first), cps: Typist.firstKeyRate)
        let rest = String(text.dropFirst())
        if rest.isEmpty { return }
        waiting: while Typist.now() < firstKeyBy {
            if !device.awaitChange(since: since, timeoutMs: Typist.ms(until: firstKeyBy)) { break }
            since = device.seq
            // A read the app does not answer in time ends the wait: the keys queue in order.
            let now: String?
            do { now = try Bounded.run(on: device.readQueue, by: firstKeyBy) { self.device.focusedField()?.value } } catch { break waiting }
            if let now, now != before { break }
        }
        // A one-time-code field legitimately advances to its next box after the first key.
        try keys(run, rest, cps: Typist.typeRate)
    }

    /// Up to n deletes, no more than the focused field holds.
    func erase(_ n: Int, target: (id: String, pid: Int32)? = nil, by deadline: TypingDeadline, ci: Bool = false) throws {
        if n <= 0 { return }
        let run = Run(deadline, "erasing", n)
        let field: FocusedField?
        if let target {
            field = try ensureFocus(run, target.id, pid: target.pid, ci: ci)
        } else {
            field = try read(run, "checking which field has keyboard focus") { self.device.focusedField() }
        }
        // XCTest also reports the placeholder as the value of an empty field. A real value can
        // equal that placeholder, so the reported length bounds the deletes.
        let count = min(n, field?.reportedLength ?? n)
        if count == 0 { return }
        run.chars = count
        try keys(run, String(repeating: "\u{8}", count: count), cps: Typist.typeRate)
    }

    /// Empties the focused field (the clear op): select all, then delete; key by key when
    /// select-all did not take (some fields refuse ⌘A). false when no field has keyboard focus.
    /// Every read waits for the app until the deadline at most, as typing's do (issue #130).
    func clear(by deadline: TypingDeadline) throws -> Bool {
        let run = Run(deadline, "clearing", 1)
        guard let value = try read(run, "checking which field has keyboard focus", { self.device.focusedField()?.value }) else { return false }
        if value.isEmpty { return true }
        do {
            try device.selectAll(timeoutMs: max(Typist.ms(until: deadline.answerBy), 1000))
        } catch let e as InputNotSent {
            throw AgentError("clearing did not start: \(e.why); no keys were sent", transient: true)
        } catch {
            throw AgentError("clearing: select-all was not confirmed (\(error)); no delete was sent")
        }
        run.sentOther = true
        try keys(run, "\u{8}", cps: Typist.typeRate)
        // Whether select-all took: a field that refused it lost one character.
        let after: String?
        do { after = try Bounded.run(on: device.readQueue, by: deadline.answerBy) { self.device.focusedField()?.value } } catch is Bounded.TimedOut {
            throw AppWait.notAnsweringAfter("clearing", since: deadline.start, "checking what the field holds",
                                            "select-all and a delete were sent")
        }
        if let after, !after.isEmpty {
            run.chars = 1 + after.count
            try keys(run, String(repeating: "\u{8}", count: after.count), cps: Typist.typeRate)
        }
        return true
    }

    /// The focused field's value once a field takes the focus, waiting on change events up to
    /// focusWaitMs; nil at once while a keyboard shows (a React Native field types with no field
    /// reporting focus), or when none takes it in time.
    private func awaitFocus(_ run: Run) throws -> String? {
        let focusedValue = { self.device.focusedField()?.value }
        let keyboard = { self.device.keyboardShown() }
        if try read(run, "checking whether the keyboard shows", keyboard) { return nil }
        var since = device.seq
        let until = min(Typist.now() + UInt64(Typist.focusWaitMs) * 1_000_000, run.deadline.keysBy)
        while Typist.now() < until {
            if !device.awaitChange(since: since, timeoutMs: Typist.ms(until: until)) { break }
            since = device.seq
            if let v = try read(run, "checking which field has keyboard focus", focusedValue) { return v }
            if try read(run, "checking whether the keyboard shows", keyboard) { return nil }
        }
        let v = try read(run, "checking which field has keyboard focus", focusedValue)
        device.log("tb: no field took the focus within \(Typist.focusWaitMs) ms of typing\(v == nil ? "; typing anyway" : "")")
        return v
    }

    /// Editing is sent to the first responder: the requested field gets the focus back before
    /// any key, deletes included. In the host's CI mode by a touch at its middle through the
    /// agent's own input, not XCUIElement.tap(), which first waits for the app to go idle (issue
    /// #46); elsewhere by XCUIElement.tap(), as before, bounded by the op's deadline.
    private func ensureFocus(_ run: Run, _ id: String, pid: Int32, ci: Bool) throws -> FocusedField {
        let device = self.device
        let focused = { () -> FocusedField? in
            guard device.foregroundPID() == pid, let f = device.focusedField(), f.id == id else { return nil }
            return f
        }
        if let current = try read(run, "checking which field has keyboard focus", focused) { return current }
        guard try read(run, "checking which app is in front", { device.foregroundPID() }) == pid else {
            throw AgentError("foreground process changed before input to \(id)")
        }
        if ci {
            try touchToFocus(run, id, pid: pid)
        } else {
            // A CI allowance (the host's CI mode, "ci" on the request): the touch above skips
            // XCTest's wait for an idle app, which a main thread busy on a slow runner held
            // (issue #46). Elsewhere the field is tapped as before: the keyboard that can rise
            // over it hidden first, then XCUIElement.tap(). Every wait stays bounded.
            if try read(run, "checking whether the keyboard shows", { device.keyboardShown() }) {
                do { _ = try device.hideKeyboard(withinMs: Typist.ms(until: run.deadline.keysBy)) } catch is Bounded.TimedOut {
                    throw Typist.notAnswering(run.verb, since: run.deadline.start, "hiding the keyboard before tapping field \(id)")
                }
            }
            try read(run, "tapping field \(id)") { try device.tapElement(id: id, pid: pid) }
            run.touched = true
        }
        var current: FocusedField?
        var late = false
        let took = device.awaitCondition(timeoutMs: min(Typist.focusWaitMs, Typist.ms(until: run.deadline.keysBy))) {
            do { current = try Bounded.run(on: device.readQueue, by: run.deadline.keysBy, focused) } catch { late = true; return true }
            return current != nil
        }
        if late { throw Typist.notAnswering(run.verb, since: run.deadline.start, "waiting for field \(id) to take the focus", touched: run.touched) }
        guard took, let current else {
            let shortly = min(Typist.now() + 500_000_000, run.deadline.answerBy)
            let observed = (try? Bounded.run(on: device.readQueue, by: shortly) { device.focusedField()?.id }) ?? nil
            throw AgentError("expected keyboard focus on \(id), observed \(observed ?? "none")")
        }
        return current
    }

    /// Gives field id the focus by a touch at the middle of its part on screen (issue #46).
    private func touchToFocus(_ run: Run, _ id: String, pid: Int32) throws {
        let device = self.device
        var plan = Refocus.plan(id, try read(run, "finding field \(id)") { try device.field(id: id, pid: pid) })
        if plan == .hideKeyboardFirst {
            do { _ = try device.hideKeyboard(withinMs: Typist.ms(until: run.deadline.keysBy)) } catch is Bounded.TimedOut {
                throw Typist.notAnswering(run.verb, since: run.deadline.start, "hiding the keyboard before touching field \(id)")
            }
            plan = Refocus.plan(id, try read(run, "finding field \(id)") { try device.field(id: id, pid: pid) })
        }
        switch plan {
        case .refuse(let why): throw AgentError(why)
        case .hideKeyboardFirst: throw AgentError("a keyboard still shows after hiding it: not touching field \(id)")
        case .touch(let p):
            do { try device.tap(p, timeoutMs: max(Typist.ms(until: run.deadline.keysBy), 500)) } catch {
                throw AgentError("touching field \(id) to focus it: \(error); no keys were sent", transient: true)
            }
            run.touched = true
        }
    }
}

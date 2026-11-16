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
    /// A secure text field: its value is bullets, not its text (erasing gives it no verdict).
    var secure = false
    /// Where it is, in the app's points (erasing's lines: CaretToEnd).
    var frame: CGRect = .zero
}

/// What one look at the app shows the typing (issue #51): the field with keyboard focus and
/// whether a keyboard shows, read together in one whole-app snapshot.
struct FocusState: Equatable {
    /// nil when no field has keyboard focus.
    let field: FocusedField?
    let keyboard: Bool
}

/// A node of a snapshot of the app, as the focus look reads it (Tree.focus wraps XCTest's).
protocol FocusNode {
    var isKeyboard: Bool { get }
    var hasKeyboardFocus: Bool { get }
    var identifier: String { get }
    var value: Any? { get }
    var placeholderValue: String? { get }
    /// A secure text field (Tree's: XCTest's element type).
    var isSecure: Bool { get }
    var frame: CGRect { get }
    var children: [Self] { get }
}

extension FocusNode {
    var isSecure: Bool { false }
    var frame: CGRect { .zero }
}

extension FocusState {
    /// The focus look (issue #51) over one snapshot of the app: the first of the app's
    /// descendants with keyboard focus, in a query's order (pre-order, as firstMatch; the app
    /// itself is not one of them), and whether a Keyboard element is among them. A keyboard's
    /// keys never hold keyboard focus: its subtree is not walked.
    static func look<N: FocusNode>(_ app: N) -> FocusState {
        var field: FocusedField?
        var keyboard = false
        var stack = Array(app.children.reversed())
        while let n = stack.popLast() {
            if n.isKeyboard {
                keyboard = true
                if field != nil { break }
                continue
            }
            if field == nil, n.hasKeyboardFocus {
                field = FocusedField.reading(id: n.identifier, value: n.value, placeholder: n.placeholderValue, secure: n.isSecure, frame: n.frame)
                if keyboard { break }
            }
            stack.append(contentsOf: n.children.reversed())
        }
        return FocusState(field: field, keyboard: keyboard)
    }
}

extension FocusedField {
    /// A field as XCTest reports it: its value "" when it shows only its placeholder (XCTest
    /// reports the placeholder as an empty field's value); reportedLength what XCTest reports.
    static func reading(id: String, value: Any?, placeholder: String?, secure: Bool = false, frame: CGRect = .zero) -> FocusedField {
        let v = value as? String ?? ""
        return FocusedField(id: id, value: v == (placeholder ?? "\u{0}") ? "" : v, reportedLength: v.count, secure: secure, frame: frame)
    }
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
    /// The focused field and whether a keyboard shows: one whole-app snapshot (issue #51; a
    /// query for each took two for the field, .exists then .snapshot(), and one for the keyboard).
    func focus() -> FocusState
    /// The editable field with this identifier (the only one) in pid's app; throws if there is
    /// none, several, or it is not editable.
    func field(id: String, pid: Int32) throws -> FieldGeometry
    // Input: synthesized through testmanagerd (the tap op's path, no wait for the app to go
    // idle), off readQueue; each returns once testmanagerd confirmed it, or throws after timeoutMs.
    func tap(_ p: CGPoint, timeoutMs: Int) throws
    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws
    /// The down arrow in the focused field (issue #301, CaretToEnd), its own record, confirmed
    /// like the keys.
    func caretDown(timeoutMs: Int) throws
    /// ⌘A in the focused field, confirmed like the keys.
    func selectAll(timeoutMs: Int) throws
    /// Hides the keyboard within ms; throws Bounded.TimedOut if the app did not answer a read.
    func hideKeyboard(withinMs: Int) throws -> Bool
    /// XCUIElement.tap() on the editable field with this identifier (the only one) in pid's app,
    /// run on readQueue: XCTest first waits for the app to go idle. How typing gives a field the
    /// focus back outside the host's CI mode. timeoutMs: what is left of the op's time for the
    /// wait for an earlier input (the input gate, issue #286).
    func tapElement(id: String, pid: Int32, timeoutMs: Int) throws
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
    /// firstKeyWaitMs, which a test lengthens so that its wait ends on its fake's events, not
    /// on the clock (issue #245).
    let firstKeyWaitMs: Int
    /// Erasing's cap (eraseCapMs), which a test shortens; nil: by the host's CI mode.
    let eraseCapMs: Int?
    init(device: TypingDevice, firstKeyWaitMs: Int = Typist.firstKeyWaitMs, eraseCapMs: Int? = nil) {
        self.device = device
        self.firstKeyWaitMs = firstKeyWaitMs
        self.eraseCapMs = eraseCapMs
    }

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
            let look = try read(run, "checking which field has keyboard focus") { self.device.focus() }
            before = look.field?.value
            // With a keyboard but no field reporting focus (React Native), it types as it is.
            if before == nil && !look.keyboard { before = try awaitFocus(run) }
        }
        guard let before else {
            try keys(run, text, cps: Typist.firstKeyRate)
            return
        }
        var since = device.seq
        let firstKeyBy = min(Typist.now() + UInt64(firstKeyWaitMs) * 1_000_000, deadline.answerBy)
        try keys(run, String(first), cps: Typist.firstKeyRate)
        let rest = String(text.dropFirst())
        if rest.isEmpty { return }
        waiting: while Typist.now() < firstKeyBy {
            if !device.awaitChange(since: since, timeoutMs: Typist.ms(until: firstKeyBy)) { break }
            since = device.seq
            // A read the app does not answer in time ends the wait: the keys queue in order.
            let now: String?
            do { now = try Bounded.run(on: device.readQueue, by: firstKeyBy) { self.device.focus().field?.value } } catch { break waiting }
            if let now, now != before { break }
        }
        // A one-time-code field legitimately advances to its next box after the first key.
        try keys(run, rest, cps: Typist.typeRate)
    }

    /// Erasing's cap (issue #301, as Android's in #289): how long the field's text must stay the
    /// same to be judged, and how long a repair DEL is waited for; 1 s in the host's CI mode.
    static let eraseCapMs = 300
    static let eraseCapCIMs = 1000
    /// How many caps a read-back waits for a still text before it gives no verdict (#297).
    static let eraseSettleCaps = 3
    /// How often the read-back looks when no change event comes.
    static let erasePollMs = 50

    /// eraseText (issue #301, the iOS half of #289): deletes n characters from the end of the
    /// focused field's text, no more than it holds. Its postcondition: the field holds its old
    /// text less its last n characters (empty when it held no more); a step that cannot
    /// establish that fails, never passes silently.
    ///
    /// - **The caret goes to the end first, always** (CaretToEnd: a down arrow a line the text
    ///   can take, one key a record). A tap leaves it where the finger landed, and a
    ///   DEL deletes before it: the tail was left ("BROWN FOX FAST") and the step passed.
    ///   XCTest reports no caret, so where it is is never trusted to skip that.
    /// - **Real DEL keys** then, as before, so the app sees key presses.
    /// - **Read back** (a field that reports its text: not a secure one, nor one reading as its
    ///   placeholder): until it is right, or it has stayed the same for a whole cap, up to
    ///   eraseSettleCaps caps. A field still showing the DELs (a lagging read, #297) is never
    ///   judged or repaired, and one still changing then gets no verdict.
    /// - **Only what DELs leave when they went wrong is judged** (Erasing.judged): the start of
    ///   the old text, or the old text with a run taken out before its end. Anything else (a
    ///   mask, a formatter, a prefix the app puts back) is the app's own: no verdict.
    /// - **Repaired once**, on a fresh read: the caret to the end again, then one DEL at a time,
    ///   each read afresh before and waited for after, until the field holds what it should.
    ///   XCTest has no way to set a field's text, and ⌘A is not taken (CaretToEnd): text taken
    ///   from before the end cannot be put back, and fails at once.
    /// - Still not right: the step fails, naming what the field holds and what was expected.
    ///
    /// Returns whether the field was put right after the DELs (the reply's erase: "repaired").
    /// Its cost for a field that takes the DELs: one down arrow a line, and one read.
    @discardableResult
    func erase(_ n: Int, target: (id: String, pid: Int32)? = nil, by deadline: TypingDeadline, ci: Bool = false) throws -> Bool {
        if n <= 0 { return false }
        let run = Run(deadline, "erasing", n)
        let field: FocusedField?
        if let target {
            field = try ensureFocus(run, target.id, pid: target.pid, ci: ci)
        } else {
            field = try read(run, "checking which field has keyboard focus") { self.device.focus().field }
        }
        // XCTest also reports the placeholder as the value of an empty field. A real value can
        // equal that placeholder, so the reported length bounds the deletes.
        let count = min(n, field?.reportedLength ?? n)
        if count == 0 { return false }
        run.chars = count
        // Judged: a field that reports its text (a secure field's value is bullets; one whose
        // value reads as its placeholder holds text we cannot see).
        let erasing = field.flatMap { f in f.secure || f.value.count != f.reportedLength ? nil : Erasing(old: f.value, count: count) }
        // As many down arrows as the text can take lines, as long as the time kept for the keys
        // allows (#301 review: a fixed cap of 40 refused a notes-style text of 100 lines).
        let lines = CaretToEnd.lines(for: field?.value, frame: field?.frame ?? .zero)
        try caretToEnd(run, lines: lines, by: run.deadline.keysBy, then: "nothing was deleted")
        // A down arrow can move the focus (an app's key command for it, a search field's
        // suggestions, a web view): the DELs would then edit another field, and the read-back
        // would give no verdict (#301 review). Nothing is deleted yet: failing is safe. No field
        // focused can be the keyboard laying out again after a down arrow: that alone is read
        // again, up to a cap. Fields with no identifier compare "" with "": a move between two
        // of them is not caught here (the read-back then judges whatever the field holds).
        if let f = field {
            let capMs = eraseCapMs ?? (ci ? Typist.eraseCapCIMs : Typist.eraseCapMs)
            let readFocus = { () throws -> FocusedField? in
                do { return try Bounded.run(on: self.device.readQueue, by: run.deadline.answerBy) { self.device.focus().field } } catch {
                    throw AppWait.notAnsweringAfter(run.verb, since: run.deadline.start, "checking the focus after the down arrows", "nothing was deleted")
                }
            }
            var now = try readFocus()
            let until = min(Typist.now() + UInt64(capMs) * 1_000_000, run.deadline.answerBy)
            while now == nil, Typist.now() < until {
                _ = device.awaitChange(since: device.seq, timeoutMs: max(min(Typist.erasePollMs, Typist.ms(until: until)), 1))
                now = try readFocus()
            }
            if now?.id != f.id {
                throw AgentError("\(run.verb): the focus moved to \(now.map { $0.id.isEmpty ? "a field with no id" : $0.id } ?? "none") from field \(f.id) with the down arrows that put its caret at the end; nothing was deleted")
            }
        }
        try keys(run, String(repeating: "\u{8}", count: count), cps: Typist.typeRate)
        guard let erasing, let id = field?.id else { return false }
        return try verifyErased(run, erasing, id: id, frame: field?.frame ?? .zero, capMs: eraseCapMs ?? (ci ? Typist.eraseCapCIMs : Typist.eraseCapMs))
    }

    /// Puts the caret at the end (CaretToEnd): lines down arrows, each its own record,
    /// confirmed before the next: the DELs after them come after the move. They stop at by
    /// (#301 review: the op answers before the host's reply limit), failing with then.
    private func caretToEnd(_ run: Run, lines: Int, by: UInt64, then: String) throws {
        for i in 0..<lines {
            let left = Typist.ms(until: by)
            if left <= 0 {
                throw AgentError("\(run.verb): out of time putting the caret at the end, after \(i) of \(lines) down arrows; \(then)")
            }
            do {
                try device.caretDown(timeoutMs: left)
            } catch let e as InputNotSent {
                if run.confirmed == 0 && !run.sentOther {
                    throw AgentError("\(run.verb) did not start: \(e.why); \(run.noKeys)", transient: true)
                }
                throw AgentError("\(run.verb): moving the caret to the end was not sent (\(e.why)); \(run.confirmed) DELs were confirmed")
            } catch {
                throw AgentError("\(run.verb): moving the caret to the end was not confirmed (\(error)); \(run.confirmed) DELs were confirmed")
            }
            run.sentOther = true
        }
    }

    /// The postcondition of erasing, and its one repair (see erase). Returns whether it repaired.
    private func verifyErased(_ run: Run, _ e: Erasing, id: String, frame: CGRect, capMs: Int) throws -> Bool {
        let device = self.device
        let answerBy = run.deadline.answerBy
        let capNs = UInt64(capMs) * 1_000_000
        // The field's text; nil once the focus left it, or the app did not answer in time.
        func now() -> String? {
            let f: FocusedField?
            do { f = try Bounded.run(on: device.readQueue, by: answerBy) { device.focus().field } } catch { return nil }
            guard let f, f.id == id else { return nil }
            return f.value
        }
        // Waits for a change event, or erasePollMs: the field is looked at either way.
        func pause(until end: UInt64) {
            _ = device.awaitChange(since: device.seq, timeoutMs: max(min(Typist.erasePollMs, Typist.ms(until: end)), 1))
        }
        // What the field holds once it is right, or the same for a whole cap (never judged,
        // nor repaired, on a read that may only be lagging, #297); still changing after
        // eraseSettleCaps caps, it is returned with stable false.
        func settled() -> (text: String?, stable: Bool) {
            var t = now()
            var since = Typist.now()
            let end = min(Typist.now() + UInt64(Typist.eraseSettleCaps) * capNs, answerBy)
            while true {
                guard let v = t else { return (nil, false) }
                if e.done(v) || Typist.now() - since >= capNs { return (v, true) }
                if Typist.now() >= end { return (v, false) }
                pause(until: end)
                let next = now()
                if next != v { t = next; since = Typist.now() }
            }
        }
        let (left, stable) = settled()
        guard let left else {
            device.log("tb: erasing: the focus left field \(id) after the DELs (or it did not answer): no verdict")
            return false
        }
        if e.done(left) { return false }
        // Still changing, or text that is not what failed DELs leave (a mask, a formatter): the
        // app's own, no verdict, nothing sent.
        guard stable, e.judged(left) else {
            device.log("tb: erasing: field \(id) holds \"\(left)\"\(stable ? "" : ", still changing"): the app's own, no verdict")
            return false
        }
        guard e.leftoverAtEnd(left) else {
            throw AgentError("erasing: the field holds \"\(left)\", expected \"\(e.expected)\" (the DELs took text from before the end: its caret could not be put at the end, and DELs cannot put that back)")
        }
        // Read afresh right before acting: a text that moved on since it was judged is not repaired.
        guard now() == left else { return false }
        device.log("tb: erasing: field \(id) holds \"\(left)\" after the DELs, expected \"\(e.expected)\": repairing once")
        try caretToEnd(run, lines: CaretToEnd.lines(for: left, frame: frame), by: answerBy, then: "the DELs were sent and the field holds \"\(left)\", expected \"\(e.expected)\"")
        var seen = left
        var took = false
        var budget = min(left.count - e.expected.count, e.count)
        while budget > 0, !e.done(seen), seen.count > e.expected.count, Typist.now() < answerBy {
            // One DEL, only into the text judged (read afresh), and shown within the cap: one not
            // shown may still be on its way, and the next could delete too much.
            guard now() == seen else { break }
            try keys(run, "\u{8}", cps: Typist.typeRate)
            budget -= 1
            let by = min(Typist.now() + capNs, answerBy)
            var shown: String?
            while shown == nil, Typist.now() < by {
                pause(until: by)
                guard let v = now() else { break }
                if v != seen { shown = v }
            }
            guard let v = shown else { break }
            took = true
            seen = v
        }
        let (end, endStable) = settled()
        if let end, e.done(end) { return true }
        // A repair DEL the field does not show, on the old text's start: the field keeps that end
        // itself (a mask that puts its separator back), but only when the DELs took something:
        // a field the DELs left whole had its caret at the start (#301 review). The field back at
        // the very text it held after a repair it took: the app writes it. Gone, or still
        // changing: no verdict either.
        guard let end, endStable, !(took && end == left), took || !e.startOfOld(left) || left == e.old else {
            device.log("tb: erasing: field \(id) holds \"\(end ?? "?")\" after one repair: the app's own, no verdict")
            return false
        }
        throw AgentError("erasing: the field holds \"\(end)\", expected \"\(e.expected)\"" +
            (took ? " (its caret was put at the end, the DELs sent, and the field put right once)"
                  : left == e.old ? " (the down arrows and the DELs were sent and nothing was deleted: its caret could not be put at the end)"
                  : " (its caret was put at the end and the DELs sent; a repair DEL showed no change: the caret could not be put at the end)"))
    }

    /// Empties the focused field (the clear op): select all, then delete; key by key when
    /// select-all did not take (some fields refuse ⌘A). false when no field has keyboard focus.
    /// Every read waits for the app until the deadline at most, as typing's do (issue #130).
    func clear(by deadline: TypingDeadline) throws -> Bool {
        let run = Run(deadline, "clearing", 1)
        guard let value = try read(run, "checking which field has keyboard focus", { self.device.focus().field?.value }) else { return false }
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
        do { after = try Bounded.run(on: device.readQueue, by: deadline.answerBy) { self.device.focus().field?.value } } catch is Bounded.TimedOut {
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
    /// focusWaitMs; nil once a keyboard shows with no field reporting focus (a React Native
    /// field types so), or when none takes it in time. Called when neither showed; one snapshot
    /// a look answers both (issue #51).
    private func awaitFocus(_ run: Run) throws -> String? {
        let look = { self.device.focus() }
        var since = device.seq
        let until = min(Typist.now() + UInt64(Typist.focusWaitMs) * 1_000_000, run.deadline.keysBy)
        while Typist.now() < until {
            if !device.awaitChange(since: since, timeoutMs: Typist.ms(until: until)) { break }
            since = device.seq
            let now = try read(run, "checking which field has keyboard focus", look)
            if let v = now.field?.value { return v }
            if now.keyboard { return nil }
        }
        let v = try read(run, "checking which field has keyboard focus", look).field?.value
        device.log("tb: no field took the focus within \(Typist.focusWaitMs) ms of typing\(v == nil ? "; typing anyway" : "")")
        return v
    }

    /// Editing is sent to the first responder: the requested field gets the focus back before
    /// any key, deletes included. In the host's CI mode by a touch at its middle through the
    /// agent's own input, not XCUIElement.tap(), which first waits for the app to go idle (issue
    /// #46); elsewhere by XCUIElement.tap(), as before, bounded by the op's deadline.
    private func ensureFocus(_ run: Run, _ id: String, pid: Int32, ci: Bool) throws -> FocusedField {
        let device = self.device
        // One snapshot a look (issue #51): the focused field, and the keyboard with it.
        let look = { () -> FocusState? in device.foregroundPID() == pid ? device.focus() : nil }
        let focused = { () -> FocusedField? in
            guard let f = look()?.field, f.id == id else { return nil }
            return f
        }
        guard let first = try read(run, "checking which field has keyboard focus", look) else {
            throw AgentError("foreground process changed before input to \(id)")
        }
        if let current = first.field, current.id == id { return current }
        if ci {
            try touchToFocus(run, id, pid: pid)
        } else {
            // A CI allowance (the host's CI mode, "ci" on the request): the touch above skips
            // XCTest's wait for an idle app, which a main thread busy on a slow runner held
            // (issue #46). Elsewhere the field is tapped as before: the keyboard that can rise
            // over it hidden first, then XCUIElement.tap(). Every wait stays bounded.
            if first.keyboard {
                do { _ = try device.hideKeyboard(withinMs: Typist.ms(until: run.deadline.keysBy)) } catch is Bounded.TimedOut {
                    throw Typist.notAnswering(run.verb, since: run.deadline.start, "hiding the keyboard before tapping field \(id)")
                }
            }
            try read(run, "tapping field \(id)") { try device.tapElement(id: id, pid: pid, timeoutMs: Typist.ms(until: run.deadline.keysBy)) }
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
            let observed = (try? Bounded.run(on: device.readQueue, by: shortly) { device.focus().field?.id }) ?? nil
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

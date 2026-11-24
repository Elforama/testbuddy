import Foundation

struct FocusedField: Equatable {
    let id: String
    let value: String
    let reportedLength: Int
    var secure = false
    var frame: CGRect = .zero
}

struct FocusState: Equatable {
    let field: FocusedField?
    let keyboard: Bool
}

protocol FocusNode {
    var isKeyboard: Bool { get }
    var hasKeyboardFocus: Bool { get }
    var identifier: String { get }
    var value: Any? { get }
    var placeholderValue: String? { get }
    var isSecure: Bool { get }
    var frame: CGRect { get }
    var children: [Self] { get }
}

extension FocusNode {
    var isSecure: Bool { false }
    var frame: CGRect { .zero }
}

extension FocusState {
    static func look<N: FocusNode>(_ app: N) -> FocusState { lookNode(app).state }

    static func lookNode<N: FocusNode>(_ app: N) -> (state: FocusState, node: N?) {
        var field: FocusedField?
        var node: N?
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
                node = n
                if keyboard { break }
            }
            stack.append(contentsOf: n.children.reversed())
        }
        return (FocusState(field: field, keyboard: keyboard), node)
    }
}

extension FocusedField {
    static func reading(id: String, value: Any?, placeholder: String?, secure: Bool = false, frame: CGRect = .zero) -> FocusedField {
        let v = value as? String ?? ""
        return FocusedField(id: id, value: v == (placeholder ?? "\u{0}") ? "" : v, reportedLength: v.count, secure: secure, frame: frame)
    }
}

struct FieldGeometry: Equatable {
    let frame: CGRect
    let screen: CGRect
    let keyboard: CGRect?
}

struct TypingDeadline {
    static let marginMs = 2000

    static func hostReplyMs(timeoutMs: Int, perCharMs: Int, chars: Int) -> Int {
        timeoutMs + perCharMs * chars + 15_000
    }

    static func keysMs(chars: Int) -> Int { 2000 + 40 * chars }

    let start: UInt64
    let answerBy: UInt64
    let keysMs: Int

    var keysBy: UInt64 {
        let keep = UInt64(keysMs) * 1_000_000
        return answerBy > start + keep ? answerBy - keep : start
    }

    static func forRequest(start: UInt64, replyMs: Int, chars: Int) -> TypingDeadline {
        let answerMs = max(replyMs - marginMs, replyMs / 2)
        return TypingDeadline(start: start, answerBy: start + UInt64(max(answerMs, 0)) * 1_000_000, keysMs: keysMs(chars: chars))
    }
}

protocol AgentClock: AnyObject {
    func now() -> UInt64
    func wait(_ s: DispatchSemaphore, until deadline: UInt64, on queue: DispatchQueue) -> Bool
}

extension AgentClock {
    func ms(until t: UInt64) -> Int { let n = now(); return t > n ? Int((t - n) / 1_000_000) : 0 }
}

final class UptimeClock: AgentClock {
    static let shared = UptimeClock()
    func now() -> UInt64 { DispatchTime.now().uptimeNanoseconds }
    func wait(_ s: DispatchSemaphore, until deadline: UInt64, on queue: DispatchQueue) -> Bool {
        s.wait(timeout: DispatchTime(uptimeNanoseconds: deadline)) == .success
    }
}

enum Bounded {
    struct TimedOut: Error {
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

    static func run<T>(on queue: DispatchQueue, by deadline: UInt64, held: HeldMain? = nil, clock: AgentClock = UptimeClock.shared,
                       _ f: @escaping () throws -> T) throws -> T {
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
        _ = clock.wait(box.done, until: deadline, on: queue)
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

enum Refocus: Equatable {
    case touch(CGPoint)
    case hideKeyboardFirst
    case refuse(String)

    static func plan(_ id: String, _ g: FieldGeometry) -> Refocus {
        let visible = g.frame.intersection(g.screen)
        guard !visible.isNull, visible.width >= 1, visible.height >= 1 else {
            return .refuse("field \(id) is off screen (at \(Int(g.frame.minX)),\(Int(g.frame.minY)), \(Int(g.frame.width))x\(Int(g.frame.height))): not touching it")
        }
        if g.keyboard != nil { return .hideKeyboardFirst }
        return .touch(CGPoint(x: visible.midX, y: visible.midY))
    }
}

protocol TypingDevice: AnyObject {
    var readQueue: DispatchQueue { get }
    func foregroundPID() -> Int32
    func focus() -> FocusState
    func field(id: String, pid: Int32) throws -> FieldGeometry
    func tap(_ p: CGPoint, timeoutMs: Int) throws
    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws
    func caretDown(timeoutMs: Int) throws
    func selection() -> TextSelection?
    func selectAll(timeoutMs: Int) throws
    func hideKeyboard(withinMs: Int) throws -> Bool
    func tapElement(id: String, pid: Int32, timeoutMs: Int) throws
    var seq: Int64 { get }
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool
    func log(_ s: String)
}

extension TypingDevice {
    func selection() -> TextSelection? { nil }
}

struct TextSelection: Equatable {
    let id: String
    let location: Int
    let length: Int
    let value: String?

    static func range(_ v: Any?) -> (location: Int, length: Int)? {
        if let v = v as? NSValue, String(cString: v.objCType).contains("NSRange") {
            let r = v.rangeValue
            return r.location == NSNotFound ? nil : (r.location, r.length)
        }
        guard let s = v as? String, s.hasPrefix("{"), s.hasSuffix("}") else { return nil }
        let parts = s.dropFirst().dropLast().split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }
        guard parts.count == 2, let l = Int(parts[0]), let n = Int(parts[1]), l >= 0, n >= 0 else { return nil }
        return (l, n)
    }
}

final class Typist {
    static let typeRate = 100
    static let firstKeyRate = 30
    static let firstKeyWaitMs = 500
    static let focusWaitMs = 2000

    let device: TypingDevice
    let firstKeyWaitMs: Int
    let eraseCapMs: Int?
    let clock: AgentClock
    init(device: TypingDevice, firstKeyWaitMs: Int = Typist.firstKeyWaitMs, eraseCapMs: Int? = nil,
         clock: AgentClock = UptimeClock.shared) {
        self.device = device
        self.firstKeyWaitMs = firstKeyWaitMs
        self.eraseCapMs = eraseCapMs
        self.clock = clock
    }

    private func msUntil(_ t: UInt64) -> Int { clock.ms(until: t) }
    private func now() -> UInt64 { clock.now() }
    private func bounded<T>(by: UInt64, _ f: @escaping () throws -> T) throws -> T {
        try Bounded.run(on: device.readQueue, by: by, clock: clock, f)
    }

    private func notAnswering(_ verb: String, since start: UInt64, _ what: String, touched: Bool = false) -> AgentError {
        AppWait.notAnswering(verb, since: start, what, touched ? "no keys were sent (the field was touched to focus it)" : "no keys were sent", now: now())
    }

    static func waitedFor(_ what: String, _ t: Bounded.TimedOut) -> String {
        t.started ? what : "\(what), behind an earlier call to the app holding the main thread"
    }

    private final class Run {
        let deadline: TypingDeadline
        let verb: String
        var chars: Int
        var confirmed = 0
        var sentOther = false
        var touched = false
        init(_ deadline: TypingDeadline, _ verb: String, _ chars: Int) { self.deadline = deadline; self.verb = verb; self.chars = chars }
        var noKeys: String { touched ? "no keys were sent (the field was touched to focus it)" : "no keys were sent" }
    }

    private func read<T>(_ run: Run, _ what: String, _ f: @escaping () throws -> T) throws -> T {
        do { return try bounded(by: run.deadline.keysBy, f) } catch let t as Bounded.TimedOut {
            throw notAnswering(run.verb, since: run.deadline.start, Typist.waitedFor(what, t), touched: run.touched)
        }
    }

    private func keys(_ run: Run, _ text: String, cps: Int) throws {
        do {
            try device.typeKeys(text, cps: cps, timeoutMs: max(msUntil(run.deadline.answerBy), 1000))
        } catch let e as InputNotSent {
            if run.confirmed == 0 && !run.sentOther {
                throw AgentError("\(run.verb) did not start: \(e.why); \(run.noKeys)", transient: true)
            }
            throw AgentError("\(run.verb) \(run.chars) characters: \(run.confirmed) of \(run.chars) characters confirmed, the next \(text.count) not sent (\(e.why))")
        } catch {
            throw AgentError("\(run.verb) \(run.chars) characters: \(run.confirmed) of \(run.chars) characters confirmed, the next \(text.count) not (\(error)): they may still arrive, so they are not sent again")
        }
        run.confirmed += text.count
    }

    func type(_ text: String, target: (id: String, pid: Int32)? = nil, by deadline: TypingDeadline, ci: Bool = false) throws {
        let run = Run(deadline, "typing", text.count)
        var before: String?
        if let target { before = try ensureFocus(run, target.id, pid: target.pid, ci: ci).value }
        guard let first = text.first else { return }
        if before == nil {
            let look = try read(run, "checking which field has keyboard focus") { self.device.focus() }
            before = look.field?.value
            if before == nil && !look.keyboard { before = try awaitFocus(run) }
        }
        guard let before else {
            try keys(run, text, cps: Typist.firstKeyRate)
            return
        }
        var since = device.seq
        let firstKeyBy = min(now() + UInt64(firstKeyWaitMs) * 1_000_000, deadline.answerBy)
        try keys(run, String(first), cps: Typist.firstKeyRate)
        let rest = String(text.dropFirst())
        if rest.isEmpty { return }
        waiting: while now() < firstKeyBy {
            if !device.awaitChange(since: since, timeoutMs: msUntil(firstKeyBy)) { break }
            since = device.seq
            let now: String?
            do { now = try bounded(by: firstKeyBy) { self.device.focus().field?.value } } catch { break waiting }
            if let now, now != before { break }
        }
        try keys(run, rest, cps: Typist.typeRate)
    }

    static let eraseCapMs = 300
    static let eraseCapCIMs = 1000
    static let eraseSettleCaps = 3
    static let erasePollMs = 50
    static let selectionReadMs = 300

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
        let count = min(n, field?.reportedLength ?? n)
        if count == 0 { return false }
        run.chars = count
        let erasing = field.flatMap { f in f.secure || f.value.count != f.reportedLength ? nil : Erasing(old: f.value, count: count) }
        let lines = CaretToEnd.lines(for: field?.value, frame: field?.frame ?? .zero)
        let selectionBy = min(run.deadline.keysBy, clock.now() + UInt64(Typist.selectionReadMs) * 1_000_000)
        let atEnd = field.map { f in
            !f.secure && CaretToEnd.atEnd((try? bounded(by: selectionBy) { self.device.selection() }) ?? nil, of: f)
        } ?? false
        if !atEnd { try caretToEnd(run, lines: lines, by: run.deadline.keysBy, then: "nothing was deleted") }
        if let f = field, !atEnd {
            let capMs = eraseCapMs ?? (ci ? Typist.eraseCapCIMs : Typist.eraseCapMs)
            let readFocus = { () throws -> FocusedField? in
                do { return try self.bounded(by: run.deadline.answerBy) { self.device.focus().field } } catch {
                    throw AppWait.notAnsweringAfter(run.verb, since: run.deadline.start, "checking the focus after the down arrows", "nothing was deleted", now: self.now())
                }
            }
            var now = try readFocus()
            let until = min(clock.now() + UInt64(capMs) * 1_000_000, run.deadline.answerBy)
            while now == nil, clock.now() < until {
                _ = device.awaitChange(since: device.seq, timeoutMs: max(min(Typist.erasePollMs, msUntil(until)), 1))
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

    private func caretToEnd(_ run: Run, lines: Int, by: UInt64, then: String) throws {
        for i in 0..<lines {
            let left = msUntil(by)
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

    private func verifyErased(_ run: Run, _ e: Erasing, id: String, frame: CGRect, capMs: Int) throws -> Bool {
        let device = self.device
        let answerBy = run.deadline.answerBy
        let capNs = UInt64(capMs) * 1_000_000
        func now() -> String? {
            let f: FocusedField?
            do { f = try bounded(by: answerBy) { device.focus().field } } catch { return nil }
            guard let f, f.id == id else { return nil }
            return f.value
        }
        func pause(until end: UInt64) {
            _ = device.awaitChange(since: device.seq, timeoutMs: max(min(Typist.erasePollMs, msUntil(end)), 1))
        }
        func settled() -> (text: String?, stable: Bool) {
            var t = now()
            var since = clock.now()
            let end = min(clock.now() + UInt64(Typist.eraseSettleCaps) * capNs, answerBy)
            while true {
                guard let v = t else { return (nil, false) }
                if e.done(v) || clock.now() - since >= capNs { return (v, true) }
                if clock.now() >= end { return (v, false) }
                pause(until: end)
                let next = now()
                if next != v { t = next; since = clock.now() }
            }
        }
        let (left, stable) = settled()
        guard let left else {
            device.log("tb: erasing: the focus left field \(id) after the DELs (or it did not answer): no verdict")
            return false
        }
        if e.done(left) { return false }
        guard stable, e.judged(left) else {
            device.log("tb: erasing: field \(id) holds \"\(left)\"\(stable ? "" : ", still changing"): the app's own, no verdict")
            return false
        }
        guard e.leftoverAtEnd(left) else {
            throw AgentError("erasing: the field holds \"\(left)\", expected \"\(e.expected)\" (the DELs took text from before the end: its caret could not be put at the end, and DELs cannot put that back)")
        }
        guard now() == left else { return false }
        device.log("tb: erasing: field \(id) holds \"\(left)\" after the DELs, expected \"\(e.expected)\": repairing once")
        try caretToEnd(run, lines: CaretToEnd.lines(for: left, frame: frame), by: answerBy, then: "the DELs were sent and the field holds \"\(left)\", expected \"\(e.expected)\"")
        var seen = left
        var took = false
        var budget = min(left.count - e.expected.count, e.count)
        while budget > 0, !e.done(seen), seen.count > e.expected.count, clock.now() < answerBy {
            guard now() == seen else { break }
            try keys(run, "\u{8}", cps: Typist.typeRate)
            budget -= 1
            let by = min(clock.now() + capNs, answerBy)
            var shown: String?
            while shown == nil, clock.now() < by {
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
        guard let end, endStable, !(took && end == left), took || !e.startOfOld(left) || left == e.old else {
            device.log("tb: erasing: field \(id) holds \"\(end ?? "?")\" after one repair: the app's own, no verdict")
            return false
        }
        throw AgentError("erasing: the field holds \"\(end)\", expected \"\(e.expected)\"" +
            (took ? " (its caret was put at the end, the DELs sent, and the field put right once)"
                  : left == e.old ? " (the down arrows and the DELs were sent and nothing was deleted: its caret could not be put at the end)"
                  : " (its caret was put at the end and the DELs sent; a repair DEL showed no change: the caret could not be put at the end)"))
    }

    func clear(by deadline: TypingDeadline) throws -> Bool {
        let run = Run(deadline, "clearing", 1)
        guard let value = try read(run, "checking which field has keyboard focus", { self.device.focus().field?.value }) else { return false }
        if value.isEmpty { return true }
        do {
            try device.selectAll(timeoutMs: max(msUntil(deadline.answerBy), 1000))
        } catch let e as InputNotSent {
            throw AgentError("clearing did not start: \(e.why); no keys were sent", transient: true)
        } catch {
            throw AgentError("clearing: select-all was not confirmed (\(error)); no delete was sent")
        }
        run.sentOther = true
        try keys(run, "\u{8}", cps: Typist.typeRate)
        let after: String?
        do { after = try bounded(by: deadline.answerBy) { self.device.focus().field?.value } } catch is Bounded.TimedOut {
            throw AppWait.notAnsweringAfter("clearing", since: deadline.start, "checking what the field holds",
                                            "select-all and a delete were sent", now: now())
        }
        if let after, !after.isEmpty {
            run.chars = 1 + after.count
            try keys(run, String(repeating: "\u{8}", count: after.count), cps: Typist.typeRate)
        }
        return true
    }

    private func awaitFocus(_ run: Run) throws -> String? {
        let look = { self.device.focus() }
        var since = device.seq
        let until = min(now() + UInt64(Typist.focusWaitMs) * 1_000_000, run.deadline.keysBy)
        while now() < until {
            if !device.awaitChange(since: since, timeoutMs: msUntil(until)) { break }
            since = device.seq
            let now = try read(run, "checking which field has keyboard focus", look)
            if let v = now.field?.value { return v }
            if now.keyboard { return nil }
        }
        let v = try read(run, "checking which field has keyboard focus", look).field?.value
        device.log("tb: no field took the focus within \(Typist.focusWaitMs) ms of typing\(v == nil ? "; typing anyway" : "")")
        return v
    }

    private func ensureFocus(_ run: Run, _ id: String, pid: Int32, ci: Bool) throws -> FocusedField {
        let device = self.device
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
            if first.keyboard {
                do { _ = try device.hideKeyboard(withinMs: msUntil(run.deadline.keysBy)) } catch is Bounded.TimedOut {
                    throw notAnswering(run.verb, since: run.deadline.start, "hiding the keyboard before tapping field \(id)")
                }
            }
            try read(run, "tapping field \(id)") { try device.tapElement(id: id, pid: pid, timeoutMs: self.msUntil(run.deadline.keysBy)) }
            run.touched = true
        }
        var current: FocusedField?
        var late = false
        let took = device.awaitCondition(timeoutMs: min(Typist.focusWaitMs, msUntil(run.deadline.keysBy))) {
            do { current = try bounded(by: run.deadline.keysBy, focused) } catch { late = true; return true }
            return current != nil
        }
        if late { throw notAnswering(run.verb, since: run.deadline.start, "waiting for field \(id) to take the focus", touched: run.touched) }
        guard took, let current else {
            let shortly = min(now() + 500_000_000, run.deadline.answerBy)
            let observed = (try? bounded(by: shortly) { device.focus().field?.id }) ?? nil
            throw AgentError("expected keyboard focus on \(id), observed \(observed ?? "none")")
        }
        return current
    }

    private func touchToFocus(_ run: Run, _ id: String, pid: Int32) throws {
        let device = self.device
        var plan = Refocus.plan(id, try read(run, "finding field \(id)") { try device.field(id: id, pid: pid) })
        if plan == .hideKeyboardFirst {
            do { _ = try device.hideKeyboard(withinMs: msUntil(run.deadline.keysBy)) } catch is Bounded.TimedOut {
                throw notAnswering(run.verb, since: run.deadline.start, "hiding the keyboard before touching field \(id)")
            }
            plan = Refocus.plan(id, try read(run, "finding field \(id)") { try device.field(id: id, pid: pid) })
        }
        switch plan {
        case .refuse(let why): throw AgentError(why)
        case .hideKeyboardFirst: throw AgentError("a keyboard still shows after hiding it: not touching field \(id)")
        case .touch(let p):
            do { try device.tap(p, timeoutMs: max(msUntil(run.deadline.keysBy), 500)) } catch {
                throw AgentError("touching field \(id) to focus it: \(error); no keys were sent", transient: true)
            }
            run.touched = true
        }
    }
}

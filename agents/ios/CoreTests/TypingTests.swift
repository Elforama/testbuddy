import XCTest
@testable import TBAgentCore

private final class FakeDevice: TypingDevice {
    let readQueue = DispatchQueue(label: "fake main thread")
    let clock: VirtualClock
    init() { clock = VirtualClock(holding: readQueue) }
    func typist(firstKeyWaitMs: Int = Typist.firstKeyWaitMs) -> Typist {
        Typist(device: self, firstKeyWaitMs: firstKeyWaitMs, clock: clock)
    }
    private let lock = NSLock()
    private var _focused: FocusedField?
    private var _keys: [String] = []
    private var _taps: [CGPoint] = []
    private var _elementTaps = 0
    private var _seq: Int64 = 0
    private var _keyboard = false
    private var _snapshots = 0
    var snapshots: Int { lock.lock(); defer { lock.unlock() }; return _snapshots }
    private func snapshot(_ n: Int = 1) { lock.lock(); _snapshots += n; lock.unlock() }
    var echoes = true
    func showKeyboard() { lock.lock(); _keyboard = true; _seq += 1; lock.unlock() }
    func tick() { lock.lock(); _seq += 1; lock.unlock() }
    var hung: Bool {
        get { lock.lock(); defer { lock.unlock() }; return _hang != nil }
        set {
            lock.lock()
            if newValue, _hang == nil { _hang = clock.blocker() }
            let released = newValue ? nil : _hang
            if !newValue { _hang = nil }
            lock.unlock()
            released?.release()
        }
    }
    private var _hang: VirtualClock.Hold?
    var pid: Int32 = 42
    var frames: [String: CGRect] = ["name": CGRect(x: 20, y: 226, width: 362, height: 34),
                                    "email": CGRect(x: 20, y: 300, width: 362, height: 34)]
    let screen = CGRect(x: 0, y: 0, width: 402, height: 874)
    let keyboardFrame = CGRect(x: 0, y: 520, width: 402, height: 354)
    var hideWorks = true
    var failKeysOnCall: Int?
    private var keyCalls = 0

    var keys: [String] { lock.lock(); defer { lock.unlock() }; return _keys }
    var taps: [CGPoint] { lock.lock(); defer { lock.unlock() }; return _taps }
    var elementTaps: Int { lock.lock(); defer { lock.unlock() }; return _elementTaps }

    func focus(_ id: String?, value: String = "") {
        lock.lock()
        _focused = id.map { FocusedField(id: $0, value: value, reportedLength: value.count) }
        _keyboard = id != nil
        _seq += 1
        lock.unlock()
    }

    private func appAnswers() {
        lock.lock(); let h = _hang; lock.unlock()
        h?.wait()
    }

    func foregroundPID() -> Int32 { appAnswers(); return pid }
    func focus() -> FocusState {
        appAnswers()
        snapshot()
        lock.lock(); defer { lock.unlock() }
        return FocusState(field: _focused, keyboard: _keyboard)
    }
    func field(id: String, pid: Int32) throws -> FieldGeometry {
        appAnswers()
        snapshot()
        guard let f = frames[id] else { throw AgentError("cannot uniquely identify field \(id)") }
        lock.lock(); defer { lock.unlock() }
        return FieldGeometry(frame: f, screen: screen, keyboard: _keyboard ? keyboardFrame : nil)
    }

    private var goesIdle = false
    private lazy var busy = clock.blocker()
    func goIdle() { lock.lock(); goesIdle = true; let b = busy; lock.unlock(); b.release() }

    func tapElement(id: String, pid: Int32, timeoutMs: Int) throws {
        lock.lock(); _elementTaps += 1; let b = goesIdle ? nil : busy; lock.unlock()
        b?.wait()
        guard frames[id] != nil else { throw AgentError("cannot uniquely identify field \(id)") }
        focus(id)
    }

    func tap(_ p: CGPoint, timeoutMs: Int) throws {
        lock.lock(); _taps.append(p); lock.unlock()
        if let (id, _) = frames.first(where: { $0.value.contains(p) }) { focus(id) }
    }

    var notSentOnCall: Int?
    var selectAllWorks = true
    var hangAfterKeys = false
    private var selected = false

    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws {
        lock.lock()
        keyCalls += 1
        if notSentOnCall == keyCalls {
            lock.unlock()
            throw InputNotSent(why: "the previous input was still not confirmed after \(timeoutMs / 1000) s")
        }
        _keys.append(text)
        let fail = failKeysOnCall == keyCalls
        if !fail, echoes, let f = _focused {
            var v = f.value
            var rest = Substring(text)
            if selected, rest.first == "\u{8}" { v = ""; rest = rest.dropFirst() }
            selected = false
            for c in rest { if c == "\u{8}" { if !v.isEmpty { v.removeLast() } } else { v.append(c) } }
            _focused = FocusedField(id: f.id, value: v, reportedLength: v.count)
            _seq += 1
        }
        if hangAfterKeys, _hang == nil { _hang = clock.blocker() }
        lock.unlock()
        if fail { throw AgentError("the input was not confirmed within \(timeoutMs / 1000) s") }
    }

    func caretDown(timeoutMs: Int) throws {
        lock.lock(); _keys.append("↓"); lock.unlock()
    }

    func selectAll(timeoutMs: Int) throws {
        lock.lock()
        _keys.append("⌘A")
        selected = selectAllWorks && _focused != nil
        lock.unlock()
    }

    var value: String? { lock.lock(); defer { lock.unlock() }; return _focused?.value }

    func hideKeyboard(withinMs: Int) throws -> Bool {
        guard hideWorks else { throw AgentError("could not hide the keyboard") }
        focus(nil)
        return true
    }

    private var _onWait: [() -> Void] = []
    private var _changesDelivered = 0
    var changesDelivered: Int { lock.lock(); defer { lock.unlock() }; return _changesDelivered }
    func onWait(_ changes: [() -> Void]) { lock.lock(); _onWait += changes; lock.unlock() }
    func onWait(ticks n: Int, then last: (() -> Void)? = nil) {
        onWait(Array(repeating: { [unowned self] in self.tick() }, count: n) + (last.map { [$0] } ?? []))
    }

    var seq: Int64 { lock.lock(); defer { lock.unlock() }; return _seq }
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool {
        if seq != since { return true }
        lock.lock()
        let change = _onWait.isEmpty ? nil : _onWait.removeFirst()
        if change != nil { _changesDelivered += 1 }
        lock.unlock()
        if let change { change(); return true }
        return clock.waitFor(ms: timeoutMs) { seq != since }
    }
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool {
        let end = clock.now() + UInt64(max(timeoutMs, 0)) * 1_000_000
        repeat { if check() { return true }; clock.advance(ms: 5) } while clock.now() < end
        return false
    }
    func log(_ s: String) {}
}

private struct NoError: Error {}
private func answer(_ f: @escaping () throws -> Void) -> Error? {
    let done = DispatchSemaphore(value: 0)
    var result: Error?
    DispatchQueue.global().async {
        do { try f(); result = NoError() } catch { result = error }
        done.signal()
    }
    return done.wait(timeout: .now() + VirtualClock.hangGuard) == .timedOut ? nil : result
}

final class TypingTests: XCTestCase {
    private func deadline(answerInMs: Int, keysMs: Int) -> TypingDeadline {
        let now = VirtualClock.start
        return TypingDeadline(start: now, answerBy: now + UInt64(answerInMs) * 1_000_000, keysMs: keysMs)
    }

    func testTypesIntoTheFocusedFieldInOrder() throws {
        let d = FakeDevice()
        d.focus("name")
        let t = d.typist()
        try t.type("hi", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080))
        XCTAssertEqual(d.keys, ["h", "i"])
        XCTAssertEqual(d.taps, [])
        XCTAssertEqual(d.clock.elapsedMs, 0, "an idle app is not kept waiting")
    }

    func testTypeAnswersByItsDeadlineWhenTheAppDoesNotAnswer() {
        let d = FakeDevice()
        d.focus("name")
        d.hung = true
        let t = d.typist()
        let err = answer { try t.type("hi", target: ("name", 42), by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        guard let err else { return XCTFail("type never answered (the app answered no accessibility request)") }
        XCTAssertFalse(err is NoError, "type succeeded although the app never answered")
        let e = err as? AgentError
        XCTAssertEqual(e?.transient, true, "no key was sent: the host may ask again (\(err))")
        XCTAssertTrue("\(err)".contains("keyboard focus"), "the error says what it waited for: \(err)")
        XCTAssertTrue("\(err)".contains("no keys were sent"), "\(err)")
        XCTAssertEqual(d.clock.elapsedMs, 1000, "answered at \(d.clock.elapsedMs) ms, not at its deadline")
        d.hung = false
        d.readQueue.sync {}
        XCTAssertEqual(d.keys, [], "keys sent after the op answered")
    }

    func testRefocusTouchesTheFieldWithoutWaitingForIdle() {
        let d = FakeDevice()
        d.focus("email")
        d.hideWorks = true
        let t = d.typist()
        let err = answer { try t.type("hi", target: ("name", 42), by: self.deadline(answerInMs: 18_000, keysMs: 2080), ci: true) }
        guard let err else { return XCTFail("type never answered") }
        XCTAssertTrue(err is NoError, "\(err)")
        XCTAssertEqual(d.elementTaps, 0, "refocused with XCUIElement.tap(), which waits for the app to go idle")
        XCTAssertEqual(d.taps, [CGPoint(x: 201, y: 243)])
        XCTAssertEqual(d.keys, ["h", "i"])
        XCTAssertEqual(d.clock.elapsedMs, 0, "kept waiting for an app that never goes idle")
    }

    func testOutsideCIRefocusUsesXCTestsTap() throws {
        let d = FakeDevice()
        d.goIdle()
        d.focus("email")
        try d.typist().type("hi", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080), ci: false)
        XCTAssertEqual(d.elementTaps, 1)
        XCTAssertEqual(d.taps, [], "refocused by a touch of the agent's own outside CI mode")
        XCTAssertEqual(d.keys, ["h", "i"])
    }

    func testOutsideCIRefocusAnswersByItsDeadline() {
        let d = FakeDevice()
        d.focus("email")
        let err = answer {
            try d.typist().type("hi", target: ("name", 42), by: self.deadline(answerInMs: 1500, keysMs: 500), ci: false)
        }
        guard let err else { return XCTFail("type never answered") }
        XCTAssertFalse(err is NoError, "type succeeded although the field never took the focus")
        XCTAssertTrue("\(err)".contains("tapping field name"), "the error says what it waited for: \(err)")
        XCTAssertTrue("\(err)".contains("no keys were sent"), "\(err)")
        XCTAssertEqual(d.clock.elapsedMs, 1000, "answered at \(d.clock.elapsedMs) ms, not at the last moment to start the keys")
        d.goIdle()
        d.readQueue.sync {}
        XCTAssertEqual(d.keys, [], "keys sent after the op answered")
    }

    func testRefocusHidesTheKeyboardFirst() throws {
        let d = FakeDevice()
        d.frames["name"] = CGRect(x: 20, y: 700, width: 362, height: 34)
        d.focus("email")
        try d.typist().type("a", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080), ci: true)
        XCTAssertEqual(d.taps, [CGPoint(x: 201, y: 717)])
        XCTAssertEqual(d.keys, ["a"])
    }

    func testRefocusRefusesAFieldOffScreen() {
        let d = FakeDevice()
        d.frames["name"] = CGRect(x: 20, y: 900, width: 362, height: 34)
        d.focus(nil)
        XCTAssertThrowsError(try d.typist().type("a", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080), ci: true)) {
            XCTAssertTrue("\($0)".contains("off screen"), "\($0)")
        }
        XCTAssertEqual(d.taps, [])
        XCTAssertEqual(d.keys, [])
    }

    func testKeysNotConfirmedAreReportedNotRetried() {
        let d = FakeDevice()
        d.focus("name")
        d.failKeysOnCall = 2
        XCTAssertThrowsError(try d.typist().type("hey", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080))) {
            XCTAssertEqual(($0 as? AgentError)?.transient, false, "\($0)")
            XCTAssertTrue("\($0)".contains("1 of 3 characters"), "\($0)")
            XCTAssertTrue("\($0)".contains("not sent again"), "\($0)")
        }
        XCTAssertEqual(d.keys, ["h", "ey"])
    }

    func testEraseIsBoundedToo() {
        let d = FakeDevice()
        d.focus("name", value: "abc")
        XCTAssertNoThrow(try d.typist().erase(10, target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080)))
        XCTAssertEqual(d.keys, ["↓", "\u{8}\u{8}\u{8}"], "the caret to the end first (issue #301)")

        let h = FakeDevice()
        h.focus("name", value: "abc")
        h.hung = true
        let err = answer { try h.typist().erase(3, target: ("name", 42), by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        XCTAssertNotNil(err, "erase never answered")
        XCTAssertEqual((err as? AgentError)?.transient, true, "\(String(describing: err))")
        XCTAssertEqual(h.clock.elapsedMs, 1000, "answered at \(h.clock.elapsedMs) ms, not at its deadline")
        h.hung = false
        h.readQueue.sync {}
        XCTAssertEqual(h.keys, [])
    }

    func testDeadlineFromTheHostsReplyLimit() {
        let start: UInt64 = 1_000_000_000
        let d = TypingDeadline.forRequest(start: start, replyMs: 20_080, chars: 2)
        XCTAssertEqual(d.answerBy, start + 18_080 * 1_000_000)
        XCTAssertEqual(d.keysBy, start + (18_080 - 2_080) * 1_000_000)
        XCTAssertEqual(TypingDeadline.hostReplyMs(timeoutMs: 5000, perCharMs: 40, chars: 2), 20_080)
        let short = TypingDeadline.forRequest(start: start, replyMs: 1000, chars: 2)
        XCTAssertLessThan(short.answerBy, start + 1000 * 1_000_000)
        XCTAssertGreaterThanOrEqual(short.keysBy, start)
    }

    func testBoundedReadGivesUpAndSkipsLateWork() {
        let q = DispatchQueue(label: "busy main")
        let clock = VirtualClock(holding: q)
        let busy = clock.hold()
        q.async { busy.wait() }
        var ran = false
        XCTAssertThrowsError(try Bounded.run(on: q, by: clock.now() + 200_000_000, clock: clock) { ran = true; return 1 }) {
            XCTAssertEqual(($0 as? Bounded.TimedOut)?.started, false, "\($0)")
        }
        XCTAssertEqual(clock.elapsedMs, 200, "given up on at \(clock.elapsedMs) ms, not at its deadline")
        busy.release()
        q.sync {}
        XCTAssertFalse(ran, "the abandoned read ran after its deadline")
        XCTAssertEqual(try Bounded.run(on: q, by: clock.now() + 1_000_000_000, clock: clock) { 7 }, 7)
        XCTAssertEqual(clock.elapsedMs, 200, "a read in time takes none of the clock")
    }

    func testBoundedReadSaysWhetherItStarted() {
        let q = DispatchQueue(label: "slow snapshot")
        let clock = VirtualClock(holding: q)
        let app = clock.blocker()
        XCTAssertThrowsError(try Bounded.run(on: q, by: clock.now() + 200_000_000, clock: clock) { app.wait(); return 1 }) {
            XCTAssertEqual(($0 as? Bounded.TimedOut)?.started, true, "the read had started: \($0)")
        }
        XCTAssertEqual(clock.elapsedMs, 200)
        app.release()
        q.sync {}
    }

    func testClearSelectsAllThenDeletes() throws {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        XCTAssertTrue(try d.typist().clear(by: deadline(answerInMs: 18_000, keysMs: 2000)))
        XCTAssertEqual(d.keys, ["⌘A", "\u{8}"])
        XCTAssertEqual(d.value, "")
    }

    func testClearDeletesKeyByKeyWhenSelectAllDoesNotTake() throws {
        let d = FakeDevice()
        d.selectAllWorks = false
        d.focus("name", value: "hello")
        XCTAssertTrue(try d.typist().clear(by: deadline(answerInMs: 18_000, keysMs: 2000)))
        XCTAssertEqual(d.keys, ["⌘A", "\u{8}", String(repeating: "\u{8}", count: 4)])
        XCTAssertEqual(d.value, "")
    }

    func testClearAnswersByItsDeadlineWhenTheAppDoesNotAnswer() {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        d.hung = true
        let err = answer { _ = try d.typist().clear(by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        guard let err else { return XCTFail("clear never answered (the app answered no accessibility request)") }
        XCTAssertEqual((err as? AgentError)?.transient, true, "no key was sent: the host may ask again (\(err))")
        XCTAssertTrue("\(err)".contains("clearing did not start"), "\(err)")
        XCTAssertTrue("\(err)".contains("keyboard focus"), "the error says what it waited for: \(err)")
        XCTAssertEqual(d.clock.elapsedMs, 1000, "answered at \(d.clock.elapsedMs) ms, not at the last moment to start the keys")
        d.hung = false
        d.readQueue.sync {}
        XCTAssertEqual(d.keys, [], "keys sent after the op answered")
    }

    func testClearAfterTheKeysReportsWhatWasSent() {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        d.hangAfterKeys = true
        let err = answer { _ = try d.typist().clear(by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        d.hung = false
        d.readQueue.sync {}
        guard let err else { return XCTFail("clear never answered") }
        XCTAssertEqual(d.clock.elapsedMs, 1500, "answered at \(d.clock.elapsedMs) ms, not at its deadline")
        XCTAssertFalse(err is NoError, "clear succeeded without knowing what the field holds")
        XCTAssertEqual((err as? AgentError)?.transient, false, "\(err)")
        XCTAssertTrue("\(err)".contains("select-all and a delete were sent"), "\(err)")
    }

    func testKeysNotSentAreTransient() {
        let d = FakeDevice()
        d.focus("name")
        d.notSentOnCall = 1
        XCTAssertThrowsError(try d.typist().type("hi", target: ("name", 42), by: deadline(answerInMs: 5000, keysMs: 1000))) {
            XCTAssertEqual(($0 as? AgentError)?.transient, true, "\($0)")
            XCTAssertTrue("\($0)".contains("no keys were sent"), "\($0)")
        }
        XCTAssertEqual(d.keys, [])
    }

    func testTypingIntoTheTargetTakesOneSnapshotPerCheck() throws {
        let d = FakeDevice()
        d.focus("name")
        try d.typist().type("hello", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2200))
        XCTAssertEqual(d.keys, ["h", "ello"])
        XCTAssertLessThanOrEqual(d.snapshots, 2, "whole-app snapshots for one type op")
    }

    func testTypingIntoTheFocusedFieldTakesOneSnapshotPerCheck() throws {
        let d = FakeDevice()
        d.focus("name")
        try d.typist().type("hello", by: deadline(answerInMs: 18_000, keysMs: 2200))
        XCTAssertEqual(d.keys, ["h", "ello"])
        XCTAssertLessThanOrEqual(d.snapshots, 2, "whole-app snapshots for one type op")
    }

    func testAKeyboardWithoutAFocusedFieldIsSeenInTheFocusSnapshot() throws {
        let d = FakeDevice()
        d.showKeyboard()
        try d.typist().type("hi", by: deadline(answerInMs: 18_000, keysMs: 2080))
        XCTAssertEqual(d.keys, ["hi"])
        XCTAssertEqual(d.snapshots, 1, "whole-app snapshots for one type op")
    }

    func testWaitingForTheFocusTakesOneSnapshotPerLook() throws {
        let d = FakeDevice()
        d.onWait([{ d.focus("name") }])
        try d.typist().type("hello", by: deadline(answerInMs: 18_000, keysMs: 2200))
        XCTAssertEqual(d.keys, ["h", "ello"])
        XCTAssertEqual(d.changesDelivered, 1)
        XCTAssertLessThanOrEqual(d.snapshots, 3, "whole-app snapshots for one type op")
    }

    func testATickerCostsOneSnapshotPerLook() throws {
        let d = FakeDevice()
        d.focus("name")
        d.echoes = false
        let ticks = 10
        d.onWait(ticks: ticks) { d.focus("name", value: "h") }
        try d.typist(firstKeyWaitMs: 60_000).type("hello", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2200))
        XCTAssertEqual(d.keys, ["h", "ello"])
        let seen = d.changesDelivered
        XCTAssertEqual(seen, ticks + 1, "the wait ended before the field showed the key")
        XCTAssertLessThanOrEqual(d.snapshots, seen + 1, "whole-app snapshots for \(seen) changes")
    }

    func testClearTakesOneSnapshotPerCheck() throws {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        XCTAssertTrue(try d.typist().clear(by: deadline(answerInMs: 18_000, keysMs: 2000)))
        XCTAssertEqual(d.snapshots, 2, "whole-app snapshots for one clear op")
    }
}

private struct Node: FocusNode {
    var isKeyboard = false
    var hasKeyboardFocus = false
    var identifier = ""
    var value: Any?
    var placeholderValue: String?
    var children: [Node] = []
}

final class FocusLookTests: XCTestCase {
    func testTheFirstFocusedNodeInQueryOrder() {
        let input = Node(hasKeyboardFocus: true, identifier: "inner", value: "x")
        let web = Node(hasKeyboardFocus: true, identifier: "web", value: "page", children: [input])
        let later = Node(hasKeyboardFocus: true, identifier: "later", value: "y")
        let app = Node(hasKeyboardFocus: true, identifier: "app", children: [Node(children: [web]), later])
        XCTAssertEqual(FocusState.look(app), FocusState(field: FocusedField(id: "web", value: "page", reportedLength: 4), keyboard: false),
                       "the app itself is not one of its descendants; the web view comes before its input")
    }

    func testAPlaceholderReadsAsEmpty() {
        let field = Node(hasKeyboardFocus: true, identifier: "email", value: "Email", placeholderValue: "Email")
        XCTAssertEqual(FocusState.look(Node(children: [field])).field, FocusedField(id: "email", value: "", reportedLength: 5))
        let typed = Node(hasKeyboardFocus: true, identifier: "email", value: "a@b", placeholderValue: "Email")
        XCTAssertEqual(FocusState.look(Node(children: [typed])).field, FocusedField(id: "email", value: "a@b", reportedLength: 3))
        let none = Node(hasKeyboardFocus: true, identifier: "n", value: nil)
        XCTAssertEqual(FocusState.look(Node(children: [none])).field, FocusedField(id: "n", value: "", reportedLength: 0))
    }

    func testTheKeyboardIsSeenAndItsKeysAreNotWalked() {
        let key = Node(hasKeyboardFocus: true, identifier: "key")
        let keyboard = Node(isKeyboard: true, children: [key])
        XCTAssertEqual(FocusState.look(Node(children: [keyboard])), FocusState(field: nil, keyboard: true))
        let field = Node(hasKeyboardFocus: true, identifier: "name", value: "a")
        let want = FocusState(field: FocusedField(id: "name", value: "a", reportedLength: 1), keyboard: true)
        XCTAssertEqual(FocusState.look(Node(children: [keyboard, field])), want)
        XCTAssertEqual(FocusState.look(Node(children: [field, Node(children: [keyboard])])), want)
        XCTAssertEqual(FocusState.look(Node(children: [Node(children: [field])])), FocusState(field: want.field, keyboard: false))
    }
}

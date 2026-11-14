import XCTest
@testable import TBAgentCore

/// A device for the typing: a field "name" (and "email"), keys recorded in order, and an app
/// that can stop answering accessibility (its main thread busy, issue #46).
private final class FakeDevice: TypingDevice {
    let readQueue = DispatchQueue(label: "fake main thread")
    private let lock = NSLock()
    private var _focused: FocusedField?
    private var _keys: [String] = []
    private var _taps: [CGPoint] = []
    private var _elementTaps = 0
    private var _seq: Int64 = 0
    private var _keyboard = false
    private var _snapshots = 0
    /// Whole-app snapshots the reads took (issue #51), as the agent's own reads take them: one
    /// for focus() (Tree.focus) and one for field(id:pid:) (Tree.field).
    var snapshots: Int { lock.lock(); defer { lock.unlock() }; return _snapshots }
    private func snapshot(_ n: Int = 1) { lock.lock(); _snapshots += n; lock.unlock() }
    /// Whether the focused field shows the keys typed (one that does not: an echo that lags).
    var echoes = true
    /// A keyboard with no field reporting keyboard focus (React Native groups fields so).
    func showKeyboard() { lock.lock(); _keyboard = true; _seq += 1; lock.unlock() }
    /// A change elsewhere on the screen (a ticker).
    func tick() { lock.lock(); _seq += 1; lock.unlock() }
    /// While set, every read waits for the app (as XCTest does) until released or for maxHangMs.
    var hung: Bool {
        get { lock.lock(); defer { lock.unlock() }; return _hung }
        set { lock.lock(); _hung = newValue; lock.unlock() }
    }
    private var _hung = false
    let release = DispatchSemaphore(value: 0)
    let maxHangMs = 4000
    var pid: Int32 = 42
    var frames: [String: CGRect] = ["name": CGRect(x: 20, y: 226, width: 362, height: 34),
                                    "email": CGRect(x: 20, y: 300, width: 362, height: 34)]
    let screen = CGRect(x: 0, y: 0, width: 402, height: 874)
    let keyboardFrame = CGRect(x: 0, y: 520, width: 402, height: 354)
    /// Whether hiding the keyboard works.
    var hideWorks = true
    /// typeKeys fails (not confirmed) on this call, when set.
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
        if hung { _ = release.wait(timeout: .now() + .milliseconds(maxHangMs)) }
    }

    // Reads.
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

    /// How long XCUIElement.tap() waits for the app to go idle: by default as long as XCTest
    /// would for an app that never does (a ticker), here maxHangMs.
    var idleWaitMs = 4000

    // XCUIElement.tap(): waits for the app to go idle first, then taps.
    func tapElement(id: String, pid: Int32) throws {
        lock.lock(); _elementTaps += 1; lock.unlock()
        _ = release.wait(timeout: .now() + .milliseconds(idleWaitMs))
        guard frames[id] != nil else { throw AgentError("cannot uniquely identify field \(id)") }
        focus(id)
    }

    // Input: synthesized, no idle wait.
    func tap(_ p: CGPoint, timeoutMs: Int) throws {
        lock.lock(); _taps.append(p); lock.unlock()
        if let (id, _) = frames.first(where: { $0.value.contains(p) }) { focus(id) }
    }

    /// typeKeys sends nothing on this call (an earlier record still unconfirmed), when set.
    var notSentOnCall: Int?
    /// Whether ⌘A selects the field's text (some fields refuse it).
    var selectAllWorks = true
    /// The app stops answering accessibility once a key went (issue #130).
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
        if hangAfterKeys { _hung = true } // the lock is held
        lock.unlock()
        if fail { throw AgentError("the input was not confirmed within \(timeoutMs / 1000) s") }
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

    var seq: Int64 { lock.lock(); defer { lock.unlock() }; return _seq }
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool {
        let end = Date().addingTimeInterval(Double(timeoutMs) / 1000)
        while Date() < end { if seq != since { return true }; Thread.sleep(forTimeInterval: 0.005) }
        return seq != since
    }
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(Double(timeoutMs) / 1000)
        repeat { if check() { return true }; Thread.sleep(forTimeInterval: 0.005) } while Date() < end
        return false
    }
    func log(_ s: String) {}
}

/// Runs f on another thread (the agent's ops queue) and waits up to ms for it: nil if it has
/// not answered by then, else its error (or NoError).
private struct NoError: Error {}
private func answer(within ms: Int, _ f: @escaping () throws -> Void) -> Error? {
    let done = DispatchSemaphore(value: 0)
    var result: Error?
    DispatchQueue.global().async {
        do { try f(); result = NoError() } catch { result = error }
        done.signal()
    }
    return done.wait(timeout: .now() + .milliseconds(ms)) == .timedOut ? nil : result
}

final class TypingTests: XCTestCase {
    private func deadline(answerInMs: Int, keysMs: Int) -> TypingDeadline {
        let now = DispatchTime.now().uptimeNanoseconds
        return TypingDeadline(start: now, answerBy: now + UInt64(answerInMs) * 1_000_000, keysMs: keysMs)
    }

    /// An idle app: the requested field has the focus, the keys go in order, first key alone.
    func testTypesIntoTheFocusedFieldInOrder() throws {
        let d = FakeDevice()
        d.focus("name")
        let t = Typist(device: d)
        let started = Date()
        try t.type("hi", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080))
        XCTAssertEqual(d.keys, ["h", "i"])
        XCTAssertEqual(d.taps, [])
        XCTAssertLessThan(Date().timeIntervalSince(started), 1, "an idle app is not kept waiting")
    }

    /// Issue #46, the CI failure (run 43005750920): right after the field was tapped the app's
    /// main thread was busy for ~20 s (the keyboard coming up on a slow runner) and answered no
    /// accessibility request. The type op sat in XCTest's focus check until the host gave up
    /// ("agent did not answer "type" within 20.08s"), then typed anyway. It must answer by its
    /// own deadline, say what it waited for and that no key was sent, and send none later.
    func testTypeAnswersByItsDeadlineWhenTheAppDoesNotAnswer() {
        let d = FakeDevice()
        d.focus("name")
        d.hung = true
        let t = Typist(device: d)
        let started = Date()
        let err = answer(within: 2500) { try t.type("hi", target: ("name", 42), by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        let took = Date().timeIntervalSince(started)
        guard let err else { return XCTFail("type did not answer within 2.5 s (the app answered no accessibility request)") }
        XCTAssertFalse(err is NoError, "type succeeded although the app never answered")
        let e = err as? AgentError
        XCTAssertEqual(e?.transient, true, "no key was sent: the host may ask again (\(err))")
        XCTAssertTrue("\(err)".contains("keyboard focus"), "the error says what it waited for: \(err)")
        XCTAssertTrue("\(err)".contains("no keys were sent"), "\(err)")
        XCTAssertLessThan(took, 1.5, "answered after \(took) s, past its deadline")
        // The app answers again: the abandoned check must not type after the answer.
        d.hung = false
        d.release.signal()
        Thread.sleep(forTimeInterval: 0.3)
        XCTAssertEqual(d.keys, [], "keys sent after the op answered")
    }

    /// Issue #46: the field lost the focus; in CI mode it is focused again by a touch at its
    /// middle through the agent's own input (no wait for the app to go idle), not by
    /// XCUIElement.tap(), which waits for an idle app that never comes on a screen with a ticker.
    func testRefocusTouchesTheFieldWithoutWaitingForIdle() {
        let d = FakeDevice()
        d.focus("email")
        d.hideWorks = true
        let t = Typist(device: d)
        let started = Date()
        let err = answer(within: 3000) { try t.type("hi", target: ("name", 42), by: self.deadline(answerInMs: 18_000, keysMs: 2080), ci: true) }
        guard let err else { return XCTFail("type did not answer within 3 s") }
        XCTAssertTrue(err is NoError, "\(err)")
        XCTAssertEqual(d.elementTaps, 0, "refocused with XCUIElement.tap(), which waits for the app to go idle")
        XCTAssertEqual(d.taps, [CGPoint(x: 201, y: 243)])
        XCTAssertEqual(d.keys, ["h", "i"])
        XCTAssertLessThan(Date().timeIntervalSince(started), 1.5)
    }

    /// Outside CI mode the field is focused again as before issue #46's fix: another field's
    /// keyboard hidden, then XCUIElement.tap() on the field. No touch of the agent's own.
    func testOutsideCIRefocusUsesXCTestsTap() throws {
        let d = FakeDevice()
        d.idleWaitMs = 0 // an app that goes idle
        d.focus("email")
        try Typist(device: d).type("hi", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080), ci: false)
        XCTAssertEqual(d.elementTaps, 1)
        XCTAssertEqual(d.taps, [], "refocused by a touch of the agent's own outside CI mode")
        XCTAssertEqual(d.keys, ["h", "i"])
    }

    /// Outside CI mode too, the op answers by its deadline (issue #46's reply limit): an
    /// XCUIElement.tap() still waiting for the app to go idle is given up on, saying so.
    func testOutsideCIRefocusAnswersByItsDeadline() {
        let d = FakeDevice()
        d.focus("email")
        let started = Date()
        let err = answer(within: 2500) {
            try Typist(device: d).type("hi", target: ("name", 42), by: self.deadline(answerInMs: 1500, keysMs: 500), ci: false)
        }
        guard let err else { return XCTFail("type did not answer within 2.5 s") }
        XCTAssertFalse(err is NoError, "type succeeded although the field never took the focus")
        XCTAssertTrue("\(err)".contains("tapping field name"), "the error says what it waited for: \(err)")
        XCTAssertTrue("\(err)".contains("no keys were sent"), "\(err)")
        XCTAssertLessThan(Date().timeIntervalSince(started), 1.5)
        d.release.signal()
        Thread.sleep(forTimeInterval: 0.3)
        XCTAssertEqual(d.keys, [], "keys sent after the op answered")
    }

    /// The keyboard of the other field is hidden first, so the touch cannot land on a key.
    func testRefocusHidesTheKeyboardFirst() throws {
        let d = FakeDevice()
        d.frames["name"] = CGRect(x: 20, y: 700, width: 362, height: 34) // under the keyboard
        d.focus("email")
        try Typist(device: d).type("a", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080), ci: true)
        XCTAssertEqual(d.taps, [CGPoint(x: 201, y: 717)])
        XCTAssertEqual(d.keys, ["a"])
    }

    /// A field outside the screen is not touched (the touch would land elsewhere).
    func testRefocusRefusesAFieldOffScreen() {
        let d = FakeDevice()
        d.frames["name"] = CGRect(x: 20, y: 900, width: 362, height: 34)
        d.focus(nil)
        XCTAssertThrowsError(try Typist(device: d).type("a", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080), ci: true)) {
            XCTAssertTrue("\($0)".contains("off screen"), "\($0)")
        }
        XCTAssertEqual(d.taps, [])
        XCTAssertEqual(d.keys, [])
    }

    /// Keys sent but not confirmed: the error says how many went and that they are not sent
    /// again, and the host must not ask again (not transient).
    func testKeysNotConfirmedAreReportedNotRetried() {
        let d = FakeDevice()
        d.focus("name")
        d.failKeysOnCall = 2
        XCTAssertThrowsError(try Typist(device: d).type("hey", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080))) {
            XCTAssertEqual(($0 as? AgentError)?.transient, false, "\($0)")
            XCTAssertTrue("\($0)".contains("1 of 3 characters"), "\($0)")
            XCTAssertTrue("\($0)".contains("not sent again"), "\($0)")
        }
        XCTAssertEqual(d.keys, ["h", "ey"])
    }

    /// Erasing waits the same way and deletes no more than the field holds.
    func testEraseIsBoundedToo() {
        let d = FakeDevice()
        d.focus("name", value: "abc")
        XCTAssertNoThrow(try Typist(device: d).erase(10, target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080)))
        XCTAssertEqual(d.keys, ["\u{8}\u{8}\u{8}"])

        let h = FakeDevice()
        h.focus("name", value: "abc")
        h.hung = true
        let err = answer(within: 2500) { try Typist(device: h).erase(3, target: ("name", 42), by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        XCTAssertNotNil(err, "erase did not answer")
        XCTAssertEqual((err as? AgentError)?.transient, true, "\(String(describing: err))")
        h.hung = false
        h.release.signal()
        Thread.sleep(forTimeInterval: 0.3)
        XCTAssertEqual(h.keys, [])
    }

    /// The deadline from the host's reply limit: the answer comes before it, with time kept for
    /// the keys; a request without one gets the host's own formula.
    func testDeadlineFromTheHostsReplyLimit() {
        let start: UInt64 = 1_000_000_000
        let d = TypingDeadline.forRequest(start: start, replyMs: 20_080, chars: 2)
        XCTAssertEqual(d.answerBy, start + 18_080 * 1_000_000)
        XCTAssertEqual(d.keysBy, start + (18_080 - 2_080) * 1_000_000)
        XCTAssertEqual(TypingDeadline.hostReplyMs(timeoutMs: 5000, perCharMs: 40, chars: 2), 20_080)
        // A short limit still leaves the op an answer before it.
        let short = TypingDeadline.forRequest(start: start, replyMs: 1000, chars: 2)
        XCTAssertLessThan(short.answerBy, start + 1000 * 1_000_000)
        XCTAssertGreaterThanOrEqual(short.keysBy, start)
    }

    /// The bounded read gives up at its deadline and the late work does not run.
    func testBoundedReadGivesUpAndSkipsLateWork() {
        let q = DispatchQueue(label: "busy main")
        let gate = DispatchSemaphore(value: 0)
        q.async { _ = gate.wait(timeout: .now() + 2) } // the main thread is busy
        var ran = false
        let now = DispatchTime.now().uptimeNanoseconds
        XCTAssertThrowsError(try Bounded.run(on: q, by: now + 200_000_000) { ran = true; return 1 }) {
            XCTAssertTrue($0 is Bounded.TimedOut)
        }
        gate.signal()
        q.sync {}
        XCTAssertFalse(ran, "the abandoned read ran after its deadline")
        XCTAssertEqual(try Bounded.run(on: q, by: DispatchTime.now().uptimeNanoseconds + 1_000_000_000) { 7 }, 7)
    }

    /// Issue #130: a read given up on says whether it had started (the main thread was reached).
    func testBoundedReadSaysWhetherItStarted() {
        let q = DispatchQueue(label: "slow snapshot")
        let now = DispatchTime.now().uptimeNanoseconds
        XCTAssertThrowsError(try Bounded.run(on: q, by: now + 200_000_000) { Thread.sleep(forTimeInterval: 0.6); return 1 }) {
            XCTAssertEqual(($0 as? Bounded.TimedOut)?.started, true, "the read had started: \($0)")
        }
        q.sync {}
    }

    // MARK: clear (issue #130)

    /// An idle app: select all, then one delete, and the field is empty.
    func testClearSelectsAllThenDeletes() throws {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        XCTAssertTrue(try Typist(device: d).clear(by: deadline(answerInMs: 18_000, keysMs: 2000)))
        XCTAssertEqual(d.keys, ["⌘A", "\u{8}"])
        XCTAssertEqual(d.value, "")
    }

    /// A field that refuses ⌘A is emptied key by key.
    func testClearDeletesKeyByKeyWhenSelectAllDoesNotTake() throws {
        let d = FakeDevice()
        d.selectAllWorks = false
        d.focus("name", value: "hello")
        XCTAssertTrue(try Typist(device: d).clear(by: deadline(answerInMs: 18_000, keysMs: 2000)))
        XCTAssertEqual(d.keys, ["⌘A", "\u{8}", String(repeating: "\u{8}", count: 4)])
        XCTAssertEqual(d.value, "")
    }

    /// Issue #130: clear read the focused field on the main thread with no limit, so an app
    /// whose main thread was busy held it until the host timed out blind. It answers by its
    /// deadline now, says what it waited for, and sends no key later.
    func testClearAnswersByItsDeadlineWhenTheAppDoesNotAnswer() {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        d.hung = true
        let started = Date()
        let err = answer(within: 2500) { _ = try Typist(device: d).clear(by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        let took = Date().timeIntervalSince(started)
        guard let err else { return XCTFail("clear did not answer within 2.5 s (the app answered no accessibility request)") }
        XCTAssertEqual((err as? AgentError)?.transient, true, "no key was sent: the host may ask again (\(err))")
        XCTAssertTrue("\(err)".contains("clearing did not start"), "\(err)")
        XCTAssertTrue("\(err)".contains("keyboard focus"), "the error says what it waited for: \(err)")
        XCTAssertLessThan(took, 1.5, "answered after \(took) s, past its deadline")
        d.hung = false
        d.release.signal()
        Thread.sleep(forTimeInterval: 0.3)
        XCTAssertEqual(d.keys, [], "keys sent after the op answered")
    }

    /// Issue #130: the app stops answering after the keys went: clear answers by its deadline,
    /// says what was sent, and is not asked again (the field may be empty already).
    func testClearAfterTheKeysReportsWhatWasSent() {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        d.hangAfterKeys = true
        let err = answer(within: 2500) { _ = try Typist(device: d).clear(by: self.deadline(answerInMs: 1500, keysMs: 500)) }
        d.hung = false
        d.release.signal()
        guard let err else { return XCTFail("clear did not answer within 2.5 s") }
        XCTAssertFalse(err is NoError, "clear succeeded without knowing what the field holds")
        XCTAssertEqual((err as? AgentError)?.transient, false, "\(err)")
        XCTAssertTrue("\(err)".contains("select-all and a delete were sent"), "\(err)")
    }

    /// Issue #130: a key record not played because an earlier one was still unconfirmed sent
    /// nothing: before any key the error is transient, as for a read the app did not answer.
    func testKeysNotSentAreTransient() {
        let d = FakeDevice()
        d.focus("name")
        d.notSentOnCall = 1
        XCTAssertThrowsError(try Typist(device: d).type("hi", target: ("name", 42), by: deadline(answerInMs: 5000, keysMs: 1000))) {
            XCTAssertEqual(($0 as? AgentError)?.transient, true, "\($0)")
            XCTAssertTrue("\($0)".contains("no keys were sent"), "\($0)")
        }
        XCTAssertEqual(d.keys, [])
    }

    // MARK: whole-app snapshots (issue #51)

    /// Issue #51: typing into the field the host tapped took four whole-app snapshots before
    /// the rest of the keys (the focus check before the keys, then the check that the field
    /// shows the first: two each, .exists then .snapshot()). One snapshot answers each check.
    func testTypingIntoTheTargetTakesOneSnapshotPerCheck() throws {
        let d = FakeDevice()
        d.focus("name")
        try Typist(device: d).type("hello", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2200))
        XCTAssertEqual(d.keys, ["h", "ello"])
        XCTAssertLessThanOrEqual(d.snapshots, 2, "whole-app snapshots for one type op")
    }

    /// Issue #51: with no target, a field already focused: the focus check and the check that
    /// the field shows the first key, one snapshot each.
    func testTypingIntoTheFocusedFieldTakesOneSnapshotPerCheck() throws {
        let d = FakeDevice()
        d.focus("name")
        try Typist(device: d).type("hello", by: deadline(answerInMs: 18_000, keysMs: 2200))
        XCTAssertEqual(d.keys, ["h", "ello"])
        XCTAssertLessThanOrEqual(d.snapshots, 2, "whole-app snapshots for one type op")
    }

    /// Issue #51: a keyboard with no field reporting focus (React Native) is seen in the same
    /// snapshot as the focus: one snapshot, then the keys at the first key's rate.
    func testAKeyboardWithoutAFocusedFieldIsSeenInTheFocusSnapshot() throws {
        let d = FakeDevice()
        d.showKeyboard()
        try Typist(device: d).type("hi", by: deadline(answerInMs: 18_000, keysMs: 2080))
        XCTAssertEqual(d.keys, ["hi"])
        XCTAssertEqual(d.snapshots, 1, "whole-app snapshots for one type op")
    }

    /// Issue #51: waiting for a field to take the focus looks once per change event, one
    /// snapshot a look (it took three: the focused field twice, the keyboard once).
    func testWaitingForTheFocusTakesOneSnapshotPerLook() throws {
        let d = FakeDevice()
        DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(100)) { d.focus("name") }
        try Typist(device: d).type("hello", by: deadline(answerInMs: 18_000, keysMs: 2200))
        XCTAssertEqual(d.keys, ["h", "ello"])
        // Before the change: one look. After it: the look that finds the field, then the look
        // that sees the first key.
        XCTAssertLessThanOrEqual(d.snapshots, 3, "whole-app snapshots for one type op")
    }

    /// Issue #51: on a screen with a ticker, the wait for the first key to show looks on every
    /// change event for up to firstKeyWaitMs: one snapshot a look, not two.
    func testATickerCostsOneSnapshotPerLook() throws {
        let d = FakeDevice()
        d.focus("name")
        d.echoes = false
        let lock = NSLock()
        var ticks = 0
        let stop = DispatchSemaphore(value: 0)
        let ticker = Thread {
            while stop.wait(timeout: .now() + .milliseconds(50)) == .timedOut { d.tick(); lock.lock(); ticks += 1; lock.unlock() }
        }
        ticker.start()
        try Typist(device: d).type("hello", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2200))
        let snapshots = d.snapshots
        lock.lock(); let seen = ticks; lock.unlock()
        stop.signal()
        XCTAssertEqual(d.keys, ["h", "ello"])
        XCTAssertGreaterThan(seen, 3)
        // The focus check, then at most one look per tick.
        XCTAssertLessThanOrEqual(snapshots, seen + 1, "whole-app snapshots for \(seen) ticks")
    }

    /// Issue #51: clear's two checks (what the field holds, before and after), one snapshot each.
    func testClearTakesOneSnapshotPerCheck() throws {
        let d = FakeDevice()
        d.focus("name", value: "hello")
        XCTAssertTrue(try Typist(device: d).clear(by: deadline(answerInMs: 18_000, keysMs: 2000)))
        XCTAssertEqual(d.snapshots, 2, "whole-app snapshots for one clear op")
    }
}

/// A snapshot node for the focus look's tests.
private struct Node: FocusNode {
    var isKeyboard = false
    var hasKeyboardFocus = false
    var identifier = ""
    var value: Any?
    var placeholderValue: String?
    var children: [Node] = []
}

/// The focus look over one snapshot (issue #51): what Tree.focus returns.
final class FocusLookTests: XCTestCase {
    /// The first focused descendant in a query's order: an outer web view that reports keyboard
    /// focus comes before the input inside it (firstMatch's pre-order).
    func testTheFirstFocusedNodeInQueryOrder() {
        let input = Node(hasKeyboardFocus: true, identifier: "inner", value: "x")
        let web = Node(hasKeyboardFocus: true, identifier: "web", value: "page", children: [input])
        let later = Node(hasKeyboardFocus: true, identifier: "later", value: "y")
        let app = Node(hasKeyboardFocus: true, identifier: "app", children: [Node(children: [web]), later])
        XCTAssertEqual(FocusState.look(app), FocusState(field: FocusedField(id: "web", value: "page", reportedLength: 4), keyboard: false),
                       "the app itself is not one of its descendants; the web view comes before its input")
    }

    /// XCTest reports an empty field's placeholder as its value: it reads as "", and the
    /// reported length stays the placeholder's.
    func testAPlaceholderReadsAsEmpty() {
        let field = Node(hasKeyboardFocus: true, identifier: "email", value: "Email", placeholderValue: "Email")
        XCTAssertEqual(FocusState.look(Node(children: [field])).field, FocusedField(id: "email", value: "", reportedLength: 5))
        let typed = Node(hasKeyboardFocus: true, identifier: "email", value: "a@b", placeholderValue: "Email")
        XCTAssertEqual(FocusState.look(Node(children: [typed])).field, FocusedField(id: "email", value: "a@b", reportedLength: 3))
        let none = Node(hasKeyboardFocus: true, identifier: "n", value: nil)
        XCTAssertEqual(FocusState.look(Node(children: [none])).field, FocusedField(id: "n", value: "", reportedLength: 0))
    }

    /// A keyboard is seen with or without a focused field (React Native reports none), in
    /// whichever order it comes, and its keys are not walked: they never hold keyboard focus.
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

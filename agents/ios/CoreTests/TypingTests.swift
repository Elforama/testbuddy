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
    /// While set, every read waits for the app (as XCTest does) until released or for maxHangMs.
    var hung = false
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
    func focusedField() -> FocusedField? { appAnswers(); lock.lock(); defer { lock.unlock() }; return _focused }
    func keyboardShown() -> Bool { appAnswers(); lock.lock(); defer { lock.unlock() }; return _keyboard }
    func field(id: String, pid: Int32) throws -> FieldGeometry {
        appAnswers()
        guard let f = frames[id] else { throw AgentError("cannot uniquely identify field \(id)") }
        lock.lock(); defer { lock.unlock() }
        return FieldGeometry(frame: f, screen: screen, keyboard: _keyboard ? keyboardFrame : nil)
    }

    // XCUIElement.tap(): waits for the app to go idle first. This app never does (a ticker),
    // so it waits as long as XCTest would, here maxHangMs, then taps.
    func tapElement(id: String, pid: Int32) throws {
        lock.lock(); _elementTaps += 1; lock.unlock()
        _ = release.wait(timeout: .now() + .milliseconds(maxHangMs))
        focus(id)
    }

    // Input: synthesized, no idle wait.
    func tap(_ p: CGPoint, timeoutMs: Int) throws {
        lock.lock(); _taps.append(p); lock.unlock()
        if let (id, _) = frames.first(where: { $0.value.contains(p) }) { focus(id) }
    }

    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws {
        lock.lock()
        keyCalls += 1
        _keys.append(text)
        let fail = failKeysOnCall == keyCalls
        if !fail, let f = _focused {
            var v = f.value
            for c in text { if c == "\u{8}" { if !v.isEmpty { v.removeLast() } } else { v.append(c) } }
            _focused = FocusedField(id: f.id, value: v, reportedLength: v.count)
            _seq += 1
        }
        lock.unlock()
        if fail { throw AgentError("the input was not confirmed within \(timeoutMs / 1000) s") }
    }

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

    /// Issue #46: the field lost the focus; it is focused again by a touch at its middle through
    /// the agent's own input (no wait for the app to go idle), not by XCUIElement.tap(), which
    /// waits for an idle app that never comes on a screen with a ticker.
    func testRefocusTouchesTheFieldWithoutWaitingForIdle() {
        let d = FakeDevice()
        d.focus("email")
        d.hideWorks = true
        let t = Typist(device: d)
        let started = Date()
        let err = answer(within: 3000) { try t.type("hi", target: ("name", 42), by: self.deadline(answerInMs: 18_000, keysMs: 2080)) }
        guard let err else { return XCTFail("type did not answer within 3 s") }
        XCTAssertTrue(err is NoError, "\(err)")
        XCTAssertEqual(d.elementTaps, 0, "refocused with XCUIElement.tap(), which waits for the app to go idle")
        XCTAssertEqual(d.taps, [CGPoint(x: 201, y: 243)])
        XCTAssertEqual(d.keys, ["h", "i"])
        XCTAssertLessThan(Date().timeIntervalSince(started), 1.5)
    }

    /// The keyboard of the other field is hidden first, so the touch cannot land on a key.
    func testRefocusHidesTheKeyboardFirst() throws {
        let d = FakeDevice()
        d.frames["name"] = CGRect(x: 20, y: 700, width: 362, height: 34) // under the keyboard
        d.focus("email")
        try Typist(device: d).type("a", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080))
        XCTAssertEqual(d.taps, [CGPoint(x: 201, y: 717)])
        XCTAssertEqual(d.keys, ["a"])
    }

    /// A field outside the screen is not touched (the touch would land elsewhere).
    func testRefocusRefusesAFieldOffScreen() {
        let d = FakeDevice()
        d.frames["name"] = CGRect(x: 20, y: 900, width: 362, height: 34)
        d.focus(nil)
        XCTAssertThrowsError(try Typist(device: d).type("a", target: ("name", 42), by: deadline(answerInMs: 18_000, keysMs: 2080))) {
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
}

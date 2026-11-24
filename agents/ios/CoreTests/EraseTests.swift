import XCTest
@testable import TBAgentCore

private final class CaretField: TypingDevice {
    let readQueue = DispatchQueue(label: "fake main thread")
    let clock: VirtualClock
    private let lock = NSLock()
    private var _value: String
    private var _caret: Int
    private var _seq: Int64 = 0
    private var _keys: [String] = []
    let secure: Bool

    private func at(_ ms: Int, _ f: @escaping () -> Void) {
        clock.at(ms) { [self] in lock.lock(); f(); lock.unlock() }
    }

    init(_ value: String, caret: Int? = nil, secure: Bool = false) {
        clock = VirtualClock(holding: readQueue)
        _value = value
        _caret = caret ?? value.count
        self.secure = secure
    }

    var readsHangAfterDels = false
    private var _hang: VirtualClock.Hold?
    func answer() { lock.lock(); let h = _hang; _hang = nil; lock.unlock(); h?.release() }

    var caretKeysWork = true
    var dropDels = 0
    var echoDelayMs = 0
    var mask: ((String) -> String)?
    var keepsChanging = false
    var downMovesFocus = false
    var downMs = 0
    private var focusedID = "name"
    var noFocusReads = 0
    private var noFocusLeft = 0

    var keys: [String] { lock.lock(); defer { lock.unlock() }; return _keys }
    var value: String { lock.lock(); defer { lock.unlock() }; return _value }
    private var delRecords = 0

    private func set(_ v: String, caret c: Int) {
        let m = mask?(v) ?? v
        _value = m
        _caret = min(max(c + (m.count - v.count), 0), m.count)
        _seq += 1
    }

    func foregroundPID() -> Int32 { 42 }
    func focus() -> FocusState {
        lock.lock(); let hang = _hang; lock.unlock()
        hang?.wait()
        lock.lock(); defer { lock.unlock() }
        if noFocusLeft > 0 { noFocusLeft -= 1; return FocusState(field: nil, keyboard: true) }
        if focusedID != "name" { return FocusState(field: FocusedField(id: focusedID, value: "", reportedLength: 0), keyboard: true) }
        return FocusState(field: FocusedField(id: "name", value: _value, reportedLength: _value.count, secure: secure, frame: CGRect(x: 20, y: 200, width: 362, height: height)), keyboard: true)
    }
    func field(id: String, pid: Int32) throws -> FieldGeometry {
        FieldGeometry(frame: CGRect(x: 20, y: 200, width: 362, height: 34), screen: CGRect(x: 0, y: 0, width: 402, height: 874), keyboard: nil)
    }
    func tap(_ p: CGPoint, timeoutMs: Int) throws {}
    func tapElement(id: String, pid: Int32, timeoutMs: Int) throws {}
    func hideKeyboard(withinMs: Int) throws -> Bool { true }
    func selectAll(timeoutMs: Int) throws { lock.lock(); _keys.append("⌘A"); lock.unlock() }

    var reportsSelection = false
    var selectionLength = 0
    var selectionValue: String?
    var selectionAnswersAfterMs: Int?
    func selection() -> TextSelection? {
        if let ms = selectionAnswersAfterMs {
            let h = clock.blocker()
            clock.at(ms) { h.release() }
            h.wait()
        }
        lock.lock(); defer { lock.unlock() }
        guard reportsSelection, focusedID == "name" else { return nil }
        return TextSelection(id: focusedID, location: String(_value.prefix(_caret)).utf16.count, length: selectionLength, value: selectionValue ?? _value)
    }

    var wrapAt = 30
    var height: CGFloat = 65
    private func lines() -> [(start: Int, length: Int)] {
        var out: [(Int, Int)] = []
        var start = 0
        for p in _value.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline) {
            var rest = p.count
            repeat { let n = min(rest, wrapAt); out.append((start, n)); start += n; rest -= n } while rest > 0
            start += 1
        }
        return out
    }

    func caretDown(timeoutMs: Int) throws {
        lock.lock(); defer { lock.unlock() }
        clock.skip(ms: downMs)
        _keys.append("↓")
        noFocusLeft = noFocusReads
        if downMovesFocus { focusedID = "other"; _seq += 1; return }
        guard caretKeysWork else { return }
        let ls = lines()
        guard let i = ls.firstIndex(where: { _caret <= $0.start + $0.length }), i + 1 < ls.count else {
            _caret = _value.count
            return
        }
        _caret = ls[i + 1].start + min(_caret - ls[i].start, ls[i + 1].length)
    }

    func typeKeys(_ text: String, cps: Int, timeoutMs: Int) throws {
        lock.lock()
        if focusedID != "name" { _keys.append("⌫\(text.count) into \(focusedID)"); lock.unlock(); return }
        _keys.append(text.allSatisfy({ $0 == "\u{8}" }) ? "⌫\(text.count)" : text)
        delRecords += 1
        var dels = text.filter { $0 == "\u{8}" }.count
        if delRecords == 1 { dels -= min(dropDels, dels) }
        let delay = echoDelayMs
        let changing = keepsChanging
        lock.unlock()
        let apply = { [self] in
            var chars = Array(_value)
            var c = _caret
            for _ in 0..<dels where c > 0 { chars.remove(at: c - 1); c -= 1 }
            set(String(chars), caret: c)
            if changing { startChanging() }
            if readsHangAfterDels, _hang == nil { _hang = clock.blocker() }
        }
        lock.lock()
        if delay > 0 { at(delay, apply) } else { apply() }
        lock.unlock()
    }

    private func startChanging() {
        for i in 0..<100 { at(40 * (i + 1)) { [self] in _value = "tick \(i)"; _seq += 1 } }
    }

    var seq: Int64 { lock.lock(); defer { lock.unlock() }; return _seq }
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool {
        clock.waitFor(ms: timeoutMs) { seq != since }
    }
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool {
        let end = clock.now() + UInt64(max(timeoutMs, 0)) * 1_000_000
        repeat {
            if check() { return true }
            _ = awaitChange(since: seq, timeoutMs: 5)
        } while clock.now() < end
        return false
    }
    func log(_ s: String) {}
}

final class EraseTests: XCTestCase {
    private func deadline() -> TypingDeadline {
        let now = VirtualClock.start
        return TypingDeadline(start: now, answerBy: now + 18_000 * 1_000_000, keysMs: 4000)
    }
    private func typist(_ d: CaretField) -> Typist { Typist(device: d, eraseCapMs: 100, clock: d.clock) }

    func testAReadBackTheAppDoesNotAnswerEndsAtTheAnswerTime() throws {
        let d = CaretField("QUICK BROWN FOX FAST")
        d.readsHangAfterDels = true
        defer { d.answer(); d.readQueue.sync {} }
        XCTAssertFalse(try typist(d).erase(4, by: deadline()), "repaired a field it could not read")
        XCTAssertEqual(d.clock.elapsedMs, 18_000, "answered at \(d.clock.elapsedMs) ms, not at its answer time")
        XCTAssertEqual(d.keys, ["↓", "⌫4"], "keys sent after the read-back was given up on")
    }

    func testAFullEraseFromAMidTextCaretLeavesTheFieldEmpty() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        let repaired = try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertFalse(repaired)
        XCTAssertEqual(d.keys, ["↓", "⌫20"], "the caret to the end, then the DELs")
    }

    func testAPartialEraseFromAMidTextCaretTakesTheEnd() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        try typist(d).erase(4, by: deadline())
        XCTAssertEqual(d.value, "QUICK BROWN FOX ")
    }

    func testMultiLineTextIsWalkedToItsEnd() throws {
        let d = CaretField("one\ntwo three\nfour", caret: 1)
        try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys, ["↓", "↓", "↓", "⌫18"])
    }

    func testAWrappedParagraphIsWalkedToItsEnd() throws {
        let text = "the quick brown fox jumps over the lazy dog and runs far away from here"
        let d = CaretField(text, caret: 5)
        try typist(d).erase(1, by: deadline())
        XCTAssertEqual(d.value, String(text.dropLast()))
        XCTAssertEqual(d.keys, ["↓", "↓", "↓", "↓", "⌫1"], "71 characters at 16 points each in 362 points: 4 lines at most")
    }

    func testAFieldOneLineTallGetsOneDownArrow() throws {
        let text = "THE QUICK BROWN FOX JUMPS OVER THE LAZY DOG AGAIN"
        let d = CaretField(text, caret: 5)
        d.height = 24
        d.wrapAt = 1000
        try typist(d).erase(1, by: deadline())
        XCTAssertEqual(d.value, String(text.dropLast()))
        XCTAssertEqual(d.keys, ["↓", "⌫1"])
        XCTAssertEqual(CaretToEnd.lines(for: "a\nb", frame: CGRect(x: 0, y: 0, width: 362, height: 24)), 2, "a line break: the estimate")
    }

    func testTheCommonCaseCostsNoDownArrow() throws {
        let d = CaretField("ada.example.com")
        d.height = 34
        d.wrapAt = 1000
        d.reportsSelection = true
        d.downMs = 350
        let t0 = d.clock.now()
        XCTAssertFalse(try typist(d).erase(30, by: deadline()))
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys, ["⌫15"], "the caret was at the end: only the DELs")
        XCTAssertLessThanOrEqual(d.clock.now() - t0, 50 * 1_000_000, "took \((d.clock.now() - t0) / 1_000_000) ms of the fake's clock")
    }

    func testACaretReportedMidTextStillGoesToTheEnd() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.height = 34
        d.reportsSelection = true
        try typist(d).erase(4, by: deadline())
        XCTAssertEqual(d.value, "QUICK BROWN FOX ")
        XCTAssertEqual(d.keys, ["↓", "⌫4"])
    }

    func testOnlyACaretAtTheEndOfTheTextReadSkipsTheDownArrow() throws {
        let f = FocusedField(id: "name", value: "abc", reportedLength: 3)
        XCTAssertTrue(CaretToEnd.atEnd(TextSelection(id: "name", location: 3, length: 0, value: "abc"), of: f))
        XCTAssertFalse(CaretToEnd.atEnd(nil, of: f), "no selection reported")
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 1, length: 0, value: "abc"), of: f), "a caret before the end")
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 0, length: 3, value: "abc"), of: f), "a selection")
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 4, length: 0, value: "abcd"), of: f), "the value moved since the look")
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 3, length: 0, value: nil), of: f), "no value read with it")
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 0, length: 0, value: ""), of: FocusedField(id: "name", value: "", reportedLength: 5)),
                       "a field reading as its placeholder: its text is not known")
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "other", location: 3, length: 0, value: "abc"), of: f), "another field's")
        XCTAssertTrue(CaretToEnd.atEnd(TextSelection(id: "name", location: 6, length: 0, value: "ab👍🏽"), of: FocusedField(id: "name", value: "ab👍🏽", reportedLength: 3)))
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 3, length: 0, value: "ab👍🏽"), of: FocusedField(id: "name", value: "ab👍🏽", reportedLength: 3)))

        XCTAssertTrue(TextSelection.range("{15, 0}")! == (15, 0))
        XCTAssertTrue(TextSelection.range(NSValue(range: NSRange(location: 2, length: 3)))! == (2, 3))
        XCTAssertNil(TextSelection.range("{15}"))
        XCTAssertNil(TextSelection.range("{-1, 0}"))
        XCTAssertNil(TextSelection.range(nil))
        XCTAssertNil(TextSelection.range(NSValue(range: NSRange(location: NSNotFound, length: 0))))

        let d = CaretField("QUICK BROWN FOX FAST")
        d.reportsSelection = true
        d.selectionValue = "QUICK BROWN FOX FAS"
        try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.keys, ["↓", "⌫20"], "a value that moved since the look: the down arrow goes")
    }

    func testASecureFieldAlwaysGetsTheDownArrow() throws {
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 2, length: 0, value: "••"),
                                        of: FocusedField(id: "name", value: "••", reportedLength: 2, secure: true)))
        let d = CaretField("••••", secure: true)
        d.height = 34
        d.reportsSelection = true
        XCTAssertFalse(try typist(d).erase(30, by: deadline()))
        XCTAssertEqual(d.keys, ["↓", "⌫4"])
    }

    func testASelectionReadTheAppDoesNotAnswerTakesTheDownArrow() throws {
        let d = CaretField("ada.example.com")
        d.height = 34
        d.reportsSelection = true
        d.selectionAnswersAfterMs = 15_000
        XCTAssertFalse(try typist(d).erase(30, by: deadline()))
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys, ["↓", "⌫15"])
    }

    func testAFieldWithItsCaretAtTheEndIsStillRepaired() throws {
        let d = CaretField("QUICK")
        d.height = 34
        d.reportsSelection = true
        d.dropDels = 2
        XCTAssertTrue(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys, ["⌫5", "↓", "⌫1", "⌫1"])
    }

    func testAnUntouchedFieldFails() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 0)
        d.caretKeysWork = false
        XCTAssertThrowsError(try typist(d).erase(50, by: deadline())) {
            XCTAssertTrue("\($0)".contains(#"erasing: the field holds "QUICK BROWN FOX FAST", expected """#), "\($0)")
        }
    }

    func testAFocusMovedByTheDownArrowFailsBeforeAnyDEL() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.downMovesFocus = true
        XCTAssertThrowsError(try typist(d).erase(50, by: deadline())) {
            XCTAssertTrue("\($0)".contains("the focus moved to other"), "\($0)")
            XCTAssertTrue("\($0)".contains("nothing was deleted"), "\($0)")
        }
        XCTAssertEqual(d.keys, ["↓"])
    }

    func testAShortFieldThatWrapsIsWalkedToItsEnd() throws {
        let text = "the quick brown fox jumps over the lazy dog and runs far away from here"
        let d = CaretField(text, caret: 5)
        d.height = 40
        try typist(d).erase(1, by: deadline())
        XCTAssertEqual(d.value, String(text.dropLast()))
        XCTAssertEqual(d.keys, ["↓", "↓", "↓", "↓", "⌫1"])
    }

    func testTheDownArrowsStopAtTheAnswerTime() {
        let d = CaretField(String(repeating: "line\n", count: 20), caret: 0)
        d.downMs = 100
        let t0 = d.clock.now()
        let dl = TypingDeadline(start: t0, answerBy: t0 + 450 * 1_000_000, keysMs: 100)
        XCTAssertThrowsError(try typist(d).erase(50, by: dl)) {
            XCTAssertTrue("\($0)".contains("after 4 of 21 down arrows; nothing was deleted"), "\($0)")
        }
        XCTAssertEqual(d.clock.now() - t0, 400 * 1_000_000, "answered by its answer time")
        XCTAssertEqual(d.keys, ["↓", "↓", "↓", "↓"])
    }

    func testALongTextViewErases() throws {
        let text = String(repeating: "line\n", count: 100)
        let d = CaretField(text, caret: 0)
        try typist(d).erase(1000, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys.filter { $0 == "↓" }.count, 101)
    }

    func testAFocusReadAsNoneForAMomentIsReadAgain() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.noFocusReads = 2
        try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
    }

    func testTheLinesATextCanTake() {
        let tall = CGRect(x: 20, y: 200, width: 362, height: 65)
        XCTAssertEqual(CaretToEnd.lines(for: "QUICK BROWN FOX FAST", frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: "", frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: nil, frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: "a\n\nb", frame: tall), 3)
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x", count: 23), frame: tall), 2)
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x", count: 23), frame: .zero), 2, "an unknown width: 320 points")
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x\n", count: 100), frame: tall), 101, "not capped")
        let long = String(repeating: "x", count: 49)
        XCTAssertEqual(CaretToEnd.lines(for: long, frame: CGRect(x: 0, y: 0, width: 362, height: 34)), 3)
        XCTAssertEqual(CaretToEnd.lines(for: long, frame: CGRect(x: 0, y: 0, width: 362, height: 24)), 1)
    }

    func testAFieldThatIgnoresTheDownArrowFailsNamingWhatItHolds() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.caretKeysWork = false
        XCTAssertThrowsError(try typist(d).erase(50, by: deadline())) {
            XCTAssertTrue("\($0)".contains(#"erasing: the field holds "BROWN FOX FAST", expected """#), "\($0)")
        }
    }

    func testAPartialEraseThatTookTextFromBeforeTheEndFails() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 10)
        d.caretKeysWork = false
        XCTAssertThrowsError(try typist(d).erase(4, by: deadline())) {
            XCTAssertTrue("\($0)".contains(#"erasing: the field holds "QUICK N FOX FAST", expected "QUICK BROWN FOX ""#), "\($0)")
            XCTAssertTrue("\($0)".contains("before the end"), "\($0)")
        }
        XCTAssertEqual(d.keys, ["↓", "⌫4"], "no repair DEL: it would take more")
    }

    func testDelsThatDidNotArriveAreRepairedOnce() throws {
        let d = CaretField("QUICK", caret: 2)
        d.dropDels = 2
        let repaired = try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertTrue(repaired)
        XCTAssertEqual(d.keys, ["↓", "⌫5", "↓", "⌫1", "⌫1"])
    }

    func testAMaskThatPutsItsPrefixBackGetsNoVerdict() throws {
        let d = CaretField("+1 555", caret: 4)
        d.mask = { $0.hasPrefix("+1 ") ? $0 : "+1 " }
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.value, "+1 ")
    }

    func testALaggingReadIsNeverRepaired() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.echoDelayMs = 60
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys, ["↓", "⌫20"])
    }

    func testAFieldStillChangingGetsNoVerdict() throws {
        let d = CaretField("QUICK BROWN FOX FAST")
        d.keepsChanging = true
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.keys, ["↓", "⌫20"])
    }

    func testASecureFieldGetsNoVerdict() throws {
        let d = CaretField("••••••", secure: true)
        d.dropDels = 2
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.keys, ["↓", "⌫6"])
    }

    func testWhatIsJudged() {
        let e = Erasing(old: "QUICK BROWN FOX FAST", count: 50)
        XCTAssertEqual(e.expected, "")
        XCTAssertTrue(e.judged("BROWN FOX FAST"), "a run taken from before the end")
        XCTAssertTrue(e.judged("QUI"), "the start left")
        XCTAssertFalse(e.judged("+1 "), "the app's own text")
        XCTAssertTrue(e.done(""))
        let p = Erasing(old: "abc👍🏽", count: 1)
        XCTAssertEqual(p.expected, "abc")
        XCTAssertTrue(p.done("abc"))
        XCTAssertTrue(p.done("abc👍"), "a field that deletes a code point leaves a longer prefix")
        XCTAssertFalse(p.judged("abc"))
    }
}

import XCTest
@testable import TBAgentCore

/// A focused field with a real caret (issue #301): a DEL deletes the character before it, as
/// UIKit's does, and a tap leaves it where the finger landed. The down arrow moves it a line
/// down, or to the end from the last line. What the field does wrong is set per test.
private final class CaretField: TypingDevice {
    let readQueue = DispatchQueue(label: "fake main thread")
    private let lock = NSLock()
    private var _value: String
    private var _caret: Int
    private var _seq: Int64 = 0
    private var _keys: [String] = []
    let secure: Bool

    init(_ value: String, caret: Int? = nil, secure: Bool = false) {
        _value = value
        _caret = caret ?? value.count
        self.secure = secure
    }

    /// Whether the down arrow moves the caret (a field that ignores them: a custom one).
    var caretKeysWork = true
    /// DELs of the first record that the field drops (they never arrive).
    var dropDels = 0
    /// How long the field takes to show a DEL record (a read that lags behind it), ms.
    var echoDelayMs = 0
    /// What the app makes of the text after each change (a mask, a formatter).
    var mask: ((String) -> String)?
    /// After the DELs, the app keeps writing to the field every 40 ms (a ticker in it).
    var keepsChanging = false

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
        lock.lock(); defer { lock.unlock() }
        return FocusState(field: FocusedField(id: "name", value: _value, reportedLength: _value.count, secure: secure, frame: CGRect(x: 20, y: 200, width: 362, height: height)), keyboard: true)
    }
    func field(id: String, pid: Int32) throws -> FieldGeometry {
        FieldGeometry(frame: CGRect(x: 20, y: 200, width: 362, height: 34), screen: CGRect(x: 0, y: 0, width: 402, height: 874), keyboard: nil)
    }
    func tap(_ p: CGPoint, timeoutMs: Int) throws {}
    func tapElement(id: String, pid: Int32, timeoutMs: Int) throws {}
    func hideKeyboard(withinMs: Int) throws -> Bool { true }
    func selectAll(timeoutMs: Int) throws { lock.lock(); _keys.append("⌘A"); lock.unlock() }

    /// The lines on screen: each line break starts one, and a line wraps after wrapAt characters
    /// (fewer than CaretToEnd's bound takes for the field's 362 points: 22).
    var wrapAt = 30
    /// The field's height in points: by default a multi-line field's (several lines show).
    var height: CGFloat = 65
    private func lines() -> [(start: Int, length: Int)] {
        var out: [(Int, Int)] = []
        var start = 0
        for p in _value.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline) {
            var rest = p.count
            repeat { let n = min(rest, wrapAt); out.append((start, n)); start += n; rest -= n } while rest > 0
            start += 1 // the line break
        }
        return out
    }

    /// The down arrow, as UIKit's: the next line at the same column, or on the last line the end.
    func caretDown(timeoutMs: Int) throws {
        lock.lock(); defer { lock.unlock() }
        _keys.append("↓")
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
        _keys.append(text.allSatisfy({ $0 == "\u{8}" }) ? "⌫\(text.count)" : text)
        delRecords += 1
        var dels = text.filter { $0 == "\u{8}" }.count
        if delRecords == 1 { dels -= min(dropDels, dels) }
        let delay = echoDelayMs
        let changing = keepsChanging
        lock.unlock()
        let apply = { [self] in
            lock.lock()
            var chars = Array(_value)
            var c = _caret
            for _ in 0..<dels where c > 0 { chars.remove(at: c - 1); c -= 1 }
            set(String(chars), caret: c)
            lock.unlock()
            if changing { startChanging() }
        }
        if delay > 0 { DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(delay), execute: apply) } else { apply() }
    }

    private func startChanging() {
        DispatchQueue.global().async { [self] in
            for i in 0..<100 {
                Thread.sleep(forTimeInterval: 0.04)
                lock.lock(); _value = "tick \(i)"; _seq += 1; lock.unlock()
            }
        }
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

/// eraseText's postcondition on iOS (issue #301, the iOS half of #289): the field holds its old
/// text less the erased characters, counted from the end; a full erase leaves it empty; a step
/// that cannot establish that fails.
final class EraseTests: XCTestCase {
    private func deadline() -> TypingDeadline {
        let now = DispatchTime.now().uptimeNanoseconds
        return TypingDeadline(start: now, answerBy: now + 18_000 * 1_000_000, keysMs: 4000)
    }
    private func typist(_ d: CaretField) -> Typist { Typist(device: d, eraseCapMs: 100) }

    /// Issue #301: a tap left the caret mid-text and the DELs took only what was before it,
    /// leaving "BROWN FOX FAST", and the step passed. The caret goes to the end first.
    func testAFullEraseFromAMidTextCaretLeavesTheFieldEmpty() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        let repaired = try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertFalse(repaired)
        XCTAssertEqual(d.keys, ["↓", "⌫20"], "the caret to the end, then the DELs")
    }

    /// Issue #301: erasing n takes the last n characters, wherever the tap left the caret.
    func testAPartialEraseFromAMidTextCaretTakesTheEnd() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        try typist(d).erase(4, by: deadline())
        XCTAssertEqual(d.value, "QUICK BROWN FOX ")
    }

    /// Issue #301: in text of several lines the down arrow goes a line at a time, reaching the
    /// end only from the last: one goes for every line the text can take.
    func testMultiLineTextIsWalkedToItsEnd() throws {
        let d = CaretField("one\ntwo three\nfour", caret: 1)
        try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys, ["↓", "↓", "↓", "⌫18"])
    }

    /// Issue #301: a paragraph that wraps takes a line on screen for every wrap: on the device
    /// one down arrow from its first line left the caret on its second, mid-text.
    func testAWrappedParagraphIsWalkedToItsEnd() throws {
        let text = "the quick brown fox jumps over the lazy dog and runs far away from here"
        let d = CaretField(text, caret: 5)
        try typist(d).erase(1, by: deadline())
        XCTAssertEqual(d.value, String(text.dropLast()))
        XCTAssertEqual(d.keys, ["↓", "↓", "↓", "↓", "⌫1"], "71 characters at 16 points each in 362 points: 4 lines at most")
    }

    /// A field one line tall holds one line, however long its text: exactly one down arrow,
    /// which on its only line takes the caret to the end.
    func testAFieldOneLineTallGetsOneDownArrow() throws {
        let text = "THE QUICK BROWN FOX JUMPS OVER THE LAZY DOG AGAIN"
        let d = CaretField(text, caret: 5)
        d.height = 34
        d.wrapAt = 1000 // a single-line field scrolls; it does not wrap
        try typist(d).erase(1, by: deadline())
        XCTAssertEqual(d.value, String(text.dropLast()))
        XCTAssertEqual(d.keys, ["↓", "⌫1"])
        XCTAssertEqual(CaretToEnd.lines(for: "a\nb", frame: CGRect(x: 0, y: 0, width: 362, height: 34)), 2, "a line break: the estimate")
    }

    /// How many lines a text can take (CaretToEnd.lines): one a line break, and one for every
    /// width / 16 points of characters; at least one, at most 40.
    func testTheLinesATextCanTake() {
        let tall = CGRect(x: 20, y: 200, width: 362, height: 65)
        XCTAssertEqual(CaretToEnd.lines(for: "QUICK BROWN FOX FAST", frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: "", frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: nil, frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: "a\n\nb", frame: tall), 3)
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x", count: 23), frame: tall), 2)
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x", count: 23), frame: .zero), 2, "an unknown width: 320 points")
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x\n", count: 100), frame: tall), 40)
    }

    /// Issue #301: a field that ignores the down arrow keeps the tail after the DELs and after
    /// its one repair: the step fails, naming what it holds, never passes silently.
    func testAFieldThatIgnoresTheDownArrowFailsNamingWhatItHolds() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.caretKeysWork = false
        XCTAssertThrowsError(try typist(d).erase(50, by: deadline())) {
            XCTAssertTrue("\($0)".contains(#"erasing: the field holds "BROWN FOX FAST", expected """#), "\($0)")
        }
    }

    /// Issue #301: a partial erase whose DELs took text from before the end cannot be put right
    /// by DELs: it fails at once, saying so.
    func testAPartialEraseThatTookTextFromBeforeTheEndFails() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 10)
        d.caretKeysWork = false
        XCTAssertThrowsError(try typist(d).erase(4, by: deadline())) {
            XCTAssertTrue("\($0)".contains(#"erasing: the field holds "QUICK N FOX FAST", expected "QUICK BROWN FOX ""#), "\($0)")
            XCTAssertTrue("\($0)".contains("before the end"), "\($0)")
        }
        XCTAssertEqual(d.keys, ["↓", "⌫4"], "no repair DEL: it would take more")
    }

    /// Issue #301: DELs that did not all arrive leave the start: put right once, from the end,
    /// one DEL at a time, and the reply says so.
    func testDelsThatDidNotArriveAreRepairedOnce() throws {
        let d = CaretField("QUICK", caret: 2)
        d.dropDels = 2
        let repaired = try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertTrue(repaired)
        XCTAssertEqual(d.keys, ["↓", "⌫5", "↓", "⌫1", "⌫1"])
    }

    /// Issue #301 (as #289): a mask that puts its prefix back is the app's own text: no verdict,
    /// and the step passes.
    func testAMaskThatPutsItsPrefixBackGetsNoVerdict() throws {
        let d = CaretField("+1 555", caret: 4)
        d.mask = { $0.hasPrefix("+1 ") ? $0 : "+1 " }
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.value, "+1 ")
    }

    /// Issue #301 (as #297): a field still showing the DELs is waited for, never repaired.
    func testALaggingReadIsNeverRepaired() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.echoDelayMs = 60
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys, ["↓", "⌫20"])
    }

    /// Issue #301: a field that keeps changing after the DELs gets no verdict, and nothing more
    /// is sent to it.
    func testAFieldStillChangingGetsNoVerdict() throws {
        let d = CaretField("QUICK BROWN FOX FAST")
        d.keepsChanging = true
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.keys, ["↓", "⌫20"])
    }

    /// A secure field's value is bullets: no verdict.
    func testASecureFieldGetsNoVerdict() throws {
        let d = CaretField("••••••", secure: true)
        d.dropDels = 2
        XCTAssertFalse(try typist(d).erase(50, by: deadline()))
        XCTAssertEqual(d.keys, ["↓", "⌫6"])
    }

    /// What erasing judges (Erasing.judged): only what failed DELs leave.
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

import XCTest
@testable import TBAgentCore

/// A focused field with a real caret (issue #301): a DEL deletes the character before it, as
/// UIKit's does, and a tap leaves it where the finger landed. The down arrow moves it a line
/// down, or to the end from the last line. What the field does wrong is set per test.
/// Its time is a virtual clock (issues #307, #314), the Typist's too: erasing's read-back
/// judges time by it, its reads' deadlines are on it, its waits advance it instead of sleeping,
/// and what the field does late (DELs it shows late, a ticker) happens at a virtual time. A lag
/// or a deadline is then the fake's, not the runner's.
private final class CaretField: TypingDevice {
    let readQueue = DispatchQueue(label: "fake main thread")
    let clock: VirtualClock
    private let lock = NSLock()
    private var _value: String
    private var _caret: Int
    private var _seq: Int64 = 0
    private var _keys: [String] = []
    let secure: Bool

    /// Runs f (under the lock) ms from now on the virtual clock. Called under the lock.
    private func at(_ ms: Int, _ f: @escaping () -> Void) {
        clock.at(ms) { [self] in lock.lock(); f(); lock.unlock() }
    }

    init(_ value: String, caret: Int? = nil, secure: Bool = false) {
        clock = VirtualClock(holding: readQueue)
        _value = value
        _caret = caret ?? value.count
        self.secure = secure
    }

    /// Once the DELs are shown, the app stops answering accessibility: every read after them
    /// waits until answer().
    var readsHangAfterDels = false
    private var _hang: VirtualClock.Hold?
    /// The app answers again.
    func answer() { lock.lock(); let h = _hang; _hang = nil; lock.unlock(); h?.release() }

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
    /// The down arrow moves the focus to another field (an app's key command, a search
    /// field's suggestions): the DELs then go there.
    var downMovesFocus = false
    /// How long each down arrow takes to be confirmed, ms of the virtual clock.
    var downMs = 0
    private var focusedID = "name"
    /// After a down arrow, this many focus reads find no focused field (the keyboard laying out).
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

    /// Whether the field reports its selection, as UIKit's fields do through XCTest's selected
    /// text range (issue #340). Off by default: a field that reports none (the down arrows
    /// always go, as #301's tests expect).
    var reportsSelection = false
    /// How many characters the reported selection takes from the caret on (0: a caret).
    var selectionLength = 0
    /// The value the selection read sees, when the field changed since the focus look.
    var selectionValue: String?
    func selection() -> TextSelection? {
        lock.lock(); defer { lock.unlock() }
        guard reportsSelection, focusedID == "name" else { return nil }
        return TextSelection(id: focusedID, location: String(_value.prefix(_caret)).utf16.count, length: selectionLength, value: selectionValue ?? _value)
    }

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
        // Runs under the lock.
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

    /// A ticker in the field, every 40 ms of virtual time. Under the lock.
    private func startChanging() {
        for i in 0..<100 { at(40 * (i + 1)) { [self] in _value = "tick \(i)"; _seq += 1 } }
    }

    var seq: Int64 { lock.lock(); defer { lock.unlock() }; return _seq }
    /// Advances the virtual clock to the first thing due before timeoutMs (running it), or by
    /// timeoutMs: no real time passes.
    func awaitChange(since: Int64, timeoutMs: Int) -> Bool {
        clock.waitFor(ms: timeoutMs) { seq != since }
    }
    /// check, again every 5 ms of the virtual clock, up to timeoutMs of it.
    func awaitCondition(timeoutMs: Int, _ check: () -> Bool) -> Bool {
        let end = clock.now() + UInt64(max(timeoutMs, 0)) * 1_000_000
        repeat {
            if check() { return true }
            _ = awaitChange(since: seq, timeoutMs: 5) // 5 ms on, running what falls due
        } while clock.now() < end
        return false
    }
    func log(_ s: String) {}
}

/// eraseText's postcondition on iOS (issue #301, the iOS half of #289): the field holds its old
/// text less the erased characters, counted from the end; a full erase leaves it empty; a step
/// that cannot establish that fails.
final class EraseTests: XCTestCase {
    /// A deadline from the start of a field's clock (VirtualClock.start), where each test's op starts.
    private func deadline() -> TypingDeadline {
        let now = VirtualClock.start
        return TypingDeadline(start: now, answerBy: now + 18_000 * 1_000_000, keysMs: 4000)
    }
    private func typist(_ d: CaretField) -> Typist { Typist(device: d, eraseCapMs: 100, clock: d.clock) }

    /// Issue #314: erasing's reads are given up on at the op's answer time on its clock. The app
    /// stops answering once the DELs are shown: the read-back gives no verdict (nothing is
    /// repaired) and the op answers at its answer time, 18 s of the field's clock.
    func testAReadBackTheAppDoesNotAnswerEndsAtTheAnswerTime() throws {
        let d = CaretField("QUICK BROWN FOX FAST")
        d.readsHangAfterDels = true
        defer { d.answer(); d.readQueue.sync {} }
        XCTAssertFalse(try typist(d).erase(4, by: deadline()), "repaired a field it could not read")
        XCTAssertEqual(d.clock.elapsedMs, 18_000, "answered at \(d.clock.elapsedMs) ms, not at its answer time")
        XCTAssertEqual(d.keys, ["↓", "⌫4"], "keys sent after the read-back was given up on")
    }

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

    /// A field too short for two lines holds one line, however long its text: exactly one down arrow,
    /// which on its only line takes the caret to the end.
    func testAFieldOneLineTallGetsOneDownArrow() throws {
        let text = "THE QUICK BROWN FOX JUMPS OVER THE LAZY DOG AGAIN"
        let d = CaretField(text, caret: 5)
        d.height = 24
        d.wrapAt = 1000 // a single-line field scrolls; it does not wrap
        try typist(d).erase(1, by: deadline())
        XCTAssertEqual(d.value, String(text.dropLast()))
        XCTAssertEqual(d.keys, ["↓", "⌫1"])
        XCTAssertEqual(CaretToEnd.lines(for: "a\nb", frame: CGRect(x: 0, y: 0, width: 362, height: 24)), 2, "a line break: the estimate")
    }

    /// Issue #340: the common case, a field one line tall whose caret is already at the end (a
    /// tap into a field puts it there), cost one down arrow, ~350 ms on the simulator before
    /// the DELs (0.8-1.0 s an erase of 30 against 0.4-0.6 s before #301). The field reports its
    /// caret at the end: no down arrow, no focus check after it, and the op takes no time of
    /// its own on the fake's clock beyond its two reads.
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

    /// Issue #340 keeps #301: a field that reports its caret mid-text (a tap left it there)
    /// still gets the down arrow first, and is erased from the end.
    func testACaretReportedMidTextStillGoesToTheEnd() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.height = 34
        d.reportsSelection = true
        try typist(d).erase(4, by: deadline())
        XCTAssertEqual(d.value, "QUICK BROWN FOX ")
        XCTAssertEqual(d.keys, ["↓", "⌫4"])
    }

    /// Issue #340: only a caret at the end of the text the focus look read skips the down
    /// arrow: a selection, a value that moved since the look, another field's selection, or a
    /// field reading as its placeholder does not.
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
        // UTF-16 units, as the app reports them.
        XCTAssertTrue(CaretToEnd.atEnd(TextSelection(id: "name", location: 6, length: 0, value: "ab👍🏽"), of: FocusedField(id: "name", value: "ab👍🏽", reportedLength: 3)))
        XCTAssertFalse(CaretToEnd.atEnd(TextSelection(id: "name", location: 3, length: 0, value: "ab👍🏽"), of: FocusedField(id: "name", value: "ab👍🏽", reportedLength: 3)))

        // The selected text range as XCTest gives it.
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

    /// Review of #301: the down arrow did not take and the caret stayed at the start, so the
    /// DELs deleted nothing and neither did the repair's: that read as a mask keeping its text,
    /// and the step passed with the field untouched. Nothing deleted at all fails.
    func testAnUntouchedFieldFails() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 0)
        d.caretKeysWork = false
        XCTAssertThrowsError(try typist(d).erase(50, by: deadline())) {
            XCTAssertTrue("\($0)".contains(#"erasing: the field holds "QUICK BROWN FOX FAST", expected """#), "\($0)")
        }
    }

    /// Review of #301: a down arrow that moves the focus (an app's key command for it, a search
    /// field's suggestions) would send the DELs into another field, and the read-back gave no
    /// verdict for a field that lost the focus. The focus is checked before any DEL: it fails,
    /// with nothing deleted.
    func testAFocusMovedByTheDownArrowFailsBeforeAnyDEL() {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.downMovesFocus = true
        XCTAssertThrowsError(try typist(d).erase(50, by: deadline())) {
            XCTAssertTrue("\($0)".contains("the focus moved to other"), "\($0)")
            XCTAssertTrue("\($0)".contains("nothing was deleted"), "\($0)")
        }
        XCTAssertEqual(d.keys, ["↓"])
    }

    /// Review of #301: a short field can show two wrapped lines under 44 points (two body lines
    /// take ~41): its text is walked line by line, not given one down arrow.
    func testAShortFieldThatWrapsIsWalkedToItsEnd() throws {
        let text = "the quick brown fox jumps over the lazy dog and runs far away from here"
        let d = CaretField(text, caret: 5)
        d.height = 40
        try typist(d).erase(1, by: deadline())
        XCTAssertEqual(d.value, String(text.dropLast()))
        XCTAssertEqual(d.keys, ["↓", "↓", "↓", "↓", "⌫1"])
    }

    /// Review of #301: the down arrows stop at the time kept for the keys, and the op says so
    /// with nothing deleted, instead of answering after the host gave up. On the fake's clock
    /// (issue #307): each down arrow takes 100 ms of it, the keys' time ends at 350 ms, so four
    /// go (at 0, 100, 200 and 300 ms) and the op stops at 400 ms, before its answer time.
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

    /// Review of #301 (second round): a notes-style text view of 100 short lines erases. The
    /// down arrows are bounded by the time the op has, not by a fixed count (40 failed it).
    func testALongTextViewErases() throws {
        let text = String(repeating: "line\n", count: 100)
        let d = CaretField(text, caret: 0)
        try typist(d).erase(1000, by: deadline())
        XCTAssertEqual(d.value, "")
        XCTAssertEqual(d.keys.filter { $0 == "↓" }.count, 101)
    }

    /// Review of #301 (second round): the focus read as none while the keyboard lays out again
    /// after a down arrow is read again, up to a cap, before it fails the step.
    func testAFocusReadAsNoneForAMomentIsReadAgain() throws {
        let d = CaretField("QUICK BROWN FOX FAST", caret: 6)
        d.noFocusReads = 2
        try typist(d).erase(50, by: deadline())
        XCTAssertEqual(d.value, "")
    }

    /// How many lines a text can take (CaretToEnd.lines): one a line break, and one for every
    /// width / 16 points of characters; at least one.
    func testTheLinesATextCanTake() {
        let tall = CGRect(x: 20, y: 200, width: 362, height: 65)
        XCTAssertEqual(CaretToEnd.lines(for: "QUICK BROWN FOX FAST", frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: "", frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: nil, frame: tall), 1)
        XCTAssertEqual(CaretToEnd.lines(for: "a\n\nb", frame: tall), 3)
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x", count: 23), frame: tall), 2)
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x", count: 23), frame: .zero), 2, "an unknown width: 320 points")
        XCTAssertEqual(CaretToEnd.lines(for: String(repeating: "x\n", count: 100), frame: tall), 101, "not capped")
        // One down arrow without the estimate only for a field too short for two lines.
        let long = String(repeating: "x", count: 49)
        XCTAssertEqual(CaretToEnd.lines(for: long, frame: CGRect(x: 0, y: 0, width: 362, height: 34)), 3)
        XCTAssertEqual(CaretToEnd.lines(for: long, frame: CGRect(x: 0, y: 0, width: 362, height: 24)), 1)
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
    /// Issue #307: on the runner's clock this lag (60 ms against a 100 ms cap) was repaired on a
    /// loaded CI runner, whose 60 ms timer fired late; on the fake's clock it is 60 ms.
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

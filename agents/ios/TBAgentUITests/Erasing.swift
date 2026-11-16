// Erasing's postcondition (issue #301, the iOS half of #289): eraseText leaves the field holding
// its old text less its last n characters, counted from the end; a full erase leaves it empty.
// A step that cannot establish that fails; it never passes silently.
//
// The decisions are here, without XCTest (Typist.erase runs them on a TypingDevice):
// - where the caret goes before the DELs (CaretToEnd): XCTest has no selection action, and a tap
//   leaves the caret where the finger landed, often mid-text. DELs delete before the caret, so
//   the tail was left ("BROWN FOX FAST") and the step passed;
// - what a read-back judges (Erasing): only what DELs leave when they went wrong.
import CoreGraphics
import Foundation

/// How erasing puts the caret at the end of a field's text (issue #301): the down arrow, typed
/// as a hardware keyboard key, once a line the text may take. Measured on the simulator (iOS 26;
/// a SwiftUI TextField, a UITextField, a multi-line SwiftUI TextField; tb's flows tapped mid-text,
/// then erased one character):
/// - The down arrow moves the caret to the next line, and on the last line (a single-line
///   field's only one) to the end of the text. At the end it does nothing more.
/// - Control-E moves it only to the end of its line: in text that wraps, a line on screen.
///   ⌘→, ⌘↓, ⌘End, End, Page Down and ⌘A (with the simulator's hardware keyboard not connected,
///   as by default) do nothing: ⌘ shortcuts are not taken. Forward delete deletes nothing, and
///   arrows typed as text are typed as "→".
/// - One key a record: an event path with several keys, or a key and then text, plays only its
///   first key (the DELs after it were lost). So each down arrow is its own record, confirmed
///   before the next (~200 ms each).
/// XCTest reports no caret, so how many lines are below it is not known: as many down arrows go
/// as the text can take lines (lines), the ones past the end doing nothing.
/// The caret moves as the app handles the key, on its main thread, and testmanagerd confirms a
/// record once the app has received it: the DELs, sent after that confirmation, come after the
/// move in the app's event queue. Unlike Android's selection action (#289), whose caret a Compose
/// field moved a frame late, there is no second channel to race.
enum CaretToEnd {
    /// The widest a character is taken to be, in points, to bound how many lines a text wraps
    /// to: wider than the system font's letters at the default size (its "W" is ~17 pt). It
    /// undercounts wider text (CJK, emoji, a large Dynamic Type size, a field's padding): too
    /// few down arrows then leave the caret above the end, and the read-back sees what the DELs
    /// took (a run before the end): the step fails, it never passes silently.
    static let charWidth: CGFloat = 16
    /// The most down arrows one erase sends (the host's reply limit allows for them twice:
    /// eraseBudget). A text that can take more lines fails before anything is sent.
    static let maxLines = 40
    /// A field shorter than this, in points, cannot show two lines: two lines of the smallest
    /// text take more (~14 pt each). XCTest reports a SwiftUI TextField(axis: .vertical) as a text
    /// field too, so the element type cannot tell single-line from multi-line; and two body
    /// lines take only ~41 pt, so a field 34-44 pt tall may show two wrapped lines (#301 review).
    static let twoLinesHeight: CGFloat = 28

    /// The down arrows for a field holding value, at frame (zero: not known): one a line the
    /// text can take, counting each line break and how a line wraps at charWidth a character
    /// (not capped: see maxLines). A field too short for two lines, holding no line break, gets
    /// one. nil value (no field reports focus): one.
    static func lines(for value: String?, frame: CGRect) -> Int {
        guard let value else { return 1 }
        if frame.height > 0 && frame.height < twoLinesHeight && !value.contains(where: \.isNewline) { return 1 }
        let w = frame.width > 0 ? frame.width : 320
        let perLine = max(Int(w / charWidth), 1)
        let paragraphs = value.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline)
        let n = paragraphs.reduce(0) { $0 + max(($1.count + perLine - 1) / perLine, 1) }
        return max(n, 1)
    }
}

/// One erase's expectation (issue #301, as Android's in #289): old less its last count
/// characters. A DEL deletes a grapheme (an emoji with its skin tone, a letter with its accent);
/// a field that deletes code points instead leaves a longer prefix, which counts as right too.
/// Compared as Unicode scalars, so a prefix cut inside a grapheme compares exactly.
struct Erasing: Equatable {
    let old: String
    let count: Int
    /// What the field must hold: old less its last count graphemes.
    let expected: String
    private let oldScalars: [Unicode.Scalar]
    private let expectedLength: Int
    private let longestLength: Int

    init(old: String, count: Int) {
        self.old = old
        self.count = max(count, 0)
        oldScalars = Array(old.unicodeScalars)
        expected = String(old.prefix(max(old.count - self.count, 0)))
        expectedLength = expected.unicodeScalars.count
        longestLength = max(oldScalars.count - self.count, expectedLength)
    }

    private func isPrefixOfOld(_ s: [Unicode.Scalar]) -> Bool { s.count <= oldScalars.count && oldScalars.starts(with: s) }

    /// Whether value is the start of old (or all of it).
    func startOfOld(_ value: String) -> Bool { isPrefixOfOld(Array(value.unicodeScalars)) }

    /// Whether a field holding value is erased as asked.
    func done(_ value: String) -> Bool {
        let s = Array(value.unicodeScalars)
        return (expectedLength...longestLength).contains(s.count) && isPrefixOfOld(s)
    }

    /// Whether value is what DELs leave when they went wrong, the only text erasing judges and
    /// repairs (as typing judges only lost keys): text left over at the end (a prefix of old,
    /// longer than expected: DELs that did not all arrive), or old with a run taken out before
    /// its end (the DELs went in at a caret that was not at the end: "BROWN FOX FAST"). Anything
    /// else is the app's own (a mask, a formatter, a prefix or a "0" it puts back): no verdict.
    func judged(_ value: String) -> Bool {
        let v = Array(value.unicodeScalars), o = oldScalars
        if v.count > expectedLength && isPrefixOfOld(v) { return true }
        let k = o.count - v.count
        if k <= 0 { return false }
        var p = 0
        while p < v.count && o[p] == v[p] { p += 1 }
        var s = 0
        while s < v.count && o[o.count - 1 - s] == v[v.count - 1 - s] { s += 1 }
        // A caret c (k <= c < old's length): old's first c - k, then old from c.
        return max(k, o.count - s) <= min(p + k, o.count - 1)
    }

    /// Whether what is left is old's start with more at its end: what DELs from the end can
    /// still take away (a repair). Text taken from before the end cannot be put back by DELs.
    func leftoverAtEnd(_ value: String) -> Bool {
        let v = Array(value.unicodeScalars)
        return v.count > expectedLength && v.starts(with: expected.unicodeScalars)
    }
}

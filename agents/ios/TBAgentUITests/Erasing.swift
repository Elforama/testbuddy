import CoreGraphics
import Foundation

enum CaretToEnd {
    static let charWidth: CGFloat = 16
    static let twoLinesHeight: CGFloat = 28

    static func atEnd(_ sel: TextSelection?, of field: FocusedField) -> Bool {
        guard !field.secure, let sel, sel.id == field.id, sel.length == 0, let v = sel.value, v == field.value,
              field.value.count == field.reportedLength else { return false }
        return sel.location == v.utf16.count
    }

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

struct Erasing: Equatable {
    let old: String
    let count: Int
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

    func startOfOld(_ value: String) -> Bool { isPrefixOfOld(Array(value.unicodeScalars)) }

    func done(_ value: String) -> Bool {
        let s = Array(value.unicodeScalars)
        return (expectedLength...longestLength).contains(s.count) && isPrefixOfOld(s)
    }

    func judged(_ value: String) -> Bool {
        let v = Array(value.unicodeScalars), o = oldScalars
        if v.count > expectedLength && isPrefixOfOld(v) { return true }
        let k = o.count - v.count
        if k <= 0 { return false }
        var p = 0
        while p < v.count && o[p] == v[p] { p += 1 }
        var s = 0
        while s < v.count && o[o.count - 1 - s] == v[v.count - 1 - s] { s += 1 }
        return max(k, o.count - s) <= min(p + k, o.count - 1)
    }

    func leftoverAtEnd(_ value: String) -> Bool {
        let v = Array(value.unicodeScalars)
        return v.count > expectedLength && v.starts(with: expected.unicodeScalars)
    }
}

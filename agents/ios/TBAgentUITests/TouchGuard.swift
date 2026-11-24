import Foundation
#if canImport(CoreGraphics)
import CoreGraphics
#endif

enum TouchGuard {
    static func pack(_ f: CGRect) -> UInt64 {
        let q = [f.origin.x, f.origin.y, f.size.width, f.size.height].map { ($0 * 4).rounded() }
        guard q.allSatisfy({ $0.isFinite }),
              q[0] >= Double(Int16.min), q[0] <= Double(Int16.max), q[1] >= Double(Int16.min), q[1] <= Double(Int16.max),
              q[2] >= 1, q[2] <= Double(UInt16.max), q[3] >= 1, q[3] <= Double(UInt16.max) else { return 0 }
        let x = UInt64(UInt16(bitPattern: Int16(q[0]))), y = UInt64(UInt16(bitPattern: Int16(q[1])))
        return x << 48 | y << 32 | UInt64(UInt16(q[2])) << 16 | UInt64(UInt16(q[3]))
    }

    static let targetedFlag: UInt64 = 1 << 63
    static let cancelledFlag: UInt64 = 1 << 62

    static func nonce(counter: UInt32, idHash: UInt32) -> UInt64 {
        UInt64(counter & (UInt32.max >> 2)) << 32 | UInt64(idHash)
    }

    static let watchedTouch: UInt32 = 0x5ca1_ab1e

    static func watchFrame(_ ltrb: [Int]) -> CGRect? {
        guard ltrb.count == 4, ltrb[2] > ltrb[0], ltrb[3] > ltrb[1] else { return nil }
        return CGRect(x: ltrb[0], y: ltrb[1], width: ltrb[2] - ltrb[0], height: ltrb[3] - ltrb[1])
    }

    static func next(_ counter: UInt32) -> UInt32 { max((counter &+ 1) & (UInt32.max >> 2), 1) }

    enum Receipt: Equatable {
        case none
        case ended(targeted: Bool)
        case cancelled

        var word: String {
            switch self {
            case .none: return "none"
            case .ended: return "ended"
            case .cancelled: return "cancelled"
            }
        }
    }

    struct SolidMark: Equatable {
        private(set) var lastSolidSeq: Int64 = 0

        mutating func note(seq: Int64, restless: Bool) {
            if !restless { lastSolidSeq = seq }
        }

        func changed(since readSeq: Int64) -> Bool { lastSolidSeq > readSeq }
    }

    static let movedFlag: UInt64 = 1 << 63

    enum Rejection: Equatable {
        case none
        case offTarget
        case moved
    }

    static func rejection(_ value: UInt64, token: UInt64) -> Rejection {
        guard value & ~movedFlag == token else { return .none }
        return value & movedFlag != 0 ? .moved : .offTarget
    }

    struct Press: Equatable {
        let seenMs: Int, pressMs: Int, stallMs: Int

        static let stallMs = 50

        static func read(_ value: UInt64, nonce: UInt16) -> Press? {
            guard nonce != 0, UInt16(value >> 48) == nonce else { return nil }
            return Press(seenMs: Int(value >> 32 & 0xffff), pressMs: Int(value >> 16 & 0xffff), stallMs: Int(value & 0xffff))
        }

        var cutShort: Bool { pressMs > 0 && seenMs * 2 < pressMs && stallMs >= Press.stallMs }

        static func next(_ nonce: UInt16) -> UInt16 { nonce == UInt16.max ? 1 : nonce + 1 }
    }

    static func receipt(_ value: UInt64, token: UInt64) -> Receipt {
        guard value & ~(targetedFlag | cancelledFlag) == token else { return .none }
        if value & cancelledFlag != 0 { return .cancelled }
        return .ended(targeted: value & targetedFlag != 0)
    }
}

// The agent's side of the probe's touch guard (TBTouchGuard.h): the target's frame, read just
// before a guarded tap, packed into the one 64-bit notify state the probe reads at touch-down.
import Foundation
#if canImport(CoreGraphics)
import CoreGraphics
#endif

enum TouchGuard {
    /// tbPackFrame's packing: x and y signed, width and height unsigned, 16-bit counts of
    /// quarter points. 0 (no frame: the probe checks only the point) when it does not fit.
    static func pack(_ f: CGRect) -> UInt64 {
        let q = [f.origin.x, f.origin.y, f.size.width, f.size.height].map { ($0 * 4).rounded() }
        guard q.allSatisfy({ $0.isFinite }),
              q[0] >= Double(Int16.min), q[0] <= Double(Int16.max), q[1] >= Double(Int16.min), q[1] <= Double(Int16.max),
              q[2] >= 1, q[2] <= Double(UInt16.max), q[3] >= 1, q[3] <= Double(UInt16.max) else { return 0 }
        let x = UInt64(UInt16(bitPattern: Int16(q[0]))), y = UInt64(UInt16(bitPattern: Int16(q[1])))
        return x << 48 | y << 32 | UInt64(UInt16(q[2])) << 16 | UInt64(UInt16(q[3]))
    }

    /// The flags in the probe's touch receipt (TBTouchGuard.h's TBReceipt*).
    static let targetedFlag: UInt64 = 1 << 63
    static let cancelledFlag: UInt64 = 1 << 62

    /// One armed tap's nonce: a 30-bit counter above the target id's hash, clear of the flags.
    static func nonce(counter: UInt32, idHash: UInt32) -> UInt64 {
        UInt64(counter & (UInt32.max >> 2)) << 32 | UInt64(idHash)
    }

    /// The next counter for nonce (never 0).
    static func next(_ counter: UInt32) -> UInt32 { max((counter &+ 1) & (UInt32.max >> 2), 1) }

    enum Receipt: Equatable {
        case none                 // no receipt for this touch (yet): ambiguous
        case ended(targeted: Bool) // the touch ended; targeted: it began on the target's live frame
        case cancelled            // the touch ended cancelled: no tap (issue #131)

        /// The tap's `touch` field (issue #134): how the touch ended, for the host's trace.
        var word: String {
            switch self {
            case .none: return "none"
            case .ended: return "ended"
            case .cancelled: return "cancelled"
            }
        }
    }

    /// Issue #149: the change count (Events' seq) at the UI's last solid change, one that holds
    /// it unsettled (a ticker's restless changes do not count). A guarded tap says whether one
    /// came after the host's read of the tree it aims from (targetSeq) and before its touch went
    /// out: SwiftUI routes a touch to a new layout at once, while the frames tb reads show the
    /// old one for a while, so only such a tap can have landed off a target that moved.
    struct SolidMark: Equatable {
        private(set) var lastSolidSeq: Int64 = 0

        /// A change that brought the change count to seq.
        mutating func note(seq: Int64, restless: Bool) {
            if !restless { lastSolidSeq = seq }
        }

        /// Whether a solid change came after the read with this change count.
        func changed(since readSeq: Int64) -> Bool { lastSolidSeq > readSeq }
    }

    /// The flag in the probe's rejection (TBTouchGuard.h's TBRejectedMoved): the target moved.
    static let movedFlag: UInt64 = 1 << 63

    enum Rejection: Equatable {
        case none      // this touch was not withheld
        case offTarget // withheld: the point was off the target
        case moved     // withheld: the target moved since the agent read it (issue #103)
    }

    /// What the probe's rejection (its rejected state) says about the touch with this token.
    static func rejection(_ value: UInt64, token: UInt64) -> Rejection {
        guard value & ~movedFlag == token else { return .none }
        return value & movedFlag != 0 ? .moved : .offTarget
    }

    /// What the probe's receipt (its completed state) says about the touch with this token.
    static func receipt(_ value: UInt64, token: UInt64) -> Receipt {
        guard value & ~(targetedFlag | cancelledFlag) == token else { return .none }
        if value & cancelledFlag != 0 { return .cancelled }
        return .ended(targeted: value & targetedFlag != 0)
    }
}

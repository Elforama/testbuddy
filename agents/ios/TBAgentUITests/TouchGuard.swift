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
}

import Foundation
import TBTouchGuard
import XCTest

/// Issue #337: the probe's gesture trace (TBGestureTrace.h). On CI a double tap and a swipe reached
/// the app (its "touches completed" came) and had no effect; the trace records what the app made
/// of each gesture, in a ring buffer per gesture, written as one block of lines.
final class GestureTraceTests: XCTestCase {
    private func entry(_ kind: TBTraceKind, t: UInt64, id: Int32 = 1, a: UInt8 = 0, b: UInt8 = 0,
                       flags: UInt8 = 0, v: (Double, Double, Double, Double) = (0, 0, 0, 0),
                       name: String = "", extra: String = "") -> TBTraceEntry {
        var x = TBTraceEntry()
        x.t = t
        x.kind = UInt8(kind.rawValue)
        x.id = id
        x.a = a
        x.b = b
        x.flags = flags
        x.v = v
        withUnsafeMutableBytes(of: &x.name) { p in
            let n = min(name.utf8.count, p.count - 1)
            p.copyBytes(from: name.utf8.prefix(n)); p[n] = 0
        }
        withUnsafeMutableBytes(of: &x.extra) { p in
            let n = min(extra.utf8.count, p.count - 1)
            p.copyBytes(from: extra.utf8.prefix(n)); p[n] = 0
        }
        return x
    }

    private func line(_ x: TBTraceEntry, start: UInt64 = 0) -> String {
        var x = x
        var buf = [CChar](repeating: 0, count: Int(TB_TRACE_LINE))
        _ = tbTraceFormat(&x, start, &buf, buf.count)
        return String(cString: buf)
    }

    private func ring(_ cap: Int) -> (TBTraceRing, UnsafeMutablePointer<TBTraceEntry>) {
        let storage = UnsafeMutablePointer<TBTraceEntry>.allocate(capacity: cap)
        var r = TBTraceRing()
        tbTraceInit(&r, storage, UInt32(cap))
        return (r, storage)
    }

    /// Issue #337: the trace is off unless TB_GESTURE_TRACE names a file; when off the probe
    /// installs none of it.
    func testOffByDefault() {
        XCTAssertFalse(tbTraceEnabled(nil))
        XCTAssertFalse(tbTraceEnabled(""))
        XCTAssertFalse(tbTraceEnabled("0"))
        XCTAssertTrue(tbTraceEnabled("/tmp/gesture-trace.log"))
        // This test process is not launched with it.
        XCTAssertFalse(tbTraceEnabled(getenv("TB_GESTURE_TRACE")))
    }

    /// Issue #337: the buffer keeps the newest entries, oldest first, and counts what it dropped.
    func testRingKeepsTheNewestAndCountsTheDropped() {
        var (r, storage) = ring(4)
        defer { storage.deallocate() }
        for i in 0..<6 {
            var x = entry(TBTraceStall, t: UInt64(i))
            tbTracePush(&r, &x)
        }
        XCTAssertEqual(r.count, 4)
        XCTAssertEqual(r.dropped, 2)
        XCTAssertEqual((0..<4).map { tbTraceAt(&r, UInt32($0))!.pointee.t }, [2, 3, 4, 5])
        XCTAssertNil(tbTraceAt(&r, 4))
        tbTraceReset(&r)
        XCTAssertEqual(r.count, 0)
        XCTAssertEqual(r.dropped, 0)
        XCTAssertNil(tbTraceAt(&r, 0))
    }

    /// Issue #337: each kind of entry as a line, its time from the gesture's start.
    func testEntryLines() {
        let start: UInt64 = 1_000_000_000
        XCTAssertEqual(line(entry(TBTraceTouch, t: start, id: 1, a: 0, v: (120, 340.5, 1.8, 1)), start: start),
                       "     +0.0 ms  touch 1 began at (120.0,340.5) taps 1, 1.8 ms after the system's event time")
        XCTAssertEqual(line(entry(TBTraceScroll, t: start + 12_300_000, id: 2, a: UInt8(TBMomentLift.rawValue),
                                  flags: UInt8(TBScrollPaging | TBScrollDragging | TBScrollPanBegan),
                                  v: (786, 0, -912, 0), name: "UICollectionView"), start: start),
                       "    +12.3 ms  scroll #2 UICollectionView lift: offset (786.0,0.0) velocity (-912,0) paging dragging pan-began")
        XCTAssertEqual(line(entry(TBTracePageTarget, t: start + 20_000_000, id: 2, v: (393, 0, 786, 0),
                                  name: "UICollectionView"), start: start),
                       "    +20.0 ms  page target #2 UICollectionView: offset (393.0,0.0), at lift (786.0,0.0)")
        XCTAssertEqual(line(entry(TBTracePageTarget, t: start, id: 2, v: (.nan, .nan, 786, 0), name: "UIScrollView"), start: start),
                       "     +0.0 ms  page target #2 UIScrollView: unknown, at lift (786.0,0.0)")
        XCTAssertEqual(line(entry(TBTraceSees, t: start, id: 3, name: "UITapGestureRecognizer",
                                  extra: "(taps 2, on UIView, possible)"), start: start),
                       "     +0.0 ms  recognizer #3 UITapGestureRecognizer sees the touch (taps 2, on UIView, possible)")
        XCTAssertEqual(line(entry(TBTraceRecognizer, t: start + 110_000_000, id: 3, a: 0, b: 3, flags: 1,
                                  name: "UITapGestureRecognizer"), start: start),
                       "   +110.0 ms  recognizer #3 UITapGestureRecognizer: possible -> ended")
        XCTAssertEqual(line(entry(TBTraceRecognizer, t: start, id: 4, a: 0, b: 5, flags: 0, name: "X"), start: start),
                       "     +0.0 ms  recognizer #4 X: possible -> failed (not on the touch)")
        XCTAssertEqual(line(entry(TBTraceRecognizer, t: start, id: 4, a: 0, b: 5, flags: 1, name: "UITapGestureRecognizer",
                                  extra: "#2 UIScrollViewPanGestureRecognizer"), start: start),
                       "     +0.0 ms  recognizer #4 UITapGestureRecognizer: possible -> failed, after #2 UIScrollViewPanGestureRecognizer")
        XCTAssertEqual(line(entry(TBTraceStall, t: start + 40_000_000, v: (73.25, 0, 0, 0)), start: start),
                       "    +40.0 ms  main thread stalled 73.2 ms")
    }

    /// Issue #337: a gesture is written as its header and one indented line per entry; the
    /// header names what was dropped, and a buffer too small stops at a whole line.
    func testGestureBlock() {
        var (r, storage) = ring(2)
        defer { storage.deallocate() }
        for i in 0..<3 {
            var x = entry(TBTraceStall, t: UInt64(i) * 1_000_000, v: (60, 0, 0, 0))
            tbTracePush(&r, &x)
        }
        var buf = [CChar](repeating: 0, count: 4096)
        let n = tbTraceFormatGesture(7, 4242, "dev.testbuddy.bench", 1_763_330_000.25, 0, &r, &buf, buf.count)
        let text = String(cString: buf)
        XCTAssertEqual(n, text.utf8.count)
        XCTAssertEqual(text, """
            gesture 7, pid 4242 dev.testbuddy.bench, 2025-11-16 21:53:20.250 UTC, 2 entries (1 earlier dropped)
                   +1.0 ms  main thread stalled 60.0 ms
                   +2.0 ms  main thread stalled 60.0 ms

            """)
        var small = [CChar](repeating: 0, count: 160)
        let m = tbTraceFormatGesture(7, 4242, "dev.testbuddy.bench", 1_763_330_000.25, 0, &r, &small, small.count)
        let cut = String(cString: small)
        XCTAssertEqual(m, cut.utf8.count)
        XCTAssertTrue(cut.hasSuffix("\n"))
        XCTAssertEqual(cut.split(separator: "\n").count, 2) // the header and one whole line
    }
}

import TBTouchGuard
import XCTest

/// The probe's restless bookkeeping (TBTouchGuard.h TBSources), driven the way TBProbe.m drives
/// it: tbMark for every changed layer (tagged, or to be tagged, or kept for the turn), tbSendEvent
/// for touches, and tbTurnRestless at the end of each run loop turn. Times are virtual.
final class RestlessTurnTests: XCTestCase {
    /// The probe's calls, with its tag (an associated object on the layer) as a set.
    private final class Probe {
        let table = UnsafeMutablePointer<TBSource>.allocate(capacity: 4096)
        let taggedKeys = UnsafeMutablePointer<UInt>.allocate(capacity: Int(TB_TURN_MAX))
        var s: TBSources
        var tags: Set<UInt> = []
        var turn: [(key: UInt, band: UInt)] = []
        var now: UInt64 = 1_000_000_000

        init(animationSpeed: Double = 0.1) {
            table.initialize(repeating: TBSource(), count: 4096)
            s = TBSources(table: table, size: 4096, framesRestlessNs: TB_FRAMES_RESTLESS_AFTER_NS,
                          tagged: taggedKeys, taggedCount: 0, touchEnded: false)
            // tbReadSpeed: tb launches apps with animations 10x faster.
            s.framesRestlessNs = UInt64(max(Double(TB_RESTLESS_AFTER_NS), Double(TB_FRAMES_RESTLESS_AFTER_NS) * animationSpeed))
        }
        deinit { table.deallocate(); taggedKeys.deallocate() }

        /// tbMark: layer `key`, in `band`, changed.
        func mark(_ key: UInt, band: UInt) {
            if tags.contains(key) { tbSrcNoteTagged(&s, key); return }
            if tbSrcTakeVerdict(&s, key) { tags.insert(key); tbSrcNoteTagged(&s, key); return }
            if !turn.contains(where: { $0.key == key }) { turn.append((key, band)) }
        }

        /// tbSendEvent: a touch event in this turn.
        func touch(ended: Bool, delivered: Bool = true) { tbSrcNoteTouch(&s, ended, delivered) }

        /// tbTurnRestless at `ms` past the clock's last turn: whether the turn went out restless.
        @discardableResult
        func endTurn(after ms: Double) -> Bool {
            now += UInt64(ms * 1_000_000)
            let keys = turn.map(\.key), bands = turn.map(\.band)
            turn = []
            return tbSrcEndTurn(&s, Int32(keys.count), keys, bands, false, false, now)
        }

        /// A layer changing every `every` ms for `forMs` ms, in turns of its own.
        func run(_ key: UInt, band: UInt, every: Double, forMs: Double) {
            var t = 0.0
            while t < forMs { mark(key, band: band); endTurn(after: every); t += every }
        }
    }

    private let counter: UInt = 0x1000, counterBand: UInt = 0x1001

    /// Issue #309, as traced on the bench's "Tap, then away" screen: as the device turns back to
    /// portrait, the counter's text layer changes in four turns, 63, 148 and 352 ms apart, each
    /// with 28 to 49 other layers (the screen laid out again). That is a run of over 500 ms with
    /// no gap of 400 ms: restless, and the layer is tagged on its next change, the tap's new
    /// count, 381 to 441 ms later and the only change of the touch's turn. It went out restless,
    /// and the tap waited out its 1 s no-change window although it had worked.
    private func rotatedCounter(_ p: Probe) {
        let screen: [UInt] = (1...30).map { 0x9000_0000 + $0 * 0x100 }
        for gap in [0.0, 63, 148, 352] {
            if gap > 0 { p.endTurn(after: gap) }
            p.mark(counter, band: counterBand)
            for k in screen { p.mark(k, band: k | 1) }
        }
        p.endTurn(after: 1)
    }

    func testTheTapsNewCountAfterTheRotationIsAChange() {
        for still in [381.0, 441] {
            let p = Probe()
            rotatedCounter(p)
            p.touch(ended: true)
            p.mark(counter, band: counterBand)
            XCTAssertTrue(p.tags.contains(counter), "the rotation did not make the counter restless")
            XCTAssertFalse(p.endTurn(after: still - 1), "the tap's change went out restless (issue #309), \(still) ms")
        }
    }

    /// The touch was withheld (off its target, or the target moved): it never reached the app,
    /// so the turn's change is not its own.
    func testAWithheldTouchIsNotCounted() {
        let p = Probe()
        rotatedCounter(p)
        p.touch(ended: true, delivered: false)
        p.mark(counter, band: counterBand)
        XCTAssertTrue(p.endTurn(after: 381), "a withheld touch made the turn a change")
    }

    /// SwiftUI replaces a TimelineView progress bar's layers every few frames: the band is
    /// restless, each new layer is a key never seen. One that comes in the tap's turn is the bar's.
    func testANewLayerInARestlessBandDuringATapIsRestless() {
        let p = Probe()
        let bar: UInt = 0x2001
        var key: UInt = 0x2000_0000
        for frame in 0..<90 { // 1.5 s at 60 fps, a new layer every 3 frames
            if frame % 3 == 0 { key += 0x100 }
            p.mark(key, band: bar)
            p.endTurn(after: 16.7)
        }
        p.touch(ended: true)
        p.mark(key + 0x100, band: bar)
        XCTAssertTrue(p.endTurn(after: 16.7), "the bar's new layer made the tap's turn a change")
    }

    /// A 100 ms ticker, tagged, ticks in a turn of a touch moving, then in the touch's end.
    func testATickerTickingDuringATouchIsRestless() {
        let p = Probe()
        let ticker: UInt = 0x3000
        p.run(ticker, band: 0x3001, every: 100, forMs: 1500)
        XCTAssertTrue(p.tags.contains(ticker))
        p.touch(ended: false)
        p.mark(ticker, band: 0x3001)
        XCTAssertTrue(p.endTurn(after: 100), "a tick in a touch-moved turn was a change")
        p.touch(ended: true)
        p.mark(ticker, band: 0x3001)
        XCTAssertTrue(p.endTurn(after: 100), "a tick in a touch-ended turn was a change")
    }

    /// A clock whose label was tagged in a burst (as #309's counter was) then ticks once a
    /// second; a tick comes in the turn of a touch's end.
    func testAOneSecondClockTickingAsATouchEndsIsRestless() {
        let p = Probe()
        let clock: UInt = 0x4000
        p.run(clock, band: 0x4001, every: 17, forMs: 700)
        XCTAssertTrue(p.tags.contains(clock))
        p.run(clock, band: 0x4001, every: 1000, forMs: 6000)
        p.touch(ended: true)
        p.mark(clock, band: 0x4001)
        XCTAssertTrue(p.endTurn(after: 1000), "the clock's tick was a change")
    }

    /// An endless animation drawn frame by frame, with an 80 ms hitch of the main thread just
    /// before a touch's end.
    func testAnEndlessAnimationWithAHitchIsRestless() {
        let p = Probe()
        let spinner: UInt = 0x5000
        p.run(spinner, band: 0x5001, every: 16.7, forMs: 1500)
        XCTAssertTrue(p.tags.contains(spinner))
        p.touch(ended: true)
        p.mark(spinner, band: 0x5001)
        XCTAssertTrue(p.endTurn(after: 80), "an 80 ms hitch made the animation's frame a change")
    }

    /// A layer that is not restless is a change, touch or not.
    func testALayerThatIsNotRestlessIsAChange() {
        let p = Probe()
        p.mark(0x6000, band: 0x6001)
        XCTAssertFalse(p.endTurn(after: 500))
    }
}

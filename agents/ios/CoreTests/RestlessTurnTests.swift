import TBTouchGuard
import XCTest

final class RestlessTurnTests: XCTestCase {
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
            s.framesRestlessNs = UInt64(max(Double(TB_RESTLESS_AFTER_NS), Double(TB_FRAMES_RESTLESS_AFTER_NS) * animationSpeed))
        }
        deinit { table.deallocate(); taggedKeys.deallocate() }

        func mark(_ key: UInt, band: UInt) {
            if tags.contains(key) { tbSrcNoteTagged(&s, key); return }
            if tbSrcTakeVerdict(&s, key) { tags.insert(key); tbSrcNoteTagged(&s, key); return }
            if !turn.contains(where: { $0.key == key }) { turn.append((key, band)) }
        }

        func touch(ended: Bool, delivered: Bool = true) { tbSrcNoteTouch(&s, ended, delivered) }

        @discardableResult
        func endTurn(after ms: Double) -> Bool {
            now += UInt64(ms * 1_000_000)
            let keys = turn.map(\.key), bands = turn.map(\.band)
            turn = []
            return tbSrcEndTurn(&s, Int32(keys.count), keys, bands, false, false, now)
        }

        func run(_ key: UInt, band: UInt, every: Double, forMs: Double) {
            var t = 0.0
            while t < forMs { mark(key, band: band); endTurn(after: every); t += every }
        }
    }

    private let counter: UInt = 0x1000, counterBand: UInt = 0x1001

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

    func testRepeatedTapsAfterTheRotationAreChanges() {
        let p = Probe()
        rotatedCounter(p)
        for (n, gap) in [380.0, 250, 600].enumerated() {
            p.touch(ended: true)
            p.mark(counter, band: counterBand)
            XCTAssertFalse(p.endTurn(after: n == 0 ? gap - 1 : gap), "tap \(n + 1) went out restless")
        }
    }

    func testRowsTickingTogetherAsATouchEndsAreRestless() {
        let p = Probe()
        let rows: [UInt] = (1...20).map { 0x7000_0000 + $0 * 0x100 }
        for _ in 0..<12 {
            for k in rows { p.mark(k, band: k | 1) }
            p.endTurn(after: 250)
        }
        XCTAssertTrue(rows.allSatisfy { p.tags.contains($0) })
        p.touch(ended: true)
        for k in rows { p.mark(k, band: k | 1) }
        XCTAssertTrue(p.endTurn(after: 250), "the rows' tick was a change")
    }

    func testRowsAndAReplacedSparklineTickingAsATouchEndsAreRestless() {
        let p = Probe()
        let rows: [UInt] = (1...20).map { 0x7000_0000 + $0 * 0x100 }
        let spark: UInt = 0x7100_0001
        var line: UInt = 0x7200_0000
        func tick() {
            line += 0x100
            for k in rows { p.mark(k, band: k | 1) }
            p.mark(line, band: spark)
        }
        for _ in 0..<12 { tick(); p.endTurn(after: 250) }
        XCTAssertTrue(rows.allSatisfy { p.tags.contains($0) })
        p.touch(ended: true)
        tick()
        XCTAssertTrue(p.endTurn(after: 250), "the rows' tick was a change")
    }

    func testAWithheldTouchIsNotCounted() {
        let p = Probe()
        rotatedCounter(p)
        p.touch(ended: true, delivered: false)
        p.mark(counter, band: counterBand)
        XCTAssertTrue(p.endTurn(after: 381), "a withheld touch made the turn a change")
    }

    func testANewLayerInARestlessBandDuringATapIsRestless() {
        let p = Probe()
        let bar: UInt = 0x2001
        var key: UInt = 0x2000_0000
        for frame in 0..<90 {
            if frame % 3 == 0 { key += 0x100 }
            p.mark(key, band: bar)
            p.endTurn(after: 16.7)
        }
        p.touch(ended: true)
        p.mark(key + 0x100, band: bar)
        XCTAssertTrue(p.endTurn(after: 16.7), "the bar's new layer made the tap's turn a change")
    }

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

    func testAnEndlessAnimationWithAHitchIsRestless() {
        let p = Probe()
        let spinner: UInt = 0x5000
        p.run(spinner, band: 0x5001, every: 16.7, forMs: 1500)
        XCTAssertTrue(p.tags.contains(spinner))
        p.touch(ended: true)
        p.mark(spinner, band: 0x5001)
        XCTAssertTrue(p.endTurn(after: 80), "an 80 ms hitch made the animation's frame a change")
    }

    func testALayerThatIsNotRestlessIsAChange() {
        let p = Probe()
        p.mark(0x6000, band: 0x6001)
        XCTAssertFalse(p.endTurn(after: 500))
    }
}

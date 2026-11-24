import XCTest
@testable import TBAgentCore

final class PerPidTests: XCTestCase {
    private final class Procs {
        var start: [Int32: UInt64] = [:]
        func of(_ pid: Int32) -> UInt64? { start[pid] }
    }

    func testANewProcessWithAnOldPidInheritsNothing() {
        let procs = Procs()
        procs.start[4242] = 100
        procs.start[4343] = 200
        let probed = PerPid<UInt32>(startOf: procs.of), bundles = PerPid<String>(startOf: procs.of)
        probed.update(4242) { $0 = 3 }
        bundles.update(4242) { $0 = "dev.testbuddy.bench" }
        probed.update(4343) { $0 = 1 }

        procs.start[4242] = 900
        XCTAssertNil(probed[4242], "the new process counts as probed")
        XCTAssertNil(bundles[4242], "the new process gets the old one's bundle id")
        XCTAssertEqual(probed.update(4242) { $0 == nil }, true, "the probe's first word from the new process is a first")
        XCTAssertEqual(probed[4343], 1, "another process keeps its own")
        XCTAssertEqual(probed.pids, [4343])
    }

    func testAnEndedProcessIsLetGoOf() {
        let procs = Procs()
        procs.start[4444] = 100
        let busy = PerPid<UInt64>(startOf: procs.of)
        busy.update(4444) { $0 = 5 }
        XCTAssertEqual(busy[4444], 5, "kept while it runs")
        procs.start[4444] = nil
        XCTAssertNil(busy[4444])
    }

    func testTheBackgroundKeepsEndedAppsButNotTheirPids() {
        let procs = Procs()
        procs.start[4545] = 100
        let background = PerPid<Bool>(keepEnded: true, startOf: procs.of)
        background.update(4545) { $0 = true }
        procs.start[4545] = nil
        XCTAssertEqual(background[4545], true, "an ended app stays out of the front")
        procs.start[4545] = 900
        XCTAssertNil(background[4545], "a new process with its pid is not in the background")

        let reused = Front.App(4545, "com.apple.mobilesafari"), other = Front.App(600, "com.example.other")
        let pick = Front.pick(active: [reused, other], background: Set(background.pids), springboardPid: 40) { $0.first }
        XCTAssertEqual(pick, reused)
    }

    func testTheLastAppSeenIsThatProcess() {
        let procs = Procs()
        procs.start[4646] = 100
        let last = SeenProcess(pid: 4646, bundle: "dev.testbuddy.bench", startOf: procs.of)
        XCTAssertTrue(last.isCurrent(startOf: procs.of))
        procs.start[4646] = 900
        XCTAssertFalse(last.isCurrent(startOf: procs.of), "a new process took its pid")
        procs.start[4646] = nil
        XCTAssertFalse(last.isCurrent(startOf: procs.of), "it ended")
    }

    func testWithoutStartTimesEverythingIsKept() {
        let probed = PerPid<UInt32>(startOf: { _ in nil })
        probed.update(4747) { $0 = 1 }
        XCTAssertEqual(probed[4747], 1)
        XCTAssertTrue(SeenProcess(pid: 4747, bundle: "a", startOf: { _ in nil }).isCurrent(startOf: { _ in nil }))
    }

    func testProcessStartLooksUpProcesses() {
        XCTAssertNotNil(ProcessStart.of(getpid()))
        XCTAssertEqual(ProcessStart.of(getpid()), ProcessStart.of(getpid()))
        XCTAssertNil(ProcessStart.of(0))
        XCTAssertNil(ProcessStart.of(Int32.max))
    }

    func testAFailedRegistrationIsTriedAgain() {
        var reg = PidRegistration()
        var tries = 0
        XCTAssertFalse(reg.ensure(5000) { tries += 1; return false })
        XCTAssertTrue(reg.ensure(5000) { tries += 1; return true }, "the next tap registers again")
        XCTAssertEqual(tries, 2)
        XCTAssertTrue(reg.ensure(5000) { tries += 1; return true })
        XCTAssertEqual(tries, 2, "registered once it worked")
        XCTAssertTrue(reg.ensure(5001) { tries += 1; return true })
        XCTAssertEqual(tries, 3, "a new process registers")
    }
}

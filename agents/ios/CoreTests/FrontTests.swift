import XCTest
@testable import TBAgentCore

final class FrontTests: XCTestCase {
    private let sb = Front.App(40, Front.springboard)
    private let bench = Front.App(40169, "dev.testbuddy.bench")
    private let safari = Front.App(500, "com.apple.mobilesafari")
    private func noState(_: [Front.App]) -> Front.App? { nil }

    private func readNow(active: [Front.App], last: Front.App?, background: Set<Int32> = []) -> Front.Read {
        let front = Front.pick(active: active, background: background, springboardPid: sb.pid, isForeground: noState)
        return Front.read(front: front, last: last) { XCTFail("asked again although the query answered"); return nil }
    }

    func testAFailedQueryReadsTheLastAppNotSpringBoard() {
        var asked = 0
        XCTAssertEqual(Front.read(front: nil, last: bench, ci: true) { asked += 1; return nil }, .lastApp(bench))
        XCTAssertEqual(asked, 0, "the last app in front is read at once")
    }

    func testAFailedQueryWithNoAppKnownAsksAgain() {
        var answers: [Front.App?] = [nil, sb]
        XCTAssertEqual(Front.read(front: nil, last: nil, ci: true) { answers.removeFirst() }, .springboard(pid: sb.pid))
        answers = [Front.App(0, ""), bench]
        XCTAssertEqual(Front.read(front: nil, last: nil, ci: true) { answers.removeFirst() }, .app(bench))
    }

    func testAQueryThatKeepsFailingReadsSpringBoard() {
        var asked = 0
        XCTAssertEqual(Front.read(front: nil, last: nil, ci: true) { asked += 1; return nil }, .springboard(pid: 0))
        XCTAssertEqual(asked, Front.requeries)
    }

    func testALaunchWaitIsToldItIsNotKnown() {
        var asked = 0
        XCTAssertEqual(Front.read(front: nil, last: nil, launching: true, ci: true) { asked += 1; return nil }, .unknown)
        XCTAssertEqual(asked, 0)
    }

    func testOutsideCIAFailedQueryReadsSpringBoardAsBefore() {
        var asked = 0
        XCTAssertEqual(Front.read(front: nil, last: bench, ci: false) { asked += 1; return nil }, .springboard(pid: 0))
        XCTAssertEqual(Front.read(front: nil, last: nil, ci: false) { asked += 1; return nil }, .springboard(pid: 0))
        XCTAssertEqual(Front.read(front: nil, last: nil, launching: true, ci: false) { asked += 1; return nil }, .springboard(pid: 0))
        XCTAssertEqual(asked, 0, "asked again outside CI mode")
        XCTAssertEqual(Front.read(front: bench, last: nil, ci: false) { asked += 1; return nil }, .app(bench))
    }

    func testAFailedQueryIsNotAnApp() {
        XCTAssertNil(Front.pick(active: [], background: [], springboardPid: sb.pid, isForeground: noState))
    }

    func testSpringBoardAloneIsRead() {
        XCTAssertEqual(readNow(active: [sb], last: bench), .springboard(pid: sb.pid))
        XCTAssertEqual(readNow(active: [Front.App(sb.pid, "")], last: bench), .springboard(pid: sb.pid))
    }

    func testTheAppInFrontIsRead() {
        XCTAssertEqual(readNow(active: [bench, sb], last: nil), .app(bench))
        XCTAssertEqual(readNow(active: [sb, safari], last: bench), .app(safari))
    }

    func testBackgroundAppsAreDroppedAndStateDecides() {
        XCTAssertEqual(readNow(active: [bench, safari, sb], last: nil, background: [bench.pid]), .app(safari))
        let front = Front.pick(active: [bench, safari, sb], background: [], springboardPid: sb.pid,
                               isForeground: { apps in apps.first { $0 == self.safari } })
        XCTAssertEqual(front, safari)
        XCTAssertEqual(Front.pick(active: [bench, safari], background: [], springboardPid: sb.pid, isForeground: noState), bench)
    }
}

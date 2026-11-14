import XCTest
@testable import TBAgentCore

/// Which app is in front, and which app a screen read reads (Front).
final class FrontTests: XCTestCase {
    private let sb = Front.App(40, Front.springboard)
    private let bench = Front.App(40169, "dev.testbuddy.bench")
    private let safari = Front.App(500, "com.apple.mobilesafari")
    private func noState(_: [Front.App]) -> Front.App? { nil }

    private func readNow(active: [Front.App], last: Front.App?, background: Set<Int32> = []) -> Front.Read {
        let front = Front.pick(active: active, background: background, springboardPid: sb.pid, isForeground: noState)
        return Front.read(front: front, last: last) { XCTFail("asked again although the query answered"); return nil }
    }

    // Issue #142: on a starved CI simulator the accessibility client's active applications
    // timed out (kAXErrorIPCTimeout) and came back empty. The agent took that for SpringBoard
    // alone in front and read SpringBoard's tree, its app switcher with the app as a card, while
    // the app was still in front: an assertion waited 28.6 s on it. A failed query reads the last
    // app seen in front.
    func testAFailedQueryReadsTheLastAppNotSpringBoard() {
        var asked = 0
        XCTAssertEqual(Front.read(front: nil, last: bench) { asked += 1; return nil }, .lastApp(bench))
        XCTAssertEqual(asked, 0, "the last app in front is read at once")
    }

    // Review of #142: with no app known in front (lastApp is nil after pressKey home or
    // stopApp, when SpringBoard really is in front), a failed query must not fail a read that
    // works. It asks again, and reads what that finds...
    func testAFailedQueryWithNoAppKnownAsksAgain() {
        var answers: [Front.App?] = [nil, sb]
        XCTAssertEqual(Front.read(front: nil, last: nil) { answers.removeFirst() }, .springboard(pid: sb.pid))
        answers = [Front.App(0, ""), bench]
        XCTAssertEqual(Front.read(front: nil, last: nil) { answers.removeFirst() }, .app(bench))
    }

    // ...and, still not known, reads SpringBoard as before.
    func testAQueryThatKeepsFailingReadsSpringBoard() {
        var asked = 0
        XCTAssertEqual(Front.read(front: nil, last: nil) { asked += 1; return nil }, .springboard(pid: 0))
        XCTAssertEqual(asked, Front.requeries)
    }

    // A launch's first-tree wait asks again itself: the read says only that it is not known.
    func testALaunchWaitIsToldItIsNotKnown() {
        var asked = 0
        XCTAssertEqual(Front.read(front: nil, last: nil, launching: true) { asked += 1; return nil }, .unknown)
        XCTAssertEqual(asked, 0)
    }

    func testAFailedQueryIsNotAnApp() {
        XCTAssertNil(Front.pick(active: [], background: [], springboardPid: sb.pid, isForeground: noState))
    }

    // What still reads SpringBoard: SpringBoard alone listed (its home screen, or one of its
    // alerts, a permission prompt, with no app listed with it), also when its bundle id could
    // not be looked up.
    func testSpringBoardAloneIsRead() {
        XCTAssertEqual(readNow(active: [sb], last: bench), .springboard(pid: sb.pid))
        XCTAssertEqual(readNow(active: [Front.App(sb.pid, "")], last: bench), .springboard(pid: sb.pid))
    }

    func testTheAppInFrontIsRead() {
        XCTAssertEqual(readNow(active: [bench, sb], last: nil), .app(bench))
        XCTAssertEqual(readNow(active: [sb, safari], last: bench), .app(safari))
    }

    // An app SpringBoard reported in the background is dropped while another is listed; XCTest's
    // state decides between several.
    func testBackgroundAppsAreDroppedAndStateDecides() {
        XCTAssertEqual(readNow(active: [bench, safari, sb], last: nil, background: [bench.pid]), .app(safari))
        let front = Front.pick(active: [bench, safari, sb], background: [], springboardPid: sb.pid,
                               isForeground: { apps in apps.first { $0 == self.safari } })
        XCTAssertEqual(front, safari)
        XCTAssertEqual(Front.pick(active: [bench, safari], background: [], springboardPid: sb.pid, isForeground: noState), bench)
    }
}

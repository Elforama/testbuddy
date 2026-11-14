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
        return Front.read(front: front, last: last)
    }

    // Issue #142: on a starved CI simulator the accessibility client's active applications
    // timed out (kAXErrorIPCTimeout) and came back empty. The agent took that for SpringBoard
    // alone in front and read SpringBoard's tree, its app switcher with the app as a card, while
    // the app was still in front: an assertion waited 28.6 s on it. A failed query reads the last
    // app seen in front.
    func testAFailedQueryReadsTheLastAppNotSpringBoard() {
        XCTAssertEqual(readNow(active: [], last: bench), .lastApp(bench))
    }

    // ...and, with no app seen in front yet, reads nothing rather than SpringBoard.
    func testAFailedQueryWithNoAppKnownReadsNothing() {
        XCTAssertEqual(readNow(active: [], last: nil), .unknown)
        XCTAssertEqual(Front.read(front: Front.App(0, ""), last: nil), .unknown)
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

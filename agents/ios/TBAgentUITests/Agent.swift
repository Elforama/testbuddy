// Test Buddy's iOS agent: an XCUITest runner whose single test serves docs/protocol.md over
// TCP on 127.0.0.1:<TB_PORT> until told to exit or left idle (TB_IDLE_S, default 30 min).
// Only an XCTest runner may drive other apps on iOS. A simulator shares the Mac's loopback,
// so no forwarding is needed. Starting it (xcodebuild test-without-building) takes seconds,
// so the host starts it in its own session and later runs reuse it (op info reports TB_BUILD).
//
// Environment (xcodebuild passes TEST_RUNNER_<NAME> to the runner as <NAME>):
//   TB_PORT   port to listen on (default 22087)
//   TB_IDLE_S seconds without a client before exiting (default 1800)
//   TB_BUILD  the build id the host gave it (op info), so a stale runner can be replaced
import XCTest

final class TBAgentUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = true
    }

    func testServe() throws {
        let env = ProcessInfo.processInfo.environment
        func value(_ k: String) -> String? { env[k] ?? env["TEST_RUNNER_" + k] }
        let port = UInt16(value("TB_PORT") ?? "") ?? 22087
        let idle = Double(value("TB_IDLE_S") ?? "") ?? 1800
        Agent.shared.build = value("TB_BUILD") ?? ""
        Agent.shared.udid = env["SIMULATOR_UDID"] ?? ""
        try Agent.shared.run(port: port, idleSeconds: idle)
    }

    /// An issue XCTest records inside its own calls (an accessibility timeout, "Failed to get
    /// matching snapshot") would end the test, and the agent with it. Each op fails on its own
    /// instead: issues are logged, never recorded.
    override func record(_ issue: XCTIssue) {
        NSLog("tb: XCTest issue (not recorded): \(issue.compactDescription)")
    }
}

final class Agent {
    static let shared = Agent()
    static let version = "0.1.0"

    var build = ""
    var udid = ""
    private(set) var stopped = false
    private var server: Server?

    func run(port: UInt16, idleSeconds: Double) throws {
        let ax = Private.setAXTimeout(Tree.axTimeoutSeconds)
        NSLog("tb: accessibility timeout \(ax ? "\(Tree.axTimeoutSeconds) s" : "unchanged")")
        Events.shared.install()
        let reg = Events.shared.register(Events.defaultRegister)
        NSLog("tb: registered \(reg.ok.count) AX notifications, \(reg.failed.count) refused")
        let s = Server(port: port, idleSeconds: idleSeconds, ops: Ops())
        try s.start()
        server = s
        NSLog("tb: listening on 127.0.0.1:\(port), build \(build)")
        // XCTest's APIs belong on the main thread: ops hop here, so keep its run loop serving.
        // A distant timer keeps the loop from returning at once when nothing else is scheduled.
        let keep = Timer(timeInterval: 3600, repeats: true) { _ in }
        RunLoop.main.add(keep, forMode: .default)
        while !stopped {
            RunLoop.main.run(mode: .default, before: .distantFuture)
        }
        keep.invalidate()
        NSLog("tb: stopped")
    }

    /// Ends the test (and so the runner). Safe from any thread.
    func stop() {
        DispatchQueue.main.async {
            self.stopped = true
            CFRunLoopStop(CFRunLoopGetMain())
        }
        // If XCTest does not wind down promptly, leave anyway.
        DispatchQueue.global().asyncAfter(deadline: .now() + 5) { exit(0) }
    }
}

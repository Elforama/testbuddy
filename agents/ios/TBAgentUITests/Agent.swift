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

    override func record(_ issue: XCTIssue) {
        let text = issue.compactDescription
        let noted = Ops.issues.note(text)
        let fails = noted.kind != .noise && !noted.stale
        NSLog("tb: XCTest issue (not recorded, \(noted.stale ? "an earlier op's call" : fails ? "fails the op" : "noise")): \(text)")
        Events.shared.traceIssue(text, fatal: fails)
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
        let keep = Timer(timeInterval: 3600, repeats: true) { _ in }
        RunLoop.main.add(keep, forMode: .default)
        while !stopped {
            RunLoop.main.run(mode: .default, before: .distantFuture)
        }
        keep.invalidate()
        NSLog("tb: stopped")
    }

    func stop() {
        DispatchQueue.main.async {
            self.stopped = true
            CFRunLoopStop(CFRunLoopGetMain())
        }
        DispatchQueue.global().asyncAfter(deadline: .now() + 5) { exit(0) }
    }
}

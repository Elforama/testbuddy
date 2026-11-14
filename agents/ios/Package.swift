// swift-tools-version:5.9
// The iOS agent's code that runs without XCTest or a simulator (its JSON in and out, gesture
// timing, request replay, the probe's touch guard, which launch errors to wait on, how typing
// focuses a field and bounds its waits, which requests are answered at once, which connection is
// the client, what is kept per process), built as a package so its unit tests run with
// `swift test` (CI's iOS agent job). The agent itself is TBAgent.xcodeproj (build.sh).
import PackageDescription

let package = Package(
    name: "TBAgentCore",
    platforms: [.macOS(.v13)],
    targets: [
        .target(name: "TBAgentCore", path: "TBAgentUITests", sources: ["Json.swift", "Gestures.swift", "Replies.swift", "TouchGuard.swift", "LaunchWait.swift", "Typing.swift", "Quiet.swift", "Liveness.swift", "AppWait.swift", "Clients.swift", "Front.swift", "PerPid.swift"]),
        // The probe (TBProbe.m) includes its header; the probe itself needs UIKit.
        .target(name: "TBTouchGuard", path: "TBTouchGuard"),
        .testTarget(name: "TBAgentCoreTests", dependencies: ["TBAgentCore", "TBTouchGuard"], path: "CoreTests"),
    ]
)

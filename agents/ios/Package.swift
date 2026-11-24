// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "TBAgentCore",
    platforms: [.macOS(.v13)],
    targets: [
        .target(name: "TBAgentCore", path: "TBAgentUITests", sources: ["Json.swift", "Gestures.swift", "Replies.swift", "TouchGuard.swift", "LaunchWait.swift", "Typing.swift", "Quiet.swift", "Liveness.swift", "AppWait.swift", "Clients.swift", "Front.swift", "PerPid.swift", "Preflight.swift", "InputGate.swift", "InputReady.swift", "Issues.swift", "Erasing.swift"]),
        .target(name: "TBTouchGuard", path: "TBTouchGuard"),
        .testTarget(name: "TBAgentCoreTests", dependencies: ["TBAgentCore", "TBTouchGuard"], path: "CoreTests"),
    ]
)

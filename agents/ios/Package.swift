// swift-tools-version:5.9
// The iOS agent's code that runs without XCTest or a simulator (its JSON in and out, gesture
// timing, request replay), built as a package so its unit tests run with `swift test` (CI's iOS
// agent job). The agent itself is TBAgent.xcodeproj (build.sh).
import PackageDescription

let package = Package(
    name: "TBAgentCore",
    platforms: [.macOS(.v13)],
    targets: [
        .target(name: "TBAgentCore", path: "TBAgentUITests", sources: ["Json.swift", "Gestures.swift", "Replies.swift"]),
        .testTarget(name: "TBAgentCoreTests", dependencies: ["TBAgentCore"], path: "CoreTests"),
    ]
)

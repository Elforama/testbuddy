import Foundation

enum AppPhase: UInt64 {
    case unknown = 0
    case active = 1
    case entering = 2
    case inactive = 3
    case background = 4

    init(state: UInt64) { self = AppPhase(rawValue: state) ?? .unknown }

    var holdsInput: Bool { self == .entering || self == .background }
}

enum InputReady {
    static let capMs = 3000

    enum End: String, Equatable {
        case none
        case active
        case leftFront = "left the front"
        case timedOut = "timed out"
    }

    struct Outcome: Equatable {
        let waitedMs: Int
        let ready: Bool
        let from: AppPhase
        let end: End
    }

    static func await(phase: @escaping () -> AppPhase, inFront: @escaping () -> Bool, leftMs: Int?,
                      now: () -> UInt64, wait: (Int, () -> Bool) -> Bool) -> Outcome {
        let first = phase()
        guard first.holdsInput, inFront() else { return Outcome(waitedMs: 0, ready: true, from: first, end: .none) }
        let limit = min(capMs, max(leftMs ?? capMs, 0))
        let t0 = now()
        var end = End.timedOut
        let ready = limit > 0 && wait(limit) {
            if !phase().holdsInput { end = .active; return true }
            if !inFront() { end = .leftFront; return true }
            return false
        }
        let waited = Int((now() &- t0) / 1_000_000)
        return Outcome(waitedMs: waited, ready: ready, from: first, end: ready ? end : .timedOut)
    }
}

final class LaunchHold {
    private let start: UInt64
    private let limitMs: Int
    private var heldSince: UInt64?
    private var endedAt: UInt64?
    private(set) var timedOut = false

    init(now: UInt64, timeoutMs: Int) {
        start = now
        limitMs = min(InputReady.capMs, max(timeoutMs, 0))
    }

    func holds(phase: AppPhase, inFront: Bool, now: UInt64) -> Bool {
        let within = now &- start < UInt64(limitMs) * 1_000_000
        if phase.holdsInput && inFront && within && endedAt == nil {
            if heldSince == nil { heldSince = now }
            return true
        }
        if heldSince != nil && endedAt == nil {
            endedAt = now
            timedOut = phase.holdsInput && inFront
        }
        return false
    }

    func recheckMs(now: UInt64) -> Int {
        guard heldSince != nil, endedAt == nil else { return 0 }
        let end = start + UInt64(limitMs) * 1_000_000
        return now < end ? max(1, Int((end - now) / 1_000_000)) : 1
    }

    func heldMs(now: UInt64) -> Int? {
        guard let since = heldSince else { return nil }
        return Int(((endedAt ?? now) &- since) / 1_000_000)
    }
}

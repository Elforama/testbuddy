import Foundation
#if canImport(CoreGraphics)
import CoreGraphics
#endif

protocol PathBuilder {
    associatedtype Path
    func touch(at p: CGPoint, offset: TimeInterval) throws -> Path
    func move(_ path: Path, to p: CGPoint, at offset: TimeInterval) throws
    func lift(_ path: Path, at offset: TimeInterval) throws
    func explicitDrag(_ moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)], liftAt: TimeInterval) throws -> Path?
}

struct Gesture<Path> {
    let paths: [Path]
    let timeout: TimeInterval
    let early: Bool
    let liftAt: TimeInterval
    let waitsForSystem: Bool
}

enum Gestures {
    static func waitsForSystem<P>(_ g: Gesture<P>, ci: Bool) -> Bool {
        g.waitsForSystem && ci
    }

    static let tapHoldMs = 18.0
    static let repeatIntervalMs = 200.0
    static let repeatHoldMs = 100.0
    static let dragKickPt = 12.0, dragKickS = 0.04, dragFastS = 0.15
    static let dragTailPt = 20.0, dragTailPtPerS = 200.0
    static let dragTailPtCI = 40.0
    static let flutterTailPt = 40.0, flutterTailPtPerS = 80.0

    static func tail(flutter: Bool, ci: Bool) -> (pt: Double, speed: Double) {
        if flutter { return (flutterTailPt, flutterTailPtPerS) }
        return (ci ? dragTailPtCI : dragTailPt, dragTailPtPerS)
    }

    static func tap<B: PathBuilder>(_ b: B, at p: CGPoint, holdMs: Double = Gestures.tapHoldMs) throws -> Gesture<B.Path> {
        let path = try b.touch(at: p, offset: 0)
        try b.lift(path, at: max(holdMs, 1) / 1000)
        return Gesture(paths: [path], timeout: 10 + holdMs / 1000, early: true, liftAt: max(holdMs, 1) / 1000, waitsForSystem: false)
    }

    static func taps<B: PathBuilder>(_ b: B, at p: CGPoint, count: Int, intervalMs: Double = Gestures.repeatIntervalMs,
                                     holdMs: Double = Gestures.repeatHoldMs) throws -> Gesture<B.Path> {
        var paths: [B.Path] = []
        for k in 0..<max(count, 1) {
            let offset = Double(k) * intervalMs / 1000
            let path = try b.touch(at: p, offset: offset)
            try b.lift(path, at: offset + holdMs / 1000)
            paths.append(path)
        }
        let liftAt = (Double(max(count, 1) - 1) * intervalMs + holdMs) / 1000
        return Gesture(paths: paths, timeout: 10 + Double(count) * intervalMs / 1000, early: false, liftAt: liftAt, waitsForSystem: false)
    }

    static let swipeRestS = 0.2
    static let swipeEndCreepS = 1.5
    static let swipeEndCreepPt = 8.0

    static func swipe<B: PathBuilder>(_ b: B, from a: CGPoint, to z: CGPoint, ms: Double, rest: Bool = false) throws -> Gesture<B.Path> {
        if rest {
            let r = swipeRestS, glide = max(ms, 50) / 1000, liftAt = r + glide + swipeEndCreepS
            let dx = Double(z.x - a.x), dy = Double(z.y - a.y), len = (dx * dx + dy * dy).squareRoot()
            let creep = len > 0 ? min(swipeEndCreepPt, len / 4) / len : 0
            let y = CGPoint(x: Double(z.x) - dx * creep, y: Double(z.y) - dy * creep)
            let moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = [
                (p: a, offset: 0, duration: 0),
                (p: a, offset: 0, duration: r),
                (p: y, offset: r, duration: glide),
                (p: z, offset: r + glide, duration: swipeEndCreepS),
            ]
            if let path = try b.explicitDrag(moves, liftAt: liftAt) {
                return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
            }
            let path = try b.touch(at: a, offset: 0)
            try b.move(path, to: a, at: r)
            try b.move(path, to: y, at: r + glide)
            try b.move(path, to: z, at: liftAt)
            try b.lift(path, at: liftAt)
            return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
        }
        let path = try b.touch(at: a, offset: 0)
        let start = 0.05
        try b.move(path, to: z, at: start)
        let liftAt = start + max(ms, 50) / 1000
        try b.lift(path, at: liftAt)
        return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
    }

    static func drag<B: PathBuilder>(_ b: B, from a: CGPoint, to z: CGPoint, tailSpeed: Double = dragTailPtPerS,
                                     tailPt: Double = dragTailPt) throws -> Gesture<B.Path> {
        let dx = Double(z.x - a.x), dy = Double(z.y - a.y)
        let len = (dx * dx + dy * dy).squareRoot()
        guard len >= 1 else { return try tap(b, at: a) }
        func point(_ d: Double) -> CGPoint { CGPoint(x: Double(a.x) + dx / len * d, y: Double(a.y) + dy / len * d) }
        let kick = min(dragKickPt, len / 10), tail = min(tailPt, len / 4)
        let fastEnd = dragKickS + dragFastS
        let liftAt = fastEnd + tail / tailSpeed
        let moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = [
            (p: a, offset: 0, duration: 0),
            (p: point(kick), offset: 0, duration: dragKickS),
            (p: point(len - tail), offset: dragKickS, duration: dragFastS),
            (p: z, offset: fastEnd, duration: liftAt - fastEnd),
        ]
        if let path = try b.explicitDrag(moves, liftAt: liftAt) {
            return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
        }
        let path = try b.touch(at: a, offset: 0)
        try b.move(path, to: point(kick), at: dragKickS)
        try b.move(path, to: point(len - tail), at: fastEnd)
        try b.move(path, to: z, at: liftAt)
        try b.lift(path, at: liftAt)
        return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
    }

    static let holdDragStepPt = 20.0
    static let holdDragDropS = 0.3
    static let holdDragMinMoveS = 0.1

    static func holdDrag<B: PathBuilder>(_ b: B, from a: CGPoint, to z: CGPoint, holdS: Double, ms: Double) throws -> Gesture<B.Path> {
        let hold = max(holdS, 0), glide = max(ms / 1000, holdDragMinMoveS)
        let dx = Double(z.x - a.x), dy = Double(z.y - a.y), len = (dx * dx + dy * dy).squareRoot()
        let steps = max(1, Int((len / holdDragStepPt).rounded(.up)))
        let stepS = glide / Double(steps)
        let dropAt = hold + glide, liftAt = dropAt + holdDragDropS
        func point(_ k: Int) -> CGPoint {
            let f = Double(k) / Double(steps)
            return CGPoint(x: Double(a.x) + dx * f, y: Double(a.y) + dy * f)
        }
        var moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = [
            (p: a, offset: 0, duration: 0),
            (p: a, offset: 0, duration: hold),
        ]
        for k in 1...steps {
            moves.append((p: k == steps ? z : point(k), offset: hold + Double(k - 1) * stepS, duration: stepS))
        }
        moves.append((p: z, offset: dropAt, duration: holdDragDropS))
        if let path = try b.explicitDrag(moves, liftAt: liftAt) {
            return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
        }
        let path = try b.touch(at: a, offset: 0)
        try b.move(path, to: a, at: hold)
        for k in 1...steps {
            try b.move(path, to: k == steps ? z : point(k), at: hold + Double(k) * stepS)
        }
        try b.move(path, to: z, at: liftAt)
        try b.lift(path, at: liftAt)
        return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
    }
}

final class SystemWait {
    static let waitS = 3.0
    static let promptS = 0.1
    static let afterBehindS = 0.2
    static let missesToStop = 3
    static let retryAfter = 20

    enum Outcome: Equatable {
        case answered(asks: Int)
        case late
        case noAnswer
        case notAsked
    }

    typealias Ask = (@escaping (Error?) -> Void) throws -> Void

    struct Clock {
        let now: () -> TimeInterval
        let sleep: (TimeInterval) -> Void
        let wait: (DispatchSemaphore, _ until: TimeInterval) -> Bool

        static let system: Clock = {
            let now = { Double(DispatchTime.now().uptimeNanoseconds) / 1e9 }
            return Clock(now: now, sleep: { Thread.sleep(forTimeInterval: $0) }, wait: { sem, until in
                sem.wait(timeout: .now() + max(0, until - now())) == .success
            })
        }()
    }

    private let ask: Ask
    private let clock: Clock
    private let waitS: TimeInterval
    private let promptS: TimeInterval
    private let lock = NSLock()
    private var misses = 0
    private var skipped = 0
    private var pid: Int32 = 0

    init(waitS: TimeInterval = SystemWait.waitS, promptS: TimeInterval = SystemWait.promptS,
         clock: Clock = .system, ask: @escaping Ask) {
        self.waitS = waitS
        self.promptS = promptS
        self.clock = clock
        self.ask = ask
    }

    func wait(pid: Int32 = 0) -> Outcome {
        lock.lock()
        if pid != 0 && pid != self.pid { self.pid = pid; misses = 0; skipped = 0 }
        if misses >= SystemWait.missesToStop {
            skipped += 1
            if skipped <= SystemWait.retryAfter { lock.unlock(); return .notAsked }
            misses = SystemWait.missesToStop - 1
            skipped = 0
        }
        lock.unlock()
        let deadline = clock.now() + waitS
        var outcome = Outcome.noAnswer
        var asks = 0
        while true {
            let asked = clock.now()
            let answered = DispatchSemaphore(value: 0)
            let errLock = NSLock()
            var failed = false
            asks += 1
            do {
                try ask { err in
                    if err != nil { errLock.lock(); failed = true; errLock.unlock() }
                    answered.signal()
                }
            } catch { return .notAsked }
            guard clock.wait(answered, deadline) else { break }
            errLock.lock(); let f = failed; errLock.unlock()
            if f { return .notAsked }
            let took = clock.now() - asked
            if took <= promptS { outcome = .answered(asks: asks); break }
            outcome = .late
            clock.sleep(SystemWait.afterBehindS)
            if clock.now() >= deadline { break }
        }
        lock.lock()
        if outcome == .noAnswer { misses += 1 } else { misses = 0 }
        lock.unlock()
        return outcome
    }
}

extension Gestures {
    static let confirmSlackS = 0.1

    static func confirmsEarly(endAt: TimeInterval, liftAt: TimeInterval) -> Bool {
        endAt >= 0 && endAt >= liftAt - confirmSlackS
    }

    static let implicitConfirmAfterS = 4.7

    static func implicitlyConfirmed(early: Bool, endedEarly: Bool, confirmedAt: TimeInterval, liftAt: TimeInterval) -> Bool {
        early && !endedEarly && confirmedAt >= liftAt + implicitConfirmAfterS
    }
}

enum Delivery {
    enum Verdict: String, Equatable { case delivered, notDelivered, unknown }
    enum Kind: Equatable { case inputEnd, idleAnswer, other }
    struct Seen: Equatable {
        let pid: Int32
        let at: TimeInterval
        let kind: Kind
        var askedAt: TimeInterval? = nil
    }
    static let marginS = 0.25
    static let quietS = 0.5
    static let promptS = 0.1
    static let waitS = 1.2
    static let noSystemS = 0.5

    static func systemReport(app: Int32, system: Int32, liftAt: TimeInterval, seen: [Seen]) -> Seen? {
        let from = liftAt - Gestures.confirmSlackS
        let ends = seen.filter { $0.kind == .inputEnd && $0.at >= from }
        if ends.contains(where: { $0.pid != system }) { return nil }
        return ends.first
    }

    static func verdict(app: Int32, system: Int32, liftAt: TimeInterval, seen: [Seen], now: TimeInterval,
                        implicitAt: TimeInterval? = nil) -> Verdict? {
        guard app > 0 else { return .unknown }
        let from = liftAt - Gestures.confirmSlackS
        if seen.contains(where: { $0.kind == .inputEnd && $0.at >= from && $0.pid == app }) { return .delivered }
        guard system > 0, app != system else { return .unknown }
        if seen.contains(where: { $0.kind == .inputEnd && $0.at >= from && $0.pid != system }) { return .delivered }
        let report = systemReport(app: app, system: system, liftAt: liftAt, seen: seen)
        if implicitAt != nil && report == nil { return .unknown }
        guard let report else {
            return now >= liftAt + noSystemS ? .unknown : nil
        }
        let answers = seen.filter { $0.pid == app && $0.kind == .idleAnswer && ($0.askedAt ?? -1) >= report.at }
        if answers.contains(where: { $0.at - ($0.askedAt ?? $0.at) > promptS }) { return .unknown }
        if let first = answers.first(where: { $0.askedAt! >= report.at + marginS }),
           answers.contains(where: { $0.askedAt! >= first.at + quietS }) {
            return .notDelivered
        }
        return now >= report.at + waitS ? .unknown : nil
    }

    static func asksIdle(app: Int32, system: Int32, liftAt: TimeInterval, seen: [Seen], now: TimeInterval, lastAsk: TimeInterval?,
                         implicitAt: TimeInterval? = nil) -> Bool {
        guard app > 0, system > 0, app != system,
              let reportAt = systemReport(app: app, system: system, liftAt: liftAt, seen: seen)?.at,
              now >= reportAt + marginS else { return false }
        if verdict(app: app, system: system, liftAt: liftAt, seen: seen, now: now, implicitAt: implicitAt) != nil { return false }
        guard let lastAsk else { return true }
        guard let answer = seen.last(where: { $0.pid == app && $0.kind == .idleAnswer && ($0.askedAt ?? -1) >= lastAsk }) else { return false }
        return now >= answer.at + quietS
    }
}

extension Delivery {
    struct Evidence: Equatable {
        var reportAt: TimeInterval?
        var answerLags: [TimeInterval]
        var implicit = false
    }

    static func evidence(app: Int32, system: Int32, liftAt: TimeInterval, seen: [Seen], implicitAt: TimeInterval? = nil) -> Evidence {
        guard let report = systemReport(app: app, system: system, liftAt: liftAt, seen: seen) else {
            if let implicitAt { return Evidence(reportAt: implicitAt, answerLags: [], implicit: true) }
            return Evidence(reportAt: nil, answerLags: [])
        }
        let lags = seen.filter { $0.pid == app && $0.kind == .idleAnswer && ($0.askedAt ?? -1) >= report.at }.map { $0.at - $0.askedAt! }
        return Evidence(reportAt: report.at, answerLags: lags)
    }

    static func tracks(ci: Bool, probeSawTouch: Bool, app: Int32) -> Bool { ci && !probeSawTouch && app > 0 }
}

struct DeliveryTokens {
    private var last: Int64
    init(base: Int64 = Int64.random(in: 1...(1 << 30)) << 20) { last = base }
    mutating func take() -> Int64 { last += 1; return last }
}

// The finger paths of tap, taps, swipe and drag, and their timing. On the device the paths are
// XCTest's private XCPointerEventPath (XCTPaths in Input.swift, through Private.swift); here they
// are any PathBuilder, so the timing runs in unit tests (CoreTests) with no simulator.
import Foundation
#if canImport(CoreGraphics)
import CoreGraphics
#endif

/// Builds finger paths. Points are the hardware's portrait points; offsets are seconds from the
/// gesture's start.
protocol PathBuilder {
    associatedtype Path
    /// A touch down at p, offset seconds in.
    func touch(at p: CGPoint, offset: TimeInterval) throws -> Path
    /// Moves the touch to p, arriving offset seconds in.
    func move(_ path: Path, to p: CGPoint, at offset: TimeInterval) throws
    func lift(_ path: Path, at offset: TimeInterval) throws
    /// A touch at moves[0].p, moved through the rest by explicit move events (each from the point
    /// before, starting at its offset, lasting its duration), lifted at the last point at liftAt.
    /// nil when this Xcode lacks the API.
    func explicitDrag(_ moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)], liftAt: TimeInterval) throws -> Path?
}

/// A gesture ready to play: its paths, how long testmanagerd may take to confirm it (seconds),
/// and whether the app's own "touches completed" may end that wait early.
struct Gesture<Path> {
    let paths: [Path]
    let timeout: TimeInterval
    let early: Bool
}

enum Gestures {
    /// A tap's hold: long enough for UIKit controls to see it span a frame (a UISwitch needs
    /// ~17 ms), short enough that a nested scroll view paging itself cannot cancel it.
    static let tapHoldMs = 18.0
    /// Keep the pair within the double-tap window while giving each contact time to be
    /// recognized by press handlers on scrollable rows.
    static let repeatIntervalMs = 200.0
    static let repeatHoldMs = 100.0
    /// A no-fling drag: a 12 pt kick in 40 ms, the bulk in 150 ms, a 40 pt tail at 200 pt/s.
    static let dragKickPt = 12.0, dragKickS = 0.04, dragFastS = 0.15
    static let dragTailPt = 40.0, dragTailPtPerS = 200.0

    static func tap<B: PathBuilder>(_ b: B, at p: CGPoint, holdMs: Double = Gestures.tapHoldMs) throws -> Gesture<B.Path> {
        let path = try b.touch(at: p, offset: 0)
        try b.lift(path, at: max(holdMs, 1) / 1000)
        return Gesture(paths: [path], timeout: 10 + holdMs / 1000, early: true)
    }

    /// count taps at a point as one gesture (one finger path per tap).
    static func taps<B: PathBuilder>(_ b: B, at p: CGPoint, count: Int, intervalMs: Double = Gestures.repeatIntervalMs,
                                     holdMs: Double = Gestures.repeatHoldMs) throws -> Gesture<B.Path> {
        var paths: [B.Path] = []
        for k in 0..<max(count, 1) {
            let offset = Double(k) * intervalMs / 1000
            let path = try b.touch(at: p, offset: offset)
            try b.lift(path, at: offset + holdMs / 1000)
            paths.append(path)
        }
        return Gesture(paths: paths, timeout: 10 + Double(count) * intervalMs / 1000, early: false)
    }

    /// How long a resting swipe's finger stays still before it moves: past a scroll view's 150 ms
    /// touch delay, short of a long press (500 ms).
    static let swipeRestS = 0.2
    /// How long it creeps at the end before it lifts (issue #182): past an app stall of about a
    /// second (~925 ms in CI, starting during the glide; #127's 200 ms did not cover it).
    static let swipeEndRestS = 1.0
    /// How far it creeps: the glide stops this far short of the end, and the finger covers the
    /// rest over swipeEndRestS. Slow enough not to fling (8 pt/s), far enough that the app gets
    /// new moves every few frames.
    static let swipeEndCreepPt = 8.0

    /// The flow format's swipe: down at the start, a glide to the end over the duration, lift
    /// there (still moving, so content flings on as it would under a finger).
    ///
    /// rest (a swipe from an element, which places what it starts on: a slider's thumb, a page,
    /// a row; the host asks for it only in CI mode, for slow runners): the finger stays still for
    /// swipeRestS before it moves, and creeps for swipeEndRestS before it lifts (#127, #182). An
    /// app that is behind gets its touches in batches. A slider in a scroll
    /// view only takes the touch once the scroll view lets it go (its touch delay ran out, or
    /// the touch moved sideways), and from wherever the finger is by then: moves that came in
    /// one batch with the touch down are lost. And a slider whose last moves come in one batch
    /// with the lift settles where its thumb had got to, not where the finger lifted (measured
    /// on iOS 27: 100, then back to 85). Resting, the app sees the finger still where it went
    /// down and where it lifts.
    ///
    /// Still is not enough at the end (issue #182): a finger that does not move sends no event.
    /// An app stalled during the glide gets all its moves as one (coalesced), and a slider's pan
    /// that only begins on that one move needs another move to follow the finger: with none
    /// before the lift, it settled at 56 instead of 100 (measured on iOS 27 with the app stopped
    /// 900 ms of every 1.2 s; in CI, 74 after a ~925 ms stall). So the finger creeps the last
    /// swipeEndCreepPt over swipeEndRestS: once the app runs again, it still gets new moves, in
    /// their own frames, before the lift. The gesture is one record on a fixed timeline that
    /// cannot wait for the app, so the creep has to outlast the stall.
    static func swipe<B: PathBuilder>(_ b: B, from a: CGPoint, to z: CGPoint, ms: Double, rest: Bool = false) throws -> Gesture<B.Path> {
        if rest {
            let r = swipeRestS, glide = max(ms, 50) / 1000, liftAt = r + glide + swipeEndRestS
            let dx = Double(z.x - a.x), dy = Double(z.y - a.y), len = (dx * dx + dy * dy).squareRoot()
            let creep = len > 0 ? min(swipeEndCreepPt, len / 4) / len : 0
            let y = CGPoint(x: Double(z.x) - dx * creep, y: Double(z.y) - dy * creep) // where the glide stops
            let moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = [
                (p: a, offset: 0, duration: 0),
                (p: a, offset: 0, duration: r),
                (p: y, offset: r, duration: glide),
                (p: z, offset: r + glide, duration: swipeEndRestS),
            ]
            if let path = try b.explicitDrag(moves, liftAt: liftAt) {
                return Gesture(paths: [path], timeout: 10 + liftAt, early: true)
            }
            // No explicit move events: moveToPoints, played as arriving at each point at its offset.
            let path = try b.touch(at: a, offset: 0)
            try b.move(path, to: a, at: r)
            try b.move(path, to: y, at: r + glide)
            try b.move(path, to: z, at: liftAt)
            try b.lift(path, at: liftAt)
            return Gesture(paths: [path], timeout: 10 + liftAt, early: true)
        }
        let path = try b.touch(at: a, offset: 0)
        let start = 0.05
        try b.move(path, to: z, at: start)
        let liftAt = start + max(ms, 50) / 1000
        try b.lift(path, at: liftAt)
        return Gesture(paths: [path], timeout: 10 + liftAt, early: true)
    }

    /// A drag that leaves the content where the finger lifts, with no fling (scrollUntilVisible).
    /// A short kick takes the pan past its slop (at full speed the content lags the finger), a
    /// fast stretch covers most of the way, and a slow tail ends it: a scroll view keeps the
    /// speed of the last moves, and the tail's is too low to coast on. Shorter than a point: a tap.
    static func drag<B: PathBuilder>(_ b: B, from a: CGPoint, to z: CGPoint, tailSpeed: Double = dragTailPtPerS) throws -> Gesture<B.Path> {
        let dx = Double(z.x - a.x), dy = Double(z.y - a.y)
        let len = (dx * dx + dy * dy).squareRoot()
        guard len >= 1 else { return try tap(b, at: a) }
        func point(_ d: Double) -> CGPoint { CGPoint(x: Double(a.x) + dx / len * d, y: Double(a.y) + dy / len * d) }
        let kick = min(dragKickPt, len / 10), tail = min(dragTailPt, len / 4)
        let fastEnd = dragKickS + dragFastS
        let liftAt = fastEnd + tail / tailSpeed
        let moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = [
            (p: a, offset: 0, duration: 0),
            (p: point(kick), offset: 0, duration: dragKickS),
            (p: point(len - tail), offset: dragKickS, duration: dragFastS),
            (p: z, offset: fastEnd, duration: liftAt - fastEnd),
        ]
        if let path = try b.explicitDrag(moves, liftAt: liftAt) {
            return Gesture(paths: [path], timeout: 10 + liftAt, early: true)
        }
        // No explicit move events in this Xcode: moveToPoints, which this one plays as arriving
        // at each point at its offset (measured: the list then moved the finger's distance and
        // stopped at the lift).
        let path = try b.touch(at: a, offset: 0)
        try b.move(path, to: point(kick), at: dragKickS)
        try b.move(path, to: point(len - tail), at: fastEnd)
        try b.move(path, to: z, at: liftAt)
        try b.lift(path, at: liftAt)
        return Gesture(paths: [path], timeout: 10 + liftAt, early: true)
    }
}

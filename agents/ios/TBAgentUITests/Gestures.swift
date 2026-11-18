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
/// whether a "touches completed" may end that wait early (confirmsEarly), when its last finger
/// lifts (seconds in), and whether it waits for SpringBoard first (SystemWait: a swipe or drag,
/// whose motion is lost while SpringBoard holds the touch; a held tap is only recognized late).
struct Gesture<Path> {
    let paths: [Path]
    let timeout: TimeInterval
    let early: Bool
    let liftAt: TimeInterval
    let waitsForSystem: Bool
}

enum Gestures {
    /// Whether g waits for SpringBoard before it is played (SystemWait, issue #137). A CI
    /// allowance (the host's CI mode, ci: "ci" on the swipe request): #137 was seen on a CI
    /// runner only. Outside CI mode a swipe or drag is played at once, as before the fix.
    static func waitsForSystem<P>(_ g: Gesture<P>, ci: Bool) -> Bool {
        g.waitsForSystem && ci
    }

    /// A tap's hold: long enough for UIKit controls to see it span a frame (a UISwitch needs
    /// ~17 ms), short enough that a nested scroll view paging itself cannot cancel it.
    static let tapHoldMs = 18.0
    /// Keep the pair within the double-tap window while giving each contact time to be
    /// recognized by press handlers on scrollable rows.
    static let repeatIntervalMs = 200.0
    static let repeatHoldMs = 100.0
    /// A no-fling drag: a 12 pt kick in 40 ms, the bulk in 150 ms, a 20 pt tail at 200 pt/s.
    static let dragKickPt = 12.0, dragKickS = 0.04, dragFastS = 0.15
    static let dragTailPt = 20.0, dragTailPtPerS = 200.0
    /// The tail in the host's CI mode: 40 pt, 200 ms at 200 pt/s, a slow-runner allowance. The
    /// tail's speed is what a scroll view keeps at the lift; an app that stalls through the whole
    /// tail gets the fast stretch's last moves and the lift together, and may coast on that.
    /// Measured on the bench's lists (iOS 27 simulator, the screen read again 700 ms after each
    /// drag): 10 and 20 pt tails at 200 pt/s never coasted (~200 drags); a 40 pt tail at
    /// 400 pt/s coasted ~155 px after half its drags, and a 100 ms bulk once in 34. 20 pt
    /// (100 ms, about 6 frames) rather than 10: a stall of a few frames still leaves a slow move
    /// before the lift.
    static let dragTailPtCI = 40.0
    /// A drag over Flutter's own scroll view (FlutterAdapter): its physics fling on what a
    /// UIKit scroll view does not, so its tail is slower, and not shortened (not measured).
    static let flutterTailPt = 40.0, flutterTailPtPerS = 80.0

    /// The tail of a scrollUntilVisible drag: its length (pt) and speed (pt/s).
    static func tail(flutter: Bool, ci: Bool) -> (pt: Double, speed: Double) {
        if flutter { return (flutterTailPt, flutterTailPtPerS) }
        return (ci ? dragTailPtCI : dragTailPt, dragTailPtPerS)
    }

    static func tap<B: PathBuilder>(_ b: B, at p: CGPoint, holdMs: Double = Gestures.tapHoldMs) throws -> Gesture<B.Path> {
        let path = try b.touch(at: p, offset: 0)
        try b.lift(path, at: max(holdMs, 1) / 1000)
        return Gesture(paths: [path], timeout: 10 + holdMs / 1000, early: true, liftAt: max(holdMs, 1) / 1000, waitsForSystem: false)
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
        let liftAt = (Double(max(count, 1) - 1) * intervalMs + holdMs) / 1000
        return Gesture(paths: paths, timeout: 10 + Double(count) * intervalMs / 1000, early: false, liftAt: liftAt, waitsForSystem: false)
    }

    /// How long a resting swipe's finger stays still before it moves: past a scroll view's 150 ms
    /// touch delay, short of a long press (500 ms).
    static let swipeRestS = 0.2
    /// How long it creeps at the end before it lifts (issue #182): past an app stall of about a
    /// second (~925 ms in CI, starting during the glide; #127's 200 ms did not cover it), with
    /// half a second to spare even when the stall starts as the glide ends.
    static let swipeEndCreepS = 1.5
    /// How far it creeps: the glide stops this far short of the end, and the finger covers the
    /// rest over swipeEndCreepS. Slow enough not to fling (about 5 pt/s), far enough that the app gets
    /// new moves every few frames.
    static let swipeEndCreepPt = 8.0

    /// The flow format's swipe: down at the start, a glide to the end over the duration, lift
    /// there (still moving, so content flings on as it would under a finger).
    ///
    /// rest (a swipe from an element, which places what it starts on: a slider's thumb, a page,
    /// a row; the host asks for it only in CI mode, for slow runners): the finger stays still for
    /// swipeRestS before it moves, and creeps for swipeEndCreepS before it lifts (#127, #182). An
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
    /// swipeEndCreepPt over swipeEndCreepS: once the app runs again, it still gets new moves, in
    /// their own frames, before the lift. The gesture is one record on a fixed timeline that
    /// cannot wait for the app, so the creep has to outlast the stall.
    static func swipe<B: PathBuilder>(_ b: B, from a: CGPoint, to z: CGPoint, ms: Double, rest: Bool = false) throws -> Gesture<B.Path> {
        if rest {
            let r = swipeRestS, glide = max(ms, 50) / 1000, liftAt = r + glide + swipeEndCreepS
            let dx = Double(z.x - a.x), dy = Double(z.y - a.y), len = (dx * dx + dy * dy).squareRoot()
            let creep = len > 0 ? min(swipeEndCreepPt, len / 4) / len : 0
            let y = CGPoint(x: Double(z.x) - dx * creep, y: Double(z.y) - dy * creep) // where the glide stops
            let moves: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)] = [
                (p: a, offset: 0, duration: 0),
                (p: a, offset: 0, duration: r),
                (p: y, offset: r, duration: glide),
                (p: z, offset: r + glide, duration: swipeEndCreepS),
            ]
            if let path = try b.explicitDrag(moves, liftAt: liftAt) {
                return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
            }
            // No explicit move events: moveToPoints, played as arriving at each point at its offset.
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

    /// A drag that leaves the content where the finger lifts, with no fling (scrollUntilVisible).
    /// A short kick takes the pan past its slop (at full speed the content lags the finger), a
    /// fast stretch covers most of the way, and a slow tail ends it: a scroll view keeps the
    /// speed of the last moves, and the tail's is too low to coast on. Shorter than a point: a tap.
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
        // No explicit move events in this Xcode: moveToPoints, which this one plays as arriving
        // at each point at its offset (measured: the list then moved the finger's distance and
        // stopped at the lift).
        let path = try b.touch(at: a, offset: 0)
        try b.move(path, to: point(kick), at: dragKickS)
        try b.move(path, to: point(len - tail), at: fastEnd)
        try b.move(path, to: z, at: liftAt)
        try b.lift(path, at: liftAt)
        return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
    }

    /// A press-and-hold drag's longest step (issue #330): the move goes in steps this long at
    /// most, each its own move event at an even pace, so a drag session that follows the finger
    /// (a reorder's placeholder, a drop target's highlight) sees it pass every row on the way.
    static let holdDragStepPt = 20.0
    /// How long the finger rests where it drops before it lifts: the list moves the gap under it
    /// and the drop target takes the drop where the finger is, not where it was a frame ago.
    static let holdDragDropS = 0.3
    /// The shortest move of a press-and-hold drag: faster than this, a drag session can lift
    /// before it has followed the finger.
    static let holdDragMinMoveS = 0.1

    /// A press-and-hold drag (issue #330): drag and drop, a list reorder. UIKit lifts the item
    /// (a long-press recognizer, UIDragInteraction, a table view's reorder) only once the finger
    /// has rested on it about half a second; a swipe that moves at once is a scroll. So the
    /// finger goes down at a, stays still for holdS, moves to z in even steps over ms (at most
    /// holdDragStepPt each), rests at z for holdDragDropS, and lifts there.
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
            (p: a, offset: 0, duration: hold), // still: the press
        ]
        for k in 1...steps {
            moves.append((p: k == steps ? z : point(k), offset: hold + Double(k - 1) * stepS, duration: stepS))
        }
        moves.append((p: z, offset: dropAt, duration: holdDragDropS)) // still where it drops
        if let path = try b.explicitDrag(moves, liftAt: liftAt) {
            return Gesture(paths: [path], timeout: 10 + liftAt, early: true, liftAt: liftAt, waitsForSystem: true)
        }
        // No explicit move events: moveToPoints, played as arriving at each point at its offset.
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

/// Waits, before a gesture is played, for SpringBoard to keep up (issue #137).
///
/// SpringBoard rules on every touch: until it has ruled a touch out as a system gesture (about
/// 80 ms after the touch goes down when it keeps up; measured on iOS 27 from the app's system
/// gesture gate), the app's own gesture recognizers are held. A scroll view's pan stays "began"
/// and its content does not follow the finger. When SpringBoard is behind, the hold lasts until
/// it catches up or about 770 ms have passed, and the content then follows the finger only from
/// where it is by then: the way it went before is lost. In #137's CI log SpringBoard confirmed
/// a pager swipe's touches 1.8 s after the app did (on time for every other gesture of the
/// run). The 0.45 s swipe lifted while the pan was still held and the page did not turn,
/// though the app itself was idle and confirmed its touches. Measured with SpringBoard stopped
/// 1 s of every 1.5 s: the first pager swipe moved the page by 0 pt, or by the last 7 pt of a
/// CI-mode swipe (whose glide ended before the hold did).
///
/// So a swipe or drag first asks SpringBoard to tell when its main run loop is idle, and is
/// played once it answers promptly. That is at once when it keeps up (about 5 ms). When it is
/// behind, its first answer comes late, as it resumes, and that is not enough: a swipe played
/// 30 ms after such an answer was still held for the whole 770 ms. It is asked again after a
/// pause, until it answers promptly: it has caught up, and rules on the touch in time.
/// A SpringBoard that falls behind in the ~80 ms after the touch goes down still holds it: the
/// wait narrows the window, it cannot close it.
///
/// The wait is bounded. An error from XCTest is not an answer: the gesture is played at once
/// ("not asked"). A SpringBoard that does not answer at all, missesToStop waits in a row, is
/// not asked for the next retryAfter gestures; a new SpringBoard (another pid: a respring) is
/// asked afresh.
final class SystemWait {
    /// The longest a gesture waits: past the ~2.3 s SpringBoard was behind in #137's CI log.
    static let waitS = 3.0
    /// An answer this quick means SpringBoard keeps up.
    static let promptS = 0.1
    /// After a late answer, the pause before asking again.
    static let afterBehindS = 0.2
    /// Waits in a row with no answer at all before SpringBoard is not asked any more...
    static let missesToStop = 3
    /// ... for this many gestures.
    static let retryAfter = 20

    enum Outcome: Equatable {
        /// SpringBoard answered promptly (after `asks` asks).
        case answered(asks: Int)
        /// It answered, but never promptly, within the wait.
        case late
        /// No answer at all within the wait.
        case noAnswer
        /// Not asked: XCTest could not ask it, answered with an error, or asking is off.
        case notAsked
    }

    /// ask asks SpringBoard to call its argument once its main run loop is idle (with an error
    /// when XCTest could not ask), and throws when it cannot be asked.
    typealias Ask = (@escaping (Error?) -> Void) throws -> Void

    /// The wait's time: uptime in seconds, a pause, and waiting for an answer until a deadline
    /// (true: answered). The agent uses the system's; tests drive their own, so what they assert
    /// does not depend on how promptly a loaded runner schedules them (issue #228).
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

    /// Blocks until SpringBoard (pid, 0 when not known) answers promptly, at most waitS.
    func wait(pid: Int32 = 0) -> Outcome {
        lock.lock()
        if pid != 0 && pid != self.pid { self.pid = pid; misses = 0; skipped = 0 }
        if misses >= SystemWait.missesToStop {
            skipped += 1
            if skipped <= SystemWait.retryAfter { lock.unlock(); return .notAsked }
            // Ask again; one more wait with no answer turns it off again.
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
            // It was behind: it answers once its run loop is idle, but rules on touches only
            // once it has caught up. Pause, then ask again.
            clock.sleep(SystemWait.afterBehindS)
            if clock.now() >= deadline { break }
        }
        lock.lock()
        // Only no answer at all is a miss: a SpringBoard that answers, however slowly, is there.
        if outcome == .noAnswer { misses += 1 } else { misses = 0 }
        lock.unlock()
        return outcome
    }
}

extension Gestures {
    /// How long before a record's lift its own "touches completed" can come: testmanagerd plays
    /// a short tap faster than its offsets (measured on iOS 27: both completions 9 and 11 ms
    /// after an 18 ms tap was sent).
    static let confirmSlackS = 0.1

    /// Whether a "touches completed" that came endAt seconds after a record started playing may
    /// confirm it early (Gesture.early): from whichever process, but only once the record is
    /// about to lift (liftAt seconds in, less confirmSlackS), never one from before it started.
    /// One that comes earlier is another record's: SpringBoard, when it is behind, confirms a
    /// gesture seconds late, during the next one, which would then be reported done while it is
    /// still being played (issue #137; #139, item 3).
    static func confirmsEarly(endAt: TimeInterval, liftAt: TimeInterval) -> Bool {
        endAt >= 0 && endAt >= liftAt - confirmSlackS
    }

    /// testmanagerd waits 5 s after a record for the app's "touches completed", then confirms
    /// it anyway ("Timed out after waiting 5.0s for TouchEventsCompleted, event will be
    /// implicitly confirmed"). A confirmation this long after the lift, with none from the app
    /// before it, is that one (issue #111). A normal one comes ~200 ms after the lift; an
    /// implicit one 5.0 s or more after it.
    static let implicitConfirmAfterS = 4.7

    /// Whether a record that may be confirmed early (Gesture.early) was confirmed only
    /// implicitly: no "touches completed" from the app ended the wait (endedEarly false), and
    /// testmanagerd's confirmation came confirmedAt seconds after the record started, at least
    /// implicitConfirmAfterS after its lift. Its touches may never have reached the app (a
    /// simulator still starting up), and the host is told so (issue #111).
    static func implicitlyConfirmed(early: Bool, endedEarly: Bool, confirmedAt: TimeInterval, liftAt: TimeInterval) -> Bool {
        early && !endedEarly && confirmedAt >= liftAt + implicitConfirmAfterS
    }
}

/// Whether the app in front got a record's touches (issue #334).
///
/// testmanagerd confirms a record on the first "touches completed" it hears, from any process:
/// SpringBoard's (it rules on every touch; pid 1716 in the CI log, "osservice<com.apple.
/// SpringBoard>:1716") as well as the app's. The agent counted any process's too. So a touch the
/// app never got was reported done: UIKit drops the touches an app gets while it presents a
/// screen (in the CI log, a tap during a full-screen cover's presentation: SpringBoard reported
/// it completed, the app never did, and went on).
///
/// The app's own report proves it got them. So does one from any process but SpringBoard: a
/// process whose view is in the app's window (the photo picker, an in-app Safari view, the share
/// sheet) took them. Only SpringBoard's report, told by its pid, with none from any other
/// process, can start a "not delivered", and only on proof that the app ran on past the touch.
///
/// The proof comes from the agent's own asks: it asks the app to say when its run loop is idle
/// (XCTest's notifyWhenRunLoopIdle), from marginS after SpringBoard's report (the touch was
/// queued by then), and again quietS later. Two answers, each prompt (within promptS of its
/// ask: the app's main thread was free), the second asked quietS after the first came, with no
/// report from the app all along: the app handled every event it had queued, twice, a quiet
/// window apart, and the touch was not one of them. The answers come back over the automation
/// session, not the relay that carries the app's report (testmanagerd's notifications), so
/// their order is not the report's. Two things cover that. A late answer (the app was busy
/// when asked: a stall, maybe with the touch queued) makes the verdict unknown at once. And
/// after a prompt first answer the app's report, posted before it, has the quiet window to
/// arrive before the second answer. Nothing else is proof: the app's other posts (a view
/// appearing; the probe's commits, over notifyd) can come just before a stall, the touch
/// handled after it. In doubt, unknown: the host never sends a tap again on unknown.
enum Delivery {
    enum Verdict: String, Equatable { case delivered, notDelivered, unknown }
    /// What a process posted: its touches completed, its answer to the agent's ask to go idle,
    /// or anything else (no proof).
    enum Kind: Equatable { case inputEnd, idleAnswer, other }
    /// at: seconds from the record's start; askedAt: an answer's ask.
    struct Seen: Equatable {
        let pid: Int32
        let at: TimeInterval
        let kind: Kind
        var askedAt: TimeInterval? = nil
    }
    /// How long after SpringBoard's report the agent first asks.
    static let marginS = 0.25
    /// How long after the first answer the second ask is made.
    static let quietS = 0.5
    /// The longest a prompt answer takes; a later one means the app was busy.
    static let promptS = 0.1
    /// How long after SpringBoard's report the agent judges, then unknown.
    static let waitS = 1.2
    /// How long after the lift, with no report from any process, before unknown.
    static let noSystemS = 0.5

    /// SpringBoard's (pid system) report of the record's touches: from the last lift on (less
    /// Gestures.confirmSlackS: earlier ones are an earlier contact's or record's), with none
    /// from any other process.
    static func systemReport(app: Int32, system: Int32, liftAt: TimeInterval, seen: [Seen]) -> Seen? {
        let from = liftAt - Gestures.confirmSlackS
        let ends = seen.filter { $0.kind == .inputEnd && $0.at >= from }
        if ends.contains(where: { $0.pid != system }) { return nil }
        return ends.first
    }

    /// The verdict on the touches of a record that lifts liftAt seconds in, for the app with pid
    /// app (SpringBoard: pid system, 0 when not known), from what was seen by now (seconds from
    /// the start); nil while it cannot tell yet. implicitAt: when testmanagerd confirmed the
    /// record only implicitly (issue #111): with no report from any process, unknown.
    static func verdict(app: Int32, system: Int32, liftAt: TimeInterval, seen: [Seen], now: TimeInterval,
                        implicitAt: TimeInterval? = nil) -> Verdict? {
        guard app > 0 else { return .unknown }
        let from = liftAt - Gestures.confirmSlackS
        if seen.contains(where: { $0.kind == .inputEnd && $0.at >= from && $0.pid == app }) { return .delivered }
        // SpringBoard not known (or in front itself): no report of its to judge from.
        guard system > 0, app != system else { return .unknown }
        // Another process's report, one in the app's window (not SpringBoard's).
        if seen.contains(where: { $0.kind == .inputEnd && $0.at >= from && $0.pid != system }) { return .delivered }
        let report = systemReport(app: app, system: system, liftAt: liftAt, seen: seen)
        // Review of c5e98bf (issue #111): testmanagerd's implicit confirmation, unlike
        // SpringBoard's report, does not show the system routed the touches: it may hold them
        // and deliver them later, after the host sent the swipe again. Prompt idle answers then
        // prove nothing.
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

    /// Whether the agent asks the app to go idle now (lastAsk: its last ask, seconds from the
    /// start): once SpringBoard has reported and the margin passed, with no verdict yet, never
    /// while an ask is unanswered, and quietS after the last answer at the soonest (the first
    /// ask: at once).
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
    /// What a verdict rests on, for the host's trace: when SpringBoard's report came (seconds
    /// from the record's start, nil without one) and how long each answer to the agent's asks
    /// to go idle took. implicit: no process reported the touches, and reportAt is when
    /// testmanagerd confirmed them anyway (issue #111), which gives "unknown".
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

    /// Whether the agent tracks a tap's delivery: only in the host's CI mode (the host asks only
    /// then), for a touch the probe did not see reach the app, to a known app.
    static func tracks(ci: Bool, probeSawTouch: Bool, app: Int32) -> Bool { ci && !probeSawTouch && app > 0 }
}

/// The agent's delivery tokens (issue #334). Each agent starts at a random base, so a token the
/// host kept from an agent that ran before (across a restart) names no record of this one.
struct DeliveryTokens {
    private var last: Int64
    init(base: Int64 = Int64.random(in: 1...(1 << 30)) << 20) { last = base }
    mutating func take() -> Int64 { last += 1; return last }
}

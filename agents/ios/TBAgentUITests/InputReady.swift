// Whether the app in front takes input yet (issue #290): no input is sent to a probed app
// while it is still coming to the foreground.
//
// An app brought back from the background (launchApp without stopping it, an activation) or
// launched is listed in front, with its window and its whole tree, as soon as SpringBoard
// starts the animation that opens it. Until the animation ends, UIKit holds the app inactive
// (deactivation reasons SystemAnimation and SuspendedEventsOnly), and SpringBoard draws its
// window scaled: a touch then reaches the window, but at the point under the scaled window.
// Seen on a simulator (iOS 26): a tap at (146, 128) on a button arrived at (146, 94) and
// (146, 104), 10 ms before the app became active, and took the Back button above the target.
// The app's frames in the tree are its own layout, unscaled: the read is right, the touch is not.
//
// The probe tells the agent each app's phase (TBProbe: UIApplication's foreground, active,
// resign and background notifications; a process starts entering). An op that sends input to
// a probed app first waits for it to leave "entering", on the probe's notification (no
// polling, no fixed delay), within capMs and the op's own deadline. The launch's first tree is
// read only once the launched app is no longer entering (LaunchHold): the phase is read from
// the probe's own notify name, which the probe sets as it loads, so this holds on a cold launch
// too, before the agent has heard the probe's hello. The host's next read is then of the app as
// it takes input.
// Only "entering" (or "background", for an app listed in front that UIKit has not yet told it
// is coming back) holds input: an app made inactive by an alert, Control Center or the app
// switcher stays inactive until a person deals with it, and the touch is for that.
//
// An app that never became active gets no "will resign active" from UIKit: one launched under
// a system alert (a permission prompt at launch) stays "entering" by its own account. What ends
// the wait there is the app no longer being in front: SpringBoard lists itself in front while
// its alert is up (Apps.foreground). So the wait holds only while the app is both entering and
// in front. An overlay SpringBoard does not list in front would hold it to the cap: that wait
// is never silent (the op's answer says appActive false, the agent logs it, the host traces it).
import Foundation

/// A probed app's phase, as its probe reports it (the state of dev.testbuddy.lifecycle.<pid>).
enum AppPhase: UInt64 {
    /// No probe, or a probe from before issue #290.
    case unknown = 0
    /// Active: its window takes input as laid out.
    case active = 1
    /// Launched or on its way back from the background, not active yet: SpringBoard's opening
    /// animation is running, and a touch lands where the scaled window is.
    case entering = 2
    /// In front but inactive: Control Center, the app switcher.
    case inactive = 3
    /// In the background.
    case background = 4

    init(state: UInt64) { self = AppPhase(rawValue: state) ?? .unknown }

    /// Whether input sent now to this app, listed in front, would land before it takes it:
    /// entering, or still in the background by its own account (it is listed in front from the
    /// start of the opening animation, before UIKit tells it it is coming back: seen on a
    /// simulator, the agent's check came first).
    var holdsInput: Bool { self == .entering || self == .background }
}

enum InputReady {
    /// The longest the agent waits for an entering app (the opening animation takes ~0.3 s
    /// with tb's fast animations, ~0.6 s without; a starved simulator takes longer).
    static let capMs = 3000

    /// How a wait ended.
    enum End: String, Equatable {
        /// There was nothing to wait for.
        case none
        /// The app became active (or something made it inactive).
        case active
        /// The app was no longer in front: a system alert over it.
        case leftFront = "left the front"
        /// Still entering and in front at the cap or the op's deadline.
        case timedOut = "timed out"
    }

    struct Outcome: Equatable {
        /// How long the op waited (0: the app took input already).
        let waitedMs: Int
        /// The wait did not time out. False: the app was still entering, in front, at the cap
        /// or the op's deadline; the input goes anyway (the host learns it: appActive false).
        let ready: Bool
        /// Its phase when the wait began.
        let from: AppPhase
        let end: End
    }

    /// Waits while phase() holds input and inFront() (the app is still the one in front), up
    /// to capMs or leftMs (the op's time left; nil: no deadline). wait(timeoutMs, check)
    /// blocks until check() is true, re-checking it on each event (Events.awaitCondition);
    /// now: uptime ns.
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

/// The hold on a launch's first tree read (Ops.awaitLaunchTree): the read waits while the
/// launched app is entering and in front, up to InputReady.capMs (and the launch's own
/// timeout). Checked on each event of the launch's wait, before each read.
final class LaunchHold {
    private let start: UInt64
    private let limitMs: Int
    private var heldSince: UInt64?
    private var endedAt: UInt64?
    /// The hold ended at the cap with the app still entering in front.
    private(set) var timedOut = false

    init(now: UInt64, timeoutMs: Int) {
        start = now
        limitMs = min(InputReady.capMs, max(timeoutMs, 0))
    }

    /// Whether the read waits now. phase: the launched app's; inFront: it is the app in front.
    /// Once the hold has ended it never starts again.
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

    /// While holding: when to look again without an event (at the cap), in ms; 0 otherwise.
    func recheckMs(now: UInt64) -> Int {
        guard heldSince != nil, endedAt == nil else { return 0 }
        let end = start + UInt64(limitMs) * 1_000_000
        return now < end ? max(1, Int((end - now) / 1_000_000)) : 1
    }

    /// How long the read was held; nil: it never was.
    func heldMs(now: UInt64) -> Int? {
        guard let since = heldSince else { return nil }
        return Int(((endedAt ?? now) &- since) / 1_000_000)
    }
}

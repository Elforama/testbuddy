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
// polling, no fixed delay), within capMs and the op's own deadline; the launch's first tree is
// read only after the same wait, so the host's next read is of the app as it takes input.
// Only "entering" (or "background", for an app listed in front that UIKit has not yet told it
// is coming back) holds input: an app made inactive by an alert, Control Center or the app
// switcher stays inactive until a person deals with it, and the touch is for that.
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
    /// In front but inactive: an alert, Control Center, the app switcher.
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

    struct Outcome: Equatable {
        /// How long the op waited (0: the app took input already).
        let waitedMs: Int
        /// The app took input when the wait ended. False: it was still entering at the cap or
        /// the op's deadline; the input goes anyway (the host learns it: appActive false).
        let ready: Bool
        /// Its phase when the wait began.
        let from: AppPhase
    }

    /// Waits until phase() no longer holds input, up to capMs or leftMs (the op's time left;
    /// nil: no deadline). wait(timeoutMs, check) blocks until check() is true, re-checking it
    /// on each event (Events.awaitCondition); now: uptime ns.
    static func await(phase: @escaping () -> AppPhase, leftMs: Int?, now: () -> UInt64,
                      wait: (Int, () -> Bool) -> Bool) -> Outcome {
        let first = phase()
        guard first.holdsInput else { return Outcome(waitedMs: 0, ready: true, from: first) }
        let limit = min(capMs, max(leftMs ?? capMs, 0))
        let t0 = now()
        let ready = limit > 0 && wait(limit) { !phase().holdsInput }
        let waited = Int((now() &- t0) / 1_000_000)
        return Outcome(waitedMs: waited, ready: ready, from: first)
    }
}

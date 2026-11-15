// The one gate every device event goes through (issue #286): no event is sent while an
// earlier one is unconfirmed.
//
// testmanagerd synthesizes one event at a time. A record the agent plays (Input.play) can
// answer before testmanagerd has confirmed it: on the app's "touches completed", ~30 ms after
// the lift, while testmanagerd's own confirmation comes ~200 ms after it, and later still when
// the tap's reaction keeps the app's main thread busy. Until then testmanagerd refuses any
// other event ("Unable to perform <XCDeviceEvent …>, only one gesture can be performed at a
// time"), and XCTest reports that only as an XCTIssue, which the op would not see. Records
// already waited for the one before them; the Home, Lock and volume buttons, the orientation
// and XCUIElement.tap() did not, and a Home press right after a busy tap did nothing.
//
// So every call that makes testmanagerd synthesize an event goes through here: a record
// (send, then leave it while unconfirmed), and an XCTest call that returns once XCTest is done
// with its event (call: the buttons, the orientation, XCUIElement.tap(), an app's launch or
// activation). The wait is the one records always had: PendingInput's bound, cut to the op's
// deadline; an event still unconfirmed at the deadline is InputNotSent (nothing was sent, the
// op's error is transient). It waits only while an event is unconfirmed: usually not at all,
// otherwise until testmanagerd's confirmation (~200 ms).
import Foundation

/// The time an op gave a device event (its deadline), as it runs out.
struct InputBudget {
    /// Uptime ns; nil: no deadline.
    let endBy: UInt64?

    init(deadlineMs: Int?) {
        endBy = deadlineMs.map { DispatchTime.now().uptimeNanoseconds + UInt64(max($0, 0)) * 1_000_000 }
    }

    /// What is left of it, in ms (0 once it is over); nil: no deadline.
    func leftMs() -> Int? {
        guard let endBy else { return nil }
        let now = DispatchTime.now().uptimeNanoseconds
        return endBy > now ? Int((endBy - now) / 1_000_000) : 0
    }
}

final class InputGate {
    /// A device event sent and not yet confirmed. Its fields are read and written under the
    /// gate's lock (`locked`): the agent's events lock, which its confirmations signal.
    final class Event {
        var started = false
        var done = false
        var error: Error?
        init() {}
    }

    /// Waits until done() (checked under the lock) or timeoutMs: true if done.
    private let wait: (() -> Bool, Int) -> Bool
    /// Runs f under the lock and wakes the waiters.
    private let locked: (() -> Void) -> Void
    /// The last event not known to be confirmed, under the lock. Ops run one at a time, on the
    /// op's thread, but typing's XCUIElement.tap() comes through on the main thread.
    private var _last: Event?
    private var last: Event? {
        get { var e: Event?; locked { e = _last }; return e }
        set { locked { _last = newValue } }
    }

    /// wait and locked: the agent's events lock (Events.awaitInput, Events.signal).
    init(wait: @escaping (() -> Bool, Int) -> Bool, locked: @escaping (() -> Void) -> Void) {
        self.wait = wait
        self.locked = locked
    }

    /// Whether an event is still unconfirmed.
    var unconfirmed: Bool {
        var pending = false
        locked { pending = _last.map { !$0.done } ?? false }
        return pending
    }

    /// Runs send once no earlier event is unconfirmed (send then sends one). The wait is
    /// PendingInput.waitMs, bounded by deadlineMs (the time the op has left, issue #130). With a
    /// deadline, an event still unconfirmed at its end is InputNotSent: send never runs, and
    /// the event stays the one to wait for, so an op asked again waits for it too. Without one,
    /// it is given up on after PendingInput.maxWaitMs and send runs, as before issue #130.
    func send<T>(deadlineMs: Int?, _ send: (InputBudget) throws -> T) throws -> T {
        let budget = InputBudget(deadlineMs: deadlineMs)
        if let e = last {
            let waitMs = PendingInput.waitMs(boundedBy: budget.leftMs())
            let t0 = DispatchTime.now().uptimeNanoseconds
            let done = wait({ e.done }, waitMs)
            let waited = Int((DispatchTime.now().uptimeNanoseconds - t0) / 1_000_000)
            if done && waited > 0 { NSLog("tb: input gate: waited %d ms for the previous input's confirmation", waited) }
            if !done && deadlineMs != nil {
                throw InputNotSent(why: String(format: "the previous input was still not confirmed after %.1f s", Double(waitMs) / 1000))
            }
            if !done { NSLog("tb: the previous input was still not confirmed after %.1f s; sending anyway", Double(waitMs) / 1000) }
            last = nil
        }
        return try send(budget)
    }

    /// The event send just sent, not yet confirmed: the next one waits for it.
    func leave(_ e: Event) { last = e }

    /// A device event made by an XCTest call that returns once XCTest is done with it (a
    /// button press, the orientation, XCUIElement.tap(), a launch): f, through the gate, run
    /// by run (the main thread, given up on at the op's deadline: AppWait.read). A call given
    /// up on after it started finishes on its own later: until it does, it is the unconfirmed
    /// event. One given up on before it started never runs (Bounded.run): it leaves nothing.
    func call(deadlineMs: Int?, on run: (@escaping () throws -> Void) throws -> Void, _ f: @escaping () throws -> Void) throws {
        try send(deadlineMs: deadlineMs) { _ in
            let e = Event()
            let locked = self.locked
            do {
                try run {
                    locked { e.started = true }
                    defer { locked { e.done = true } }
                    try f()
                }
            } catch {
                var running = false
                locked { running = e.started && !e.done }
                if running { leave(e) }
                throw error
            }
        }
    }
}

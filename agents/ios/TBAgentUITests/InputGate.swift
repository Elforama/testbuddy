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
    /// ns on clock; nil: no deadline.
    let endBy: UInt64?
    /// Uptime in the agent; a test's virtual clock (issue #314).
    let clock: AgentClock

    init(deadlineMs: Int?, clock: AgentClock = UptimeClock.shared) {
        self.clock = clock
        endBy = deadlineMs.map { clock.now() + UInt64(max($0, 0)) * 1_000_000 }
    }

    /// What is left of it, in ms (0 once it is over); nil: no deadline.
    func leftMs() -> Int? {
        guard let endBy else { return nil }
        return clock.ms(until: endBy)
    }
}

final class InputGate {
    /// A device event: being sent, or sent and not yet confirmed. Its fields are read and
    /// written under the gate's lock (`locked`): the agent's events lock, which its
    /// confirmations signal.
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
    /// Made on the op's thread before each call, run around it where it runs (Ops: the op's
    /// XCTest issue generation, so an issue the call raises is filed as that op's, IssueSink).
    private let wrap: (@escaping () throws -> Void) -> () throws -> Void
    /// The last event not known to be done, read and written only under the lock. An event is
    /// put here before it is sent and stays until it is confirmed: ops run one at a time on the
    /// op's thread, but a call given up on keeps running on the main thread (typing's
    /// XCUIElement.tap() runs there inline, a button press is sent there).
    private var last: Event?
    /// The clock an op's deadline runs out on: uptime in the agent; a test's virtual one, which
    /// its wait advances (issue #314).
    private let clock: AgentClock

    /// wait and locked: the agent's events lock (Events.awaitInput, Events.signal).
    init(wait: @escaping (() -> Bool, Int) -> Bool, locked: @escaping (() -> Void) -> Void,
         wrap: @escaping (@escaping () throws -> Void) -> () throws -> Void = { $0 },
         clock: AgentClock = UptimeClock.shared) {
        self.wait = wait
        self.locked = locked
        self.wrap = wrap
        self.clock = clock
    }

    /// Whether an event is being sent or still unconfirmed.
    var unconfirmed: Bool {
        var pending = false
        locked { pending = last.map { !$0.done } ?? false }
        return pending
    }

    /// Makes slot the last event once no earlier one is unconfirmed, checked and set under one
    /// lock: two threads cannot both pass. The wait is PendingInput.waitMs, bounded by the
    /// budget (the time the op has left, issue #130). With a deadline, an event still
    /// unconfirmed at its end is InputNotSent: nothing is sent, and it stays the one to wait
    /// for, so an op asked again waits for it too. Without one, it is given up on after
    /// PendingInput.maxWaitMs, as before issue #130.
    private func admit(_ slot: Event, _ budget: InputBudget, deadline: Bool) throws {
        let t0 = clock.now()
        var waitedFor: Event?
        while true {
            var pending: Event?
            locked {
                if let l = last, !l.done, l !== waitedFor { pending = l } else { last = slot }
            }
            guard let e = pending else { break }
            let waitMs = PendingInput.waitMs(boundedBy: budget.leftMs())
            if !wait({ e.done }, waitMs) {
                if deadline {
                    // A call that started and has not returned: an XCTest call hung on the main
                    // thread (a wedged app), not a confirmation testmanagerd owes.
                    var running = false
                    locked { running = e.started && !e.done }
                    throw InputNotSent(why: running
                        ? String(format: "an earlier device call is still running on the main thread after %.1f s", Double(waitMs) / 1000)
                        : String(format: "the previous input was still not confirmed after %.1f s", Double(waitMs) / 1000))
                }
                NSLog("tb: the previous input was still not confirmed after %.1f s; sending anyway", Double(waitMs) / 1000)
                waitedFor = e // given up on: the next pass takes the slot over it
            }
        }
        let waited = Int((clock.now() - t0) / 1_000_000)
        if waited > 0 { NSLog("tb: input gate: waited %d ms for the previous input's confirmation", waited) }
    }

    /// Runs send once no earlier event is unconfirmed (admit); send then sends one (a record).
    /// Until send returns no other event goes; one it leaves (leave) stays the event to wait for.
    func send<T>(deadlineMs: Int?, _ send: (InputBudget) throws -> T) throws -> T {
        let budget = InputBudget(deadlineMs: deadlineMs, clock: clock)
        let slot = Event()
        try admit(slot, budget, deadline: deadlineMs != nil)
        defer { locked { slot.done = true } }
        return try send(budget)
    }

    /// The record send just sent, not yet confirmed: the next event waits for it.
    func leave(_ e: Event) { locked { last = e } }

    /// A device event made by an XCTest call that returns once XCTest is done with it (a
    /// button press, the orientation, XCUIElement.tap(), a launch): f, through the gate, run
    /// by run (the main thread, given up on at the op's deadline: AppWait.read; or inline). The
    /// event is the last one from before it runs: while it runs, wherever it runs, and after
    /// the op gave up on it, nothing else is sent. One given up on before it started never runs
    /// (Bounded.run): it is done at once.
    func call(deadlineMs: Int?, on run: (@escaping () throws -> Void) throws -> Void, _ f: @escaping () throws -> Void) throws {
        let e = Event()
        try admit(e, InputBudget(deadlineMs: deadlineMs, clock: clock), deadline: deadlineMs != nil)
        let wrapped = wrap(f)
        let locked = self.locked
        do {
            try run {
                locked { e.started = true }
                defer { locked { e.done = true } }
                try wrapped()
            }
        } catch {
            // Never started: it never will. Started: it is done when it ends.
            locked { if !e.started { e.done = true } }
            throw error
        }
    }
}

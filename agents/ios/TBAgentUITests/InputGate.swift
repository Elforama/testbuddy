import Foundation

struct InputBudget {
    let endBy: UInt64?
    let clock: AgentClock

    init(deadlineMs: Int?, clock: AgentClock = UptimeClock.shared) {
        self.clock = clock
        endBy = deadlineMs.map { clock.now() + UInt64(max($0, 0)) * 1_000_000 }
    }

    func leftMs() -> Int? {
        guard let endBy else { return nil }
        return clock.ms(until: endBy)
    }
}

final class InputGate {
    final class Event {
        var started = false
        var done = false
        var error: Error?
        init() {}
    }

    private let wait: (() -> Bool, Int) -> Bool
    private let locked: (() -> Void) -> Void
    private let wrap: (@escaping () throws -> Void) -> () throws -> Void
    private var last: Event?
    private let clock: AgentClock

    init(wait: @escaping (() -> Bool, Int) -> Bool, locked: @escaping (() -> Void) -> Void,
         wrap: @escaping (@escaping () throws -> Void) -> () throws -> Void = { $0 },
         clock: AgentClock = UptimeClock.shared) {
        self.wait = wait
        self.locked = locked
        self.wrap = wrap
        self.clock = clock
    }

    var unconfirmed: Bool {
        var pending = false
        locked { pending = last.map { !$0.done } ?? false }
        return pending
    }

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
                    var running = false
                    locked { running = e.started && !e.done }
                    throw InputNotSent(why: running
                        ? String(format: "an earlier device call is still running on the main thread after %.1f s", Double(waitMs) / 1000)
                        : String(format: "the previous input was still not confirmed after %.1f s", Double(waitMs) / 1000))
                }
                NSLog("tb: the previous input was still not confirmed after %.1f s; sending anyway", Double(waitMs) / 1000)
                waitedFor = e
            }
        }
        let waited = Int((clock.now() - t0) / 1_000_000)
        if waited > 0 { NSLog("tb: input gate: waited %d ms for the previous input's confirmation", waited) }
    }

    func send<T>(deadlineMs: Int?, _ send: (InputBudget) throws -> T) throws -> T {
        let budget = InputBudget(deadlineMs: deadlineMs, clock: clock)
        let slot = Event()
        try admit(slot, budget, deadline: deadlineMs != nil)
        defer { locked { slot.done = true } }
        return try send(budget)
    }

    func leave(_ e: Event) { locked { last = e } }

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
            locked { if !e.started { e.done = true } }
            throw error
        }
    }
}

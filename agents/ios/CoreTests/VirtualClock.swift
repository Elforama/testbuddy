import Foundation
import XCTest
@testable import TBAgentCore

/// A virtual clock for the agent's waits (issue #314): what the tests give Bounded.run,
/// ScreenRead, the input gate and typing in place of uptime. Time moves only when a wait has
/// nothing else to wait for, never with the runner's: a runner that stalls cannot make a wait
/// end early or late, and a test can say exactly when an op answered.
///
/// - A fake's own waits (waitFor) run what falls due, in order, and otherwise move time to their
///   end: no real time passes.
/// - A wait for work on a queue (wait, as Bounded.run's) waits in real time for work that is
///   free to run, however long the runner takes; while the app holds something (a Hold: a busy
///   main thread, a read the app does not answer), the work can only end at what falls due,
///   or not at all: the wait runs what falls due before its deadline, then ends at it.
///
/// Real time is waited only as a guard against a test that would hang (hangGuard), never
/// asserted on.
final class VirtualClock: AgentClock {
    /// Where every virtual clock starts (ns): a test's deadlines are taken from it.
    static let start: UInt64 = 1_000_000_000_000
    /// The longest any wait waits in real time: only a broken test (or a broken wait in the
    /// code under test) gets there.
    static let hangGuard: TimeInterval = 30

    private let cond = NSCondition()
    private var _now = VirtualClock.start
    private var events: [(due: UInt64, n: Int, run: () -> Void)] = []
    private var made = 0
    /// Holds in force: something the app holds that only an event or the test releases.
    private var held = 0

    func now() -> UInt64 { cond.lock(); defer { cond.unlock() }; return _now }
    /// ms of virtual time since start.
    var elapsedMs: Int { Int((now() - VirtualClock.start) / 1_000_000) }

    /// Runs f ms from now on this clock, once a wait reaches that time (on the waiting thread,
    /// under no lock of the clock's).
    func at(_ ms: Int, _ f: @escaping () -> Void) {
        cond.lock()
        made += 1
        events.append((_now + UInt64(max(ms, 0)) * 1_000_000, made, f))
        cond.unlock()
    }

    /// Something the app holds until released: a busy main thread, an XCTest call waiting on
    /// an app that does not answer. A thread that waits on it blocks until it is released.
    final class Hold {
        fileprivate unowned let clock: VirtualClock
        fileprivate var counted = false
        fileprivate var released = false
        fileprivate init(_ clock: VirtualClock) { self.clock = clock }

        /// Blocks until released (or hangGuard). A Hold not held yet is held from now.
        func wait() {
            let c = clock.cond
            c.lock()
            defer { c.unlock() }
            if released { return }
            if !counted { counted = true; clock.held += 1 }
            let giveUp = Date().addingTimeInterval(VirtualClock.hangGuard)
            while !released { if !c.wait(until: giveUp) { XCTFail("a hold was never released"); return } }
        }

        func release() {
            let c = clock.cond
            c.lock()
            if !released {
                released = true
                if counted { clock.held -= 1 }
            }
            c.broadcast()
            c.unlock()
        }
    }

    /// A hold in force from now (a main thread already busy, ahead of any work sent to it).
    func hold() -> Hold {
        let h = Hold(self)
        cond.lock(); h.counted = true; held += 1; cond.unlock()
        return h
    }

    /// A hold in force from now that an event releases ms from now (a main thread busy that
    /// long).
    func hold(forMs ms: Int) -> Hold {
        let h = hold()
        at(ms) { h.release() }
        return h
    }

    /// A hold in force from the moment a thread waits on it (work that, once started, waits on
    /// an app that does not answer): until then nothing is held.
    func blocker() -> Hold { Hold(self) }

    /// The first event due by end, taken off the list, its time now. Under the lock.
    private func takeEvent(dueBy end: UInt64) -> (() -> Void)? {
        guard let i = events.indices.min(by: { (events[$0].due, events[$0].n) < (events[$1].due, events[$1].n) }),
              events[i].due <= end else { return nil }
        let e = events.remove(at: i)
        _now = max(_now, e.due)
        return e.run
    }

    /// A fake's wait: check, then each event due within ms, in order, until check holds; else
    /// time moves to the end. No real time passes.
    func waitFor(ms: Int, _ check: () -> Bool) -> Bool {
        if check() { return true }
        cond.lock()
        let end = _now + UInt64(max(ms, 0)) * 1_000_000
        cond.unlock()
        while true {
            cond.lock()
            guard let run = takeEvent(dueBy: end) else {
                _now = max(_now, end)
                cond.unlock()
                return check()
            }
            cond.unlock()
            run()
            if check() { return true }
        }
    }

    /// ms on, running what falls due.
    func advance(ms: Int) { _ = waitFor(ms: ms) { false } }

    // AgentClock: Bounded.run's wait for its work.
    func wait(_ s: DispatchSemaphore, until deadline: UInt64) -> Bool {
        let giveUp = Date().addingTimeInterval(VirtualClock.hangGuard)
        while true {
            if s.wait(timeout: .now() + .milliseconds(1)) == .success { return true }
            cond.lock()
            if held > 0 {
                if let run = takeEvent(dueBy: deadline) {
                    cond.unlock()
                    run()
                    continue
                }
                _now = max(_now, deadline)
                cond.unlock()
                return false
            }
            cond.unlock()
            if Date() > giveUp { XCTFail("work free to run did not end"); return false }
        }
    }
}

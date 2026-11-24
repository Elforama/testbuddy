import Foundation
import XCTest
@testable import TBAgentCore

final class VirtualClock: AgentClock {
    static let start: UInt64 = 1_000_000_000_000
    static let hangGuard: TimeInterval = 30

    let heldQueue: DispatchQueue?
    private let key = DispatchSpecificKey<ObjectIdentifier>()

    init(holding queue: DispatchQueue? = nil) {
        heldQueue = queue
        queue?.setSpecific(key: key, value: ObjectIdentifier(self))
    }

    fileprivate var onHeldQueue: Bool { DispatchQueue.getSpecific(key: key) == ObjectIdentifier(self) }

    private let cond = NSCondition()
    private var _now = VirtualClock.start
    private var events: [(due: UInt64, n: Int, run: () -> Void)] = []
    private var made = 0
    private var held = 0

    func now() -> UInt64 { cond.lock(); defer { cond.unlock() }; return _now }
    var elapsedMs: Int { Int((now() - VirtualClock.start) / 1_000_000) }

    func at(_ ms: Int, _ f: @escaping () -> Void) {
        cond.lock()
        made += 1
        events.append((_now + UInt64(max(ms, 0)) * 1_000_000, made, f))
        cond.unlock()
    }

    final class Hold {
        fileprivate unowned let clock: VirtualClock
        fileprivate var counted = false
        fileprivate var released = false
        fileprivate init(_ clock: VirtualClock) { self.clock = clock }

        func wait() {
            guard clock.onHeldQueue else {
                return XCTFail("a hold waited on off its clock's held queue (\(clock.heldQueue?.label ?? "none")): one held queue per clock")
            }
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

    func hold() -> Hold {
        if heldQueue == nil { XCTFail("a hold on a clock with no held queue") }
        let h = Hold(self)
        cond.lock(); h.counted = true; held += 1; cond.unlock()
        return h
    }

    func hold(forMs ms: Int) -> Hold {
        let h = hold()
        at(ms) { h.release() }
        return h
    }

    func blocker() -> Hold { Hold(self) }

    private func takeEvent(dueBy end: UInt64) -> (() -> Void)? {
        guard let i = events.indices.min(by: { (events[$0].due, events[$0].n) < (events[$1].due, events[$1].n) }),
              events[i].due <= end else { return nil }
        let e = events.remove(at: i)
        _now = max(_now, e.due)
        return e.run
    }

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

    func advance(ms: Int) { _ = waitFor(ms: ms) { false } }

    func skip(ms: Int) {
        cond.lock(); _now += UInt64(max(ms, 0)) * 1_000_000; cond.unlock()
    }

    func wait(_ s: DispatchSemaphore, until deadline: UInt64, on queue: DispatchQueue) -> Bool {
        let giveUp = Date().addingTimeInterval(VirtualClock.hangGuard)
        let holdable = queue === heldQueue
        while true {
            if s.wait(timeout: .now() + .milliseconds(1)) == .success { return true }
            cond.lock()
            if holdable, held > 0 {
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

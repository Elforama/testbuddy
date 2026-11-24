import Foundation

enum AppWait {
    static func notAnswering(_ doing: String, since start: UInt64, _ what: String, _ outcome: String,
                             ended: String = "did not start", now: UInt64 = UptimeClock.shared.now()) -> AgentError {
        AgentError(String(format: "%@ %@: the app did not answer accessibility for %.1f s (%@); %@",
                          doing, ended, seconds(since: start, now: now), what, outcome), transient: true)
    }

    static func notAnsweringAfter(_ doing: String, since start: UInt64, _ what: String, _ done: String,
                                  now: UInt64 = UptimeClock.shared.now()) -> AgentError {
        AgentError(String(format: "%@: %@, then the app did not answer accessibility for %.1f s (%@)",
                          doing, done, seconds(since: start, now: now), what))
    }

    static func seconds(since start: UInt64, now: UInt64) -> Double {
        now > start ? Double(now - start) / 1e9 : 0
    }

    static func read<T>(on queue: DispatchQueue, by: UInt64?, clock: AgentClock = UptimeClock.shared, _ f: @escaping () throws -> T,
                        else fail: (Bounded.TimedOut) -> Error) throws -> T {
        guard let by else {
            if queue === DispatchQueue.main && Thread.isMainThread { return try f() }
            return try queue.sync(execute: f)
        }
        do { return try Bounded.run(on: queue, by: by, clock: clock, f) } catch let t as Bounded.TimedOut { throw fail(t) }
    }
}

enum ScreenRead {
    static func run(on queue: DispatchQueue, by deadline: TypingDeadline?, clock: AgentClock = UptimeClock.shared,
                    _ write: @escaping (JW) throws -> Void) throws -> JW {
        let asked = clock.now()
        return try AppWait.read(on: queue, by: deadline?.answerBy, clock: clock, {
            let reached = clock.now()
            let j = JW(capacity: 4096).obj()
            try write(j)
            if let waited = mainWaitMs(asked: asked, reached: reached) { j.put("mainWaitMs", waited) }
            return j.end()
        }, else: { t in
            AppWait.notAnswering("reading the screen", since: deadline?.start ?? 0,
                                 t.started ? "taking the snapshot" : "waiting for the main thread, held by an earlier call to the app",
                                 "nothing was read", ended: "did not finish", now: clock.now())
        })
    }

    static let mainWaitShownMs = 1000

    static func mainWaitMs(asked: UInt64, reached: UInt64) -> Int? {
        let ms = reached > asked ? Int((reached - asked) / 1_000_000) : 0
        return ms >= mainWaitShownMs ? ms : nil
    }
}

struct InputNotSent: Error, CustomStringConvertible {
    let why: String
    var description: String { why }
}

enum PendingInput {
    static let maxWaitMs = 10_000

    static func waitMs(boundedBy ms: Int?) -> Int {
        guard let ms else { return maxWaitMs }
        return min(maxWaitMs, max(ms, 0))
    }

    static let minConfirmMs = 500

    static func confirmMs(ownMs: Int, leftMs: Int?) -> Int? {
        guard let leftMs else { return ownMs }
        if leftMs < minConfirmMs { return nil }
        return min(ownMs, leftMs)
    }
}

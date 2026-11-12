// Waiting on the app for the ops other than type and erase (issue #130): the tree read, which
// every command depends on, and clear, key and hideKeyboard.
//
// Every XCTest call the agent makes runs on its main thread and waits there for the app to
// answer accessibility; an app whose main thread is busy (bringing up a keyboard on a slow
// runner took ~21 s in CI, issue #46) answers none, and the calls queued behind it wait too.
// Issue #46 gave type and erase a deadline from the host's reply limit (replyMs, Typing.swift).
// The ops here get the same one: the host sends replyMs, the op answers 2 s before it
// (TypingDeadline), and a read the app has not answered by then is given up on (Bounded.run)
// with an error that says what it waited for. Before anything was sent the error is
// transient: the host may ask again. A host that sends no replyMs gets the old, unbounded wait.
import Foundation

enum AppWait {
    /// The error of an op that gave up on the app before it did anything: transient, the host
    /// may ask again. doing: "reading the screen", "pressing key 66"; what: what it waited
    /// for; outcome: what that means ("nothing was read", "the key was not pressed").
    static func notAnswering(_ doing: String, since start: UInt64, _ what: String, _ outcome: String,
                             ended: String = "did not start") -> AgentError {
        AgentError(String(format: "%@ %@: the app did not answer accessibility for %.1f s (%@); %@",
                          doing, ended, seconds(since: start), what, outcome), transient: true)
    }

    /// The error of an op that gave up on the app after it did something: not transient (asked
    /// again, it could do it twice).
    static func notAnsweringAfter(_ doing: String, since start: UInt64, _ what: String, _ done: String) -> AgentError {
        AgentError(String(format: "%@: %@, then the app did not answer accessibility for %.1f s (%@)",
                          doing, done, seconds(since: start), what))
    }

    static func seconds(since start: UInt64) -> Double {
        let now = DispatchTime.now().uptimeNanoseconds
        return now > start ? Double(now - start) / 1e9 : 0
    }

    /// f's result on queue (the main thread), given up on at by (uptime ns; nil: no limit, as
    /// for a host that sends no replyMs). On giving up, the error of `fail`, told whether f had
    /// started.
    static func read<T>(on queue: DispatchQueue, by: UInt64?, _ f: @escaping () throws -> T,
                        else fail: (Bounded.TimedOut) -> Error) throws -> T {
        guard let by else {
            if queue === DispatchQueue.main && Thread.isMainThread { return try f() }
            return try queue.sync(execute: f)
        }
        do { return try Bounded.run(on: queue, by: by, f) } catch let t as Bounded.TimedOut { throw fail(t) }
    }
}

/// A tree read (the tree op), the read every command depends on.
enum ScreenRead {
    /// Runs write (the snapshot and its JSON) on queue, into a writer of its own, and returns
    /// that writer, a finished object whose fields the op adds to its reply (JW.fields). With a
    /// deadline, a read the app has not answered by deadline.answerBy is given up on: the op
    /// answers, saying whether it waited for the main thread (an earlier XCTest call still
    /// waiting on the app) or in the snapshot; the late snapshot finishes into its own writer,
    /// never the reply.
    static func run(on queue: DispatchQueue, by deadline: TypingDeadline?, _ write: @escaping (JW) throws -> Void) throws -> JW {
        try AppWait.read(on: queue, by: deadline?.answerBy, {
            let j = JW(capacity: 4096).obj()
            try write(j)
            return j.end()
        }, else: { t in
            AppWait.notAnswering("reading the screen", since: deadline?.start ?? 0,
                                 t.started ? "taking the snapshot" : "waiting for the main thread, held by an earlier call to the app",
                                 "nothing was read", ended: "did not finish")
        })
    }
}

/// A record not played because the one before it was still unconfirmed when the op's time ran
/// out (Input.play): none of its events went.
struct InputNotSent: Error, CustomStringConvertible {
    let why: String
    var description: String { why }
}

/// Before a record plays, the last one testmanagerd has not confirmed yet is waited for:
/// overlapping records could mix their events (Input.play).
enum PendingInput {
    /// How long, at most: as before issue #130.
    static let maxWaitMs = 10_000

    /// How long to wait for the previous record: maxWaitMs, and no longer than the time the
    /// op gave this record (ms: its confirmation timeout cut to the op's deadline, issue #130;
    /// nil: an op without one).
    static func waitMs(boundedBy ms: Int?) -> Int {
        guard let ms else { return maxWaitMs }
        return min(maxWaitMs, max(ms, 0))
    }
}

import Foundation
import XCTest
@testable import TBAgentCore

/// Issue #130: after #46 bounded type and erase, the tree read (which every command depends
/// on), clear, key and hideKeyboard still waited on the agent's main thread with no limit. An
/// XCTest read there waits for the app to answer accessibility, which an app whose main thread
/// is busy does not do: the host timed out blind ("agent did not answer"), and the request it
/// gave up on held the next ones. They answer by the host's reply limit now, saying what they
/// waited for.
final class AppWaitTests: XCTestCase {
    /// A main thread held by an earlier XCTest call until release is called, or for at most ms.
    private func busyMain(forMs ms: Int = 4000) -> (DispatchQueue, release: () -> Void) {
        let q = DispatchQueue(label: "fake main thread")
        let gate = DispatchSemaphore(value: 0)
        q.async { _ = gate.wait(timeout: .now() + .milliseconds(ms)) }
        return (q, { gate.signal() })
    }

    private func deadline(answerInMs ms: Int) -> TypingDeadline {
        let now = DispatchTime.now().uptimeNanoseconds
        return TypingDeadline(start: now, answerBy: now + UInt64(ms) * 1_000_000, keysMs: 0)
    }

    /// Runs f on another thread (the ops queue) and waits up to ms for it: nil if it has not
    /// answered by then.
    private func answer<T>(within ms: Int, _ f: @escaping () throws -> T) -> Result<T, Error>? {
        let done = DispatchSemaphore(value: 0)
        var result: Result<T, Error>?
        DispatchQueue.global().async {
            result = Result { try f() }
            done.signal()
        }
        return done.wait(timeout: .now() + .milliseconds(ms)) == .timedOut ? nil : result
    }

    /// The main thread is busy (the app does not answer the XCTest call ahead of the read): the
    /// read answers by its deadline, transient, saying it never reached the main thread.
    func testATreeReadAnswersByItsDeadlineWhileTheMainThreadIsBusy() {
        let (q, release) = busyMain()
        defer { release() }
        var wrote = false
        let started = Date()
        guard let r = answer(within: 2500, { try ScreenRead.run(on: q, by: self.deadline(answerInMs: 500)) { _ in wrote = true } }) else {
            return XCTFail("the tree read did not answer within 2.5 s (the main thread stayed busy)")
        }
        let took = Date().timeIntervalSince(started)
        XCTAssertLessThan(took, 1.5, "answered after \(took) s, past its deadline")
        guard case .failure(let err) = r else { return XCTFail("the read succeeded on a busy main thread") }
        XCTAssertEqual((err as? AgentError)?.transient, true, "nothing was read: the host may ask again (\(err))")
        XCTAssertTrue("\(err)".contains("reading the screen"), "\(err)")
        XCTAssertTrue("\(err)".contains("the app did not answer accessibility"), "\(err)")
        XCTAssertTrue("\(err)".contains("waiting for the main thread"), "the error says what it waited for: \(err)")
        release()
        q.sync {}
        XCTAssertFalse(wrote, "the abandoned read ran after the op answered")
    }

    /// The snapshot itself waits for the app: the read answers by its deadline, saying so, and
    /// the late snapshot does not touch the reply.
    func testATreeReadAnswersByItsDeadlineWhileTheSnapshotWaits() {
        let q = DispatchQueue(label: "fake main thread")
        let app = DispatchSemaphore(value: 0)
        guard let r = answer(within: 2500, {
            try ScreenRead.run(on: q, by: self.deadline(answerInMs: 500)) { w in
                _ = app.wait(timeout: .now() + 4)
                w.put("windows", "late")
            }
        }) else {
            app.signal()
            return XCTFail("the tree read did not answer within 2.5 s (the snapshot waited for the app)")
        }
        app.signal()
        q.sync {}
        guard case .failure(let err) = r else { return XCTFail("the read succeeded although the snapshot never ended") }
        XCTAssertTrue("\(err)".contains("taking the snapshot"), "the error says what it waited for: \(err)")
        XCTAssertEqual((err as? AgentError)?.transient, true, "\(err)")
    }

    /// A read in time gives its fields, which the op adds to its reply.
    func testATreeReadInTimeGivesItsFields() throws {
        let q = DispatchQueue(label: "fake main thread")
        let fields = try ScreenRead.run(on: q, by: deadline(answerInMs: 2000)) { w in w.put("seq", 3).put("keyboard", false) }
        let reply = JW().obj().put("id", 7).put("ok", true).fields(of: fields).put("seq", 4).end()
        XCTAssertEqual(String(decoding: reply.bytes, as: UTF8.self), #"{"id":7,"ok":true,"seq":3,"keyboard":false,"seq":4}"#)
        // Without a deadline (a host that sends no replyMs) the read waits, as before.
        XCTAssertEqual(String(decoding: try ScreenRead.run(on: q, by: nil) { $0.put("a", 1) }.bytes, as: UTF8.self), #"{"a":1}"#)
    }

    /// Item 2: Input.play waited up to 10 s for an earlier record testmanagerd had not
    /// confirmed, whatever the op's deadline. With one, it waits no longer than the time the
    /// op gave the record; without one, 10 s as before.
    func testThePendingRecordWaitIsBoundedByTheOpsDeadline() {
        XCTAssertEqual(PendingInput.waitMs(boundedBy: 1500), 1500)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: 60_000), 10_000)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: -5), 0)
        XCTAssertEqual(PendingInput.waitMs(boundedBy: nil), 10_000)
    }

    /// An op told it may take longer than the stuck limit (replyMs) is not stuck before then.
    func testAnOpIsNotStuckBeforeItsReplyLimit() {
        XCTAssertEqual(Vitals.limitMs(Request(raw: ["op": "tree"])), Vitals.stuckAfterMs)
        XCTAssertEqual(Vitals.limitMs(Request(raw: ["op": "tree", "replyMs": 100_000])), 100_000 + Vitals.stuckMarginMs)
    }

    /// The error of an op that gave up on the app: how long, what it waited for, what was done.
    func testTheErrorSaysWhatWasWaitedFor() {
        let start = DispatchTime.now().uptimeNanoseconds - 2_500_000_000
        let e = AppWait.notAnswering("pressing key 66", since: start, "checking which app is in front", "the key was not pressed")
        XCTAssertTrue(e.transient)
        XCTAssertTrue(e.message.hasPrefix("pressing key 66 did not start: the app did not answer accessibility for 2.5 s (checking which app is in front); the key was not pressed"), e.message)
    }
}

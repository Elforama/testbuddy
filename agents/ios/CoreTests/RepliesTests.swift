import Foundation
import XCTest
@testable import TBAgentCore

/// A request the host sends again after its connection dropped is answered from its first run.
final class RepliesTests: XCTestCase {
    private func bytes(_ s: String) -> [UInt8] { Array(s.utf8) }

    func testARememberedReplyIsFound() {
        var r = Replies()
        XCTAssertNil(r.earlier("a"))
        r.remember("a", bytes(#"{"id":1,"ok":true}"#), Data([1, 2]))
        XCTAssertEqual(r.earlier("a")?.reply, bytes(#"{"id":1,"ok":true}"#))
        XCTAssertEqual(r.earlier("a")?.bin, Data([1, 2]))
    }

    func testOnlyTheLatestAreKept() {
        var r = Replies()
        for i in 0...Replies.kept { r.remember("r\(i)", bytes("{}"), nil) }
        XCTAssertEqual(r.count, Replies.kept)
        XCTAssertNil(r.earlier("r0"))
        XCTAssertNotNil(r.earlier("r1"))
        XCTAssertNotNil(r.earlier("r\(Replies.kept)"))
    }

    func testWithIdReaddressesAReply() {
        let reply = bytes(#"{"id":1,"ok":true,"seq":4}"#)
        XCTAssertEqual(String(decoding: Replies.withId(reply, 77), as: UTF8.self), #"{"id":77,"ok":true,"seq":4}"#)
        XCTAssertEqual(Replies.withId(bytes("{}"), 5), bytes("{}"))
    }
}

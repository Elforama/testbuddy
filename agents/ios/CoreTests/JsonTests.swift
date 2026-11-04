import Foundation
import XCTest
@testable import TBAgentCore

/// The agent's replies are written by JW: tb reads every screen through it, so a bad escape or
/// a misplaced comma breaks every read.
final class JsonWriterTests: XCTestCase {
    private func text(_ w: JW) -> String { String(decoding: w.bytes, as: UTF8.self) }

    func testObjectsArraysAndCommas() {
        let w = JW().obj()
        w.put("id", 7).put("ok", true).put("x", 1.5).put("n", 2.0)
        w.key("windows").arr().obj().put("t", "a").end().obj().end().end()
        w.key("none").null()
        w.end()
        XCTAssertEqual(text(w), #"{"id":7,"ok":true,"x":1.5,"n":2,"windows":[{"t":"a"},{}],"none":null}"#)
    }

    func testEscapesAndParsesBack() throws {
        let tricky = "quote \" backslash \\ newline \n tab \t bell \u{07} emoji 🙂 é"
        let w = JW().obj().put("t", tricky).end()
        let back = try JSONSerialization.jsonObject(with: Data(w.bytes)) as? [String: String]
        XCTAssertEqual(back?["t"], tricky)
        XCTAssertTrue(text(w).contains(#"\u0007"#))
    }

    func testOptLeavesOutEmptyStrings() {
        let w = JW().obj().opt("t", "").opt("cd", "Label").end()
        XCTAssertEqual(text(w), #"{"cd":"Label"}"#)
    }

    func testNumbersThatJSONCannotHold() {
        let w = JW().arr().num(.infinity).num(.nan).num(-3).end()
        XCTAssertEqual(text(w), "[0,0,-3]")
    }

    func testRawEmbedsEncodedJSON() {
        let inner = JW().arr().int(1).int(2).end()
        let w = JW().obj().key("nodes").raw(inner.bytes).put("after", 1).end()
        XCTAssertEqual(text(w), #"{"nodes":[1,2],"after":1}"#)
    }
}

/// Requests from tb: typed reads with defaults, and errors for missing required fields.
final class RequestTests: XCTestCase {
    private func request(_ json: String) throws -> Request {
        Request(raw: try JSONSerialization.jsonObject(with: Data(json.utf8)) as! [String: Any])
    }

    func testTypedFieldsAndDefaults() throws {
        let r = try request(#"{"id": 12, "op": "tap", "x": 10.5, "hold": 20, "sync": true, "keys": ["a", 1, "b"], "env": {"A": "1", "B": 2}, "none": null}"#)
        XCTAssertEqual(r.id, 12)
        XCTAssertEqual(r.op, "tap")
        XCTAssertEqual(r.double("x", 0), 10.5)
        XCTAssertEqual(r.int("hold", 0), 20)
        XCTAssertEqual(r.int("missing", 5), 5)
        XCTAssertTrue(r.bool("sync", false))
        XCTAssertEqual(r.strings("keys"), ["a", "b"])
        XCTAssertEqual(r.dict("env"), ["A": "1", "B": "2"])
        XCTAssertTrue(r.has("x"))
        XCTAssertFalse(r.has("none"))
        XCTAssertFalse(r.has("missing"))
    }

    func testRequiredFields() throws {
        let r = try request(#"{"op": "type", "text": ""}"#)
        XCTAssertThrowsError(try r.need("x")) { XCTAssertEqual("\($0)", #"missing number "x""#) }
        XCTAssertThrowsError(try r.needStr("text"))
        XCTAssertEqual(r.id, 0)
    }
}

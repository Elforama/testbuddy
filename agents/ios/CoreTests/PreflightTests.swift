import XCTest
@testable import TBAgentCore

/// A node of a fake snapshot.
private struct Node: PreflightNode {
    var isButton = false
    var identifier = ""
    var isEnabled = true
    var frame = CGRect.zero
    var children: [Node] = []
}

final class PreflightTests: XCTestCase {
    private let card = CGRect(x: 16, y: 200, width: 370, height: 180)
    private let add = CGRect(x: 32, y: 330, width: 120, height: 34)

    /// A card that is itself a button, with a button inside it (as SwiftUI's
    /// Button { VStack { ...; Button("Add to list →") } }).
    private func screen(addEnabled: Bool = true, inner: [Node] = []) -> Node {
        let addButton = Node(isButton: true, identifier: "recipes_featured_add_to_list", isEnabled: addEnabled, frame: add,
                             children: [Node(identifier: "", frame: add)])
        let cardButton = Node(isButton: true, identifier: "recipes_featured_card", frame: card,
                              children: [Node(frame: CGRect(x: 32, y: 210, width: 200, height: 20)), addButton] + inner)
        return Node(frame: CGRect(x: 0, y: 0, width: 402, height: 874),
                    children: [Node(frame: CGRect(x: 0, y: 0, width: 402, height: 874), children: [cardButton])])
    }

    /// Issue #39: a button inside another button was never found (the search stopped at the
    /// card), so every tap on it by id was withheld before the touch, though nothing moved.
    func testFindsAButtonInsideAButton() {
        let point = CGPoint(x: add.midX, y: add.midY)
        XCTAssertEqual(Preflight.check(screen(), id: "recipes_featured_add_to_list", point: point, frontPid: 7, pid: 7),
                       .tap(frame: add))
        // The outer one still is found.
        XCTAssertEqual(Preflight.check(screen(), id: "recipes_featured_card", point: CGPoint(x: card.midX, y: 220), frontPid: 7, pid: 7),
                       .tap(frame: card))
    }

    /// Issue #39: a withheld touch says why, with the live frame when one button was found.
    func testSaysWhyATouchIsWithheld() {
        let id = "recipes_featured_add_to_list"
        let on = CGPoint(x: add.midX, y: add.midY)
        XCTAssertEqual(Preflight.check(screen(), id: id, point: on, frontPid: 8, pid: 7),
                       .withheld(reason: Preflight.otherProcess, frame: nil))
        XCTAssertEqual(Preflight.check(screen(), id: "missing", point: on, frontPid: 7, pid: 7),
                       .withheld(reason: Preflight.notFound, frame: nil))
        let twice = screen(inner: [Node(isButton: true, identifier: id, frame: CGRect(x: 200, y: 330, width: 100, height: 34))])
        XCTAssertEqual(Preflight.check(twice, id: id, point: on, frontPid: 7, pid: 7),
                       .withheld(reason: Preflight.notUnique(2), frame: nil))
        XCTAssertEqual(Preflight.check(screen(addEnabled: false), id: id, point: on, frontPid: 7, pid: 7),
                       .withheld(reason: Preflight.disabled, frame: add))
        XCTAssertEqual(Preflight.check(screen(), id: id, point: CGPoint(x: 300, y: 600), frontPid: 7, pid: 7),
                       .withheld(reason: Preflight.offTarget, frame: add))
    }
}

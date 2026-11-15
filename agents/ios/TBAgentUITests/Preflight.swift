// The agent's own check before a tap on a named button (the tap op's targetID,
// docs/protocol.md "iOS targeted input"): the button, found in a live read of the app, must be
// the only one with that id, enabled, and under the point. The decision is here, without
// XCTest; Tree.containsTap reads the app and wraps XCTest's snapshot.
import CoreGraphics

/// A node of a snapshot of the app, as the preflight reads it.
protocol PreflightNode {
    var isButton: Bool { get }
    var identifier: String { get }
    var isEnabled: Bool { get }
    var frame: CGRect { get }
    var children: [Self] { get }
}

/// What the preflight decided: tap, with the button's live frame, or withhold the touch, saying
/// why (issue #39: the reason went nowhere, and the host said the target was moving).
enum Preflight: Equatable {
    case tap(frame: CGRect)
    /// frame: the button's live frame, when one button was found.
    case withheld(reason: String, frame: CGRect?)

    static let otherProcess = "another app is in front"
    static let notFound = "no button with this id in the app"
    static func notUnique(_ n: Int) -> String { "\(n) buttons with this id" }
    static let disabled = "the button is disabled"
    static let offTarget = "the point is off the button's live frame"

    /// The check over one snapshot of the app (root). frontPid: the app in front now; pid: the
    /// app the host read the button in.
    static func check<N: PreflightNode>(_ root: N, id: String, point: CGPoint, frontPid: Int32, pid: Int32) -> Preflight {
        guard frontPid == pid else { return .withheld(reason: otherProcess, frame: nil) }
        let matches = buttons(root, id: id)
        guard matches.count == 1 else {
            return .withheld(reason: matches.isEmpty ? notFound : notUnique(matches.count), frame: nil)
        }
        let b = matches[0]
        guard b.isEnabled else { return .withheld(reason: disabled, frame: b.frame) }
        guard b.frame.contains(point) else { return .withheld(reason: offTarget, frame: b.frame) }
        return .tap(frame: b.frame)
    }

    /// The buttons with this id, a button inside another button's subtree included (issue #39:
    /// the search stopped at the first button, so a card that is a button hid the button in it).
    static func buttons<N: PreflightNode>(_ root: N, id: String) -> [N] {
        var out: [N] = []
        var stack = [root]
        while let n = stack.popLast() {
            if n.isButton && n.identifier == id { out.append(n) }
            stack.append(contentsOf: n.children.reversed())
        }
        return out
    }
}

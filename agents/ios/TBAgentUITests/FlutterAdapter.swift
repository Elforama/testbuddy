// Flutter's scroll physics can turn the ordinary end of a drag into a fling. Ask the
// in-process adapter about the point first. A missing bridge, a native child or an
// unresponsive process always uses the platform gesture.
import XCTest
import notify

enum FlutterAdapter {
    static let dragSpeed = 80.0
    private static let requests = NSLock()

    // XCTest flattens a Flutter list into an empty scroll view followed by its rows.
    // Only restore that edge inside a confirmed Flutter surface, and only when every
    // sibling fits the viewport. Nested lists are handled by the same preorder walk.
    static func isListGroup(_ children: [XCUIElementSnapshot], pid: Int32, window: CGRect) -> Bool {
        guard children.count > 1, let list = children.first, list.elementType == .scrollView,
              list.children.isEmpty, !list.frame.isEmpty else { return false }
        let clip = list.frame.insetBy(dx: -1, dy: -1)
        let visible = list.frame.intersection(window)
        guard !visible.isNull, !visible.isEmpty else { return false }
        var found = false
        for row in children.dropFirst() where !row.frame.isEmpty {
            guard clip.contains(row.frame) else { return false }
            found = true
        }
        return found && ownsPoint(pid: pid, x: visible.midX, y: visible.midY)
    }

    static func ownsPoint(pid: Int32, x: Double, y: Double) -> Bool {
        guard pid > 0, let px = Int16(exactly: x.rounded()), let py = Int16(exactly: y.rounded()) else { return false }
        requests.lock(); defer { requests.unlock() }
        let prefix = "dev.testbuddy.flutter.\(pid)"
        let answer = prefix + ".answer"
        var check: Int32 = 0, state: UInt64 = 0
        guard notify_register_check(answer, &check) == NOTIFY_STATUS_OK else { return false }
        defer { notify_cancel(check) }
        guard notify_get_state(check, &state) == NOTIFY_STATUS_OK, state != 0 else { return false }

        let nonce = UInt64(UInt32.random(in: 2...UInt32.max))
        let done = DispatchSemaphore(value: 0)
        var listener: Int32 = 0
        guard notify_register_dispatch(answer, &listener, .global(), { token in
            var value: UInt64 = 0
            if notify_get_state(token, &value) == NOTIFY_STATUS_OK, value >> 1 == nonce { done.signal() }
        }) == NOTIFY_STATUS_OK else { return false }
        defer { notify_cancel(listener) }
        var request: Int32 = 0
        guard notify_register_check(prefix, &request) == NOTIFY_STATUS_OK else { return false }
        defer { notify_cancel(request) }
        let query = nonce << 32 | UInt64(UInt16(bitPattern: px)) << 16 | UInt64(UInt16(bitPattern: py))
        guard notify_set_state(request, query) == NOTIFY_STATUS_OK, notify_post(prefix) == NOTIFY_STATUS_OK,
              done.wait(timeout: .now() + .milliseconds(80)) == .success,
              notify_get_state(check, &state) == NOTIFY_STATUS_OK else { return false }
        return state == (nonce << 1 | 1)
    }
}

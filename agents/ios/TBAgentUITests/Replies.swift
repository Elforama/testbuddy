// The replies to the last few requests that carried a token ("rid": the ops that change the
// device), across connections. When the host's connection drops it reconnects and sends the
// request again; it is answered from here instead of tapping or typing twice.
import Foundation

struct Replies {
    static let kept = 64

    private var byRid: [String: (reply: [UInt8], bin: Data?)] = [:]
    private var order: [String] = []

    var count: Int { byRid.count }

    /// The first run's reply to the request with this token, if it is still kept.
    func earlier(_ rid: String) -> (reply: [UInt8], bin: Data?)? { byRid[rid] }

    /// Keeps a reply, forgetting the oldest beyond Replies.kept.
    mutating func remember(_ rid: String, _ reply: [UInt8], _ bin: Data?) {
        byRid[rid] = (reply, bin)
        order.append(rid)
        while order.count > Replies.kept {
            byRid.removeValue(forKey: order.removeFirst())
        }
    }

    /// A reply ({"id":N,...}) re-addressed to request id.
    static func withId(_ reply: [UInt8], _ id: Int64) -> [UInt8] {
        guard let comma = reply.firstIndex(of: 0x2C) else { return reply }
        return Array("{\"id\":\(id)".utf8) + reply[comma...]
    }
}

import Foundation

struct Replies {
    static let kept = 64

    private var byRid: [String: (reply: [UInt8], bin: Data?)] = [:]
    private var order: [String] = []

    var count: Int { byRid.count }

    func earlier(_ rid: String) -> (reply: [UInt8], bin: Data?)? { byRid[rid] }

    mutating func remember(_ rid: String, _ reply: [UInt8], _ bin: Data?) {
        byRid[rid] = (reply, bin)
        order.append(rid)
        while order.count > Replies.kept {
            byRid.removeValue(forKey: order.removeFirst())
        }
    }

    static func withId(_ reply: [UInt8], _ id: Int64) -> [UInt8] {
        guard let comma = reply.firstIndex(of: 0x2C) else { return reply }
        return Array("{\"id\":\(id)".utf8) + reply[comma...]
    }
}

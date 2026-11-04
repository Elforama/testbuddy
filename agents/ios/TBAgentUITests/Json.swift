// JSON in and out. Requests are small and parsed with JSONSerialization; responses (a tree
// can hold hundreds of nodes) are written straight into a byte buffer.
import Foundation

/// An error the agent reports to tb as a request's failure.
struct AgentError: Error, CustomStringConvertible {
    let message: String
    init(_ message: String) { self.message = message }
    var description: String { message }
}

/// A request: the decoded JSON object with typed accessors.
struct Request {
    let raw: [String: Any]
    var id: Int64 { (raw["id"] as? NSNumber)?.int64Value ?? 0 }
    var op: String { raw["op"] as? String ?? "" }

    func has(_ k: String) -> Bool { raw[k] != nil && !(raw[k] is NSNull) }
    func int(_ k: String, _ def: Int) -> Int { (raw[k] as? NSNumber)?.intValue ?? def }
    func int64(_ k: String, _ def: Int64) -> Int64 { (raw[k] as? NSNumber)?.int64Value ?? def }
    func double(_ k: String, _ def: Double) -> Double { (raw[k] as? NSNumber)?.doubleValue ?? def }
    func bool(_ k: String, _ def: Bool) -> Bool { (raw[k] as? NSNumber)?.boolValue ?? def }
    func str(_ k: String, _ def: String = "") -> String { raw[k] as? String ?? def }
    func strings(_ k: String) -> [String] { (raw[k] as? [Any])?.compactMap { $0 as? String } ?? [] }
    func ints(_ k: String) -> [Int] { (raw[k] as? [Any])?.compactMap { ($0 as? NSNumber)?.intValue } ?? [] }
    func dict(_ k: String) -> [String: String] {
        var out: [String: String] = [:]
        for (key, v) in raw[k] as? [String: Any] ?? [:] { out[key] = v as? String ?? "\(v)" }
        return out
    }

    func need(_ k: String) throws -> Double {
        guard let n = raw[k] as? NSNumber else { throw AgentError("missing number \"\(k)\"") }
        return n.doubleValue
    }

    func needStr(_ k: String) throws -> String {
        guard let s = raw[k] as? String, !s.isEmpty else { throw AgentError("missing string \"\(k)\"") }
        return s
    }
}

/// A streaming JSON writer. Commas are placed automatically.
final class JW {
    private(set) var bytes: [UInt8] = []
    private var first: [Bool] = []
    private var closers: [UInt8] = []
    private var afterKey = false

    init(capacity: Int = 256) { bytes.reserveCapacity(capacity) }

    private func sep() {
        if afterKey { afterKey = false; return }
        guard let f = first.last else { return }
        if f { first[first.count - 1] = false } else { bytes.append(0x2C) }
    }

    @discardableResult func obj() -> JW { sep(); bytes.append(0x7B); first.append(true); closers.append(0x7D); return self }
    @discardableResult func arr() -> JW { sep(); bytes.append(0x5B); first.append(true); closers.append(0x5D); return self }
    @discardableResult func end() -> JW { bytes.append(closers.removeLast()); first.removeLast(); return self }

    @discardableResult func key(_ k: String) -> JW { sep(); quoted(k); bytes.append(0x3A); afterKey = true; return self }

    @discardableResult func str(_ s: String) -> JW { sep(); quoted(s); return self }
    @discardableResult func int<T: BinaryInteger>(_ v: T) -> JW { sep(); bytes.append(contentsOf: Array(String(v).utf8)); return self }
    @discardableResult func num(_ v: Double) -> JW {
        sep()
        if v.isFinite {
            if v == v.rounded() && abs(v) < 1e15 { bytes.append(contentsOf: Array(String(Int64(v)).utf8)) }
            else { bytes.append(contentsOf: Array(String(v).utf8)) }
        } else { bytes.append(contentsOf: Array("0".utf8)) }
        return self
    }
    @discardableResult func bool(_ v: Bool) -> JW { sep(); bytes.append(contentsOf: Array((v ? "true" : "false").utf8)); return self }
    @discardableResult func null() -> JW { sep(); bytes.append(contentsOf: Array("null".utf8)); return self }

    // Shorthands for key/value pairs.
    @discardableResult func put(_ k: String, _ v: String) -> JW { key(k).str(v) }
    @discardableResult func put<T: BinaryInteger>(_ k: String, _ v: T) -> JW { key(k).int(v) }
    @discardableResult func put(_ k: String, _ v: Double) -> JW { key(k).num(v) }
    @discardableResult func put(_ k: String, _ v: Bool) -> JW { key(k).bool(v) }
    /// The key only when the string is not empty (the node format omits empty strings).
    @discardableResult func opt(_ k: String, _ v: String) -> JW { v.isEmpty ? self : put(k, v) }

    /// Raw, already-encoded JSON as the next value.
    @discardableResult func raw(_ json: [UInt8]) -> JW { sep(); bytes.append(contentsOf: json); return self }

    private func quoted(_ s: String) {
        bytes.append(0x22)
        for b in s.utf8 {
            switch b {
            case 0x22: bytes.append(0x5C); bytes.append(0x22)
            case 0x5C: bytes.append(0x5C); bytes.append(0x5C)
            case 0x0A: bytes.append(0x5C); bytes.append(0x6E)
            case 0x0D: bytes.append(0x5C); bytes.append(0x72)
            case 0x09: bytes.append(0x5C); bytes.append(0x74)
            case 0..<0x20:
                bytes.append(contentsOf: Array(String(format: "\\u%04x", b).utf8))
            default: bytes.append(b)
            }
        }
        bytes.append(0x22)
    }
}

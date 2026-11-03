// The TCP server: 127.0.0.1:<port>, one JSON object per line each way (docs/protocol.md).
// One host at a time: a new connection replaces the old one. Requests run in order on one
// queue, so pushed events keep flowing while an op waits.
import Foundation
import Darwin

final class Client {
    let fd: Int32
    private let writeLock = NSLock()
    private var closed = false

    init(fd: Int32) { self.fd = fd }

    /// Writes one line (and binary data after it). False once the connection is gone.
    @discardableResult
    func send(_ line: [UInt8], bin: Data? = nil) -> Bool {
        writeLock.lock()
        defer { writeLock.unlock() }
        if closed { return false }
        var buf = line
        buf.append(0x0A)
        if !writeAll(buf) { closed = true; return false }
        if let bin, !bin.isEmpty {
            let ok = bin.withUnsafeBytes { raw -> Bool in
                writeAll(UnsafeRawBufferPointer(start: raw.baseAddress, count: raw.count))
            }
            if !ok { closed = true; return false }
        }
        return true
    }

    private func writeAll(_ bytes: [UInt8]) -> Bool {
        bytes.withUnsafeBytes { writeAll($0) }
    }

    private func writeAll(_ p: UnsafeRawBufferPointer) -> Bool {
        var sent = 0
        while sent < p.count {
            let r = Darwin.send(fd, p.baseAddress! + sent, p.count - sent, 0)
            if r <= 0 {
                if r < 0 && errno == EINTR { continue }
                return false
            }
            sent += r
        }
        return true
    }

    func close() {
        writeLock.lock()
        if !closed { closed = true; shutdown(fd, SHUT_RDWR) }
        writeLock.unlock()
    }

    /// Reads lines until the connection closes, handing each to onLine.
    func readLines(_ onLine: (Data) -> Void) {
        var buf = Data()
        var chunk = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            let n = chunk.withUnsafeMutableBytes { recv(fd, $0.baseAddress!, $0.count, 0) }
            if n < 0 && errno == EINTR { continue }
            if n <= 0 { break }
            buf.append(chunk, count: n)
            while let nl = buf.firstIndex(of: 0x0A) {
                let line = buf[buf.startIndex..<nl]
                if !line.isEmpty { onLine(Data(line)) }
                buf.removeSubrange(buf.startIndex...nl)
            }
        }
        Darwin.close(fd)
    }
}

final class Server {
    private let port: UInt16
    private let idleSeconds: Double
    private var listener: Int32 = -1
    private let lock = NSLock()
    private var current: Client?
    private var idleTimer: DispatchSourceTimer?
    private let opsQueue = DispatchQueue(label: "tb.ops", qos: .userInitiated)
    private let ops: Ops

    /// The replies to the last few requests that carried a token ("rid": the ops that change
    /// the device), across connections. When the host's connection drops it reconnects and
    /// sends the request again; it is answered from here instead of tapping or typing twice.
    /// Only touched on opsQueue, which runs requests one at a time, so a first run has always
    /// finished when its repeat is handled.
    private var recent: [String: (reply: [UInt8], bin: Data?)] = [:]
    private var recentOrder: [String] = []
    private static let recentMax = 64

    init(port: UInt16, idleSeconds: Double, ops: Ops) {
        self.port = port
        self.idleSeconds = idleSeconds
        self.ops = ops
    }

    func start() throws {
        listener = socket(AF_INET, SOCK_STREAM, 0)
        guard listener >= 0 else { throw AgentError("socket: errno \(errno)") }
        var one: Int32 = 1
        setsockopt(listener, SOL_SOCKET, SO_REUSEADDR, &one, socklen_t(MemoryLayout<Int32>.size))
        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = port.bigEndian
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")
        let bound = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(listener, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        guard bound == 0, listen(listener, 8) == 0 else { throw AgentError("cannot listen on 127.0.0.1:\(port): errno \(errno)") }
        armIdle()
        let t = Thread { [weak self] in self?.acceptLoop() }
        t.name = "tb-accept"
        t.start()
    }

    /// Exits after idleSeconds with no client, so a forgotten runner does not linger. One timer,
    /// re-armed when the last client leaves and cancelled while one is connected.
    private func armIdle() {
        lock.lock(); defer { lock.unlock() }
        idleTimer?.cancel()
        let t = DispatchSource.makeTimerSource(queue: .global(qos: .utility))
        t.schedule(deadline: .now() + idleSeconds)
        t.setEventHandler { NSLog("tb: idle for \(Int(self.idleSeconds)) s, exiting"); Agent.shared.stop() }
        t.resume()
        idleTimer = t
    }

    private func acceptLoop() {
        while true {
            let fd = accept(listener, nil, nil)
            if fd < 0 { if errno == EINTR || errno == ECONNABORTED { continue }; NSLog("tb: accept failed: errno \(errno)"); return }
            var one: Int32 = 1
            setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &one, socklen_t(MemoryLayout<Int32>.size))
            setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout<Int32>.size))
            let c = Client(fd: fd)
            lock.lock()
            let old = current
            current = c
            idleTimer?.cancel()
            idleTimer = nil
            lock.unlock()
            old?.close()
            let t = Thread { [weak self] in self?.serve(c) }
            t.name = "tb-client"
            t.start()
        }
    }

    private func serve(_ c: Client) {
        // The hello is sent from the ops queue, so it is ordered before any response.
        opsQueue.async {
            Events.shared.client = c
            c.send(self.ops.hello())
        }
        c.readLines { line in
            opsQueue.async { self.handle(line, c) }
        }
        lock.lock()
        let last = current === c
        if last { current = nil }
        lock.unlock()
        if last {
            opsQueue.async { if Events.shared.client === c { Events.shared.client = nil } }
            armIdle()
        }
    }

    private func handle(_ line: Data, _ c: Client) {
        guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else {
            NSLog("tb: unreadable request: \(String(decoding: line, as: UTF8.self))")
            return
        }
        let req = Request(raw: obj)
        let rid = req.str("rid")
        if !rid.isEmpty, let earlier = recent[rid] {
            NSLog("tb: request \(rid) (\(req.op)) sent again: answering from its first run")
            c.send(Server.withId(earlier.reply, req.id), bin: earlier.bin)
            return
        }
        let w = JW(capacity: 1024)
        w.obj().put("id", req.id).put("ok", true)
        var reply: [UInt8]
        var bin: Data?
        do {
            bin = try ops.handle(req, w)
            if let bin { w.put("bin", bin.count) }
            w.end()
            reply = w.bytes
        } catch {
            bin = nil
            reply = JW().obj().put("id", req.id).put("ok", false).put("error", "\(error)").end().bytes
        }
        if !rid.isEmpty { remember(rid, reply, bin) }
        c.send(reply, bin: bin)
        if ops.exitRequested { Agent.shared.stop() }
    }

    private func remember(_ rid: String, _ reply: [UInt8], _ bin: Data?) {
        recent[rid] = (reply, bin)
        recentOrder.append(rid)
        while recentOrder.count > Server.recentMax {
            recent.removeValue(forKey: recentOrder.removeFirst())
        }
    }

    /// A reply ({"id":N,...}) re-addressed to request id.
    static func withId(_ reply: [UInt8], _ id: Int64) -> [UInt8] {
        guard let comma = reply.firstIndex(of: 0x2C) else { return reply }
        return Array("{\"id\":\(id)".utf8) + reply[comma...]
    }
}

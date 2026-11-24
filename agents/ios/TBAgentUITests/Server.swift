import Foundation
import Darwin

final class Client {
    let fd: Int32
    private let writeLock = NSLock()
    private var closed = false

    init(fd: Int32) { self.fd = fd }

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
    private var clients: Clients<Client>!
    private var idleTimer: DispatchSourceTimer?
    private let opsQueue: DispatchQueue
    private let dispatcher: Dispatcher
    private let ops: Ops

    private var recent = Replies()

    init(port: UInt16, idleSeconds: Double, ops: Ops) {
        self.port = port
        self.idleSeconds = idleSeconds
        self.ops = ops
        opsQueue = DispatchQueue(label: "tb.ops", qos: .userInitiated)
        dispatcher = Dispatcher(queue: opsQueue, vitals: ops.vitals)
        clients = Clients<Client>(
            events: { c in Events.shared.client = c },
            idle: { [unowned self] arm in if arm { self.armIdle(afterClient: true) } else { self.cancelIdle() } })
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

    static var keep = false
    static let orphanSeconds = 60.0

    private func armIdle(afterClient: Bool = false) {
        lock.lock(); defer { lock.unlock() }
        idleTimer?.cancel()
        let limit = afterClient && !Server.keep ? min(Server.orphanSeconds, idleSeconds) : idleSeconds
        let t = DispatchSource.makeTimerSource(queue: .global(qos: .utility))
        t.schedule(deadline: .now() + limit)
        t.setEventHandler { NSLog("tb: idle for \(Int(limit)) s, exiting"); Agent.shared.stop() }
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
            clients.arrived(c)
            let t = Thread { [weak self] in self?.serve(c) }
            t.name = "tb-client"
            t.start()
        }
    }

    private func serve(_ c: Client) {
        dispatcher.serve(
            greet: {
                c.send(self.ops.hello())
                self.clients.greeted(c)
            },
            read: c.readLines,
            seen: { req in
                self.clients.request(c, op: req.op)?.close()
            },
            now: { req in c.send(self.ops.live(req)) },
            queued: { req in self.handle(req, c) })
        clients.left(c)
    }

    private func cancelIdle() {
        lock.lock()
        idleTimer?.cancel()
        idleTimer = nil
        lock.unlock()
    }

    private func handle(_ req: Request, _ c: Client) {
        let rid = req.str("rid")
        if !rid.isEmpty, let earlier = recent.earlier(rid) {
            NSLog("tb: request \(rid) (\(req.op)) sent again: answering from its first run")
            c.send(Replies.withId(earlier.reply, req.id), bin: earlier.bin)
            return
        }
        let w = JW(capacity: 1024)
        w.obj().put("id", req.id).put("ok", true)
        var reply: [UInt8]
        var bin: Data?
        ops.vitals.begin(req.op, limitMs: Vitals.limitMs(req))
        do {
            bin = try ops.handle(req, w)
            if let bin { w.put("bin", bin.count) }
            w.end()
            reply = w.bytes
        } catch {
            bin = nil
            let r = JW().obj().put("id", req.id).put("ok", false).put("error", "\(error)")
            if (error as? AgentError)?.transient == true { r.put("transient", true) }
            reply = r.end().bytes
        }
        if !rid.isEmpty { recent.remember(rid, reply, bin) }
        ops.noteVitals()
        ops.vitals.end()
        c.send(reply, bin: bin)
        if ops.exitRequested { Agent.shared.stop() }
    }
}

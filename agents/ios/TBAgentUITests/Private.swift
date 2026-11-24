import XCTest
import ObjectiveC

enum RT {
    static let rtldDefault = UnsafeMutableRawPointer(bitPattern: -2)

    static func cls(_ name: String) throws -> NSObject.Type {
        guard let c = NSClassFromString(name) as? NSObject.Type else { throw AgentError("\(name) is missing") }
        return c
    }

    static func get(_ target: AnyObject, _ sel: String) -> AnyObject? {
        let s = NSSelectorFromString(sel)
        guard target.responds(to: s) else { return nil }
        return target.perform(s)?.takeUnretainedValue()
    }

    static func imp<T>(_ target: AnyObject, _ sel: String, as: T.Type) throws -> (T, Selector) {
        let s = NSSelectorFromString(sel)
        guard target.responds(to: s), let o = target as? NSObject else {
            throw AgentError("\(type(of: target)).\(sel) is missing")
        }
        return (unsafeBitCast(o.method(for: s), to: T.self), s)
    }

    static func cFunction<T>(_ name: String, as: T.Type) -> T? {
        guard let p = dlsym(rtldDefault, name) else { return nil }
        return unsafeBitCast(p, to: T.self)
    }

    static func methods(of className: String) -> [String] {
        guard let c = NSClassFromString(className) else { return ["(no class \(className))"] }
        var out: [String] = []
        for (target, prefix): (AnyClass, String) in [(c, "-"), (object_getClass(c)!, "+")] {
            var n: UInt32 = 0
            if let list = class_copyMethodList(target, &n) {
                for i in 0..<Int(n) {
                    let m = list[i]
                    let enc = method_getTypeEncoding(m).map { String(cString: $0) } ?? ""
                    out.append(prefix + NSStringFromSelector(method_getName(m)) + " " + enc)
                }
                free(list)
            }
        }
        var sup: AnyClass? = class_getSuperclass(c)
        var chain: [String] = []
        while let s = sup { chain.append(NSStringFromClass(s)); sup = class_getSuperclass(s) }
        return out.sorted() + ["superclasses: " + chain.joined(separator: " < ")]
    }

    static func protocolMethods(_ name: String) -> [String] {
        guard let p = objc_getProtocol(name) else { return ["(no protocol \(name))"] }
        var out: [String] = []
        for (req, inst) in [(true, true), (false, true), (true, false), (false, false)] {
            var n: UInt32 = 0
            if let list = protocol_copyMethodDescriptionList(p, req, inst, &n) {
                for i in 0..<Int(n) {
                    let d = list[i]
                    let types = d.types.map { String(cString: $0) } ?? ""
                    out.append((inst ? "-" : "+") + NSStringFromSelector(d.name!) + " " + types)
                }
                free(list)
            }
        }
        return out.sorted()
    }
}

enum Private {
    static func session() throws -> NSObject {
        let c = try RT.cls("XCTRunnerDaemonSession")
        guard let s = RT.get(c, "sharedSession") as? NSObject else { throw AgentError("XCTRunnerDaemonSession.sharedSession is missing") }
        return s
    }

    static func proxy() throws -> NSObject {
        guard let p = RT.get(try session(), "daemonProxy") as? NSObject else { throw AgentError("XCTRunnerDaemonSession.daemonProxy is missing") }
        return p
    }

    static func axClient() throws -> NSObject {
        guard let c = RT.get(XCUIDevice.shared, "accessibilityInterface") as? NSObject else {
            throw AgentError("XCUIDevice.accessibilityInterface is missing")
        }
        return c
    }

    @discardableResult
    static func setAXTimeout(_ seconds: Double) -> Bool {
        guard let client = try? axClient() else { return false }
        if let (f, s) = try? RT.imp(client, "_setAXTimeout:error:", as: (@convention(c) (NSObject, Selector, Double, UnsafeMutablePointer<NSError?>?) -> Bool).self) {
            var err: NSError?
            return f(client, s, seconds, &err)
        }
        if let (f, s) = try? RT.imp(client, "setAXTimeout:", as: (@convention(c) (NSObject, Selector, Double) -> Void).self) {
            f(client, s, seconds)
            return true
        }
        return false
    }

    struct ActiveApp { let pid: Int32; let element: NSObject }

    static func activeApps() -> [ActiveApp] {
        guard let client = try? axClient(), let list = RT.get(client, "activeApplications") as? [NSObject] else { return [] }
        return list.compactMap { e in pid(of: e).map { ActiveApp(pid: $0, element: e) } }
    }

    static func systemApplicationPid() -> Int32? {
        guard let client = try? axClient(), let e = RT.get(client, "systemApplication") as? NSObject else { return nil }
        return pid(of: e)
    }

    static func pid(of element: NSObject) -> Int32? {
        guard let (f, s) = try? RT.imp(element, "processIdentifier", as: (@convention(c) (NSObject, Selector) -> Int32).self) else { return nil }
        return f(element, s)
    }

    private static let bundleIDs = PerPid<String>()

    static func bundleID(pid: Int32) -> String? {
        if let id = bundleIDs[pid] { return id }
        guard let proxy = try? proxy(),
              let (f, s) = try? RT.imp(proxy, "_XCT_requestBundleIDForPID:reply:", as: (@convention(c) (NSObject, Selector, Int32, @escaping (NSString?, NSError?) -> Void) -> Void).self)
        else { return nil }
        let done = DispatchSemaphore(value: 0)
        var id: String?
        f(proxy, s, pid) { b, _ in id = b as String?; done.signal() }
        _ = done.wait(timeout: .now() + 2)
        if let id {
            bundleIDs.update(pid) { $0 = id }
        }
        return id
    }

    static func process(of app: XCUIApplication) -> NSObject? {
        for path in [["applicationImpl", "currentProcess"], ["currentProcess"], ["applicationProcess"], ["applicationImpl", "applicationProcess"]] {
            var o: AnyObject? = app
            for name in path {
                guard let cur = o else { break }
                o = RT.get(cur, name)
            }
            if let p = o as? NSObject { return p }
        }
        return nil
    }

    static func accessibilityElement(ofProcess p: NSObject) -> NSObject? {
        RT.get(p, "accessibilityElement") as? NSObject
    }

    static func element(pid: Int32) -> NSObject? {
        guard let c = NSClassFromString("XCAccessibilityElement") as? NSObject.Type,
              let (f, s) = try? RT.imp(c, "elementWithProcessIdentifier:", as: (@convention(c) (AnyObject, Selector, Int32) -> Unmanaged<NSObject>?).self)
        else { return nil }
        return f(c, s, pid)?.takeUnretainedValue()
    }

    static func notifyWhenRunLoopIdle(_ process: NSObject, _ done: @escaping (Error?) -> Void) throws {
        let (f, s) = try RT.imp(process, "_notifyWhenMainRunLoopIsIdle:", as: (@convention(c) (NSObject, Selector, @escaping (NSError?) -> Void) -> Void).self)
        f(process, s) { done($0) }
    }

    static func notifyWhenAnimationsIdle(_ process: NSObject, _ done: @escaping (Error?) -> Void) throws {
        let (f, s) = try RT.imp(process, "_notifyWhenAnimationsAreIdle:", as: (@convention(c) (NSObject, Selector, @escaping (NSError?) -> Void) -> Void).self)
        f(process, s) { done($0) }
    }

    static func supportsAnimationsIdle(_ process: NSObject) -> Bool {
        guard let (f, s) = try? RT.imp(process, "_supportsAnimationsIdleNotifications", as: (@convention(c) (NSObject, Selector) -> Bool).self) else { return false }
        return f(process, s)
    }

    static func touchPath(at p: CGPoint, offset: TimeInterval = 0) throws -> NSObject {
        let c = try RT.cls("XCPointerEventPath")
        let alloc = c.perform(NSSelectorFromString("alloc")).takeUnretainedValue() as! NSObject
        let (f, s) = try RT.imp(alloc, "initForTouchAtPoint:offset:", as: (@convention(c) (NSObject, Selector, CGPoint, TimeInterval) -> NSObject).self)
        return f(alloc, s, p, offset)
    }

    static func lift(_ path: NSObject, at offset: TimeInterval) throws {
        let (f, s) = try RT.imp(path, "liftUpAtOffset:", as: (@convention(c) (NSObject, Selector, TimeInterval) -> Void).self)
        f(path, s, offset)
    }

    static func move(_ path: NSObject, to p: CGPoint, at offset: TimeInterval) throws {
        let (f, s) = try RT.imp(path, "moveToPoint:atOffset:", as: (@convention(c) (NSObject, Selector, CGPoint, TimeInterval) -> Void).self)
        f(path, s, p, offset)
    }

    static func explicitDrag(_ points: [(p: CGPoint, offset: TimeInterval, duration: TimeInterval)], liftAt: TimeInterval) throws -> NSObject? {
        guard let first = points.first, let last = points.last, let event = NSClassFromString("XCPointerEvent") else { return nil }
        let makeSel = NSSelectorFromString("moveEventWithStartPoint:destination:offset:duration:")
        let addSel = NSSelectorFromString("_addPointerEvent:")
        guard let make = class_getClassMethod(event, makeSel) else { return nil }
        let path = try touchPath(at: first.p)
        guard path.responds(to: addSel) else { return nil }
        typealias Make = @convention(c) (AnyClass, Selector, CGPoint, CGPoint, TimeInterval, TimeInterval) -> NSObject
        typealias Add = @convention(c) (NSObject, Selector, NSObject) -> Void
        let move = unsafeBitCast(method_getImplementation(make), to: Make.self)
        let add = unsafeBitCast(path.method(for: addSel), to: Add.self)
        for (a, b) in zip(points, points.dropFirst()) {
            add(path, addSel, move(event, makeSel, a.p, b.p, b.offset, b.duration))
        }
        let at = try touchPath(at: last.p)
        try lift(at, at: liftAt)
        guard let events = RT.get(at, "pointerEvents") as? [NSObject], let up = events.last else { return nil }
        add(path, addSel, up)
        return path
    }

    static func textPath() throws -> NSObject {
        let c = try RT.cls("XCPointerEventPath")
        let alloc = c.perform(NSSelectorFromString("alloc")).takeUnretainedValue() as! NSObject
        let s = NSSelectorFromString("initForTextInput")
        guard alloc.responds(to: s) else { throw AgentError("XCPointerEventPath.initForTextInput is missing") }
        return alloc.perform(s).takeUnretainedValue() as! NSObject
    }

    static func type(_ path: NSObject, _ text: String, at offset: TimeInterval, cps: Int) throws {
        let (f, s) = try RT.imp(path, "typeText:atOffset:typingSpeed:shouldRedact:", as: (@convention(c) (NSObject, Selector, NSString, TimeInterval, UInt64, Bool) -> Void).self)
        f(path, s, text as NSString, offset, UInt64(max(cps, 1)), false)
    }

    static func typeKey(_ path: NSObject, _ key: String, modifiers: UInt, at offset: TimeInterval) throws {
        let (f, s) = try RT.imp(path, "typeKey:modifiers:atOffset:", as: (@convention(c) (NSObject, Selector, NSString, UInt, TimeInterval) -> Void).self)
        f(path, s, key as NSString, modifiers, offset)
    }

    static func record(_ paths: [NSObject], name: String = "Test Buddy action") throws -> NSObject {
        let c = try RT.cls("XCSynthesizedEventRecord")
        let alloc = c.perform(NSSelectorFromString("alloc")).takeUnretainedValue() as! NSObject
        let (f, s) = try RT.imp(alloc, "initWithName:interfaceOrientation:", as: (@convention(c) (NSObject, Selector, NSString, Int) -> NSObject).self)
        let rec = f(alloc, s, name as NSString, UIInterfaceOrientation.portrait.rawValue)
        let (add, sa) = try RT.imp(rec, "addPointerEventPath:", as: (@convention(c) (NSObject, Selector, NSObject) -> Void).self)
        for p in paths { add(rec, sa, p) }
        return rec
    }

    static func synthesize(_ record: NSObject, _ done: @escaping (Error?) -> Void) throws {
        let proxy = try proxy()
        let (f, s) = try RT.imp(proxy, "_XCT_synthesizeEvent:completion:", as: (@convention(c) (NSObject, Selector, NSObject, @escaping (NSError?) -> Void) -> Void).self)
        f(proxy, s, record) { done($0) }
    }

    typealias SetTimeout = @convention(c) (Double) -> Void
    typealias GetTimeout = @convention(c) () -> Double
    static let setStateTimeout = RT.cFunction("_XCTSetApplicationStateTimeout", as: SetTimeout.self)
    static let stateTimeout = RT.cFunction("_XCTApplicationStateTimeout", as: GetTimeout.self)

    static func axAttributes(forKeyPaths keyPaths: [String]) -> [AnyObject]? {
        guard let c = NSClassFromString("XCElementSnapshot") as? NSObject.Type,
              let (f, s) = try? RT.imp(c, "axAttributesForElementSnapshotKeyPaths:isMacOS:", as: (@convention(c) (AnyObject, Selector, NSSet, Bool) -> Unmanaged<AnyObject>?).self)
        else { return nil }
        let r = f(c, s, NSSet(array: keyPaths), false)?.takeUnretainedValue()
        if let a = r as? [AnyObject] { return a }
        if let set = r as? NSSet { return set.allObjects as [AnyObject] }
        return nil
    }

    static func defaultSnapshotParameters() -> [String: Any] {
        guard let client = try? axClient(), let d = RT.get(client, "defaultParameters") as? [String: Any] else { return [:] }
        return d
    }

    static func attributes(of element: NSObject, _ names: [String]) throws -> [String: Any] {
        let client = try axClient()
        let (f, s) = try RT.imp(client, "attributesForElement:attributes:error:", as: (@convention(c) (NSObject, Selector, NSObject, NSArray, UnsafeMutablePointer<NSError?>?) -> Unmanaged<NSDictionary>?).self)
        var err: NSError?
        guard let d = f(client, s, element, names as NSArray, &err)?.takeUnretainedValue() as? [String: Any] else {
            throw AgentError("reading attributes failed: \(err?.localizedDescription ?? "no result")")
        }
        return d
    }

    static func requestSnapshot(_ element: NSObject, attributes: [AnyObject], parameters: [String: Any]) throws -> NSObject {
        let client = try axClient()
        let (f, s) = try RT.imp(client, "requestSnapshotForElement:attributes:parameters:error:", as: (@convention(c) (NSObject, Selector, NSObject, NSArray, NSDictionary, UnsafeMutablePointer<NSError?>?) -> Unmanaged<NSObject>?).self)
        var err: NSError?
        guard let r = f(client, s, element, attributes as NSArray, parameters as NSDictionary, &err)?.takeUnretainedValue() else {
            throw AgentError("snapshot failed: \(err?.localizedDescription ?? "no result")")
        }
        if let inner = RT.get(r, "elementSnapshot") as? NSObject { return inner }
        return r
    }

    static func pressLock() throws {
        let s = NSSelectorFromString("pressLockButton")
        guard XCUIDevice.shared.responds(to: s) else { throw AgentError("XCUIDevice.pressLockButton is missing") }
        XCUIDevice.shared.perform(s)
    }

    static func pressButton(_ raw: Int) throws {
        let (f, s) = try RT.imp(XCUIDevice.shared, "pressButton:", as: (@convention(c) (NSObject, Selector, Int) -> Void).self)
        f(XCUIDevice.shared, s, raw)
    }
}

// The screen as docs/protocol.md's windows and nodes.
//
// Windows, top-most first: a SpringBoard alert over the app (type 3), the keyboard (type 2,
// split out of the app's tree), the foreground app (type 1). Attribute mapping:
//   t = title, else value; cd = label; id = identifier; h = placeholderValue; v = value;
//   c = element type name. Flags per docs/protocol.md; iOS has no "visible to user" attribute,
//   so a node is visible unless its frame lies outside the app's frame or is empty.
//
// Two ways to read: "fast" asks testmanagerd for a snapshot with only the attributes used here
// (XCAXClient_iOS requestSnapshotForElement:attributes:parameters:error:); "public" is
// XCUIApplication.snapshot() (every attribute). Fast is the default when this Xcode has it.
import XCTest

final class Tree {
    /// How long one snapshot may wait for the app before failing (and being tried again).
    static let axTimeoutSeconds = 3.0
    /// The key paths a node needs (fast snapshots ask for exactly these).
    static let keyPaths = ["elementType", "frame", "label", "title", "value", "identifier", "placeholderValue",
                           "enabled", "selected", "hasKeyboardFocus", "hasFocus"]

    /// "public" measured faster than "fast" on Xcode 27.2 (Settings, 167 nodes: 35 ms vs
    /// 58 ms): XCUIApplication.snapshot() is answered in the app over the automation session,
    /// the trimmed request goes through testmanagerd. See docs/research/ios-events.md.
    var mode = "public"
    private lazy var fastAttributes: [AnyObject]? = Private.axAttributes(forKeyPaths: Tree.keyPaths)
    private lazy var fastParameters: [String: Any] = Private.defaultSnapshotParameters()

    /// What one read saw, gathered in the read and published when it ends.
    private struct Seen {
        var appFrame = CGRect.zero
        var landscape: Bool?
        var keyboard = false
        var ms: (snap: Double, walk: Double, nodes: Int) = (0, 0, 0)
    }

    /// What the last read saw. A read runs on the main thread, and one given up on by its op
    /// (issue #130) may still end there while the ops queue reads these: they are published
    /// under a lock, all at once.
    private let lock = NSLock()
    private var _lastSignature: (seq: Int64, hash: Int)?
    private var _landscape = false
    private var _lastAppFrame = CGRect.zero
    private var _lastKeyboard = false
    private var _lastMs: (snap: Double, walk: Double, nodes: Int) = (0, 0, 0)

    /// Hash of the windows of the last read, and the change counter when it was taken.
    var lastSignature: (seq: Int64, hash: Int)? { lock.lock(); defer { lock.unlock() }; return _lastSignature }
    /// Whether the app's frame was wider than tall at the last read (touch coordinates turn).
    var landscape: Bool { lock.lock(); defer { lock.unlock() }; return _landscape }
    var lastAppFrame: CGRect { lock.lock(); defer { lock.unlock() }; return _lastAppFrame }
    var lastKeyboard: Bool { lock.lock(); defer { lock.unlock() }; return _lastKeyboard }
    var lastMs: (snap: Double, walk: Double, nodes: Int) { lock.lock(); defer { lock.unlock() }; return _lastMs }

    private func publish(_ seen: Seen, signature: (seq: Int64, hash: Int)? = nil) {
        lock.lock(); defer { lock.unlock() }
        _lastAppFrame = seen.appFrame
        if let l = seen.landscape { _landscape = l }
        _lastKeyboard = seen.keyboard
        _lastMs = seen.ms
        if let signature { _lastSignature = signature }
    }

    /// Writes "keyboard" and "windows" for the current screen into w. Main thread. wanted: false
    /// once the op gave the read up: what it saw is not published (issue #130).
    func write(_ w: JW, mode requested: String? = nil, seq: Int64, launchBundle: String? = nil, launchPID: Int32 = 0,
               wanted: () -> Bool = { true }) throws {
        var seen = Seen(appFrame: lastAppFrame)
        let (bytes, keyboard) = try windows(mode: requested ?? mode, launchBundle: launchBundle, launchPID: launchPID, seen: &seen)
        if wanted() { publish(seen, signature: (seq, Tree.hash(bytes))) }
        w.put("keyboard", keyboard)
        w.key("windows").raw(bytes)
        w.key("timing").obj().put("snapMs", seen.ms.snap).put("walkMs", seen.ms.walk).put("nodes", seen.ms.nodes).put("mode", requested ?? mode).end()
    }

    /// A hash of everything a read shows: equal hashes, same screen. One snapshot. Main thread.
    func signature() throws -> Int {
        var seen = Seen(appFrame: lastAppFrame)
        let h = Tree.hash(try windows(mode: mode, seen: &seen).bytes)
        publish(seen)
        return h
    }

    private static func hash(_ b: [UInt8]) -> Int {
        var h = Hasher()
        b.withUnsafeBytes { h.combine(bytes: $0) }
        return h.finalize()
    }

    /// The windows array (JSON) and whether a keyboard shows.
    private func windows(mode: String, launchBundle: String? = nil, launchPID: Int32 = 0, seen: inout Seen) throws -> (bytes: [UInt8], keyboard: Bool) {
        func snapshot(bundle: String, pid: Int32, mode: String) throws -> XCUIElementSnapshot {
            let t = DispatchTime.now().uptimeNanoseconds
            defer { seen.ms.snap = Double(DispatchTime.now().uptimeNanoseconds - t) / 1e6 }
            return try self.snapshot(bundle: bundle, pid: pid, mode: mode)
        }
        let fg = Apps.shared.foreground()
        var windows: [[UInt8]] = []
        var keyboard = false
        var springboardAlert: [UInt8]?
        var appBundle = fg.bundle, appPid = fg.pid

        if fg.bundle == Apps.springboard || fg.bundle.isEmpty {
            // SpringBoard alone: its home screen, or one of its alerts over an app.
            let sb = try snapshot(bundle: Apps.springboard, pid: fg.pid, mode: mode)
            let alerts = Tree.find(sb, .alert)
            var behind = Apps.shared.lastApp()
            if !alerts.isEmpty, let bundle = launchBundle {
                let launched = Apps.shared.app(bundle)
                if launched.state == .runningForeground, let proc = Private.process(of: launched),
                   let element = Private.accessibilityElement(ofProcess: proc), let pid = Private.pid(of: element) {
                    behind = (pid, bundle)
                }
            }
            if !alerts.isEmpty, let behind, behind.bundle != Apps.springboard {
                let nodes = JW(capacity: 4096).arr()
                for a in alerts { _ = walk(a, depth: 0, into: nodes, bounds: frame(of: sb), keyboard: nil) }
                springboardAlert = window(id: Int(fg.pid), type: 3, pkg: Apps.springboard, title: "", frame: frame(of: sb), nodes: nodes.end().bytes, active: true)
                appBundle = behind.bundle
                appPid = behind.pid
            } else {
                windows += appWindow(sb, bundle: Apps.springboard, pid: fg.pid, keyboard: &keyboard, active: true, seen: &seen)
                appBundle = ""
            }
        }
        if let bundle = launchBundle {
            guard appBundle == bundle, launchPID == 0 || launchPID == appPid else {
                throw LaunchWindowChanged()
            }
        }
        if !appBundle.isEmpty {
            let snap = try snapshot(bundle: appBundle, pid: appPid, mode: mode)
            windows += appWindow(snap, bundle: appBundle, pid: appPid, keyboard: &keyboard, active: springboardAlert == nil, seen: &seen)
            // A full-screen system browser can leave the host's snapshot empty on iOS 26.
            // Keep the host as the foreground app and read the visible page from its service.
            if springboardAlert == nil, let browser = browserWindow(over: appBundle, host: snap, appFrame: seen.appFrame) {
                windows.insert(browser, at: 0)
            }
        }
        if let a = springboardAlert { windows.insert(a, at: 0) }
        seen.keyboard = keyboard
        let out = JW(capacity: windows.reduce(16) { $0 + $1.count + 1 }).arr()
        for win in windows { out.raw(win) }
        return (out.end().bytes, keyboard)
    }

    private func browserWindow(over bundle: String, host: XCUIElementSnapshot, appFrame lastAppFrame: CGRect) -> [UInt8]? {
        let service = "com.apple.SafariViewService"
        guard #available(iOS 26, *), bundle != service, bundle != Apps.springboard else { return nil }
        var pending = host.children
        while let node = pending.popLast() {
            if (!node.label.isEmpty || !node.title.isEmpty), node.frame.intersects(lastAppFrame) {
                return nil
            }
            pending.append(contentsOf: node.children)
        }
        let app = Apps.shared.app(service)
        let state = app.state
        guard state == .runningForeground || state == .runningBackground,
              let snapshot = try? app.snapshot(),
              Tree.find(snapshot, .webView).contains(where: { $0.frame.intersects(lastAppFrame) })
        else { return nil }
        let nodes = JW(capacity: 8 * 1024).arr()
        _ = walk(snapshot, depth: 0, into: nodes, bounds: lastAppFrame, keyboard: nil)
        return window(id: -1, type: 4, pkg: service, title: snapshot.label,
                      frame: lastAppFrame, nodes: nodes.end().bytes, active: true)
    }

    /// The app's window, preceded by the keyboard's when the app shows one.
    private func appWindow(_ snap: XCUIElementSnapshot, bundle: String, pid: Int32, keyboard: inout Bool, active: Bool,
                           seen: inout Seen) -> [[UInt8]] {
        let t = DispatchTime.now().uptimeNanoseconds
        let bounds = frame(of: snap)
        seen.appFrame = bounds
        if bundle != Apps.springboard { seen.landscape = bounds.width > bounds.height }
        let nodes = JW(capacity: 64 * 1024).arr()
        let kb = JW(capacity: 8 * 1024).arr()
        var kbFrame = CGRect.null
        let count = walk(snap, depth: 0, into: nodes, bounds: bounds, keyboard: (kb, { kbFrame = kbFrame.union($0) }), flutterPID: active && Events.shared.isProbed(pid) ? pid : nil)
        nodes.end(); kb.end()
        seen.ms.walk = Double(DispatchTime.now().uptimeNanoseconds - t) / 1e6
        seen.ms.nodes = count
        var out: [[UInt8]] = []
        if !kbFrame.isNull {
            keyboard = true
            out.append(window(id: Int(pid) + 1_000_000, type: 2, pkg: bundle, title: "Keyboard", frame: kbFrame, nodes: kb.bytes, active: false))
        }
        out.append(window(id: Int(pid), type: 1, pkg: bundle, title: snap.label, frame: bounds, nodes: nodes.bytes, active: active))
        return out
    }

    private func window(id: Int, type: Int, pkg: String, title: String, frame: CGRect, nodes: [UInt8], active: Bool) -> [UInt8] {
        let w = JW(capacity: nodes.count + 200).obj()
            .put("id", id).put("type", type).put("active", active).put("focused", type == 1 && active).put("pkg", pkg)
        w.opt("title", title)
        w.key("b").arr().int(Int(frame.minX.rounded())).int(Int(frame.minY.rounded())).int(Int(frame.maxX.rounded())).int(Int(frame.maxY.rounded())).end()
        w.key("nodes").raw(nodes)
        return w.end().bytes
    }

    // MARK: snapshots

    /// A snapshot of an app. Main thread. retry: the public path asks once more after a failure.
    func snapshot(bundle: String, pid: Int32, mode: String, retry: Bool = true) throws -> XCUIElementSnapshot {
        let app = Apps.shared.app(bundle)
        if mode == "fast", let attrs = fastAttributes {
            let element = Private.process(of: app).flatMap(Private.accessibilityElement(ofProcess:)) ?? (pid > 0 ? Private.element(pid: pid) : nil)
            if let element {
                do {
                    let raw = try Private.requestSnapshot(element, attributes: attrs, parameters: fastParameters)
                    if let s = raw as? XCUIElementSnapshot { return s }
                    NSLog("tb: fast snapshot returned \(type(of: raw)); using the public one")
                } catch {
                    NSLog("tb: fast snapshot failed (\(error)); using the public one")
                }
            }
        }
        // Public path; one retry, as a read in a transition can time out.
        do { return try app.snapshot() } catch {
            guard retry else { throw error }
            return try app.snapshot()
        }
    }

    /// Before injecting a selector tap, resolve its live button in the same process.
    /// Never select another matching row or tap a point outside the button.
    /// Its frame as read now, or nil when the tap must not go.
    func containsTap(id: String, pid: Int32, point: CGPoint) throws -> CGRect? {
        let fg = Apps.shared.foreground()
        guard fg.pid == pid, !fg.bundle.isEmpty else { return nil }
        let snapshot = try self.snapshot(bundle: fg.bundle, pid: pid, mode: mode)
        let matches = Tree.find(snapshot, .button).filter { $0.identifier == id }
        guard matches.count == 1, matches[0].isEnabled, matches[0].frame.contains(point) else { return nil }
        return matches[0].frame
    }

    /// The editable field with this identifier in pid's app (the only one), with the app's
    /// frame and the keyboard's, from one snapshot: where typing touches to focus it. Main thread.
    func field(id: String, pid: Int32) throws -> FieldGeometry {
        let fg = Apps.shared.foreground()
        guard fg.pid == pid, !fg.bundle.isEmpty else { throw AgentError("process changed while refocusing \(id)") }
        let snap = try snapshot(bundle: fg.bundle, pid: pid, mode: mode)
        var matches: [XCUIElementSnapshot] = []
        var keyboard: CGRect?
        var stack = [snap]
        while let n = stack.popLast() {
            if n.identifier == id { matches.append(n) }
            if n.elementType == .keyboard, keyboard == nil { keyboard = n.frame }
            stack.append(contentsOf: n.children)
        }
        guard matches.count == 1 else { throw AgentError("cannot uniquely identify field \(id)") }
        guard [.textField, .secureTextField, .textView, .searchField].contains(matches[0].elementType) else {
            throw AgentError("expected an editable field at \(id)")
        }
        return FieldGeometry(frame: matches[0].frame, screen: frame(of: snap), keyboard: keyboard)
    }

    /// The field with keyboard focus (the first in the tree, as a query's firstMatch; its value
    /// "" when it shows only its placeholder) and whether a keyboard shows, from one snapshot of
    /// the foreground app: what typing looks at (issue #51; a query for each took two snapshots
    /// for the field, .exists then .snapshot(), and one for the keyboard). Main thread.
    func focus() throws -> FocusState {
        let fg = Apps.shared.foreground()
        guard !fg.bundle.isEmpty else { return FocusState(field: nil, keyboard: false) }
        // No retry: on an app that does not answer, a look must not hold the main thread for two
        // AX timeouts; typing looks again on the next change.
        let snap = try snapshot(bundle: fg.bundle, pid: fg.pid, mode: mode, retry: false)
        return FocusState.look(SnapNode(s: snap))
    }

    /// XCTest's snapshot as the focus look reads it (FocusState.look).
    private struct SnapNode: FocusNode {
        let s: XCUIElementSnapshot
        var isKeyboard: Bool { s.elementType == .keyboard }
        var hasKeyboardFocus: Bool { Tree.keyboardFocus(s) }
        var identifier: String { s.identifier }
        var value: Any? { s.value }
        var placeholderValue: String? { s.placeholderValue }
        var children: [SnapNode] { s.children.map { SnapNode(s: $0) } }
    }

    private func frame(of s: XCUIElementSnapshot) -> CGRect {
        let f = s.frame
        if f.width.isFinite, f.height.isFinite, f.width > 0, f.height > 0 { return f }
        return UIScreen.main.bounds
    }

    static func find(_ s: XCUIElementSnapshot, _ type: XCUIElement.ElementType) -> [XCUIElementSnapshot] {
        var out: [XCUIElementSnapshot] = []
        var stack = [s]
        while let n = stack.popLast() {
            if n.elementType == type { out.append(n); continue }
            stack.append(contentsOf: n.children.reversed())
        }
        return out
    }

    // MARK: nodes

    private static let keyboardFocusSel = NSSelectorFromString("hasKeyboardFocus")

    private static func keyboardFocus(_ s: XCUIElementSnapshot) -> Bool {
        guard let o = s as? NSObject, o.responds(to: keyboardFocusSel) else { return false }
        typealias Get = @convention(c) (NSObject, Selector) -> Bool
        return unsafeBitCast(o.method(for: keyboardFocusSel), to: Get.self)(o, keyboardFocusSel)
    }

    /// Pre-order walk writing nodes; a Keyboard element's subtree goes to the keyboard list
    /// (depth from 0) when one is given. Returns the number of nodes written.
    @discardableResult
    private func walk(_ s: XCUIElementSnapshot, depth: Int, into w: JW, bounds: CGRect,
                      keyboard: (JW, (CGRect) -> Void)?, flutterPID: Int32? = nil) -> Int {
        let type = s.elementType
        if type == .keyboard, let (kb, note) = keyboard {
            note(s.frame)
            return walk(s, depth: 0, into: kb, bounds: bounds, keyboard: nil)
        }
        var count = 1
        writeNode(s, type: type, depth: depth, into: w, bounds: bounds)
        let children = s.children
        let listRows = flutterPID.map { FlutterAdapter.isListGroup(children, pid: $0, window: bounds) } ?? false
        for (i, child) in children.enumerated() {
            let childDepth = listRows && i > 0 ? depth + 2 : depth + 1
            count += walk(child, depth: childDepth, into: w, bounds: bounds, keyboard: keyboard, flutterPID: flutterPID)
        }
        return count
    }

    private func writeNode(_ s: XCUIElementSnapshot, type: XCUIElement.ElementType, depth: Int, into w: JW, bounds: CGRect) {
        let value: String
        switch s.value {
        case let v as String: value = v
        case let n as NSNumber: value = n.stringValue
        case nil: value = ""
        case let v?: value = "\(v)"
        }
        let title = s.title
        let text = title.isEmpty ? value : title
        let placeholder = s.placeholderValue ?? ""
        let f = s.frame
        let finite = f.minX.isFinite && f.minY.isFinite && f.width.isFinite && f.height.isFinite
        let r = finite ? f : .zero

        var flags = 0
        if s.isEnabled { flags |= 2 }
        if s.isSelected { flags |= 32 }
        if s.hasFocus || Tree.keyboardFocus(s) { flags |= 4 }
        switch type {
        case .button, .cell, .link, .key, .menuItem, .tab, .segmentedControl, .radioButton, .stepper, .slider,
             .incrementArrow, .decrementArrow, .disclosureTriangle, .popUpButton, .menuButton, .toolbarButton:
            flags |= 1
        case .switch, .toggle, .checkBox:
            flags |= 1 | 64
            if value == "1" { flags |= 16 }
        case .textField, .searchField, .textView:
            flags |= 1 | 256 | 1024
        case .secureTextField:
            flags |= 1 | 128 | 256 | 1024
        case .scrollView, .table, .collectionView, .outline:
            flags |= 8
        default: break
        }
        if Tree.spelledChecked(value) { flags |= 16 | 64 }
        if !placeholder.isEmpty && value == placeholder && flags & 256 != 0 { flags |= 2048 }
        if r.width > 0 && r.height > 0 && r.intersects(bounds) { flags |= 4096 }

        w.obj().put("d", depth)
        w.key("b").arr().int(Int(r.minX.rounded())).int(Int(r.minY.rounded())).int(Int(r.maxX.rounded())).int(Int(r.maxY.rounded())).end()
        if r.minX != r.minX.rounded() || r.minY != r.minY.rounded()
            || r.maxX != r.maxX.rounded() || r.maxY != r.maxY.rounded() {
            w.key("pb").arr().num(r.minX).num(r.minY).num(r.maxX).num(r.maxY).end()
        }
        w.put("c", Tree.typeName(type))
        w.opt("t", text).opt("cd", s.label).opt("id", s.identifier).opt("h", placeholder).opt("v", value)
        if flags != 0 { w.put("f", flags) }
        w.end()
    }

    /// React Native spells a checkbox's state into its value ("checkbox, checked").
    private static func spelledChecked(_ value: String) -> Bool {
        guard value.contains(", ") else { return false }
        let words = value.components(separatedBy: ", ")
        return words.count >= 2 && (words[0] == "checkbox" || words[0] == "radio button") && words[1] == "checked"
    }

    static func typeName(_ t: XCUIElement.ElementType) -> String {
        switch t {
        case .any: return "Any"
        case .other: return "Other"
        case .application: return "Application"
        case .group: return "Group"
        case .window: return "Window"
        case .sheet: return "Sheet"
        case .drawer: return "Drawer"
        case .alert: return "Alert"
        case .dialog: return "Dialog"
        case .button: return "Button"
        case .radioButton: return "RadioButton"
        case .radioGroup: return "RadioGroup"
        case .checkBox: return "CheckBox"
        case .disclosureTriangle: return "DisclosureTriangle"
        case .popUpButton: return "PopUpButton"
        case .comboBox: return "ComboBox"
        case .menuButton: return "MenuButton"
        case .toolbarButton: return "ToolbarButton"
        case .popover: return "Popover"
        case .keyboard: return "Keyboard"
        case .key: return "Key"
        case .navigationBar: return "NavigationBar"
        case .tabBar: return "TabBar"
        case .tabGroup: return "TabGroup"
        case .toolbar: return "Toolbar"
        case .statusBar: return "StatusBar"
        case .table: return "Table"
        case .tableRow: return "TableRow"
        case .tableColumn: return "TableColumn"
        case .outline: return "Outline"
        case .outlineRow: return "OutlineRow"
        case .browser: return "Browser"
        case .collectionView: return "CollectionView"
        case .slider: return "Slider"
        case .pageIndicator: return "PageIndicator"
        case .progressIndicator: return "ProgressIndicator"
        case .activityIndicator: return "ActivityIndicator"
        case .segmentedControl: return "SegmentedControl"
        case .picker: return "Picker"
        case .pickerWheel: return "PickerWheel"
        case .switch: return "Switch"
        case .toggle: return "Toggle"
        case .link: return "Link"
        case .image: return "Image"
        case .icon: return "Icon"
        case .searchField: return "SearchField"
        case .scrollView: return "ScrollView"
        case .scrollBar: return "ScrollBar"
        case .staticText: return "StaticText"
        case .textField: return "TextField"
        case .secureTextField: return "SecureTextField"
        case .datePicker: return "DatePicker"
        case .textView: return "TextView"
        case .menu: return "Menu"
        case .menuItem: return "MenuItem"
        case .menuBar: return "MenuBar"
        case .menuBarItem: return "MenuBarItem"
        case .map: return "Map"
        case .webView: return "WebView"
        case .incrementArrow: return "IncrementArrow"
        case .decrementArrow: return "DecrementArrow"
        case .timeline: return "Timeline"
        case .ratingIndicator: return "RatingIndicator"
        case .valueIndicator: return "ValueIndicator"
        case .splitGroup: return "SplitGroup"
        case .splitter: return "Splitter"
        case .relevanceIndicator: return "RelevanceIndicator"
        case .colorWell: return "ColorWell"
        case .helpTag: return "HelpTag"
        case .matte: return "Matte"
        case .dockItem: return "DockItem"
        case .ruler: return "Ruler"
        case .rulerMarker: return "RulerMarker"
        case .grid: return "Grid"
        case .levelIndicator: return "LevelIndicator"
        case .cell: return "Cell"
        case .layoutArea: return "LayoutArea"
        case .layoutItem: return "LayoutItem"
        case .handle: return "Handle"
        case .stepper: return "Stepper"
        case .tab: return "Tab"
        case .touchBar: return "TouchBar"
        case .statusItem: return "StatusItem"
        @unknown default: return "ElementType\(t.rawValue)"
        }
    }
}

/// A foreground switch during launch acquisition. Wait for the next app event.
struct LaunchWindowChanged: Error {}

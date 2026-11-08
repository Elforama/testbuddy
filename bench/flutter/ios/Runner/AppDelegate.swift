import Flutter
import SwiftUI
import AVFoundation
import Contacts
import CoreLocation
import UserNotifications

private struct CloseToolkitKey: EnvironmentKey {
    static let defaultValue: () -> Void = {}
}

private extension EnvironmentValues {
    var closeToolkit: () -> Void {
        get { self[CloseToolkitKey.self] }
        set { self[CloseToolkitKey.self] = newValue }
    }
}

struct BackButton: View {
    @Environment(\.closeToolkit) private var close
    var body: some View { Button("Back", action: close).accessibilityIdentifier("back") }
}
func status(_ text: String, _ id: String) -> some View {
    Text(verbatim: text).accessibilityIdentifier(id)
}

private final class LoadingVeilFactory: NSObject, FlutterPlatformViewFactory {
    func create(withFrame frame: CGRect, viewIdentifier viewId: Int64,
                arguments args: Any?) -> FlutterPlatformView {
        LoadingVeil(frame: frame)
    }
}

private final class LoadingVeil: NSObject, FlutterPlatformView {
    private let cover: UIView

    init(frame: CGRect) {
        cover = UIView(frame: frame)
        super.init()
        cover.backgroundColor = UIColor.black.withAlphaComponent(0.4)
        let spinner = UIActivityIndicatorView(style: .large)
        spinner.color = .white
        spinner.translatesAutoresizingMaskIntoConstraints = false
        spinner.isAccessibilityElement = true
        spinner.startAnimating()
        cover.addSubview(spinner)
        NSLayoutConstraint.activate([
            spinner.centerXAnchor.constraint(equalTo: cover.centerXAnchor),
            spinner.centerYAnchor.constraint(equalTo: cover.centerYAnchor),
        ])
        cover.accessibilityElements = [spinner]
    }

    func view() -> UIView { cover }
}

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate, CLLocationManagerDelegate, FlutterStreamHandler {
    private let location = CLLocationManager()
    private var pendingLocation: FlutterResult?
    private var locationUpdates: FlutterEventSink?

    func didInitializeImplicitFlutterEngine(_ bridge: FlutterImplicitEngineBridge) {
        GeneratedPluginRegistrant.register(with: bridge.pluginRegistry)
        let registrar = bridge.pluginRegistry.registrar(forPlugin: "BuddyHost")!
        registrar.register(LoadingVeilFactory(), withId: "buddy/loading-veil")
        FlutterEventChannel(name: "buddy/location", binaryMessenger: registrar.messenger()).setStreamHandler(self)
        let channel = FlutterMethodChannel(name: "buddy/host", binaryMessenger: registrar.messenger())
        channel.setMethodCallHandler { [weak self] call, result in
            guard let self else { return }
            if call.method == "post" {
                let center = UNUserNotificationCenter.current()
                center.getNotificationSettings { settings in
                    guard settings.authorizationStatus == .authorized else {
                        DispatchQueue.main.async { result("Not allowed") }; return
                    }
                    let content = UNMutableNotificationContent()
                    content.title = "Bench says hi"
                    center.add(UNNotificationRequest(identifier:"bench", content: content, trigger: nil)) { error in
                        DispatchQueue.main.async { result(error == nil ? "Posted":"Not allowed") }
                    }
                }
                return
            }
            guard let key = call.arguments as? String else { result(FlutterMethodNotImplemented); return }
            if call.method == "toolkit", let toolkit = Toolkit(rawValue: key),
               let root = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene })
                .flatMap({ $0.windows }).first(where: { $0.isKeyWindow })?.rootViewController {
                let host = UIHostingController(rootView: toolkit.screen.environment(\.closeToolkit, { [weak root] in
                    root?.dismiss(animated: false)
                }))
                host.modalPresentationStyle = .fullScreen
                let show = { root.present(host, animated: false) { result(nil) } }
                let transition = root.transitionCoordinator ?? root.presentedViewController?.transitionCoordinator
                // UIKit can return a coordinator which has already stopped accepting
                // callbacks. Present immediately if it did not register this request.
                if transition?.animate(alongsideTransition: nil, completion: { _ in show() }) != true {
                    show()
                }
            } else {
                permission(key, request: call.method == "permission", result: result)
            }
        }
    }

    private func permission(_ key: String, request: Bool, result: @escaping FlutterResult) {
        let finish: (String) -> Void = { value in DispatchQueue.main.async { result(value) } }
        switch key {
        case "Location":
            let state = location.authorizationStatus
            if request && state == .notDetermined {
                pendingLocation = result
                location.delegate = self
                location.requestWhenInUseAuthorization()
            } else { finish(state == .notDetermined ? "not asked" : state == .authorizedAlways || state == .authorizedWhenInUse ? "granted" : "denied") }
        case "Contacts":
            let state = CNContactStore.authorizationStatus(for: .contacts)
            if request && state == .notDetermined {
                CNContactStore().requestAccess(for: .contacts) { _,_ in
                    DispatchQueue.main.async { self.permission(key, request: false, result: result) }
                }
            } else if #available(iOS 18, *), state == .limited { finish("limited") }
            else { finish(state == .notDetermined ? "not asked" : state == .authorized ? "granted" : "denied") }
        case "Notifications":
            let center = UNUserNotificationCenter.current()
            if request { center.requestAuthorization(options: [.alert,.sound]) { allowed,_ in finish(allowed ? "granted":"denied") } }
            else { center.getNotificationSettings { s in finish(s.authorizationStatus == .notDetermined ? "not asked" : s.authorizationStatus == .denied ? "denied":"granted") } }
        default:
            let media: AVMediaType = key == "Microphone" ? .audio : .video
            let state = AVCaptureDevice.authorizationStatus(for: media)
            if request && state == .notDetermined { AVCaptureDevice.requestAccess(for: media) { ok in finish(ok ? "granted":"denied") } }
            else { finish(state == .authorized ? "granted" : state == .notDetermined ? "not asked":"denied") }
        }
    }

    func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        guard let callback = pendingLocation, manager.authorizationStatus != .notDetermined else { return }
        pendingLocation = nil
        permission("Location", request: false, result: callback)
    }

    func onListen(withArguments arguments: Any?, eventSink events: @escaping FlutterEventSink) -> FlutterError? {
        locationUpdates = events
        location.delegate = self
        location.startUpdatingLocation()
        return nil
    }

    func onCancel(withArguments arguments: Any?) -> FlutterError? {
        location.stopUpdatingLocation()
        locationUpdates = nil
        return nil
    }

    func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard let coordinate = locations.last?.coordinate else { return }
        locationUpdates?([coordinate.latitude, coordinate.longitude])
    }
}

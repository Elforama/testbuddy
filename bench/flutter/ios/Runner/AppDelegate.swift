import Flutter
import SwiftUI
import AVFoundation
import Contacts
import CoreLocation
import UserNotifications

struct BackButton: View {
    @Environment(\.dismiss) private var dismiss
    var body: some View { Button("Back") { dismiss() }.accessibilityIdentifier("back") }
}
func status(_ text: String, _ id: String) -> some View {
    Text(verbatim: text).accessibilityIdentifier(id)
}

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate, CLLocationManagerDelegate {
    private let location = CLLocationManager()
    private var pendingLocation: FlutterResult?

    func didInitializeImplicitFlutterEngine(_ bridge: FlutterImplicitEngineBridge) {
        GeneratedPluginRegistrant.register(with: bridge.pluginRegistry)
        let registrar = bridge.pluginRegistry.registrar(forPlugin: "BuddyHost")!
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
                let host = UIHostingController(rootView: toolkit.screen)
                host.modalPresentationStyle = .fullScreen
                let show = { root.present(host, animated: true) { result(nil) } }
                if let transition = root.presentedViewController?.transitionCoordinator {
                    transition.animate(alongsideTransition: nil) { _ in show() }
                } else { show() }
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
}

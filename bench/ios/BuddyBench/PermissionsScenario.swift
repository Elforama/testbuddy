// Scenario 3, Permissions: real system permission prompts, one button per permission, with a
// status line each read back from the system.
import AVFoundation
import Contacts
import CoreLocation
import SwiftUI
import UserNotifications

struct PermissionsScenario: View {
    @StateObject private var perms = Permissions()
    @State private var rationale = false

    var body: some View {
        ScenarioScreen(scenario: .permissions, scrolls: true) {
            row("Ask camera", "perm_camera", "Camera", perms.camera, "perm_camera_status",
                perms.askCamera)
            row("Ask location", "perm_location", "Location", perms.location,
                "perm_location_status", perms.askLocation)
            row("Ask notifications", "perm_notifications", "Notifications", perms.notifications,
                "perm_notifications_status", perms.askNotifications)
            row("Ask contacts", "perm_contacts", "Contacts", perms.contacts, "perm_contacts_status",
                perms.askContacts)
            row("Ask with rationale", "perm_rationale", "Microphone", perms.microphone,
                "perm_mic_status") { rationale = true }
            Button("Post a notification", action: perms.post)
                .buttonStyle(.bordered)
                .accessibilityIdentifier("perm_post")
            if let posted = perms.posted {
                status(posted, "perm_post_status")
            }
            Button("Refresh", action: perms.refresh)
                .buttonStyle(.bordered)
                .accessibilityIdentifier("perm_refresh")
        }
        .overlay {
            if rationale {
                InAppDialog(id: "rationale_dialog", title: "We need your microphone", buttons: [
                    ("Not now", "rationale_no", { rationale = false }),
                    ("Continue", "rationale_yes", {
                        rationale = false
                        perms.askMicrophone()
                    }),
                ])
            }
        }
        .onAppear(perform: perms.refresh)
        .onReceive(NotificationCenter.default.publisher(
            for: UIApplication.didBecomeActiveNotification)) { _ in perms.refresh() }
    }

    private func row(_ title: String, _ id: String, _ name: String, _ state: String,
                     _ statusID: String, _ ask: @escaping () -> Void) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Button(title, action: ask)
                .buttonStyle(.bordered)
                .accessibilityIdentifier(id)
            status("\(name): \(state)", statusID)
        }
    }
}

/// Each permission's state as "granted" / "denied" / "not asked".
@MainActor final class Permissions: NSObject, ObservableObject, CLLocationManagerDelegate {
    @Published var camera = "not asked"
    @Published var location = "not asked"
    @Published var notifications = "not asked"
    @Published var contacts = "not asked"
    @Published var microphone = "not asked"
    @Published var posted: String?
    private let locationManager = CLLocationManager()

    override init() {
        super.init()
        locationManager.delegate = self
    }

    func refresh() {
        camera = Self.text(AVCaptureDevice.authorizationStatus(for: .video))
        microphone = switch AVAudioApplication.shared.recordPermission {
        case .undetermined: "not asked"
        case .granted: "granted"
        default: "denied"
        }
        location = switch locationManager.authorizationStatus {
        case .notDetermined: "not asked"
        case .authorizedWhenInUse, .authorizedAlways: "granted"
        default: "denied"
        }
        contacts = switch CNContactStore.authorizationStatus(for: .contacts) {
        case .notDetermined: "not asked"
        case .authorized, .limited: "granted"
        default: "denied"
        }
        Task {
            let settings = await UNUserNotificationCenter.current().notificationSettings()
            notifications = switch settings.authorizationStatus {
            case .notDetermined: "not asked"
            case .denied: "denied"
            default: "granted"
            }
        }
    }

    private static func text(_ status: AVAuthorizationStatus) -> String {
        switch status {
        case .notDetermined: "not asked"
        case .authorized: "granted"
        default: "denied"
        }
    }

    func askCamera() {
        AVCaptureDevice.requestAccess(for: .video) { _ in
            Task { @MainActor in self.refresh() }
        }
    }

    func askMicrophone() {
        AVAudioApplication.requestRecordPermission { _ in
            Task { @MainActor in self.refresh() }
        }
    }

    func askLocation() {
        if locationManager.authorizationStatus == .notDetermined {
            locationManager.requestWhenInUseAuthorization()
        } else {
            refresh()
        }
    }

    nonisolated func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        Task { @MainActor in self.refresh() }
    }

    func askNotifications() {
        Task {
            _ = try? await UNUserNotificationCenter.current()
                .requestAuthorization(options: [.alert, .badge, .sound])
            refresh()
        }
    }

    func askContacts() {
        CNContactStore().requestAccess(for: .contacts) { _, _ in
            Task { @MainActor in self.refresh() }
        }
    }

    /// Posts "Bench says hi" now, if notifications are allowed.
    func post() {
        Task {
            let center = UNUserNotificationCenter.current()
            let settings = await center.notificationSettings()
            guard [.authorized, .provisional, .ephemeral].contains(settings.authorizationStatus) else {
                posted = "Not allowed"
                return
            }
            let content = UNMutableNotificationContent()
            content.title = "Bench says hi"
            let request = UNNotificationRequest(identifier: "bench-hi", content: content, trigger: nil)
            do {
                try await center.add(request)
                posted = "Posted"
            } catch {
                posted = "Not allowed"
            }
        }
    }
}

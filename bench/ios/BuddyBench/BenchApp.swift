import SwiftUI
import UserNotifications

@main
struct BuddyBenchApp: App {
    @StateObject private var model = BenchModel()
    @StateObject private var router = Router()

    init() {
        UNUserNotificationCenter.current().delegate = NotificationPresenter.shared
    }

    var body: some Scene {
        WindowGroup {
            NavigationStack(path: $router.path) {
                ScenarioListView()
                    .navigationDestination(for: Route.self) { $0.screen }
            }
            .environmentObject(model)
            .environmentObject(router)
            .onOpenURL { router.open($0) }
        }
    }
}

enum Timing {
    static let signIn: Duration = .milliseconds(1000)
    static let loadMore: Duration = .milliseconds(800)
    static let reviews: Duration = .milliseconds(600)
    static let ticker: TimeInterval = 0.1
}

final class BenchModel: ObservableObject {
    @Published var user: String?
    @Published var cart = 0
    var onboardingShown = false
}

enum Route: Hashable {
    case scenario(Scenario)
    case detail(Int)
    case live
    case slowList
    case relabel
    case level(Int)
    case navForm
    case list(ListKind)
    case toolkit(Toolkit)
    case kbAutofocus

    @ViewBuilder var screen: some View {
        switch self {
        case .scenario(let scenario): scenario.screen
        case .detail(let item): DetailView(item: item)
        case .live: LiveView()
        case .slowList: SlowListView()
        case .relabel: RelabelView()
        case .level(let depth): LevelView(depth: depth)
        case .navForm: NavFormView()
        case .list(let kind): kind.screen
        case .toolkit(let toolkit): toolkit.screen
        case .kbAutofocus: AutofocusView()
        }
    }
}

final class Router: ObservableObject {
    @Published var path: [Route] = []

    func open(_ url: URL) {
        guard url.scheme == "bench", url.host == "level",
              let depth = Int(url.lastPathComponent), (1...5).contains(depth) else { return }
        path = [.scenario(.navigation)] + (1...depth).map { .level($0) }
    }
}

struct BackButton: View {
    @Environment(\.dismiss) private var dismiss
    var action: (() -> Void)?

    var body: some View {
        Button("Back") {
            if let action { action() } else { dismiss() }
        }
        .accessibilityIdentifier("back")
    }
}

final class NotificationPresenter: NSObject, UNUserNotificationCenterDelegate {
    static let shared = NotificationPresenter()

    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                willPresent notification: UNNotification) async
        -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }
}

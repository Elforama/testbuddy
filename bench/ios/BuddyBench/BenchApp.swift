// Buddy Bench for iOS: the test app described in bench/SPEC.md and bench/SCENARIOS.md. The
// Android and web versions have the same screens, texts, ids and timings, so one set of flows
// runs on all three. Parts of it never stop moving (spinners, a ticker, a stopwatch, a ball) on
// purpose, and the scenarios put a test runner in known hard situations on purpose.
//
// State lives in memory only: a fresh launch starts on the scenario list with an empty cart.
import SwiftUI
import UserNotifications

@main
struct BuddyBenchApp: App {
    @StateObject private var model = BenchModel()
    @StateObject private var router = Router()

    init() {
        // Show "Bench says hi" as a banner even while the app is in front.
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

/// The timing constants of bench/SPEC.md (identical on every platform).
enum Timing {
    static let signIn: Duration = .milliseconds(1000)
    static let loadMore: Duration = .milliseconds(800)
    static let reviews: Duration = .milliseconds(600)
    static let ticker: TimeInterval = 0.1
}

/// App-wide state: who is signed in, the cart count every Journey screen shares, and whether
/// onboarding was already shown in this process.
final class BenchModel: ObservableObject {
    @Published var user: String?
    @Published var cart = 0
    var onboardingShown = false
}

/// Every pushed screen of the app: one navigation stack from the scenario list down.
enum Route: Hashable {
    case scenario(Scenario)
    // Journey
    case detail(Int)
    case live
    // Slow and janky
    case slowList
    // Navigation and back stack
    case level(Int)
    case navForm
    // Lists
    case list(ListKind)
    // Native toolkits
    case toolkit(Toolkit)
    // Keyboard
    case kbAutofocus

    @ViewBuilder var screen: some View {
        switch self {
        case .scenario(let scenario): scenario.screen
        case .detail(let item): DetailView(item: item)
        case .live: LiveView()
        case .slowList: SlowListView()
        case .level(let depth): LevelView(depth: depth)
        case .navForm: NavFormView()
        case .list(let kind): kind.screen
        case .toolkit(let toolkit): toolkit.screen
        case .kbAutofocus: AutofocusView()
        }
    }
}

/// The navigation stack's path, shared so any screen can push, pop or pop to a scenario.
final class Router: ObservableObject {
    @Published var path: [Route] = []

    /// `bench://level/3`: the Navigation scenario at Level 3, with Levels 1 and 2 under it.
    func open(_ url: URL) {
        guard url.scheme == "bench", url.host == "level",
              let depth = Int(url.lastPathComponent), (1...5).contains(depth) else { return }
        path = [.scenario(.navigation)] + (1...depth).map { .level($0) }
    }
}

/// The in-app Back button of a pushed screen (the system one is hidden with the bar).
struct BackButton: View {
    @Environment(\.dismiss) private var dismiss
    var action: (() -> Void)?

    var body: some View {
        // A plain text label: a Label's icon and title would show up as elements of their own.
        Button("Back") {
            if let action { action() } else { dismiss() }
        }
        .accessibilityIdentifier("back")
    }
}

/// Presents local notifications as banners while the app is in the foreground.
final class NotificationPresenter: NSObject, UNUserNotificationCenterDelegate {
    static let shared = NotificationPresenter()

    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                willPresent notification: UNNotification) async
        -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }
}

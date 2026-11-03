// Buddy Bench for iOS: the test app described in bench/SPEC.md. The Android and web versions
// have the same screens, texts, ids and timings, so one set of flows runs on all three.
// Parts of it never stop moving (spinners, a ticker, a stopwatch, a ball) on purpose.
//
// State lives in memory only: a fresh launch starts on Login with an empty cart.
import SwiftUI

@main
struct BuddyBenchApp: App {
    @StateObject private var model = BenchModel()

    var body: some Scene {
        WindowGroup {
            RootView().environmentObject(model)
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

/// App-wide state: who is signed in, and the cart count every screen shares.
final class BenchModel: ObservableObject {
    @Published var user: String?
    @Published var cart = 0
}

/// Screens pushed over Home.
enum Route: Hashable {
    case detail(Int)
    case live
}

/// Login until someone signs in, then Home with its navigation stack.
struct RootView: View {
    @EnvironmentObject private var model: BenchModel

    var body: some View {
        if let user = model.user {
            HomeStack(user: user)
        } else {
            LoginView()
        }
    }
}

struct HomeStack: View {
    let user: String
    @State private var path: [Route] = []

    var body: some View {
        NavigationStack(path: $path) {
            HomeView(user: user, path: $path)
                .navigationDestination(for: Route.self) { route in
                    switch route {
                    case .detail(let item): DetailView(item: item)
                    case .live: LiveView()
                    }
                }
        }
    }
}

/// The in-app Back button of a pushed screen (the system one is hidden with the bar).
struct BackButton: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        // A plain text label: a Label's icon and title would show up as elements of their own.
        Button("Back") { dismiss() }
            .accessibilityIdentifier("back")
    }
}

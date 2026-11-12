// Scenario 6, Navigation and back stack: pushed levels, tabs with their own stacks, a dialog
// before leaving, an unsaved-changes guard, and the deep link bench://level/3.
import SwiftUI
import UIKit

struct NavigationScenario: View {
    @EnvironmentObject private var router: Router
    @State private var tabs = false
    @State private var leave = false

    var body: some View {
        ScenarioScreen(scenario: .navigation) {
            status("Depth 0", "nav_depth")
            Button("Push") { router.path.append(.level(1)) }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("nav_push")
            Button("Tabbed screens") { tabs = true }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("nav_tabs")
            Button("Open confirm") { leave = true }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("nav_dialog")
            Button("Edit form") { router.path.append(.navForm) }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("nav_form")
        }
        .overlay {
            if leave {
                InAppDialog(id: "leave_dialog", title: "Leave?", buttons: [
                    ("Stay", "leave_stay", { leave = false }),
                    ("Leave", "leave_go", {
                        leave = false
                        router.path.removeAll()
                    }),
                ])
            }
        }
        .fullScreenCover(isPresented: $tabs) { TabbedScreens() }
    }
}

/// "Level N": pushes up to depth 5, pops one, or pops back to the scenario screen (depth 0).
struct LevelView: View {
    @EnvironmentObject private var router: Router
    let depth: Int

    var body: some View {
        SubScreen(title: "Level \(depth)", titleID: "level_title") {
            VStack(alignment: .leading, spacing: 16) {
                status("Depth \(depth)", "nav_depth")
                Button("Push") { router.path.append(.level(depth + 1)) }
                    .buttonStyle(.bordered)
                    .disabled(depth >= 5)
                    .accessibilityIdentifier("nav_push")
                Button("Pop") { router.path.removeLast() }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("nav_pop")
                Button("Pop to root") {
                    if let root = router.path.lastIndex(of: .scenario(.navigation)) {
                        router.path.removeSubrange((root + 1)...)
                    }
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("nav_home")
            }
            .padding(.horizontal, 20)
        }
    }
}

/// A screen with a bottom tab bar; each tab keeps its own navigation stack.
private struct TabbedScreens: View {
    @Environment(\.dismiss) private var dismiss
    @State private var tab = "feed"
    @State private var feedPath: [String] = []

    var body: some View {
        TabView(selection: $tab) {
            // Keeps the system navigation bar (no .toolbar(.hidden)): its back button
            // (BackButton) pops Post, as navigation.yaml taps it on iOS (SCENARIOS.md, issue #213).
            NavigationStack(path: $feedPath) {
                VStack(spacing: 16) {
                    Text(verbatim: "Feed").font(.title.bold())
                    Button("Open post") { feedPath.append("post") }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("open_post")
                    closeButton
                }
                .navigationDestination(for: String.self) { _ in
                    VStack(spacing: 16) {
                        Text(verbatim: "Post").font(.title.bold()).accessibilityIdentifier("post_title")
                        closeButton
                    }
                }
            }
            .tabItem { Label("Feed", systemImage: "list.bullet").accessibilityIdentifier("navtab_feed") }
            .tag("feed")
            NavigationStack {
                VStack(spacing: 16) {
                    Text(verbatim: "Profile").font(.title.bold())
                    closeButton
                }
            }
            .tabItem { Label("Profile", systemImage: "person").accessibilityIdentifier("navtab_profile") }
            .tag("profile")
        }
    }

    private var closeButton: some View {
        Button("Close tabs") { dismiss() }
            .accessibilityIdentifier("tabs_close")
    }
}

/// A form that asks before throwing away what was typed.
struct NavFormView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var title = ""
    @State private var asking = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            BackButton {
                if title.isEmpty { dismiss() } else { asking = true }
            }
            Text(verbatim: "Edit form").font(.title.bold())
            TextField("Title", text: $title)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("form_field")
            Spacer()
        }
        .padding(.horizontal, 20)
        .padding(.top, 8)
        .toolbar(.hidden, for: .navigationBar)
        // No swipe-back past unsaved changes: the Back button asks first.
        .background(PopGestureBlocker(blocked: !title.isEmpty))
        .overlay {
            if asking {
                InAppDialog(id: "discard_dialog", title: "Discard changes?", buttons: [
                    ("Keep editing", "discard_keep", { asking = false }),
                    ("Discard", "discard_yes", {
                        asking = false
                        dismiss()
                    }),
                ])
            }
        }
    }
}

/// Turns the navigation controller's swipe-back gesture off while `blocked`.
struct PopGestureBlocker: UIViewControllerRepresentable {
    let blocked: Bool

    func makeUIViewController(context: Context) -> Controller { Controller() }

    func updateUIViewController(_ controller: Controller, context: Context) {
        controller.blocked = blocked
    }

    final class Controller: UIViewController {
        var blocked = false { didSet { apply() } }

        override func viewDidAppear(_ animated: Bool) {
            super.viewDidAppear(animated)
            apply()
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            navigationController?.interactivePopGestureRecognizer?.isEnabled = true
        }

        private func apply() {
            navigationController?.interactivePopGestureRecognizer?.isEnabled = !blocked
        }
    }
}

/// An in-app dialog: a card over a dimmed screen, with an id on the card and each button.
struct InAppDialog: View {
    let id: String
    let title: String
    var message: String?
    let buttons: [(String, String, () -> Void)]

    var body: some View {
        ZStack {
            Color.black.opacity(0.35)
                .ignoresSafeArea()
                .accessibilityHidden(true)
            VStack(spacing: 16) {
                Text(verbatim: title).font(.headline)
                if let message { Text(verbatim: message) }
                HStack(spacing: 12) {
                    ForEach(buttons.indices, id: \.self) { i in
                        Button(buttons[i].0, action: buttons[i].2)
                            .buttonStyle(.bordered)
                            .accessibilityIdentifier(buttons[i].1)
                    }
                }
            }
            .padding(24)
            .background(Color(.systemBackground), in: RoundedRectangle(cornerRadius: 14))
            .padding(32)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier(id)
            .accessibilityAddTraits(.isModal)
        }
        .contentShape(Rectangle())
    }
}

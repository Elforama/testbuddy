// The start screen (bench/SCENARIOS.md): a list of scenarios, each a self-contained part of the
// app that puts a test runner in a known hard situation. Plus the shared scenario screen frame.
import SwiftUI

enum Scenario: String, CaseIterable, Hashable {
    case journey, slow, permissions, overlays, controls, navigation, onboarding, lists
    case a11y_poor, a11y_good, native, animations, errors, keyboard
    case transitions

    var name: String {
        switch self {
        case .journey: "Journey"
        case .slow: "Slow and janky"
        case .permissions: "Permissions"
        case .overlays: "Overlays"
        case .controls: "Controls"
        case .navigation: "Navigation and back stack"
        case .onboarding: "Onboarding and login"
        case .lists: "Lists"
        case .a11y_poor: "Poor accessibility"
        case .a11y_good: "Good accessibility"
        case .native: "Native toolkits"
        case .animations: "Animations"
        case .errors: "Errors and retries"
        case .keyboard: "Keyboard"
        case .transitions: "Screen transitions"
        }
    }

    @ViewBuilder var screen: some View {
        switch self {
        case .journey: JourneyView()
        case .slow: SlowScenario()
        case .permissions: PermissionsScenario()
        case .overlays: OverlaysScenario()
        case .controls: ControlsScenario()
        case .navigation: NavigationScenario()
        case .onboarding: OnboardingScenario()
        case .lists: ListsScenario()
        case .a11y_poor: PoorAccessibilityScenario()
        case .a11y_good: GoodAccessibilityScenario()
        case .native: ToolkitsScenario()
        case .animations: AnimationsScenario()
        case .errors: ErrorsScenario()
        case .keyboard: KeyboardScenario()
        case .transitions: TransitionsScenario()
        }
    }
}

struct ScenarioListView: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(verbatim: "Scenarios")
                .font(.largeTitle.bold())
                .accessibilityIdentifier("scenarios_title")
                .padding(.horizontal, 20)
                .padding(.vertical, 12)
            // A List (a UICollectionView): rows scrolled out of it leave the accessibility
            // tree, so the last rows really are below the fold.
            List(Scenario.allCases, id: \.self) { scenario in
                NavigationLink(value: Route.scenario(scenario)) {
                    Text(verbatim: scenario.name)
                        .frame(maxWidth: .infinity, minHeight: 64, alignment: .leading)
                }
                .accessibilityIdentifier("scenario_\(scenario.rawValue)")
                .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
            }
            .listStyle(.plain)
            .accessibilityIdentifier("scenarios")
        }
        .toolbar(.hidden, for: .navigationBar)
    }
}

/// The frame of every scenario screen: Back at the top left, the title (`scenario_title`),
/// then the content, in a page that scrolls (`page`) when `scrolls` is set.
struct ScenarioScreen<Content: View>: View {
    let scenario: Scenario
    var scrolls = false
    var back: (() -> Void)?
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            BackButton(action: back)
                .padding(.horizontal, 20)
            Text(verbatim: scenario.name)
                .font(.title.bold())
                .accessibilityIdentifier("scenario_title")
                .padding(.horizontal, 20)
            if scrolls {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) { content }
                        .padding(.horizontal, 20)
                        .padding(.bottom, 40)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .accessibilityIdentifier("page")
            } else {
                VStack(alignment: .leading, spacing: 16) { content }
                    .padding(.horizontal, 20)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
        }
        .padding(.top, 8)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .toolbar(.hidden, for: .navigationBar)
    }
}

/// A pushed sub-screen of a scenario: Back, a title with the given id, then the content.
struct SubScreen<Content: View>: View {
    let title: String
    let titleID: String
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            BackButton().padding(.horizontal, 20)
            Text(verbatim: title)
                .font(.title.bold())
                .accessibilityIdentifier(titleID)
                .padding(.horizontal, 20)
            content
        }
        .padding(.top, 8)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .toolbar(.hidden, for: .navigationBar)
    }
}

/// Scenario 1, Journey: the original Buddy Bench (SPEC.md). Login until someone signs in,
/// then Home; Detail and Live are pushed on the app's one navigation stack.
struct JourneyView: View {
    @EnvironmentObject private var model: BenchModel

    var body: some View {
        if let user = model.user {
            HomeView(user: user)
        } else {
            LoginView()
        }
    }
}

/// A text with an id: the status lines of every scenario.
func status(_ text: String, _ id: String) -> some View {
    Text(verbatim: text).accessibilityIdentifier(id)
}

/// Sleeps on the main actor, then runs `body` unless the task was cancelled.
@MainActor func after(_ ms: Int, _ body: @escaping @MainActor () -> Void) -> Task<Void, Never> {
    Task { @MainActor in
        guard (try? await Task.sleep(for: .milliseconds(ms))) != nil else { return }
        body()
    }
}

/// A count of lines as the multiline fields report it: 0 when empty.
func lineCount(_ text: String) -> Int {
    text.isEmpty ? 0 : text.components(separatedBy: "\n").count
}

extension View {
    /// A list row of exactly `height` points (the List's own insets would add to it).
    func benchRow(_ height: CGFloat = 56) -> some View {
        frame(maxWidth: .infinity, minHeight: height, alignment: .leading)
            .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
    }
}

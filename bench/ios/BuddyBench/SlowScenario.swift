// Scenario 2, Slow and janky: a staged load, a late button, a slow push with a 1500 ms
// transition, a main-thread block, and a list that loads late.
import SwiftUI

struct SlowScenario: View {
    @EnvironmentObject private var router: Router
    @State private var loaded = false
    @State private var rowsShown = 0
    @State private var lateShown = false
    @State private var lateTaps = 0
    @State private var jankTaps = 0
    @State private var detail = false
    @State private var covered = false
    @State private var opening = false
    @State private var tasks: [Task<Void, Never>] = []

    private static let slide = Animation.easeInOut(duration: 1.5)

    var body: some View {
        ZStack {
            // Like a real push: once the slide has finished, the screen under it is gone
            // (from the screen and the accessibility tree).
            if !covered {
            ScenarioScreen(scenario: .slow, scrolls: true) {
                stagedLoad
                if lateShown {
                    Button("Late button") { lateTaps += 1 }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("late_button")
                }
                status("Late taps: \(lateTaps)", "late_count")
                Button("Open slowly") {
                    guard !opening else { return }
                    opening = true
                    _ = after(1200) {
                        opening = false
                        withAnimation(Self.slide) { detail = true }
                        _ = after(1500) { if detail { covered = true } }
                    }
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("slow_open")
                Button("Block UI") {
                    jankTaps += 1
                    // A busy loop on the main thread: nothing draws or takes touches.
                    let end = Date().addingTimeInterval(0.7)
                    while Date() < end {}
                    jankText = "Unblocked \(jankTaps)"
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("jank_button")
                status(jankText, "jank_text")
                Button("Slow list") { router.path.append(.slowList) }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("slow_list_open")
            }
            }

            if detail {
                SlowDetail {
                    covered = false
                    withAnimation(Self.slide) { detail = false }
                }
                    .transition(.move(edge: .trailing))
                    .zIndex(1)
            }
        }
        .toolbar(.hidden, for: .navigationBar)
        .onAppear(perform: start)
        .onDisappear { tasks.forEach { $0.cancel() } }
    }

    @State private var jankText = "Not blocked"

    @ViewBuilder private var stagedLoad: some View {
        if loaded {
            status("Loaded", "stage_text")
            ForEach(1...3, id: \.self) { n in
                if n <= rowsShown {
                    Text(verbatim: "Slow row \(n)")
                        .accessibilityIdentifier("slow_row_\(n)")
                        .transition(.opacity.animation(.easeIn(duration: 0.3)))
                }
            }
        } else {
            ForEach(1...3, id: \.self) { n in
                RoundedRectangle(cornerRadius: 6)
                    .fill(Color.gray.opacity(0.3))
                    .frame(width: CGFloat(260 - n * 40), height: 18)
                    .accessibilityElement()
                    .accessibilityIdentifier("skeleton_\(n)")
            }
        }
    }

    private func start() {
        guard tasks.isEmpty else { return }
        tasks = [
            after(2000) { loaded = true },
            after(2400) { withAnimation(.easeIn(duration: 0.3)) { rowsShown = 1 } },
            after(2800) { withAnimation(.easeIn(duration: 0.3)) { rowsShown = 2 } },
            after(3200) { withAnimation(.easeIn(duration: 0.3)) { rowsShown = 3 } },
            after(3000) { lateShown = true },
        ]
    }
}

/// The screen "pushed" with a custom 1500 ms slide (NavigationStack's own push can't be slowed).
private struct SlowDetail: View {
    let back: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            BackButton(action: back)
            Text(verbatim: "Slow detail")
                .font(.title.bold())
                .accessibilityIdentifier("slow_detail_title")
            Spacer()
        }
        .padding(.horizontal, 20)
        .padding(.top, 8)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color(.systemBackground).ignoresSafeArea())
    }
}

struct SlowListView: View {
    @State private var count = 0
    @State private var loadingMore = false

    var body: some View {
        SubScreen(title: "Slow list", titleID: "slow_list_title") {
            if count == 0 {
                ProgressView()
                    .frame(maxWidth: .infinity)
                    .accessibilityIdentifier("slow_list_spinner")
                Spacer()
            } else {
                List {
                    ForEach(1...count, id: \.self) { n in
                        Text(verbatim: "Slow item \(n)")
                            .benchRow()
                            .accessibilityIdentifier("slow_item_\(n)")
                            .onAppear { if n == count { loadMore() } }
                    }
                    if loadingMore {
                        ProgressView()
                            .frame(maxWidth: .infinity)
                            .accessibilityIdentifier("slow_list_spinner")
                    }
                }
                .listStyle(.plain)
            }
        }
        .task {
            guard count == 0, (try? await Task.sleep(for: .milliseconds(2500))) != nil else { return }
            count = 20
        }
    }

    private func loadMore() {
        guard count == 20, !loadingMore else { return }
        loadingMore = true
        _ = after(2500) {
            count = 40
            loadingMore = false
        }
    }
}

import SwiftUI

struct HomeView: View {
    @EnvironmentObject private var model: BenchModel
    @EnvironmentObject private var router: Router
    let user: String
    @State private var itemCount = 40
    @State private var loadingMore = false

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            List {
                ForEach(1...itemCount, id: \.self) { item in
                    NavigationLink(value: Route.detail(item)) {
                        Text(verbatim: "Item \(item)")
                            .frame(maxWidth: .infinity, minHeight: 64, alignment: .leading)
                    }
                    .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
                }
                loadMore
                    .frame(maxWidth: .infinity, minHeight: 64)
                    .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
            }
            .listStyle(.plain)
            .accessibilityIdentifier("items")
        }
        .toolbar(.hidden, for: .navigationBar)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(verbatim: "Welcome, \(user)")
                .font(.title2.bold())
                .accessibilityIdentifier("welcome")
            Text(verbatim: "Cart: \(model.cart)")
                .accessibilityIdentifier("cart_count")
            HStack(spacing: 8) {
                ProgressView()
                    .accessibilityIdentifier("home_spinner")
                Text(verbatim: "Syncing\u{2026}")
                    .accessibilityIdentifier("sync_label")
            }
            Ticker()
            HStack {
                Button("Live") { router.path.append(.live) }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("open_live")
                Spacer()
                Button("Log out") { model.user = nil }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("logout")
            }
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder private var loadMore: some View {
        if loadingMore {
            ProgressView()
                .accessibilityIdentifier("load_more_spinner")
        } else {
            Button("Load more") {
                loadingMore = true
                Task { @MainActor in
                    try? await Task.sleep(for: Timing.loadMore)
                    itemCount += 10
                    loadingMore = false
                }
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("load_more")
        }
    }
}

private struct Ticker: View {
    @StateObject private var clock = TickerClock()

    var body: some View {
        Text(verbatim: "Live: \(clock.shown)")
            .monospacedDigit()
            .accessibilityIdentifier("ticker")
            .onAppear { clock.visible = true }
            .onDisappear { clock.visible = false }
    }
}

private final class TickerClock: ObservableObject {
    @Published private(set) var shown = 0
    private var count = 0
    private var timer: Timer?
    var visible = true {
        didSet { if visible { shown = count } }
    }

    init() {
        let timer = Timer(timeInterval: Timing.ticker, repeats: true) { [weak self] _ in
            guard let self else { return }
            count += 1
            if visible { shown = count }
        }
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
    }

    deinit { timer?.invalidate() }
}

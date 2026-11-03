// Scenario 8, Lists: long, grid, carousel, two-pane, pull-to-refresh, swipe-to-delete,
// reorder, endless and empty lists, each on its own pushed screen.
import SwiftUI

enum ListKind: String, CaseIterable, Hashable {
    case long, grid, carousels, twoPane, refresh, swipeDelete, reorder, infinite, empty

    var button: (id: String, text: String) {
        switch self {
        case .long: ("open_long_list", "Long list")
        case .grid: ("open_grid", "Grid")
        case .carousels: ("open_carousels", "Carousels")
        case .twoPane: ("open_two_pane", "Two panes")
        case .refresh: ("open_refresh", "Pull to refresh")
        case .swipeDelete: ("open_swipe_delete", "Swipe to delete")
        case .reorder: ("open_reorder", "Reorder")
        case .infinite: ("open_infinite", "Endless feed")
        case .empty: ("open_empty", "Empty list")
        }
    }

    @ViewBuilder var screen: some View {
        switch self {
        case .long: LongListView()
        case .grid: GridListView()
        case .carousels: CarouselsView()
        case .twoPane: TwoPaneView()
        case .refresh: RefreshListView()
        case .swipeDelete: SwipeDeleteView()
        case .reorder: ReorderView()
        case .infinite: InfiniteFeedView()
        case .empty: EmptyListView()
        }
    }
}

struct ListsScenario: View {
    @EnvironmentObject private var router: Router

    var body: some View {
        ScenarioScreen(scenario: .lists) {
            ForEach(ListKind.allCases, id: \.self) { kind in
                Button(kind.button.text) { router.path.append(.list(kind)) }
                    .accessibilityIdentifier(kind.button.id)
            }
        }
    }
}

/// A list sub-screen: Back, a title, and "Picked …" at the top.
private struct ListScreen<Content: View>: View {
    let title: String
    var picked: String?
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            BackButton().padding(.horizontal, 20)
            Text(verbatim: title).font(.title2.bold()).padding(.horizontal, 20)
            if let picked {
                status(picked, "picked").padding(.horizontal, 20)
            }
            content
        }
        .padding(.top, 8)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .toolbar(.hidden, for: .navigationBar)
    }
}

private let nothingPicked = "Nothing picked"

private struct LongListView: View {
    @State private var picked = nothingPicked

    var body: some View {
        ListScreen(title: "Long list", picked: picked) {
            List {
                ForEach(0..<20, id: \.self) { section in
                    let letter = String(UnicodeScalar(UInt8(65 + section)))
                    Section {
                        ForEach(section * 25 + 1...section * 25 + 25, id: \.self) { n in
                            Button { picked = "Picked Row \(n)" } label: {
                                Text(verbatim: "Row \(n)")
                                    .frame(maxWidth: .infinity, minHeight: 56, alignment: .leading)
                                    .contentShape(Rectangle())
                            }
                            .foregroundStyle(.primary)
                            .accessibilityIdentifier("row_\(n)")
                            .benchRow()
                        }
                    } header: {
                        Text(verbatim: "Section \(letter)")
                            .accessibilityIdentifier("section_\(letter)")
                    }
                }
            }
            .listStyle(.plain)
        }
    }
}

private struct GridListView: View {
    @State private var picked = nothingPicked

    var body: some View {
        ListScreen(title: "Grid", picked: picked) {
            ScrollView {
                LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 8), count: 3),
                          spacing: 8) {
                    ForEach(1...120, id: \.self) { n in
                        Button { picked = "Picked Cell \(n)" } label: {
                            Text(verbatim: "Cell \(n)")
                                .frame(maxWidth: .infinity)
                                .aspectRatio(1, contentMode: .fit)
                                .background(Color(.secondarySystemBackground),
                                            in: RoundedRectangle(cornerRadius: 8))
                        }
                        .foregroundStyle(.primary)
                        .accessibilityIdentifier("cell_\(n)")
                    }
                }
                .padding(.horizontal, 12)
            }
        }
    }
}

private struct CarouselsView: View {
    @State private var picked = nothingPicked

    var body: some View {
        ListScreen(title: "Carousels", picked: picked) {
            // A ScrollView, not a List: a List cell merges its children into one element
            // ("Shelf 1, Card 1-1, …"), and each card must be its own button.
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    ForEach(1...10, id: \.self) { shelf in
                        VStack(alignment: .leading, spacing: 8) {
                            Text(verbatim: "Shelf \(shelf)").font(.headline)
                                .padding(.horizontal, 20)
                            ScrollView(.horizontal, showsIndicators: false) {
                                LazyHStack(spacing: 10) {
                                    ForEach(1...15, id: \.self) { n in
                                        Button { picked = "Picked Card \(shelf)-\(n)" } label: {
                                            Text(verbatim: "Card \(shelf)-\(n)")
                                                .frame(width: 140, height: 90)
                                                .background(Color(.secondarySystemBackground),
                                                            in: RoundedRectangle(cornerRadius: 10))
                                        }
                                        .buttonStyle(.plain)
                                        .accessibilityIdentifier("card_\(shelf)_\(n)")
                                    }
                                }
                                .padding(.horizontal, 20)
                            }
                        }
                    }
                }
            }
        }
    }
}

private struct TwoPaneView: View {
    @State private var selected = 1

    var body: some View {
        ListScreen(title: "Two panes") {
            GeometryReader { geo in
                HStack(spacing: 0) {
                    List(1...80, id: \.self) { n in
                        Button { selected = n } label: {
                            Text(verbatim: "Mail \(n)")
                                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                                .contentShape(Rectangle())
                        }
                        .foregroundStyle(.primary)
                        .listRowBackground(n == selected ? Color.accentColor.opacity(0.15) : nil)
                        .accessibilityIdentifier("mail_\(n)")
                    }
                    .listStyle(.plain)
                    .frame(width: geo.size.width * 0.4)
                    Divider()
                    VStack(alignment: .leading, spacing: 8) {
                        Text(verbatim: "Mail \(selected)").font(.headline)
                            .accessibilityIdentifier("mail_detail")
                        ScrollView {
                            VStack(alignment: .leading, spacing: 4) {
                                ForEach(1...60, id: \.self) { k in
                                    Text(verbatim: "Line \(k) of mail \(selected)")
                                }
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .id(selected)
                        .accessibilityIdentifier("mail_body")
                    }
                    .padding(.horizontal, 12)
                    .frame(width: geo.size.width * 0.6)
                }
            }
        }
    }
}

private struct RefreshListView: View {
    @State private var count = 0
    @State private var refreshing = false

    var body: some View {
        ListScreen(title: "Pull to refresh") {
            status("Refreshed \(count) times", "refresh_count").padding(.horizontal, 20)
            List {
                if refreshing {
                    ProgressView().frame(maxWidth: .infinity)
                        .accessibilityIdentifier("refresh_spinner")
                }
                ForEach(1...10, id: \.self) { n in
                    Text(verbatim: "Fresh \(n)").frame(minHeight: 44)
                }
            }
            .listStyle(.plain)
            .refreshable {
                refreshing = true
                try? await Task.sleep(for: .milliseconds(1000))
                refreshing = false
                count += 1
            }
        }
    }
}

private struct SwipeDeleteView: View {
    @State private var tasks = Array(1...8)

    var body: some View {
        ListScreen(title: "Swipe to delete") {
            status("Tasks: \(tasks.count)", "delete_status").padding(.horizontal, 20)
            List {
                ForEach(tasks, id: \.self) { n in
                    Text(verbatim: "Task \(n)")
                        .benchRow()
                        .accessibilityIdentifier("task_\(n)")
                }
                .onDelete { tasks.remove(atOffsets: $0) }
            }
            .listStyle(.plain)
        }
    }
}

private struct ReorderView: View {
    @State private var rows = ["A", "B", "C", "D", "E"]

    var body: some View {
        ListScreen(title: "Reorder") {
            status("Order: \(rows.joined())", "order_status").padding(.horizontal, 20)
            // With onMove, a List row can be long-pressed and dragged without edit mode.
            List {
                ForEach(rows, id: \.self) { row in
                    HStack {
                        Text(verbatim: row)
                            .accessibilityIdentifier("reorder_\(row)")
                        Spacer()
                        Image(systemName: "line.3.horizontal")
                            .foregroundStyle(.secondary)
                            .frame(width: 44, height: 44)
                            .accessibilityLabel("Reorder \(row)")
                            .accessibilityIdentifier("handle_\(row)")
                    }
                    .benchRow()
                }
                .onMove { rows.move(fromOffsets: $0, toOffset: $1) }
            }
            .listStyle(.plain)
        }
    }
}

private struct InfiniteFeedView: View {
    @State private var count = 20
    @State private var loading = false

    var body: some View {
        ListScreen(title: "Endless feed") {
            List {
                ForEach(1...count, id: \.self) { n in
                    Text(verbatim: "Post \(n)")
                        .benchRow()
                        .accessibilityIdentifier("post_\(n)")
                        .onAppear { if n == count { loadMore() } }
                }
                if loading {
                    ProgressView().frame(maxWidth: .infinity)
                        .accessibilityIdentifier("feed_spinner")
                }
            }
            .listStyle(.plain)
        }
    }

    private func loadMore() {
        guard !loading, count < 200 else { return }
        loading = true
        _ = after(800) {
            count = min(200, count + 20)
            loading = false
        }
    }
}

private struct EmptyListView: View {
    @State private var rows = 0

    var body: some View {
        ListScreen(title: "Empty list") {
            Button("Add one") { rows += 1 }
                .accessibilityIdentifier("empty_add")
                .padding(.horizontal, 20)
            if rows == 0 {
                VStack(spacing: 12) {
                    Image(systemName: "tray").font(.largeTitle).foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    status("Nothing here yet", "empty_text")
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(1...rows, id: \.self) { n in
                    Text(verbatim: "New row \(n)").accessibilityIdentifier("new_row_\(n)")
                }
                .listStyle(.plain)
            }
        }
    }
}

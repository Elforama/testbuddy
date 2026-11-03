// Screen 3, Detail: one item, Add to cart, and reviews that load after 600 ms.
import SwiftUI

struct DetailView: View {
    @EnvironmentObject private var model: BenchModel
    let item: Int
    @State private var reviewsLoaded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            BackButton()
            Text(verbatim: "Item \(item) details")
                .font(.title.bold())
                .accessibilityIdentifier("detail_title")
            Text(verbatim: "This is item \(item).")
                .accessibilityIdentifier("detail_text")
            Button("Add to cart") { model.cart += 1 }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("add_to_cart")
            Text(verbatim: "In cart: \(model.cart)")
                .accessibilityIdentifier("detail_cart")
            if reviewsLoaded {
                Text(verbatim: "4.8 stars")
                    .accessibilityIdentifier("reviews")
            } else {
                ProgressView()
                    .accessibilityIdentifier("reviews_spinner")
            }
            Spacer()
        }
        .padding(.horizontal, 20)
        .padding(.top, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .toolbar(.hidden, for: .navigationBar)
        .task {
            guard (try? await Task.sleep(for: Timing.reviews)) != nil else { return }
            reviewsLoaded = true
        }
    }
}

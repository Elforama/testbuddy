import SwiftUI
import UIKit

struct PoorAccessibilityScenario: View {
    @State private var result = "Nothing yet"
    @State private var nickname = ""

    var body: some View {
        ScenarioScreen(scenario: .a11y_poor, scrolls: true) {
            status(result, "poor_result")

            HStack(spacing: 32) {
                PlainIcon(symbol: "heart") { result = "Liked" }
                PlainIcon(symbol: "square.and.arrow.up") { result = "Shared" }
                PlainIcon(symbol: "trash") { result = "Trashed" }
            }

            GeometryReader { geo in
                Canvas { context, size in
                    let w = size.width / 5
                    for k in 0..<5 {
                        let rect = CGRect(x: CGFloat(k) * w + 2, y: size.height * CGFloat(4 - k) / 5,
                                          width: w - 4, height: size.height * CGFloat(k + 1) / 5)
                        context.fill(Path(rect), with: .color(.blue.opacity(0.3 + 0.14 * Double(k))))
                    }
                }
                .contentShape(Rectangle())
                .onTapGesture { point in
                    let k = min(5, max(1, Int(point.x / (geo.size.width / 5)) + 1))
                    result = "Volume \(k)"
                }
            }
            .frame(height: 44)

            HStack {
                Text(verbatim: "Nickname")
                TextField("", text: $nickname)
                    .autocorrectionDisabled()
                    .padding(6)
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary))
                    .onChange(of: nickname) { result = "Nickname: \(nickname)" }
            }

            Text(verbatim: "Continue")
                .foregroundStyle(.blue)
                .onTapGesture { result = "Continued" }

            ForEach(["Alpha", "Beta", "Gamma"], id: \.self) { row in
                HStack {
                    Text(verbatim: row)
                    Spacer()
                    Button("Edit") { result = "Edit \(row)" }
                }
            }

            MergedCard { result = "Tracking 42" }
                .frame(height: 70)

            HStack(spacing: 8) {
                Text(verbatim: "Tiny:")
                Circle().fill(Color.red).frame(width: 12, height: 12)
                    .onTapGesture { result = "Tiny tapped" }
            }

            Image(uiImage: textImage("Secret offer"))
                .onTapGesture { result = "Offer opened" }
        }
    }
}

private struct PlainIcon: View {
    let symbol: String
    let tap: () -> Void

    var body: some View {
        Image(uiImage: UIImage(systemName: symbol, withConfiguration:
            UIImage.SymbolConfiguration(pointSize: 26))!.withTintColor(.systemPink,
                                                                        renderingMode: .alwaysOriginal)
            .pngRoundTrip)
            .frame(width: 44, height: 44)
            .contentShape(Rectangle())
            .onTapGesture(perform: tap)
    }
}

private extension UIImage {
    var pngRoundTrip: UIImage {
        UIImage(data: UIGraphicsImageRenderer(size: size, format: pixelFormat)
            .pngData { _ in draw(at: .zero) }, scale: pixelFormat.scale)!
    }
}

private let pixelFormat: UIGraphicsImageRendererFormat = {
    let format = UIGraphicsImageRendererFormat()
    format.scale = 3
    return format
}()

private func textImage(_ text: String) -> UIImage {
    let attrs: [NSAttributedString.Key: Any] = [
        .font: UIFont.boldSystemFont(ofSize: 22), .foregroundColor: UIColor.white,
    ]
    let textSize = (text as NSString).size(withAttributes: attrs)
    let size = CGSize(width: textSize.width + 32, height: textSize.height + 24)
    let data = UIGraphicsImageRenderer(size: size, format: pixelFormat).pngData { _ in
        UIColor.systemPurple.setFill()
        UIBezierPath(roundedRect: CGRect(origin: .zero, size: size), cornerRadius: 10).fill()
        (text as NSString).draw(at: CGPoint(x: 16, y: 12), withAttributes: attrs)
    }
    return UIImage(data: data, scale: pixelFormat.scale)!
}

private struct MergedCard: UIViewRepresentable {
    let track: () -> Void

    func makeUIView(context: Context) -> UIView {
        let host = UIHostingController(rootView: OrderCard(track: track))
        host.view.backgroundColor = .clear
        host.view.translatesAutoresizingMaskIntoConstraints = false
        context.coordinator.host = host
        let card = UIView()
        card.addSubview(host.view)
        NSLayoutConstraint.activate([
            host.view.topAnchor.constraint(equalTo: card.topAnchor),
            host.view.bottomAnchor.constraint(equalTo: card.bottomAnchor),
            host.view.leadingAnchor.constraint(equalTo: card.leadingAnchor),
            host.view.trailingAnchor.constraint(equalTo: card.trailingAnchor),
        ])
        card.isAccessibilityElement = true
        card.accessibilityElements = []
        card.accessibilityLabel = "Order 42, 3 items, Track"
        return card
    }

    func updateUIView(_ view: UIView, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator() }
    final class Coordinator { var host: UIHostingController<OrderCard>? }
}

private struct OrderCard: View {
    let track: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(verbatim: "Order 42").font(.headline)
            HStack {
                Text(verbatim: "3 items").foregroundStyle(.secondary)
                Spacer()
                Button("Track", action: track)
                    .buttonStyle(.bordered)
            }
        }
        .padding(12)
        .frame(maxHeight: .infinity)
        .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12))
    }
}

struct GoodAccessibilityScenario: View {
    @State private var result = "Nothing yet"
    @State private var nickname = ""
    @State private var volume = 3.0
    @State private var saved = "Not saved"
    @State private var quantity = 1
    @State private var push = false

    var body: some View {
        ScenarioScreen(scenario: .a11y_good, scrolls: true) {
            status(result, "good_result")

            HStack(spacing: 32) {
                iconButton("heart", "Like", "good_like") { result = "Liked" }
                iconButton("square.and.arrow.up", "Share", "good_share") { result = "Shared" }
                iconButton("trash", "Trash", "good_trash") { result = "Trashed" }
            }

            Slider(value: $volume, in: 1...5, step: 1)
                .accessibilityLabel("Volume")
                .accessibilityValue("Volume \(Int(volume))")
                .accessibilityIdentifier("good_volume")
                .onChange(of: volume) { result = "Volume \(Int(volume))" }

            VStack(alignment: .leading, spacing: 4) {
                Text(verbatim: "Nickname").accessibilityHidden(true)
                TextField("Nickname", text: $nickname)
                    .textFieldStyle(.roundedBorder)
                    .autocorrectionDisabled()
                    .accessibilityLabel("Nickname")
                    .accessibilityIdentifier("good_nickname")
                    .onChange(of: nickname) { result = "Nickname: \(nickname)" }
            }

            Button("Continue") { result = "Continued" }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("good_continue")

            ForEach(["Alpha", "Beta", "Gamma"], id: \.self) { row in
                HStack {
                    Text(verbatim: row)
                    Spacer()
                    Button("Edit") { result = "Edit \(row)" }
                        .accessibilityLabel("Edit \(row)")
                        .accessibilityIdentifier("edit_\(row.lowercased())")
                }
            }

            HStack {
                VStack(alignment: .leading) {
                    Text(verbatim: "Order 42").font(.headline)
                    Text(verbatim: "3 items").foregroundStyle(.secondary)
                }
                Spacer()
                Button("Track") { result = "Tracking 42" }
                    .buttonStyle(.bordered)
                    .accessibilityLabel("Track order 42")
                    .accessibilityIdentifier("order_track")
            }
            .padding(12)
            .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12))
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("order_card")

            Button { result = "Tiny tapped" } label: {
                HStack(spacing: 8) {
                    Text(verbatim: "Tiny")
                    Circle().fill(Color.red).frame(width: 12, height: 12)
                }
                .frame(minWidth: 48, minHeight: 48)
                .contentShape(Rectangle())
            }
            .accessibilityIdentifier("good_tiny")

            Button { result = "Offer opened" } label: {
                Image(uiImage: textImage("Secret offer"))
            }
            .accessibilityLabel("Secret offer")
            .accessibilityAddTraits(.isImage)
            .accessibilityIdentifier("good_offer")

            HStack(spacing: 16) {
                Button("Save") {
                    saved = "Saving\u{2026}"
                    _ = after(1000) {
                        saved = "Saved"
                        UIAccessibility.post(notification: .announcement, argument: "Saved")
                    }
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("good_save")
                status(saved, "good_status")
                    .accessibilityAddTraits(.updatesFrequently)
            }

            HStack(spacing: 16) {
                Button(action: { quantity = max(0, quantity - 1); result = "Quantity \(quantity)" }) {
                    Text(verbatim: "\u{2212}").frame(width: 44, height: 44)
                }
                .accessibilityLabel("Decrease quantity")
                .accessibilityValue("\(quantity)")
                .accessibilityIdentifier("good_qty_minus")
                Text(verbatim: "\(quantity)").monospacedDigit()
                    .accessibilityIdentifier("good_qty_count")
                Button(action: { quantity = min(9, quantity + 1); result = "Quantity \(quantity)" }) {
                    Text(verbatim: "+").frame(width: 44, height: 44)
                }
                .accessibilityLabel("Increase quantity")
                .accessibilityValue("\(quantity)")
                .accessibilityIdentifier("good_qty_plus")
            }
            Toggle("Push notifications", isOn: $push)
                .labelsHidden()
                .accessibilityLabel("Push notifications")
                .accessibilityIdentifier("good_push")
                .onChange(of: push) { result = push ? "Push on" : "Push off" }
        }
    }

    private func iconButton(_ symbol: String, _ label: String, _ id: String,
                            _ tap: @escaping () -> Void) -> some View {
        Button(action: tap) {
            Image(systemName: symbol).font(.title2).frame(width: 44, height: 44)
        }
        .accessibilityLabel(label)
        .accessibilityIdentifier(id)
    }
}

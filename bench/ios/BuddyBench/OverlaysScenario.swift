import SwiftUI
import UIKit

struct OverlaysScenario: View {
    @State private var result = "Nothing yet"
    @State private var underTaps = 0
    @State private var sheet = false
    @State private var snackbar = false
    @State private var snackbarTask: Task<Void, Never>?
    @State private var veil = false
    @State private var tooltip = false
    @State private var cover = false

    var body: some View {
        ScenarioScreen(scenario: .overlays) {
            status(result, "overlay_result")
            Button("Open dialog", action: openDialog)
                .accessibilityIdentifier("open_dialog")
            Button("Open sheet") { sheet = true }
                .accessibilityIdentifier("open_sheet")
            Button("Show snackbar", action: showSnackbar)
                .accessibilityIdentifier("show_snackbar")
            Button("Show veil") {
                veil = true
                _ = after(1500) { veil = false }
            }
            .accessibilityIdentifier("show_veil")
            Button("Show hidden veil", action: showHiddenVeil)
            .accessibilityIdentifier("show_hidden_veil")
            Button("Show tooltip") { tooltip = true }
                .accessibilityIdentifier("show_tooltip")
                .popover(isPresented: $tooltip, attachmentAnchor: .rect(.bounds),
                         arrowEdge: .top) {
                    Text(verbatim: "Tip: long press for more")
                        .font(.callout)
                        .padding(12)
                        .accessibilityIdentifier("tooltip_text")
                        .presentationCompactAdaptation(.popover)
                }
            Button("Open cover") { cover = true }
                .accessibilityIdentifier("open_cover")
            Spacer()
            status("Under taps: \(underTaps)", "under_count")
            Button { underTaps += 1 } label: {
                Text(verbatim: "Under").frame(maxWidth: .infinity, minHeight: 44)
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("under_button")
            .padding(.bottom, 8)
        }
        .overlay(alignment: .bottom) {
            if snackbar {
                HStack {
                    Text(verbatim: "Saved").accessibilityIdentifier("snackbar_text")
                    Spacer()
                    Button("Undo") {
                        result = "Undone"
                        hideSnackbar()
                    }
                    .accessibilityIdentifier("snackbar_undo")
                }
                .foregroundStyle(.white)
                .tint(.yellow)
                .padding(.horizontal, 16)
                .frame(minHeight: 64)
                .background(Color(white: 0.2), in: RoundedRectangle(cornerRadius: 10))
                .padding(.horizontal, 12)
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .overlay {
            if veil {
                CompactLoadingVeil().ignoresSafeArea()
            }
        }
        .sheet(isPresented: $sheet) {
            VStack(spacing: 20) {
                Text(verbatim: "Sheet").font(.title2.bold()).accessibilityIdentifier("sheet_title")
                Button("Sheet action") { result = "Sheet action" }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("sheet_action")
                Button("Close") { sheet = false }
                    .accessibilityIdentifier("sheet_close")
            }
            .padding(.top, 24)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .presentationDetents([.medium])
            .presentationDragIndicator(.visible)
        }
        .fullScreenCover(isPresented: $cover) { CoverView() }
    }

    private func openDialog() {
        let alert = UIAlertController(title: "Delete item?", message: nil, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { _ in result = "Cancelled" })
        alert.addAction(UIAlertAction(title: "Delete", style: .destructive) { _ in result = "Deleted" })
        topViewController()?.present(alert, animated: true) {
            tagLabel(in: alert.view, text: "Delete item?", id: "dialog_title")
        }
    }

    private func showHiddenVeil() {
        guard let window = topViewController()?.view.window else { return }
        let cover = UIView(frame: window.bounds)
        cover.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        cover.backgroundColor = UIColor.black.withAlphaComponent(0.4)
        let spinner = UIActivityIndicatorView(style: .large)
        spinner.color = .white
        spinner.center = CGPoint(x: cover.bounds.midX, y: cover.bounds.midY)
        spinner.autoresizingMask = [.flexibleLeftMargin, .flexibleRightMargin, .flexibleTopMargin, .flexibleBottomMargin]
        spinner.startAnimating()
        cover.addSubview(spinner)
        cover.accessibilityElementsHidden = true
        window.addSubview(cover)
        _ = after(2500) { cover.removeFromSuperview() }
    }

    private func showSnackbar() {
        snackbarTask?.cancel()
        withAnimation { snackbar = true }
        snackbarTask = after(3000) { withAnimation { snackbar = false } }
    }

    private func hideSnackbar() {
        snackbarTask?.cancel()
        withAnimation { snackbar = false }
    }
}

private struct CompactLoadingVeil: UIViewRepresentable {
    func makeUIView(context: Context) -> UIView {
        let cover = UIView()
        cover.backgroundColor = UIColor.black.withAlphaComponent(0.4)
        let spinner = UIActivityIndicatorView(style: .large)
        spinner.color = .white
        spinner.translatesAutoresizingMaskIntoConstraints = false
        spinner.isAccessibilityElement = true
        spinner.startAnimating()
        cover.addSubview(spinner)
        NSLayoutConstraint.activate([
            spinner.centerXAnchor.constraint(equalTo: cover.centerXAnchor),
            spinner.centerYAnchor.constraint(equalTo: cover.centerYAnchor),
        ])
        cover.accessibilityElements = [spinner]
        return cover
    }

    func updateUIView(_ view: UIView, context: Context) {}
}

private struct CoverView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var sheet = false
    @State private var result = ""

    var body: some View {
        VStack(spacing: 20) {
            Text(verbatim: "Cover").font(.largeTitle.bold()).accessibilityIdentifier("cover_title")
            Button("Open sheet on cover") { sheet = true }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("cover_sheet")
            Button("Close cover") { dismiss() }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("cover_close")
            if !result.isEmpty {
                status(result, "cover_result")
            }
            Spacer()
        }
        .padding(.top, 60)
        .frame(maxWidth: .infinity)
        .background(Color(.systemTeal).opacity(0.25).ignoresSafeArea())
        .sheet(isPresented: $sheet) {
            VStack(spacing: 20) {
                Text(verbatim: "Sheet on cover").font(.title2.bold())
                    .accessibilityIdentifier("cover_sheet_title")
                Button("Done") {
                    result = "Done on cover"
                    sheet = false
                }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("cover_sheet_done")
            }
            .padding(.top, 24)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .presentationDetents([.large])
        }
    }
}

func topViewController() -> UIViewController? {
    let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
    var top = scenes.flatMap(\.windows).first(where: \.isKeyWindow)?.rootViewController
    while let presented = top?.presentedViewController { top = presented }
    return top
}

@discardableResult
func tagLabel(in view: UIView, text: String, id: String) -> Bool {
    if let label = view as? UILabel, label.text == text {
        label.accessibilityIdentifier = id
        return true
    }
    return view.subviews.contains { tagLabel(in: $0, text: text, id: id) }
}

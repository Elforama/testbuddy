// Scenario 11, Native toolkits: the same mini screen built with SwiftUI (List), UIKit (a
// UITableView in a UIViewController) and WKWebView (a local HTML page).
import SwiftUI
import UIKit
import WebKit

enum Toolkit: String, CaseIterable, Hashable {
    case swiftui, uikit, webview, browser, rotation

    var name: String {
        switch self {
        case .swiftui: "SwiftUI"
        case .uikit: "UIKit"
        case .webview: "WKWebView"
        case .browser: "Embedded browser"
        case .rotation: "Rotation readout"
        }
    }

    @ViewBuilder var screen: some View {
        switch self {
        case .swiftui: SwiftUIMini()
        case .uikit: UIKitMini()
        case .webview: WebMini()
        case .browser: VStack { BackButton(); LocalBrowser() }
        case .rotation: RotationReadout()
        }
    }
}

private struct RotationReadout: View {
    @State private var orientation = UIDevice.current.orientation

    var body: some View {
        GeometryReader { geometry in
            VStack {
                BackButton()
                Text(geometry.size.width > geometry.size.height ? "Landscape layout" : "Portrait layout")
                switch orientation {
                case .landscapeLeft: Text("Device turned left")
                case .landscapeRight: Text("Device turned right")
                default: EmptyView()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear {
            UIDevice.current.beginGeneratingDeviceOrientationNotifications()
            orientation = UIDevice.current.orientation
        }
        .onDisappear { UIDevice.current.endGeneratingDeviceOrientationNotifications() }
        .onReceive(NotificationCenter.default.publisher(for: UIDevice.orientationDidChangeNotification)) { _ in
            orientation = UIDevice.current.orientation
        }
    }
}

// MARK: SwiftUI

private struct SwiftUIMini: View {
    @State private var taps = 0
    @State private var on = false
    @State private var text = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            BackButton()
            Text(verbatim: "SwiftUI").font(.title.bold()).accessibilityIdentifier("tk_title")
            HStack {
                Button("Tap") { taps += 1 }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("tk_tap")
                status("Taps: \(taps)", "tk_count")
            }
            HStack {
                Toggle("", isOn: $on).labelsHidden().accessibilityIdentifier("tk_switch")
                status(on ? "On" : "Off", "tk_switch_status")
            }
            TextField("Type here", text: $text)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                .accessibilityIdentifier("tk_field")
            status("Echo: \(text)", "tk_echo")
            List(1...30, id: \.self) { n in
                Text(verbatim: "Toolkit row \(n)").accessibilityIdentifier("tk_row_\(n)")
            }
            .listStyle(.plain)
        }
        .padding(.horizontal, 20)
        .padding(.top, 8)
        .toolbar(.hidden, for: .navigationBar)
    }
}

// MARK: UIKit

private struct UIKitMini: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        UIKitMiniRepresentable(back: { dismiss() })
            .toolbar(.hidden, for: .navigationBar)
    }
}

private struct UIKitMiniRepresentable: UIViewControllerRepresentable {
    let back: () -> Void

    func makeUIViewController(context: Context) -> UIKitMiniController {
        let controller = UIKitMiniController()
        controller.back = back
        return controller
    }

    func updateUIViewController(_ controller: UIKitMiniController, context: Context) {
        controller.back = back
    }
}

/// A plain UIKit screen: a stack of controls over a UITableView of 30 rows.
final class UIKitMiniController: UIViewController, UITableViewDataSource {
    var back: (() -> Void)?
    private var taps = 0
    private let countLabel = UILabel()
    private let switchLabel = UILabel()
    private let echoLabel = UILabel()
    private let table = UITableView(frame: .zero, style: .plain)

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground

        let backButton = UIButton(type: .system)
        backButton.setTitle("Back", for: .normal)
        backButton.accessibilityIdentifier = "back"
        backButton.contentHorizontalAlignment = .leading
        backButton.addAction(UIAction { [weak self] _ in self?.back?() }, for: .touchUpInside)

        let title = UILabel()
        title.text = "UIKit"
        title.font = .preferredFont(forTextStyle: .title1).bold
        title.accessibilityIdentifier = "tk_title"

        var config = UIButton.Configuration.bordered()
        config.title = "Tap"
        let tap = UIButton(configuration: config)
        tap.accessibilityIdentifier = "tk_tap"
        tap.addAction(UIAction { [weak self] _ in
            guard let self else { return }
            taps += 1
            countLabel.text = "Taps: \(taps)"
        }, for: .touchUpInside)
        countLabel.text = "Taps: 0"
        countLabel.accessibilityIdentifier = "tk_count"

        let toggle = UISwitch()
        toggle.accessibilityIdentifier = "tk_switch"
        toggle.addAction(UIAction { [weak self] action in
            self?.switchLabel.text = (action.sender as? UISwitch)?.isOn == true ? "On" : "Off"
        }, for: .valueChanged)
        switchLabel.text = "Off"
        switchLabel.accessibilityIdentifier = "tk_switch_status"

        let field = UITextField()
        field.placeholder = "Type here"
        field.borderStyle = .roundedRect
        field.autocorrectionType = .no
        field.accessibilityIdentifier = "tk_field"
        field.addAction(UIAction { [weak self] action in
            self?.echoLabel.text = "Echo: \((action.sender as? UITextField)?.text ?? "")"
        }, for: .editingChanged)
        echoLabel.text = "Echo: "
        echoLabel.accessibilityIdentifier = "tk_echo"

        let tapRow = UIStackView(arrangedSubviews: [tap, countLabel])
        tapRow.spacing = 12
        let switchRow = UIStackView(arrangedSubviews: [toggle, switchLabel])
        switchRow.spacing = 12
        let stack = UIStackView(arrangedSubviews: [backButton, title, tapRow, switchRow, field, echoLabel])
        stack.axis = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        field.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true

        table.dataSource = self
        table.register(UITableViewCell.self, forCellReuseIdentifier: "row")

        for v in [stack, table] as [UIView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            view.addSubview(v)
        }
        let guide = view.safeAreaLayoutGuide
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: guide.topAnchor, constant: 8),
            stack.leadingAnchor.constraint(equalTo: guide.leadingAnchor, constant: 20),
            stack.trailingAnchor.constraint(equalTo: guide.trailingAnchor, constant: -20),
            table.topAnchor.constraint(equalTo: stack.bottomAnchor, constant: 10),
            table.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            table.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            table.bottomAnchor.constraint(equalTo: view.bottomAnchor),
        ])
    }

    func tableView(_ tableView: UITableView, numberOfRowsInSection section: Int) -> Int { 30 }

    func tableView(_ tableView: UITableView, cellForRowAt indexPath: IndexPath) -> UITableViewCell {
        let cell = tableView.dequeueReusableCell(withIdentifier: "row", for: indexPath)
        var content = cell.defaultContentConfiguration()
        content.text = "Toolkit row \(indexPath.row + 1)"
        cell.contentConfiguration = content
        cell.accessibilityIdentifier = "tk_row_\(indexPath.row + 1)"
        return cell
    }
}

private extension UIFont {
    var bold: UIFont {
        UIFont(descriptor: fontDescriptor.withSymbolicTraits(.traitBold) ?? fontDescriptor, size: 0)
    }
}

// MARK: WKWebView

private struct WebMini: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            BackButton().padding(.horizontal, 20).padding(.top, 8)
            WebView(html: Self.html)
        }
        .toolbar(.hidden, for: .navigationBar)
    }

    /// The mini screen as a local page (no network): ids are the elements' `id`s.
    static let html: String = {
        let rows = (1...30).map { "<li id=\"tk_row_\($0)\">Toolkit row \($0)</li>" }.joined(separator: "\n")
        return """
        <!doctype html>
        <html><head><meta charset="utf-8">
        <meta name="viewport" content="width=device-width, initial-scale=1">
        <style>
          body { font: -apple-system-body; margin: 0 20px; }
          h1 { font-size: 28px; margin: 8px 0; }
          .row { display: flex; align-items: center; gap: 12px; margin: 8px 0; }
          button { font-size: 17px; padding: 6px 14px; }
          input[type=text] { font-size: 17px; padding: 6px; width: 100%; box-sizing: border-box; }
          ul { list-style: none; padding: 0; }
          li { padding: 12px 0; border-bottom: 1px solid #ddd; }
        </style></head>
        <body>
          <h1 id="tk_title">WKWebView</h1>
          <div class="row"><button id="tk_tap">Tap</button><span id="tk_count">Taps: 0</span></div>
          <div class="row">
            <input type="checkbox" role="switch" id="tk_switch" aria-label="Switch">
            <span id="tk_switch_status">Off</span>
          </div>
          <input type="text" id="tk_field" placeholder="Type here" autocorrect="off" autocapitalize="off">
          <p id="tk_echo">Echo: </p>
          <ul id="tk_rows">
        \(rows)
          </ul>
          <script>
            let taps = 0;
            const $ = (id) => document.getElementById(id);
            $('tk_tap').addEventListener('click', () => { $('tk_count').textContent = 'Taps: ' + (++taps); });
            $('tk_switch').addEventListener('change', (e) => {
              $('tk_switch_status').textContent = e.target.checked ? 'On' : 'Off';
            });
            $('tk_field').addEventListener('input', (e) => { $('tk_echo').textContent = 'Echo: ' + e.target.value; });
          </script>
        </body></html>
        """
    }()
}

private struct WebView: UIViewRepresentable {
    let html: String

    func makeUIView(context: Context) -> WKWebView {
        let web = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        web.isInspectable = true
        web.loadHTMLString(html, baseURL: nil)
        return web
    }

    func updateUIView(_ web: WKWebView, context: Context) {}
}

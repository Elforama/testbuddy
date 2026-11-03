// A small app the agent's measurements run against (not shipped with tb): each button makes
// one kind of UI change, so docs/research/ios-events.md can say which changes the XCUITest
// runner hears about (accessibility notifications) and which it does not.
import SwiftUI
import UIKit
import UserNotifications

// iOS 27 requires the scene lifecycle: a SwiftUI App hosting the UIKit screens.
@main
struct FixtureApp: App {
    var body: some Scene {
        WindowGroup { FixtureRoot().ignoresSafeArea() }
    }
}

struct FixtureRoot: UIViewControllerRepresentable {
    func makeUIViewController(context: Context) -> UINavigationController {
        UINavigationController(rootViewController: FixtureViewController())
    }
    func updateUIViewController(_ vc: UINavigationController, context: Context) {}
}

final class FixtureViewController: UIViewController {
    private let status = UILabel()
    private let box = UIView(frame: CGRect(x: 20, y: 0, width: 40, height: 40))
    private let stack = UIStackView()
    private let spinner = UIActivityIndicatorView(style: .medium)
    private var ticker: Timer?
    private var count = 0

    override func viewDidLoad() {
        super.viewDidLoad()
        title = "Fixture"
        view.backgroundColor = .systemBackground
        let scroll = UIScrollView()
        scroll.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(scroll)
        stack.axis = .vertical
        stack.spacing = 6
        stack.translatesAutoresizingMaskIntoConstraints = false
        scroll.addSubview(stack)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            scroll.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            stack.topAnchor.constraint(equalTo: scroll.contentLayoutGuide.topAnchor, constant: 8),
            stack.bottomAnchor.constraint(equalTo: scroll.contentLayoutGuide.bottomAnchor, constant: -8),
            stack.leadingAnchor.constraint(equalTo: scroll.frameLayoutGuide.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: scroll.frameLayoutGuide.trailingAnchor, constant: -16),
        ])
        status.text = "status 0"
        status.accessibilityIdentifier = "status"
        stack.addArrangedSubview(status)
        let boxHolder = UIView()
        boxHolder.heightAnchor.constraint(equalToConstant: 40).isActive = true
        box.backgroundColor = .systemBlue
        box.isAccessibilityElement = true
        box.accessibilityLabel = "box"
        boxHolder.addSubview(box)
        stack.addArrangedSubview(boxHolder)
        spinner.accessibilityIdentifier = "spinner"
        stack.addArrangedSubview(spinner)

        let field = UITextField()
        field.placeholder = "Type here"
        field.borderStyle = .roundedRect
        field.accessibilityIdentifier = "field"
        stack.addArrangedSubview(field)
        let toggle = UISwitch()
        toggle.accessibilityIdentifier = "toggle"
        stack.addArrangedSubview(toggle)

        button("Change text") { [unowned self] in self.bump() }
        button("Change text later") { [unowned self] in
            DispatchQueue.main.asyncAfter(deadline: .now() + 1) { self.bump() }
        }
        button("Ticker") { [unowned self] in
            if let t = self.ticker { t.invalidate(); self.ticker = nil; return }
            self.ticker = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { _ in self.bump() }
        }
        button("Push") { [unowned self] in
            let vc = UIViewController()
            vc.view.backgroundColor = .systemBackground
            vc.title = "Pushed"
            let l = UILabel(frame: CGRect(x: 20, y: 140, width: 300, height: 40))
            l.text = "Pushed screen"
            vc.view.addSubview(l)
            self.navigationController?.pushViewController(vc, animated: true)
        }
        button("Present") { [unowned self] in
            let vc = UIViewController()
            vc.view.backgroundColor = .systemYellow
            let b = UIButton(type: .system, primaryAction: UIAction(title: "Dismiss") { [weak vc] _ in vc?.dismiss(animated: true) })
            b.frame = CGRect(x: 20, y: 60, width: 200, height: 44)
            vc.view.addSubview(b)
            self.present(vc, animated: true)
        }
        button("Alert") { [unowned self] in
            let a = UIAlertController(title: "Hello", message: "An alert", preferredStyle: .alert)
            a.addAction(UIAlertAction(title: "OK", style: .default))
            self.present(a, animated: true)
        }
        button("Animate") { [unowned self] in
            UIView.animate(withDuration: 1.0) {
                self.box.frame.origin.x = self.box.frame.origin.x > 100 ? 20 : 260
            }
        }
        button("Spinner") { [unowned self] in
            if self.spinner.isAnimating { self.spinner.stopAnimating() } else { self.spinner.startAnimating() }
        }
        button("Add row") { [unowned self] in
            let l = UILabel()
            l.text = "row \(self.stack.arrangedSubviews.count)"
            self.stack.insertArrangedSubview(l, at: 1)
        }
        button("Hide box") { [unowned self] in self.box.isHidden.toggle() }
        button("Color") { [unowned self] in self.box.backgroundColor = self.box.backgroundColor == .systemBlue ? .systemRed : .systemBlue }
        button("Ask notifications") {
            // A SpringBoard alert over the app (the first time only).
            UNUserNotificationCenter.current().requestAuthorization(options: [.alert]) { _, _ in }
        }
        button("SwiftUI") { [unowned self] in
            self.navigationController?.pushViewController(UIHostingController(rootView: SwiftUIScreen()), animated: true)
        }
        for k in 0..<12 {
            let l = UILabel()
            l.text = "filler \(k)"
            stack.addArrangedSubview(l)
        }
    }

    private func bump() {
        count += 1
        status.text = "status \(count)"
    }

    private func button(_ title: String, _ action: @escaping () -> Void) {
        let b = UIButton(type: .system, primaryAction: UIAction(title: title) { _ in action() })
        b.contentHorizontalAlignment = .leading
        stack.addArrangedSubview(b)
    }
}

struct SwiftUIScreen: View {
    @State private var count = 0
    @State private var later = "idle"
    @State private var shown = false

    var body: some View {
        List {
            Text("count \(count)").accessibilityIdentifier("count")
            Button("Increment") { count += 1 }
            Text(later).accessibilityIdentifier("later")
            Button("Later") {
                later = "waiting"
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) { later = "done \(count)" }
            }
            Button("Toggle row") { withAnimation { shown.toggle() } }
            if shown { Text("extra row") }
        }
        .navigationTitle("SwiftUI")
    }
}

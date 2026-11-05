// Scenario 15, Screen transitions: an account list that closes over a form while the app is
// busy. After a pick it is already gone for accessibility (the form is listed at once), but it
// stays drawn, perfectly still, and takes touches for 800 ms, so a tap on the form's amount
// field in that time picks "Credit card".
import SwiftUI

struct TransitionsScenario: View {
    /// How long a closed list stays drawn (and takes touches) before it is removed.
    static let closeDelay = 800

    @State private var listOpen = false
    /// Whether the list is closing: picked, out of the accessibility tree, not removed yet.
    @State private var closing = false
    @State private var removal: Task<Void, Never>?
    @State private var account = "none"
    @State private var amount = ""

    var body: some View {
        ScenarioScreen(scenario: .transitions) {
            ZStack(alignment: .topLeading) {
                form
                if listOpen {
                    AccountList(closing: closing, pick: pick)
                        // Full width under the title, down to the bottom of the screen.
                        .padding(.horizontal, -20)
                        .ignoresSafeArea(.container, edges: .bottom)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            // Opened and removed in one step: no transition.
            .transaction { $0.animation = nil }
        }
        .onDisappear { removal?.cancel() }
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 16) {
            Button("Choose account") {
                removal?.cancel()
                closing = false
                listOpen = true
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("tr_pick")
            status("To: \(account)", "tr_account")
            status(amount.isEmpty ? "Send nothing to \(account)" : "Send \(amount) to \(account)",
                   "tr_summary")
            // Below the list's first two rows (2 × 64 pt), so under "Credit card".
            TextField("Amount", text: $amount)
                .keyboardType(.numberPad)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("tr_amount")
                .padding(.top, 40)
        }
    }

    /// Picks an account and closes the list; a pick while it closes starts the 800 ms again.
    private func pick(_ name: String) {
        account = name
        closing = true
        removal?.cancel()
        removal = after(Self.closeDelay) {
            var instant = Transaction()
            instant.disablesAnimations = true
            withTransaction(instant) {
                listOpen = false
                closing = false
            }
        }
    }
}

/// The account list, in UIKit: SwiftUI's `accessibilityHidden` (and UIKit's
/// `accessibilityElementsHidden` alone) still leave the rows in what XCUITest reads, so the
/// list takes itself out of the tree by hand. Its rows are plain controls that draw no
/// highlight, so nothing on screen changes between a pick and the list's removal.
private struct AccountList: UIViewRepresentable {
    let closing: Bool
    let pick: (String) -> Void

    func makeUIView(context: Context) -> AccountListView { AccountListView() }

    func updateUIView(_ view: AccountListView, context: Context) {
        view.onPick = pick
        view.setClosing(closing)
    }
}

final class AccountListView: UIView {
    var onPick: ((String) -> Void)?

    private var rows: [AccountRow] = []
    private var separators: [UIView] = []

    override init(frame: CGRect) {
        super.init(frame: frame)
        backgroundColor = .systemBackground
        accessibilityIdentifier = "tr_list"
        let accounts = [("Checking", "tr_account_checking"), ("Savings", "tr_account_savings"),
                        ("Credit card", "tr_account_credit")]
        for (index, (name, id)) in accounts.enumerated() {
            let row = AccountRow(name: name, id: id)
            row.addAction(UIAction { [weak self] _ in self?.onPick?(name) }, for: .touchUpInside)
            addSubview(row)
            rows.append(row)
            if index < accounts.count - 1 {
                let line = UIView()
                line.backgroundColor = .separator
                addSubview(line)
                separators.append(line)
            }
        }
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    override func layoutSubviews() {
        super.layoutSubviews()
        let w = bounds.width
        // Checking and Savings 64 pt each, Credit card the rest of the height.
        rows[0].frame = CGRect(x: 0, y: 0, width: w, height: 64)
        rows[1].frame = CGRect(x: 0, y: 64, width: w, height: 64)
        rows[2].frame = CGRect(x: 0, y: 128, width: w, height: max(0, bounds.height - 128))
        let hair = 1 / max(1, traitCollection.displayScale)
        separators[0].frame = CGRect(x: 0, y: 64 - hair, width: w, height: hair)
        separators[1].frame = CGRect(x: 0, y: 128 - hair, width: w, height: hair)
    }

    /// Closing: out of the accessibility tree at once (no children, no id, rows no longer
    /// elements), but drawn as it was and still taking touches.
    func setClosing(_ closing: Bool) {
        guard closing != accessibilityElementsHidden else { return }
        accessibilityElementsHidden = closing
        accessibilityElements = closing ? [] : nil
        accessibilityIdentifier = closing ? nil : "tr_list"
        rows.forEach { $0.isAccessibilityElement = !closing }
        UIAccessibility.post(notification: .layoutChanged, argument: nil)
    }
}

/// A list row: a control with a label that looks the same pressed or not.
final class AccountRow: UIControl {
    init(name: String, id: String) {
        super.init(frame: .zero)
        let label = UILabel()
        label.text = name
        label.textColor = .label
        label.isUserInteractionEnabled = false
        label.isAccessibilityElement = false
        label.translatesAutoresizingMaskIntoConstraints = false
        addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 20),
            label.topAnchor.constraint(equalTo: topAnchor, constant: 22),
        ])
        isAccessibilityElement = true
        accessibilityLabel = name
        accessibilityTraits = .button
        accessibilityIdentifier = id
    }

    required init?(coder: NSCoder) { fatalError("not used") }
}

// Scenario 14, Keyboard: a field and a button the keyboard covers (the screen does not move
// for it), a Return chain across three fields, a multiline field, a field that counts its
// Backspace keys, and an autofocus screen.
import SwiftUI

struct KeyboardScenario: View {
    @EnvironmentObject private var router: Router
    @State private var statusText = "Nothing typed"
    @State private var first = ""
    @State private var second = ""
    @State private var third = ""
    @State private var multiline = ""
    @State private var bottom = ""
    @State private var keys = ""
    @State private var backspaces = 0
    @FocusState private var focus: Field?

    private enum Field { case first, second, third }

    var body: some View {
        ScenarioScreen(scenario: .keyboard) {
            status(statusText, "kb_status")
            TextField("First", text: $first)
                .focused($focus, equals: .first)
                .submitLabel(.next)
                .onSubmit { focus = .second }
                .accessibilityIdentifier("kb_first")
            TextField("Second", text: $second)
                .focused($focus, equals: .second)
                .submitLabel(.next)
                .onSubmit { focus = .third }
                .accessibilityIdentifier("kb_second")
            TextField("Third", text: $third)
                .focused($focus, equals: .third)
                .submitLabel(.done)
                .onSubmit {
                    focus = nil
                    statusText = "Chain done: \(first)/\(second)/\(third)"
                }
                .accessibilityIdentifier("kb_third")
            TextField("Long text", text: $multiline, axis: .vertical)
                .lineLimit(3...5)
                .accessibilityIdentifier("kb_multiline")
            status("Lines: \(lineCount(multiline))", "kb_lines")
            Button("Open autofocus") { router.path.append(.kbAutofocus) }
                .accessibilityIdentifier("kb_autofocus_open")
            KeyField(placeholder: "Key field", text: $keys) { backspaces += 1 }
                .frame(height: 34)
                .accessibilityIdentifier("kb_keys")
            status("Backspaces: \(backspaces)", "kb_backspaces")
            Spacer()
            TextField("Bottom field", text: $bottom)
                .accessibilityIdentifier("kb_bottom")
                .onChange(of: bottom) { statusText = "Bottom: \(bottom)" }
            Button("Submit") { statusText = "Submitted" }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("kb_submit")
                .padding(.bottom, 8)
        }
        .textFieldStyle(.roundedBorder)
        .autocorrectionDisabled()
        .textInputAutocapitalization(.never)
        // The keyboard covers the bottom of the screen: nothing moves out of its way.
        .ignoresSafeArea(.keyboard)
    }
}

/// A UITextField whose own `deleteBackward` counts the Backspace keys it gets (SwiftUI's
/// TextField has no key handler to see them).
private struct KeyField: UIViewRepresentable {
    let placeholder: String
    @Binding var text: String
    let onBackspace: () -> Void

    func makeUIView(context: Context) -> CountingField {
        let field = CountingField()
        field.placeholder = placeholder
        field.borderStyle = .roundedRect
        field.autocorrectionType = .no
        field.autocapitalizationType = .none
        field.setContentHuggingPriority(.defaultLow, for: .horizontal)
        field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        field.addTarget(context.coordinator, action: #selector(Coordinator.changed(_:)),
                        for: .editingChanged)
        field.delegate = context.coordinator
        return field
    }

    func updateUIView(_ field: CountingField, context: Context) {
        context.coordinator.parent = self
        field.onBackspace = onBackspace
        if field.text != text { field.text = text }
    }

    func makeCoordinator() -> Coordinator { Coordinator(parent: self) }

    final class Coordinator: NSObject, UITextFieldDelegate {
        var parent: KeyField
        init(parent: KeyField) { self.parent = parent }

        @objc func changed(_ field: UITextField) { parent.text = field.text ?? "" }

        func textFieldShouldReturn(_ field: UITextField) -> Bool {
            field.resignFirstResponder()
            return true
        }
    }
}

final class CountingField: UITextField {
    var onBackspace: (() -> Void)?

    override func deleteBackward() {
        onBackspace?()
        super.deleteBackward()  // still deletes as usual
    }
}

/// A screen whose field has focus (and the keyboard up) as soon as it opens.
struct AutofocusView: View {
    @State private var text = ""
    @FocusState private var focused: Bool

    var body: some View {
        SubScreen(title: "Autofocus", titleID: "kb_autofocus_title") {
            TextField("Focused at start", text: $text)
                .textFieldStyle(.roundedBorder)
                .focused($focused)
                .accessibilityIdentifier("kb_auto")
                .padding(.horizontal, 20)
            Spacer()
        }
        .defaultFocus($focused, true)
        .onAppear { focused = true }
    }
}

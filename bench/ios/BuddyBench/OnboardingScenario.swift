// Scenario 7, Onboarding and login: a 3-page pager (once per app launch), a sign-in form with
// validation and a slow submit, and a six-box one-time code.
import SwiftUI
import UIKit

struct OnboardingScenario: View {
    @EnvironmentObject private var model: BenchModel
    @State private var stage: Stage = .onboarding

    enum Stage: Equatable { case onboarding, signIn, otp(email: String), done(email: String) }

    var body: some View {
        ScenarioScreen(scenario: .onboarding) {
            switch stage {
            case .onboarding:
                Pager { finishOnboarding() }
            case .signIn:
                SignInForm { stage = .otp(email: $0) }
            case .otp(let email):
                OTPEntry { stage = .done(email: email) }
            case .done(let email):
                status("Signed in as \(email)", "signin_done")
                Button("Sign out") { stage = .signIn }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("signout")
            }
        }
        .onAppear {
            if model.onboardingShown, stage == .onboarding { stage = .signIn }
        }
    }

    private func finishOnboarding() {
        model.onboardingShown = true
        stage = .signIn
    }
}

private struct Pager: View {
    let finish: () -> Void
    @State private var page = 1
    private static let titles = ["Welcome", "Track things", "Stay in sync"]

    var body: some View {
        VStack(spacing: 16) {
            TabView(selection: $page) {
                ForEach(1...3, id: \.self) { n in
                    VStack(spacing: 16) {
                        Image(systemName: ["hand.wave", "checklist", "arrow.triangle.2.circlepath"][n - 1])
                            .font(.system(size: 64))
                            .accessibilityHidden(true)
                        Text(verbatim: Self.titles[n - 1])
                            .font(.title.bold())
                            .accessibilityIdentifier("page_\(n)")
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .tag(n)
                }
            }
            .tabViewStyle(.page(indexDisplayMode: .never))
            .accessibilityIdentifier("pager")
            status("Page \(page) of 3", "pager_indicator")
            HStack {
                Button("Skip", action: finish).accessibilityIdentifier("onboarding_skip")
                Spacer()
                if page < 3 {
                    Button("Next") { withAnimation { page += 1 } }
                        .buttonStyle(.borderedProminent)
                        .accessibilityIdentifier("onboarding_next")
                } else {
                    Button("Get started", action: finish)
                        .buttonStyle(.borderedProminent)
                        .accessibilityIdentifier("onboarding_done")
                }
            }
            .padding(.bottom, 24)
        }
    }
}

private struct SignInForm: View {
    let success: (String) -> Void
    @State private var email = ""
    @State private var password = ""
    @State private var remember = false
    @State private var error: String?
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            TextField("Email", text: $email)
                .keyboardType(.emailAddress)
                .textContentType(.username)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("signin_email")
            SecureField("Password", text: $password)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("signin_password")
            Checkbox(label: "Remember me", isOn: $remember)
                .accessibilityIdentifier("signin_remember")
            HStack(spacing: 12) {
                Button("Sign in", action: submit)
                    .buttonStyle(.borderedProminent)
                    .disabled(email.isEmpty || password.isEmpty || busy)
                    .accessibilityIdentifier("signin_submit")
                if busy {
                    ProgressView().accessibilityIdentifier("signin_spinner")
                }
            }
            if let error {
                Text(verbatim: error).foregroundStyle(.red).accessibilityIdentifier("signin_error")
            }
        }
    }

    private func submit() {
        guard email.contains("@") else {
            error = "Enter a valid email"
            return
        }
        error = nil
        busy = true
        let wrong = password == "wrong"
        let address = email
        _ = after(1000) {
            busy = false
            if wrong {
                error = "Wrong password"
            } else {
                // Empty the fields first, so iOS doesn't offer to save a password.
                email = ""
                password = ""
                success(address)
            }
        }
    }
}

/// Six one-digit boxes. A digit moves to the next box; backspace in an empty box moves back.
/// Focus moves synchronously (UIKit first responder), so fast typing lands in the right box.
private struct OTPEntry: View {
    let success: () -> Void
    @State private var digits = Array(repeating: "", count: 6)
    @State private var fields = OTPFields()
    @State private var wrong = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(verbatim: "Enter the code we sent you")
            HStack(spacing: 8) {
                ForEach(0..<6, id: \.self) { i in
                    OTPBox(index: i, text: $digits[i], fields: fields, onDigit: { typed(at: i) })
                        .frame(width: 44, height: 52)
                        .background(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary))
                }
            }
            if wrong {
                Text(verbatim: "Wrong code").foregroundStyle(.red).accessibilityIdentifier("otp_error")
            }
        }
        .onAppear {
            DispatchQueue.main.async { fields.focus(0) }
        }
    }

    private func typed(at index: Int) {
        if digits.allSatisfy({ !$0.isEmpty }) {
            if digits.joined() == "123456" {
                fields.focus(nil)
                success()
            } else {
                wrong = true
                digits = Array(repeating: "", count: 6)
                for field in fields.all { field?.text = "" }
                fields.focus(0)
            }
        } else if index < 5 {
            fields.focus(index + 1)
        }
    }
}

final class OTPFields {
    private var boxes: [Int: WeakField] = [:]
    private struct WeakField { weak var field: BackspaceField? }

    var all: [BackspaceField?] { (0..<6).map { boxes[$0]?.field } }
    func register(_ field: BackspaceField, at index: Int) { boxes[index] = WeakField(field: field) }

    func focus(_ index: Int?) {
        guard let index else {
            all.forEach { $0?.resignFirstResponder() }
            return
        }
        boxes[index]?.field?.becomeFirstResponder()
    }
}

/// A UITextField box: SwiftUI can't see a backspace in an empty field, UIKit can.
private struct OTPBox: UIViewRepresentable {
    let index: Int
    @Binding var text: String
    let fields: OTPFields
    let onDigit: () -> Void

    func makeUIView(context: Context) -> BackspaceField {
        let field = BackspaceField()
        field.accessibilityIdentifier = "otp_\(index + 1)"
        field.keyboardType = .numberPad
        field.textAlignment = .center
        field.font = .monospacedDigitSystemFont(ofSize: 24, weight: .semibold)
        field.delegate = context.coordinator
        field.onEmptyBackspace = { [weak coordinator = context.coordinator] in
            coordinator?.backspaceInEmpty()
        }
        fields.register(field, at: index)
        return field
    }

    func updateUIView(_ field: BackspaceField, context: Context) {
        context.coordinator.parent = self
        if field.text != text { field.text = text }
    }

    func makeCoordinator() -> Coordinator { Coordinator(parent: self) }

    final class Coordinator: NSObject, UITextFieldDelegate {
        var parent: OTPBox
        init(parent: OTPBox) { self.parent = parent }

        func textField(_ textField: UITextField, shouldChangeCharactersIn range: NSRange,
                       replacementString string: String) -> Bool {
            if string.isEmpty {
                parent.text = ""
                textField.text = ""
                return false
            }
            guard let digit = string.last(where: \.isNumber) else { return false }
            parent.text = String(digit)
            textField.text = String(digit)
            parent.onDigit()
            return false
        }

        func backspaceInEmpty() {
            guard parent.index > 0 else { return }
            parent.fields.focus(parent.index - 1)
        }
    }
}

final class BackspaceField: UITextField {
    var onEmptyBackspace: (() -> Void)?

    override func deleteBackward() {
        if text?.isEmpty ?? true { onEmptyBackspace?() }
        super.deleteBackward()
    }
}

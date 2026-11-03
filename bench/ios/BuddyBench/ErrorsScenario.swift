// Scenario 13, Errors and retries: a load that fails once, form validation, an empty state, a
// request that outlasts a runner's settle time, and a short-lived error snackbar.
import SwiftUI

struct ErrorsScenario: View {
    @State private var message: String?
    @State private var loading = false
    @State private var failedOnce = false
    @State private var showRetry = false
    @State private var empty = false
    @State private var email = ""
    @State private var age = ""
    @State private var emailError: String?
    @State private var ageError: String?
    @State private var formStatus: String?
    @State private var toast = false

    var body: some View {
        ScenarioScreen(scenario: .errors, scrolls: true) {
            Button("Load data") { load() }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("err_load")
            Button("Show empty") {
                showRetry = false
                empty = true
                message = "No results"
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("err_empty")
            Button("Slow request") {
                showRetry = false
                empty = false
                message = nil
                loading = true
                _ = after(6000) {
                    loading = false
                    message = "Request timed out"
                }
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("err_timeout")
            Button("Fail quietly") {
                withAnimation { toast = true }
                _ = after(2000) { withAnimation { toast = false } }
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("err_toast")

            if loading {
                ProgressView().accessibilityIdentifier("err_spinner")
            }
            if empty {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 48))
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
            }
            if let message {
                status(message, "err_message")
            }
            if showRetry {
                Button("Retry") { load() }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("err_retry")
            }

            Divider()
            form
        }
        .overlay(alignment: .bottom) {
            if toast {
                Text(verbatim: "Something went wrong")
                    .foregroundStyle(.white)
                    .padding(.horizontal, 16)
                    .frame(maxWidth: .infinity, minHeight: 56, alignment: .leading)
                    .background(Color(white: 0.2), in: RoundedRectangle(cornerRadius: 10))
                    .padding(.horizontal, 12)
                    .accessibilityIdentifier("err_toast_text")
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("Email", text: $email)
                .keyboardType(.emailAddress)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("err_email")
            if let emailError {
                Text(verbatim: emailError).font(.footnote).foregroundStyle(.red)
                    .accessibilityIdentifier("err_email_error")
            }
            TextField("Age", text: $age)
                .keyboardType(.numberPad)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("err_age")
            if let ageError {
                Text(verbatim: ageError).font(.footnote).foregroundStyle(.red)
                    .accessibilityIdentifier("err_age_error")
            }
            Button("Submit") {
                emailError = email.contains("@") ? nil : "Invalid email"
                ageError = (Int(age) ?? 0) >= 18 ? nil : "Must be 18 or older"
                formStatus = emailError == nil && ageError == nil ? "Form sent" : nil
            }
            .buttonStyle(.borderedProminent)
            .accessibilityIdentifier("err_form_submit")
            if let formStatus {
                status(formStatus, "err_form_status")
            }
        }
    }

    /// Fails the first time, succeeds on every later try.
    private func load() {
        showRetry = false
        empty = false
        message = nil
        loading = true
        _ = after(800) {
            loading = false
            if failedOnce {
                message = "Loaded 3 items"
            } else {
                failedOnce = true
                message = "Couldn't load. Try again."
                showRetry = true
            }
        }
    }
}

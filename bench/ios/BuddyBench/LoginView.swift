// Screen 1, Login: a logo that spins forever, two fields, and a 1000 ms "Signing in…" overlay.
import SwiftUI

struct LoginView: View {
    @EnvironmentObject private var model: BenchModel
    @State private var username = ""
    @State private var password = ""
    @State private var showError = false
    @State private var signingIn = false
    @FocusState private var focused: Field?

    private enum Field { case username, password }

    var body: some View {
        VStack(spacing: 20) {
            // Back to the scenario list (Login is the first screen of the Journey scenario).
            HStack {
                BackButton()
                Spacer()
            }
            Text(verbatim: "Buddy Bench")
                .font(.largeTitle.bold())
                .accessibilityIdentifier("title")
            LogoSpinner()
            VStack(spacing: 12) {
                TextField("Username", text: $username)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focused, equals: .username)
                    .accessibilityIdentifier("username")
                SecureField("Password", text: $password)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focused, equals: .password)
                    .accessibilityIdentifier("password")
            }
            .textFieldStyle(.roundedBorder)
            Button("Log in", action: logIn)
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("login")
            if showError {
                Text(verbatim: "Enter a username and password")
                    .foregroundStyle(.red)
                    .accessibilityIdentifier("error")
            }
            Spacer()
        }
        .padding(.horizontal, 32)
        .padding(.top, 8)
        .ignoresSafeArea(.keyboard)
        .toolbar(.hidden, for: .navigationBar)
        .overlay {
            if signingIn { SigningInOverlay() }
        }
    }

    private func logIn() {
        guard !username.isEmpty, !password.isEmpty else {
            showError = true
            return
        }
        showError = false
        focused = nil
        signingIn = true
        let name = username
        Task { @MainActor in
            try? await Task.sleep(for: Timing.signIn)
            // Empty the fields before they leave the screen: iOS offers to save a password
            // (a system "Save Password?" sheet) when a filled-in login form goes away.
            username = ""
            password = ""
            model.user = name
        }
    }
}

/// Covers the screen (and blocks taps) while signing in.
private struct SigningInOverlay: View {
    var body: some View {
        ZStack {
            Color.black.opacity(0.35).ignoresSafeArea()
            VStack(spacing: 12) {
                ProgressView().controlSize(.large)
                Text(verbatim: "Signing in\u{2026}")
                    .accessibilityIdentifier("loading_text")
            }
            .padding(28)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
        }
        // Its own element (so the id stays on the overlay), with its children inside it.
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("loading")
    }
}

/// A ring that rotates forever (a repeatForever animation, never stopped).
private struct LogoSpinner: View {
    @State private var spinning = false

    var body: some View {
        Circle()
            .trim(from: 0.08, to: 0.82)
            .stroke(Color.accentColor, style: StrokeStyle(lineWidth: 6, lineCap: .round))
            .frame(width: 56, height: 56)
            .rotationEffect(.degrees(spinning ? 360 : 0))
            .animation(.linear(duration: 1).repeatForever(autoreverses: false), value: spinning)
            .onAppear { spinning = true }
            .accessibilityElement()
            .accessibilityIdentifier("logo_spinner")
    }
}

// Screen 4, Live: a stress screen where something always moves (a stopwatch redrawn every
// frame and a ball bouncing forever), plus a counter button and a switch.
import SwiftUI

struct LiveView: View {
    @State private var opened = Date()
    @State private var taps = 0
    @State private var notify = false

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            BackButton()
            Text(verbatim: "Live")
                .font(.largeTitle.bold())
                .accessibilityIdentifier("live_title")
            TimelineView(.animation) { context in
                Text(verbatim: Self.elapsed(context.date.timeIntervalSince(opened)))
                    .font(.system(.title, design: .monospaced))
                    .accessibilityIdentifier("stopwatch")
            }
            Ball()
            Button("Tap me") { taps += 1 }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("tap_me")
            Text(verbatim: "Tapped \(taps)")
                .accessibilityIdentifier("tap_count")
            HStack(spacing: 12) {
                Toggle("", isOn: $notify)
                    .labelsHidden()
                    .accessibilityIdentifier("notify_switch")
                Text(verbatim: notify ? "Notifications on" : "Notifications off")
                    .accessibilityIdentifier("notify_label")
            }
            Spacer()
        }
        .padding(.horizontal, 20)
        .padding(.top, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .toolbar(.hidden, for: .navigationBar)
    }

    /// "mm:ss.SSS".
    static func elapsed(_ seconds: TimeInterval) -> String {
        let ms = Int((max(0, seconds) * 1000).rounded(.down))
        return String(format: "%02d:%02d.%03d", ms / 60_000, (ms / 1000) % 60, ms % 1000)
    }
}

/// A small circle moving back and forth across a 280 pt track forever.
private struct Ball: View {
    private static let size: CGFloat = 24
    private static let track: CGFloat = 280
    @State private var right = false

    var body: some View {
        Circle()
            .fill(Color.orange)
            .frame(width: Self.size, height: Self.size)
            .offset(x: right ? Self.track - Self.size : 0)
            .animation(.easeInOut(duration: 1).repeatForever(autoreverses: true), value: right)
            .accessibilityElement()
            .accessibilityIdentifier("ball")
            .frame(width: Self.track, height: Self.size, alignment: .leading)
            .onAppear { right = true }
    }
}

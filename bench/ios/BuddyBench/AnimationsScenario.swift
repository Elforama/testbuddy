// Scenario 12, Animations: things that never stop moving, things that arrive late or move
// while you aim at them, and particles that cover the screen without taking touches.
import Combine
import SwiftUI

struct AnimationsScenario: View {
    @State private var statusText = "Idle"
    @State private var sprung = false
    @State private var faded = false
    @State private var shifted = false
    @State private var shiftStarted = false
    @State private var confetti: Date?

    var body: some View {
        ScenarioScreen(scenario: .animations, scrolls: true) {
            status(statusText, "anim_status")
            ProgressView().accessibilityIdentifier("anim_spinner")
            Marquee()
            Pulse { statusText = "Pulse tapped" }

            Button("Spring in") {
                withAnimation(.spring(response: 0.6, dampingFraction: 0.45)) { sprung = true }
            }
            .accessibilityIdentifier("anim_spring_open")
            if sprung {
                Button("Sprung") { statusText = "Sprung tapped" }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("anim_sprung")
                    .transition(.offset(x: 500))
            }

            Button("Fade in") {
                withAnimation(.linear(duration: 1.5)) { faded = true }
            }
            .accessibilityIdentifier("anim_fade_open")
            // In the tree from the start, invisible until "Fade in". (At opacity 0 exactly
            // SwiftUI drops a view from the accessibility tree; 0.01 is just as invisible.)
            Button("Faded") { statusText = "Faded tapped" }
                .buttonStyle(.bordered)
                .opacity(faded ? 1 : 0.01)
                .accessibilityIdentifier("anim_faded")

            Button("Shift") {
                guard !shiftStarted else { return }
                shiftStarted = true
                _ = after(600) { withAnimation(.easeInOut(duration: 0.4)) { shifted = true } }
            }
            .accessibilityIdentifier("anim_shift_open")
            if shifted {
                Text(verbatim: "Banner")
                    .frame(maxWidth: .infinity, minHeight: 200)
                    .background(Color.orange.opacity(0.3), in: RoundedRectangle(cornerRadius: 12))
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
            Button("Target") { statusText = "Target tapped" }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("anim_target")

            AutoCarousel { statusText = "Stopped at Slide \($0)" }

            TimelineView(.animation) { context in
                let t = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 4)
                ProgressView(value: t / 4).accessibilityIdentifier("anim_progress")
            }

            Button("Celebrate") { confetti = Date() }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("anim_confetti")
            Button("Under confetti") { statusText = "Under tapped" }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("anim_under")
        }
        .overlay {
            if let start = confetti {
                Confetti(start: start) { confetti = nil }
            }
        }
    }
}

/// "Breaking news: everything is fine" scrolling right to left forever.
private struct Marquee: View {
    @State private var moving = false

    var body: some View {
        GeometryReader { geo in
            Text(verbatim: "Breaking news: everything is fine")
                .fixedSize()
                .offset(x: moving ? -320 : geo.size.width)
                .animation(.linear(duration: 6).repeatForever(autoreverses: false), value: moving)
                .accessibilityIdentifier("anim_marquee")
                .onAppear { moving = true }
        }
        .frame(height: 24)
        .clipped()
    }
}

/// A button scaling 1.0 → 1.15 → 1.0 every 800 ms, forever.
private struct Pulse: View {
    let tap: () -> Void
    @State private var big = false

    var body: some View {
        Button("Pulse", action: tap)
            .buttonStyle(.borderedProminent)
            .scaleEffect(big ? 1.15 : 1.0)
            .animation(.easeInOut(duration: 0.4).repeatForever(autoreverses: true), value: big)
            .accessibilityIdentifier("anim_pulse")
            .onAppear { big = true }
    }
}

/// Five slides that advance every 1500 ms until "Stop".
private struct AutoCarousel: View {
    let stopped: (Int) -> Void
    @State private var slide = 1
    @State private var running = true
    private let timer = Timer.publish(every: 1.5, on: .main, in: .common).autoconnect()

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            TabView(selection: $slide) {
                ForEach(1...5, id: \.self) { n in
                    Text(verbatim: "Slide \(n)")
                        .font(.title2)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(Color(hue: Double(n) / 6, saturation: 0.3, brightness: 1),
                                    in: RoundedRectangle(cornerRadius: 12))
                        .accessibilityIdentifier("slide_\(n)")
                        .tag(n)
                }
            }
            .tabViewStyle(.page(indexDisplayMode: .always))
            .frame(height: 140)
            .accessibilityIdentifier("anim_carousel")
            Button("Stop") {
                running = false
                stopped(slide)
            }
            .accessibilityIdentifier("anim_carousel_tap")
        }
        .onReceive(timer) { _ in
            guard running else { return }
            withAnimation { slide = slide % 5 + 1 }
        }
    }
}

/// 60 particles falling over the whole screen for 2500 ms. Not touchable, not accessible.
private struct Confetti: View {
    let start: Date
    let done: () -> Void

    var body: some View {
        GeometryReader { geo in
            TimelineView(.animation) { context in
                let t = context.date.timeIntervalSince(start) / 2.5
                Canvas { ctx, size in
                    for i in 0..<60 {
                        // Fixed, deterministic spread: no randomness.
                        let x = (Double(i * 37 % 100) / 100) * size.width
                        let delay = Double(i % 10) / 40
                        let y = (t - delay) * (size.height + 40) - 20
                        let rect = CGRect(x: x, y: y, width: 8, height: 12)
                        ctx.fill(Path(rect), with: .color(Color(hue: Double(i % 7) / 7,
                                                               saturation: 0.8, brightness: 0.95)))
                    }
                }
                .frame(width: geo.size.width, height: geo.size.height)
            }
        }
        .ignoresSafeArea()
        .allowsHitTesting(false)
        .accessibilityHidden(true)
        .task {
            try? await Task.sleep(for: .milliseconds(2500))
            done()
        }
    }
}

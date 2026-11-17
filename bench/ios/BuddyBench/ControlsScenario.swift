// Scenario 5, Controls: every standard control, each with a status line showing its value.
import SwiftUI
import UIKit

struct ControlsScenario: View {
    @State private var tab = "One"
    @State private var size = "M"
    @State private var color: String?
    @State private var milk = false
    @State private var eggs = false
    @State private var wifi = false
    @State private var volume = 50.0
    @State private var quantity = 1
    @State private var fruit = "Apple"
    @State private var date = Date()
    @State private var datePicked = false
    @State private var chips: Set<String> = []
    @State private var rating = 0
    @State private var name = ""
    @State private var age = ""
    @State private var secret = ""
    @State private var notes = ""
    @State private var search = ""
    @State private var progress = 0
    @State private var progressTask: Task<Void, Never>?
    @State private var expanded = false
    @State private var hidden = false
    @State private var longPress = "Untouched"
    @State private var held = false
    @State private var doubleTap = "Untouched"
    @State private var taps = 0
    @State private var tapDecision: Task<Void, Never>?
    @State private var lastReactionPress: TimeInterval?
    @State private var reactions = 0
    @State private var placeholderDigits = ""
    @State private var plan = "Basic"
    @State private var planPresses = 0
    @State private var planChangedAt = 0 // the press that last changed the plan (0: none)
    @State private var stallShortPresses = 0
    @State private var stallLongPresses = 0

    private static let fruits = ["Apple", "Banana", "Cherry"]
    private static let chipNames = ["Fast", "Cheap", "Good"]
    private static let searchable = ["Apple", "Apricot", "Banana", "Blueberry", "Cherry"]

    var body: some View {
        ScenarioScreen(scenario: .controls, scrolls: true) {
            tabs
            segmented
            radios
            checkboxes
            LabeledSwitch(label: "Wi-Fi", isOn: $wifi, id: "switch_wifi")
            status(wifi ? "Wi-Fi on" : "Wi-Fi off", "switch_status")
            Slider(value: $volume, in: 0...100, step: 1).accessibilityIdentifier("slider_volume")
            status("Volume: \(Int(volume))", "slider_status")
            stepper
            dropdown
            datePicker
            chipRow
            stars
            fields
            searchField
            progressBar
            expandable
            Button("Can't tap me") {}
                .buttonStyle(.bordered)
                .disabled(true)
                .accessibilityIdentifier("disabled_button")
            status("Untouched", "disabled_status")
            longPressButton
            doubleTapBox
            reactionBox
            TextField("1234", text: $placeholderDigits)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("placeholder_digits")
            status("Placeholder digits: \(placeholderDigits.count)", "placeholder_status")
            drawnOptions
            stallingPresses
        }
    }

    // MARK: Presses whose down stalls the app (issue #337)

    /// Two press handlers whose touch-down blocks the main thread on purpose, as a busy app's
    /// can: delivering the down takes 150 ms (the short one, a 75 ms press, like the reaction
    /// box) or 2.2 s (the long one, a 1 s press). A lift sent before the block ends reaches the
    /// app right after the down, so the press it sees is cut short. bench/checks/ios-press-stall.sh
    /// checks that tb says so.
    private var stallingPresses: some View {
        VStack(alignment: .leading, spacing: 8) {
            StallingPress(title: "Stall 150 ms", id: "stall_short_box", stall: 0.15, minimum: 0.075) { stallShortPresses += 1 }
                .frame(maxWidth: .infinity, minHeight: 64)
            status("Short stall presses: \(stallShortPresses)", "stall_short_status")
            StallingPress(title: "Stall 2.2 s", id: "stall_long_box", stall: 2.2, minimum: 1.0) { stallLongPresses += 1 }
                .frame(maxWidth: .infinity, minHeight: 64)
            status("Long stall presses: \(stallLongPresses)", "stall_long_status")
        }
    }

    // MARK: Custom-drawn options (issue #324)

    /// Two options drawn by plain UIViews: a press shows nothing (no highlight, no dimming), so
    /// pressing the one already selected changes nothing on screen. Presses are counted, and
    /// the count shows only when the plan changes: "Plan: Pro (press 2)" after one press on
    /// Basic and one on Pro.
    private var drawnOptions: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                ForEach(["Basic", "Pro"], id: \.self) { name in
                    DrawnOption(title: name, selected: plan == name) {
                        planPresses += 1
                        if plan != name {
                            plan = name
                            planChangedAt = planPresses
                        }
                    }
                    .frame(width: 120, height: 44)
                }
            }
            status(planChangedAt == 0 ? "Plan: \(plan)" : "Plan: \(plan) (press \(planChangedAt))", "plan_status")
        }
    }

    // MARK: Choices

    private var tabs: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 0) {
                ForEach(["One", "Two", "Three"], id: \.self) { name in
                    Button { tab = name } label: {
                        VStack(spacing: 6) {
                            Text(verbatim: name).fontWeight(tab == name ? .semibold : .regular)
                            Rectangle()
                                .fill(tab == name ? Color.accentColor : .clear)
                                .frame(height: 2)
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .accessibilityAddTraits(tab == name ? [.isSelected] : [])
                    .accessibilityIdentifier("tab_\(name.lowercased())")
                }
            }
            status("Tab: \(tab)", "tabs_status")
        }
    }

    private var segmented: some View {
        VStack(alignment: .leading, spacing: 8) {
            Picker("Size", selection: $size) {
                ForEach(["S", "M", "L"], id: \.self) { s in
                    Text(verbatim: s).tag(s).accessibilityIdentifier("seg_\(s.lowercased())")
                }
            }
            .pickerStyle(.segmented)
            status("Size: \(size)", "seg_status")
        }
    }

    private var radios: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(["Red", "Green", "Blue"], id: \.self) { name in
                Button { color = name } label: {
                    HStack {
                        Image(systemName: color == name ? "largecircle.fill.circle" : "circle")
                            .accessibilityHidden(true)
                        Text(verbatim: name)
                    }
                }
                .accessibilityAddTraits(color == name ? [.isSelected] : [])
                .accessibilityIdentifier("radio_\(name.lowercased())")
            }
            status("Color: \(color ?? "none")", "radio_status")
        }
    }

    private var checkboxes: some View {
        VStack(alignment: .leading, spacing: 8) {
            Checkbox(label: "Milk", isOn: $milk).accessibilityIdentifier("check_milk")
            Checkbox(label: "Eggs", isOn: $eggs).accessibilityIdentifier("check_eggs")
            let basket = [milk ? "Milk" : nil, eggs ? "Eggs" : nil].compactMap { $0 }
            status("Basket: \(basket.isEmpty ? "empty" : basket.joined(separator: ", "))",
                   "check_status")
        }
    }

    private var stepper: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                Button("\u{2212}") { quantity = max(0, quantity - 1) }
                    .buttonStyle(.bordered)
                    .disabled(quantity == 0)
                    .accessibilityIdentifier("stepper_minus")
                Text(verbatim: "\(quantity)").monospacedDigit()
                Button("+") { quantity = min(10, quantity + 1) }
                    .buttonStyle(.bordered)
                    .disabled(quantity == 10)
                    .accessibilityIdentifier("stepper_plus")
            }
            status("Quantity: \(quantity)", "stepper_status")
        }
    }

    private var dropdown: some View {
        VStack(alignment: .leading, spacing: 8) {
            Picker("Fruit", selection: $fruit) {
                ForEach(Self.fruits, id: \.self) { Text(verbatim: $0).tag($0) }
            }
            .pickerStyle(.menu)
            .accessibilityIdentifier("dropdown_fruit")
            status("Fruit: \(fruit)", "dropdown_status")
        }
    }

    private var datePicker: some View {
        VStack(alignment: .leading, spacing: 8) {
            // The compact picker's button shows a date; its label sits beside it.
            HStack {
                Text(verbatim: "Pick a date")
                Spacer()
                DatePicker("Pick a date", selection: $date, displayedComponents: .date)
                    .datePickerStyle(.compact)
                    .labelsHidden()
                    .accessibilityIdentifier("date_open")
                    .onChange(of: date) { datePicked = true }
            }
            status(datePicked ? "Date: \(Self.iso(date))" : "Date: none", "date_status")
        }
    }

    private static func iso(_ date: Date) -> String {
        let c = Calendar.current.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", c.year!, c.month!, c.day!)
    }

    private var chipRow: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                ForEach(Self.chipNames, id: \.self) { name in
                    let on = chips.contains(name)
                    Button {
                        if on { chips.remove(name) } else { chips.insert(name) }
                    } label: {
                        Text(verbatim: name)
                            .padding(.horizontal, 14).padding(.vertical, 6)
                            .background(on ? Color.accentColor.opacity(0.2) : Color.clear,
                                        in: Capsule())
                            .overlay(Capsule().stroke(on ? Color.accentColor : .secondary))
                    }
                    .accessibilityAddTraits(on ? [.isSelected] : [])
                    .accessibilityIdentifier("chip_\(name.lowercased())")
                }
            }
            let picked = Self.chipNames.filter { chips.contains($0) }
            status("Filters: \(picked.isEmpty ? "none" : picked.joined(separator: ", "))",
                   "chip_status")
        }
    }

    private var stars: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 4) {
                ForEach(1...5, id: \.self) { n in
                    Button { rating = n } label: {
                        Image(systemName: n <= rating ? "star.fill" : "star")
                            .font(.title2)
                            .frame(width: 44, height: 44)
                    }
                    .accessibilityLabel("\(n) star\(n == 1 ? "" : "s")")
                    .accessibilityIdentifier("star_\(n)")
                }
            }
            status("Rating: \(rating)", "rating_status")
        }
    }

    // MARK: Text

    private var fields: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("Your name", text: $name)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                .accessibilityIdentifier("field_name")
            status("Hello, \(name.isEmpty ? "nobody" : name)", "field_status")
            TextField("Age", text: $age)
                .textFieldStyle(.roundedBorder)
                .keyboardType(.numberPad)
                .accessibilityIdentifier("field_age")
            status("Age: \(age.isEmpty ? "none" : age)", "age_status")
            SecureField("Secret", text: $secret)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("field_secret")
            status("Secret length: \(secret.count)", "secret_status")
            TextField("Notes", text: $notes, axis: .vertical)
                .lineLimit(3, reservesSpace: true)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("field_notes")
            status("Lines: \(lineCount(notes))", "notes_status")
        }
    }

    private var searchField: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                TextField("Search fruit", text: $search)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .submitLabel(.search)
                    .accessibilityIdentifier("field_search")
            }
            .padding(8)
            .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 10))
            VStack(alignment: .leading, spacing: 6) {
                ForEach(Self.searchable.filter {
                    search.isEmpty || $0.lowercased().hasPrefix(search.lowercased())
                }, id: \.self) { fruit in
                    Text(verbatim: fruit).accessibilityIdentifier("search_\(fruit.lowercased())")
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("search_results")
        }
    }

    // MARK: Progress, disclosure, gestures

    private var progressBar: some View {
        VStack(alignment: .leading, spacing: 8) {
            ProgressView(value: Double(progress), total: 100)
                .accessibilityIdentifier("progress_bar")
            HStack {
                Button("Start") {
                    progressTask?.cancel()
                    progress = 0
                    progressTask = Task { @MainActor in
                        for _ in 0..<10 {
                            guard (try? await Task.sleep(for: .milliseconds(200))) != nil else { return }
                            progress += 10
                        }
                    }
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("progress_go")
                status("Progress: \(progress)%", "progress_status")
            }
        }
    }

    private var expandable: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                withAnimation { expanded.toggle() }
            } label: {
                HStack {
                    Text(verbatim: "More options")
                    Image(systemName: "chevron.right")
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                        .accessibilityHidden(true)
                }
            }
            .accessibilityIdentifier("expand_toggle")
            if expanded {
                VStack(alignment: .leading, spacing: 8) {
                    Text(verbatim: "Hidden option").accessibilityIdentifier("expand_content")
                    LabeledSwitch(label: "Hidden switch", isOn: $hidden, id: "switch_hidden")
                    status(hidden ? "Hidden on" : "Hidden off", "hidden_status")
                }
                .padding(.leading, 16)
            }
        }
    }

    private var longPressButton: some View {
        VStack(alignment: .leading, spacing: 8) {
            // The button's action runs on release; a hold of 500 ms or more fires first.
            Button("Hold me") {
                if held { held = false } else { longPress = "Just tapped" }
            }
            .buttonStyle(.bordered)
            .simultaneousGesture(LongPressGesture(minimumDuration: 0.5).onEnded { _ in
                held = true
                longPress = "Held"
            })
            .accessibilityIdentifier("longpress_button")
            status(longPress, "longpress_status")
        }
    }

    private var doubleTapBox: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(verbatim: "Double-tap me")
                .frame(maxWidth: .infinity, minHeight: 80)
                .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12))
                .contentShape(Rectangle())
                // Decided 300 ms after the last tap: two or more taps are a double tap.
                .onTapGesture {
                    taps += 1
                    tapDecision?.cancel()
                    tapDecision = after(300) {
                        doubleTap = taps >= 2 ? "Double-tapped" : "Single tap"
                        taps = 0
                    }
                }
                .accessibilityAddTraits(.isButton)
                .accessibilityIdentifier("doubletap_box")
            status(doubleTap, "doubletap_status")
        }
    }

    private var reactionBox: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("React to message")
                .frame(maxWidth: .infinity, minHeight: 64)
                .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12))
                .contentShape(Rectangle())
                // Model a scrollable message row whose touch handler recognizes a press
                // only after brief contact. Two recognized presses make one reaction.
                .onLongPressGesture(minimumDuration: 0.075) {
                    let now = ProcessInfo.processInfo.systemUptime
                    if let previous = lastReactionPress, now - previous < 0.350 {
                        reactions += 1
                        lastReactionPress = nil
                    } else {
                        lastReactionPress = now
                    }
                }
                .accessibilityAddTraits(.isButton)
                .accessibilityIdentifier("reaction_box")
            status("Reactions: \(reactions)", "reaction_status")
        }
    }
}

/// A checkbox (iOS has no native one): a box and a label, one button element.
struct Checkbox: View {
    let label: String
    @Binding var isOn: Bool

    var body: some View {
        Button { isOn.toggle() } label: {
            HStack {
                Image(systemName: isOn ? "checkmark.square.fill" : "square")
                    .accessibilityHidden(true)
                Text(verbatim: label)
            }
        }
        .accessibilityAddTraits(isOn ? [.isSelected] : [])
    }
}

/// A label and a switch: the id sits on the switch itself, so tapping the element flips it
/// (a full-width Toggle's centre is empty space between its label and its switch).
struct LabeledSwitch: View {
    let label: String
    @Binding var isOn: Bool
    let id: String

    var body: some View {
        HStack {
            Text(verbatim: label).accessibilityHidden(true)
            Spacer()
            Toggle(label, isOn: $isOn)
                .labelsHidden()
                .accessibilityIdentifier(id)
        }
    }
}

/// A press handler whose touch-down blocks the main thread for `stall` seconds before its long
/// press (of `minimum` seconds) sees it (issue #337). The block is in the recognizer, which gets
/// the down at once (a scroll view delays only its view's touches): the down's delivery takes
/// that long, as the app's did on CI.
struct StallingPress: UIViewRepresentable {
    let title: String
    let id: String
    let stall: TimeInterval
    let minimum: TimeInterval
    let onPress: () -> Void

    final class Press: UILongPressGestureRecognizer {
        var stall: TimeInterval = 0
        override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent) {
            Thread.sleep(forTimeInterval: stall) // a deliberate stall of the main thread
            super.touchesBegan(touches, with: event)
        }
    }

    final class View: UIView {
        var onPress: () -> Void = {}
        @objc func pressed(_ g: UILongPressGestureRecognizer) { if g.state == .began { onPress() } }
    }

    func makeUIView(context: Context) -> View {
        let v = View()
        v.layer.cornerRadius = 12
        v.backgroundColor = .secondarySystemBackground
        let label = UILabel()
        label.text = title
        label.textAlignment = .center
        label.translatesAutoresizingMaskIntoConstraints = false
        v.addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            label.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            label.centerYAnchor.constraint(equalTo: v.centerYAnchor),
        ])
        v.isAccessibilityElement = true
        v.accessibilityLabel = title
        v.accessibilityTraits = .button
        v.accessibilityIdentifier = id
        let press = Press(target: v, action: #selector(View.pressed(_:)))
        press.stall = stall
        press.minimumPressDuration = minimum
        v.addGestureRecognizer(press)
        return v
    }

    func updateUIView(_ v: View, context: Context) { v.onPress = onPress }
}

/// An option drawn by a plain UIView, as React Native, Flutter and custom-drawn controls are: a
/// button to accessibility, whose press draws nothing (issue #324). Selected, it has a border;
/// that is all that changes, and only when the selection does.
struct DrawnOption: UIViewRepresentable {
    let title: String
    let selected: Bool
    let onTap: () -> Void

    final class View: UIView {
        var onTap: () -> Void = {}
        var selected: Bool?
        @objc func tapped() { onTap() }
    }

    func makeUIView(context: Context) -> View {
        let v = View()
        v.layer.cornerRadius = 8
        v.backgroundColor = .secondarySystemBackground
        let label = UILabel()
        label.text = title
        label.textAlignment = .center
        label.translatesAutoresizingMaskIntoConstraints = false
        v.addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            label.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            label.centerYAnchor.constraint(equalTo: v.centerYAnchor),
        ])
        v.isAccessibilityElement = true
        v.accessibilityLabel = title
        v.accessibilityIdentifier = "plan_\(title.lowercased())"
        v.addGestureRecognizer(UITapGestureRecognizer(target: v, action: #selector(View.tapped)))
        return v
    }

    func updateUIView(_ v: View, context: Context) {
        v.onTap = onTap
        guard v.selected != selected else { return } // nothing drawn unless the selection changed
        v.selected = selected
        v.accessibilityTraits = selected ? [.button, .selected] : .button
        v.layer.borderWidth = selected ? 2 : 0
        v.layer.borderColor = UIColor.systemBlue.cgColor
    }
}

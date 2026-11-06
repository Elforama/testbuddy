import SwiftUI

@main
struct EverydayNativeApp: App {
    var body: some Scene {
        WindowGroup { EverydayRoot().tint(Color(red: 0.14, green: 0.42, blue: 0.38)) }
    }
}

private struct Item: Identifiable {
    let id: Int
    let title: String
    var done = false
}

private struct DemoCheckbox: View {
    let title: String
    @Binding var checked: Bool

    var body: some View {
        Button { checked.toggle() } label: {
            Label(title, systemImage: checked ? "checkmark.square.fill" : "square")
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.vertical, 8)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityRepresentation {
            Toggle(title, isOn: $checked).toggleStyle(.switch)
        }
    }
}

private struct EverydayRoot: View {
    @State private var signedIn = false
    @State private var email = ""
    @State private var password = ""
    @State private var loading = false
    @State private var tab = 0
    @State private var menu = false
    @State private var path: [String] = []
    @State private var items = [
        Item(id: 1, title: "Buy groceries"), Item(id: 2, title: "Water the plants"),
        Item(id: 3, title: "Read a chapter")
    ]
    @State private var nextID = 4
    @State private var adding = false
    @State private var itemTitle = ""
    @State private var switches = [true, false, false]
    @State private var topics = [true, false, false]
    @State private var frequency = "Daily"
    @State private var note = ""
    @State private var name = "Alex"
    @State private var city = ""
    @State private var terms = false
    @State private var settingsStatus = ""
    @State private var profileStatus = ""

    var body: some View {
        if signedIn {
            NavigationStack(path: $path) {
                VStack(spacing: 0) {
                    Group {
                        switch tab {
                        case 0: listPage
                        case 1: settingsPage
                        default: profilePage
                        }
                    }.frame(maxWidth: .infinity, maxHeight: .infinity)
                    Divider()
                    HStack {
                        nav("My list", image: "checklist", tag: "nav_list", index: 0)
                        nav("Settings", image: "slider.horizontal.3", tag: "nav_settings", index: 1)
                        nav("Profile", image: "person", tag: "nav_profile", index: 2)
                    }
                    .padding(.vertical, 10)
                    .background(.bar)
                }
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .principal) {
                        Text(["My list", "Settings", "Profile"][tab])
                            .font(.headline).accessibilityIdentifier("home_title")
                    }
                    ToolbarItem(placement: .topBarLeading) {
                        Button { menu = true } label: { Image(systemName: "line.3.horizontal") }
                            .accessibilityLabel("Open menu").accessibilityIdentifier("open_menu")
                    }
                }
                .sheet(isPresented: $menu) { menuPage }
                .navigationDestination(for: String.self) { title in
                    VStack(alignment: .leading) {
                        Text(title == "Help"
                             ? "Add items with the Add item button. Use Settings to choose your preferences."
                             : "Everyday is a local demo app with sample data.")
                        Spacer()
                    }
                    .padding(24)
                    .navigationBarBackButtonHidden()
                    .toolbar {
                        ToolbarItem(placement: .principal) {
                            Text(title).font(.headline).accessibilityIdentifier("info_title")
                        }
                        ToolbarItem(placement: .topBarLeading) {
                            Button("Back") { path.removeLast() }
                        }
                    }
                }
                .sheet(isPresented: $adding) {
                    NavigationStack {
                        VStack(alignment: .leading, spacing: 20) {
                            Text("Add item").font(.title2).accessibilityIdentifier("add_dialog_title")
                            field("Item name", text: $itemTitle, id: "new_item_text")
                            HStack {
                                Button("Cancel") { adding = false }
                                Spacer()
                                Button("OK") {
                                    let word = itemTitle.trimmingCharacters(in: .whitespacesAndNewlines)
                                    if !word.isEmpty {
                                        items.append(Item(id: nextID, title: word))
                                        nextID += 1
                                    }
                                    adding = false
                                }.buttonStyle(.borderedProminent).accessibilityIdentifier("confirm_item")
                            }
                            Spacer()
                        }.padding(24)
                    }.presentationDetents([.medium])
                }
            }
        } else {
            NavigationStack {
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        Text("Welcome back").font(.largeTitle)
                        Text("Use any email and password to try the demo.")
                        field("Email", text: $email, id: "login_email")
                            .keyboardType(.emailAddress)
                        SecureField("Password", text: $password)
                            .textFieldStyle(.roundedBorder)
                            .submitLabel(.done)
                            .accessibilityIdentifier("login_password")
                        Button(loading ? "Signing in…" : "Sign in") {
                            loading = true
                            Task { @MainActor in
                                try? await Task.sleep(for: .milliseconds(500))
                                signedIn = true
                                loading = false
                            }
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(loading)
                        .accessibilityIdentifier("login_submit")
                        if loading { ProgressView().accessibilityIdentifier("login_loading") }
                    }.padding(24)
                }
                .toolbar {
                    ToolbarItem(placement: .principal) {
                        Text("Everyday").font(.headline).accessibilityIdentifier("login_title")
                    }
                }
            }
        }
    }

    private func field(_ label: String, text: Binding<String>, id: String) -> some View {
        TextField(label, text: text)
            .textFieldStyle(.roundedBorder)
            .autocorrectionDisabled()
            .textInputAutocapitalization(.never)
            .submitLabel(.done)
            .accessibilityIdentifier(id)
    }

    private func nav(_ title: String, image: String, tag: String, index: Int) -> some View {
        Button { tab = index } label: {
            VStack(spacing: 4) {
                Image(systemName: image)
                Text(title).font(.caption)
            }.frame(maxWidth: .infinity)
        }
        .foregroundStyle(tab == index ? Color.accentColor : Color.secondary)
        .accessibilityIdentifier(tag)
        .accessibilityAddTraits(tab == index ? .isSelected : [])
    }

    private var listPage: some View {
        VStack {
            HStack {
                Text("\(items.count) items").accessibilityIdentifier("item_count")
                Spacer()
                Button("Add item") { itemTitle = ""; adding = true }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("add_item")
            }.padding(16)
            List($items) { $item in
                DemoCheckbox(title: item.title, checked: $item.done)
                    .accessibilityIdentifier("item_\(item.id)")
            }.listStyle(.plain)
        }
    }

    private var settingsPage: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Text("Preferences").font(.title2)
                ForEach(0..<3) { i in
                    HStack {
                        Text(["Notifications", "Email reminders", "Sound effects"][i])
                        Spacer()
                        Toggle(["Notifications", "Email reminders", "Sound effects"][i], isOn: $switches[i])
                            .labelsHidden().accessibilityIdentifier("toggle_\(i)")
                    }
                }
                Divider()
                Text("Topics").font(.title2)
                ForEach(0..<3) { i in
                    DemoCheckbox(title: ["Product updates", "Tips and ideas", "Community news"][i], checked: $topics[i])
                        .accessibilityIdentifier("topic_\(i)")
                }
                Divider()
                Text("Reminder frequency").font(.title2)
                ForEach(["Daily", "Weekly", "Never"], id: \.self) { value in
                    Button { frequency = value } label: {
                        Label(value, systemImage: frequency == value ? "largecircle.fill.circle" : "circle")
                    }
                    .accessibilityIdentifier("frequency_\(value.lowercased())")
                    .accessibilityAddTraits(frequency == value ? .isSelected : [])
                }
                field("Personal note", text: $note, id: "settings_note")
                Button("Save preferences") { settingsStatus = "Preferences saved" }
                    .buttonStyle(.borderedProminent).accessibilityIdentifier("save_settings")
                Text(settingsStatus).accessibilityIdentifier("settings_status")
            }.padding(20)
        }
    }

    private var profilePage: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text("Your details").font(.title2)
                Text(email.isEmpty ? "Demo account" : email)
                field("Name", text: $name, id: "profile_name")
                field("City", text: $city, id: "profile_city")
                DemoCheckbox(title: "I agree to the demo terms", checked: $terms)
                    .accessibilityIdentifier("profile_terms")
                Button("Save profile") { profileStatus = "Profile saved" }
                    .buttonStyle(.borderedProminent).accessibilityIdentifier("save_profile")
                Text(profileStatus).accessibilityIdentifier("profile_status")
            }.padding(20)
        }
    }

    private var menuPage: some View {
        NavigationStack {
            List {
                ForEach(Array(["My list", "Settings", "Profile"].enumerated()), id: \.offset) { index, title in
                    Button(title) { tab = index; menu = false }.accessibilityIdentifier("menu_\(index)")
                }
                Button("Help") { menu = false; path.append("Help") }.accessibilityIdentifier("menu_help")
                Button("About") { menu = false; path.append("About") }.accessibilityIdentifier("menu_about")
                Button("Sign out") { menu = false; reset() }.accessibilityIdentifier("menu_logout")
            }
            .navigationTitle("Everyday")
            .toolbar { ToolbarItem(placement: .topBarTrailing) { Button("Close") { menu = false } } }
        }.presentationDetents([.medium, .large])
    }

    private func reset() {
        signedIn = false; email = ""; password = ""; tab = 0; path = []
        items = [Item(id: 1, title: "Buy groceries"), Item(id: 2, title: "Water the plants"), Item(id: 3, title: "Read a chapter")]
        nextID = 4; switches = [true, false, false]; topics = [true, false, false]
        frequency = "Daily"; note = ""; name = "Alex"; city = ""; terms = false
        settingsStatus = ""; profileStatus = ""
    }
}

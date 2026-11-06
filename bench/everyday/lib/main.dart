import 'package:flutter/material.dart';

void main() => runApp(const EverydayApp());

Widget identified(String id, Widget child) =>
    Semantics(key: ValueKey(id), identifier: id, child: child);

Widget action(String id, String text, VoidCallback? onPressed) =>
    FilledButton(onPressed: onPressed, child: identified(id, Text(text)));

Widget control(String id, Widget child) =>
    MergeSemantics(child: identified(id, child));

Widget input(
  String id,
  String label,
  TextEditingController controller, {
  bool password = false,
  TextInputType? keyboard,
  TextInputAction textAction = TextInputAction.done,
}) => identified(
  id,
  TextField(
    controller: controller,
    obscureText: password,
    keyboardType: keyboard,
    textInputAction: textAction,
    onTapOutside: (_) => FocusManager.instance.primaryFocus?.unfocus(),
    onSubmitted: (_) => FocusManager.instance.primaryFocus?.unfocus(),
    decoration: InputDecoration(
      labelText: label,
      border: const OutlineInputBorder(),
    ),
  ),
);

class EverydayApp extends StatelessWidget {
  const EverydayApp({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'Everyday',
    theme: ThemeData(
      colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xff246b60)),
      useMaterial3: true,
      inputDecorationTheme: const InputDecorationTheme(
        contentPadding: EdgeInsets.symmetric(horizontal: 16, vertical: 16),
      ),
    ),
    home: const LoginScreen(),
  );
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key});

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final email = TextEditingController();
  final password = TextEditingController();
  bool loading = false;

  @override
  void dispose() {
    email.dispose();
    password.dispose();
    super.dispose();
  }

  Future<void> login() async {
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() => loading = true);
    await Future<void>.delayed(const Duration(milliseconds: 500));
    if (!mounted) return;
    Navigator.of(context).pushReplacement(
      MaterialPageRoute<void>(builder: (_) => HomeScreen(email: email.text)),
    );
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: identified('login_title', const Text('Everyday'))),
    body: SafeArea(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 440),
          child: ListView(
            shrinkWrap: true,
            padding: const EdgeInsets.all(24),
            children: [
              Text(
                'Welcome back',
                style: Theme.of(context).textTheme.headlineMedium,
              ),
              const SizedBox(height: 12),
              const Text('Use any email and password to try the demo.'),
              const SizedBox(height: 28),
              input(
                'login_email',
                'Email',
                email,
                keyboard: TextInputType.emailAddress,
              ),
              const SizedBox(height: 16),
              input('login_password', 'Password', password, password: true),
              const SizedBox(height: 24),
              action(
                'login_submit',
                loading ? 'Signing in…' : 'Sign in',
                loading ? null : login,
              ),
              if (loading) ...[
                const SizedBox(height: 16),
                identified(
                  'login_loading',
                  const Center(child: CircularProgressIndicator()),
                ),
              ],
            ],
          ),
        ),
      ),
    ),
  );
}

class ListItem {
  ListItem(this.id, this.title, {this.done = false});
  final int id;
  final String title;
  bool done;
}

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key, required this.email});
  final String email;

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  final items = [
    ListItem(1, 'Buy groceries'),
    ListItem(2, 'Water the plants'),
    ListItem(3, 'Read a chapter'),
  ];
  final name = TextEditingController(text: 'Alex');
  final note = TextEditingController();
  final city = TextEditingController();
  final selectedTopics = <String>{'Product updates'};
  final switches = <String, bool>{
    'Notifications': true,
    'Email reminders': false,
    'Sound effects': false,
  };
  int nextId = 4;
  int tab = 0;
  bool terms = false;
  String frequency = 'Daily';
  String settingsStatus = '';
  String profileStatus = '';

  @override
  void dispose() {
    name.dispose();
    note.dispose();
    city.dispose();
    super.dispose();
  }

  Future<void> addItem() async {
    final title = await showDialog<String>(
      context: context,
      builder: (_) => const AddItemDialog(),
    );
    if (!mounted || title == null || title.trim().isEmpty) return;
    setState(() => items.add(ListItem(nextId++, title.trim())));
  }

  void openPage(String title) {
    Navigator.pop(context); // close the drawer before pushing a page
    Navigator.push(
      context,
      MaterialPageRoute<void>(
        builder: (context) => Scaffold(
          appBar: AppBar(
            leading: IconButton(
              onPressed: () => Navigator.pop(context),
              icon: const Icon(Icons.arrow_back),
              tooltip: 'Back',
            ),
            title: identified('info_title', Text(title)),
          ),
          body: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              title == 'Help'
                  ? 'Add items with the Add item button. Use Settings to choose your preferences.'
                  : 'Everyday is a local demo app with sample data.',
            ),
          ),
        ),
      ),
    );
  }

  void signOut() {
    Navigator.of(context).pushAndRemoveUntil(
      MaterialPageRoute<void>(builder: (_) => const LoginScreen()),
      (_) => false,
    );
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: identified(
        'home_title',
        Text(['My list', 'Settings', 'Profile'][tab]),
      ),
      leading: Builder(
        builder: (context) => identified(
          'open_menu',
          IconButton(
            tooltip: 'Open menu',
            icon: const Icon(Icons.menu),
            onPressed: () => Scaffold.of(context).openDrawer(),
          ),
        ),
      ),
    ),
    drawer: Drawer(
      child: SafeArea(
        child: ListView(
          children: [
            const Padding(
              padding: EdgeInsets.all(20),
              child: Text('Everyday', style: TextStyle(fontSize: 24)),
            ),
            for (final entry in ['My list', 'Settings', 'Profile'].indexed)
              identified(
                'menu_${entry.$1}',
                ListTile(
                  title: Text(entry.$2),
                  onTap: () {
                    Navigator.pop(context);
                    setState(() => tab = entry.$1);
                  },
                ),
              ),
            const Divider(),
            identified(
              'menu_help',
              ListTile(
                title: const Text('Help'),
                onTap: () => openPage('Help'),
              ),
            ),
            identified(
              'menu_about',
              ListTile(
                title: const Text('About'),
                onTap: () => openPage('About'),
              ),
            ),
            identified(
              'menu_logout',
              ListTile(title: const Text('Sign out'), onTap: signOut),
            ),
          ],
        ),
      ),
    ),
    body: SafeArea(child: [listPage(), settingsPage(), profilePage()][tab]),
    bottomNavigationBar: NavigationBar(
      selectedIndex: tab,
      onDestinationSelected: (value) {
        FocusManager.instance.primaryFocus?.unfocus();
        setState(() {
          tab = value;
        });
      },
      destinations: [
        NavigationDestination(
          icon: identified('nav_list', const Icon(Icons.checklist)),
          label: 'My list',
        ),
        NavigationDestination(
          icon: identified('nav_settings', const Icon(Icons.tune)),
          label: 'Settings',
        ),
        NavigationDestination(
          icon: identified('nav_profile', const Icon(Icons.person_outline)),
          label: 'Profile',
        ),
      ],
    ),
  );

  Widget listPage() => Column(
    children: [
      Padding(
        padding: const EdgeInsets.all(16),
        child: Row(
          children: [
            Expanded(
              child: identified('item_count', Text('${items.length} items')),
            ),
            action('add_item', 'Add item', addItem),
          ],
        ),
      ),
      Expanded(
        child: ListView(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          children: [
            for (final item in items)
              control(
                'item_${item.id}',
                CheckboxListTile(
                  value: item.done,
                  title: Text(item.title),
                  onChanged: (value) => setState(() => item.done = value!),
                ),
              ),
          ],
        ),
      ),
    ],
  );

  Widget settingsPage() => ListView(
    padding: const EdgeInsets.all(16),
    children: [
      Text('Preferences', style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 12),
      for (final entry in switches.entries)
        control(
          'toggle_${switches.keys.toList().indexOf(entry.key)}',
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: Text(entry.key),
            value: entry.value,
            onChanged: (value) => setState(() => switches[entry.key] = value),
          ),
        ),
      const Divider(height: 32),
      Text('Topics', style: Theme.of(context).textTheme.titleLarge),
      for (final topic in [
        'Product updates',
        'Tips and ideas',
        'Community news',
      ].indexed)
        control(
          'topic_${topic.$1}',
          CheckboxListTile(
            contentPadding: EdgeInsets.zero,
            title: Text(topic.$2),
            value: selectedTopics.contains(topic.$2),
            onChanged: (value) => setState(() {
              if (value!) {
                selectedTopics.add(topic.$2);
              } else {
                selectedTopics.remove(topic.$2);
              }
            }),
          ),
        ),
      const Divider(height: 32),
      Text('Reminder frequency', style: Theme.of(context).textTheme.titleLarge),
      RadioGroup<String>(
        groupValue: frequency,
        onChanged: (value) => setState(() => frequency = value!),
        child: Column(
          children: [
            for (final value in ['Daily', 'Weekly', 'Never'])
              control(
                'frequency_${value.toLowerCase()}',
                RadioListTile<String>(
                  contentPadding: EdgeInsets.zero,
                  title: Text(value),
                  value: value,
                ),
              ),
          ],
        ),
      ),
      const SizedBox(height: 16),
      input('settings_note', 'Personal note', note),
      const SizedBox(height: 16),
      action('save_settings', 'Save preferences', () {
        FocusManager.instance.primaryFocus?.unfocus();
        setState(() => settingsStatus = 'Preferences saved');
      }),
      identified('settings_status', Text(settingsStatus)),
    ],
  );

  Widget profilePage() => ListView(
    padding: const EdgeInsets.all(20),
    children: [
      Text('Your details', style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 12),
      Text(widget.email.isEmpty ? 'Demo account' : widget.email),
      const SizedBox(height: 24),
      input('profile_name', 'Name', name),
      const SizedBox(height: 16),
      input('profile_city', 'City', city),
      const SizedBox(height: 16),
      control(
        'profile_terms',
        CheckboxListTile(
          title: const Text('I agree to the demo terms'),
          contentPadding: EdgeInsets.zero,
          value: terms,
          onChanged: (value) => setState(() => terms = value!),
        ),
      ),
      const SizedBox(height: 16),
      action('save_profile', 'Save profile', () {
        FocusManager.instance.primaryFocus?.unfocus();
        setState(() => profileStatus = 'Profile saved');
      }),
      identified('profile_status', Text(profileStatus)),
    ],
  );
}

class AddItemDialog extends StatefulWidget {
  const AddItemDialog({super.key});

  @override
  State<AddItemDialog> createState() => _AddItemDialogState();
}

class _AddItemDialogState extends State<AddItemDialog> {
  final text = TextEditingController();
  @override
  void dispose() {
    text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: identified('add_dialog_title', const Text('Add item')),
    content: input('new_item_text', 'Item name', text),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('Cancel'),
      ),
      action('confirm_item', 'OK', () => Navigator.pop(context, text.text)),
    ],
  );
}

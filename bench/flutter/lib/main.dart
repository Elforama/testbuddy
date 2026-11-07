import 'dart:async';
import 'dart:io';
import 'dart:math' as math;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter/semantics.dart';

part 'forms.dart';
part 'motion.dart';
part 'collections.dart';
part 'navigation.dart';
part 'platform.dart';

const scenarios = {
  'journey': 'Journey',
  'slow': 'Slow and janky',
  'permissions': 'Permissions',
  'overlays': 'Overlays',
  'controls': 'Controls',
  'navigation': 'Navigation and back stack',
  'onboarding': 'Onboarding and login',
  'lists': 'Lists',
  'a11y_poor': 'Poor accessibility',
  'a11y_good': 'Good accessibility',
  'native': 'Native toolkits',
  'animations': 'Animations',
  'errors': 'Errors and retries',
  'keyboard': 'Keyboard',
  'transitions': 'Screen transitions',
};
late final SemanticsHandle accessibility;
void main() {
  WidgetsFlutterBinding.ensureInitialized();
  accessibility = SemanticsBinding.instance.ensureSemantics();
  runApp(
    MaterialApp(
      title: 'Buddy Bench',
      restorationScopeId: 'bench',
      home: const Menu(),
      onGenerateRoute: (settings) {
        final uri = Uri.tryParse(settings.name ?? '');
        if (uri != null &&
            (uri.host == 'level' || uri.path.startsWith('/level/'))) {
          return MaterialPageRoute(
            builder: (_) => NavigationScreen(
              depth: int.tryParse(uri.pathSegments.last) ?? 0,
            ),
          );
        }
        return null;
      },
    ),
  );
}

Widget mark(String id, Widget child) => Semantics(
  key: id.isEmpty ? null : ValueKey(id),
  identifier: id,
  child: child,
);
Widget label(String id, String value) => mark(id, Text(value));
Widget button(String id, String title, VoidCallback? action, {String? value}) =>
    Semantics(
      key: id.isEmpty ? null : ValueKey(id),
      identifier: id,
      label: title,
      value: value,
      button: true,
      enabled: action != null,
      onTap: action,
      excludeSemantics: true,
      child: OutlinedButton(onPressed: action, child: Text(title)),
    );
Widget field(
  String id,
  String hint,
  ValueChanged<String> change, {
  bool obscure = false,
  TextInputType? type,
  int lines = 1,
  FocusNode? focus,
  TextInputAction? action,
  ValueChanged<String>? submit,
  bool autofocus = false,
  TextEditingController? controller,
}) => mark(
  id,
  TextField(
    decoration: InputDecoration(labelText: hint),
    controller: controller,
    focusNode: focus,
    onChanged: change,
    obscureText: obscure,
    keyboardType: type,
    maxLines: lines,
    textInputAction: action,
    onSubmitted: submit,
    autofocus: autofocus,
  ),
);
Widget frame(
  BuildContext c,
  String title,
  Widget body, {
  VoidCallback? back,
  String titleId = 'scenario_title',
}) => Scaffold(
  appBar: AppBar(
    leadingWidth: 72,
    leading: button('back', 'Back', back ?? () => Navigator.pop(c)),
    title: label(titleId, title),
  ),
  body: SafeArea(child: body),
);
Widget page(List<Widget> children) => SingleChildScrollView(
  padding: const EdgeInsets.all(12),
  child: Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: children,
  ),
);
Future<void> push(BuildContext c, Widget screen) =>
    Navigator.push(c, MaterialPageRoute(builder: (_) => screen));

class Menu extends StatelessWidget {
  const Menu({super.key});
  @override
  Widget build(BuildContext c) => Scaffold(
    appBar: AppBar(title: label('scenarios_title', 'Scenarios')),
    body: ListView(
      children: [
        for (final e in scenarios.entries)
          SizedBox(
            height: 60,
            child: button('scenario_${e.key}', e.value, () {
              if (e.key == 'navigation') {
                Navigator.restorablePush(c, navRoute);
                return;
              }
              push(c, switch (e.key) {
                'journey' => const Journey(),
                'lists' => const CollectionsScreen(),
                'controls' ||
                'errors' ||
                'keyboard' ||
                'onboarding' ||
                'a11y_good' ||
                'a11y_poor' => Forms(e.key),
                'permissions' || 'native' => PlatformScreen(e.key),
                _ => Motion(e.key),
              });
            }),
          ),
      ],
    ),
  );
}

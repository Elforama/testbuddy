part of 'main.dart';

class PlatformScreen extends StatefulWidget {
  const PlatformScreen(this.kind, {super.key});
  final String kind;
  @override
  State<PlatformScreen> createState() => _PlatformState();
}

class _PlatformState extends State<PlatformScreen> {
  static const channel = MethodChannel('buddy/host');
  static const permissions = [
    'Camera',
    'Location',
    'Contacts',
    'Notifications',
    'Microphone',
  ];
  final states = <String, String>{};
  Future<void> refreshAllFiles() async {
    final allowed = await channel.invokeMethod<bool>('allFilesStatus') ?? false;
    if (mounted) {
      setState(() => states['allFiles'] = allowed ? 'Allowed' : 'Not allowed');
    }
  }

  Future<void> request(String key, {bool refresh = false}) async {
    final state = await channel.invokeMethod<String>(
      refresh ? 'status' : 'permission',
      key,
    );
    if (mounted) setState(() => states[key] = state ?? 'not asked');
  }

  @override
  Widget build(BuildContext c) => frame(
    c,
    scenarios[widget.kind]!,
    page(
      widget.kind == 'native'
          ? [
              for (final e
                  in (Platform.isIOS
                          ? {
                              'swiftui': 'SwiftUI',
                              'uikit': 'UIKit',
                              'webview': 'WKWebView',
                              'browser': 'Embedded browser',
                              'rotation': 'Rotation readout',
                            }
                          : {
                              'views': 'Android Views',
                              'compose': 'Jetpack Compose',
                              'webview': 'WebView',
                            })
                      .entries)
                button(
                  'tk_${e.key}',
                  e.value,
                  () => channel.invokeMethod<void>('toolkit', e.key),
                ),
            ]
          : [
              if (Platform.isAndroid) ...[
                button(
                  'perm_all_files',
                  'Check all-files access',
                  refreshAllFiles,
                ),
                if (states.containsKey('allFiles'))
                  label(
                    'perm_all_files_status',
                    "All files: ${states['allFiles']}",
                  ),
              ],
              for (final p in permissions) ...[
                Text('$p: ${states[p] ?? 'not asked'}'),
                button(
                  'perm_${p.toLowerCase()}',
                  'Request $p',
                  () => request(p),
                ),
              ],
              button('perm_post', 'Post a notification', () async {
                final value = await channel.invokeMethod<String>('post');
                if (mounted) {
                  setState(() => states['post'] = value ?? 'Not allowed');
                }
              }),
              label('perm_post_status', states['post'] ?? ''),
              button(
                'perm_rationale',
                'Rationale',
                () => showDialog(
                  context: c,
                  builder: (c) => AlertDialog(
                    title: const Text('We need your microphone'),
                    actions: [
                      button('rationale_no', 'Not now', () => Navigator.pop(c)),
                      button('rationale_yes', 'Continue', () {
                        Navigator.pop(c);
                        request('Microphone');
                      }),
                    ],
                  ),
                ),
              ),
              button('perm_refresh', 'Refresh', () async {
                for (final p in permissions) {
                  await request(p, refresh: true);
                }
              }),
            ],
    ),
  );
}

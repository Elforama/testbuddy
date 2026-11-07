part of 'main.dart';

@pragma('vm:entry-point')
Route<void> navRoute(BuildContext c, Object? depth) => MaterialPageRoute(
  builder: (_) => NavigationScreen(depth: depth as int? ?? 0),
);

class NavigationScreen extends StatefulWidget {
  const NavigationScreen({super.key, this.depth = 0});
  final int depth;
  @override
  State<NavigationScreen> createState() => _NavigationState();
}

class _NavigationState extends State<NavigationScreen> with RestorationMixin {
  late final RestorableInt depth = RestorableInt(widget.depth);
  @override
  String get restorationId => 'navigation';
  @override
  void restoreState(RestorationBucket? oldBucket, bool initialRestore) =>
      registerForRestoration(depth, 'depth');
  @override
  void dispose() {
    depth.dispose();
    super.dispose();
  }

  void pop() {
    if (depth.value > 0) {
      setState(() => depth.value--);
    } else {
      Navigator.pop(context);
    }
  }

  @override
  Widget build(BuildContext c) => PopScope(
    canPop: depth.value == 0,
    onPopInvokedWithResult: (didPop, _) {
      if (!didPop) pop();
    },
    child: frame(
      c,
      depth.value == 0 ? scenarios['navigation']! : 'Level ${depth.value}',
      page([
        label('nav_depth', 'Depth ${depth.value}'),
        button(
          'nav_push',
          'Push',
          depth.value == 5 ? null : () => setState(() => depth.value++),
        ),
        if (depth.value > 0) button('nav_pop', 'Pop', pop),
        button('nav_home', 'Home', () => setState(() => depth.value = 0)),
        button('nav_tabs', 'Tabs', () {
          bool feed = true, post = false;
          push(
            c,
            StatefulBuilder(
              builder: (c, update) => frame(
                c,
                'Tabs',
                page([
                  Row(
                    children: [
                      for (final name in ['Feed', 'Profile'])
                        button(
                          'navtab_${name.toLowerCase()}',
                          name,
                          () => update(() => feed = name == 'Feed'),
                        ),
                    ],
                  ),
                  if (feed && post)
                    label('post_title', 'Post')
                  else if (feed)
                    button(
                      'open_post',
                      'Open post',
                      () => update(() => post = true),
                    ),
                  button('tabs_close', 'Close', () => Navigator.pop(c)),
                ]),
              ),
            ),
          );
        }),
        button(
          'nav_dialog',
          'Dialog',
          () => showDialog(
            context: c,
            builder: (c) => AlertDialog(
              title: label('leave_dialog', 'Leave?'),
              actions: [
                button(
                  'leave_stay',
                  'Stay',
                  () => Navigator.pop(c),
                  separateSemantics: true,
                ),
              ],
            ),
          ),
        ),
        button('nav_form', 'Form', () {
          String draft = '';
          push(
            c,
            Builder(
              builder: (c) => frame(
                c,
                'Form',
                page([field('form_field', 'Draft', (v) => draft = v)]),
                back: () async {
                  final discard =
                      draft.isEmpty ||
                      await showDialog<bool>(
                            context: c,
                            builder: (c) => AlertDialog(
                              title: label(
                                'discard_dialog',
                                'Discard changes?',
                              ),
                              actions: [
                                button(
                                  'discard_keep',
                                  'Keep editing',
                                  () => Navigator.pop(c, false),
                                  separateSemantics: true,
                                ),
                                button(
                                  'discard_yes',
                                  'Discard',
                                  () => Navigator.pop(c, true),
                                  separateSemantics: true,
                                ),
                              ],
                            ),
                          ) ==
                          true;
                  if (discard && c.mounted) Navigator.pop(c);
                },
              ),
            ),
          );
        }),
      ]),
      back: pop,
    ),
  );
}

class Journey extends StatefulWidget {
  const Journey({super.key});
  @override
  State<Journey> createState() => _JourneyState();
}

class _JourneyState extends State<Journey> {
  String user = '', password = '', error = '';
  bool home = false, busy = false;
  int cart = 0, items = 40, tick = 0;
  Timer? timer;
  @override
  void dispose() {
    timer?.cancel();
    super.dispose();
  }

  Future<void> login() async {
    if (user.isEmpty || password.isEmpty) {
      setState(() => error = 'Enter a username and password');
      return;
    }
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() => busy = true);
    await Future<void>.delayed(const Duration(seconds: 1));
    if (!mounted) return;
    setState(() {
      busy = false;
      home = true;
    });
    timer = Timer.periodic(
      const Duration(milliseconds: 100),
      (_) => setState(() => tick++),
    );
  }

  Future<void> detail(int n) async {
    bool loaded = false;
    Timer? delay;
    await push(
      context,
      StatefulBuilder(
        builder: (c, update) {
          delay ??= Timer(const Duration(milliseconds: 600), () {
            if (c.mounted) update(() => loaded = true);
          });
          return frame(
            c,
            'Item $n details',
            page([
              label('detail_text', 'This is item $n.'),
              if (loaded)
                label('reviews', '4.8 stars')
              else
                const CircularProgressIndicator(),
              button('add_to_cart', 'Add to cart', () {
                setState(() => cart++);
                update(() {});
              }),
              label('detail_cart', 'In cart: $cart'),
            ]),
          );
        },
      ),
    );
    delay?.cancel();
  }

  @override
  Widget build(BuildContext c) {
    if (!home) {
      return frame(
        c,
        'Buddy Bench',
        page([
          mark(
            'logo_spinner',
            const Center(child: CircularProgressIndicator()),
          ),
          field('username', 'Username', (v) => user = v),
          field('password', 'Password', (v) => password = v, obscure: true),
          button('login', 'Log in', busy ? null : login),
          if (busy) ...[
            mark('loading', const Center(child: CircularProgressIndicator())),
            label('loading_text', 'Signing in…'),
          ],
          if (error.isNotEmpty) label('error', error),
        ]),
      );
    }
    return frame(
      c,
      'Welcome, $user',
      Column(
        children: [
          label('cart_count', 'Cart: $cart'),
          Row(
            children: [
              mark('home_spinner', const CircularProgressIndicator()),
              label('sync_label', 'Syncing…'),
            ],
          ),
          label('ticker', 'Live: $tick'),
          Row(
            children: [
              button('logout', 'Logout', () {
                timer?.cancel();
                setState(() {
                  home = false;
                  cart = 0;
                  items = 40;
                  password = '';
                  user = '';
                  error = '';
                });
              }),
              button('open_live', 'Live', () => push(c, const LiveScreen())),
            ],
          ),
          Expanded(
            child: ListView.builder(
              itemExtent: 64,
              itemCount: items + 1,
              itemBuilder: (_, i) => i == items
                  ? busy
                        ? mark(
                            'load_more_spinner',
                            const Center(child: CircularProgressIndicator()),
                          )
                        : button(
                            'load_more',
                            'Load more',
                            busy
                                ? null
                                : () async {
                                    setState(() => busy = true);
                                    await Future<void>.delayed(
                                      const Duration(milliseconds: 800),
                                    );
                                    if (mounted) {
                                      setState(() {
                                        items += 10;
                                        busy = false;
                                      });
                                    }
                                  },
                          )
                  : button(
                      'item_${i + 1}',
                      'Item ${i + 1}',
                      () => detail(i + 1),
                    ),
            ),
          ),
        ],
      ),
    );
  }
}

class LiveScreen extends StatefulWidget {
  const LiveScreen({super.key});
  @override
  State<LiveScreen> createState() => _LiveState();
}

class _LiveState extends State<LiveScreen> with SingleTickerProviderStateMixin {
  late final controller = AnimationController(
    vsync: this,
    duration: const Duration(seconds: 2),
  )..repeat(reverse: true);
  final watch = Stopwatch()..start();
  int taps = 0;
  bool on = false;
  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext c) => frame(
    c,
    'Live',
    page([
      AnimatedBuilder(
        animation: controller,
        builder: (c, w) => Column(
          children: [
            label(
              'stopwatch',
              '${watch.elapsed.toString().split('.').first.substring(2)}.${(watch.elapsedMilliseconds % 1000).toString().padLeft(3, '0')}',
            ),
            Align(
              alignment: Alignment(controller.value * 2 - 1, 0),
              child: const Icon(Icons.circle),
            ),
          ],
        ),
      ),
      button('tap_me', 'Tap me', () => setState(() => taps++)),
      label('tap_count', 'Tapped $taps'),
      mark(
        'notify_switch',
        SwitchListTile(
          title: Text('Notifications ${on ? 'on' : 'off'}'),
          value: on,
          onChanged: (v) => setState(() => on = v),
        ),
      ),
    ]),
  );
}

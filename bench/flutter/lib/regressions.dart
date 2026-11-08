part of 'main.dart';

class RegressionMenu extends StatelessWidget {
  const RegressionMenu({super.key});

  @override
  Widget build(BuildContext context) => frame(
    context,
    scenarios['regressions']!,
    page([
      button(
        'open-profile',
        'Profile',
        () => push(context, const _PinProfile()),
      ),
      button(
        'open-disclosure',
        'Disclosure',
        () => push(context, const _MovingDisclosure()),
      ),
      button(
        'open-shift',
        'Timed layout shift',
        () => push(context, const _TimedShift()),
      ),
      button(
        'open-inputs',
        'Editor keys',
        () => push(context, const _EditorKeys()),
      ),
      button(
        'open-rendered-motion',
        'Rendered motion',
        () => push(context, const _RenderedMotion()),
      ),
      button(
        'open-location-readout',
        'Location readout',
        () => push(context, const _LocationReadout()),
      ),
    ]),
  );
}

class _LocationReadout extends StatefulWidget {
  const _LocationReadout();

  @override
  State<_LocationReadout> createState() => _LocationReadoutState();
}

class _LocationReadoutState extends State<_LocationReadout> {
  final coordinates = const EventChannel(
    'buddy/location',
  ).receiveBroadcastStream();

  @override
  Widget build(BuildContext context) => frame(
    context,
    'Location readout',
    StreamBuilder<dynamic>(
      stream: coordinates,
      builder: (context, snapshot) {
        if (snapshot.hasError) return const Text('Location unavailable');
        final coordinates = snapshot.data as List<dynamic>?;
        if (coordinates == null) return const Text('Waiting for location');
        final latitude = (coordinates[0] as num).toStringAsFixed(6);
        final longitude = (coordinates[1] as num).toStringAsFixed(6);
        return page([
          Text('Latitude: $latitude'),
          Text('Longitude: $longitude'),
        ]);
      },
    ),
  );
}

class _RenderedMotion extends StatelessWidget {
  const _RenderedMotion();

  @override
  Widget build(BuildContext context) => frame(
    context,
    'Rendered motion',
    page([
      const _MotionPanel(),
      button('open-moving-route', 'Open moving route', () {
        Navigator.of(context).push(
          PageRouteBuilder<void>(
            transitionDuration: const Duration(milliseconds: 900),
            pageBuilder: (context, _, _) {
              final scale = MediaQuery.devicePixelRatioOf(context);
              return frame(
                context,
                'Moving route',
                ColoredBox(
                  color: const Color(0xFF008000),
                  child: Center(
                    child: Semantics(
                      identifier: 'physical-pixel-crop',
                      child: SizedBox(width: 400 / scale, height: 320 / scale),
                    ),
                  ),
                ),
              );
            },
            transitionsBuilder: (_, animation, _, child) => SlideTransition(
              position: Tween(
                begin: const Offset(1, 0),
                end: Offset.zero,
              ).animate(animation),
              child: child,
            ),
          ),
        );
      }),
    ]),
  );
}

class _MotionPanel extends StatefulWidget {
  const _MotionPanel();

  @override
  State<_MotionPanel> createState() => _MotionPanelState();
}

class _MotionPanelState extends State<_MotionPanel>
    with SingleTickerProviderStateMixin {
  late final motion = AnimationController(
    vsync: this,
    duration: const Duration(seconds: 4),
    animationBehavior: AnimationBehavior.preserve,
  );

  @override
  void dispose() {
    motion.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Column(
    children: [
      button('start-rendered-motion', 'Start rendered motion', () {
        motion.forward(from: 0);
      }),
      AnimatedBuilder(
        animation: motion,
        builder: (_, _) => SizedBox(
          height: 200,
          width: double.infinity,
          child: ColoredBox(
            color: Color.lerp(Colors.deepPurple, Colors.yellow, motion.value)!,
          ),
        ),
      ),
    ],
  );
}

class _PinProfile extends StatefulWidget {
  const _PinProfile();

  @override
  State<_PinProfile> createState() => _PinProfileState();
}

class _PinProfileState extends State<_PinProfile> {
  bool confirmed = false;

  @override
  Widget build(BuildContext context) => frame(
    context,
    'Profile',
    page([
      button('open-pin', 'Set a PIN', () async {
        final saved = await Navigator.push<bool>(
          context,
          MaterialPageRoute(builder: (_) => const _PinKeypad()),
        );
        if (mounted && saved == true) setState(() => confirmed = true);
      }),
      if (confirmed) const Text('PIN set'),
      for (var i = 1; i <= 16; i++) ListTile(title: Text('Setting $i')),
    ]),
    titleId: 'profile-title',
  );
}

class _PinKeypad extends StatefulWidget {
  const _PinKeypad();

  @override
  State<_PinKeypad> createState() => _PinKeypadState();
}

class _PinKeypadState extends State<_PinKeypad> {
  String? first;
  String digits = '';
  bool mismatch = false;
  Timer? verify;

  void press(String digit) {
    if (digits.length == 4) return;
    setState(() {
      mismatch = false;
      digits += digit;
    });
    if (digits.length != 4) return;
    verify = Timer(const Duration(milliseconds: 250), () {
      if (first == digits) {
        Navigator.pop(context, true);
      } else {
        setState(() {
          mismatch = first != null;
          first = mismatch ? null : digits;
          digits = '';
        });
      }
    });
  }

  @override
  void dispose() {
    verify?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => frame(
    context,
    first == null ? 'Create a PIN' : 'Confirm your PIN',
    page([
      Text('${digits.length} of 4 digits'),
      if (mismatch) const Text('PINs do not match'),
      for (var row = 0; row < 3; row++)
        Row(
          children: [
            for (var col = 1; col <= 3; col++)
              Expanded(
                child: button(
                  'pin-${row * 3 + col}',
                  '${row * 3 + col}',
                  () => press('${row * 3 + col}'),
                ),
              ),
          ],
        ),
    ]),
  );
}

class _MovingDisclosure extends StatefulWidget {
  const _MovingDisclosure();

  @override
  State<_MovingDisclosure> createState() => _MovingDisclosureState();
}

class _MovingDisclosureState extends State<_MovingDisclosure> {
  bool open = false;

  @override
  Widget build(BuildContext context) => frame(
    context,
    'Disclosure',
    page([
      if (open)
        SizedBox(
          height: 140,
          child: label('disclosure-content', 'Extra details'),
        ),
      button(
        'disclosure-toggle',
        'Show more',
        () => setState(() => open = !open),
      ),
    ]),
  );
}

class _TimedShift extends StatefulWidget {
  const _TimedShift();

  @override
  State<_TimedShift> createState() => _TimedShiftState();
}

class _TimedShiftState extends State<_TimedShift> {
  late final Timer reveal;
  bool shifted = false;
  int taps = 0;

  @override
  void initState() {
    super.initState();
    reveal = Timer(
      const Duration(seconds: 1),
      () => setState(() => shifted = true),
    );
  }

  @override
  void dispose() {
    reveal.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => frame(
    context,
    'Timed layout shift',
    page([
      if (shifted)
        const SizedBox(height: 140, child: Text('The banner arrived')),
      button('shift-target', 'Tap', () => setState(() => taps++)),
      label('shift-count', 'Taps: $taps'),
    ]),
  );
}

class _EditorKeys extends StatefulWidget {
  const _EditorKeys();

  @override
  State<_EditorKeys> createState() => _EditorKeysState();
}

class _EditorKeysState extends State<_EditorKeys> {
  // This cache file survives an app restart and is removed with app data.
  final draftFile = File('${Directory.systemTemp.path}/bench-editor-draft.txt');
  late final TextEditingController draft;
  String editor = '', submitted = 'nothing';
  int backspaces = 0;

  @override
  void initState() {
    super.initState();
    draft = TextEditingController(
      text: draftFile.existsSync() ? draftFile.readAsStringSync() : '',
    );
  }

  @override
  void dispose() {
    draft.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => frame(
    context,
    'Editor keys',
    page([
      Text(
        'Keyboard: ${MediaQuery.viewInsetsOf(context).bottom > 0 ? 'shown' : 'hidden'}',
      ),
      field('draft', 'Draft', (text) {
        draftFile.writeAsStringSync(text);
        setState(() {});
      }, controller: draft),
      label(
        'saved-draft',
        'Saved draft: ${draft.text.isEmpty ? 'empty' : draft.text}',
      ),
      field(
        'editor',
        'Enter and Backspace',
        (text) {
          setState(() {
            // Count deletions delivered to Flutter's editing callback, including IME edits.
            backspaces += math.max(0, editor.length - text.length);
            editor = text;
          });
        },
        action: TextInputAction.done,
        submit: (text) => setState(() => submitted = text),
      ),
      label('editor-value', 'Editor: $editor'),
      label('submitted', 'Submitted: $submitted'),
      label('backspaces', 'Backspaces: $backspaces'),
    ]),
  );
}

part of 'main.dart';

class Motion extends StatefulWidget {
  const Motion(this.kind, {super.key});
  final String kind;
  @override
  State<Motion> createState() => _MotionState();
}

class _MotionState extends State<Motion> with TickerProviderStateMixin {
  late final AnimationController loop;
  late final AnimationController particles;
  Timer? closeTimer;
  final timers = <Timer>[];
  bool spring = false,
      fade = false,
      banner = false,
      confetti = false,
      stopped = false,
      veil = false,
      tip = false,
      picker = false,
      closing = false,
      missReferenceTap = true;
  int slide = 1, rows = -1, lateTaps = 0, janks = 0, under = 0;
  String result = 'Nothing yet', anim = 'Idle', account = 'none', amount = '';
  String reference = '';
  void later(int ms, VoidCallback fn) => timers.add(
    Timer(Duration(milliseconds: ms), () {
      if (mounted) setState(fn);
    }),
  );
  @override
  void initState() {
    super.initState();
    loop = AnimationController(
      vsync: this,
      duration: const Duration(seconds: 2),
    );
    particles = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 2500),
    );
    if (widget.kind == 'animations') {
      loop.duration = const Duration(seconds: 4);
      loop.repeat();
      timers.add(
        Timer.periodic(const Duration(milliseconds: 1500), (_) {
          if (!stopped) setState(() => slide = slide % 5 + 1);
        }),
      );
    }
    if (widget.kind == 'slow') {
      for (int i = 0; i <= 3; i++) {
        later(2000 + 400 * i, () => rows = i);
      }
      later(3000, () => banner = true);
    }
  }

  @override
  void dispose() {
    for (final t in timers) {
      t.cancel();
    }
    closeTimer?.cancel();
    particles.dispose();
    loop.dispose();
    super.dispose();
  }

  void report(String s) => setState(() => anim = s);
  @override
  Widget build(BuildContext c) =>
      frame(c, scenarios[widget.kind]!, switch (widget.kind) {
        'animations' => Stack(
          children: [
            animations(),
            if (confetti)
              Positioned.fill(
                child: IgnorePointer(
                  child: CustomPaint(painter: ConfettiPainter(particles)),
                ),
              ),
          ],
        ),
        'slow' => slow(),
        'overlays' => overlays(),
        _ => transitions(),
      });
  Widget animations() => page([
    label('anim_status', anim),
    mark('anim_spinner', const Center(child: CircularProgressIndicator())),
    ClipRect(
      child: AnimatedBuilder(
        animation: loop,
        builder: (c, w) => Transform.translate(
          offset: Offset(240 * (1 - loop.value * 2), 0),
          child: w,
        ),
        child: label('anim_marquee', 'Breaking news: everything is fine'),
      ),
    ),
    AnimatedBuilder(
      animation: loop,
      builder: (c, w) => Transform.scale(
        scale: 1 + .075 * (1 - math.cos(loop.value * 10 * math.pi)),
        child: w,
      ),
      child: button('anim_pulse', 'Pulse', () => report('Pulse tapped')),
    ),
    button(
      'anim_spring_open',
      'Open spring',
      () => setState(() => spring = true),
    ),
    if (spring)
      TweenAnimationBuilder<double>(
        tween: Tween(begin: 0, end: 1),
        duration: const Duration(milliseconds: 900),
        curve: Curves.elasticOut,
        builder: (c, v, w) => Transform.translate(
          offset: Offset(MediaQuery.sizeOf(c).width * (1 - v), 0),
          child: w,
        ),
        child: button('anim_sprung', 'Sprung', () => report('Sprung tapped')),
      ),
    button('anim_fade_open', 'Fade in', () => setState(() => fade = true)),
    if (fade)
      TweenAnimationBuilder<double>(
        tween: Tween(begin: 0, end: 1),
        duration: const Duration(milliseconds: 1500),
        builder: (_, v, w) => Opacity(opacity: v, child: w),
        child: button('anim_faded', 'Faded', () => report('Faded tapped')),
      ),
    button(
      'anim_shift_open',
      'Shift layout',
      () => later(600, () => banner = true),
    ),
    AnimatedContainer(
      duration: const Duration(milliseconds: 400),
      height: banner ? 200 : 0,
      color: Colors.amber,
      child: banner ? const Center(child: Text('Banner')) : null,
    ),
    button('anim_target', 'Target', () => report('Target tapped')),
    SizedBox(
      height: 190,
      child: Center(child: label('slide_$slide', 'Slide $slide')),
    ),
    button('anim_carousel_tap', 'Stop', () {
      stopped = true;
      report('Stopped at Slide $slide');
    }),
    AnimatedBuilder(
      animation: loop,
      builder: (c, w) => LinearProgressIndicator(value: loop.value),
    ),
    button('anim_confetti', 'Celebrate', () {
      setState(() => confetti = true);
      particles.forward(from: 0).whenCompleteOrCancel(() {
        if (mounted && particles.isCompleted) setState(() => confetti = false);
      });
    }),
    button('anim_under', 'Under confetti', () => report('Under tapped')),
  ]);
  Widget slow() => page([
    if (rows < 0)
      for (int i = 0; i < 3; i++)
        Container(
          height: 28,
          margin: const EdgeInsets.all(4),
          color: Colors.grey,
        )
    else
      label('stage_text', 'Loaded'),
    for (int i = 1; i <= rows; i++)
      TweenAnimationBuilder<double>(
        tween: Tween(begin: 0, end: 1),
        duration: const Duration(milliseconds: 300),
        builder: (_, v, w) => Opacity(opacity: v, child: w),
        child: Text('Slow row $i'),
      ),
    if (banner)
      button('late_button', 'Late button', () => setState(() => lateTaps++)),
    Text('Late taps: $lateTaps'),
    button('jank_button', 'Block UI', () {
      final watch = Stopwatch()..start();
      while (watch.elapsedMilliseconds < 700) {}
      setState(() => janks++);
    }),
    Text('Unblocked $janks'),
    button(
      'slow_open',
      'Open slowly',
      () => later(1200, () {
        Navigator.of(context).push(
          PageRouteBuilder(
            pageBuilder: (c, a, b) => frame(c, 'Slow detail', const SizedBox()),
            transitionDuration: const Duration(milliseconds: 1500),
            transitionsBuilder: (_, a, b, w) => SlideTransition(
              position: Tween(
                begin: const Offset(1, 0),
                end: Offset.zero,
              ).animate(a),
              child: w,
            ),
          ),
        );
      }),
    ),
    button(
      'slow_list_open',
      'Slow list',
      () => push(context, const Feed(slow: true)),
    ),
  ]);
  Widget overlays() => Stack(
    children: [
      page([
        label('overlay_result', result),
        button('open_dialog', 'Open dialog', () async {
          final choice = await showDialog<String>(
            context: context,
            builder: (c) => AlertDialog(
              title: const Text('Delete item?'),
              actions: [
                for (final x in ['Cancel', 'Delete'])
                  TextButton(
                    onPressed: () => Navigator.pop(c, x),
                    child: Text(x),
                  ),
              ],
            ),
          );
          if (mounted) {
            setState(
              () => result = choice == 'Delete' ? 'Deleted' : 'Cancelled',
            );
          }
        }),
        button(
          'open_sheet',
          'Open sheet',
          () => showModalBottomSheet(
            context: context,
            builder: (c) => Wrap(
              children: [
                button('sheet_action', 'Action', () {
                  setState(() => result = 'Sheet action');
                  Navigator.pop(c);
                }),
                button('sheet_close', 'Close', () => Navigator.pop(c)),
              ],
            ),
          ),
        ),
        button('show_veil', 'Show veil', () {
          setState(() => veil = true);
          later(1500, () => veil = false);
        }),
        if (Platform.isAndroid) ...[
          FutureBuilder<bool?>(
            future: _PlatformState.channel.invokeMethod<bool>('overlayStatus'),
            builder: (c, s) => label(
              'perm_overlay_status',
              'Overlay: ${s.data == true ? 'allowed' : 'not allowed'}',
            ),
          ),
          button('system_overlay', 'Show floating bubble', () async {
            final reply = await _PlatformState.channel.invokeMethod<String>(
              'overlay',
            );
            if (mounted) setState(() => result = reply ?? '');
          }),
        ],
        button(
          'under_button',
          'Under',
          veil ? null : () => setState(() => under++),
        ),
        Text('Under taps: $under'),
        button('show_tooltip', 'Tooltip', () => setState(() => tip = true)),
        button(
          'show_snackbar',
          'Snackbar',
          () => ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: button('snackbar_undo', 'Undo', () {
                setState(() => result = 'Undone');
                ScaffoldMessenger.of(context).hideCurrentSnackBar();
              }),
            ),
          ),
        ),
        button(
          'open_cover',
          'Open cover',
          () => push(
            context,
            StatefulBuilder(
              builder: (c, update) => frame(
                c,
                'Cover',
                page([
                  button(
                    'cover_sheet',
                    'Open sheet',
                    () => showModalBottomSheet(
                      context: c,
                      builder: (sheet) => Wrap(
                        children: [
                          label('cover_sheet_title', 'Sheet'),
                          button('cover_sheet_done', 'Done', () {
                            Navigator.pop(sheet);
                            update(() => result = 'Done on cover');
                          }),
                        ],
                      ),
                    ),
                  ),
                  Text(result),
                  button('cover_close', 'Close', () => Navigator.pop(c)),
                ]),
                titleId: 'cover_title',
              ),
            ),
          ),
        ),
      ]),
      if (veil)
        const Positioned.fill(
          child: ColoredBox(
            color: Colors.black26,
            child: Center(child: CircularProgressIndicator()),
          ),
        ),
      if (tip)
        Positioned.fill(
          child: mark(
            'PopoverDismissRegion',
            GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: () => setState(() => tip = false),
              child: const Center(
                child: Card(child: Text('Tip: long press for more')),
              ),
            ),
          ),
        ),
    ],
  );
  void choose(String next) {
    closeTimer?.cancel();
    setState(() {
      account = next;
      picker = false;
      closing = true;
    });
    closeTimer = Timer(const Duration(milliseconds: 800), () {
      if (mounted) setState(() => closing = false);
    });
  }

  Widget transitions() => Stack(
    children: [
      ExcludeSemantics(
        excluding: picker,
        child: page([
          label(
            'tr_summary',
            'Send ${amount.isEmpty ? 'nothing' : amount} to $account',
          ),
          label('tr_account', 'To: $account'),
          button(
            'tr_pick',
            'Choose account',
            () => setState(() => picker = true),
          ),
          const SizedBox(height: 120),
          field(
            'tr_amount',
            'Amount',
            (v) => setState(() => amount = v),
            type: TextInputType.number,
            enabled: !closing,
          ),
          Stack(
            children: [
              field('tr_reference', 'Reference', (v) {
                setState(() => reference = v);
              }),
              if (missReferenceTap)
                Positioned.fill(
                  child: GestureDetector(
                    behavior: HitTestBehavior.opaque,
                    excludeFromSemantics: true,
                    onTap: () => setState(() => missReferenceTap = false),
                  ),
                ),
            ],
          ),
          label('tr_reference_summary', 'Reference: $reference'),
        ]),
      ),
      if (picker || closing)
        Positioned.fill(
          child: ExcludeSemantics(
            excluding: closing,
            child: ColoredBox(
              color: Colors.white,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (final a in ['Checking', 'Savings'])
                    SizedBox(
                      height: 64,
                      child: button(
                        'tr_account_${a.toLowerCase()}',
                        a,
                        () => choose(a),
                      ),
                    ),
                  Expanded(
                    child: button(
                      'tr_account_credit',
                      'Credit card',
                      () => choose('Credit card'),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
    ],
  );
}

class ConfettiPainter extends CustomPainter {
  ConfettiPainter(this.animation) : super(repaint: animation);
  final Animation<double> animation;
  @override
  void paint(Canvas canvas, Size size) {
    for (int i = 0; i < 60; i++) {
      final x =
          size.width * ((i * 73 % 101) / 101) +
          12 * math.sin(animation.value * 5 + i);
      final y = size.height * (animation.value * 1.45 - (i % 12) / 28);
      canvas.drawRect(
        Rect.fromLTWH(x, y, 5, 9),
        Paint()..color = Colors.primaries[i % Colors.primaries.length],
      );
    }
  }

  @override
  bool shouldRepaint(ConfettiPainter old) => old.animation != animation;
}

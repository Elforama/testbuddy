part of 'main.dart';

class Forms extends StatefulWidget {
  const Forms(this.kind, {super.key});
  final String kind;
  @override
  State<Forms> createState() => _FormsState();
}

class _FormsState extends State<Forms> {
  final values = <String, String>{};
  final flags = <String>{};
  int quantity = 1, rating = 0, progress = 0, backs = 0, stage = 0, index = 0;
  double volume = 50;
  double formVolume = 3;
  String result = '', status = '';
  DateTime? date;
  bool signingIn = false;
  final pager = PageController();
  final otp = List.generate(6, (_) => TextEditingController());
  final otpFocus = List.generate(6, (_) => FocusNode());
  Timer? timer;
  @override
  void dispose() {
    timer?.cancel();
    pager.dispose();
    for (final c in otp) {
      c.dispose();
    }
    for (final f in otpFocus) {
      f.dispose();
    }
    super.dispose();
  }

  String val(String key, [String fallback = '']) => values[key] ?? fallback;
  void update(String key, String value) => setState(() => values[key] = value);
  void message(String value) => setState(() => result = value);
  void toggle(String key) => setState(() {
    if (!flags.remove(key)) flags.add(key);
  });
  Widget input(
    String id,
    String hint, {
    bool secret = false,
    TextInputType? type,
  }) => field(id, hint, (v) => update(id, v), obscure: secret, type: type);
  @override
  Widget build(BuildContext c) {
    if (widget.kind.startsWith('a11y')) return accessibilityPage();
    return frame(
      c,
      scenarios[widget.kind]!,
      page(switch (widget.kind) {
        'controls' => controls(),
        'keyboard' => keyboard(),
        'errors' => errors(),
        _ => onboarding(),
      }),
    );
  }

  List<Widget> controls() {
    Widget choices(
      String key,
      List<String> items,
      String prefix,
      String caption,
    ) => Column(
      children: [
        Wrap(
          children: [
            for (final item in items)
              ChoiceChip(
                label: mark('${prefix}_${item.toLowerCase()}', Text(item)),
                selected:
                    val(
                      key,
                      key == 'size'
                          ? 'M'
                          : key == 'color'
                          ? 'none'
                          : items.first,
                    ) ==
                    item,
                onSelected: (_) => update(key, item),
              ),
          ],
        ),
        Text(
          '$caption: ${val(key, key == 'size'
              ? 'M'
              : key == 'color'
              ? 'none'
              : items.first)}',
        ),
      ],
    );
    return [
      choices('tab', ['One', 'Two', 'Three'], 'tab', 'Tab'),
      choices('size', ['S', 'M', 'L'], 'seg', 'Size'),
      choices('color', ['Red', 'Green', 'Blue'], 'radio', 'Color'),
      for (final food in ['Milk', 'Eggs'])
        mark(
          'check_${food.toLowerCase()}',
          CheckboxListTile(
            value: flags.contains(food),
            title: Text(food),
            onChanged: (_) => toggle(food),
          ),
        ),
      Text('Basket: ${['Milk', 'Eggs'].where(flags.contains).join(', ')}'),
      mark(
        'switch_wifi',
        SwitchListTile(
          value: flags.contains('wifi'),
          title: Text('Wi-Fi ${flags.contains('wifi') ? 'on' : 'off'}'),
          onChanged: (_) => toggle('wifi'),
        ),
      ),
      mark(
        'slider_volume',
        Slider(
          value: volume,
          min: 0,
          max: 100,
          onChanged: (v) => setState(() => volume = v),
        ),
      ),
      Text('Volume: ${volume.round()}'),
      Row(
        children: [
          button(
            'stepper_minus',
            '−',
            quantity > 0 ? () => setState(() => quantity--) : null,
          ),
          Text('Quantity: $quantity'),
          button(
            'stepper_plus',
            '+',
            quantity < 10 ? () => setState(() => quantity++) : null,
          ),
        ],
      ),
      mark(
        'dropdown_fruit',
        DropdownButton<String>(
          value: val('fruit', 'Apple'),
          items: [
            for (final f in ['Apple', 'Banana', 'Cherry'])
              DropdownMenuItem(value: f, child: Text(f)),
          ],
          onChanged: (v) => update('fruit', v!),
        ),
      ),
      Text('Fruit: ${val('fruit', 'Apple')}'),
      button('date_open', 'Pick a date', () async {
        final next = await showDatePicker(
          context: context,
          initialDate: date ?? DateTime(2026, 1, 1),
          firstDate: DateTime(2000),
          lastDate: DateTime(2100),
        );
        if (mounted && next != null) setState(() => date = next);
      }),
      label(
        'date_status',
        'Date: ${date?.toIso8601String().split('T').first ?? 'none'}',
      ),
      Wrap(
        children: [
          for (final f in ['Fast', 'Cheap', 'Good'])
            mark(
              'chip_${f.toLowerCase()}',
              FilterChip(
                label: Text(f),
                selected: flags.contains(f),
                onSelected: (_) => toggle(f),
              ),
            ),
        ],
      ),
      Text(
        'Filters: ${['Fast', 'Cheap', 'Good'].where(flags.contains).join(', ')}',
      ),
      Wrap(
        children: [
          for (int i = 1; i <= 5; i++)
            button('star_$i', '$i', () => setState(() => rating = i)),
        ],
      ),
      Text('Rating: $rating'),
      input('field_name', 'Name'),
      Text('Hello, ${val('field_name')}'),
      input('field_age', 'Age', type: TextInputType.number),
      Text('Age: ${val('field_age')}'),
      input('field_secret', 'Secret', secret: true),
      Text('Secret length: ${val('field_secret').length}'),
      field('field_notes', 'Notes', (v) => update('notes', v), lines: 3),
      label(
        'notes_status',
        'Lines: ${val('notes').isEmpty ? 0 : val('notes').split('\n').length}',
      ),
      input('field_search', 'Search fruit'),
      for (final fruit in ['Apple', 'Apricot', 'Banana', 'Blueberry', 'Cherry'])
        if (fruit.toLowerCase().startsWith(val('field_search').toLowerCase()))
          label('search_${fruit.toLowerCase()}', fruit),
      LinearProgressIndicator(value: progress / 100),
      Text('Progress: $progress%'),
      button('progress_go', 'Start', () {
        timer?.cancel();
        setState(() => progress = 0);
        timer = Timer.periodic(const Duration(milliseconds: 200), (t) {
          setState(() => progress += 10);
          if (progress >= 100) t.cancel();
        });
      }),
      button('expand_toggle', 'Advanced', () => toggle('expanded')),
      if (flags.contains('expanded'))
        mark(
          'switch_hidden',
          SwitchListTile(
            value: flags.contains('hidden'),
            title: Text('Hidden ${flags.contains('hidden') ? 'on' : 'off'}'),
            onChanged: (_) => toggle('hidden'),
          ),
        ),
      button('disabled_button', "Can't tap me", null),
      label('disabled_status', 'Untouched'),
      mark(
        'longpress_button',
        ElevatedButton(
          onPressed: () => message('Just tapped'),
          onLongPress: () => message('Held'),
          child: const Text('Hold'),
        ),
      ),
      mark(
        'doubletap_box',
        GestureDetector(
          onTap: () => message('Single tap'),
          onDoubleTap: () => message('Double-tapped'),
          child: const SizedBox(
            height: 52,
            child: Center(child: Text('Double tap')),
          ),
        ),
      ),
      Text(result),
    ];
  }

  List<Widget> errors() => [
    button(
      'err_load',
      'Load',
      () => delayedError("Couldn't load. Try again.", 800),
    ),
    if (result.startsWith("Couldn't"))
      button('err_retry', 'Retry', () => delayedError('Loaded 3 items', 800)),
    if (result == 'Loading…') const CircularProgressIndicator(),
    Text(result),
    input('err_email', 'Email'),
    input('err_age', 'Age', type: TextInputType.number),
    button('err_form_submit', 'Submit', () {
      FocusManager.instance.primaryFocus?.unfocus();
      toggleSubmit();
    }),
    if (flags.contains('submitted')) ...[
      if (!val('err_email').contains('@')) const Text('Invalid email'),
      if ((int.tryParse(val('err_age')) ?? 0) < 18)
        const Text('Must be 18 or older'),
      if (val('err_email').contains('@') &&
          (int.tryParse(val('err_age')) ?? 0) >= 18)
        const Text('Form sent'),
    ],
    button('err_empty', 'Show empty', () => message('No results')),
    if (Platform.isAndroid)
      button('err_freeze', 'Freeze 6 s', () {
        final watch = Stopwatch()..start();
        while (watch.elapsedMilliseconds < 6000) {}
        message('Thawed');
      }),
    button(
      'err_timeout',
      'Slow request',
      () => delayedError('Request timed out', 6000),
    ),
    button(
      'err_toast',
      'Show error',
      () => ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Something went wrong'),
          duration: Duration(seconds: 2),
        ),
      ),
    ),
  ];
  void toggleSubmit() => setState(() => flags.add('submitted'));
  void delayedError(String value, int ms) {
    message('Loading…');
    timer?.cancel();
    timer = Timer(Duration(milliseconds: ms), () => message(value));
  }

  List<Widget> keyboard() => [
    for (final id in ['first', 'second', 'third'])
      field(
        'kb_$id',
        id,
        (v) => update(id, v),
        action: id == 'third' ? TextInputAction.done : TextInputAction.next,
        submit: id == 'third'
            ? (_) => message(
                'Chain done: ${val('first')}/${val('second')}/${val('third')}',
              )
            : null,
      ),
    Text(result),
    field('kb_multiline', 'Notes', (v) => update('lines', v), lines: 3),
    Text('Lines: ${val('lines').split('\n').length}'),
    input('kb_bottom', 'Bottom'),
    Text('Bottom: ${val('kb_bottom')}'),
    button('kb_submit', 'Submit', () {
      FocusManager.instance.primaryFocus?.unfocus();
      message('Submitted');
    }),
    field('kb_keys', 'Keys', (v) {
      backs += math.max(0, val('keys').length - v.length);
      update('keys', v);
    }),
    Text('Backspaces: $backs'),
    button(
      'kb_autofocus_open',
      'Open autofocus',
      () => push(
        context,
        Builder(
          builder: (c) => frame(
            c,
            'Autofocus',
            page([field('kb_auto', 'Auto', (_) {}, autofocus: true)]),
          ),
        ),
      ),
    ),
  ];
  List<Widget> onboarding() {
    if (stage == 0) {
      return [
        label('pager_indicator', 'Page ${index + 1} of 3'),
        SizedBox(
          height: 290,
          child: mark(
            'pager',
            PageView(
              controller: pager,
              onPageChanged: (n) => setState(() => index = n),
              children: [
                for (final t in ['Welcome', 'Track things', 'Stay in sync'])
                  Center(child: Text(t)),
              ],
            ),
          ),
        ),
        button('onboarding_skip', 'Skip', () => setState(() => stage = 1)),
        if (index < 2)
          button(
            'onboarding_next',
            'Next',
            () => pager.nextPage(
              duration: const Duration(milliseconds: 250),
              curve: Curves.easeOut,
            ),
          )
        else
          button(
            'onboarding_done',
            'Get started',
            () => setState(() => stage = 1),
          ),
      ];
    }
    if (stage == 1) {
      return [
        input('signin_email', 'Email'),
        input('signin_password', 'Password', secret: true),
        mark(
          'signin_remember',
          CheckboxListTile(
            title: const Text('Remember me'),
            value: flags.contains('remember'),
            onChanged: (_) => toggle('remember'),
          ),
        ),
        button(
          'signin_submit',
          'Sign in',
          val('signin_email').isEmpty ||
                  val('signin_password').isEmpty ||
                  signingIn
              ? null
              : () async {
                  if (!val('signin_email').contains('@')) {
                    message('Enter a valid email');
                    return;
                  }
                  setState(() => signingIn = true);
                  await Future<void>.delayed(const Duration(seconds: 1));
                  if (!mounted) return;
                  setState(() {
                    signingIn = false;
                    if (val('signin_password') == 'wrong') {
                      result = 'Wrong password';
                    } else {
                      stage = 2;
                      result = '';
                    }
                  });
                },
        ),
        if (signingIn)
          mark('signin_spinner', const CircularProgressIndicator()),
        Text(result),
      ];
    }
    if (stage == 2) {
      return [
        Row(
          children: [
            for (int i = 0; i < 6; i++)
              Expanded(
                child: field(
                  'otp_${i + 1}',
                  '${i + 1}',
                  (v) => digits(i, v),
                  type: TextInputType.number,
                  controller: otp[i],
                  focus: otpFocus[i],
                ),
              ),
          ],
        ),
        Text(result),
      ];
    }
    return [
      Text('Signed in as ${val('signin_email')}'),
      button(
        'signout',
        'Sign out',
        () => setState(() {
          stage = 1;
          values.clear();
          result = '';
        }),
      ),
    ];
  }

  void digits(int i, String text) {
    int n = i;
    for (final char in text.replaceAll(RegExp(r'\D'), '').split('')) {
      if (n == 6) break;
      otp[n++].text = char;
    }
    if (n < 6) {
      otpFocus[text.isEmpty ? math.max(0, i - 1) : n].requestFocus();
      return;
    }
    if (otp.map((e) => e.text).join() == '123456') {
      setState(() => stage = 3);
    } else {
      for (final f in otp) {
        f.clear();
      }
      otpFocus.first.requestFocus();
      message('Wrong code');
    }
  }

  Widget accessibilityPage() {
    final good = widget.kind == 'a11y_good';
    return frame(
      context,
      scenarios[widget.kind]!,
      Column(
        children: [
          label(
            good ? 'good_result' : 'poor_result',
            result.isEmpty ? 'Nothing yet' : result,
          ),
          Expanded(
            child: page([
              Row(
                children: [
                  for (final item in ['Like', 'Share', 'Trash'])
                    if (good)
                      button(
                        'good_${item.toLowerCase()}',
                        item,
                        () => message(
                          {
                            'Like': 'Liked',
                            'Share': 'Shared',
                            'Trash': 'Trashed',
                          }[item]!,
                        ),
                      )
                    else
                      ExcludeSemantics(
                        child: IconButton(
                          onPressed: () => message(
                            {
                              'Like': 'Liked',
                              'Share': 'Shared',
                              'Trash': 'Trashed',
                            }[item]!,
                          ),
                          icon: Icon(
                            {
                              'Like': Icons.favorite,
                              'Share': Icons.share,
                              'Trash': Icons.delete,
                            }[item],
                          ),
                        ),
                      ),
                ],
              ),
              if (good)
                Semantics(
                  identifier: 'good_volume',
                  label: 'Volume',
                  child: Slider(
                    value: formVolume,
                    min: 1,
                    max: 5,
                    divisions: 4,
                    onChanged: (v) => setState(() {
                      formVolume = v;
                      result = 'Volume ${v.round()}';
                    }),
                  ),
                )
              else
                ExcludeSemantics(
                  child: LayoutBuilder(
                    builder: (c, box) => GestureDetector(
                      onTapDown: (d) => message(
                        'Volume ${(d.localPosition.dx * 5 / box.maxWidth).floor().clamp(0, 4) + 1}',
                      ),
                      child: Row(
                        children: [
                          for (int i = 0; i < 5; i++)
                            Expanded(
                              child: Container(
                                height: 26,
                                margin: const EdgeInsets.all(2),
                                color: Colors.blue,
                              ),
                            ),
                        ],
                      ),
                    ),
                  ),
                ),
              Row(
                children: [
                  const Text('Nickname'),
                  Expanded(
                    child: field(
                      good ? 'good_nickname' : '',
                      '',
                      (v) => message('Nickname: $v'),
                    ),
                  ),
                ],
              ),
              button(
                good ? 'good_continue' : '',
                'Continue',
                () => message('Continued'),
              ),
              for (final name in ['Alpha', 'Beta', 'Gamma'])
                Row(
                  children: [
                    Expanded(child: Text(name)),
                    button(
                      good ? 'edit_${name.toLowerCase()}' : '',
                      good ? 'Edit $name' : 'Edit',
                      () => message('Edit $name'),
                    ),
                  ],
                ),
              Semantics(
                identifier: good ? 'order_card' : '',
                container: good,
                explicitChildNodes: good,
                label: good ? null : 'Order 42, 3 items, Track',
                excludeSemantics: !good,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text('Order 42'),
                    Row(
                      children: [
                        const Expanded(child: Text('3 items')),
                        button(
                          good ? 'order_track' : '',
                          good ? 'Track order 42' : 'Track',
                          () => message('Tracking 42'),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
              if (good)
                button('good_tiny', 'Tiny', () => message('Tiny tapped'))
              else
                Row(
                  children: [
                    const Text('Tiny:'),
                    ExcludeSemantics(
                      child: GestureDetector(
                        onTap: () => message('Tiny tapped'),
                        child: Container(
                          width: 12,
                          height: 12,
                          color: Colors.blue,
                        ),
                      ),
                    ),
                  ],
                ),
              if (good)
                button(
                  'good_offer',
                  'Secret offer',
                  () => message('Offer opened'),
                )
              else
                ExcludeSemantics(
                  child: TextButton(
                    onPressed: () => message('Offer opened'),
                    child: const Text('Secret offer'),
                  ),
                ),
              if (good) ...[
                button('good_save', 'Save', () {
                  timer?.cancel();
                  timer = Timer(
                    const Duration(seconds: 1),
                    () => setState(() => status = 'Saved'),
                  );
                }),
                Semantics(
                  identifier: 'good_status',
                  liveRegion: true,
                  child: Text(status),
                ),
                Row(
                  children: [
                    Expanded(
                      child: button(
                        'good_qty_minus',
                        'Decrease quantity',
                        () => setState(() => quantity--),
                        value: '$quantity',
                      ),
                    ),
                    label('good_qty_count', '$quantity'),
                    Expanded(
                      child: button(
                        'good_qty_plus',
                        'Increase quantity',
                        () => setState(() => quantity++),
                        value: '$quantity',
                      ),
                    ),
                  ],
                ),
                MergeSemantics(
                  child: mark(
                    'good_push',
                    SwitchListTile(
                      title: const Text('Push notifications'),
                      value: flags.contains('push'),
                      onChanged: (_) => toggle('push'),
                    ),
                  ),
                ),
              ],
            ]),
          ),
        ],
      ),
    );
  }
}

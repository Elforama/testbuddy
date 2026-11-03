// Scenario 14, "Keyboard" (bench/SCENARIOS.md): a field the keyboard covers, an IME Next
// chain, autofocus and a multiline field.
(() => {
  const { h } = Bench;

  Bench.scenario('keyboard', 'Keyboard', (page, sec) => {
    const status = h('span', { id: 'kb_status', class: 'sticky-status' }, 'Nothing typed');
    const set = (t) => (status.textContent = t);

    const first = h('input', { id: 'kb_first', placeholder: 'First', enterkeyhint: 'next', autocomplete: 'off' });
    const second = h('input', { id: 'kb_second', placeholder: 'Second', enterkeyhint: 'next', autocomplete: 'off' });
    const third = h('input', { id: 'kb_third', placeholder: 'Third', enterkeyhint: 'done', autocomplete: 'off' });
    const onEnter = (el, f) => el.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); f(); } });
    onEnter(first, () => second.focus());
    onEnter(second, () => third.focus());
    onEnter(third, () => { third.blur(); set(`Chain done: ${first.value}/${second.value}/${third.value}`); });

    const lines = h('span', { id: 'kb_lines' }, 'Lines: 0');
    const multi = h('textarea', { id: 'kb_multiline', rows: '3', placeholder: 'Long text', oninput: (e) => (lines.textContent = 'Lines: ' + (e.target.value ? e.target.value.split('\n').length : 0)) });

    // Pinned to the bottom (position: fixed): the screen does not move for the keyboard.
    const bottom = h('input', { id: 'kb_bottom', placeholder: 'Bottom field', autocomplete: 'off', oninput: (e) => set('Bottom: ' + e.target.value) });
    const pinned = h('div', { class: 'kb-bottom' }, bottom, h('button', { id: 'kb_submit', onclick: () => set('Submitted') }, 'Submit'));

    page.append(
      status,
      first, second, third,
      multi, lines,
      h('button', { id: 'kb_autofocus_open', onclick: () => Bench.go('/keyboard/autofocus') }, 'Open autofocus'),
    );
    sec.append(pinned);
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        for (const f of [first, second, third, multi, bottom]) f.value = '';
        lines.textContent = 'Lines: 0';
        set('Nothing typed');
      },
    };
  });

  Bench.sub('kb_autofocus', '/keyboard/autofocus', 'Autofocus', 'sub_title', (page) => {
    const field = h('input', { id: 'kb_auto', placeholder: 'Focused at start', autofocus: true, autocomplete: 'off' });
    page.append(field);
    return { enter() { field.value = ''; field.focus({ preventScroll: true }); } };
  });
})();

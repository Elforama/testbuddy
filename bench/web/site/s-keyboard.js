// Scenario 14, "Keyboard" (bench/SCENARIOS.md): a field the keyboard covers, an IME Next
// chain, autofocus, a multiline field and a field with its own key handler.
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

    // A field with its own key handler: counts the Backspace keys it gets (and lets them delete).
    const backspaces = h('span', { id: 'kb_backspaces' }, 'Backspaces: 0');
    let nBack = 0;
    const keys = h('input', { id: 'kb_keys', placeholder: 'Key field', autocomplete: 'off',
      onkeydown: (e) => { if (e.key === 'Backspace') backspaces.textContent = 'Backspaces: ' + ++nBack; } });

    // Pinned to the bottom (position: fixed): the screen does not move for the keyboard.
    const bottom = h('input', { id: 'kb_bottom', placeholder: 'Bottom field', autocomplete: 'off', oninput: (e) => set('Bottom: ' + e.target.value) });
    const pinned = h('div', { class: 'kb-bottom' }, bottom, h('button', { id: 'kb_submit', onclick: () => set('Submitted') }, 'Submit'));

    page.append(
      status,
      first, second, third,
      multi, lines,
      h('button', { id: 'kb_autofocus_open', onclick: () => Bench.go('/keyboard/autofocus') }, 'Open autofocus'),
      // Last, below the rest, so the fields above keep their places.
      keys, backspaces,
      h('button', { id: 'kb_formats_open', onclick: () => Bench.go('/keyboard/formats') }, 'Open formatted fields'),
    );
    sec.append(pinned);
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        for (const f of [first, second, third, multi, bottom, keys]) f.value = '';
        lines.textContent = 'Lines: 0';
        nBack = 0;
        backspaces.textContent = 'Backspaces: 0';
        set('Nothing typed');
      },
    };
  });

  Bench.sub('kb_autofocus', '/keyboard/autofocus', 'Autofocus', 'sub_title', (page) => {
    const field = h('input', { id: 'kb_auto', placeholder: 'Focused at start', autofocus: true, autocomplete: 'off' });
    page.append(field);
    return { enter() { field.value = ''; field.focus({ preventScroll: true }); } };
  });

  // Fields whose value the page changes after the keys (web only: bench/SCENARIOS.md, 14):
  // a phone mask, a debounced search, a late rewrite and a controlled field.
  Bench.sub('kb_formats', '/keyboard/formats', 'Formatted fields', 'sub_title', (page) => {
    // A mask: on every input, the digits are rewritten as "(555) 123-4567".
    const phone = h('input', { id: 'fmt_phone', placeholder: 'Phone', autocomplete: 'off', inputmode: 'tel' });
    phone.addEventListener('input', () => {
      const d = phone.value.replace(/\D/g, '').slice(0, 10);
      phone.value = d.length > 6 ? `(${d.slice(0, 3)}) ${d.slice(3, 6)}-${d.slice(6)}` : d.length > 3 ? `(${d.slice(0, 3)}) ${d.slice(3)}` : d;
    });
    // A debounced update: the results line follows the field 300 ms after the last key.
    const results = h('span', { id: 'fmt_search_results' }, 'Results for: ');
    let debounce = 0;
    const search = h('input', { id: 'fmt_search', placeholder: 'Search', autocomplete: 'off',
      oninput: () => { clearTimeout(debounce); debounce = setTimeout(() => (results.textContent = 'Results for: ' + search.value), 300); } });
    // A late rewrite: 150 ms after the last key, the field's value is upper-cased.
    let late = 0;
    const upper = h('input', { id: 'fmt_upper', placeholder: 'Upper case', autocomplete: 'off',
      oninput: () => { clearTimeout(late); late = setTimeout(() => (upper.value = upper.value.toUpperCase()), 150); } });
    // A controlled field, as a UI framework renders one: the input event updates state, and the
    // next frame renders the field from it (letters and digits only, upper case).
    let code = '';
    const codeField = h('input', { id: 'fmt_code', placeholder: 'Code', autocomplete: 'off',
      oninput: (e) => { code = e.target.value.replace(/[^0-9a-z]/gi, '').toUpperCase(); requestAnimationFrame(() => { if (codeField.value !== code) codeField.value = code; }); } });
    page.append(phone, search, results, upper, codeField);
    return {
      enter() {
        clearTimeout(debounce);
        clearTimeout(late);
        for (const f of [phone, search, upper, codeField]) f.value = '';
        code = '';
        results.textContent = 'Results for: ';
      },
    };
  });
})();

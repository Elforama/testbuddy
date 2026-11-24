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

    const backspaces = h('span', { id: 'kb_backspaces' }, 'Backspaces: 0');
    let nBack = 0;
    const keys = h('input', { id: 'kb_keys', placeholder: 'Key field', autocomplete: 'off',
      onkeydown: (e) => { if (e.key === 'Backspace') backspaces.textContent = 'Backspaces: ' + ++nBack; } });

    const bottom = h('input', { id: 'kb_bottom', placeholder: 'Bottom field', autocomplete: 'off', oninput: (e) => set('Bottom: ' + e.target.value) });
    const pinned = h('div', { class: 'kb-bottom' }, bottom, h('button', { id: 'kb_submit', onclick: () => set('Submitted') }, 'Submit'));

    page.append(
      status,
      first, second, third,
      multi, lines,
      h('button', { id: 'kb_autofocus_open', onclick: () => Bench.go('/keyboard/autofocus') }, 'Open autofocus'),
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

  Bench.sub('kb_formats', '/keyboard/formats', 'Formatted fields', 'sub_title', (page) => {
    const phone = h('input', { id: 'fmt_phone', placeholder: 'Phone', autocomplete: 'off', inputmode: 'tel' });
    phone.addEventListener('input', () => {
      const d = phone.value.replace(/\D/g, '').slice(0, 10);
      phone.value = d.length > 6 ? `(${d.slice(0, 3)}) ${d.slice(3, 6)}-${d.slice(6)}` : d.length > 3 ? `(${d.slice(0, 3)}) ${d.slice(3)}` : d;
    });
    const results = h('span', { id: 'fmt_search_results' }, 'Results for: ');
    let debounce = 0;
    const search = h('input', { id: 'fmt_search', placeholder: 'Search', autocomplete: 'off',
      oninput: () => { clearTimeout(debounce); debounce = setTimeout(() => (results.textContent = 'Results for: ' + search.value), 300); } });
    let late = 0;
    const upper = h('input', { id: 'fmt_upper', placeholder: 'Upper case', autocomplete: 'off',
      oninput: () => { clearTimeout(late); late = setTimeout(() => (upper.value = upper.value.toUpperCase()), 150); } });
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

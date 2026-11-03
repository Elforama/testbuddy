// Scenario 5, "Controls" (bench/SCENARIOS.md): every standard control, each with a status line.
(() => {
  const { h } = Bench;

  Bench.scenario('controls', 'Controls', (page) => {
    const group = (title, ...kids) => h('div', { class: 'group' }, h('h2', {}, title), ...kids);
    const status = (id, text) => h('span', { id }, text);
    const setText = (el, t) => (el.textContent = t);
    const resets = [];

    // Tabs
    const tabsStatus = status('tabs_status', 'Tab: One');
    const tabs = ['One', 'Two', 'Three'].map((n) => h('button', { id: 'tab_' + n.toLowerCase(), role: 'tab', 'aria-selected': 'false' }, n));
    const pickTab = (n) => {
      tabs.forEach((t) => t.setAttribute('aria-selected', String(t.textContent === n)));
      setText(tabsStatus, 'Tab: ' + n);
    };
    tabs.forEach((t) => t.addEventListener('click', () => pickTab(t.textContent)));
    resets.push(() => pickTab('One'));

    // Segmented control
    const segStatus = status('seg_status', 'Size: M');
    const segs = ['S', 'M', 'L'].map((n) => h('button', { id: 'seg_' + n.toLowerCase(), 'aria-pressed': 'false' }, n));
    const pickSeg = (n) => {
      segs.forEach((s) => s.setAttribute('aria-pressed', String(s.textContent === n)));
      setText(segStatus, 'Size: ' + n);
    };
    segs.forEach((s) => s.addEventListener('click', () => pickSeg(s.textContent)));
    resets.push(() => pickSeg('M'));

    // Radio group
    const radioStatus = status('radio_status', 'Color: none');
    const radios = ['Red', 'Green', 'Blue'].map((n) => {
      const input = h('input', { type: 'radio', name: 'color', id: 'radio_' + n.toLowerCase(), onchange: () => setText(radioStatus, 'Color: ' + n) });
      return h('label', { class: 'row' }, input, n);
    });
    resets.push(() => { radios.forEach((l) => (l.firstChild.checked = false)); setText(radioStatus, 'Color: none'); });

    // Checkboxes
    const checkStatus = status('check_status', 'Basket: empty');
    const checks = ['Milk', 'Eggs'].map((n) => h('input', { type: 'checkbox', id: 'check_' + n.toLowerCase(), 'data-name': n }));
    const basket = () => {
      const on = checks.filter((c) => c.checked).map((c) => c.dataset.name);
      setText(checkStatus, 'Basket: ' + (on.length ? on.join(', ') : 'empty'));
    };
    checks.forEach((c) => c.addEventListener('change', basket));
    resets.push(() => { checks.forEach((c) => (c.checked = false)); basket(); });

    // Switch
    const switchStatus = status('switch_status', 'Wi-Fi off');
    const wifi = h('input', { type: 'checkbox', role: 'switch', id: 'switch_wifi', onchange: (e) => setText(switchStatus, e.target.checked ? 'Wi-Fi on' : 'Wi-Fi off') });
    resets.push(() => { wifi.checked = false; setText(switchStatus, 'Wi-Fi off'); });

    // Slider
    const sliderStatus = status('slider_status', 'Volume: 50');
    const slider = h('input', { type: 'range', id: 'slider_volume', min: '0', max: '100', step: '1', value: '50', 'aria-label': 'Volume', oninput: (e) => setText(sliderStatus, 'Volume: ' + e.target.value) });
    resets.push(() => { slider.value = '50'; setText(sliderStatus, 'Volume: 50'); });

    // Stepper
    let qty = 1;
    const stepperStatus = status('stepper_status', 'Quantity: 1');
    const step = (d) => { qty = Math.max(0, Math.min(10, qty + d)); setText(stepperStatus, 'Quantity: ' + qty); };
    resets.push(() => { qty = 1; step(0); });

    // Dropdown
    const dropStatus = status('dropdown_status', 'Fruit: Apple');
    const drop = h('select', { id: 'dropdown_fruit', onchange: (e) => setText(dropStatus, 'Fruit: ' + e.target.value) },
      ['Apple', 'Banana', 'Cherry'].map((f) => h('option', { value: f }, f)));
    resets.push(() => { drop.value = 'Apple'; setText(dropStatus, 'Fruit: Apple'); });

    // Date picker: the button opens the browser's picker; the field can also be typed into.
    const dateStatus = status('date_status', 'Date: none');
    const dateField = h('input', { type: 'date', id: 'date_field', 'aria-label': 'Date', onchange: (e) => setText(dateStatus, e.target.value ? 'Date: ' + e.target.value : 'Date: none') });
    const dateOpen = h('button', {
      id: 'date_open',
      onclick: () => { dateField.focus(); try { dateField.showPicker(); } catch (e) { /* not allowed here: type instead */ } },
    }, 'Pick a date');
    resets.push(() => { dateField.value = ''; setText(dateStatus, 'Date: none'); });

    // Chips
    const chipStatus = status('chip_status', 'Filters: none');
    const chips = ['Fast', 'Cheap', 'Good'].map((n) => h('button', { id: 'chip_' + n.toLowerCase(), class: 'chip', 'aria-pressed': 'false' }, n));
    const filters = () => {
      const on = chips.filter((c) => c.getAttribute('aria-pressed') === 'true').map((c) => c.textContent);
      setText(chipStatus, 'Filters: ' + (on.length ? on.join(', ') : 'none'));
    };
    chips.forEach((c) => c.addEventListener('click', () => { c.setAttribute('aria-pressed', String(c.getAttribute('aria-pressed') !== 'true')); filters(); }));
    resets.push(() => { chips.forEach((c) => c.setAttribute('aria-pressed', 'false')); filters(); });

    // Rating
    const ratingStatus = status('rating_status', 'Rating: 0');
    const stars = [1, 2, 3, 4, 5].map((k) => h('button', { id: 'star_' + k, class: 'star', 'aria-label': k + (k === 1 ? ' star' : ' stars') }, '★'));
    const rate = (k) => { stars.forEach((s, i) => s.classList.toggle('on', i < k)); setText(ratingStatus, 'Rating: ' + k); };
    stars.forEach((s, i) => s.addEventListener('click', () => rate(i + 1)));
    resets.push(() => rate(0));

    // Text fields
    const nameStatus = status('field_status', 'Hello, nobody');
    const nameField = h('input', { id: 'field_name', placeholder: 'Your name', autocomplete: 'off', oninput: (e) => setText(nameStatus, 'Hello, ' + (e.target.value || 'nobody')) });
    const ageStatus = status('age_status', 'Age: none');
    const ageField = h('input', { id: 'field_age', type: 'number', inputmode: 'numeric', placeholder: 'Age', oninput: (e) => setText(ageStatus, 'Age: ' + (e.target.value || 'none')) });
    const secretStatus = status('secret_status', 'Secret length: 0');
    const secretField = h('input', { id: 'field_secret', type: 'password', placeholder: 'Secret', autocomplete: 'off', oninput: (e) => setText(secretStatus, 'Secret length: ' + e.target.value.length) });
    const notesStatus = status('notes_status', 'Lines: 0');
    const notesField = h('textarea', { id: 'field_notes', rows: '3', placeholder: 'Notes', oninput: (e) => setText(notesStatus, 'Lines: ' + (e.target.value ? e.target.value.split('\n').length : 0)) });
    const FRUIT = ['Apple', 'Apricot', 'Banana', 'Blueberry', 'Cherry'];
    const results = h('ul', { id: 'search_results' });
    const search = (s) => results.replaceChildren(...FRUIT.filter((f) => f.toLowerCase().startsWith(s.toLowerCase()))
      .map((f) => h('li', { id: 'search_' + f.toLowerCase() }, f)));
    const searchField = h('input', { id: 'field_search', type: 'search', placeholder: 'Search fruit', autocomplete: 'off', oninput: (e) => search(e.target.value) });
    search('');
    resets.push(() => {
      for (const f of [nameField, ageField, secretField, notesField, searchField]) f.value = '';
      setText(nameStatus, 'Hello, nobody'); setText(ageStatus, 'Age: none'); setText(secretStatus, 'Secret length: 0'); setText(notesStatus, 'Lines: 0');
      search('');
    });

    // Progress bar: 0 → 100 over 2000 ms in steps of 10.
    const progress = h('progress', { id: 'progress_bar', max: '100', value: '0' });
    const progressStatus = status('progress_status', 'Progress: 0%');
    let progressTimer;
    const setProgress = (v) => { progress.value = v; setText(progressStatus, 'Progress: ' + v + '%'); };
    const startProgress = () => {
      clearInterval(progressTimer);
      let v = 0;
      setProgress(0);
      progressTimer = setInterval(() => { v += 10; setProgress(v); if (v >= 100) clearInterval(progressTimer); }, 200);
    };
    resets.push(() => { clearInterval(progressTimer); setProgress(0); });

    // Expandable section
    const hiddenStatus = status('hidden_status', 'Hidden off');
    const hiddenSwitch = h('input', { type: 'checkbox', role: 'switch', id: 'switch_hidden', onchange: (e) => setText(hiddenStatus, e.target.checked ? 'Hidden on' : 'Hidden off') });
    const expandContent = h('div', { id: 'expand_content', class: 'group', hidden: true },
      'Hidden option',
      h('label', { class: 'row' }, hiddenSwitch, 'Hidden switch'),
      hiddenStatus);
    const expandToggle = h('button', {
      id: 'expand_toggle', 'aria-expanded': 'false',
      onclick: () => { expandContent.hidden = !expandContent.hidden; expandToggle.setAttribute('aria-expanded', String(!expandContent.hidden)); },
    }, 'More options');
    resets.push(() => { expandContent.hidden = true; expandToggle.setAttribute('aria-expanded', 'false'); hiddenSwitch.checked = false; setText(hiddenStatus, 'Hidden off'); });

    // Disabled button
    const disabledStatus = status('disabled_status', 'Untouched');
    const disabled = h('button', { id: 'disabled_button', disabled: true, onclick: () => setText(disabledStatus, 'Touched') }, "Can't tap me");

    // Long press (≥ 500 ms) vs tap
    const lpStatus = status('longpress_status', '');
    let lpTimer, held = false, pressing = false;
    const lp = h('button', { id: 'longpress_button' }, 'Hold me');
    lp.addEventListener('pointerdown', () => { pressing = true; held = false; clearTimeout(lpTimer); lpTimer = setTimeout(() => { held = true; setText(lpStatus, 'Held'); }, 500); });
    const lpEnd = (tapped) => () => {
      if (!pressing) return;
      pressing = false;
      clearTimeout(lpTimer);
      if (!held && tapped) setText(lpStatus, 'Just tapped');
    };
    lp.addEventListener('pointerup', lpEnd(true));
    lp.addEventListener('pointercancel', lpEnd(false));
    lp.addEventListener('pointerleave', lpEnd(false));
    lp.addEventListener('contextmenu', (e) => e.preventDefault());
    resets.push(() => setText(lpStatus, ''));

    // Double tap vs single tap, decided 300 ms after the last tap.
    const dtStatus = status('doubletap_status', '');
    let taps = 0, dtTimer;
    const dt = h('div', { id: 'doubletap_box', role: 'button', tabindex: '0' }, 'Double-tap me');
    dt.addEventListener('click', () => {
      taps++;
      clearTimeout(dtTimer);
      dtTimer = setTimeout(() => { setText(dtStatus, taps >= 2 ? 'Double-tapped' : 'Single tap'); taps = 0; }, 300);
    });
    resets.push(() => setText(dtStatus, ''));

    page.append(
      group('Tabs', h('div', { role: 'tablist' }, tabs), tabsStatus),
      group('Segmented control', h('div', { class: 'segmented' }, segs), segStatus),
      group('Radio group', h('div', { role: 'radiogroup', class: 'row' }, radios), radioStatus),
      group('Checkboxes', h('div', { class: 'row' }, checks.map((c) => h('label', { class: 'row' }, c, c.dataset.name))), checkStatus),
      group('Switch', h('label', { class: 'row' }, wifi, 'Wi-Fi'), switchStatus),
      group('Slider', slider, sliderStatus),
      group('Stepper', h('div', { class: 'row stepper' },
        h('button', { id: 'stepper_minus', 'aria-label': 'Decrease', onclick: () => step(-1) }, '−'),
        h('button', { id: 'stepper_plus', 'aria-label': 'Increase', onclick: () => step(1) }, '+')), stepperStatus),
      group('Dropdown', drop, dropStatus),
      group('Date picker', h('div', { class: 'row' }, dateOpen, dateField), dateStatus),
      group('Chips', h('div', { class: 'row' }, chips), chipStatus),
      group('Rating', h('div', { class: 'row' }, stars), ratingStatus),
      group('Text field', nameField, nameStatus),
      group('Number field', ageField, ageStatus),
      group('Password field', secretField, secretStatus),
      group('Multiline field', notesField, notesStatus),
      group('Search field', searchField, results),
      group('Progress', progress, h('button', { id: 'progress_go', onclick: startProgress }, 'Start'), progressStatus),
      group('Expandable section', expandToggle, expandContent),
      group('Disabled button', disabled, disabledStatus),
      group('Long press', lp, lpStatus),
      group('Double tap', dt, dtStatus),
    );
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        resets.forEach((r) => r());
        page.scrollTop = 0;
      },
      leave() { clearInterval(progressTimer); },
    };
  });
})();

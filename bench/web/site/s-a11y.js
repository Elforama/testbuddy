// Scenarios 9 and 10, "Poor accessibility" and "Good accessibility" (bench/SCENARIOS.md):
// the same small form, built badly on purpose and then done right.
(() => {
  const { h } = Bench;

  const svg = (path) => 'data:image/svg+xml,' + encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${path}</svg>`);
  const ICONS = {
    heart: '<path d="M12 21s-7-4.5-9.5-9A5.5 5.5 0 0 1 12 6a5.5 5.5 0 0 1 9.5 6c-2.5 4.5-9.5 9-9.5 9z"/>',
    share: '<path d="M4 12v7a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-7"/><path d="M12 3v12"/><path d="M7 8l5-5 5 5"/>',
    trash: '<path d="M4 7h16"/><path d="M9 7V4h6v3"/><path d="M6 7l1 13h10l1-13"/>',
  };
  // "Secret offer" rendered into a bitmap (a PNG data URL): no text anywhere in the DOM.
  const offerBitmap = () => {
    const c = document.createElement('canvas');
    c.width = 400; c.height = 120;
    const g = c.getContext('2d');
    g.fillStyle = '#ffe2a8'; g.fillRect(0, 0, 400, 120);
    g.strokeStyle = '#e09b00'; g.lineWidth = 6; g.setLineDash([16, 10]); g.strokeRect(6, 6, 388, 108);
    g.fillStyle = '#7a3e00'; g.font = 'bold 48px sans-serif'; g.textAlign = 'center'; g.textBaseline = 'middle';
    g.fillText('Secret offer', 200, 62);
    return c.toDataURL('image/png');
  };

  // ---- 9. Poor accessibility: div soup, no ids (except the result), no labels, no roles ----
  Bench.scenario('a11y_poor', 'Poor accessibility', (page) => {
    const result = h('span', { id: 'poor_result', class: 'sticky-status' }, 'Nothing yet');
    const set = (t) => (result.textContent = t);
    const tappable = (el, f) => { el.addEventListener('click', f); return el; };

    // Icon-only "buttons": images in plain divs.
    const icon = (name, text) => tappable(h('div', { class: 'poor-icon' }, h('img', { src: svg(ICONS[name]) })), () => set(text));

    // A volume bar drawn on a canvas: one element, five segments.
    const vol = h('canvas', { width: '250', height: '40', style: 'width:250px;height:40px' });
    const drawVol = (k) => {
      const g = vol.getContext('2d');
      g.clearRect(0, 0, 250, 40);
      for (let i = 0; i < 5; i++) {
        g.fillStyle = i < k ? '#3366ff' : '#dde3f0';
        g.fillRect(i * 50 + 3, 40 - (i + 1) * 7 - 2, 44, (i + 1) * 7 + 2);
      }
    };
    vol.addEventListener('click', (e) => {
      const r = vol.getBoundingClientRect();
      const k = Math.max(1, Math.min(5, Math.floor((e.clientX - r.left) / (r.width / 5)) + 1));
      drawVol(k);
      set('Volume ' + k);
    });

    // A text next to a field, not linked to it; the field has no hint and no id.
    const nick = h('input', { autocomplete: 'off', oninput: (e) => set('Nickname: ' + e.target.value) });

    // Three rows, each with a button "Edit".
    const editRow = (name) => h('div', { class: 'poor-row' }, h('span', { style: 'width:70px' }, name), h('button', { onclick: () => set('Edit ' + name) }, 'Edit'));

    // A card that is one accessibility element: the parts inside are hidden from assistive tech;
    // "Track" is only drawn like a button.
    const card = h('div', { class: 'card-box', role: 'button', tabindex: '0', 'aria-label': 'Order 42, 3 items, Track' },
      h('strong', { 'aria-hidden': 'true' }, 'Order 42'),
      // The count, and Track at the row's right end (SCENARIOS.md: the same on every platform).
      h('div', { 'aria-hidden': 'true', style: 'display:flex;align-items:center;justify-content:space-between;gap:12px' },
        h('span', { class: 'muted' }, '3 items'),
        h('span', { class: 'fake-btn' }, 'Track')));
    card.addEventListener('click', (e) => { if (e.target.closest('.fake-btn')) set('Tracking 42'); });

    const offer = tappable(h('img', { src: offerBitmap(), class: 'offer-img' }), () => set('Offer opened'));

    page.append(
      result,
      h('div', { class: 'poor-icons' }, icon('heart', 'Liked'), icon('share', 'Shared'), icon('trash', 'Trashed')),
      vol,
      h('div', { class: 'poor-row' }, h('div', {}, 'Nickname'), nick),
      tappable(h('div', { class: 'poor-text' }, 'Continue'), () => set('Continued')),
      editRow('Alpha'), editRow('Beta'), editRow('Gamma'),
      card,
      h('div', { class: 'poor-row' }, h('span', {}, 'Tiny:'), tappable(h('div', { class: 'tiny-dot' }), () => set('Tiny tapped'))),
      offer,
    );
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        set('Nothing yet');
        nick.value = '';
        drawVol(0);
      },
    };
  });

  // ---- 10. Good accessibility: ids, labels, roles and state ----
  Bench.scenario('a11y_good', 'Good accessibility', (page) => {
    const result = h('span', { id: 'good_result', class: 'sticky-status' }, 'Nothing yet');
    const set = (t) => (result.textContent = t);
    const inlineSvg = (name) => {
      const wrap = document.createElement('span');
      wrap.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${ICONS[name]}</svg>`;
      return wrap.firstChild;
    };
    const iconButton = (id, label, name, text) => h('button', { id, class: 'icon-btn', 'aria-label': label, onclick: () => set(text) }, inlineSvg(name));

    const volume = h('input', { id: 'good_volume', type: 'range', min: '1', max: '5', step: '1', value: '3', 'aria-label': 'Volume', 'aria-valuetext': 'Volume 3' });
    const onVolume = () => {
      volume.setAttribute('aria-valuetext', 'Volume ' + volume.value);
      set('Volume ' + volume.value);
    };
    volume.addEventListener('input', onVolume);
    volume.addEventListener('click', onVolume); // a tap on the current value still reports it

    const editRow = (name) => h('div', { class: 'poor-row' }, h('span', { style: 'width:70px' }, name),
      h('button', { id: 'edit_' + name.toLowerCase(), 'aria-label': 'Edit ' + name, onclick: () => set('Edit ' + name) }, 'Edit'));

    const status = h('p', { id: 'good_status', role: 'status', 'aria-live': 'polite', style: 'margin:0' });
    let saveTimer;
    const offer = h('div', {
      id: 'good_offer', role: 'img', 'aria-label': 'Secret offer', tabindex: '0',
      style: `background-image:url("${offerBitmap()}")`,
      onclick: () => set('Offer opened'),
      onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') set('Offer opened'); },
    });

    page.append(
      result,
      h('div', { class: 'row', role: 'group', 'aria-label': 'Actions' },
        iconButton('good_like', 'Like', 'heart', 'Liked'),
        iconButton('good_share', 'Share', 'share', 'Shared'),
        iconButton('good_trash', 'Trash', 'trash', 'Trashed')),
      // Labels wrap their fields (linked implicitly; a `for` attribute would also work, but tb
      // takes a label's `for` as its id, and the label would then share the field's id).
      h('label', { class: 'field' }, 'Volume', volume),
      h('label', { class: 'field' }, 'Nickname',
        h('input', { id: 'good_nickname', autocomplete: 'off', oninput: (e) => set('Nickname: ' + e.target.value) })),
      h('button', { id: 'good_continue', onclick: () => set('Continued') }, 'Continue'),
      editRow('Alpha'), editRow('Beta'), editRow('Gamma'),
      h('div', { id: 'order_card', class: 'card-box', role: 'group', 'aria-labelledby': 'order_card_title' },
        h('strong', { id: 'order_card_title' }, 'Order 42'),
        h('span', { class: 'muted' }, '3 items'),
        h('button', { id: 'order_track', 'aria-label': 'Track order 42', onclick: () => set('Tracking 42') }, 'Track')),
      h('div', { class: 'poor-row' }, h('span', {}, 'Tiny:'), h('button', { id: 'good_tiny', onclick: () => set('Tiny tapped') }, 'Tiny')),
      offer,
      h('button', { id: 'good_save', onclick: () => { clearTimeout(saveTimer); status.textContent = ''; saveTimer = setTimeout(() => (status.textContent = 'Saved'), 1000); } }, 'Save'),
      status,
    );
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        set('Nothing yet');
        volume.value = '3';
        volume.setAttribute('aria-valuetext', 'Volume 3');
        page.querySelector('#good_nickname').value = '';
        clearTimeout(saveTimer);
        status.textContent = '';
      },
    };
  });
})();

// The "Native toolkits" mini screen (bench/SCENARIOS.md, scenario 11), built into any root: a
// section of the page (Plain DOM), an open shadow root (Shadow DOM) or an iframe's document
// (toolkit.html). Same ids and texts in each.
window.BenchToolkit = {
  css: `
    .tk { display: flex; flex-direction: column; height: 100%; font: 16px/1.4 -apple-system, system-ui, sans-serif; color: #111; background: #fff; }
    .tk * { box-sizing: border-box; }
    .tk button, .tk input { font: inherit; padding: 10px 12px; }
    .tk-bar { flex: none; display: flex; align-items: center; gap: 12px; padding: 12px 16px; border-bottom: 1px solid #ddd; }
    .tk-bar h1 { margin: 0; font-size: 20px; }
    .tk-controls { flex: none; display: flex; flex-direction: column; gap: 10px; padding: 12px 16px; border-bottom: 1px solid #ddd; }
    .tk-row { display: flex; align-items: center; gap: 10px; }
    .tk-list { flex: 1; min-height: 0; overflow-y: auto; }
    .tk-list div { height: 56px; display: flex; align-items: center; padding: 0 16px; border-bottom: 1px solid #eee; }
  `,
  // build(root, title, onBack): root is an element or a shadow root; returns the mini screen.
  build(root, title, onBack) {
    const doc = root.ownerDocument || root;
    const h = (tag, props, ...kids) => {
      const el = doc.createElement(tag);
      for (const [k, v] of Object.entries(props || {})) {
        if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
        else if (k === 'class') el.className = v;
        else el.setAttribute(k, v);
      }
      for (const c of kids.flat()) el.append(c);
      return el;
    };
    let taps = 0;
    const count = h('span', { id: 'tk_count' }, 'Taps: 0');
    const switchStatus = h('span', { id: 'tk_switch_status' }, 'Off');
    const echo = h('span', { id: 'tk_echo' }, 'Echo: ');
    const rows = [];
    for (let i = 1; i <= 30; i++) rows.push(h('div', { id: 'tk_row_' + i }, 'Toolkit row ' + i));
    const screen = h('div', { class: 'tk' },
      h('style', {}, this.css),
      h('div', { class: 'tk-bar' },
        h('button', { id: 'back', onclick: () => onBack() }, 'Back'),
        h('h1', { id: 'tk_title' }, title)),
      h('div', { class: 'tk-controls' },
        h('div', { class: 'tk-row' }, h('button', { id: 'tk_tap', onclick: () => (count.textContent = 'Taps: ' + ++taps) }, 'Tap'), count),
        h('div', { class: 'tk-row' },
          h('label', { class: 'tk-row' },
            h('input', { id: 'tk_switch', type: 'checkbox', role: 'switch', onchange: (e) => (switchStatus.textContent = e.target.checked ? 'On' : 'Off') }),
            'Switch'),
          switchStatus),
        h('div', { class: 'tk-row' },
          h('input', { id: 'tk_field', placeholder: 'Type here', autocomplete: 'off', oninput: (e) => (echo.textContent = 'Echo: ' + e.target.value) }),
          echo)),
      h('div', { class: 'tk-list' }, rows));
    root.append(screen);
    return screen;
  },
};

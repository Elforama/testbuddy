// Scenario 11, "Native toolkits" (bench/SCENARIOS.md), web edition: the same mini screen in
// plain DOM, inside an open shadow root, in a same-origin iframe, and (the counter only) on a
// canvas.
(() => {
  const { h } = Bench;

  Bench.scenario('native', 'Native toolkits', (page) => {
    page.append(
      h('button', { id: 'tk_dom', onclick: () => Bench.go('/native/dom') }, 'Plain DOM'),
      h('button', { id: 'tk_shadow', onclick: () => Bench.go('/native/shadow') }, 'Shadow DOM'),
      h('button', { id: 'tk_iframe', onclick: () => Bench.go('/native/iframe') }, 'Iframe'),
      h('button', { id: 'tk_canvas', onclick: () => Bench.go('/native/canvas') }, 'Canvas'),
    );
  });

  // A pushed screen holding only `content` (no bar: the mini screen has its own Back).
  const bare = (name, path, content, hooks) => {
    const sec = h('section', { id: 'screen-' + name, class: 'scn' }, content);
    Bench.screen(name, sec, Object.assign({ pushed: true }, hooks));
    Bench.route(path, name);
  };

  // Plain DOM
  const domHost = h('div', { class: 'tk-host' });
  bare('tk_dom', '/native/dom', domHost, {
    enter(params, info) {
      if (info.how === 'pop') return;
      domHost.replaceChildren();
      BenchToolkit.build(domHost, 'Plain DOM', () => Bench.back()).style.flex = '1';
    },
  });

  // Shadow DOM: everything, Back included, inside an open shadow root.
  const shadowHost = h('div', { id: 'tk_host', class: 'tk-host' });
  const shadow = shadowHost.attachShadow({ mode: 'open' });
  bare('tk_shadow', '/native/shadow', shadowHost, {
    enter(params, info) {
      if (info.how === 'pop') return;
      shadow.replaceChildren(h('style', {}, ':host { display: flex; flex-direction: column; } .tk { flex: 1; min-height: 0; }'));
      BenchToolkit.build(shadow, 'Shadow DOM', () => Bench.back());
    },
  });

  // Iframe (same origin): toolkit.html in a new iframe on each open (setting a live frame's src
  // would add browser history entries).
  const frameHost = h('div', { class: 'tk-host' });
  bare('tk_iframe', '/native/iframe', frameHost, {
    enter(params, info) {
      if (info.how === 'pop' && frameHost.firstChild) return;
      frameHost.replaceChildren(h('iframe', { id: 'tk_frame', class: 'tk-frame', title: 'Iframe', src: 'toolkit.html?title=Iframe' }));
    },
    leave() { frameHost.replaceChildren(); },
  });

  // Canvas: only the counter. A "Tap" button drawn at the centre of a canvas; the count is DOM.
  Bench.sub('tk_canvas', '/native/canvas', 'Canvas', 'tk_title', (page) => {
    const W = 300, H = 200, BW = 120, BH = 52;
    const canvas = h('canvas', { id: 'tk_surface' });
    const count = h('span', { id: 'tk_count' }, 'Taps: 0');
    let taps = 0, pressed = false;
    const draw = () => {
      const dpr = window.devicePixelRatio || 1;
      canvas.width = W * dpr; canvas.height = H * dpr;
      const g = canvas.getContext('2d');
      g.scale(dpr, dpr);
      g.fillStyle = '#f7f7f7'; g.fillRect(0, 0, W, H);
      const x = (W - BW) / 2, y = (H - BH) / 2;
      g.fillStyle = pressed ? '#2448b8' : '#3366ff';
      g.beginPath(); g.roundRect(x, y, BW, BH, 10); g.fill();
      g.fillStyle = '#fff'; g.font = '600 20px -apple-system, system-ui, sans-serif';
      g.textAlign = 'center'; g.textBaseline = 'middle';
      g.fillText('Tap', W / 2, H / 2 + 1);
    };
    const inButton = (e) => {
      const r = canvas.getBoundingClientRect();
      const x = (e.clientX - r.left) * (W / r.width), y = (e.clientY - r.top) * (H / r.height);
      return Math.abs(x - W / 2) <= BW / 2 && Math.abs(y - H / 2) <= BH / 2;
    };
    canvas.addEventListener('pointerdown', (e) => { if (inButton(e)) { pressed = true; draw(); } });
    canvas.addEventListener('pointerup', () => { if (pressed) { pressed = false; draw(); } });
    canvas.addEventListener('click', (e) => { if (inButton(e)) count.textContent = 'Taps: ' + ++taps; });
    page.append(canvas, count);
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        taps = 0; count.textContent = 'Taps: 0'; pressed = false; draw();
      },
    };
  });
})();

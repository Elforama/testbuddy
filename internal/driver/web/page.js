// Test Buddy's in-page helper. Installed in every document before its own scripts run.
//
// 1. Change events: a MutationObserver plus input/scroll/animation listeners call the
//    __tbNotify binding, which reaches the host as a CDP event. The host never polls the page.
// 2. Snapshots: window.__tb.snapshot() returns the visible elements in pre-order with depth,
//    using the flow format's web rules for text and ids.
(function () {
  if (window.__tb) return;
  const tb = (window.__tb = { nodes: [] });

  // ---- change events ----
  // Each notification says whether the change is "solid" (the page is doing something that
  // will end) or only "restless": an element that has kept changing for 500 ms with gaps
  // under 400 ms (a ticker, a stopwatch). Restless changes still count as changes, but they
  // do not hold the page unsettled, so a test can act on a screen that never stops moving.
  // An element found restless stays so: a ticker that paused while its screen was hidden is
  // not learned again when the screen comes back. (Per-frame changes are not taken as restless
  // any sooner: a scripted 300 ms slide-in changes every frame too, and must be waited out.)
  let anims = 0;
  const RESTLESS_AFTER = 500, RESTLESS_GAP = 400;
  const changeLog = new WeakMap();
  const restless = new WeakSet();
  // Changes that follow the test's own input (a key, a click, a drag) answer it: they do not
  // count towards an element's restless time. Else a list that grows with every Enter of a
  // quick test is taken for a ticker, and its next change is not waited for.
  let lastInput = -1e9;
  const acted = () => { lastInput = performance.now(); };
  tb.acted = acted;
  for (const ev of ['keydown', 'mousedown', 'pointerdown', 'touchstart', 'wheel']) {
    window.addEventListener(ev, (e) => { if (e.isTrusted) acted(); }, { capture: true, passive: true });
  }
  const notify = (solid = true) => {
    try { window.__tbNotify && window.__tbNotify(JSON.stringify({ a: Math.max(0, anims), s: solid ? 1 : 0 })); } catch (e) {}
  };
  tb.notify = notify;
  const onMutations = (records) => {
    const now = performance.now();
    let solid = false;
    for (const r of records) {
      let t = r.target;
      if (t && t.nodeType === 3) t = t.parentNode;
      if (!t || typeof t !== 'object') { solid = true; continue; }
      if (restless.has(t)) continue;
      let h = changeLog.get(t);
      if (!h || now - h.last > RESTLESS_GAP || now - lastInput < RESTLESS_GAP) h = { since: now, last: now };
      h.last = now;
      changeLog.set(t, h);
      if (now - h.since >= RESTLESS_AFTER) {
        restless.add(t);
        continue;
      }
      solid = true;
    }
    notify(solid);
  };
  const watchedShadows = new WeakSet();
  const watchShadow = (root) => {
    if (watchedShadows.has(root)) return;
    watchedShadows.add(root);
    try {
      new MutationObserver(onMutations).observe(root, { subtree: true, childList: true, attributes: true, characterData: true });
    } catch (e) {}
  };
  const observe = () => {
    try {
      new MutationObserver(onMutations).observe(document, {
        subtree: true, childList: true, attributes: true, characterData: true,
      });
    } catch (e) {}
  };
  if (document.documentElement) observe();
  else document.addEventListener('readystatechange', observe, { once: true });
  const solid = () => notify(true);
  for (const ev of ['input', 'change', 'focusin', 'focusout', 'toggle', 'load']) {
    window.addEventListener(ev, solid, true);
  }
  window.addEventListener('scroll', solid, { capture: true, passive: true });
  const start = () => { anims++; notify(); };
  const end = () => { anims = Math.max(0, anims - 1); notify(); };
  // A color fading (a hover, a completed item's text going gray) moves and hides nothing, so a
  // tap during it lands the same: it does not hold the page up. Opacity does: a fade-in hides.
  const colorOnly = (e) => /(^|-)color$|^(fill|stroke|box-shadow|text-shadow)$/.test(e.propertyName || '');
  window.addEventListener('transitionrun', (e) => { if (!colorOnly(e)) start(); }, true);
  window.addEventListener('transitionend', (e) => { if (!colorOnly(e)) end(); }, true);
  window.addEventListener('transitioncancel', (e) => { if (!colorOnly(e)) end(); }, true);
  window.addEventListener('animationstart', (e) => {
    // An endless animation (a spinner) would keep the page busy forever: count only finite ones.
    try { if (getComputedStyle(e.target).animationIterationCount === 'infinite') return; } catch (x) {}
    start();
  }, true);
  window.addEventListener('animationend', end, true);
  window.addEventListener('animationcancel', end, true);

  // ---- snapshot ----
  const SKIP = new Set(['noscript', 'script', 'br', 'img', 'svg', 'g', 'path', 'style', 'head', 'meta', 'link', 'title', 'template']);
  const TEXT_INPUTS = new Set(['', 'text', 'email', 'password', 'search', 'tel', 'url', 'number', 'date', 'datetime-local', 'month', 'time', 'week']);
  const CLICK_ROLES = new Set(['button', 'link', 'checkbox', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab', 'switch', 'option', 'radio', 'treeitem']);

  const str = (v) => (typeof v === 'string' ? v : '');
  const clean = (s) => s.replace(/[\n\t]/g, ' ').replace(/\s+/g, ' ').trim();

  const textOf = (el, tag) => {
    switch (tag) {
      case 'input':
      case 'textarea':
        return el.value || el.placeholder || el.getAttribute('aria-label') || '';
      case 'select':
        return Array.from(el.selectedOptions || []).map((o) => o.text).join(', ');
    }
    let t = '';
    for (const n of el.childNodes) if (n.nodeType === 3) t += n.textContent;
    return clean(t);
  };

  // The resource id: the first of these that is set.
  const idOf = (el) => {
    const a = (name) => { const v = el.getAttribute(name); return v ? v : ''; };
    return a('flt-semantics-identifier') || a('id') || a('aria-label') || a('name') ||
      a('title') || a('for') || a('data-testid');
  };

  let cursorCache;
  const clickable = (el, tag, role) => {
    if (tag === 'a' && el.hasAttribute('href')) return true;
    if (tag === 'button' || tag === 'select' || tag === 'summary' || tag === 'label' || tag === 'option') return true;
    if (tag === 'input' && el.type !== 'hidden') return true;
    if (CLICK_ROLES.has(role)) return true;
    if (el.hasAttribute('onclick') || typeof el.onclick === 'function') return true;
    const ti = el.getAttribute('tabindex');
    if (ti !== null && +ti >= 0) return true;
    try {
      const cur = getComputedStyle(el).cursor;
      if (cur === 'pointer') {
        // a pointer cursor is inherited: count it only where it starts
        const p = el.parentElement;
        return !p || getComputedStyle(p).cursor !== 'pointer';
      }
    } catch (e) {}
    return false;
  };

  const scrollable = (el) => {
    if (el.scrollHeight <= el.clientHeight + 1 && el.scrollWidth <= el.clientWidth + 1) return false;
    if (el === document.scrollingElement || el === document.body) return true;
    try {
      const s = getComputedStyle(el);
      return /(auto|scroll)/.test(s.overflowY + ' ' + s.overflowX);
    } catch (e) { return false; }
  };

  tb.snapshot = () => {
    const out = [];
    const nodes = [];
    let active = document.activeElement;
    while (active && active.shadowRoot && active.shadowRoot.activeElement) active = active.shadowRoot.activeElement;
    const visit = (el, depth, ox, oy) => {
      const tag = el.tagName.toLowerCase();
      if (SKIP.has(tag)) return;
      if (tag === 'iframe') {
        const r = el.getBoundingClientRect();
        // The content box: where the frame's own (0,0) is.
        const cs = getComputedStyle(el);
        const cx = r.x + (parseFloat(cs.borderLeftWidth) || 0) + (parseFloat(cs.paddingLeft) || 0);
        const cy = r.y + (parseFloat(cs.borderTopWidth) || 0) + (parseFloat(cs.paddingTop) || 0);
        let doc = null;
        try { doc = el.contentDocument; } catch (e) {}
        if (doc && doc.body) {
          visit(doc.body, depth, ox + cx, oy + cy);
        } else if (el.src) {
          // A cross-origin frame: the host reads it through its own CDP session and puts
          // its nodes here.
          out.push({ d: depth, b: [Math.round(r.left + ox), Math.round(r.top + oy), Math.round(r.right + ox), Math.round(r.bottom + oy)],
            c: 'iframe', f: 4096 | 2, frame: { src: el.src, x: Math.round(cx + ox), y: Math.round(cy + oy) } });
          nodes.push(el);
        }
        return;
      }
      if (tag === 'option' && !(el.parentElement && el.parentElement.matches(':focus-within'))) return;
      if (el.checkVisibility && !el.checkVisibility({ visibilityProperty: true, opacityProperty: false })) return;
      const r = el.getBoundingClientRect();
      // A box with no height (or width) that clips its overflow shows nothing, its children
      // included: a banner about to grow from 0, a collapsed panel.
      if (r.width === 0 || r.height === 0) {
        try {
          const cs = getComputedStyle(el);
          if (/(hidden|clip)/.test(cs.overflowX + ' ' + cs.overflowY)) return;
        } catch (e) {}
      }
      const role = (el.getAttribute('role') || '').toLowerCase();
      const type = tag === 'input' ? (el.type || '').toLowerCase() : '';
      let f = 0;
      const disabled = el.disabled === true || el.getAttribute('aria-disabled') === 'true';
      if (clickable(el, tag, role)) f |= 1;
      if (!disabled) f |= 2;
      if (el === active) f |= 4;
      if (scrollable(el)) f |= 8;
      if (el.checked === true || el.getAttribute('aria-checked') === 'true') f |= 16;
      if (el.selected === true || el.getAttribute('aria-selected') === 'true') f |= 32;
      if (type === 'checkbox' || type === 'radio' || role === 'checkbox' || role === 'switch' || role === 'radio') f |= 64;
      if (type === 'password') f |= 128;
      if ((tag === 'input' && TEXT_INPUTS.has(type)) || tag === 'textarea' || el.isContentEditable) f |= 256;
      if ((tag === 'input' || tag === 'textarea') && !el.value && el.placeholder) f |= 2048;
      f |= 4096;
      const n = { d: depth, b: [Math.round(r.left + ox), Math.round(r.top + oy), Math.round(r.right + ox), Math.round(r.bottom + oy)], c: type ? tag + ':' + type : tag, f };
      const t = textOf(el, tag);
      if (t) n.t = t;
      const id = idOf(el);
      if (id) n.id = id;
      const label = el.getAttribute('aria-label');
      if (label) n.cd = label;
      if (el.placeholder) n.h = el.placeholder;
      if (tag === 'input' || tag === 'textarea') n.v = el.value || '';
      if (role) n.r = role;
      out.push(n);
      nodes.push(el);
      // An open shadow root: its content is the element's (a web component's own DOM). Its
      // changes are watched too: the document's observer does not see inside it.
      if (el.shadowRoot) {
        watchShadow(el.shadowRoot);
        for (const c of el.shadowRoot.children) visit(c, depth + 1, ox, oy);
      }
      for (const c of el.children) visit(c, depth + 1, ox, oy);
    };
    if (document.body) visit(document.body, 0, 0, 0);
    tb.nodes = nodes;
    return JSON.stringify({ w: window.innerWidth, h: window.innerHeight, url: location.href, title: document.title, loading: document.readyState !== 'complete', nodes: out });
  };

  // What a click at (x, y) would reach instead of node i of the last snapshot: '' when it
  // reaches the node, something inside it, or something it is inside of (a label around a
  // field, a parent that takes the click for an element with pointer-events: none); else a
  // short name of what covers it (a veil, a dialog's backdrop, a toast). Shadow roots are
  // followed down.
  tb.covered = (i, x, y) => {
    const t = tb.nodes[i];
    if (!t || !t.isConnected) return '';
    // In a same-origin frame (read inline): this document's hit test only sees the frame.
    if (t.ownerDocument !== document) return '';
    let h = document.elementFromPoint(x, y);
    while (h && h.shadowRoot) {
      const inner = h.shadowRoot.elementFromPoint(x, y);
      if (!inner || inner === h) break;
      h = inner;
    }
    if (!h) return '';
    const inside = (a, b) => {
      for (let n = a; n; n = n.parentNode || (n instanceof ShadowRoot ? n.host : null)) if (n === b) return true;
      return false;
    };
    if (inside(h, t) || inside(t, h)) return '';
    const name = h.id ? '#' + h.id : h.tagName.toLowerCase() + (h.className && typeof h.className === 'string' ? '.' + h.className.split(/\s+/)[0] : '');
    return name || '?';
  };

  // Indices (in the last snapshot) of the elements a CSS selector matches.
  tb.css = (sel) => {
    const set = new Set(document.querySelectorAll(sel));
    const idx = [];
    tb.nodes.forEach((el, i) => { if (set.has(el)) idx.push(i); });
    return JSON.stringify(idx);
  };
})();


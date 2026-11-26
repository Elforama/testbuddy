(function () {
  if (window.__tb) return;
  const tb = (window.__tb = { nodes: [] });

  const running = new Map();
  const runningCount = () => {
    let n = 0;
    for (const [el, keys] of running) {
      if (!el.isConnected) running.delete(el);
      else n += keys.size;
    }
    return n;
  };
  const RESTLESS_AFTER = 500, RESTLESS_GAP = 400;
  const changeLog = new WeakMap();
  const restless = new WeakSet();
  let lastInput = -1e9;
  let lastSolid = -1e9;
  const acted = () => { lastInput = performance.now(); };
  tb.acted = acted;
  for (const ev of ['keydown', 'mousedown', 'pointerdown', 'touchstart', 'wheel']) {
    window.addEventListener(ev, (e) => { if (e.isTrusted) acted(); }, { capture: true, passive: true });
  }
  const notify = (solid = true) => {
    if (solid) lastSolid = performance.now();
    try { window.__tbNotify && window.__tbNotify(JSON.stringify({ a: runningCount(), s: solid ? 1 : 0 })); } catch (e) {}
  };
  tb.notify = notify;

  const SHORT_TIMER = 1000, CHAIN_MAX = 1000, RECUR_MAX = 1000;
  const nativeSetTimeout = window.setTimeout, nativeClearTimeout = window.clearTimeout;
  const nativeSetInterval = window.setInterval, nativeClearInterval = window.clearInterval;
  const pendingTimers = new Set();
  let chainStart = null;
  let sentTimers = 0;
  let sendQueued = false;
  const sendTimers = () => {
    if (sendQueued) return;
    sendQueued = true;
    queueMicrotask(() => {
      sendQueued = false;
      const n = pendingTimers.size;
      if (n === sentTimers) return;
      sentTimers = n;
      try { window.__tbNotify && window.__tbNotify(JSON.stringify({ k: 't', t: n })); } catch (e) {}
    });
  };
  const STACK_LIMIT = 12;
  const sites = new Map();
  const callers = new Map();
  const structured = (e, frames) => frames;
  const frameName = (c) => {
    try { return (c.getScriptNameOrSourceURL() || '') + ':' + c.getLineNumber() + ':' + c.getColumnNumber(); } catch (e) { return '?'; }
  };
  const callSite = () => {
    let cs = null;
    const lim = Error.stackTraceLimit, prep = Error.prepareStackTrace;
    try {
      Error.stackTraceLimit = STACK_LIMIT;
      Error.prepareStackTrace = structured;
      cs = new Error().stack;
    } catch (e) {}
    try { Error.stackTraceLimit = lim; Error.prepareStackTrace = prep; } catch (e) {}
    if (!Array.isArray(cs)) return '';
    const n = cs.length;
    let site = '', f = n > 2 ? frameName(cs[2]) : '';
    for (let i = 2; i < n; i++) {
      const up = i + 1 < n ? frameName(cs[i + 1]) : '';
      site += f + '\n';
      let c = callers.get(f);
      if (!c) {
        if (callers.size > 2000) callers.clear();
        callers.set(f, (c = { up, shared: false }));
      } else if (c.up !== up) {
        c.shared = true;
      }
      if (!c.shared) return site;
      f = up;
    }
    return n > 2 && n < STACK_LIMIT ? site : '';
  };
  const AFTER_INPUT = 100, ONE_SHOT = 120;
  const recurs = (site, now, wait) => {
    if (site === '') return false;
    const r = sites.get(site);
    if (!r) {
      if (sites.size > 500) sites.clear();
      sites.set(site, { ran: null, since: null, known: false });
      return false;
    }
    if (r.known) return !(wait <= ONE_SHOT && now - lastInput <= AFTER_INPUT);
    if (r.ran === null || lastInput > r.ran) {
      r.since = null;
      return false;
    }
    if (r.since === null) r.since = now;
    r.known = now - r.since >= RECUR_MAX;
    return r.known;
  };
  const ran = (site) => { const r = sites.get(site); if (r) r.ran = performance.now(); };
  const done = (set, id, outer) => queueMicrotask(() => {
    chainStart = outer;
    if (set.delete(id)) sendTimers();
  });
  const disguised = new Map();
  const disguise = (wrapper, native, name) => {
    try {
      Object.defineProperty(wrapper, 'name', { value: name, configurable: true });
      Object.defineProperty(wrapper, 'length', { value: native.length, configurable: true });
    } catch (e) {}
    disguised.set(wrapper, native);
    return wrapper;
  };
  const nativeToString = Function.prototype.toString;
  const toString = disguise(function () {
    return nativeToString.call(disguised.get(this) || this);
  }, nativeToString, 'toString');
  try { Object.defineProperty(Function.prototype, 'toString', { value: toString, writable: true, configurable: true }); } catch (e) {}

  window.setTimeout = disguise(function (fn, delay, ...args) {
    if (typeof fn !== 'function') return nativeSetTimeout.apply(window, arguments);
    const wait = Math.max(0, Number(delay) || 0);
    const now = performance.now();
    const chain = chainStart === null ? now : chainStart;
    const short = wait <= SHORT_TIMER;
    const site = short ? callSite() : '';
    const counted = short && now + wait - chain <= CHAIN_MAX && !recurs(site, now, wait);
    const ownChain = short ? chain : null;
    let id;
    id = nativeSetTimeout.call(window, function () {
      const outer = chainStart;
      chainStart = ownChain;
      if (short) ran(site);
      try {
        return fn.apply(this, args);
      } finally {
        done(pendingTimers, id, outer);
      }
    }, delay);
    if (counted) {
      pendingTimers.add(id);
      sendTimers();
    }
    return id;
  }, nativeSetTimeout, 'setTimeout');
  window.setInterval = disguise(function (fn, delay, ...args) {
    if (typeof fn !== 'function') return nativeSetInterval.apply(window, arguments);
    const wait = Math.max(0, Number(delay) || 0);
    let first = true, id;
    id = nativeSetInterval.call(window, function () {
      if (!first) return fn.apply(this, args);
      first = false;
      try {
        return fn.apply(this, args);
      } finally {
        done(pendingTimers, id, chainStart);
      }
    }, delay);
    if (wait <= SHORT_TIMER) {
      pendingTimers.add(id);
      sendTimers();
    }
    return id;
  }, nativeSetInterval, 'setInterval');
  const clear = (native) => function (id) {
    native.call(window, id);
    if (pendingTimers.delete(id)) sendTimers();
  };
  window.clearTimeout = disguise(clear(nativeClearTimeout), nativeClearTimeout, 'clearTimeout');
  window.clearInterval = disguise(clear(nativeClearInterval), nativeClearInterval, 'clearInterval');
  const REST_QUIET = 150, FLOW_CACHE = 1000, FRAME_GAP = 20, SHIFT_SOURCES = 5;
  let restlessLayoutAt = -1e9, lastShift = -1e9;
  const flowSeen = new WeakMap();
  const inFlow = (el, now) => {
    const c = flowSeen.get(el);
    if (c && now - c.at < FLOW_CACHE) return c.flows;
    let flows = true;
    try {
      const pos = getComputedStyle(el).position;
      flows = pos !== 'absolute' && pos !== 'fixed';
    } catch (e) {}
    flowSeen.set(el, { at: now, flows });
    return flows;
  };
  let shifts = [];
  const noteShifts = (entries) => {
    const now = performance.now();
    for (const e of entries) {
      const sources = e.sources || [];
      if (!sources.length || sources.length >= SHIFT_SOURCES) lastShift = now;
      for (const src of sources) {
        for (const q of [src.previousRect, src.currentRect]) {
          if (q) shifts.push({ at: now, l: q.left, t: q.top, r: q.right, b: q.bottom });
          else lastShift = now;
        }
      }
    }
    if (shifts.length > 200) shifts = shifts.filter((q) => now - q.at < REST_QUIET);
  };
  let shiftObserver = null;
  try {
    if (PerformanceObserver.supportedEntryTypes.includes('layout-shift')) {
      shiftObserver = new PerformanceObserver((list) => noteShifts(list.getEntries()));
      shiftObserver.observe({ type: 'layout-shift' });
    }
  } catch (e) { shiftObserver = null; }
  const PAINT_ONLY = /color$|^(opacity|fill|stroke|boxShadow|textShadow|filter|backdropFilter|offset|easing|composite|computedOffset)$/i;
  const OWN_BOX_ONLY = /^(transform|translate|rotate|scale|perspective|transformOrigin|clipPath|visibility)$/;
  const animates = (anim, spare) => {
    try {
      for (const f of anim.effect.getKeyframes()) {
        for (const prop in f) if (!PAINT_ONLY.test(prop) && !(spare && spare.test(prop))) return true;
      }
      return false;
    } catch (e) { return true; }
  };
  const above = (e) => {
    if (e.assignedSlot) return e.assignedSlot;
    const p = e.parentNode;
    if (!p) return null;
    if (p.nodeType === 11) return p.host || null;
    return p.nodeType === 1 ? p : null;
  };
  const opaqueHost = (e) => {
    try { return e.shadowRoot === null && e.localName.includes('-') && !!customElements.get(e.localName); } catch (x) { return true; }
  };
  tb.unrest = (i, l, t, r, b) => {
    const el = tb.nodes[i];
    if (!shiftObserver) return 'no layout-shift reports';
    if (!el || !el.isConnected || el.ownerDocument !== document) return 'not in the page';
    try { noteShifts(shiftObserver.takeRecords()); } catch (e) { return 'no layout-shift reports'; }
    const now = performance.now();
    if (now - lastSolid < FRAME_GAP) return 'the page just changed';
    if (now - lastShift < REST_QUIET) return 'much of the layout shifted';
    if (now - restlessLayoutAt < REST_QUIET) return 'a restless element changes the layout';
    const box = el.getBoundingClientRect();
    const near = (a, c) => Math.abs(Math.round(a) - c) <= 1;
    if (!near(box.left, l) || !near(box.top, t) || !near(box.right, r) || !near(box.bottom, b)) return 'moved';
    for (const q of shifts) {
      if (now - q.at < REST_QUIET && q.l < box.right && q.r > box.left && q.t < box.bottom && q.b > box.top) return 'layout shifted';
    }
    const chain = new Set();
    for (let e = el; e; e = above(e)) {
      if (restless.has(e)) return 'restless';
      if (e !== el && opaqueHost(e)) return 'inside a custom element with no open shadow tree';
      const tag = e.localName;
      if (tag === 'svg' || tag === 'marquee') return 'inside ' + tag;
      chain.add(e);
    }
    try {
      for (const e of chain) {
        for (const anim of e.getAnimations()) if (anim.playState === 'running' && animates(anim, null)) return 'animated';
      }
      for (const anim of document.getAnimations()) {
        if (anim.playState !== 'running') continue;
        const target = anim.effect && anim.effect.target;
        if (chain.has(target) || (target && !inFlow(target, now))) continue;
        if (animates(anim, OWN_BOX_ONLY)) return 'an animation changes the layout';
      }
    } catch (x) { return 'animations unknown'; }
    return '';
  };

  const onMutations = (records) => {
    const now = performance.now();
    let solid = false;
    for (const r of records) {
      let t = r.target;
      if (t && t.nodeType === 3) t = t.parentNode;
      if (!t || typeof t !== 'object') { solid = true; continue; }
      if (restless.has(t)) {
        if (r.type === 'attributes' && inFlow(t, now)) restlessLayoutAt = now;
        continue;
      }
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
  const runKey = (kind, e) => kind + ':' + (e.pseudoElement || '') + ':' + (e.propertyName || e.animationName || '');
  const start = (kind, e) => {
    const el = e.target;
    if (el && typeof el === 'object') {
      let keys = running.get(el);
      if (!keys) running.set(el, (keys = new Set()));
      keys.add(runKey(kind, e));
    }
    notify();
  };
  const end = (kind, e) => {
    const keys = running.get(e.target);
    if (keys) {
      keys.delete(runKey(kind, e));
      if (!keys.size) running.delete(e.target);
    }
    notify();
  };
  const colorOnly = (e) => /(^|-)color$|^(fill|stroke|box-shadow|text-shadow)$/.test(e.propertyName || '');
  window.addEventListener('transitionrun', (e) => { if (!colorOnly(e)) start('t', e); }, true);
  window.addEventListener('transitionend', (e) => { if (!colorOnly(e)) end('t', e); }, true);
  window.addEventListener('transitioncancel', (e) => { if (!colorOnly(e)) end('t', e); }, true);
  window.addEventListener('animationstart', (e) => {
    try { if (getComputedStyle(e.target).animationIterationCount === 'infinite') return; } catch (x) {}
    start('a', e);
  }, true);
  window.addEventListener('animationend', (e) => end('a', e), true);
  window.addEventListener('animationcancel', (e) => end('a', e), true);

  const SKIP = new Set(['noscript', 'script', 'br', 'img', 'svg', 'g', 'path', 'style', 'head', 'meta', 'link', 'title', 'template']);
  const TEXT_INPUTS = new Set(['', 'text', 'email', 'password', 'search', 'tel', 'url', 'number', 'date', 'datetime-local', 'month', 'time', 'week']);
  const CLICK_ROLES = new Set(['button', 'link', 'checkbox', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab', 'switch', 'option', 'radio', 'treeitem']);
  const CHECK_ROLES = new Set(['checkbox', 'switch', 'radio', 'menuitemcheckbox', 'menuitemradio']);

  const str = (v) => (typeof v === 'string' ? v : '');
  const clean = (s) => s.replace(/[\n\t]/g, ' ').replace(/\s+/g, ' ').trim();

  const visuallyHidden = (el, cs, r) => {
    if (cs.clip && cs.clip !== 'auto') return true;
    if (cs.clipPath && cs.clipPath !== 'none' && /inset\(\s*(50%|100%)/.test(cs.clipPath)) return true;
    if ((r.width <= 1 || r.height <= 1) && /(hidden|clip)/.test(cs.overflowX + ' ' + cs.overflowY)) return true;
    return parseFloat(cs.opacity) === 0;
  };
  const shownControl = (el) => {
    const r = el.getBoundingClientRect();
    if (r.width < 8 || r.height < 8) return false;
    if (el.checkVisibility && !el.checkVisibility({ opacityProperty: true, visibilityProperty: true })) return false;
    try { return !visuallyHidden(el, getComputedStyle(el), r); } catch (e) { return true; }
  };
  const CONTROL_TAGS = new Set(['INPUT', 'SELECT', 'TEXTAREA', 'BUTTON', 'SCRIPT', 'STYLE', 'TEMPLATE']);
  const hiddenText = (el) => {
    if (CONTROL_TAGS.has(el.tagName) || el.hidden || el.getAttribute('aria-hidden') === 'true') return true;
    if (el.checkVisibility && !el.checkVisibility({ visibilityProperty: true })) return true;
    try { return visuallyHidden(el, getComputedStyle(el), el.getBoundingClientRect()); } catch (e) { return false; }
  };
  const labelsText = (el) => {
    let t = '';
    const walk = (n) => {
      for (const c of n.childNodes) {
        if (c.nodeType === 3) t += c.textContent + ' ';
        else if (c.nodeType === 1 && !hiddenText(c)) walk(c);
      }
    };
    for (const l of el.labels || []) walk(l);
    return clean(t);
  };
  const nameOf = (el) => {
    const by = el.getAttribute('aria-labelledby');
    if (by) {
      const t = clean(by.split(/\s+/).map((id) => {
        const r = el.getRootNode && el.getRootNode();
        const l = (r && r.getElementById ? r.getElementById(id) : null) || document.getElementById(id);
        return l ? l.textContent : '';
      }).join(' '));
      if (t) return t;
    }
    return el.getAttribute('aria-label') || labelsText(el);
  };

  const textOf = (el, tag) => {
    switch (tag) {
      case 'input': {
        const type = (el.type || '').toLowerCase();
        if (type === 'checkbox' || type === 'radio' || type === 'range') return shownControl(el) ? nameOf(el) : '';
        return el.value || el.placeholder || el.getAttribute('aria-label') || '';
      }
      case 'textarea':
        return el.value || el.placeholder || el.getAttribute('aria-label') || '';
      case 'select':
        return Array.from(el.selectedOptions || []).map((o) => o.text).join(', ');
    }
    let t = '';
    for (const n of el.childNodes) if (n.nodeType === 3) t += n.textContent;
    return clean(t);
  };

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

  tb.snapshot = (hits) => {
    const out = [];
    const nodes = [];
    let active = document.activeElement;
    while (active && active.shadowRoot && active.shadowRoot.activeElement) active = active.shadowRoot.activeElement;
    const visit = (el, depth, ox, oy) => {
      const tag = el.tagName.toLowerCase();
      if (SKIP.has(tag)) return;
      if (tag === 'iframe') {
        const r = el.getBoundingClientRect();
        const cs = getComputedStyle(el);
        const cx = r.x + (parseFloat(cs.borderLeftWidth) || 0) + (parseFloat(cs.paddingLeft) || 0);
        const cy = r.y + (parseFloat(cs.borderTopWidth) || 0) + (parseFloat(cs.paddingTop) || 0);
        let doc = null;
        try { doc = el.contentDocument; } catch (e) {}
        if (doc && doc.body) {
          visit(doc.body, depth, ox + cx, oy + cy);
        } else if (el.src) {
          out.push({ d: depth, b: [Math.round(r.left + ox), Math.round(r.top + oy), Math.round(r.right + ox), Math.round(r.bottom + oy)],
            c: 'iframe', f: 4096 | 2, frame: { src: el.src, x: Math.round(cx + ox), y: Math.round(cy + oy) } });
          nodes.push(el);
        }
        return;
      }
      if (tag === 'option' && !(el.parentElement && el.parentElement.matches(':focus-within'))) return;
      if (el.checkVisibility && !el.checkVisibility({ visibilityProperty: true, opacityProperty: false })) return;
      const r = el.getBoundingClientRect();
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
      if (el.selected === true || el.getAttribute('aria-selected') === 'true' || el.getAttribute('aria-pressed') === 'true') f |= 32;
      if (type === 'checkbox' || type === 'radio' || CHECK_ROLES.has(role)) f |= 64;
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
      else if (role === 'slider') n.v = el.getAttribute('aria-valuetext') || el.getAttribute('aria-valuenow') || '';
      if (role) n.r = role;
      else if (type === 'range') n.r = 'slider';
      out.push(n);
      nodes.push(el);
      if (el.shadowRoot) {
        watchShadow(el.shadowRoot);
        for (const c of el.shadowRoot.children) visit(c, depth + 1, ox, oy);
      }
      for (const c of el.children) visit(c, depth + 1, ox, oy);
    };
    if (document.body) visit(document.body, 0, 0, 0);
    tb.nodes = nodes;
    try { modalPass(out, nodes); } catch (e) {}
    if (hits !== false) {
      try { hitPass(out, nodes); } catch (e) {}
    }
    return JSON.stringify({ w: window.innerWidth, h: window.innerHeight, url: location.href, title: document.title, loading: document.readyState !== 'complete', nodes: out });
  };

  const up = (n) => {
    const p = n.parentNode;
    if (!p) return null;
    if (p.nodeType === 1) return p;
    if (p.host) return p.host;
    if (p.nodeType === 9) {
      try { return p.defaultView ? p.defaultView.frameElement : null; } catch (e) { return null; }
    }
    return null;
  };
  const inside = (a, b) => {
    for (let n = a; n; n = up(n)) if (n === b) return true;
    return false;
  };
  const modalPass = (out, nodes) => {
    const W = window.innerWidth, H = window.innerHeight;
    let top = null, topIsNative = false;
    for (let i = 0; i < nodes.length; i++) {
      const el = nodes[i], b = out[i].b;
      if (!el || out[i].frame || el.ownerDocument !== document) continue;
      if (b[2] <= Math.max(b[0], 0) || b[3] <= Math.max(b[1], 0) || b[0] >= W || b[1] >= H) continue;
      let native = false;
      if (el.tagName === 'DIALOG' && el.open) {
        try { native = el.matches(':modal'); } catch (e) {}
      }
      if (!native && el.getAttribute('aria-modal') !== 'true') continue;
      out[i].mo = 1;
      if (native || !topIsNative) { top = el; topIsNative = native; }
    }
    if (!top) return;
    const zMemo = new Map();
    const zOf = (el) => {
      if (!el || el.nodeType !== 1 || (el !== top && inside(top, el))) return null;
      if (zMemo.has(el)) return zMemo.get(el);
      let z = zOf(up(el));
      if (!z) {
        try {
          const cs = getComputedStyle(el);
          if (cs.position !== 'static' && cs.zIndex !== 'auto') z = { z: +cs.zIndex, at: el };
        } catch (e) {}
      }
      zMemo.set(el, z);
      return z;
    };
    const topZ = topIsNative ? null : zOf(top);
    const over = (z) => {
      const tz = topZ ? topZ.z : 0;
      if (z.z !== tz) return z.z > tz;
      const at = topZ ? topZ.at : top;
      return !!(at.compareDocumentPosition(z.at) & Node.DOCUMENT_POSITION_FOLLOWING);
    };
    const above = new Map();
    const isAbove = (el) => {
      if (!el || el.nodeType !== 1 || inside(top, el)) return false;
      if (above.has(el)) return above.get(el);
      let a = false;
      try { a = el.matches(':popover-open'); } catch (e) {}
      if (!a && !topIsNative) {
        const z = zOf(el);
        a = !!z && z.at === el && over(z);
      }
      a = a || isAbove(up(el));
      above.set(el, a);
      return a;
    };
    for (let i = 0; i < nodes.length; i++) {
      const el = nodes[i];
      if (el && !inside(el, top) && !inside(top, el) && !isAbove(el)) out[i].um = 1;
    }
  };

  const frameBox = (el) => {
    const r = el.getBoundingClientRect();
    let cs = null;
    try { cs = (el.ownerDocument.defaultView || window).getComputedStyle(el); } catch (e) {}
    return {
      x: r.x + (cs ? (parseFloat(cs.borderLeftWidth) || 0) + (parseFloat(cs.paddingLeft) || 0) : 0),
      y: r.y + (cs ? (parseFloat(cs.borderTopWidth) || 0) + (parseFloat(cs.paddingTop) || 0) : 0),
    };
  };
  const docOffset = (doc, memo) => {
    if (doc === document) return { x: 0, y: 0 };
    if (memo && memo.has(doc)) return memo.get(doc);
    let o = { x: 0, y: 0 };
    try {
      const fe = doc.defaultView && doc.defaultView.frameElement;
      if (fe) {
        const outer = docOffset(fe.ownerDocument, memo), b = frameBox(fe);
        o = { x: outer.x + b.x, y: outer.y + b.y };
      }
    } catch (e) {}
    if (memo) memo.set(doc, o);
    return o;
  };
  const hitAt = (x, y) => {
    let h = document.elementFromPoint(x, y), ox = 0, oy = 0;
    for (let k = 0; h && k < 32; k++) {
      if (h.shadowRoot) {
        const inner = h.shadowRoot.elementFromPoint(x - ox, y - oy);
        if (inner && inner !== h) { h = inner; continue; }
      }
      if (h.tagName === 'IFRAME' || h.tagName === 'FRAME') {
        let doc = null;
        try { doc = h.contentDocument; } catch (e) {}
        if (doc && doc.documentElement) {
          const b = frameBox(h);
          const inner = doc.elementFromPoint(x - ox - b.x, y - oy - b.y);
          if (inner) { ox += b.x; oy += b.y; h = inner; continue; }
        }
      }
      break;
    }
    return h;
  };

  tb.frameAt = (x, y) => {
    const h = hitAt(x, y);
    if (!h || (h.tagName !== 'IFRAME' && h.tagName !== 'FRAME')) return null;
    let doc = null;
    try { doc = h.contentDocument; } catch (e) {}
    if (doc && doc.documentElement) return null;
    const o = docOffset(h.ownerDocument), b = frameBox(h);
    tb.frameOrigin = [Math.round(o.x + b.x), Math.round(o.y + b.y)];
    return h;
  };

  tb.covered = (i, x, y) => {
    const t = tb.nodes[i];
    if (!t || !t.isConnected) return '';
    if (t.tagName === 'IFRAME' || t.tagName === 'FRAME') {
      const b = covers([]).box(t);
      if (x < b[0] || x >= b[2] || y < b[1] || y >= b[3]) return '';
    }
    const h = hitAt(x, y);
    if (!h || inside(h, t) || inside(t, h)) return '';
    const name = h.id ? '#' + h.id : h.tagName.toLowerCase() + (h.className && typeof h.className === 'string' ? '.' + h.className.split(/\s+/)[0] : '');
    return name || '?';
  };

  const isect = (a, b) => {
    const r = [Math.max(a[0], b[0]), Math.max(a[1], b[1]), Math.min(a[2], b[2]), Math.min(a[3], b[3])];
    return r[2] <= r[0] || r[3] <= r[1] ? null : r;
  };
  const mid = (a, b) => Math.trunc((a + b) / 2);

  const covers = (nodes) => {
    const index = new Map();
    nodes.forEach((el, i) => index.set(el, i));
    const memo = new Map(), offsets = new Map();
    const hit = (x, y) => {
      const k = x * 100003 + y;
      if (!memo.has(k)) memo.set(k, hitAt(x, y));
      return memo.get(k);
    };
    const box = (el) => {
      const r = el.getBoundingClientRect(), o = docOffset(el.ownerDocument, offsets);
      return [Math.round(r.left + o.x), Math.round(r.top + o.y), Math.round(r.right + o.x), Math.round(r.bottom + o.y)];
    };
    const ref = (el) => {
      const i = index.get(el);
      return i !== undefined ? { i } : { i: -1, b: box(el), c: el.tagName.toLowerCase() };
    };
    const coverOf = (t, h, p, x, y) => {
      let by = h;
      const l = [];
      for (let n = h; n && !inside(t, n); n = up(n)) {
        let cs = null;
        try { cs = (n.ownerDocument.defaultView || window).getComputedStyle(n); } catch (e) {}
        const takes = !cs || cs.pointerEvents !== 'none';
        if (takes && n !== h) {
          const b = box(n);
          if (x >= b[0] && x < b[2] && y >= b[1] && y < b[3]) by = n;
        }
        let k = '';
        try { if (n.matches(':modal')) k = 'modal'; } catch (e) {}
        if (!k && n.getAttribute && n.getAttribute('aria-modal') === 'true') k = 'modal';
        if (!k && takes && (cs.position === 'fixed' || cs.position === 'sticky')) k = cs.position;
        if (k) l.push(Object.assign(ref(n), { k }));
      }
      const c = { p, x, y, by: ref(by) };
      if (l.length) c.l = l;
      return c;
    };
    return { hit, box, coverOf };
  };

  const hitPass = (out, nodes) => {
    const W = window.innerWidth, H = window.innerHeight;
    const parent = new Array(out.length);
    const stack = [];
    for (let i = 0; i < out.length; i++) {
      while (stack.length && out[stack[stack.length - 1]].d >= out[i].d) stack.pop();
      parent[i] = stack.length ? stack[stack.length - 1] : -1;
      stack.push(i);
    }
    const { hit, coverOf } = covers(nodes);
    for (let i = 0; i < out.length; i++) {
      const n = out[i], t = nodes[i];
      if (n.frame || !t) continue;
      let v = isect(n.b, [0, 0, W, H]);
      for (let p = parent[i]; p >= 0 && v; p = parent[p]) {
        if (out[p].f & 8) {
          const c = isect(v, out[p].b);
          if (c) v = c;
        }
      }
      if (!v) continue;
      const cx = mid(v[0], v[2]), cy = mid(v[1], v[3]);
      const pts = [[cx, cy],
        [mid(v[0], cx), mid(v[1], cy)], [mid(cx, v[2]), mid(v[1], cy)],
        [mid(v[0], cx), mid(cy, v[3])], [mid(cx, v[2]), mid(cy, v[3])]];
      let cv = null, clear = 0;
      for (let p = 0; p < pts.length; p++) {
        const [x, y] = pts[p];
        const h = hit(x, y);
        if (!h || inside(h, t) || inside(t, h)) {
          if (p > 0 && ++clear >= 2) break;
          continue;
        }
        (cv || (cv = [])).push(coverOf(t, h, p, x, y));
        if (p === 0) break;
      }
      if (cv) n.cv = cv;
    }
  };

  tb.frameHits = (req) => {
    const { hit, box, coverOf } = covers(tb.nodes);
    const boxes = new Map();
    return JSON.stringify(req.map(([f, pts]) => {
      const t = tb.nodes[f];
      if (!t || !t.isConnected) return null;
      if (!boxes.has(t)) boxes.set(t, box(t));
      const b = boxes.get(t);
      let cv = null;
      pts.forEach(([x, y], p) => {
        if (x < b[0] || x >= b[2] || y < b[1] || y >= b[3]) return;
        const h = hit(x, y);
        if (!h || inside(h, t) || inside(t, h)) return;
        (cv || (cv = [])).push(coverOf(t, h, p, x, y));
      });
      return cv;
    }));
  };

  tb.css = (sel) => {
    const set = new Set(document.querySelectorAll(sel));
    const idx = [];
    tb.nodes.forEach((el, i) => { if (set.has(el)) idx.push(i); });
    return JSON.stringify(idx);
  };
})();

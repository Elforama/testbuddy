// Test Buddy's in-page helper. Installed in every document before its own scripts run.
//
// 1. Change events: a MutationObserver plus input/scroll/animation listeners call the
//    __tbNotify binding, which reaches the host as a CDP event. The host never polls the page.
// 2. Snapshots: window.__tb.snapshot() returns the visible elements in pre-order with depth,
//    using the flow format's web rules for text and ids, and what covers each one on screen
//    (the browser's own hit test, in the same evaluation: see hitPass).
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
  // Running transitions and animations, per element: element -> Set of 'kind:pseudo:name'.
  // An element removed mid-run gets its cancel event while detached, where it never reaches
  // the window (issue #75): one that is no longer in the page is dropped when counting.
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
    try { window.__tbNotify && window.__tbNotify(JSON.stringify({ a: runningCount(), s: solid ? 1 : 0 })); } catch (e) {}
  };
  tb.notify = notify;

  // Short timers (issue #312): a setTimeout due within SHORT_TIMER counts as pending work until
  // its callback has run, so a page that renders in two phases (a click, then a timer that
  // renders the rest; a debounced search) is not settled between them. So does a setInterval's
  // first tick when it is due that soon (a one-shot render from an interval). The host is told
  // the pending count ({k:'t'}), which is not a change, and lets it hold a settle only for a
  // while after the action (softCap). A requestIdleCallback is not counted: headless Chrome
  // can leave one unrun for good on a page that drew nothing since, so it would hold every
  // settle for the cap.
  // Not counted: string callbacks, long timers, an interval's later ticks, and timers that keep
  // re-arming on their own (a countdown, a poller, a clock):
  // - a chain: a timer set from a counted timer's callback, or from the microtasks it ran (an
  //   await-sleep loop), once the chain is CHAIN_MAX old;
  // - a recurring call site: one set again after its last callback ran, with no input from the
  //   test in between, for RECUR_MAX. Whatever re-arms it (a MessageChannel, an effect after
  //   a render, a requestAnimationFrame, a fetch's then, an interval), a countdown is so known
  //   after a second or two, while a debounce, re-armed by each key, is never taken for one.
  const SHORT_TIMER = 1000, CHAIN_MAX = 1000, RECUR_MAX = 1000;
  const nativeSetTimeout = window.setTimeout, nativeClearTimeout = window.clearTimeout;
  const nativeSetInterval = window.setInterval, nativeClearInterval = window.clearInterval;
  const pendingTimers = new Set(); // setTimeout and setInterval ids (one id pool)
  let chainStart = null; // while a timer's callback (and its microtasks) runs: when its chain began
  let sentTimers = 0;
  // The count goes to the host once per microtask checkpoint, if it changed: a page that sets
  // and clears thousands of timers in one task (a debounce in a loop) sends nothing for them.
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
  // Call sites: when each last ran and since when it recurs. A site is the stack from the
  // caller of setTimeout up to its first frame that is not shared code. A frame is shared
  // (a page's own setTimeout wrapper: Sentry's, Datadog's, Zone.js's; a sleep or debounce
  // helper) once it has been seen called from two different frames: then its caller is part
  // of the site too, and so on up. Every app timer reaching tb through a wrapper's one line
  // is so told apart by where the app set it, not taken for one site that recurs. When every
  // frame is shared, the whole stack is the site if it is whole (the frame the browser called
  // is in it); if it was cut off, or there is no stack, the site is '': it cannot be told
  // apart, and the timer is always counted.
  const STACK_LIMIT = 12;
  const sites = new Map();
  const callers = new Map(); // frame -> { up: the frame first seen calling it, shared }
  // The stack is taken as V8's call-site objects (prepareStackTrace set for the capture, and
  // the stack's depth capped by stackTraceLimit), not as text: no frame is formatted, and
  // the page's own Error.prepareStackTrace (an error reporter's) does not run. A frame is
  // named by its script, line and column, worked out only as far up as the walk goes.
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
    // This function, tb's setTimeout, then the frame that called it and its callers.
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
  // recurs: the call site was set again after its last callback ran, with no input since, for
  // RECUR_MAX. Re-armed after an input (a debounce), it starts over. A site found recurring
  // stays so, like a restless element: a poller re-armed right after a tap is still a poller
  // (else every tap had it counted, and held, for RECUR_MAX again). But the same component
  // code can set its timer in the background and for a tap (a banner it rotates, and the
  // tap's result it mounts): one site. So a recurring site's timer set within AFTER_INPUT of
  // the test's input, due within ONE_SHOT (the 120 ms that tb's quiet window once was),
  // still counts: what main caught is still caught, at a cost of at most about AFTER_INPUT +
  // ONE_SHOT per input for a page with a fast poller.
  const AFTER_INPUT = 100, ONE_SHOT = 120;
  const recurs = (site, now, wait) => {
    if (site === '') return false; // a site that cannot be told apart: counted
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
  // done ends a pending callback. It is reported from a microtask queued once the callback
  // returned: the mutation observer's delivery for what the callback changed was queued
  // before it, so that change reaches the host first. A render the callback's microtasks make
  // later still can arrive after it: the host waits a quiet window after a timer ends for those.
  const done = (set, id, outer) => queueMicrotask(() => {
    chainStart = outer;
    if (set.delete(id)) sendTimers();
  });
  // Wrappers look native: the same name and length, and Function.prototype.toString shows the
  // native source for them (and for itself), so a page's native-function check still passes.
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
    const ownChain = short ? chain : null; // a long timer's callback starts a chain of its own
    let id;
    id = nativeSetTimeout.call(window, function () {
      const outer = chainStart;
      chainStart = ownChain;
      if (short) ran(site);
      try {
        return fn.apply(this, args);
      } finally {
        done(pendingTimers, id, outer); // the chain is kept through the callback's microtasks
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
    if (wait <= SHORT_TIMER) { // its first tick only: the later ones are a clock's
      pendingTimers.add(id);
      sendTimers();
    }
    return id;
  }, nativeSetInterval, 'setInterval');
  // clearTimeout and clearInterval each clear the other's ids too (one pool).
  const clear = (native) => function (id) {
    native.call(window, id);
    if (pendingTimers.delete(id)) sendTimers();
  };
  window.clearTimeout = disguise(clear(nativeClearTimeout), nativeClearTimeout, 'clearTimeout');
  window.clearInterval = disguise(clear(nativeClearInterval), nativeClearInterval, 'clearInterval');
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
  // kind is 't' (a transition, keyed by property) or 'a' (an animation, keyed by name).
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
  // A color fading (a hover, a completed item's text going gray) moves and hides nothing, so a
  // tap during it lands the same: it does not hold the page up. Opacity does: a fade-in hides.
  const colorOnly = (e) => /(^|-)color$|^(fill|stroke|box-shadow|text-shadow)$/.test(e.propertyName || '');
  window.addEventListener('transitionrun', (e) => { if (!colorOnly(e)) start('t', e); }, true);
  window.addEventListener('transitionend', (e) => { if (!colorOnly(e)) end('t', e); }, true);
  window.addEventListener('transitioncancel', (e) => { if (!colorOnly(e)) end('t', e); }, true);
  window.addEventListener('animationstart', (e) => {
    // An endless animation (a spinner) would keep the page busy forever: count only finite ones.
    try { if (getComputedStyle(e.target).animationIterationCount === 'infinite') return; } catch (x) {}
    start('a', e);
  }, true);
  window.addEventListener('animationend', (e) => end('a', e), true);
  window.addEventListener('animationcancel', (e) => end('a', e), true);

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

  // hits: false leaves out the hit test (an embedded WebView read for its text alone).
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
    if (hits !== false) {
      try { hitPass(out, nodes); } catch (e) {}
    }
    return JSON.stringify({ w: window.innerWidth, h: window.innerHeight, url: location.href, title: document.title, loading: document.readyState !== 'complete', nodes: out });
  };

  // ---- the hit test: what a click reaches ----
  // The browser's own hit test is the truth about covers on the web: a page's element order is
  // not its drawing order (z-index, fixed layers, the top layer, transforms), and only the
  // browser knows what an element with pointer-events: none lets through, that a
  // visibility: hidden one is not hit, or that an opacity: 0 one still is.

  // up is an element's parent across shadow roots (its host) and same-origin frames (the
  // frame element).
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
  // inside: a is b or in it (across shadow roots and same-origin frames).
  const inside = (a, b) => {
    for (let n = a; n; n = up(n)) if (n === b) return true;
    return false;
  };
  // frameBox is where a frame element's own (0, 0) is, in its document's viewport.
  const frameBox = (el) => {
    const r = el.getBoundingClientRect();
    let cs = null;
    try { cs = (el.ownerDocument.defaultView || window).getComputedStyle(el); } catch (e) {}
    return {
      x: r.x + (cs ? (parseFloat(cs.borderLeftWidth) || 0) + (parseFloat(cs.paddingLeft) || 0) : 0),
      y: r.y + (cs ? (parseFloat(cs.borderTopWidth) || 0) + (parseFloat(cs.paddingTop) || 0) : 0),
    };
  };
  // docOffset is where a (same-origin frame's) document's viewport sits in the page's.
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
  // hitAt is the element a click at (x, y) of the page reaches: down through open shadow roots
  // and same-origin frames.
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

  // frameAt is the cross-origin frame element a click at (x, y) reaches (down through shadow
  // roots and same-origin frames), or null; tb.frameOrigin is then where the frame's own (0, 0)
  // is, in this document's viewport (issue #326: the host scrolls such a frame in its session).
  tb.frameAt = (x, y) => {
    const h = hitAt(x, y);
    if (!h || (h.tagName !== 'IFRAME' && h.tagName !== 'FRAME')) return null;
    let doc = null;
    try { doc = h.contentDocument; } catch (e) {}
    if (doc && doc.documentElement) return null; // same-origin: hitAt went into it
    const o = docOffset(h.ownerDocument), b = frameBox(h);
    tb.frameOrigin = [Math.round(o.x + b.x), Math.round(o.y + b.y)];
    return h;
  };

  // What a click at (x, y) would reach instead of node i of the last snapshot: '' when it
  // reaches the node, something inside it, or something it is inside of (a label around a
  // field, a parent that takes the click for an element with pointer-events: none); else a
  // short name of what covers it (a veil, a dialog's backdrop, a toast).
  tb.covered = (i, x, y) => {
    const t = tb.nodes[i];
    if (!t || !t.isConnected) return '';
    // A cross-origin frame's element is checked against its frame element (issue #315): a
    // point outside the frame's box is not the element's (the frame clips it), as frameHits.
    if (t.tagName === 'IFRAME' || t.tagName === 'FRAME') {
      const b = covers([]).box(t);
      if (x < b[0] || x >= b[2] || y < b[1] || y >= b[3]) return '';
    }
    const h = hitAt(x, y);
    if (!h || inside(h, t) || inside(t, h)) return '';
    const name = h.id ? '#' + h.id : h.tagName.toLowerCase() + (h.className && typeof h.className === 'string' ? '.' + h.className.split(/\s+/)[0] : '');
    return name || '?';
  };

  // ---- proof of rest (issue #369) ----
  // Properties whose animation only repaints: they move nothing, the element they run on included.
  const PAINT_ONLY = new Set(['opacity', 'color', 'background', 'background-color', 'background-image',
    'background-position', 'border-color', 'border-top-color', 'border-right-color', 'border-bottom-color',
    'border-left-color', 'outline-color', 'box-shadow', 'text-shadow', 'filter', 'backdrop-filter', 'fill',
    'stroke', 'stroke-dashoffset', 'caret-color', 'text-decoration-color', 'visibility', 'clip-path']);
  // Properties that move the element they run on, and nothing else (no layout).
  const MOVES_ITSELF = new Set(['transform', 'translate', 'rotate', 'scale', 'offset-distance', 'offset-path',
    'offset-rotate', 'offset-anchor', 'offset-position']);
  const kebab = (p) => p.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase());
  const animatedProps = (a) => {
    if (typeof a.transitionProperty === 'string') return [a.transitionProperty];
    const out = [];
    for (const k of a.effect.getKeyframes()) {
      for (const p in k) if (p !== 'offset' && p !== 'computedOffset' && p !== 'easing' && p !== 'composite') out.push(kebab(p));
    }
    return out;
  };
  // animating: an animation or transition that runs (or waits out its delay) can move t: one on
  // t or on what holds it (its ancestors, across shadow roots and same-origin frames), whatever
  // it animates; elsewhere, one that animates layout (a height, a margin: what is beside or
  // above t moves it), or a finite one that moves its own element (a drawer sliding in over t).
  // An endless one elsewhere that only repaints or turns its own element (a spinner) moves
  // nothing of t. Anything it cannot tell counts as moving.
  const animating = (t) => {
    const holders = new Set(), roots = new Set();
    for (let n = t; n; n = up(n)) {
      holders.add(n);
      const r = n.getRootNode && n.getRootNode();
      if (r && r.getAnimations) roots.add(r);
    }
    const seen = new Set();
    for (const root of roots) {
      for (const a of root.getAnimations()) {
        if (seen.has(a)) continue;
        seen.add(a);
        if (a.playState !== 'running' && !a.pending) continue;
        const e = a.effect;
        if (!e || !e.target || holders.has(e.target)) return true;
        const props = animatedProps(a);
        if (props.length === 0 || props.includes('all')) return true;
        if (props.every((p) => PAINT_ONLY.has(p))) continue;
        if (!props.every((p) => PAINT_ONLY.has(p) || MOVES_ITSELF.has(p))) return true; // layout
        if (e.getComputedTiming().endTime !== Infinity) return true;
      }
    }
    return false;
  };
  // still(i, l, t, r, b): node i of the last snapshot, read at [l, t, r, b], is at rest now: its
  // box (exact, not rounded) is still where the read had it and the same two frames running,
  // and no animation that can move it runs (animating). A promise of true or false; false when
  // the frames do not come within 250 ms (a page in the background draws none).
  tb.still = (i, l, tp, r, b) => {
    const t = tb.nodes[i];
    if (!t || !t.isConnected) return false;
    const at = () => {
      const c = t.getBoundingClientRect(), o = docOffset(t.ownerDocument);
      return [c.left + o.x, c.top + o.y, c.right + o.x, c.bottom + o.y];
    };
    const same = (x, y) => x.every((v, k) => v === y[k]);
    let a0;
    try {
      a0 = at();
      if (Math.round(a0[0]) !== l || Math.round(a0[1]) !== tp || Math.round(a0[2]) !== r || Math.round(a0[3]) !== b) return false;
      if (animating(t)) return false;
    } catch (e) {
      return false;
    }
    return new Promise((done) => {
      const timer = setTimeout(() => done(false), 250);
      requestAnimationFrame(() => {
        let a1 = null;
        try { a1 = at(); } catch (e) {}
        requestAnimationFrame(() => {
          clearTimeout(timer);
          let ok = false;
          try { ok = !!a1 && t.isConnected && same(a0, a1) && same(a1, at()) && !animating(t); } catch (e) {}
          done(ok);
        });
      });
    });
  };

  // A rect is [l, t, r, b]; null is empty (as ui.Rect.Empty).
  const isect = (a, b) => {
    const r = [Math.max(a[0], b[0]), Math.max(a[1], b[1]), Math.min(a[2], b[2]), Math.min(a[3], b[3])];
    return r[2] <= r[0] || r[3] <= r[1] ? null : r;
  };
  const mid = (a, b) => Math.trunc((a + b) / 2);

  // covers is what the hit tests of one read share over the elements of a snapshot (nodes): the
  // hit at each point (memoized), and coverOf, the cover a click at a point reaches instead of
  // an element t (see hitPass).
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
          // By is an element whose own box holds the point: not a host or wrapper of no size
          // (a shadow host, a portal's root) that only holds it in the tree.
          const b = box(n);
          if (x >= b[0] && x < b[2] && y >= b[1] && y < b[3]) by = n;
        }
        let k = '';
        try { if (n.matches(':modal')) k = 'modal'; } catch (e) {}
        if (!k && n.getAttribute && n.getAttribute('aria-modal') === 'true') k = 'modal';
        // A see-through fixed frame (pointer-events: none) holding a floating button is no
        // layer over the content: only what it draws takes clicks.
        if (!k && takes && (cs.position === 'fixed' || cs.position === 'sticky')) k = cs.position;
        if (k) l.push(Object.assign(ref(n), { k }));
      }
      const c = { p, x, y, by: ref(by) };
      if (l.length) c.l = l;
      return c;
    };
    return { hit, box, coverOf };
  };

  // hitPass annotates each element of a snapshot that shows on screen with what covers it
  // (n.cv), from one hit test per sample point, in the same evaluation as the snapshot: the
  // points package occlusion samples (the middle of the element's visible part, then the
  // middles of its quarters, only while 3 of the 4 can still be covered), computed as it
  // computes them. A cover is the branch over the element that a click there reaches, up to
  // the nearest element holding both: by is its outermost element that takes clicks there (a
  // drawer, a backdrop, a header; not a see-through wrapper with pointer-events: none, nor a
  // host whose own box does not hold the point), and
  // l lists the elements of the branch that may make it a modal layer: one shown modal (a
  // <dialog> opened with showModal, in the top layer) or marked aria-modal="true" ("modal"),
  // or a fixed or sticky one that takes clicks ("fixed", "sticky": package occlusion says
  // whether it holds the element and is no bar). An element is given by its index in the snapshot, or, when it is
  // not listed, by its box and tag.
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
      if (n.frame || !t) continue; // a cross-origin frame: its own snapshot tests its elements (and frameHits the page over them)
      let v = isect(n.b, [0, 0, W, H]);
      for (let p = parent[i]; p >= 0 && v; p = parent[p]) {
        if (out[p].f & 8) {
          const c = isect(v, out[p].b);
          if (c) v = c;
        }
      }
      if (!v) continue; // not on screen: nothing to hit
      const cx = mid(v[0], v[2]), cy = mid(v[1], v[3]);
      const pts = [[cx, cy],
        [mid(v[0], cx), mid(v[1], cy)], [mid(cx, v[2]), mid(v[1], cy)],
        [mid(v[0], cx), mid(cy, v[3])], [mid(cx, v[2]), mid(cy, v[3])]];
      let cv = null, clear = 0;
      for (let p = 0; p < pts.length; p++) {
        const [x, y] = pts[p];
        const h = hit(x, y);
        if (!h || inside(h, t) || inside(t, h)) {
          if (p > 0 && ++clear >= 2) break; // 3 of the 4 quarters can no longer be covered
          continue;
        }
        (cv || (cv = [])).push(coverOf(t, h, p, x, y));
        if (p === 0) break; // the middle is covered: the element is
      }
      if (cv) n.cv = cv;
    }
  };

  // frameHits is what the page draws over a cross-origin frame's elements (issue #315). The
  // frame tests its elements in its own process (its own hitPass), where nothing of the page
  // shows: a modal, a backdrop or a banner of the page over the frame is only found here.
  // req lists, per element of a frame, [f, pts]: f the frame element's index in the last
  // snapshot, pts the element's sample points (page coordinates, as package occlusion samples
  // them). It returns, per element, the covers of the page at each point where a click does not
  // reach the frame element (n.cv's form; null when none): one test per point, all of them (the
  // host merges them with the frame's own). A point outside the frame element's box is not the
  // element's (the frame clips it): no cover.
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

  // Indices (in the last snapshot) of the elements a CSS selector matches.
  tb.css = (sel) => {
    const set = new Set(document.querySelectorAll(sel));
    const idx = [];
    tb.nodes.forEach((el, i) => { if (set.has(el)) idx.push(i); });
    return JSON.stringify(idx);
  };
})();


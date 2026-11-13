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
    // It still bumps the counter (moved, below): it can start with no mutation (a :hover, a media
    // query) and move a box from then on (issue #80).
    try {
      if (getComputedStyle(e.target).animationIterationCount === 'infinite') {
        moved();
        return;
      }
    } catch (x) {}
    start('a', e);
  }, true);
  window.addEventListener('animationend', (e) => end('a', e), true);
  window.addEventListener('animationcancel', (e) => end('a', e), true);
  // Changes that move boxes without a DOM mutation (issue #80: a tap skips its re-reads of a
  // target when no change arrived since the first read). This is a list of hooks, not a
  // guarantee: what is not on it (see "known untracked" in docs/architecture.md) moves a box
  // unseen, and the ResizeObserver below is the safety net for what changes a box's size. They
  // bump the change counter as restless changes do, without holding the page unsettled (a loop
  // restarting an animation would otherwise hold the page forever), once per microtask.
  let movedQueued = false;
  const moved = () => {
    if (movedQueued) return;
    movedQueued = true;
    queueMicrotask(() => {
      movedQueued = false;
      notify(false);
    });
  };
  // Wrappers keep the original's name, length and property attributes.
  const keep = (fn, orig) => {
    for (const k of ['name', 'length']) {
      try { Object.defineProperty(fn, k, { value: orig[k], configurable: true }); } catch (e) {}
    }
    return fn;
  };
  const hookMethod = (proto, key, after) => {
    try {
      const d = proto && Object.getOwnPropertyDescriptor(proto, key);
      if (!d || typeof d.value !== 'function') return;
      const orig = d.value;
      const fn = keep({ [key](...args) {
        const r = orig.apply(this, args);
        try { after(this, r); } catch (e) {}
        return r;
      } }[key], orig);
      Object.defineProperty(proto, key, { configurable: d.configurable, enumerable: d.enumerable, writable: d.writable, value: fn });
    } catch (e) {}
  };
  const hookSetter = (proto, key, after) => {
    try {
      const d = proto && Object.getOwnPropertyDescriptor(proto, key);
      if (!d || typeof d.set !== 'function') return;
      const orig = d.set;
      const set = keep({ set(v) {
        orig.call(this, v);
        try { after(this); } catch (e) {}
      } }.set, orig);
      Object.defineProperty(proto, key, { configurable: d.configurable, enumerable: d.enumerable, get: d.get, set });
    } catch (e) {}
  };
  const movedAfter = (self, r) => {
    moved();
    if (r && typeof r.then === 'function') r.then(moved, moved); // CSSStyleSheet.replace
  };
  const W = window;
  // Script animations, however they are started, seeked or changed.
  hookMethod(W.Element && Element.prototype, 'animate', movedAfter);
  if (W.Animation) {
    for (const k of ['play', 'pause', 'reverse', 'finish', 'cancel', 'updatePlaybackRate', 'persist']) hookMethod(Animation.prototype, k, movedAfter);
    for (const k of ['currentTime', 'startTime', 'playbackRate', 'effect', 'timeline']) hookSetter(Animation.prototype, k, movedAfter);
  }
  if (W.AnimationEffect) hookMethod(AnimationEffect.prototype, 'updateTiming', movedAfter);
  if (W.KeyframeEffect) {
    hookMethod(KeyframeEffect.prototype, 'setKeyframes', movedAfter);
    for (const k of ['target', 'composite', 'pseudoElement']) hookSetter(KeyframeEffect.prototype, k, movedAfter);
  }
  // Style sheets changed through the CSSOM (CSS-in-JS libraries insert their rules so).
  if (W.CSSStyleSheet) {
    for (const k of ['insertRule', 'deleteRule', 'addRule', 'removeRule', 'replace', 'replaceSync']) hookMethod(CSSStyleSheet.prototype, k, movedAfter);
  }
  if (W.StyleSheet) hookSetter(StyleSheet.prototype, 'disabled', movedAfter);
  if (W.CSSGroupingRule) for (const k of ['insertRule', 'deleteRule']) hookMethod(CSSGroupingRule.prototype, k, movedAfter);
  if (W.CSSKeyframesRule) for (const k of ['appendRule', 'deleteRule']) hookMethod(CSSKeyframesRule.prototype, k, movedAfter);
  if (W.CSSStyleRule) hookSetter(CSSStyleRule.prototype, 'selectorText', movedAfter);
  if (W.MediaList) {
    for (const k of ['appendMedium', 'deleteMedium']) hookMethod(MediaList.prototype, k, movedAfter);
    hookSetter(MediaList.prototype, 'mediaText', movedAfter);
  }
  if (W.CSSStyleDeclaration) {
    // A rule's declarations (an element's own style attribute is a mutation already).
    const ofRule = (self) => { if (self.parentRule) moved(); };
    for (const k of ['setProperty', 'removeProperty']) hookMethod(CSSStyleDeclaration.prototype, k, ofRule);
    hookSetter(CSSStyleDeclaration.prototype, 'cssText', ofRule);
  }
  for (const P of [W.Document, W.ShadowRoot]) if (P) hookSetter(P.prototype, 'adoptedStyleSheets', movedAfter);
  // Every shadow root, open or closed, has its changes watched from the moment it is made.
  hookMethod(W.Element && Element.prototype, 'attachShadow', (self, root) => {
    if (root) watchShadow(root);
    moved();
  });
  // Elements' own loads (an image, a stylesheet, a video's size): a load event reaches the
  // document, not the window. A web font landing, and the viewport or a video resizing.
  for (const ev of ['load', 'error', 'loadedmetadata', 'resize']) document.addEventListener(ev, moved, true);
  try { document.fonts && document.fonts.addEventListener('loadingdone', moved); } catch (e) {}
  window.addEventListener('resize', moved, true);
  // The safety net: a box that changes size (the page, or an element a snapshot listed) bumps
  // the counter. A new observation's first report only records the size.
  const sizes = new WeakMap();
  let resizes = null;
  try {
    resizes = new ResizeObserver((entries) => {
      let changed = false;
      for (const e of entries) {
        const r = e.contentRect;
        const was = sizes.get(e.target);
        sizes.set(e.target, r.width + 'x' + r.height);
        if (was !== undefined && was !== r.width + 'x' + r.height) changed = true;
      }
      if (changed) moved();
    });
  } catch (e) {}
  const watched = new WeakSet();
  const watchSize = (el) => {
    if (!resizes || !el || watched.has(el)) return;
    watched.add(el);
    try { resizes.observe(el); } catch (e) {}
  };
  tb.watchSize = watchSize;
  // Whether a read of doc can be the last word: not while something that will move boxes when
  // it lands is still loading (an image, a stylesheet, a font).
  const loadingIn = (doc) => {
    try {
      if (doc.fonts && doc.fonts.status === 'loading') return true;
      for (const l of doc.querySelectorAll('link[rel~="stylesheet" i]')) if (!l.sheet && !l.disabled) return true;
      for (const i of doc.images) if (!i.complete) return true;
      for (const v of doc.querySelectorAll('video')) if (v.readyState < 1 && v.preload !== 'none' && (v.src || v.querySelector('source'))) return true;
      return false;
    } catch (e) {
      return true;
    }
  };
  // Whether an animation that can move a box runs in doc (issue #80): any running one but those
  // that only change paint (a color, opacity, a shadow, a filter). An endless one counts too
  // (a spinner turning): its frames send no event, so a read of the page is not the last word.
  const PAINT_ONLY = /^(opacity|filter|backdrop-filter|box-shadow|text-shadow|fill|stroke|fill-opacity|stroke-opacity|color|(?!--).*-color|background-image)$/;
  const KEYFRAME_META = new Set(['offset', 'computedOffset', 'easing', 'composite']);
  const camel = (p) => p.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase());
  const animating = (doc) => {
    try {
      if (!doc.getAnimations) return true;
      for (const a of doc.getAnimations()) {
        if (a.playState !== 'running' && a.pending !== true) continue;
        if (typeof a.transitionProperty === 'string') {
          if (!PAINT_ONLY.test(a.transitionProperty)) return true;
          continue;
        }
        if (!a.effect || !a.effect.getKeyframes) return true;
        for (const k of a.effect.getKeyframes()) {
          for (const p of Object.keys(k)) if (!KEYFRAME_META.has(p) && !PAINT_ONLY.test(camel(p))) return true;
        }
      }
      return false;
    } catch (e) {
      return true;
    }
  };

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
    // moving: the read may not be the last word (issue #80): an animation that can move a box
    // runs, something that moves boxes as it lands still loads, or the helper came late (a page
    // could hold the originals of what it wraps). Also in the same-origin frames read below.
    let moving = tb.late === true || animating(document) || loadingIn(document);
    watchSize(document.documentElement);
    if (document.body) watchSize(document.body);
    const nodes = [];
    let active = document.activeElement;
    while (active && active.shadowRoot && active.shadowRoot.activeElement) active = active.shadowRoot.activeElement;
    const visit = (el, depth, ox, oy) => {
      const tag = el.tagName.toLowerCase();
      if (SKIP.has(tag)) {
        if (tag === 'img' && !el.complete) moving = true; // it moves boxes when it lands
        return;
      }
      if (tag === 'iframe') {
        const r = el.getBoundingClientRect();
        // The content box: where the frame's own (0,0) is.
        const cs = getComputedStyle(el);
        const cx = r.x + (parseFloat(cs.borderLeftWidth) || 0) + (parseFloat(cs.paddingLeft) || 0);
        const cy = r.y + (parseFloat(cs.borderTopWidth) || 0) + (parseFloat(cs.paddingTop) || 0);
        let doc = null;
        try { doc = el.contentDocument; } catch (e) {}
        if (doc && doc.body) {
          if (animating(doc) || loadingIn(doc)) moving = true;
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
      watchSize(el);
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
    return JSON.stringify({ w: window.innerWidth, h: window.innerHeight, url: location.href, title: document.title, loading: document.readyState !== 'complete', moving, nodes: out });
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


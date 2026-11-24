(() => {
  const B = (window.Bench = {});

  const h = (B.h = (tag, props, ...kids) => {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === false || v == null) continue;
      if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k === 'class') el.className = v;
      else if (k === 'style') el.style.cssText = v;
      else if (k === 'value') el.value = v;
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const c of kids.flat()) if (c != null && c !== false) el.append(c);
    return el;
  });
  const q = (B.q = (root, id) => root.querySelector('#' + id));
  B.spinner = (id) => h('div', { id, class: 'spinner', role: 'progressbar', 'aria-busy': 'true' });

  const screens = {};
  const routes = [];
  const parentRules = [];
  B.screen = (name, el, opts = {}) => {
    el.classList.add('screen');
    if (opts.pushed) el.classList.add('pushed');
    if (opts.slide) el.style.setProperty('--slide', opts.slide + 'ms');
    el.hidden = true;
    if (!el.parentNode) document.body.append(el);
    screens[name] = Object.assign({ name, el }, opts);
    return el;
  };
  B.route = (pattern, name) => {
    const keys = [];
    const re = new RegExp('^' + pattern.replace(/:(\w+)/g, (_, k) => (keys.push(k), '([^/]+)')) + '$');
    routes.push({ re, keys, name });
  };
  B.parentRule = (f) => parentRules.push(f);
  const parentOf = (path) => {
    if (path === '/') return null;
    for (const f of parentRules) { const p = f(path); if (p) return p; }
    const i = path.lastIndexOf('/');
    return i <= 0 ? '/' : path.slice(0, i);
  };
  const resolve = (path) => {
    for (const r of routes) {
      const m = r.re.exec(path);
      if (m) {
        const params = {};
        r.keys.forEach((k, i) => (params[k] = decodeURIComponent(m[i + 1])));
        return { path, name: r.name, params };
      }
    }
    return null;
  };

  const bar = (title, titleId) =>
    h('header', { class: 'bar' }, h('button', { id: 'back', class: 'back' }, 'Back'), h('h1', { id: titleId }, title));
  B.bar = bar;
  B.scenario = (key, name, build, opts = {}) => B.sub(key, '/' + key, name, 'scenario_title', build, opts);
  B.sub = (name, pattern, title, titleId, build, opts = {}) => {
    const page = h('div', { id: 'page', class: 'page' });
    const sec = h('section', { id: 'screen-' + name, class: 'scn' }, bar(title, titleId), opts.top || null, page);
    document.body.append(sec);
    const hooks = (build && build(page, sec)) || {};
    if (hooks.top) sec.insertBefore(hooks.top, page);
    B.screen(name, sec, Object.assign({ pushed: true }, opts, hooks));
    B.route(pattern, name);
    return sec;
  };

  let cur = null;
  let depth = 0;
  const stack = [];
  B.current = () => (cur ? cur.path : '/');
  const urlOf = (path) => (path === '/' ? location.pathname + location.search : '#' + path);
  const pathNow = () => decodeURIComponent(location.hash.replace(/^#/, '')) || '/';

  const show = (path, how) => {
    const r = resolve(path) || resolve('/');
    const prev = cur;
    if (prev && prev.path === r.path) return;
    const ps = prev && screens[prev.name];
    const ns = screens[r.name];
    if (ps && ps.leave) ps.leave(r);
    for (const s of Object.values(screens)) {
      if (s === ns) continue;
      s.el.hidden = true;
      s.el.classList.remove('enter', 'leaving');
    }
    ns.el.hidden = false;
    ns.el.classList.remove('enter', 'leaving');
    if (how === 'push' && ns.pushed) { void ns.el.offsetWidth; ns.el.classList.add('enter'); }
    if (how === 'pop' && ps && ps !== ns && ps.slide) {
      ps.el.hidden = false;
      ps.el.classList.add('leaving');
      ps.el.addEventListener('animationend', () => {
        ps.el.classList.remove('leaving');
        if (!cur || cur.name !== ps.name) ps.el.hidden = true;
      }, { once: true });
    }
    cur = r;
    stack[depth] = r.path;
    stack.length = depth + 1;
    if (ns.enter) ns.enter(r.params, { how, from: prev ? prev.path : null });
  };

  B.go = (path, o = {}) => {
    if (o.replace) {
      history.replaceState({ depth }, '', urlOf(path));
      show(path, o.how || 'replace');
    } else {
      depth++;
      history.pushState({ depth }, '', urlOf(path));
      show(path, 'push');
    }
  };
  let bypass = false;
  const guarded = () => cur && screens[cur.name].guard && screens[cur.name].guard();
  B.back = (o = {}) => {
    if (!o.force && guarded()) return;
    if (depth > 0) { bypass = !!o.force; history.back(); return; }
    const p = parentOf(cur.path);
    if (p) B.go(p, { replace: true, how: 'pop' });
  };
  B.popTo = (path) => {
    for (let i = depth - 1; i >= 0; i--) {
      if (stack[i] === path) { bypass = true; history.go(i - depth); return; }
    }
    B.go(path, { replace: true, how: 'pop' });
  };
  window.addEventListener('popstate', (e) => {
    const st = e.state;
    if (!st || typeof st.depth !== 'number') {
      startAt(pathNow());
      return;
    }
    const goingBack = st.depth < depth;
    if (goingBack && !bypass && guarded()) {
      depth = st.depth + 1;
      history.pushState({ depth }, '', '#' + cur.path);
      stack[depth] = cur.path;
      return;
    }
    bypass = false;
    depth = st.depth;
    show(pathNow(), goingBack ? 'pop' : 'push');
  });
  document.addEventListener('click', (e) => {
    const b = e.target.closest && e.target.closest('.back');
    if (b && !b.disabled) B.back();
  });

  const startAt = (path) => {
    const target = resolve(path) ? path : '/';
    const chain = [];
    for (let p = target; p; p = parentOf(p)) chain.unshift(p);
    depth = 0;
    history.replaceState({ depth: 0 }, '', urlOf('/'));
    stack[0] = '/';
    for (let i = 1; i < chain.length; i++) {
      depth = i;
      history.pushState({ depth }, '', urlOf(chain[i]));
      stack[i] = chain[i];
    }
    show(target, 'init');
  };
  B.start = () => startAt(pathNow());

  const SCENARIOS = [
    ['journey', 'Journey'], ['slow', 'Slow and janky'], ['permissions', 'Permissions'],
    ['overlays', 'Overlays'], ['controls', 'Controls'], ['navigation', 'Navigation and back stack'],
    ['onboarding', 'Onboarding and login'], ['lists', 'Lists'], ['a11y_poor', 'Poor accessibility'],
    ['a11y_good', 'Good accessibility'], ['native', 'Native toolkits'], ['animations', 'Animations'],
    ['errors', 'Errors and retries'], ['keyboard', 'Keyboard'],
    ['transitions', 'Screen transitions'],
  ];
  B.SCENARIOS = SCENARIOS;
  const listSec = h('section', { id: 'screen-list' },
    h('h1', { id: 'scenarios_title' }, 'Scenarios'),
    h('div', { id: 'scenarios', class: 'scenario-list' },
      SCENARIOS.map(([k, n]) => h('button', { id: 'scenario_' + k, class: 'scenario-row', onclick: () => B.go('/' + k) }, n))));
  document.body.prepend(listSec);
  B.screen('list', listSec);
  B.route('/', 'list');

  const $ = (id) => document.getElementById(id);
  const login = $('screen-login'), home = $('screen-home'), detail = $('screen-detail'), live = $('screen-live');
  const state = { user: '', cart: 0, items: 40, item: 0, taps: 0 };
  const inJourney = (p) => p === '/journey' || p.startsWith('/journey/');

  $('login').addEventListener('click', () => {
    const u = $('username').value, p = $('password').value;
    if (!u || !p) { $('error').hidden = false; return; }
    $('error').hidden = true;
    $('loading').hidden = false;
    setTimeout(() => {
      $('loading').hidden = true;
      if (B.current() !== '/journey') return;
      state.user = u;
      $('welcome').textContent = 'Welcome, ' + u;
      B.go('/journey/home', { replace: true });
    }, 1000);
  });
  B.screen('login', login, {
    enter() { if (state.user) B.go('/journey/home', { replace: true }); },
  });
  B.route('/journey', 'login');
  B.parentRule((p) => (p === '/journey/home' ? '/' : null));
  B.parentRule((p) => (/^\/journey\/(detail\/\d+|live)$/.test(p) ? '/journey/home' : null));

  let tick = 0;
  setInterval(() => { tick++; if (cur && cur.name === 'home') $('ticker').textContent = 'Live: ' + tick; }, 100);
  const list = $('items');
  const renderCart = () => {
    $('cart_count').textContent = 'Cart: ' + state.cart;
    $('detail_cart').textContent = 'In cart: ' + state.cart;
  };
  const addRows = (from, to) => {
    const more = $('load_more');
    for (let i = from; i <= to; i++) {
      const row = document.createElement('div');
      row.className = 'item';
      row.setAttribute('role', 'button');
      row.textContent = 'Item ' + i;
      row.addEventListener('click', () => B.go('/journey/detail/' + i));
      list.insertBefore(row, more);
    }
  };
  const loadMore = document.createElement('button');
  loadMore.id = 'load_more';
  loadMore.textContent = 'Load more';
  list.appendChild(loadMore);
  addRows(1, state.items);
  loadMore.addEventListener('click', () => {
    loadMore.hidden = true;
    const sp = document.createElement('div');
    sp.id = 'load_more_spinner';
    sp.className = 'spinner';
    list.appendChild(sp);
    setTimeout(() => {
      sp.remove();
      addRows(state.items + 1, state.items + 10);
      state.items += 10;
      loadMore.hidden = false;
      list.appendChild(loadMore);
    }, 800);
  });
  $('open_live').addEventListener('click', () => B.go('/journey/live'));
  let homeScroll = 0;
  $('logout').addEventListener('click', () => {
    state.user = '';
    homeScroll = 0;
    $('username').value = '';
    $('password').value = '';
    B.go('/journey', { replace: true });
  });
  B.screen('home', home, {
    enter() {
      if (!state.user) { B.go('/journey', { replace: true }); return; }
      window.scrollTo(0, homeScroll);
    },
    leave(next) { homeScroll = inJourney(next.path) ? window.scrollY : 0; },
  });
  B.route('/journey/home', 'home');

  let reviewsTimer;
  B.screen('detail', detail, {
    pushed: true,
    enter(params) {
      state.item = +params.n;
      $('detail_title').textContent = 'Item ' + state.item + ' details';
      $('detail_text').textContent = 'This is item ' + state.item + '.';
      renderCart();
      $('reviews').hidden = true;
      $('reviews_spinner').hidden = false;
      clearTimeout(reviewsTimer);
      reviewsTimer = setTimeout(() => { $('reviews_spinner').hidden = true; $('reviews').hidden = false; }, 600);
    },
  });
  B.route('/journey/detail/:n', 'detail');
  $('add_to_cart').addEventListener('click', () => { state.cart++; renderCart(); });

  let raf = 0, started = 0;
  B.screen('live', live, {
    pushed: true,
    enter() {
      started = performance.now();
      state.taps = 0;
      $('tap_count').textContent = 'Tapped 0';
      cancelAnimationFrame(raf);
      const frame = (now) => {
        const ms = Math.max(0, Math.floor(now - started));
        const m = String(Math.floor(ms / 60000)).padStart(2, '0');
        const s = String(Math.floor(ms / 1000) % 60).padStart(2, '0');
        $('stopwatch').textContent = m + ':' + s + '.' + String(ms % 1000).padStart(3, '0');
        raf = requestAnimationFrame(frame);
      };
      raf = requestAnimationFrame(frame);
    },
    leave() { cancelAnimationFrame(raf); },
  });
  B.route('/journey/live', 'live');
  $('tap_me').addEventListener('click', () => { state.taps++; $('tap_count').textContent = 'Tapped ' + state.taps; });
  $('notify_switch').addEventListener('change', (e) => {
    $('notify_label').textContent = e.target.checked ? 'Notifications on' : 'Notifications off';
  });
})();

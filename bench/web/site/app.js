// Buddy Bench (web). Spec: bench/SPEC.md. Plain JS, no libraries, no network.
(() => {
  const $ = (id, root = document) => root.querySelector('#' + id);
  const screens = { login: $('screen-login'), home: $('screen-home'), detail: $('screen-detail'), live: $('screen-live') };
  const state = { user: '', cart: 0, items: 40, item: 0, taps: 0 };
  let current = 'login';

  let homeScroll = 0;
  const show = (name, animate) => {
    if (current === 'home' && name !== 'home') homeScroll = window.scrollY;
    for (const [k, el] of Object.entries(screens)) el.hidden = k !== name;
    if (name === 'home') window.scrollTo(0, homeScroll);
    const el = screens[name];
    el.classList.remove('enter');
    if (animate && el.classList.contains('pushed')) { void el.offsetWidth; el.classList.add('enter'); }
    current = name;
    if (name === 'detail') openDetail();
    if (name === 'live') openLive(); else stopLive();
  };

  // Navigation: Detail and Live are pushed onto the browser history, so Back (the in-app
  // button or the browser's) pops them.
  const push = (name, extra) => { history.pushState({ screen: name, ...extra }, ''); show(name, true); };
  window.addEventListener('popstate', (e) => {
    const s = (e.state && e.state.screen) || (state.user ? 'home' : 'login');
    if (e.state && e.state.item) state.item = e.state.item;
    show(s, false);
  });
  for (const b of document.querySelectorAll('.back')) b.addEventListener('click', () => history.back());

  // ---- Login ----
  $('login').addEventListener('click', () => {
    const u = $('username').value, p = $('password').value;
    if (!u || !p) { $('error').hidden = false; return; }
    $('error').hidden = true;
    $('loading').hidden = false;
    setTimeout(() => {
      $('loading').hidden = true;
      state.user = u;
      $('welcome').textContent = 'Welcome, ' + u;
      history.replaceState({ screen: 'home' }, '');
      show('home', false);
    }, 1000);
  });

  // ---- Home ----
  let tick = 0;
  // It keeps counting behind other screens, but only redraws while Home shows (as on iOS).
  setInterval(() => { tick++; if (current === 'home') $('ticker').textContent = 'Live: ' + tick; }, 100);
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
      row.addEventListener('click', () => { state.item = i; push('detail', { item: i }); });
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
  $('open_live').addEventListener('click', () => push('live'));
  $('logout').addEventListener('click', () => {
    state.user = '';
    homeScroll = 0;
    $('username').value = '';
    $('password').value = '';
    history.replaceState({ screen: 'login' }, '');
    show('login', false);
  });

  // ---- Detail ----
  let reviewsTimer;
  function openDetail() {
    $('detail_title').textContent = 'Item ' + state.item + ' details';
    $('detail_text').textContent = 'This is item ' + state.item + '.';
    renderCart();
    $('reviews').hidden = true;
    $('reviews_spinner').hidden = false;
    clearTimeout(reviewsTimer);
    reviewsTimer = setTimeout(() => { $('reviews_spinner').hidden = true; $('reviews').hidden = false; }, 600);
  }
  $('add_to_cart').addEventListener('click', () => { state.cart++; renderCart(); });

  // ---- Live ----
  let raf = 0, started = 0;
  function openLive() {
    started = performance.now();
    state.taps = 0;
    $('tap_count').textContent = 'Tapped 0';
    cancelAnimationFrame(raf);
    const frame = (now) => {
      const ms = Math.floor(now - started);
      const m = String(Math.floor(ms / 60000)).padStart(2, '0');
      const s = String(Math.floor(ms / 1000) % 60).padStart(2, '0');
      $('stopwatch').textContent = m + ':' + s + '.' + String(ms % 1000).padStart(3, '0');
      raf = requestAnimationFrame(frame);
    };
    raf = requestAnimationFrame(frame);
  }
  function stopLive() { cancelAnimationFrame(raf); }
  $('tap_me').addEventListener('click', () => { state.taps++; $('tap_count').textContent = 'Tapped ' + state.taps; });
  $('notify_switch').addEventListener('change', (e) => {
    $('notify_label').textContent = e.target.checked ? 'Notifications on' : 'Notifications off';
  });

  history.replaceState({ screen: 'login' }, '');
  show('login', false);
})();

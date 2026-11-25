(() => {
  const { h } = Bench;
  const timers = (() => {
    let ids = [];
    return {
      later: (ms, f) => ids.push(setTimeout(f, ms)),
      clear: () => { ids.forEach(clearTimeout); ids = []; },
    };
  })();

  Bench.scenario('slow', 'Slow and janky', (page) => {
    const stage = h('div', { class: 'stage' });
    const lateSlot = h('div');
    const lateCount = h('span', { id: 'late_count' }, 'Late taps: 0');
    const jankText = h('span', { id: 'jank_text' }, 'Not blocked');
    let lateTaps = 0, janks = 0;
    page.append(
      stage,
      lateSlot,
      lateCount,
      h('button', {
        id: 'slow_open',
        onclick: () => {
          const at = Bench.current();
          setTimeout(() => { if (Bench.current() === at) Bench.go('/slow/detail'); }, 1200);
        },
      }, 'Open slowly'),
      h('button', {
        id: 'jank_button',
        onclick: () => {
          const end = performance.now() + 700;
          while (performance.now() < end) { }
          janks++;
          jankText.textContent = 'Unblocked ' + janks;
        },
      }, 'Block UI'),
      jankText,
      h('button', { id: 'slow_list_open', onclick: () => Bench.go('/slow/list') }, 'Slow list'),
      h('button', { id: 'relabel_open', onclick: () => Bench.go('/slow/relabel') }, 'Relabel'),
    );
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        timers.clear();
        lateTaps = 0; janks = 0;
        lateCount.textContent = 'Late taps: 0';
        jankText.textContent = 'Not blocked';
        stage.replaceChildren(...[1, 2, 3].map((i) => h('div', { id: 'skeleton_' + i, class: 'skeleton' })));
        lateSlot.replaceChildren();
        timers.later(2000, () => {
          stage.replaceChildren(h('span', { id: 'stage_text' }, 'Loaded'));
          [1, 2, 3].forEach((i, k) => timers.later(k * 400, () =>
            stage.append(h('div', { id: 'slow_row_' + i, class: 'fade-in' }, 'Slow row ' + i))));
        });
        timers.later(3000, () => lateSlot.replaceChildren(h('button', {
          id: 'late_button',
          onclick: () => { lateTaps++; lateCount.textContent = 'Late taps: ' + lateTaps; },
        }, 'Late button')));
      },
      leave(next) { if (!next.path.startsWith('/slow/')) timers.clear(); },
    };
  });

  Bench.sub('slow_detail', '/slow/detail', 'Slow detail', 'slow_detail_title', (page) => {
    page.append(h('p', {}, 'This screen slid in over 1500 ms. Back slides it out the same way.'));
  }, { slide: 1500 });

  Bench.sub('slow_list', '/slow/list', 'Slow list', 'sub_title', (page) => {
    page.classList.add('flush');
    let loaded = 0, loading = false, t1 = 0, t2 = 0;
    const rows = (from, to) => {
      for (let i = from; i <= to; i++) page.append(h('div', { id: 'slow_item_' + i, class: 'list-row' }, 'Slow item ' + i));
    };
    page.addEventListener('scroll', () => {
      if (loaded !== 20 || loading) return;
      if (page.scrollTop + page.clientHeight < page.scrollHeight - 4) return;
      loading = true;
      const sp = Bench.spinner('slow_list_spinner');
      page.append(sp);
      t2 = setTimeout(() => { sp.remove(); rows(21, 40); loaded = 40; loading = false; }, 2500);
    });
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        clearTimeout(t1); clearTimeout(t2);
        loaded = 0; loading = false;
        page.replaceChildren(Bench.spinner('slow_list_spinner'));
        page.scrollTop = 0;
        t1 = setTimeout(() => { page.replaceChildren(); rows(1, 20); loaded = 20; }, 2500);
      },
      leave() { clearTimeout(t1); clearTimeout(t2); },
    };
  });

  Bench.sub('slow_relabel', '/slow/relabel', 'Relabel', 'relabel_title', (page) => {
    const hints = ['Roof Repair', 'House Cleaner', 'Plumber'];
    const count = h('span', { id: 'relabel_taps' }, 'Search taps: 0');
    let taps = 0, first = 0, every = 0;
    const search = h('button', {
      id: 'relabel_search',
      onclick: () => { taps++; count.textContent = 'Search taps: ' + taps; },
    }, 'Search');
    page.append(search, count);
    const stop = () => { clearTimeout(first); clearInterval(every); };
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        stop();
        taps = 0;
        count.textContent = 'Search taps: 0';
        search.textContent = 'Search';
        let i = 0;
        const next = () => { search.textContent = 'Search for ' + hints[i++ % hints.length]; };
        first = setTimeout(() => { next(); every = setInterval(next, 3000); }, 4000);
      },
      leave: stop,
    };
  });
})();

// Scenario 6, "Navigation and back stack" (bench/SCENARIOS.md). Every level is a browser
// history entry: #/navigation/level/3 opens with the stack 0 → 1 → 2 → 3.
(() => {
  const { h, q } = Bench;
  const MAX = 5;

  // An in-app confirm dialog: Bench-styled, not the browser's confirm().
  const confirmDialog = (id, text, buttons) => {
    const back = h('div', { class: 'modal-back', hidden: true },
      h('div', { id, class: 'modal', role: 'alertdialog', 'aria-modal': 'true' },
        h('h2', {}, text),
        h('div', { class: 'actions' }, buttons.map(([bid, label, f]) => h('button', { id: bid, onclick: () => { back.hidden = true; f(); } }, label)))));
    return back;
  };

  Bench.scenario('navigation', 'Navigation and back stack', (page, sec) => {
    const leave = confirmDialog('leave_dialog', 'Leave?', [
      ['leave_stay', 'Stay', () => {}],
      ['leave_go', 'Leave', () => Bench.popTo('/')],
    ]);
    page.append(
      h('span', { id: 'nav_depth' }, 'Depth 0'),
      h('button', { id: 'nav_push', onclick: () => Bench.go('/navigation/level/1') }, 'Push'),
      h('button', { id: 'nav_tabs', onclick: () => Bench.go('/navigation/tabs') }, 'Tabbed screens'),
      h('button', { id: 'nav_dialog', onclick: () => (leave.hidden = false) }, 'Open confirm'),
      h('button', { id: 'nav_form', onclick: () => Bench.go('/navigation/form') }, 'Edit form'),
    );
    sec.append(leave);
    return { leave() { leave.hidden = true; } };
  });

  // Level N: one screen, re-entered for each level (each a history entry, sliding in).
  Bench.parentRule((p) => {
    const m = /^\/navigation\/level\/(\d+)$/.exec(p);
    return m ? (+m[1] > 1 ? '/navigation/level/' + (+m[1] - 1) : '/navigation') : null;
  });
  let level = 0;
  Bench.sub('nav_level', '/navigation/level/:n', 'Level 1', 'level_title', (page, sec) => {
    const push = h('button', { id: 'nav_push', onclick: () => Bench.go('/navigation/level/' + (level + 1)) }, 'Push');
    page.append(
      h('span', { id: 'nav_depth' }, 'Depth 1'),
      push,
      h('button', { id: 'nav_pop', onclick: () => Bench.back() }, 'Pop'),
      h('button', { id: 'nav_home', onclick: () => Bench.popTo('/navigation') }, 'Pop to root'),
    );
    return {
      enter(params) {
        level = Math.max(1, Math.min(MAX, +params.n || 1));
        q(sec, 'level_title').textContent = 'Level ' + level;
        q(sec, 'nav_depth').textContent = 'Depth ' + level;
        push.disabled = level >= MAX;
      },
    };
  });

  // Tabs with their own stacks.
  Bench.sub('nav_tabs', '/navigation/tabs', 'Tabbed screens', 'sub_title', (page, sec) => {
    page.classList.add('flush');
    const feedList = h('div', { class: 'view' },
      h('h2', { id: 'feed_title' }, 'Feed'),
      h('p', { class: 'muted' }, 'Posts from people you follow.'),
      h('button', { id: 'open_post', onclick: () => showPost(true) }, 'Open post'));
    const post = h('div', { class: 'view', hidden: true },
      h('button', { id: 'post_back', onclick: () => showPost(false) }, 'Back to feed'),
      h('h2', { id: 'post_title' }, 'Post'),
      h('p', {}, 'A post, opened inside the Feed tab.'));
    const showPost = (on) => { post.hidden = !on; feedList.hidden = on; };
    const feed = h('div', { class: 'tab-body', role: 'tabpanel' }, feedList, post);
    const profile = h('div', { class: 'tab-body', role: 'tabpanel', hidden: true },
      h('h2', { id: 'profile_title' }, 'Profile'),
      h('p', { class: 'muted' }, 'Your name and settings.'));
    const tabFeed = h('button', { id: 'navtab_feed', role: 'tab', onclick: () => pick('feed') }, 'Feed');
    const tabProfile = h('button', { id: 'navtab_profile', role: 'tab', onclick: () => pick('profile') }, 'Profile');
    const pick = (t) => {
      feed.hidden = t !== 'feed';
      profile.hidden = t !== 'profile';
      tabFeed.setAttribute('aria-selected', String(t === 'feed'));
      tabProfile.setAttribute('aria-selected', String(t === 'profile'));
    };
    page.append(
      h('div', { style: 'padding:12px 16px 0' }, h('button', { id: 'tabs_close', onclick: () => Bench.back() }, 'Close tabs')),
      feed, profile);
    page.style.overflow = 'hidden';
    sec.append(h('div', { class: 'tabbar', role: 'tablist' }, tabFeed, tabProfile));
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        pick('feed');
        showPost(false);
      },
    };
  });

  // A form that asks before throwing away what was typed (in-app Back and the browser's back).
  let discardOpen = false;
  Bench.sub('nav_form', '/navigation/form', 'Edit form', 'sub_title', (page, sec) => {
    const field = h('input', { id: 'form_field', placeholder: 'Title', autocomplete: 'off' });
    const discard = confirmDialog('discard_dialog', 'Discard changes?', [
      ['discard_keep', 'Keep editing', () => { discardOpen = false; field.focus(); }],
      ['discard_yes', 'Discard', () => { discardOpen = false; field.value = ''; Bench.back({ force: true }); }],
    ]);
    page.append(field);
    sec.append(discard);
    return {
      enter(params, info) { if (info.how !== 'pop') field.value = ''; discard.hidden = true; discardOpen = false; },
      guard() {
        if (!field.value) return false;
        discard.hidden = false;
        discardOpen = true;
        return true;
      },
    };
  });
})();

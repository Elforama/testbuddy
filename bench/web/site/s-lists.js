// Scenario 8, "Lists" (bench/SCENARIOS.md): long, grid, nested horizontal, two-pane,
// pull-to-refresh, swipe-to-delete, reorder, endless and empty lists.
(() => {
  const { h } = Bench;
  const fresh = (info) => info.how !== 'pop';

  Bench.scenario('lists', 'Lists', (page) => {
    const open = (id, label, path) => h('button', { id, onclick: () => Bench.go('/lists/' + path) }, label);
    page.append(
      open('open_long_list', 'Long list', 'long'),
      open('open_grid', 'Grid', 'grid'),
      open('open_carousels', 'Carousels', 'carousels'),
      open('open_two_pane', 'Two panes', 'two-pane'),
      open('open_refresh', 'Pull to refresh', 'refresh'),
      open('open_swipe_delete', 'Swipe to delete', 'swipe-delete'),
      open('open_reorder', 'Reorder', 'reorder'),
      open('open_infinite', 'Endless feed', 'infinite'),
      open('open_empty', 'Empty list', 'empty'),
    );
  });

  // A list screen with a `picked` line fixed above the list.
  const pickedList = (name, path, title, build) => {
    const picked = h('div', { id: 'picked', class: 'strip' }, 'Nothing picked');
    Bench.sub(name, path, title, 'sub_title', (page) => {
      page.classList.add('flush');
      build(page, (t) => (picked.textContent = 'Picked ' + t));
      return {
        enter(params, info) {
          if (!fresh(info)) return;
          picked.textContent = 'Nothing picked';
          page.scrollTop = 0;
          page.querySelectorAll('.shelf-row').forEach((r) => (r.scrollLeft = 0));
        },
      };
    }, { top: picked });
  };

  // Long list: 500 rows, sticky section headers every 25 rows.
  pickedList('list_long', '/lists/long', 'Long list', (page, pick) => {
    for (let s = 0; s < 20; s++) {
      const letter = String.fromCharCode(65 + s);
      const sec = h('div', {}, h('div', { id: 'section_' + letter, class: 'sec-head' }, 'Section ' + letter));
      for (let i = s * 25 + 1; i <= s * 25 + 25; i++) {
        sec.append(h('div', { id: 'row_' + i, class: 'long-row', role: 'button', onclick: () => pick('Row ' + i) }, 'Row ' + i));
      }
      page.append(sec);
    }
  });

  // Grid: 3 × 40 square cells.
  pickedList('list_grid', '/lists/grid', 'Grid', (page, pick) => {
    const grid = h('div', { class: 'grid' });
    for (let i = 1; i <= 120; i++) grid.append(h('div', { id: 'cell_' + i, class: 'cell', role: 'button', onclick: () => pick('Cell ' + i) }, 'Cell ' + i));
    page.append(grid);
  });

  // Carousels: 10 shelves of 15 cards, each shelf scrolling sideways.
  pickedList('list_carousels', '/lists/carousels', 'Carousels', (page, pick) => {
    for (let s = 1; s <= 10; s++) {
      const row = h('div', { class: 'shelf-row' });
      for (let n = 1; n <= 15; n++) {
        row.append(h('div', { id: `card_${s}_${n}`, class: 'card', role: 'button', onclick: () => pick(`Card ${s}-${n}`) }, `Card ${s}-${n}`));
      }
      page.append(h('div', { class: 'shelf' }, h('h2', { id: 'shelf_' + s }, 'Shelf ' + s), row));
    }
  });

  // Two panes: a mail list (40 %) and the selected mail, whose body scrolls on its own.
  Bench.sub('list_two_pane', '/lists/two-pane', 'Two panes', 'sub_title', (page) => {
    page.classList.add('flush');
    page.style.overflow = 'hidden';
    const list = h('div', { id: 'mail_list', class: 'mail-list' });
    const title = h('h2', { id: 'mail_detail' }, 'Mail 1');
    const body = h('div', { id: 'mail_body', class: 'mail-body' });
    const select = (n) => {
      list.querySelectorAll('.mail-row').forEach((r, i) => r.setAttribute('aria-selected', String(i + 1 === n)));
      title.textContent = 'Mail ' + n;
      body.replaceChildren(...Array.from({ length: 60 }, (_, k) => h('p', {}, `Line ${k + 1} of mail ${n}`)));
      body.scrollTop = 0;
    };
    for (let n = 1; n <= 80; n++) list.append(h('div', { id: 'mail_' + n, class: 'mail-row', role: 'option', onclick: () => select(n) }, 'Mail ' + n));
    page.append(h('div', { class: 'two-pane' }, list, h('div', { class: 'mail-pane' }, title, body)));
    return { enter(params, info) { if (fresh(info)) { select(1); list.scrollTop = 0; } } };
  });

  // Pull to refresh: pull down at the top (a touch or mouse drag, or a wheel/trackpad swipe
  // down while the list is at its top), 1000 ms refresh.
  Bench.sub('list_refresh', '/lists/refresh', 'Pull to refresh', 'sub_title', (page) => {
    page.classList.add('flush');
    let count = 0, refreshing = false;
    const countEl = h('div', { id: 'refresh_count', class: 'strip' }, 'Refreshed 0 times');
    const head = h('div', { class: 'refresh-head', hidden: true }, Bench.spinner('refresh_spinner'));
    const refresh = () => {
      if (refreshing) return;
      refreshing = true;
      head.hidden = false;
      setTimeout(() => {
        head.hidden = true;
        refreshing = false;
        countEl.textContent = 'Refreshed ' + ++count + ' times';
      }, 1000);
    };
    page.append(head);
    for (let i = 1; i <= 10; i++) page.append(h('div', { id: 'fresh_' + i, class: 'list-row' }, 'Fresh ' + i));
    // wheel: an upward scroll while already at the top is a pull
    let pull = 0, pullTimer;
    page.addEventListener('wheel', (e) => {
      if (page.scrollTop > 0 || e.deltaY >= 0) { pull = 0; return; }
      pull += -e.deltaY;
      clearTimeout(pullTimer);
      pullTimer = setTimeout(() => (pull = 0), 300);
      if (pull >= 60) { pull = 0; refresh(); }
    }, { passive: true });
    // touch and mouse drags
    let y0 = null;
    const down = (y) => { y0 = page.scrollTop <= 0 ? y : null; };
    const up = (y) => { if (y0 != null && y - y0 > 70) refresh(); y0 = null; };
    page.addEventListener('touchstart', (e) => down(e.touches[0].clientY), { passive: true });
    page.addEventListener('touchend', (e) => up(e.changedTouches[0].clientY));
    page.addEventListener('mousedown', (e) => down(e.clientY));
    window.addEventListener('mouseup', (e) => up(e.clientY));
    return {
      top: countEl,
      enter(params, info) { if (fresh(info)) { count = 0; countEl.textContent = 'Refreshed 0 times'; page.scrollTop = 0; } },
    };
  });

  // Swipe to delete: each row scrolls sideways to reveal its Delete button.
  Bench.sub('list_swipe', '/lists/swipe-delete', 'Swipe to delete', 'sub_title', (page) => {
    page.classList.add('flush');
    const status = h('div', { id: 'delete_status', class: 'strip' }, 'Tasks: 8');
    const fill = () => {
      page.replaceChildren();
      for (let i = 1; i <= 8; i++) {
        const row = h('div', { class: 'swipe-row' },
          h('div', { id: 'task_' + i, class: 'swipe-content' }, 'Task ' + i),
          h('button', { id: 'task_delete_' + i, class: 'swipe-delete', onclick: () => { row.remove(); count(); } }, 'Delete'));
        page.append(row);
      }
      count();
    };
    const count = () => (status.textContent = 'Tasks: ' + page.querySelectorAll('.swipe-row').length);
    fill();
    return { top: status, enter(params, info) { if (fresh(info)) fill(); } };
  });

  // Reorder: drag a row by its handle (pointer), or move it with ↑/↓ on a focused handle.
  Bench.sub('list_reorder', '/lists/reorder', 'Reorder', 'sub_title', (page) => {
    page.classList.add('flush');
    const status = h('div', { id: 'order_status', class: 'strip' }, 'Order: ABCDE');
    const order = () => [...page.querySelectorAll('.reorder-row')].map((r) => r.dataset.key).join('');
    const update = () => (status.textContent = 'Order: ' + order());
    const fill = () => {
      page.replaceChildren(...'ABCDE'.split('').map((k) => {
        const handle = h('div', { id: 'handle_' + k, class: 'handle', role: 'button', tabindex: '0', 'aria-label': 'Move ' + k }, '≡');
        const row = h('div', { class: 'reorder-row', 'data-key': k }, h('span', { id: 'reorder_' + k }, k), handle);
        handle.addEventListener('pointerdown', (e) => {
          e.preventDefault();
          handle.setPointerCapture(e.pointerId);
          row.classList.add('dragging');
          const move = (ev) => {
            const rows = [...page.querySelectorAll('.reorder-row')];
            for (const other of rows) {
              if (other === row) continue;
              const r = other.getBoundingClientRect();
              const mid = r.top + r.height / 2;
              const idx = rows.indexOf(row), oi = rows.indexOf(other);
              if (oi < idx && ev.clientY < mid) { page.insertBefore(row, other); update(); break; }
              if (oi > idx && ev.clientY > mid) { page.insertBefore(row, other.nextSibling); update(); break; }
            }
          };
          const end = () => {
            row.classList.remove('dragging');
            handle.removeEventListener('pointermove', move);
            update();
          };
          handle.addEventListener('pointermove', move);
          handle.addEventListener('pointerup', end, { once: true });
          handle.addEventListener('pointercancel', end, { once: true });
        });
        handle.addEventListener('keydown', (e) => {
          if (e.key === 'ArrowUp' && row.previousElementSibling) page.insertBefore(row, row.previousElementSibling);
          else if (e.key === 'ArrowDown' && row.nextElementSibling) page.insertBefore(row.nextElementSibling, row);
          else return;
          e.preventDefault();
          handle.focus();
          update();
        });
        return row;
      }));
      update();
    };
    fill();
    return { top: status, enter(params, info) { if (fresh(info)) fill(); } };
  });

  // Endless feed: 20 more rows 800 ms after reaching the end, up to 200.
  Bench.sub('list_infinite', '/lists/infinite', 'Endless feed', 'sub_title', (page) => {
    page.classList.add('flush');
    let n = 0, loading = false, timer;
    const add = (k) => { for (let i = 0; i < k && n < 200; i++) { n++; page.append(h('div', { id: 'post_' + n, class: 'list-row' }, 'Post ' + n)); } };
    page.addEventListener('scroll', () => {
      if (loading || n >= 200) return;
      if (page.scrollTop + page.clientHeight < page.scrollHeight - 4) return;
      loading = true;
      const sp = Bench.spinner('feed_spinner');
      page.append(sp);
      timer = setTimeout(() => { sp.remove(); add(20); loading = false; }, 800);
    }, { passive: true });
    return {
      enter(params, info) {
        if (!fresh(info)) return;
        clearTimeout(timer);
        loading = false; n = 0;
        page.replaceChildren();
        add(20);
        page.scrollTop = 0;
      },
    };
  });

  // Empty list, and a button that adds rows.
  Bench.sub('list_empty', '/lists/empty', 'Empty list', 'sub_title', (page) => {
    let n = 0;
    const rows = h('div', { style: 'width:100%' });
    const empty = h('p', { id: 'empty_text', class: 'muted' }, 'Nothing here yet');
    page.append(h('button', {
      id: 'empty_add',
      onclick: () => { n++; rows.append(h('div', { id: 'new_row_' + n, class: 'list-row' }, 'New row ' + n)); empty.hidden = true; },
    }, 'Add one'), empty, rows);
    return { enter(params, info) { if (fresh(info)) { n = 0; rows.replaceChildren(); empty.hidden = false; } } };
  });
})();

// Scenario 4, "Overlays" (bench/SCENARIOS.md): things drawn over the screen that take taps.
// The "Under" button sits at the bottom, where every overlay covers it.
(() => {
  const { h } = Bench;

  // A bottom sheet: a backdrop and a panel. Closes on its Close button, a backdrop tap, or a
  // drag down (a pointer drag, or a downward wheel/trackpad swipe over it).
  const makeSheet = (contents, onClose) => {
    const back = h('div', { class: 'backdrop', hidden: true });
    const sheet = h('div', { class: 'sheet', role: 'dialog', hidden: true }, h('div', { class: 'grabber' }), ...contents);
    const close = () => { back.hidden = true; sheet.hidden = true; sheet.style.transform = ''; if (onClose) onClose(); };
    const open = () => { back.hidden = false; sheet.hidden = false; };
    back.addEventListener('click', close);
    let y0 = null, pull = 0, wheelTimer;
    sheet.addEventListener('pointerdown', (e) => { if (e.target.closest('button')) return; y0 = e.clientY; sheet.setPointerCapture(e.pointerId); sheet.style.transition = 'none'; });
    sheet.addEventListener('pointermove', (e) => { if (y0 != null) sheet.style.transform = 'translateY(' + Math.max(0, e.clientY - y0) + 'px)'; });
    const up = (e) => {
      if (y0 == null) return;
      const dy = e.clientY - y0;
      y0 = null;
      sheet.style.transition = '';
      if (dy > 80) close(); else sheet.style.transform = '';
    };
    sheet.addEventListener('pointerup', up);
    sheet.addEventListener('pointercancel', up);
    sheet.addEventListener('wheel', (e) => {
      e.preventDefault();
      pull += -e.deltaY; // a swipe down scrolls up
      clearTimeout(wheelTimer);
      wheelTimer = setTimeout(() => (pull = 0), 300);
      if (pull > 60) { pull = 0; close(); }
    }, { passive: false });
    return { back, sheet, open, close };
  };

  Bench.scenario('overlays', 'Overlays', (page, sec) => {
    const result = h('span', { id: 'overlay_result', class: 'sticky-status' }, 'Nothing yet');
    const set = (t) => (result.textContent = t);

    let under = 0;
    const underCount = h('span', { id: 'under_count' }, 'Under taps: 0');
    const underBar = h('div', { class: 'under-bar' },
      h('button', { id: 'under_button', onclick: () => (underCount.textContent = 'Under taps: ' + ++under) }, 'Under'),
      underCount);

    // Modal dialog: the platform's own, <dialog>.
    const dlg = h('dialog', { class: 'alert' },
      h('h2', { id: 'dialog_title' }, 'Delete item?'),
      h('div', { class: 'actions' },
        h('button', { onclick: () => { dlg.close(); set('Cancelled'); } }, 'Cancel'),
        h('button', { onclick: () => { dlg.close(); set('Deleted'); } }, 'Delete')));
    dlg.addEventListener('cancel', () => set('Cancelled')); // Esc

    // Bottom sheet.
    const sheet = makeSheet([
      h('h2', { id: 'sheet_title' }, 'Sheet'),
      h('button', { id: 'sheet_action', onclick: () => set('Sheet action') }, 'Sheet action'),
      h('button', { id: 'sheet_close', onclick: () => sheet.close() }, 'Close'),
    ]);
    sheet.sheet.id = 'sheet';

    // Snackbar, 3000 ms.
    let snackTimer;
    const snack = h('div', { id: 'snackbar', class: 'snackbar', role: 'status', hidden: true },
      h('span', { id: 'snackbar_text' }, 'Saved'),
      h('button', { id: 'snackbar_undo', onclick: () => { set('Undone'); snack.hidden = true; clearTimeout(snackTimer); } }, 'Undo'));
    const showSnack = () => {
      snack.hidden = false;
      clearTimeout(snackTimer);
      snackTimer = setTimeout(() => (snack.hidden = true), 3000);
    };

    // Loading veil, 1500 ms, swallows every tap (it is simply on top).
    let veilTimer;
    const veil = h('div', { id: 'veil', class: 'veil', hidden: true }, h('div', { class: 'spinner' }));
    const showVeil = () => {
      veil.hidden = false;
      clearTimeout(veilTimer);
      veilTimer = setTimeout(() => (veil.hidden = true), 1500);
    };

    // Tooltip: anchored under its button; a tap anywhere (on a transparent catcher over the
    // whole screen) dismisses it without reaching what is under.
    const tipButton = h('button', { id: 'show_tooltip' }, 'Show tooltip');
    const tip = h('div', { id: 'tooltip', class: 'tooltip', role: 'tooltip', hidden: true }, h('span', { id: 'tooltip_text' }, 'Tip: long press for more'));
    const catcher = h('div', { class: 'catcher', hidden: true });
    const hideTip = (e) => { if (e) { e.preventDefault(); e.stopPropagation(); } tip.hidden = true; catcher.hidden = true; };
    catcher.addEventListener('click', hideTip);
    tip.addEventListener('click', hideTip);
    tipButton.addEventListener('click', () => {
      const r = tipButton.getBoundingClientRect();
      tip.style.left = r.left + 'px';
      tip.style.top = r.bottom + 8 + 'px';
      tip.hidden = false;
      catcher.hidden = false;
    });

    // Full-screen cover, with a sheet that opens over it.
    const coverResult = h('span', { id: 'cover_result', hidden: true });
    const coverSheet = makeSheet([
      h('h2', { id: 'cover_sheet_title' }, 'Sheet on cover'),
      h('button', {
        id: 'cover_sheet_done',
        onclick: () => { coverSheet.close(); coverResult.textContent = 'Done on cover'; coverResult.hidden = false; },
      }, 'Done'),
    ]);
    const cover = h('div', { id: 'cover', class: 'cover', role: 'dialog', 'aria-modal': 'true', hidden: true },
      h('h1', { id: 'cover_title' }, 'Cover'),
      h('button', { id: 'cover_sheet', onclick: () => coverSheet.open() }, 'Open sheet on cover'),
      coverResult,
      h('button', { id: 'cover_close', onclick: () => { coverSheet.close(); cover.hidden = true; } }, 'Close cover'),
      coverSheet.back, coverSheet.sheet);

    page.append(
      result,
      h('button', { id: 'open_dialog', onclick: () => dlg.showModal() }, 'Open dialog'),
      h('button', { id: 'open_sheet', onclick: () => sheet.open() }, 'Open sheet'),
      h('button', { id: 'show_snackbar', onclick: showSnack }, 'Show snackbar'),
      h('button', { id: 'show_veil', onclick: showVeil }, 'Show veil'),
      tipButton,
      h('button', {
        id: 'open_cover',
        onclick: () => { coverResult.hidden = true; coverResult.textContent = ''; cover.hidden = false; },
      }, 'Open cover'),
    );
    page.style.paddingBottom = '96px';
    sec.append(underBar, dlg, sheet.back, sheet.sheet, snack, veil, catcher, tip, cover);

    const closeAll = () => {
      if (dlg.open) dlg.close();
      sheet.close(); coverSheet.close();
      snack.hidden = true; veil.hidden = true; cover.hidden = true;
      hideTip();
    };
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        closeAll();
        under = 0;
        underCount.textContent = 'Under taps: 0';
        set('Nothing yet');
      },
      leave: closeAll,
    };
  });
})();

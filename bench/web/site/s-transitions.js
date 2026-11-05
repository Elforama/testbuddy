// Scenario 15, "Screen transitions" (bench/SCENARIOS.md): an account list that closes over
// the form while the app is busy. From the pick on it is hidden from accessibility (the form
// under it is listed), but for 800 ms it stays drawn exactly as it was (no fade, nothing
// redrawn) and still takes clicks: a click on it picks the row under the click and starts the
// 800 ms again. Then it is removed in one step.
(() => {
  const { h } = Bench;
  const CLOSING = 800;

  Bench.scenario('transitions', 'Screen transitions', (page) => {
    let account = 'none';
    const accountLine = h('span', { id: 'tr_account' }, 'To: none');
    const summary = h('span', { id: 'tr_summary' }, 'Send nothing to none');
    const amount = h('input', { id: 'tr_amount', type: 'number', inputmode: 'numeric', placeholder: 'Amount', autocomplete: 'off' });
    const render = () => {
      accountLine.textContent = 'To: ' + account;
      summary.textContent = `Send ${amount.value || 'nothing'} to ${account}`;
    };
    amount.addEventListener('input', render);

    let closeTimer;
    const list = h('div', { id: 'tr_list', class: 'tr-list', hidden: true });
    const close = () => {
      // Out of the accessibility tree at once; nothing drawn changes until it is removed.
      list.setAttribute('aria-hidden', 'true');
      clearTimeout(closeTimer);
      closeTimer = setTimeout(() => { list.hidden = true; }, CLOSING);
    };
    const open = () => {
      clearTimeout(closeTimer);
      list.removeAttribute('aria-hidden');
      list.hidden = false;
    };
    const row = (id, name) => h('button', { id, onclick: () => { account = name; render(); close(); } }, name);
    list.append(
      row('tr_account_checking', 'Checking'),
      row('tr_account_savings', 'Savings'),
      row('tr_account_credit', 'Credit card'), // fills the rest of the list's height
    );

    page.append(
      h('button', { id: 'tr_pick', onclick: open }, 'Choose account'),
      accountLine,
      summary,
      amount,
      list,
    );
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        clearTimeout(closeTimer);
        list.hidden = true;
        list.removeAttribute('aria-hidden');
        account = 'none';
        amount.value = '';
        render();
      },
      leave() { clearTimeout(closeTimer); list.hidden = true; },
    };
  });
})();

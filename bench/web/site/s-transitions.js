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
      row('tr_account_credit', 'Credit card'),
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

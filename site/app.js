const copyStatus = document.querySelector('#copy-status');

async function copyText(button, text) {
  const original = button.textContent;
  try {
    await navigator.clipboard.writeText(text);
    button.textContent = 'Copied';
    copyStatus.textContent = 'Copied to clipboard.';
  } catch {
    button.textContent = 'Select to copy';
    copyStatus.textContent = 'Clipboard unavailable. Select the command and copy it.';
  }
  setTimeout(() => { button.textContent = original; }, 1800);
}

document.querySelectorAll('.copy').forEach(button => {
  button.addEventListener('click', () => {
    const source = button.dataset.copyFrom && document.getElementById(button.dataset.copyFrom);
    copyText(button, source ? source.textContent : button.dataset.copy);
  });
});

document.querySelectorAll('[data-tabs]').forEach(group => {
  const tabs = [...group.querySelectorAll('[role="tab"]')];

  function select(tab, focus) {
    tabs.forEach(item => {
      const active = item === tab;
      item.setAttribute('aria-selected', String(active));
      item.tabIndex = active ? 0 : -1;
      document.getElementById(item.getAttribute('aria-controls')).hidden = !active;
    });
    if (focus) tab.focus();
  }

  tabs.forEach((tab, index) => {
    tab.addEventListener('click', () => select(tab, false));
    tab.addEventListener('keydown', event => {
      const step = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
      if (!step) return;
      event.preventDefault();
      select(tabs[(index + step + tabs.length) % tabs.length], true);
    });
  });

  const platform = navigator.userAgentData?.platform || navigator.platform || '';
  const match = /win/i.test(platform) ? 'tab-windows' : /linux/i.test(platform) ? 'tab-linux' : null;
  const preferred = match && document.getElementById(match);
  if (tabs.includes(preferred)) select(preferred, false);
});

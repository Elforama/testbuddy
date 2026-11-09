const state = { platform: 'android', timing: 'Flow', data: null };
const failureReasons = {
  'Buddy Bench · native': 'App did not stop in time.',
  'Everyday · Flutter': 'App did not stop in time.',
  'Everyday · native': 'Could not hide the keyboard.'
};

function renderBenchmarks() {
  if (!state.data) return;
  const rows = state.data.results.filter(row => row.platform === state.platform);
  const body = document.querySelector('#benchmark-rows');
  body.replaceChildren();
  for (const row of rows) {
    const tr = document.createElement('tr');
    const values = [row.workload, `${row[`tb${state.timing}`].toFixed(2)} s · pass`, `${row[`other${state.timing}`].toFixed(2)} s · ${row.otherPassed ? 'pass' : 'failed'}`, row.otherPassed ? `${(row[`other${state.timing}`] / row[`tb${state.timing}`]).toFixed(1)}×` : 'Other: time to failure'];
    values.forEach((value, index) => {
      const td = document.createElement('td');
      td.textContent = value;
      if (index === 1) td.className = 'passed';
      if (index === 2 && !row.otherPassed) td.className = 'failed';
      if (index === 3 && !row.otherPassed) {
        const note = document.createElement('span');
        note.className = 'result-note';
        note.textContent = failureReasons[row.workload];
        td.append(note);
      }
      tr.append(td);
    });
    body.append(tr);
  }
}

document.querySelectorAll('[data-platform]').forEach(button => button.addEventListener('click', () => {
  state.platform = button.dataset.platform;
  document.querySelectorAll('[data-platform]').forEach(item => item.setAttribute('aria-pressed', String(item === button)));
  renderBenchmarks();
}));
document.querySelector('#timing').addEventListener('change', event => {
  state.timing = event.target.value;
  renderBenchmarks();
});
fetch('./data/comparison.json').then(response => {
  if (!response.ok) throw new Error('Benchmark data unavailable');
  return response.json();
}).then(data => { state.data = data; renderBenchmarks(); }).catch(() => {
  document.querySelector('#benchmark-rows').innerHTML = '<tr><td colspan="4">Results are available in the downloadable dataset.</td></tr>';
});

function installCommand() {
  const platform = document.querySelector('#install-platform').value;
  const base = 'https://github.com/Elforama/testbuddy/releases/latest/download/';
  if (platform.startsWith('windows')) {
    return `Invoke-WebRequest ${base}tb_${platform}.zip -OutFile tb.zip\nExpand-Archive tb.zip -DestinationPath .\n.\\tb_${platform}\\tb.exe version`;
  }
  return `curl -fsSL ${base}tb_${platform}.tar.gz | tar -xz\n./tb_${platform}/tb version`;
}
function renderInstall() { document.querySelector('#install-command').textContent = installCommand(); }
document.querySelector('#install-platform').addEventListener('change', renderInstall);
renderInstall();

async function copyText(button, value) {
  try {
    await navigator.clipboard.writeText(value);
    const original = button.textContent;
    button.textContent = 'Copied ✓';
    document.querySelector('#copy-status').textContent = 'Copied to clipboard.';
    setTimeout(() => { button.textContent = original; }, 1800);
  } catch {
    document.querySelector('#copy-status').textContent = 'Clipboard unavailable. Select and copy the displayed command.';
    button.textContent = 'Select to copy';
  }
}
document.querySelector('#copy-install').addEventListener('click', event => copyText(event.currentTarget, installCommand()));

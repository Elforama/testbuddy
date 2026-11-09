const state = { platform: 'android', timing: 'Flow', data: null };
const platformNames = { android: 'Android', ios: 'iOS', web: 'web' };
const failureReasons = {
  'Buddy Bench · native': 'App did not stop in time.',
  'Everyday · Flutter': 'App did not stop in time.',
  'Everyday · native': 'Could not hide the keyboard.'
};

function renderBenchmarks() {
  if (!state.data) return;
  const rows = state.data.results.filter(row => row.platform === state.platform);
  // Only completed journeys can be compared for speed. Keep every failure in the table.
  const featured = rows.find(row => row.tbPassed && row.otherPassed);
  const tb = featured[`tb${state.timing}`];
  const other = featured[`other${state.timing}`];
  const max = Math.ceil(Math.max(tb, other) / 10) * 10;
  document.querySelector('#chart-workload').textContent = featured.workload.toUpperCase();
  document.querySelector('#speedup').innerHTML = `${(other / tb).toFixed(1)}<span>×</span>`;
  document.querySelector('#speedup-description').textContent = `faster in this ${platformNames[state.platform]} journey`;
  document.querySelector('#chart').innerHTML = [
    ['TestBuddy', tb, ''], ['Other', other, 'other']
  ].map(([name, time, className]) => `<div class="chart-row"><div class="chart-label"><span>${name}</span><strong>${time.toFixed(2)} s</strong></div><div class="bar-track" aria-hidden="true"><div class="bar ${className}" style="width:${time / max * 100}%"></div></div></div>`).join('') + `<div class="chart-grid" aria-hidden="true"><span>0 s</span><span>${max / 2} s</span><span>${max} s</span></div>`;
  const body = document.querySelector('#benchmark-rows');
  body.replaceChildren();
  for (const row of rows) {
    const tr = document.createElement('tr');
    const values = [row.workload, `${row[`tb${state.timing}`].toFixed(2)} s · pass`, `${row[`other${state.timing}`].toFixed(2)} s · ${row.otherPassed ? 'pass' : 'failed'}`, row.otherPassed ? `${(row[`other${state.timing}`] / row[`tb${state.timing}`]).toFixed(1)}× faster` : 'Other: time to failure'];
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
  document.querySelector('#chart').textContent = 'Could not load the interactive chart. Use the Download data link below to view the recorded results.';
  document.querySelector('#benchmark-rows').innerHTML = '<tr><td colspan="4">Results are available in the downloadable dataset.</td></tr>';
});

const demos = {
  look: '❯ tb look\n\nandroid · com.example.app\n\n@1 TextField "Email" #email\n@2 TextField "Password" #password\n@3 Button "Sign in" #sign_in\n\n# Compact screen text. Ready to act.',
  act: '❯ tb do \'tapOn: @1\' \'inputText: hello@example.com\'\n\nok  tapOn: @1\nok  inputText: hello@example.com\n\n~ @1 TextField "hello@example.com" #email\n\n# Read what changed after each action.\n# Add --json for structured results.',
  preview: '❯ tb save --preview flows/sign-in.yaml --json\n\n# Inspect before writing:\n# • Recorded YAML\n# • Successful steps\n# • Warnings about fragile selectors\n\n❯ tb save flows/sign-in.yaml\n\n# Save a reusable test from the session.',
  replay: '❯ tb close -d alice\n❯ tb replay flows/sign-in.yaml -d emulator-5554 --json\n\n# One flow. One explicit device.\n# Uses the ordinary test runner.\n# Failure artifacts included.\n\n# Record launchApp as the first required step\n# and an assertion to verify the outcome.'
};
document.querySelectorAll('[data-agent]').forEach(button => button.addEventListener('click', () => {
  document.querySelectorAll('[data-agent]').forEach(item => item.setAttribute('aria-pressed', String(item === button)));
  document.querySelector('#agent-output').textContent = demos[button.dataset.agent];
}));

function installCommand() {
  const platform = document.querySelector('#install-platform').value;
  const base = 'https://github.com/Elforama/testbuddy/releases/latest/download/';
  if (platform.startsWith('windows')) {
    return `Invoke-WebRequest ${base}tb_${platform}.zip -OutFile tb.zip\nExpand-Archive tb.zip -DestinationPath .\n.\\tb_${platform}\\tb.exe version\n# Move tb.exe to a directory on your PATH`;
  }
  return `curl -fsSL ${base}tb_${platform}.tar.gz | tar -xz\n./tb_${platform}/tb version\n# Move tb to a directory on your PATH`;
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
document.querySelectorAll('[data-copy]').forEach(button => button.addEventListener('click', () => copyText(button, button.dataset.copy)));
document.querySelector('#copy-install').addEventListener('click', event => copyText(event.currentTarget, installCommand()));

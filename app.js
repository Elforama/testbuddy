const base = 'https://github.com/Elforama/testbuddy/releases/latest/download/';
const select = document.querySelector('#install-platform');
const output = document.querySelector('#install-command');
const copyButton = document.querySelector('#copy-install');
const status = document.querySelector('#copy-status');

// Default to the visitor's OS when we can tell.
const ua = navigator.userAgent;
if (/Windows/.test(ua)) select.value = 'windows_amd64';
else if (/Linux/.test(ua) && !/Android/.test(ua)) select.value = 'linux_amd64';

function installCommand() {
  const platform = select.value;
  if (platform.startsWith('windows')) {
    return `Invoke-WebRequest ${base}tb_${platform}.zip -OutFile tb.zip\nExpand-Archive tb.zip -DestinationPath .\n.\\tb_${platform}\\tb.exe version`;
  }
  return `curl -fsSL ${base}tb_${platform}.tar.gz | tar -xz\n./tb_${platform}/tb version`;
}

function render() { output.textContent = installCommand(); }
select.addEventListener('change', render);
render();

copyButton.addEventListener('click', async () => {
  const label = copyButton.querySelector('span');
  const icon = copyButton.querySelector('use');
  try {
    await navigator.clipboard.writeText(installCommand());
    label.textContent = 'Copied';
    icon.setAttribute('href', '#i-check');
    status.textContent = 'Copied to clipboard.';
  } catch {
    label.textContent = 'Select to copy';
    status.textContent = 'Clipboard unavailable. Select the command to copy it.';
  }
  setTimeout(() => { label.textContent = 'Copy'; icon.setAttribute('href', '#i-copy'); }, 1800);
});

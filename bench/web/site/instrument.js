// Click instrument for the tap-reliability check (bench/README.md). Loaded on every page, it
// does nothing unless the URL has ?instrument=1. Then it logs every pointerdown and click to
// the server (POST /log, a beacon: the page's own XHR/fetch is untouched): what it hit, and
// whether a pushed screen was still sliding in at that moment.
(function () {
  if (!/[?&]instrument\b/.test(location.search)) return;
  const run = Math.random().toString(36).slice(2, 10);
  const t0 = performance.now();
  const send = (e) => {
    try {
      navigator.sendBeacon('/log', JSON.stringify(Object.assign({ run, t: Math.round(performance.now() - t0) }, e)));
    } catch (x) {}
  };
  // What a click hit, named as the flow names it: a list row by its text, else the nearest
  // element with an id (a screen itself: "section#screen-…", a miss).
  const label = (el) => {
    for (let n = el; n && n !== document.body; n = n.parentElement) {
      if (n.classList && n.classList.contains('item')) return n.textContent.trim();
      if (n.id && n.tagName !== 'SECTION') return n.id;
      if (n.tagName === 'SECTION') return 'section#' + n.id;
    }
    return el && el.tagName ? el.tagName.toLowerCase() : '?';
  };
  // The pushed screen still sliding in, if any.
  const sliding = () => {
    for (const s of document.querySelectorAll('.screen.pushed:not([hidden])')) {
      if (s.getAnimations().some((a) => a.playState === 'running')) return s.id;
    }
    return '';
  };
  send({ type: 'load' });
  for (const type of ['pointerdown', 'click']) {
    window.addEventListener(type, (e) => {
      const shown = [...document.querySelectorAll('.screen:not([hidden])')].map((s) => s.id).join(',');
      send({ type, target: label(e.target), x: Math.round(e.clientX), y: Math.round(e.clientY), sliding: sliding(), screens: shown });
    }, true);
  }
})();

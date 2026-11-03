// Scenario 3, "Permissions" (bench/SCENARIOS.md): the browser's real permission prompts.
// Headless Chrome may deny some at once; the status shows what the browser answered.
(() => {
  const { h } = Bench;

  Bench.scenario('permissions', 'Permissions', (page) => {
    const setters = {};
    const status = (key, name) => {
      const el = h('span', { id: 'perm_' + key + '_status' }, name + ': not asked');
      setters[key] = (v) => (el.textContent = name + ': ' + v);
      return el;
    };
    const row = (key, name, label, ask) =>
      h('div', { class: 'row' }, h('button', { id: 'perm_' + key, onclick: ask }, label), status(key, name));

    const media = async (constraints) => {
      try {
        const s = await navigator.mediaDevices.getUserMedia(constraints);
        s.getTracks().forEach((t) => t.stop());
        return 'granted';
      } catch (e) {
        return 'denied'; // refused, or no such device
      }
    };
    const camera = async () => setters.camera(await media({ video: true }));
    const mic = async () => setters.mic(await media({ audio: true }));
    const location = () => {
      if (!navigator.geolocation) { setters.location('denied'); return; }
      navigator.geolocation.getCurrentPosition(
        () => setters.location('granted'),
        (e) => setters.location(e.code === 1 ? 'denied' : 'granted'), // 1: PERMISSION_DENIED; else allowed but no fix
        { timeout: 10000 });
    };
    const notifications = async () => {
      if (!('Notification' in window)) { setters.notifications('denied'); return; }
      const r = await Notification.requestPermission();
      setters.notifications(r === 'granted' ? 'granted' : 'denied');
    };
    const contacts = () => setters.contacts('denied'); // the web has no contacts API

    const NAMES = { camera: 'camera', location: 'geolocation', notifications: 'notifications', mic: 'microphone' };
    const refresh = async () => {
      for (const [key, name] of Object.entries(NAMES)) {
        try {
          const s = await navigator.permissions.query({ name });
          setters[key](s.state === 'granted' ? 'granted' : s.state === 'denied' ? 'denied' : 'not asked');
        } catch (e) { /* not queryable here: leave it */ }
      }
    };

    const dialog = h('div', { class: 'modal-back', hidden: true },
      h('div', { id: 'rationale_dialog', class: 'modal', role: 'dialog', 'aria-modal': 'true' },
        h('p', { style: 'margin:0' }, 'We need your microphone'),
        h('div', { class: 'actions' },
          h('button', { id: 'rationale_no', onclick: () => (dialog.hidden = true) }, 'Not now'),
          h('button', { id: 'rationale_yes', onclick: () => { dialog.hidden = true; mic(); } }, 'Continue'))));

    const postStatus = h('span', { id: 'perm_post_status' });
    const post = () => {
      if ('Notification' in window && Notification.permission === 'granted') {
        try { new Notification('Bench says hi'); postStatus.textContent = 'Posted'; return; } catch (e) {}
      }
      postStatus.textContent = 'Not allowed';
    };

    page.append(
      row('camera', 'Camera', 'Ask camera', camera),
      row('location', 'Location', 'Ask location', location),
      row('notifications', 'Notifications', 'Ask notifications', notifications),
      row('contacts', 'Contacts', 'Ask contacts', contacts),
      h('button', { id: 'perm_refresh', onclick: refresh }, 'Refresh'),
      h('div', { class: 'row' }, h('button', { id: 'perm_rationale', onclick: () => (dialog.hidden = false) }, 'Ask with rationale'), status('mic', 'Microphone')),
      h('div', { class: 'row' }, h('button', { id: 'perm_post', onclick: post }, 'Post a notification'), postStatus),
      dialog,
    );
    return { leave() { dialog.hidden = true; } };
  });
})();

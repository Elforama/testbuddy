// Scenario 7, "Onboarding and login" (bench/SCENARIOS.md): a pager shown once per page load,
// then sign in (validation, a 1000 ms spinner), a six-box one-time code, signed in.
(() => {
  const { h } = Bench;

  Bench.scenario('onboarding', 'Onboarding and login', (page) => {
    let seen = false; // onboarding was finished in this page load
    let email = '';

    // ---- pager ----
    const TITLES = ['Welcome', 'Track things', 'Stay in sync'];
    const BLURBS = ['Buddy Bench keeps your things in one place.', 'Add anything and watch it change.', 'Everything follows you on every device.'];
    const pager = h('div', { id: 'pager', class: 'pager', 'aria-roledescription': 'carousel' },
      TITLES.map((t, i) => h('div', { class: 'pager-page', 'aria-roledescription': 'slide' }, h('h2', { id: 'page_' + (i + 1) }, t), h('p', { class: 'muted' }, BLURBS[i]))));
    const indicator = h('span', { id: 'pager_indicator' }, 'Page 1 of 3');
    const next = h('button', { id: 'onboarding_next', onclick: () => pager.scrollTo({ left: (pageIndex() + 1) * pager.clientWidth, behavior: 'smooth' }) }, 'Next');
    const done = h('button', { id: 'onboarding_done', hidden: true, onclick: () => finish() }, 'Get started');
    const pageIndex = () => Math.max(0, Math.min(2, Math.round(pager.scrollLeft / Math.max(1, pager.clientWidth))));
    const onPage = () => {
      const i = pageIndex();
      indicator.textContent = 'Page ' + (i + 1) + ' of 3';
      next.hidden = i === 2;
      done.hidden = i !== 2;
    };
    pager.addEventListener('scroll', onPage, { passive: true });
    // A horizontal wheel/trackpad swipe turns one page once it passes 20 % of the width, as a
    // phone pager's fling does; a shorter one springs back. (Touch uses the native snap.)
    let swipe = 0, swipeTurned = false, swipeTimer;
    pager.addEventListener('wheel', (e) => {
      if (Math.abs(e.deltaX) <= Math.abs(e.deltaY)) return;
      e.preventDefault();
      clearTimeout(swipeTimer);
      swipeTimer = setTimeout(() => { swipe = 0; swipeTurned = false; }, 250);
      if (swipeTurned) return;
      swipe += e.deltaX;
      if (Math.abs(swipe) < pager.clientWidth * 0.2) return;
      swipeTurned = true;
      const to = Math.max(0, Math.min(2, pageIndex() + Math.sign(swipe)));
      pager.scrollTo({ left: to * pager.clientWidth, behavior: 'smooth' });
    }, { passive: false });
    const onboarding = h('div', { class: 'view' },
      pager,
      h('div', { class: 'row', style: 'justify-content:space-between' },
        h('button', { id: 'onboarding_skip', onclick: () => finish() }, 'Skip'), indicator, next, done));

    // ---- sign in ----
    const emailField = h('input', { id: 'signin_email', type: 'email', inputmode: 'email', placeholder: 'Email', autocomplete: 'off' });
    const pwField = h('input', { id: 'signin_password', type: 'password', placeholder: 'Password', autocomplete: 'off' });
    const remember = h('input', { id: 'signin_remember', type: 'checkbox' });
    const submit = h('button', { id: 'signin_submit', disabled: true }, 'Sign in');
    const error = h('span', { id: 'signin_error', class: 'error-text', hidden: true });
    const spinner = Bench.spinner('signin_spinner');
    spinner.hidden = true;
    const showError = (t) => { error.textContent = t; error.hidden = !t; };
    const canSubmit = () => (submit.disabled = !(emailField.value && pwField.value) || !spinner.hidden);
    emailField.addEventListener('input', canSubmit);
    pwField.addEventListener('input', canSubmit);
    let signinTimer;
    submit.addEventListener('click', () => {
      if (!emailField.value.includes('@')) { showError('Enter a valid email'); return; }
      showError('');
      spinner.hidden = false;
      canSubmit();
      signinTimer = setTimeout(() => {
        spinner.hidden = true;
        canSubmit();
        if (pwField.value === 'wrong') { showError('Wrong password'); return; }
        email = emailField.value;
        show('otp');
      }, 1000);
    });
    const signin = h('form', { class: 'view', onsubmit: (e) => { e.preventDefault(); if (!submit.disabled) submit.click(); } },
      h('h2', {}, 'Sign in'),
      emailField, pwField,
      h('label', { class: 'row' }, remember, 'Remember me'),
      submit, spinner, error);

    // ---- one-time code ----
    const boxes = [1, 2, 3, 4, 5, 6].map((i) => h('input', {
      id: 'otp_' + i, inputmode: 'numeric', autocomplete: 'one-time-code', 'aria-label': 'Digit ' + i,
    }));
    const otpError = h('span', { id: 'otp_error', class: 'error-text', hidden: true }, 'Wrong code');
    const check = () => {
      const code = boxes.map((b) => b.value).join('');
      if (code.length < 6) return;
      if (code === '123456') { signedIn.textContent = 'Signed in as ' + email; show('done'); return; }
      otpError.hidden = false;
      boxes.forEach((b) => { b.value = ''; b.dataset.v = ''; });
      boxes[0].focus();
    };
    boxes.forEach((box, i) => {
      box.addEventListener('input', (e) => {
        otpError.hidden = true;
        // One digit per box: a typed digit replaces the box's digit; a pasted (or inserted)
        // code spreads over the boxes from here.
        const typed = e.inputType === 'insertText' && e.data != null;
        const digits = (typed ? e.data : box.value).replace(/\D/g, '');
        if (typed && !digits) { box.value = box.dataset.v || ''; return; } // not a digit
        box.dataset.v = digits.slice(0, 1);
        box.value = digits.slice(0, 1);
        let k = i;
        for (const d of digits.slice(1)) { if (++k > 5) break; boxes[k].value = d; boxes[k].dataset.v = d; }
        if (digits && i < 5) boxes[Math.min(5, i + digits.length)].focus();
        check();
      });
      box.addEventListener('keydown', (e) => {
        if (e.key === 'Backspace' && !box.value && i > 0) { e.preventDefault(); boxes[i - 1].focus(); }
      });
    });
    const otp = h('div', { class: 'view' },
      h('h2', {}, 'Enter the code'),
      h('p', { class: 'muted', style: 'margin:0' }, 'We sent a 6-digit code to your email.'),
      h('div', { class: 'otp' }, boxes),
      otpError);

    // ---- signed in ----
    const signedIn = h('span', { id: 'signin_done' });
    const signedView = h('div', { class: 'view' }, signedIn,
      h('button', { id: 'signout', onclick: () => { resetSignin(); show('signin'); } }, 'Sign out'));

    const views = { onboarding, signin, otp, done: signedView };
    let view = 'onboarding';
    const show = (v) => {
      view = v;
      for (const [k, el] of Object.entries(views)) el.hidden = k !== v;
      if (v === 'otp') { boxes.forEach((b) => { b.value = ''; b.dataset.v = ''; }); otpError.hidden = true; setTimeout(() => boxes[0].focus(), 0); }
    };
    const resetSignin = () => {
      clearTimeout(signinTimer);
      emailField.value = ''; pwField.value = ''; remember.checked = false;
      spinner.hidden = true; showError(''); canSubmit();
    };
    const finish = () => { seen = true; resetSignin(); show('signin'); };

    page.append(onboarding, signin, otp, signedView);
    show('onboarding');
    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        if (!seen) { show('onboarding'); pager.scrollLeft = 0; onPage(); }
        else show(view);
      },
    };
  });
})();

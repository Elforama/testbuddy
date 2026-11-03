// Scenario 13, "Errors and retries" (bench/SCENARIOS.md).
(() => {
  const { h } = Bench;

  Bench.scenario('errors', 'Errors and retries', (page, sec) => {
    const message = h('span', { id: 'err_message', role: 'status', hidden: true });
    const illustration = h('div', { id: 'err_illustration', class: 'illustration', 'aria-hidden': 'true', hidden: true });
    const spinner = Bench.spinner('err_spinner');
    const retry = h('button', { id: 'err_retry', hidden: true }, 'Retry');
    let failedOnce = false, busyTimer;
    const say = (t) => { message.textContent = t; message.hidden = !t; illustration.hidden = true; };
    const busy = (ms, then) => {
      clearTimeout(busyTimer);
      say('');
      retry.hidden = true;
      spinner.hidden = false;
      busyTimer = setTimeout(() => { spinner.hidden = true; then(); }, ms);
    };
    const load = () => busy(800, () => {
      if (!failedOnce) { failedOnce = true; say("Couldn't load. Try again."); retry.hidden = false; return; }
      say('Loaded 3 items');
    });
    retry.addEventListener('click', load);

    // Form with inline errors.
    const email = h('input', { id: 'err_email', type: 'email', placeholder: 'Email', autocomplete: 'off' });
    const age = h('input', { id: 'err_age', type: 'number', inputmode: 'numeric', placeholder: 'Age' });
    const emailError = h('span', { id: 'err_email_error', class: 'error-text', hidden: true }, 'Invalid email');
    const ageError = h('span', { id: 'err_age_error', class: 'error-text', hidden: true }, 'Must be 18 or older');
    const formStatus = h('span', { id: 'err_form_status', hidden: true });
    const submit = () => {
      const badEmail = !/^[^@\s]+@[^@\s]+$/.test(email.value);
      const badAge = !(Number(age.value) >= 18);
      emailError.hidden = !badEmail;
      ageError.hidden = !badAge;
      email.setAttribute('aria-invalid', String(badEmail));
      age.setAttribute('aria-invalid', String(badAge));
      formStatus.textContent = badEmail || badAge ? '' : 'Form sent';
      formStatus.hidden = badEmail || badAge;
    };

    // Toast-like snackbar, 2000 ms.
    let toastTimer;
    const toast = h('div', { class: 'snackbar', role: 'status', hidden: true }, h('span', { id: 'err_toast_text' }, 'Something went wrong'));
    const showToast = () => { toast.hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => (toast.hidden = true), 2000); };

    page.append(
      h('div', { class: 'sticky-status row' }, spinner, message, illustration, retry),
      h('button', { id: 'err_load', onclick: load }, 'Load data'),
      h('button', { id: 'err_empty', onclick: () => { clearTimeout(busyTimer); spinner.hidden = true; retry.hidden = true; say('No results'); illustration.hidden = false; } }, 'Show empty'),
      h('button', { id: 'err_timeout', onclick: () => busy(6000, () => say('Request timed out')) }, 'Slow request'),
      h('button', { id: 'err_toast', onclick: showToast }, 'Fail quietly'),
      h('div', { class: 'group' },
        h('h2', {}, 'Form'),
        h('div', { class: 'field' }, email, emailError),
        h('div', { class: 'field' }, age, ageError),
        h('button', { id: 'err_form_submit', onclick: submit }, 'Submit'),
        formStatus),
    );
    sec.append(toast);

    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        clearTimeout(busyTimer);
        failedOnce = false;
        spinner.hidden = true; retry.hidden = true; say('');
        email.value = ''; age.value = '';
        emailError.hidden = true; ageError.hidden = true; formStatus.hidden = true;
        toast.hidden = true;
      },
      leave() { clearTimeout(busyTimer); clearTimeout(toastTimer); toast.hidden = true; spinner.hidden = true; },
    };
  });
})();

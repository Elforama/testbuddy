// Scenario 12, "Animations" (bench/SCENARIOS.md): things that never stop moving, things that
// arrive animated, and confetti over everything.
(() => {
  const { h } = Bench;

  Bench.scenario('animations', 'Animations', (page, sec) => {
    const status = h('span', { id: 'anim_status', class: 'sticky-status' }, 'Idle');
    const set = (t) => (status.textContent = t);
    const timers = [];

    // Spring in: from off-screen right, overshoots and settles (~900 ms).
    const sprung = h('button', { id: 'anim_sprung', hidden: true, onclick: () => set('Sprung tapped') }, 'Sprung');
    const springIn = () => { sprung.hidden = false; sprung.classList.remove('spring'); void sprung.offsetWidth; sprung.classList.add('spring'); };

    // Fade in late: in the DOM from the start at opacity 0, fades in over 1500 ms.
    const faded = h('button', { id: 'anim_faded', onclick: () => set('Faded tapped') }, 'Faded');

    // Layout shift: 600 ms later a 200 px banner grows (400 ms) above the target.
    const banner = h('div', { id: 'anim_banner', 'aria-hidden': 'true' }, 'Banner');
    const shift = () => timers.push(setTimeout(() => banner.classList.add('open'), 600));

    // Auto carousel: a slide every 1500 ms, forever, until stopped.
    const track = h('div', { class: 'carousel-track' }, [1, 2, 3, 4, 5].map((i) => h('div', { id: 'slide_' + i, class: 'slide' }, 'Slide ' + i)));
    let slide = 0, carouselTimer = 0;
    const showSlide = (i) => { slide = i; track.style.transform = `translateX(${-100 * i}%)`; };
    const startCarousel = () => { clearInterval(carouselTimer); showSlide(0); carouselTimer = setInterval(() => showSlide((slide + 1) % 5), 1500); };
    const stopCarousel = () => { clearInterval(carouselTimer); carouselTimer = 0; };

    // Confetti: 60 particles falling over 2500 ms on a canvas over the whole screen that takes
    // no taps. Deterministic (no randomness).
    const confetti = h('canvas', { id: 'confetti', class: 'confetti', hidden: true });
    let confettiRaf = 0;
    const celebrate = () => {
      cancelAnimationFrame(confettiRaf);
      const W = window.innerWidth, H = window.innerHeight, dpr = window.devicePixelRatio || 1;
      confetti.width = W * dpr; confetti.height = H * dpr;
      confetti.hidden = false;
      const g = confetti.getContext('2d');
      const COLORS = ['#f44336', '#ffb300', '#4caf50', '#2196f3', '#9c27b0'];
      const parts = Array.from({ length: 60 }, (_, i) => ({
        x: ((i * 37) % 60) / 60 * W + (i % 3) * 7,
        delay: (i % 10) * 60,
        speed: 0.8 + ((i * 7) % 5) / 10,
        spin: (i % 4) - 1.5,
        color: COLORS[i % COLORS.length],
      }));
      const t0 = performance.now();
      const frame = (now) => {
        const t = now - t0;
        g.setTransform(dpr, 0, 0, dpr, 0, 0);
        g.clearRect(0, 0, W, H);
        if (t >= 2500) { confetti.hidden = true; return; }
        for (const p of parts) {
          const k = Math.max(0, t - p.delay) / (2500 - 600);
          const y = -20 + k * (H + 40) * p.speed;
          g.save();
          g.translate(p.x + Math.sin(k * 12 + p.spin) * 18, y);
          g.rotate(k * 10 * p.spin);
          g.fillStyle = p.color;
          g.fillRect(-5, -3, 10, 6);
          g.restore();
        }
        confettiRaf = requestAnimationFrame(frame);
      };
      confettiRaf = requestAnimationFrame(frame);
    };

    page.append(
      status,
      h('div', { class: 'row' }, h('div', { id: 'anim_spinner', class: 'spinner', role: 'progressbar', 'aria-label': 'Loading' }), h('span', {}, 'Spinner')),
      h('div', { class: 'marquee' }, h('span', { id: 'anim_marquee' }, 'Breaking news: everything is fine')),
      h('button', { id: 'anim_pulse', onclick: () => set('Pulse tapped') }, 'Pulse'),
      h('button', { id: 'anim_spring_open', onclick: springIn }, 'Spring in'),
      h('div', { class: 'spring-slot' }, sprung),
      h('button', { id: 'anim_fade_open', onclick: () => faded.classList.add('shown') }, 'Fade in'),
      faded,
      h('button', { id: 'anim_shift_open', onclick: shift }, 'Shift'),
      banner,
      h('button', { id: 'anim_target', onclick: () => set('Target tapped') }, 'Target'),
      h('div', { id: 'anim_carousel', class: 'carousel', 'aria-roledescription': 'carousel' }, track),
      h('button', { id: 'anim_carousel_tap', onclick: () => { stopCarousel(); set('Stopped at Slide ' + (slide + 1)); } }, 'Stop'),
      h('div', { class: 'bar-track', id: 'anim_progress', role: 'progressbar', 'aria-label': 'Progress' }, h('div', { class: 'bar-fill' })),
      h('button', { id: 'anim_confetti', onclick: celebrate }, 'Celebrate'),
      h('button', { id: 'anim_under', onclick: () => set('Under tapped') }, 'Under confetti'),
    );
    sec.append(confetti);

    return {
      enter(params, info) {
        if (info.how === 'pop') return;
        set('Idle');
        timers.splice(0).forEach(clearTimeout);
        sprung.hidden = true; sprung.classList.remove('spring');
        faded.classList.remove('shown');
        banner.classList.remove('open');
        startCarousel();
        page.scrollTop = 0;
      },
      leave() {
        stopCarousel();
        timers.splice(0).forEach(clearTimeout);
        cancelAnimationFrame(confettiRaf);
        confetti.hidden = true;
      },
    };
  });
})();

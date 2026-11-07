const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;

function typeTerminal(pre, step = 0.09) {
  pre.innerHTML = pre.innerHTML
    .split('\n')
    .map((line, i) => `<span class="line" style="--delay:${(0.3 + i * step).toFixed(2)}s">${line || ' '}</span>`)
    .join('\n');
}

function revealOnScroll(selector) {
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      entry.target.classList.add('in');
      observer.unobserve(entry.target);
    }
  }, { threshold: 0.1 });
  document.querySelectorAll(selector).forEach((el) => {
    const index = [...el.parentElement.children].indexOf(el);
    el.style.setProperty('--delay', `${Math.min(index, 6) * 0.07}s`);
    el.classList.add('reveal');
    observer.observe(el);
  });
}

function countUp(el, duration = 1200) {
  const target = Number(el.dataset.count);
  const format = new Intl.NumberFormat(document.documentElement.lang);
  const start = performance.now();
  const tick = (now) => {
    const progress = Math.min((now - start) / duration, 1);
    el.textContent = format.format(Math.round(target * (1 - (1 - progress) ** 3)));
    if (progress < 1) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

function countWhenVisible(selector) {
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      countUp(entry.target);
      observer.unobserve(entry.target);
    }
  });
  document.querySelectorAll(selector).forEach((el) => observer.observe(el));
}

function copyButtons() {
  document.querySelectorAll('[data-copy]').forEach((button) => button.addEventListener('click', async () => {
    await navigator.clipboard.writeText(button.dataset.copy);
    const label = button.querySelector('.copy');
    button.classList.add('copied');
    label.textContent = 'copied';
    setTimeout(() => {
      button.classList.remove('copied');
      label.textContent = 'copy';
    }, 1500);
  }));
}

copyButtons();
if (!reducedMotion) {
  document.querySelectorAll('.term pre').forEach((pre) => typeTerminal(pre));
  revealOnScroll('section h2, .sub, .what .big, .stat, .cols > *, .feature > *, .case, .table-scroll, .ledger tr, .final .cta');
  countWhenVisible('[data-count]');
}

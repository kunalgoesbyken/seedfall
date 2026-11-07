const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;

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

function tabLists() {
  document.querySelectorAll('[role="tablist"]').forEach((list) => {
    const tabs = [...list.querySelectorAll('[role="tab"]')];
    const select = (chosen) => tabs.forEach((tab) => {
      const active = tab === chosen;
      tab.setAttribute('aria-selected', String(active));
      tab.tabIndex = active ? 0 : -1;
      document.getElementById(tab.getAttribute('aria-controls')).hidden = !active;
    });
    tabs.forEach((tab, index) => {
      tab.addEventListener('click', () => select(tab));
      tab.addEventListener('keydown', (event) => {
        const step = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
        if (!step) return;
        const next = tabs[(index + step + tabs.length) % tabs.length];
        select(next);
        next.focus();
      });
    });
  });
}

function stillVideos() {
  document.querySelectorAll('video[autoplay]').forEach((video) => {
    video.removeAttribute('autoplay');
    video.load();
    video.controls = true;
  });
}

copyButtons();
tabLists();
if (reducedMotion) {
  stillVideos();
} else {
  revealOnScroll('section h2, .sub, .what .big, .cols > *, .feature > *, .case, .table-scroll, .ledger tr, .final .cta');
}

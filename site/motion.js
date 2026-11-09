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
    button.classList.add('copied');
    setTimeout(() => button.classList.remove('copied'), 1500);
  }));
}

function neighbour(tabs, index, key) {
  const targets = { ArrowRight: index + 1, ArrowDown: index + 1, ArrowLeft: index - 1, ArrowUp: index - 1, Home: 0, End: tabs.length - 1 };
  const target = targets[key];
  return target === undefined ? null : tabs[(target + tabs.length) % tabs.length];
}

function tabLists() {
  document.querySelectorAll('[role="tablist"]').forEach((list) => {
    const tabs = [...list.querySelectorAll('[role="tab"]')];
    const select = (chosen) => {
      tabs.forEach((tab) => {
        const active = tab === chosen;
        tab.setAttribute('aria-selected', String(active));
        tab.tabIndex = active ? 0 : -1;
        document.getElementById(tab.getAttribute('aria-controls')).hidden = !active;
      });
      list.dispatchEvent(new CustomEvent('tabselect', { detail: chosen }));
    };
    tabs.forEach((tab, index) => {
      tab.addEventListener('click', () => select(tab));
      tab.addEventListener('keydown', (event) => {
        const next = neighbour(tabs, index, event.key);
        if (!next) return;
        event.preventDefault();
        select(next);
        next.focus();
      });
    });
  });
}

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function type(element, alive) {
  element.dataset.text ??= element.textContent;
  element.textContent = '';
  element.classList.add('typing');
  for (const char of element.dataset.text) {
    if (!alive()) return;
    element.textContent += char;
    await wait(22);
  }
  element.classList.remove('typing');
}

function resetPane(pane) {
  pane.querySelectorAll('.ln').forEach((line) => line.classList.remove('in'));
  pane.querySelectorAll('.type').forEach((element) => {
    element.textContent = element.dataset.text ?? element.textContent;
    element.classList.remove('typing');
  });
}

async function playPane(pane, alive) {
  resetPane(pane);
  for (const line of pane.querySelectorAll('.ln')) {
    if (!alive()) return;
    line.classList.add('in');
    const typed = line.querySelector('.type');
    if (typed) await type(typed, alive);
    await wait(typed ? 280 : 90);
  }
}

function stepper(root) {
  const list = root.querySelector('[role="tablist"]');
  const tabs = [...list.querySelectorAll('[role="tab"]')];
  const activePane = () => document.getElementById(list.querySelector('[aria-selected="true"]').getAttribute('aria-controls'));
  let run = 0;
  const play = () => {
    const current = ++run;
    playPane(activePane(), () => current === run);
  };
  list.addEventListener('tabselect', play);
  list.addEventListener('animationend', () => {
    const index = tabs.findIndex((tab) => tab.getAttribute('aria-selected') === 'true');
    tabs[(index + 1) % tabs.length].click();
  });
  const observer = new IntersectionObserver(([entry]) => {
    if (!entry.isIntersecting) return;
    observer.disconnect();
    root.dataset.live = '';
    play();
  }, { threshold: 0.3 });
  observer.observe(root);
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
  revealOnScroll('section h2, .sub, .what .big, .feature > *, .case, .table-scroll, .ledger tr, .final .cta');
  document.querySelectorAll('.stepper').forEach(stepper);
}

const toc = document.querySelector("[data-toc]");
const headings = [...document.querySelectorAll("article h2[id]")];

const entry = (heading) => {
  const link = Object.assign(document.createElement("a"), { href: `#${heading.id}`, textContent: heading.textContent });
  const item = document.createElement("li");
  item.append(link);
  return { heading, link, item };
};

const markCurrent = (entries) => {
  const offset = parseFloat(getComputedStyle(headings[0]).scrollMarginTop) + 1;
  const passed = entries.filter(({ heading }) => heading.getBoundingClientRect().top <= offset);
  const current = passed.at(-1) ?? entries[0];
  entries.forEach(({ link }) => link === current.link ? link.setAttribute("aria-current", "location") : link.removeAttribute("aria-current"));
};

if (toc && headings.length >= 3) {
  const entries = headings.map(entry);
  const list = document.createElement("ul");
  list.append(...entries.map(({ item }) => item));
  toc.append(list);
  toc.hidden = false;
  let pending = false;
  addEventListener("scroll", () => {
    if (pending) return;
    pending = true;
    requestAnimationFrame(() => {
      pending = false;
      markCurrent(entries);
    });
  }, { passive: true });
  markCurrent(entries);
}

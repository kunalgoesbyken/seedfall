const head = document.querySelector(".site-head");
const threshold = 8;
let last = scrollY;
let pending = false;

const update = () => {
  pending = false;
  const y = scrollY;
  if (Math.abs(y - last) < threshold) return;
  head.toggleAttribute("data-hidden", y > last && y > head.offsetHeight);
  last = y;
};

if (head) {
  addEventListener("scroll", () => {
    if (pending) return;
    pending = true;
    requestAnimationFrame(update);
  }, { passive: true });
}

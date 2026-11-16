const KEY = "theme";
const root = document.documentElement;
const system = matchMedia("(prefers-color-scheme: dark)");
const systemTheme = () => (system.matches ? "dark" : "light");

const apply = (theme) => {
  if (theme === systemTheme()) {
    delete root.dataset.theme;
    localStorage.removeItem(KEY);
    return;
  }
  root.dataset.theme = theme;
  localStorage.setItem(KEY, theme);
};

const saved = localStorage.getItem(KEY);
if (saved) root.dataset.theme = saved;

const toggle = () => apply((root.dataset.theme ?? systemTheme()) === "dark" ? "light" : "dark");

document.addEventListener("DOMContentLoaded", () => {
  document.querySelectorAll("[data-theme-toggle]").forEach((button) => button.addEventListener("click", toggle));
});

const CACHE_MS = 60 * 60 * 1000;
const format = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 });

function readCache(key) {
  try {
    return JSON.parse(localStorage.getItem(key));
  } catch {
    return null;
  }
}

function writeCache(key, count) {
  try {
    localStorage.setItem(key, JSON.stringify({ count, at: Date.now() }));
  } catch {
    return;
  }
}

async function fetchStars(repo) {
  const response = await fetch(`https://api.github.com/repos/${repo}`, { headers: { Accept: 'application/vnd.github+json' } });
  if (!response.ok) throw new Error(`GitHub answered ${response.status}`);
  const { stargazers_count: count } = await response.json();
  if (!Number.isFinite(count)) throw new Error('no star count');
  return count;
}

async function stars(repo) {
  const key = `stars:${repo}`;
  const cached = readCache(key);
  if (cached && Date.now() - cached.at < CACHE_MS) return cached.count;
  try {
    const count = await fetchStars(repo);
    writeCache(key, count);
    return count;
  } catch {
    return cached ? cached.count : null;
  }
}

async function showStars(link) {
  const repo = new URL(link.href).pathname.slice(1);
  const count = await stars(repo);
  if (!count) return;
  link.querySelector('.gh-count').textContent = format.format(count);
  link.querySelector('.gh-stat').hidden = false;
  link.setAttribute('aria-label', `Star ${repo} on GitHub, ${count} stars`);
}

document.querySelectorAll('a.gh').forEach(showStars);

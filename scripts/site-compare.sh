#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
site="$root/site"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

extract() { sed -n "$1" "$site/index.html" | sed -E 's|(<a [^>]*href=")#|\1./#|g'; }
extract '/<header class="nav">/,/<\/header>/p' > "$work/header"
extract '/<footer class="foot">/,/<\/footer>/p' > "$work/footer"
sed -n '/<link rel="preconnect"/,/<link rel="stylesheet"/p' "$site/index.html" > "$work/head-links"
grep -m1 'class="sprite"' "$site/index.html" > "$work/sprite"

node - "$site/compare.json" "$work" > "$work/table" <<'EOF'
const fs = require('fs');
const [data] = process.argv.slice(2);
const cells = JSON.parse(fs.readFileSync(data, 'utf8'));
const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const unique = (key) => [...new Set(cells.map((c) => c[key]))];
const tools = unique('tool');
const columns = unique('column');
for (const c of cells) {
  if (!/^https:\/\//.test(c.source)) throw new Error(`${c.tool} / ${c.column}: source must be an https URL`);
}
const cell = (tool, column) => {
  const c = cells.find((x) => x.tool === tool && x.column === column);
  return c ? `<td><a href="${esc(c.source)}">${esc(c.value)}</a></td>` : '<td class="na">n/a</td>';
};
const head = `<thead><tr><th></th>${tools.map((t) => `<th>${esc(t)}</th>`).join('')}</tr></thead>`;
const body = columns.map((col) => `          <tr><td>${esc(col)}</td>${tools.map((t) => cell(t, col)).join('')}</tr>`).join('\n');
process.stdout.write(`<div class="table-scroll" id="compare-table">\n      <table class="compare">\n        ${head}\n        <tbody>\n${body}\n        </tbody>\n      </table>\n    </div>\n`);
console.error(`wrote ${tools.length} tools x ${columns.length} rows into site/compare.html`);
EOF

{
cat <<EOF
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>How seedfall compares · seedfall</title>
<meta name="description" content="How seedfall compares with gosim, testing/synctest, Antithesis, Jepsen, madsim and turmoil, using what each tool's own documentation says.">
$(cat "$work/head-links")
</head>
<body>
$(cat "$work/sprite")
$(cat "$work/header")
<main class="doc">
  <div class="wrap doc-wrap wide">
    <a class="crumb" href="./">&larr; Back to seedfall</a>
    <p class="eyebrow">Compare</p>
    <h1>How seedfall compares</h1>
    <p class="prose">What each tool's own documentation says. Every cell links to its source; n/a means the source does not say.</p>
    $(cat "$work/table")
    <p class="note">Data in <a href="compare.json">compare.json</a>, sources fetched 2026-11-09. madsim and turmoil are for Rust, and Jepsen tests real clusters from Clojure.</p>
  </div>
</main>
$(cat "$work/footer")
<script src="stars.js" defer></script>
</body>
</html>
EOF
} > "$site/compare.html"
echo "wrote site/compare.html"

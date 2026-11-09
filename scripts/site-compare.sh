#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

node - "$root/site/compare.json" "$root/site/index.html" <<'EOF'
const fs = require('fs');
const [data, page] = process.argv.slice(2);
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
const table = `<div class="table-scroll" id="compare-table">\n      <table class="compare">\n        ${head}\n        <tbody>\n${body}\n        </tbody>\n      </table>\n    </div>`;
const html = fs.readFileSync(page, 'utf8');
const re = /<div class="table-scroll" id="compare-table">[\s\S]*?<\/table>\n    <\/div>/;
if (!re.test(html)) throw new Error('compare table not found in ' + page);
fs.writeFileSync(page, html.replace(re, table));
console.log(`wrote ${tools.length} tools x ${columns.length} rows into site/index.html`);
EOF

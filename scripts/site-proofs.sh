#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src="$root/docs/findings"
site="$root/site"
out="$site/proof"
marker="seedfall-proof-page"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

extract() { sed -n "$1" "$site/index.html" | sed -E 's|(<a [^>]*href=")#|\1../#|g; s|href="proof/"|href="./"|g; s|href="proof/|href="|g'; }
sprite="$(grep -m1 'class="sprite"' "$site/index.html")"
header="$(extract '/<header class="nav">/,/<\/header>/p')"
footer="$(extract '/<footer class="foot">/,/<\/footer>/p')"
head_links="$(sed -n '/<link rel="preconnect"/,/<link rel="stylesheet"/p' "$site/index.html" | sed -E 's#href="(favicon\.svg|style\.css)"#href="../\1"#')"

for md in "$src"/*.md; do
  printf '\n<!-- %s %s -->\n\n' "$marker" "$(basename "$md" .md)"
  cat "$md"
done > "$work/all.md"

npx --yes marked@18 -i "$work/all.md" \
  | sed -E 's|href="README\.md"|href="./"|g; s|href="([^":/]+)\.md"|href="\1.html"|g; s|^<table>|<div class="table-scroll"><table>|; s|^</table>$|</table></div>|' \
  | awk -v marker="$marker" -v dir="$work" '
      match($0, "^<!-- " marker " [^ ]+ -->") { file = dir "/" $3 ".html"; $0 = substr($0, RLENGTH + 1); if ($0 == "") next }
      file { print > file }'

mkdir -p "$out"
find "$out" -maxdepth 1 -name '*.html' -delete

for md in "$src"/*.md; do
  name="$(basename "$md" .md)"
  html="$work/$name.html"
  if [ "$name" = README ]; then
    page="$out/index.html"
    back='<a class="crumb" href="../#proof">&larr; Back to seedfall</a>'
  else
    page="$out/$name.html"
    back='<a class="crumb" href="./">&larr; All findings</a>'
  fi
  title_html="$(sed -n -E '0,/^<h1[^>]*>/s|^<h1[^>]*>(.*)</h1>$|\1|p' "$html" | sed -E 's|^DRAFT( ISSUE)? \(not filed\): ||')"
  title_text="$(printf '%s' "$title_html" | sed -E 's|<[^>]+>||g')"
  body="$(sed '0,/^<h1[^>]*>.*<\/h1>$/{//d}' "$html")"
  cat > "$page" <<EOF
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>$title_text · seedfall</title>
<meta name="description" content="$title_text. Found by seedfall, deterministic simulation testing for Go.">
$head_links
</head>
<body>
$sprite
$header
<main class="doc">
  <div class="wrap doc-wrap">
    $back
    <p class="eyebrow">Proof</p>
    <h1>$title_html</h1>
    <article class="prose">
$body
    </article>
  </div>
</main>
$footer
</body>
</html>
EOF
  echo "wrote ${page#"$root"/}"
done

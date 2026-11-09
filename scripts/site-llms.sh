#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
site="$root/site"
branch="main"
module="$(sed -n 's/^module //p' "$root/go.mod")"
raw="https://raw.githubusercontent.com/${module#github.com/}/$branch"
web="$(sed -n -E 's|.*<a href="([^"]+)">Website</a>.*|\1|p' "$root/README.md")"

[ -n "$web" ] || { echo "site-llms: no Website link found in README.md" >&2; exit 1; }

title() { awk '/^# / { sub(/^# /, ""); print; exit }' "$1"; }
summary() { awk 'BEGIN { RS = "" } NR == 2 && $0 !~ /^(#|```|[|<])/ { gsub(/\n/, " "); print }' "$1"; }
intro() { awk 'BEGIN { RS = "" } /^[[:alpha:]]/ { gsub(/\n/, " "); print; exit }' "$1"; }
item() {
  local line="- [$1]($2)"
  if [ -n "${3:-}" ]; then line="$line: $3"; fi
  if [ -n "${4:-}" ]; then line="$line ([Markdown]($4))"; fi
  echo "$line"
}
frontmatter() { awk -v key="$2" '/^---$/ { if (++n == 2) exit; next } n == 1 && index($0, key ": ") == 1 { print substr($0, length(key) + 3) }' "$1"; }
body() { awk 'NR == 1 && $0 == "---" { skip = 1; next } skip && $0 == "---" { skip = 0; next } !skip' "$1"; }

{
  echo "# seedfall"
  echo
  echo "> $(sed -n -E 's|.*<b>(.*)</b>.*|\1|p' "$root/README.md") $(intro "$root/README.md")"
  echo
  echo "Install with \`go install $module/cmd/seedfall@latest\`. \`seedfall init\` onboards a Go module, \`seedfall mcp\` serves every command to coding agents over MCP, and the skills below teach an agent the full loop. Every docs page is an HTML page with a raw Markdown copy beside it. The complete text of every page is in [llms-full.txt]($web/llms-full.txt)."
  echo
  echo "## Docs"
  echo
  for md in "$root"/docs/*.md; do
    name="$(basename "$md" .md)"
    [ "$name" = README ] && name=index
    item "$(title "$md")" "$web/docs/$name.html" "$(summary "$md")" "$web/docs/$name.md"
  done
  echo
  echo "## Agent skills"
  echo
  for skill in "$root"/skills/*/SKILL.md; do
    item "$(frontmatter "$skill" name)" "$raw/skills/$(basename "$(dirname "$skill")")/SKILL.md" "$(frontmatter "$skill" description)"
  done
  echo
  echo "## Optional"
  echo
  echo "- [README]($raw/README.md): bugs found, quickstart, features, examples and limitations"
  for md in "$root"/docs/findings/*.md; do
    name="$(basename "$md" .md)"
    [ "$name" = README ] && page="$web/proof/" || page="$web/proof/$name.html"
    item "$(title "$md" | sed -E 's/^DRAFT( ISSUE)? \(not filed\): //')" "$page"
  done
} > "$site/llms.txt"

{
  echo "# seedfall"
  for md in "$root"/docs/*.md; do
    printf '\n\n<!-- %s -->\n\n' "docs/$(basename "$md")"
    cat "$md"
  done
  for skill in "$root"/skills/*/SKILL.md; do
    printf '\n\n<!-- %s -->\n\n' "skills/$(basename "$(dirname "$skill")")/SKILL.md"
    body "$skill"
  done
} > "$site/llms-full.txt"

cp "$root/scripts/install.sh" "$site/install.sh"

echo "wrote ${site#"$root"/}/llms.txt ($(wc -l < "$site/llms.txt") lines) and ${site#"$root"/}/llms-full.txt ($(wc -c < "$site/llms-full.txt") bytes)"

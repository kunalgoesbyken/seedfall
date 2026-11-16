#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
site="$root/site"
page="$site/index.html"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
status=0

fail() {
  echo "site-check: $1" >&2
  status=1
}

grep -ohE 'Name: +"seedfall_[a-z_]+"' "$root"/cmd/seedfall/mcp*.go | grep -oE 'seedfall_[a-z_]+' | sort -u > "$work/code-tools"
grep -oE 'seedfall_[a-z_]+' "$root/docs/agents.md" | sort -u > "$work/doc-tools"
sed -n -E 's/^[[:space:]]*\{"([a-z ]+)", .*, cmd[A-Za-z]+\},$/\1/p' "$root/cmd/seedfall/main.go" | sort -u > "$work/code-commands"

for f in code-tools doc-tools code-commands; do
  [ -s "$work/$f" ] || { echo "site-check: found no entries for $f" >&2; exit 1; }
done

if ! diff "$work/code-tools" "$work/doc-tools" > "$work/diff"; then
  fail "MCP tools in docs/agents.md differ from cmd/seedfall (< code, > docs)"
  cat "$work/diff" >&2
fi

cat "$root"/docs/*.md "$root/README.md" > "$work/docs"
while read -r command; do
  grep -qF "seedfall $command" "$work/docs" || fail "no doc mentions the command seedfall $command"
done < "$work/code-commands"

web="$(sed -n -E 's|.*<a href="([^"]+)">Website</a>.*|\1|p' "$root/README.md")"
[ -n "$web" ] || fail "no Website link in README.md"
grep -qF "<link rel=\"canonical\" href=\"$web/\">" "$page" || fail "the canonical link in site/index.html is not $web/"

grep -oE '(href|src)="[^"]+"' "$page" | sed -E 's/^[a-z]+="(.*)"$/\1/' | grep -vE '^(https?:|mailto:|#)' | sed -E 's/[#?].*//' | sort -u | while read -r link; do
  target="$site/$link"
  [ -d "$target" ] && target="$target/index.html"
  [ -e "$target" ] || echo "site/index.html links to $link, which does not exist" >&2
done 2> "$work/dead"
if [ -s "$work/dead" ]; then
  cat "$work/dead" >&2
  status=1
fi

if [ "$status" = 0 ]; then
  echo "site-check: ok, $(wc -l < "$work/code-tools" | tr -d ' ') MCP tools and $(wc -l < "$work/code-commands" | tr -d ' ') commands are documented, and the landing page links resolve"
fi
exit "$status"

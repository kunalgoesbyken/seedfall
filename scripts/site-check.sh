#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
page="$root/site/index.html"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
status=0

grep -oE 'Name: +"seedfall_[a-z_]+"' "$root/cmd/seedfall/mcp.go" | grep -oE 'seedfall_[a-z_]+' | sort -u > "$work/code-tools"
sed -n -E 's/^[[:space:]]*\{"([a-z ]+)", .*, cmd[A-Za-z]+\},$/\1/p' "$root/cmd/seedfall/main.go" | sort -u > "$work/code-commands"
grep -oE 'seedfall_[a-z_]+' "$page" | sort -u > "$work/site-tool-mentions"
grep -E 'id="mcp-tools"' "$page" | grep -oE 'seedfall_[a-z_]+' | sort -u > "$work/site-tools"
sed -n '/id="commands"/,/<\/table>/p' "$page" | grep -oE '<code>seedfall [a-z ]+</code>' | sed -E 's|<code>seedfall (.*)</code>|\1|' | sort -u > "$work/site-commands"

for f in code-tools code-commands site-tools site-commands; do
  if [ ! -s "$work/$f" ]; then
    echo "site-check: found no entries for $f" >&2
    exit 1
  fi
done

differ() {
  if ! diff "$work/$2" "$work/$3" > "$work/diff"; then
    echo "site-check: $1 (< $2, > $3)" >&2
    cat "$work/diff" >&2
    status=1
  fi
}

differ "MCP tools listed on the site differ from cmd/seedfall/mcp.go" code-tools site-tools
differ "commands listed on the site differ from cmd/seedfall/main.go" code-commands site-commands

unknown="$(comm -13 "$work/code-tools" "$work/site-tool-mentions")"
if [ -n "$unknown" ]; then
  echo "site-check: the site names MCP tools that do not exist: $unknown" >&2
  status=1
fi

tools="$(wc -l < "$work/code-tools" | tr -d ' ')"
counts="$(grep -oE '[0-9]+ MCP tools' "$page" | grep -oE '^[0-9]+' | sort -u || true)"
if [ -z "$counts" ]; then
  echo "site-check: the site does not state how many MCP tools there are" >&2
  status=1
fi
for n in $counts; do
  if [ "$n" != "$tools" ]; then
    echo "site-check: the site says $n MCP tools, cmd/seedfall/mcp.go registers $tools" >&2
    status=1
  fi
done

if [ "$status" = 0 ]; then
  echo "site-check: ok, $tools MCP tools and $(wc -l < "$work/code-commands" | tr -d ' ') commands match cmd/seedfall"
fi
exit "$status"

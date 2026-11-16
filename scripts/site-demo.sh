#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT
out="$root/site/demo/kvlog-seed-1.html"

go build -o "$bin/seedfall" "$root/cmd/seedfall"
mkdir -p "$(dirname "$out")"
cd "$root/examples/kvlog"
"$bin/seedfall" shrink -c seedfall.always.json -seed 1 > /dev/null || true
"$bin/seedfall" replay -c seedfall.always.json -seed 1 -faults .seedfall/seed-1.faults.json -html "$out" > /dev/null || true
[ -s "$out" ] || { echo "site-demo: no timeline written" >&2; exit 1; }
echo "site-demo: wrote ${out#"$root/"}"

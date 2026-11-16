#!/usr/bin/env bash
set -e
from="$1"
to="$2"
tmp="$(mktemp -d)"
cp -r "$from" "$tmp/a"
cp -r "$to" "$tmp/b"
(cd "$tmp" && diff -ruN a b | sed -E 's/^(---|\+\+\+) ([^[:space:]]+)[[:space:]].*/\1 \2/') || true
rm -rf "$tmp"

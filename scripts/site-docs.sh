#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$root/scripts/sitedocs"
go run . "$root"

echo "site-docs: wrote $(find "$root/site/docs" -name '*.html' | wc -l | tr -d ' ') pages to site/docs"

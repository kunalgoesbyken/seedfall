#!/bin/sh
set -e
scen=$1
out=$2
here=$(cd "$(dirname "$0")/.." && pwd)
rm -rf "$out"
mkdir -p "$out" "$out/stage/rl"
"$here/build_tree.sh" "$out/stage/tree"
cp -r "$here/rlenv" "$here/train.py" "$out/stage/rl/"
find "$out/stage" -name __pycache__ -prune -exec rm -rf {} +
cp "$scen" "$out/stage/rl/scenarios.json"
tar -C "$out/stage" -czf "$out/bundle.tar.gz" tree rl
rm -rf "$out/stage"
cat > "$out/dataset-metadata.json" <<META
{"title": "seedfall-rl-src", "id": "krockxz/seedfall-rl-src", "licenses": [{"name": "other"}], "isPrivate": true}
META

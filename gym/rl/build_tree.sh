#!/bin/sh
set -e
out=$1
here=$(cd "$(dirname "$0")" && pwd)
repo=$(git -C "$here" rev-parse --show-toplevel)
rm -rf "$out"
mkdir -p "$out"
git -C "$repo" archive HEAD -- . ':!examples' ':!site' ':!gym' | tar -x -C "$out"
mkdir -p "$out/examples/etcdraft"
cp -r "$here/etcdraft/." "$out/examples/etcdraft/"

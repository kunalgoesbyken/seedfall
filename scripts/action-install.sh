#!/usr/bin/env bash
set -euo pipefail

bin="$RUNNER_TEMP/seedfall"

build_from_source() {
  [ -z "$VERSION" ] || { echo "::error title=seedfall::the version input needs a Linux or macOS runner and a released action, not a local one"; exit 1; }
  echo "::notice title=seedfall::building seedfall from source"
  (cd "$GITHUB_ACTION_PATH" && go build -trimpath -o "$bin" ./cmd/seedfall)
}

action_dir="$(dirname "$GITHUB_ACTION_PATH")"
repository="$(basename "$(dirname "$action_dir")")/$(basename "$action_dir")"
ref="$(basename "$GITHUB_ACTION_PATH")"
version="${VERSION:-$ref}"

case "$GITHUB_ACTION_PATH" in
  */_actions/*) released=true ;;
  *) released=false ;;
esac

if [ "$released" != true ] || [ "$RUNNER_OS" = Windows ]; then
  build_from_source
else
  if [ "$version" = latest ]; then
    base="https://github.com/$repository/releases/latest/download"
  else
    base="https://github.com/$repository/releases/download/$version"
  fi
  base="${SEEDFALL_BASE_URL:-$base}"
  if [ -n "$VERSION" ] || curl -fsSL -o /dev/null "$base/checksums.txt"; then
    SEEDFALL_VERSION="$version" SEEDFALL_BASE_URL="$base" SEEDFALL_INSTALL_DIR="$RUNNER_TEMP" sh "$GITHUB_ACTION_PATH/scripts/install.sh"
    echo "::notice title=seedfall::installed the $version release"
  else
    build_from_source
  fi
fi
"$bin" version

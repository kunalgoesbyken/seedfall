#!/bin/sh
set -eu

repo="kunalgoesbyken/seedfall"
version="${SEEDFALL_VERSION:-latest}"
install_dir="${SEEDFALL_INSTALL_DIR:-$HOME/.local/bin}"

die() {
  echo "seedfall install: $1" >&2
  exit 1
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "no installer for $(uname -s); on Windows use scoop (see docs/install.md), or go install github.com/$repo/cmd/seedfall@latest" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "no release for $(uname -m); use go install github.com/$repo/cmd/seedfall@latest" ;;
esac

if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi
base="${SEEDFALL_BASE_URL:-$base}"

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    die "needs curl or wget"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  else
    die "needs sha256sum or shasum to verify the download"
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

archive="seedfall_${os}_${arch}.tar.gz"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "no checksums.txt at $base; releases before the automated pipeline have none, so use go install github.com/$repo/cmd/seedfall@latest"
fetch "$base/$archive" "$tmp/$archive" || die "could not download $base/$archive"

expected="$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/checksums.txt")"
[ -n "$expected" ] || die "$archive is not listed in checksums.txt"
actual="$(sha256 "$tmp/$archive")"
[ "$expected" = "$actual" ] || die "checksum mismatch for $archive: expected $expected, got $actual"

tar -xzf "$tmp/$archive" -C "$tmp" seedfall
mkdir -p "$install_dir"
cp "$tmp/seedfall" "$install_dir/.seedfall.new"
chmod 0755 "$install_dir/.seedfall.new"
mv -f "$install_dir/.seedfall.new" "$install_dir/seedfall"

echo "installed seedfall ($version, checksum verified) to $install_dir/seedfall"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) echo "add $install_dir to your PATH" ;;
esac
echo "seedfall builds your nodes with Go, so install Go 1.25 or newer before running a sweep; seedfall doctor checks the rest"

#!/bin/sh
# Install the archyl CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/archyl-com/cli/main/install.sh | sh
#
# ARCHYL_VERSION   version to install (default: the latest release)
# INSTALL_DIR      where to put the binary (default: /usr/local/bin if writable,
#                  else ~/.local/bin)
set -eu

repo="archyl-com/cli"

fail() {
  printf 'archyl install: %s\n' "$1" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

need uname
need tar
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
  download() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
  download() { wget -qO "$2" "$1"; }
else
  fail "curl or wget is required"
fi

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW* | MSYS* | CYGWIN*) os=windows ;;
  *) fail "unsupported system $(uname -s); download a release from https://github.com/$repo/releases" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

version="${ARCHYL_VERSION:-}"
if [ -z "$version" ]; then
  version="$(fetch "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$version" ] || fail "could not find the latest release"
fi
number="${version#v}"
ext=tar.gz
binary=archyl
if [ "$os" = windows ]; then
  ext=zip
  binary=archyl.exe
fi
archive="archyl_${number}_${os}_${arch}.$ext"
base="https://github.com/$repo/releases/download/$version"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

printf 'Downloading archyl %s for %s/%s…\n' "$version" "$os" "$arch"
download "$base/$archive" "$tmp/$archive" || fail "download failed: $base/$archive"
download "$base/checksums.txt" "$tmp/checksums.txt" || fail "could not download checksums"

expected="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d ' ' -f 1)"
[ -n "$expected" ] || fail "no checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | cut -d ' ' -f 1)"
else
  actual="$(shasum -a 256 "$tmp/$archive" | cut -d ' ' -f 1)"
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive"

if [ "$ext" = zip ]; then
  need unzip
  unzip -q "$tmp/$archive" "$binary" -d "$tmp"
else
  tar -xzf "$tmp/$archive" -C "$tmp" "$binary"
fi

dir="${INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/$binary" "$dir/$binary"

printf '\n  ◆ archyl %s installed to %s\n\n' "$version" "$dir/$binary"
case ":$PATH:" in
  *":$dir:"*) printf '  Get started:  archyl login\n\n' ;;
  *) printf '  Add %s to your PATH, then:  archyl login\n\n' "$dir" ;;
esac

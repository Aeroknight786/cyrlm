#!/bin/sh
# Install the latest cyrlm release binary.
#   curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/main/install.sh | sh
# Environment: CYRLM_VERSION (default: latest), CYRLM_INSTALL_DIR (default: ~/.local/bin)
set -eu

repo="aeroknight786/cyrlm"
version="${CYRLM_VERSION:-latest}"
dir="${CYRLM_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported OS: $os (on Windows, download the .zip from https://github.com/$repo/releases)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

name="cyrlm_${os}_${arch}.tar.gz"
if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/cyrlm-install.XXXXXX")
cleanup() { [ -n "$tmp" ] && [ -d "$tmp" ] && rm -rf -- "$tmp"; }
trap cleanup EXIT

echo "downloading $name..."
curl -fsSL -o "$tmp/$name" "$base/$name"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
expected=$(grep " $name\$" "$tmp/checksums.txt" | awk '{print $1}')
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$name" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$name" | awk '{print $1}')
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "checksum mismatch for $name" >&2
  exit 1
fi

tar -C "$tmp" -xzf "$tmp/$name"
mkdir -p "$dir"
mv "$tmp/cyrlm_${os}_${arch}/cyrlm" "$dir/cyrlm"
chmod +x "$dir/cyrlm"
echo "installed $dir/cyrlm"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "add $dir to your PATH" ;;
esac
"$dir/cyrlm" version

#!/bin/sh
# Install cyrlm, then print the prompt to paste into your coding agent.
#   curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.sh | sh
# Environment: CYRLM_VERSION (default: latest), CYRLM_INSTALL_DIR (default: ~/.local/bin),
#              CYRLM_NO_PROMPT=1 to skip printing the prompt.
set -eu

repo="aeroknight786/cyrlm"
branch="ccr-db94bc6e-e47992"
version="${CYRLM_VERSION:-latest}"
dir="${CYRLM_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported OS: $os (on Windows run: irm https://raw.githubusercontent.com/$repo/$branch/install.ps1 | iex)" >&2; exit 1 ;;
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

installed=""
if curl -fsSL -o "$tmp/$name" "$base/$name" 2>/dev/null && curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" 2>/dev/null; then
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
  installed="$dir/cyrlm"
elif command -v go >/dev/null 2>&1; then
  echo "no release binary found; building with Go instead..."
  GOBIN="$dir" go install "github.com/$repo/cmd/cyrlm@$branch"
  installed="$dir/cyrlm"
else
  echo "No cyrlm release is published yet and Go is not installed." >&2
  echo "Install Go (https://go.dev/dl) and rerun, or wait for the first release." >&2
  exit 1
fi

echo "installed $installed ($("$installed" version))"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "note: add $dir to your PATH (e.g. echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile)" ;;
esac

if [ "${CYRLM_NO_PROMPT:-}" != "1" ]; then
  echo
  echo "──────── Paste everything below into your coding agent (Claude Code, Codex, OpenCode, …) ────────"
  echo
  "$installed" prompt
  echo
  echo "────────────────────────────────────────────────────────────────────────────────────────────"
fi

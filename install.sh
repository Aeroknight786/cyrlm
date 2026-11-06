#!/bin/sh
# Install triaLM, then start it.
#   curl -fsSL https://github.com/aeroknight786/trialm/releases/latest/download/install.sh | sh
# Environment: TRIALM_VERSION (default: latest), TRIALM_INSTALL_DIR (default: ~/.local/bin),
#              TRIALM_NO_START=1 to install without starting trialm (for coding agents and scripts).
# The CYRLM_* names of these variables still work for one release.
set -eu

repo="aeroknight786/trialm"
version="${TRIALM_VERSION:-${CYRLM_VERSION:-latest}}"
dir="${TRIALM_INSTALL_DIR:-${CYRLM_INSTALL_DIR:-$HOME/.local/bin}}"
no_start="${TRIALM_NO_START:-${CYRLM_NO_PROMPT:-}}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported OS: $os (on Windows run: irm https://github.com/$repo/releases/latest/download/install.ps1 | iex)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

name="trialm_${os}_${arch}.tar.gz"
if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/trialm-install.XXXXXX")
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
  mv "$tmp/trialm_${os}_${arch}/trialm" "$dir/trialm"
  chmod +x "$dir/trialm"
  installed="$dir/trialm"
elif command -v go >/dev/null 2>&1; then
  # Go 1.21+ downloads the toolchain go.mod asks for by itself.
  gover=$(go env GOVERSION 2>/dev/null || echo go1.0)
  minor=$(echo "$gover" | sed -E 's/^go1\.([0-9]+).*/\1/')
  case "$minor" in ''|*[!0-9]*) minor=0 ;; esac
  if [ "$minor" -lt 21 ]; then
    echo "No trialm release binary was found, and your Go ($gover) is too old to build it." >&2
    echo "Install Go 1.21 or newer (https://go.dev/dl) and rerun; it fetches the rest by itself." >&2
    exit 1
  fi
  echo "no release binary found; building with Go instead..."
  mkdir -p "$dir"
  GOTOOLCHAIN=auto GOBIN="$dir" go install "github.com/$repo/cmd/trialm@latest"
  installed="$dir/trialm"
else
  echo "No trialm release binary was found for $os/$arch and Go is not installed." >&2
  echo "Install Go 1.21 or newer (https://go.dev/dl) and rerun." >&2
  exit 1
fi

echo "installed $installed ($("$installed" version))"

# cyrlm is now triaLM: an old cyrlm binary here becomes a shim, so old
# commands and setups keep working for one release.
if [ -e "$dir/cyrlm" ] || [ -L "$dir/cyrlm" ]; then
  rm -f "$dir/cyrlm"
  printf '#!/bin/sh\n# cyrlm is now triaLM; this shim goes away in v1.0.\nexec "%s" "$@"\n' "$installed" > "$dir/cyrlm"
  chmod +x "$dir/cyrlm"
  echo "replaced the old cyrlm in $dir with a shim that runs trialm"
fi

on_path=1
case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    on_path=0
    rc="$HOME/.profile"
    case "${SHELL:-}" in
      */zsh) rc="$HOME/.zprofile" ;;
      */bash) [ "$os" = darwin ] && rc="$HOME/.bash_profile" ;;
    esac
    echo "note: $dir is not on your PATH. Add it with:"
    echo "  echo 'export PATH=\"$dir:\$PATH\"' >> $rc && export PATH=\"$dir:\$PATH\""
    ;;
esac

# Start trialm on the terminal (stdin is the piped script, so read from
# /dev/tty). Agents and scripts set TRIALM_NO_START=1; without a terminal
# there is nothing to start.
if [ "$no_start" != "1" ] && [ -t 1 ] && (: </dev/tty) 2>/dev/null; then
  echo
  exec "$installed" </dev/tty
fi
if [ "$on_path" = 1 ]; then
  echo "run: trialm"
else
  echo "run: $installed"
fi

#!/bin/sh
# cyrlm is now triaLM. This branch's installer forwards to the new one:
#   curl -fsSL https://github.com/aeroknight786/trialm/releases/latest/download/install.sh | sh
# It installs `trialm`, turns an old `cyrlm` into a shim, and keeps your key and setups.
set -eu
echo "cyrlm is now triaLM: installing trialm from https://github.com/aeroknight786/trialm"
tmp=$(mktemp "${TMPDIR:-/tmp}/trialm-install.XXXXXX")
trap 'rm -f "$tmp"' EXIT
curl -fsSL -o "$tmp" https://github.com/aeroknight786/trialm/releases/latest/download/install.sh
sh "$tmp"

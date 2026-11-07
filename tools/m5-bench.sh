#!/bin/sh
# Owner-run real-model validation from PLAN §10 P0b. Never run automatically.
set -eu
bench=${LLAMA_BENCH:-llama-bench}
if ! command -v "$bench" >/dev/null 2>&1; then
  if [ -x /opt/homebrew/bin/llama-bench ]; then bench=/opt/homebrew/bin/llama-bench
  else echo 'llama-bench is missing. Install llama.cpp first.' >&2; exit 1; fi
fi
base=${TRIALM_HOME:-"$HOME/.trialm"}
models=${TRIALM_BENCH_MODELS:-"$base/cache/m5-bench"}
fixtures=${TRIALM_BENCH_OUTPUT:-"$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/internal/calib/testdata/apple-m5"}
qwen=Qwen3.6-35B-A3B-UD-IQ3_XXS.gguf
oss=gpt-oss-20b-MXFP4.gguf
# Pinned repository revisions; curl resumes into .part, never a final filename.
qwen_url=https://huggingface.co/unsloth/Qwen3.6-35B-A3B-GGUF/resolve/a483e9e6cbd595906af30beda3187c2663a1118c/$qwen
oss_url=https://huggingface.co/ggml-org/gpt-oss-20b-GGUF/resolve/15dcfc7e7c608c6161507211ed43c5b590801b4b/$oss
if [ ! -f "$models/$qwen" ] || [ ! -f "$models/$oss" ]; then
  printf 'This downloads about 25 GB of real model weights into %s. Continue? [y/N] ' "$models" >&2
  # Explicit approval is required even in scripts or CI. No environment bypass.
  if ! read -r answer </dev/tty; then echo 'A terminal is required to approve downloads.' >&2; exit 3; fi
  case "$answer" in y|Y|yes|YES) ;; *) echo 'Skipped.' >&2; exit 0 ;; esac
  mkdir -p "$models"
  download() {
    name=$1; url=$2
    if [ ! -f "$models/$name" ]; then
      curl --fail --location --retry 3 --continue-at - --output "$models/$name.part" "$url"
      mv "$models/$name.part" "$models/$name"
    fi
  }
  download "$qwen" "$qwen_url"
  download "$oss" "$oss_url"
fi
mkdir -p "$fixtures"
"$bench" --version >"$fixtures/build.txt" 2>&1
for name in "$qwen" "$oss"; do
  stem=${name%.gguf}
  echo "Benchmarking $stem (about 15 min for both models)." >&2
  "$bench" -m "$models/$name" -p 512,4096 -n 128 -d 0,8192,32768 -r 2 -ngl 99 -fa on -o json >"$fixtures/$stem.json.part" 2>"$fixtures/$stem.log"
  mv "$fixtures/$stem.json.part" "$fixtures/$stem.json"
  "$bench" -m "$models/$name" -p 1,2,3,4,8 -n 0 -d 0,8192,32768 -r 2 -ngl 99 -fa on -o json >"$fixtures/$stem-verify.json.part" 2>"$fixtures/$stem-verify.log"
  mv "$fixtures/$stem-verify.json.part" "$fixtures/$stem-verify.json"
done
printf 'Apple M5 fixtures saved in %s\n' "$fixtures"

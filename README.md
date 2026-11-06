# cyrlm — try a local model before you download it

`cyrlm` tells you whether an open-weight model will run on **your** machine, how fast it will feel **inside your coding agent**, and then lets you actually use it that way — before you download 5–100 GB and set up a runtime.

It runs a small local server that looks like Ollama (and also speaks the OpenAI and Anthropic APIs). Your harness — Claude Code, Codex, OpenCode, Cline, Aider, Continue, Zed — connects to it unchanged. Each request is answered by the **real model running in the cloud**, but the answer is released at the pace **your device** would produce it: model loading, prompt processing, generation speed, context limits and prompt-cache behaviour of the runtime you pick.

```
$ cyrlm predict gpt-oss:20b --device "Apple M4 Pro 20c" --ram 48

gpt-oss 20B on what-if: Apple M4 Pro (20-core GPU), 48 GB unified memory
Scenario: Claude Code — 33k-token system prompt + tools, then ~2.5k new tokens and ~300 output tokens per turn

RUNTIME                                  FILE               CONTEXT  MEMORY        FIRST REPLY  FOLLOW-UP  WRITES AT  TASK*
Ollama (defaults)                        MXFP4 12.1 GB      32k ⚠    ✓ 12.6/36 GB  1 min 38 s   17 s       50 tok/s   ✗ breaks
Ollama (tuned)                           MXFP4 12.1 GB      88k      ✓ 13.9/36 GB  1 min 38 s   20 s       47 tok/s   18 min 11 s
llama.cpp (llama-server) (defaults)      MXFP4 12.1 GB      128k     ✓ 14.9/36 GB  1 min 35 s   20 s       48 tok/s   20 min 21 s
LM Studio (llama.cpp engine) (defaults)  MXFP4 12.1 GB      4k ⚠     ✓ 12.5/36 GB  1 min 36 s   17 s       51 tok/s   ✗ breaks
LM Studio (MLX engine) (tuned)           MLX-MXFP4 12.1 GB  88k      ✓ 13.9/36 GB  1 min 33 s   19 s       52 tok/s   17 min 01 s
...
* TASK = a 40-call agent task; at datacenter speed it would take about 2 min 00 s.

  ⚠ Ollama (defaults): Claude Code's ~33k-token prompt does not fit the 32768-token context: the prompt is silently truncated
```

## Why

Most "can I run it" tools stop at *fits in memory* and a single tokens-per-second number. For coding agents that number is misleading: every request carries a 7–33k-token system prompt, so what you feel is **prompt processing and prompt-cache reuse**, not generation speed. A 32k-token first turn takes ~6 s on an RTX 4090 and ~60–80 s on an M3 Max/M4 Pro; whether later turns take 0.3 s or a minute depends on the runtime's cache, its default context window, and quirks of your harness. `cyrlm` simulates those, explains them, and tells you which settings fix them.

## Install

Single static binary for macOS, Linux and Windows (Go, no dependencies).

```sh
# from source (Go 1.24+)
go install github.com/aeroknight786/cyrlm/cmd/cyrlm@latest

# or a release binary (once releases are published)
curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.sh | sh   # prints the prompt to paste
```

## Let your coding agent do everything

Paste this into Claude Code, Codex, OpenCode or any coding agent:

```text
Help me find, try and set up the best local AI model for coding on this computer, using cyrlm (https://github.com/aeroknight786/cyrlm).

1. If `cyrlm version` fails, install it — ask me first:
   macOS/Linux: curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.sh | CYRLM_NO_PROMPT=1 sh
   Windows (PowerShell): $env:CYRLM_NO_PROMPT=1; irm https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.ps1 | iex
2. Run `cyrlm prompt --harness <the coding tool you are running in>` and follow those instructions exactly.
```

The agent installs cyrlm, shows the best options for your machine, starts a live trial (you open one extra terminal for it), and — when you say go — downloads, configures and verifies the setup, asking before every change. The only thing you type yourself is your key: `cyrlm login --openrouter` (it never goes through the chat). Running the install command yourself prints the same instructions to paste.

## Quick start

```sh
cyrlm detect                                   # your GPU/RAM and installed runtimes
cyrlm models                                   # catalog (or: cyrlm inspect <any .gguf URL>)
cyrlm predict qwen3-coder:30b                  # every runtime, defaults vs tuned
cyrlm predict gpt-oss:120b --device "RTX 4090" --ram 64   # what-if another machine

export OPENROUTER_API_KEY=...                  # the real model runs on OpenRouter
cyrlm try gpt-oss:20b --harness claude-code    # predict + serve + exact connect command
cyrlm try gpt-oss:20b --harness codex --launch # starts the harness for you
cyrlm serve qwen3-coder:30b --upstream mock    # offline timing demo, no account needed
```

While you work, each request prints one line, and Ctrl-C prints a session summary:

```
#1   anthropic   prompt 18.0k · first token 86 s · 169 tok @ 34 tok/s · 1 min 35 s (cloud 991 ms)
     ↳ load 6.2 s: first request: model loads from disk
#2   anthropic   prompt 18.2k (cached 18.2k, new 6) · first token 300 ms · 170 tok @ 34 tok/s · 5.3 s
#3   anthropic   prompt 18.2k (cached 22, new 18.2k) · first token 81 s · 170 tok @ 34 tok/s · 86 s
     ↳ cache miss at token 22 of a 18233-token cached prompt: the system prompt or tool definitions
       changed (a per-request value such as a timestamp or attribution header breaks the cache)
       was:    same⏎You are Claude Code. Follow the repository conven…
       now:    changed⏎You are Claude Code. Follow the repository…
```

## Models and runtimes

- **24 catalog models** (`cyrlm models`): Qwen3.8 27B, Qwen3.6 35B-A3B, Qwen3.5 4B/9B, Qwen3-Coder 30B, Gemma 4 12B/26B-A4B, gpt-oss 20B/120B, GLM-4.7-Flash, Devstral Small 2, Nemotron 3.5 Lightning, Granite 4.2, Llama 3.x and more. Newer entries are generated from the models' own GGUF headers (`tools/catalog-gen`).
- **Any other model**: pass a GGUF file or Hugging Face URL to `predict`/`serve`/`inspect`; only the header is read.
- **Runtimes**: `detect` finds Ollama, LM Studio, llama.cpp, mlx-lm, vLLM, Jan, Lemonade and Docker Model Runner. `predict` compares Ollama, llama.cpp, LM Studio (llama.cpp and MLX engines) and mlx-lm with default and tuned settings, marks the ones you have installed (●), and recommends the best setup — preferring what you already have when it is within 5% of the best. Jan, Lemonade and Docker Model Runner are simulated with the llama.cpp profile; vLLM is detected but not simulated yet.

## What is simulated

| Simulated | How |
|---|---|
| Model load, keep-alive unloads, reloads on context change | runtime profile (e.g. Ollama unloads after 5 idle minutes) |
| Prompt processing time | compute model by backend, as a function of context depth |
| Generation speed | memory-bandwidth model by backend, slowing with context depth |
| Prompt-cache reuse | per-runtime slots, longest-common-prefix reuse, sliding-window checkpoint limits, RAM cache; explains every big miss |
| Context window | runtime default (Ollama's VRAM tiers, LM Studio's 4k) or tuned; prompts are **truncated or rejected exactly as the runtime would**, and the cloud model sees the truncated prompt |
| Concurrency | one request at a time; background/subagent requests queue and can evict the cache |
| Hidden reasoning | reasoning tokens cost decode time even when the provider doesn't stream them |
| Token counts | exact counts from the provider when it finishes before the simulated first token (almost always), else a calibrated estimate |

Labelled, not simulated: quantization quality (the cloud usually serves BF16/FP8; gpt-oss is MXFP4 everywhere so it matches), local tool-call parser bugs, swapping/thermal throttling, other apps' memory use. When the cloud is slower than your simulated device, that moment runs at cloud speed and the log says so.

## How accurate is it?

The predictor is a roofline model (bytes read per token ÷ bandwidth for generation; FLOPs ÷ throughput for prompt processing) with efficiencies fitted per backend on 565 public llama.cpp results, evaluated **leave-one-device-out**. Typical error (about 3 in 4 predictions):

| Machine | Generation | Prompt processing | Notes |
|---|---|---|---|
| Apple M1–M4 | ±15% | ±15% | best-covered segment |
| NVIDIA desktop | ±20% | ±35% | prompt speed varies with llama.cpp version |
| AMD desktop GPU | ±20% | ±50%, measure for ROCm | ROCm vs Vulkan matters |
| NVIDIA laptop, iGPU, Intel Arc | ±35–40% | ±45–70% | power limits / memory config: calibrate |
| Strix Halo | ±20% | measure only | driver settings move prefill 2–3× |
| CPU-only | ±50% (±25% with `--probe`) | ±50% | |
| Model spilling to system RAM | no claim | no claim | shown with `?` |

Long contexts (≥16k) widen these by 10–15 points. Details, data and scripts: [docs/RESEARCH.md](docs/RESEARCH.md), [research/](research/).

## Harnesses

| Harness | Protocol cyrlm serves | Connect |
|---|---|---|
| Claude Code | Anthropic Messages | env vars (printed by `cyrlm try`) |
| Codex CLI | OpenAI Responses | `~/.codex/config.toml` profile, or `codex --oss` on port 11434 |
| OpenCode, Qwen Code, Cline | OpenAI Chat Completions | config snippet / settings |
| Aider, Continue, Zed, JetBrains | Ollama native | point at the Ollama URL |

Cursor sends requests from its own servers, so it needs a public tunnel to reach a local endpoint.

## Accounts and sharing (optional)

- `cyrlm login` stores the key from your cyrlm account page (owner-only file). With it, trials run on cyrlm's hosted models instead of your own OpenRouter key.
- `cyrlm verify` always saves its result locally. Sharing is opt-in: `cyrlm share --last` shows the exact payload (timings, token counts, hardware class — never prompts, paths or hostnames) and asks first; `cyrlm config set share always|ask|never` sets the default.
- Server contract: [docs/CLOUD_API.md](docs/CLOUD_API.md).

## Privacy and cost

During a trial, what your harness sends to the model — prompts, file contents it read, command output — goes to the upstream provider (OpenRouter by default; any OpenAI-compatible URL works). Files and tool execution stay local. You pay the provider with your own key: a 45-minute Claude Code session is roughly $0.15–0.40 on gpt-oss-20b and $0.65–1.35 on Qwen3-Coder-30B before cache discounts.

## Status

v0.1, under active development. Hardware detection is fixture-tested on macOS/Windows and live-tested on Linux; runtime-profile settings marked low-confidence are printed as assumptions. Next: on-device calibration probe (llama-bench), install hand-off of the exact tested configuration, opt-in predicted-vs-actual data, and a self-hosted "fidelity mode" serving the exact quantized file. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and [docs/RUNTIME_PROFILES.md](docs/RUNTIME_PROFILES.md).

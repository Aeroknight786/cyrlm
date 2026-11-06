# How cyrlm works

The details behind the [README](../README.md).

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

Long contexts (≥16k) widen these by 10–15 points. Details, data and scripts: [RESEARCH.md](RESEARCH.md), [research/](../research/).

## Harnesses

| Harness | Protocol cyrlm serves | Connect |
|---|---|---|
| Claude Code | Anthropic Messages | env vars (printed by `cyrlm try`) |
| Codex CLI | OpenAI Responses | one `codex -c …` command (no config file edits) |
| OpenCode, Qwen Code, Cline | OpenAI Chat Completions | config snippet / settings |
| Aider, Continue, Zed | Ollama native or OpenAI-compatible | point at the Ollama URL |

Cursor sends requests from its own servers, so it needs a public tunnel to reach a local endpoint.

### After setup: `cyrlm bridge`

Coding tools and local runtimes disagree in small ways that break real sessions even when the runtime is fast. Examples seen on real machines:
- Codex sends namespaced tools, which llama.cpp skips.
- Codex sends several developer messages, which strict chat templates (Qwen, Gemma, Mistral) reject with "System message must be at the beginning".
- When the runtime answers HTTP 500, Codex shows it as "high demand" and retries.

`cyrlm bridge` puts the same protocol layer the trial uses in front of the runtime's plain Chat Completions API, its most mature one. It:
- merges system messages for the chat template;
- flattens namespaced tools and maps their calls back;
- reports hosted tools that a local model cannot run;
- returns deterministic runtime errors as errors the tool shows instead of retrying.

Plans use it for Codex, and for any tool whose API the runtime lacks (for example Claude Code on mlx-lm). `cyrlm verify --harness <tool>` then sends a greeting, a tool call and a tool result in that tool's own request format, and reports **Ready** only when all three work.

Servers cyrlm starts (`try`, `bridge`, and runtimes via `cyrlm bg`) detach properly and return only once they answer, so they survive the agent's command runner.

## Models and runtimes

- **24 catalog models** (`cyrlm models`): Qwen3.8 27B, Qwen3.6 35B-A3B, Qwen3.5 4B/9B, Qwen3-Coder 30B, Gemma 4 12B/26B-A4B, gpt-oss 20B/120B, GLM-4.7-Flash, Devstral Small 2, Nemotron 3.5 Lightning, Granite 4.2, Llama 3.x and more. Newer entries are generated from the models' own GGUF headers (`tools/catalog-gen`).
- **Files**: each model lists several quantizations (4-bit and 8-bit, plus 3-bit and IQ4 files for the large mixture-of-experts models). `recommend` tries every file and shows the one that runs best on your machine. That is the usual 4-bit file when it is fast enough, otherwise a smaller file that lets a stronger model fit (with a quality caveat below 4 bits).
- **Any other model**: pass a GGUF file or Hugging Face URL to `predict`/`serve`/`inspect`; only the header is read.
- **Runtimes**: `detect` finds Ollama, LM Studio, llama.cpp, mlx-lm, vLLM, Jan, Lemonade and Docker Model Runner. `predict` compares Ollama, llama.cpp, LM Studio (llama.cpp and MLX engines) and mlx-lm with default and tuned settings, marks the ones you have installed (●), and recommends the best setup — preferring what you already have when it is within 5% of the best. Jan, Lemonade and Docker Model Runner are simulated with the llama.cpp profile; vLLM is detected but not simulated yet.

## Commands

`cyrlm help` shows the customer commands; `cyrlm help --all` lists everything, including `predict` (full per-runtime table), `serve` (the trial server without harness setup), `inspect` (read any GGUF header), `models`, `runtimes` and what-if flags such as `--device "RTX 4090" --ram 64`.

## Sharing results

`cyrlm verify` saves its result on your machine. Uploading results to improve predictions needs the cyrlm account server, which is not available yet; the protocol is in [CLOUD_API.md](CLOUD_API.md).

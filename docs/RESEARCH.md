# Research summary (2026-11)

The decisions behind cyrlm. Raw data and scripts are in [`research/`](../research/).

## 1. Can local performance be predicted without running the model?

Dataset: 565 measurements (278 generation, 287 prompt processing) on 91 device variants from 13 public sources (llama.cpp performance discussions for Apple/CUDA/Vulkan/ROCm/SYCL, gpt-oss long-prompt and Strix Halo depth sweeps, MLX comparisons). Each row has its source URL.

Model: generation time per token = bytes read (active weights + KV cache at depth) ÷ (efficiency × spec bandwidth) + per-token overhead; prompt processing = matmul FLOPs ÷ (eff × spec FLOPS) + attention FLOPs at depth ÷ (eff_attn × spec FLOPS). Efficiencies are fitted per backend; errors below are **leave-one-device-out** (median abs error / share within ±25%).

| Segment | Generation | Prompt processing |
|---|---|---|
| Apple M1–M4 base/Pro | 4% / 100% | 6% / 100% |
| Apple M1–M4 Max/Ultra | 10% / 80% | 6% / 93% |
| NVIDIA desktop | 11% / 91% | 18% / 69% |
| AMD discrete | 11% / 95% | 15% / 68% |
| Strix Halo | 8% / 81% | 176% / 4% |
| Intel Arc | 41% / 44% | 239% / 22% |
| CPU-only | 137% / 0% (9% / 100% calibrated) | 30% / 0% |
| All | 10% / 79% | 14% / 68% |

Findings:
- Pure spec rooflines over-predict by ~1.5×; fitted efficiencies are required (CUDA 0.74 + 0.9 ms/token, Metal 0.85 + 4.3 ms/token, ROCm 0.64, Vulkan 0.75).
- On independent crowd data errors are ~3× larger than on curated scoreboards (software versions, settings, mislabelled runs): claims must be scoped by segment and shown as ranges.
- Not predictable from specs: partial/expert offload to CPU (2–7×), ROCm and Strix Halo/Arc prompt processing (one env var moves Strix Halo pp512 349 → 883 tok/s), long-context prefill on some backends, new architectures.
- Many-small-expert MoE (Qwen3-30B-A3B) runs at ~0.7 of the bandwidth model on two independent devices; cyrlm applies a per-model factor.
- A ≤60 s on-device probe (bandwidth microbenchmark + a 2–5B model run) cuts generation error to ~5–8% median where it applies.

## 2. llmfit: reuse or rebuild?

llmfit (MIT, ~75k lines of Rust, v1.1.20, very active) has excellent hardware detection and a large model catalog, but for this product:
- its speed estimate has no context-depth term and no prompt-processing estimate by default (TTFT is null unless TFLOPS is configured), which are what decide the agentic experience;
- reimplemented on the same dataset its generation error is ~26% median vs ~10% for a backend-fitted model; bandwidth table errors (e.g. RTX 5060, RX 9070), laptops/Arc/CPU fall back to constants;
- Mac fit is graded against total RAM, not the ~2/3–3/4 Metal working-set limit;
- no OpenAI-compatible or paced endpoint; patch releases change the public API.

Decision: **rebuild** the predictor, simulator and server; **learn from** llmfit's detection edge cases (Windows 64-bit VRAM from the registry, APU carve-outs, laptop guards). No llmfit code is vendored.

## 3. Competitors

"Can I run it" tools are commoditized (llmfit, whichllm, llm-checker, CanIRun.ai, HF hardware compatibility, LM Studio/Jan fit badges, calculators). Paced endpoints exist only with synthetic text (llm-d-inference-sim, LLM_Sim_Bench), and "same model in the cloud" exists only at datacenter speed (Ollama `-cloud`, OpenRouter). Nothing relays real model output paced to the user's device inside their own harness, and nothing models prompt-cache behaviour per runtime. The biggest threat is Ollama adding a "pace to my device" mode.

## 4. Harness protocols

Chat Completions + Anthropic Messages + Responses cover >90% of harnesses; serving Ollama's native API as well makes every Ollama-oriented guide work. Base prompts: Claude Code ~33k tokens (24k tool schemas), Qwen Code ~25k, Cline ~12k, Codex ~9k, OpenCode ~7k, Aider ~4k + repo map.

## 5. Prompt caching dominates agentic latency

- llama-server: `cache_prompt` on, 4 slots, similarity-based slot choice, RAM prompt cache; sliding-window and hybrid models can only roll back to checkpoints.
- Ollama: one slot (background requests evict the conversation), 5-minute keep-alive, small default context on <24 GiB GPUs.
- LM Studio: 4k default context; MLX engine rolls back to the common prefix.
- Harness quirks: Claude Code's per-request attribution header changes the system prompt and defeats caching (`CLAUDE_CODE_ATTRIBUTION_HEADER=0`).

## 6. Rejected: cloud prefill with KV transfer / mid-response hand-off

Shipping the KV cache to the device is faster only where local prefill is slow and the KV cache is compact (Macs, APUs, CPUs with sliding-window/hybrid models): break-even link speed = local prefill tok/s × KV bytes/token (≈85 Mbps for gpt-oss-20b on an M4 Pro, ≈1 Gbps on an RTX 4090). But the cloud then sees the whole prompt (no privacy gain), plain cloud inference is faster and costs about the same (98% of agentic tokens are input), a session moves 4–8 GB, and it needs an exact-quant GPU fleet. Handing decode back to the device mid-response only ever slows the reply because cloud decode is faster than any consumer device. The same goal — a fast first turn — is better met locally by prewarming the harness's static prompt and persisting the prompt cache to disk. Models: [`research/hybrid/`](../research/hybrid/).

## 7. Quality fidelity

Cloud BF16/FP8 vs local 4-bit: ~0–3 points on coding benchmarks for 20–35B models; 4-bit raises invented tool names under long agent sessions; Q5_K_M is safer for 7–12B tool use. gpt-oss ships MXFP4, so cloud and local weights match. Local tool-call parser bugs cannot be reproduced through a cloud API; a self-hosted "fidelity mode" serving the exact GGUF is the remedy.
